package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/devpg"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/grant"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/store"
)

const (
	internalToken = "internal-test-token"
	testAsset     = "0x036CbD53842c5426634e7929541eC2318f3dCF7e" // USDC on base-sepolia
	testNetwork   = "base-sepolia"
	testScheme    = "exact"
	payTo         = "0x209693Bc6afc0C5328bA36FaF03C514EF312287C"
	endpoint      = "https://api.example-data.test/v1"
)

var (
	testDSN    string
	dsnOnce    sync.Once
	testSigner *grant.Signer
)

func TestMain(m *testing.M) {
	code := func() int {
		if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
			testDSN = dsn
		} else {
			inst, err := devpg.Start()
			if err != nil {
				fmt.Fprintln(os.Stderr, "embedded postgres:", err)
				return 1
			}
			defer inst.Stop()
			testDSN = inst.DSN
		}
		var err error
		testSigner, err = grant.Generate()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return m.Run()
	}()
	os.Exit(code)
}

// env is a running API over a shared database. Each test creates its own
// organizations, so tests are isolated by tenant rather than by schema.
type env struct {
	t     *testing.T
	st    *store.Store
	srv   *httptest.Server
	clock *fakeClock
}

type fakeClock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().UTC().Add(c.offset)
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset += d
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(context.Background(), testDSN)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	clock := &fakeClock{}
	st.Now = clock.now
	api, err := New(st, testSigner, Config{InternalToken: internalToken, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(func() { srv.Close(); st.Close() })
	return &env{t: t, st: st, srv: srv, clock: clock}
}

type resp struct {
	status int
	body   []byte
}

func (r resp) into(v any) {
	if err := json.Unmarshal(r.body, v); err != nil {
		panic(fmt.Sprintf("decode %s: %v", r.body, err))
	}
}

func (r resp) errCode() string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.body, &e)
	return e.Error.Code
}

func (e *env) call(method, path, token string, body any) resp {
	e.t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, buf)
	if err != nil {
		e.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, body: b}
}

func (e *env) must(r resp, want int) resp {
	e.t.Helper()
	if r.status != want {
		e.t.Fatalf("status %d, want %d: %s", r.status, want, r.body)
	}
	return r
}

// tenant is a fully provisioned organization with one agent and policy.
type tenant struct {
	orgID    string
	ownerKey string
	agentID  string
	agentKey string
	policyID string
}

func defaultPolicy() PolicyRequest {
	return PolicyRequest{
		Asset:           domain.AssetSpec{Asset: testAsset, Network: testNetwork, Decimals: 6, Scheme: testScheme},
		PerRequestMax:   amt("1000000"), // 1 USDC
		PerTaskMax:      amt("5000000"), // 5 USDC
		AgentDailyMax:   amt("10000000"),
		OrgDailyMax:     amt("20000000"),
		FeeAllowance:    amt("0"),
		Endpoints:       []domain.EndpointRule{{URLPrefix: endpoint, Methods: []string{"GET", "POST"}, Recipients: []string{payTo}}},
		MaxTaskSeconds:  3600,
		GrantTTLSeconds: 60,
	}
}

func (e *env) provision(name string, deposit string, pol PolicyRequest) tenant {
	e.t.Helper()
	var org struct {
		Organization domain.Organization `json:"organization"`
		OwnerAPIKey  string              `json:"owner_api_key"`
	}
	e.must(e.call("POST", "/v1/organizations", "", map[string]string{"name": name}), 201).into(&org)
	tn := tenant{orgID: org.Organization.ID, ownerKey: org.OwnerAPIKey}
	var ag struct {
		Agent       domain.AgentPrincipal `json:"agent"`
		AgentAPIKey string                `json:"agent_api_key"`
	}
	e.must(e.call("POST", "/v1/agents", tn.ownerKey, map[string]string{"name": "research-agent"}), 201).into(&ag)
	tn.agentID, tn.agentKey = ag.Agent.ID, ag.AgentAPIKey
	var p domain.BudgetPolicyVersion
	e.must(e.call("PUT", "/v1/agents/"+tn.agentID+"/policy", tn.ownerKey, pol), 201).into(&p)
	tn.policyID = p.ID
	if deposit != "" {
		e.must(e.call("POST", "/v1/funding/deposits", tn.ownerKey, map[string]any{"asset": testAsset, "network": testNetwork, "amount": deposit, "reference": "test"}), 200)
	}
	return tn
}

func (e *env) createTask(tn tenant, budget string) domain.Task {
	e.t.Helper()
	var task domain.Task
	e.must(e.call("POST", "/v1/tasks", tn.agentKey, map[string]any{"description": "t", "budget": budget}), 201).into(&task)
	return task
}

func quote(amount string) domain.ResourceQuote {
	q := domain.ResourceQuote{Scheme: testScheme, Network: testNetwork, Asset: testAsset, PayTo: payTo,
		MaxAmountRequired: amt(amount), Resource: endpoint + "/weather", MaxTimeoutSeconds: 60}
	q.RequirementsHash = grant.RequirementsHash(q)
	return q
}

// begin + authorize as the gateway would. Returns the raw responses.
func (e *env) begin(tn tenant, taskID, idem, url string) resp {
	return e.call("POST", "/internal/v1/paid-requests/begin", internalToken, BeginRequest{AgentKey: tn.agentKey, TaskID: taskID, IdempotencyKey: idem, Method: "GET", URL: url})
}

func (e *env) authorize(attemptID string, q domain.ResourceQuote) resp {
	return e.call("POST", "/internal/v1/paid-requests/"+attemptID+"/authorize", internalToken, AuthorizeRequest{Quote: q})
}

func (e *env) submitted(attemptID, grantID string) resp {
	return e.call("POST", "/internal/v1/paid-requests/"+attemptID+"/submitted", internalToken, SubmittedRequest{GrantID: grantID, PaymentHash: "hash-" + attemptID})
}

func (e *env) outcome(attemptID string, status domain.PaymentStatus, settled string, service *domain.ServiceOutcome) resp {
	var req OutcomeRequest
	req.Payment.Status = status
	req.Payment.SubmissionRef = "0xtx-" + attemptID
	req.Payment.SettledAmount = amt(settled)
	req.Service = service
	return e.call("POST", "/internal/v1/paid-requests/"+attemptID+"/outcome", internalToken, req)
}

// pay runs the full gateway sequence and returns the authorize result.
func (e *env) pay(tn tenant, taskID, idem, amount string) (BeginResponse, AuthorizeResponse, resp) {
	e.t.Helper()
	var br BeginResponse
	r := e.begin(tn, taskID, idem, endpoint+"/weather")
	if r.status != 201 {
		return br, AuthorizeResponse{}, r
	}
	r.into(&br)
	ar := e.authorize(br.Attempt.ID, quote(amount))
	var a AuthorizeResponse
	if ar.status == 201 {
		ar.into(&a)
	}
	return br, a, ar
}

func (e *env) balance(tn tenant) domain.AccountBalance {
	e.t.Helper()
	b, err := e.st.GetBalance(context.Background(), tn.orgID, testAsset, testNetwork)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.LedgerInvariant(context.Background(), tn.orgID, testAsset, testNetwork); err != nil {
		e.t.Fatalf("ledger invariant: %v", err)
	}
	return b
}
