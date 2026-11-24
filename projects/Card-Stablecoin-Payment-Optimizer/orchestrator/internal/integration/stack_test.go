package integration

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
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/api"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/app"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/engine"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/ids"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/quotes"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/service"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/store"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/testutil"
)

var (
	engineURL string
	dsn       string
	db        *store.Store
	recorded  *quotes.Recorded
)

func TestMain(m *testing.M) {
	t := &fatalLogger{}
	engineURL = testutil.StartEngine(t)
	dsn = testutil.StartPostgres(t)
	var err error
	if db, err = store.Open(context.Background(), dsn); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if recorded, err = quotes.NewRecorded(filepath.Join(testutil.RepoRoot(), "fixtures", "recorded", "v1")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	db.Close()
	t.cleanup()
	os.Exit(code)
}

// fatalLogger adapts testing.TB-style helpers for TestMain.
type fatalLogger struct {
	testing.TB
	cleanups []func()
}

func (f *fatalLogger) Helper()                         {}
func (f *fatalLogger) Cleanup(fn func())               { f.cleanups = append(f.cleanups, fn) }
func (f *fatalLogger) Logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func (f *fatalLogger) Fatal(args ...any)               { fmt.Fprintln(os.Stderr, args...); f.cleanup(); os.Exit(1) }
func (f *fatalLogger) Fatalf(format string, args ...any) {
	f.Logf(format, args...)
	f.cleanup()
	os.Exit(1)
}
func (f *fatalLogger) cleanup() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
	f.cleanups = nil
}

// clock is a settable time source shared by a stack.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// stack is one tenant's API over the shared engine and database.
type stack struct {
	t      *testing.T
	srv    *httptest.Server
	svc    *service.Service
	tenant string
	key    string
	clock  *clock
}

func newStack(t *testing.T, mode string) *stack {
	t.Helper()
	ctx := context.Background()
	policy, err := app.LoadPolicy(filepath.Join(testutil.RepoRoot(), "config", "policy.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{now: time.Now().UTC().Truncate(time.Second)}
	svc, err := service.New(service.Options{
		Store:    db,
		Engine:   engine.NewClient(engineURL, testutil.EngineToken),
		Quotes:   quotes.NewCached(recorded, 30*time.Second),
		Balances: recorded,
		Policy:   policy,
		Mode:     mode,
		Now:      clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	tenant := ids.New("ten")
	if err := db.EnsureTenant(ctx, tenant, "Test tenant", clk.Now()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(svc, db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))).Handler())
	t.Cleanup(srv.Close)
	s := &stack{t: t, srv: srv, svc: svc, tenant: tenant, clock: clk}
	s.key = s.newKey(api.AllScopes...)
	return s
}

func (s *stack) newKey(scopes ...string) string {
	secret := ids.NewAPIKey()
	if _, err := db.CreateAPIKey(context.Background(), s.tenant, "test", scopes, secret, s.clock.Now()); err != nil {
		s.t.Fatal(err)
	}
	return secret
}

type response struct {
	status  int
	headers http.Header
	body    map[string]any
}

func (r response) errorCode() string {
	if e, ok := r.body["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

func (r response) str(path ...string) string {
	v := lookup(r.body, path...)
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func lookup(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[p]
	}
	return cur
}

func (s *stack) do(key, method, path string, body any, headers map[string]string) response {
	s.t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, s.srv.URL+path, reader)
	if err != nil {
		s.t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := response{status: resp.StatusCode, headers: resp.Header}
	data, _ := io.ReadAll(resp.Body)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out.body); err != nil {
			s.t.Fatalf("%s %s: non-JSON body %q", method, path, data)
		}
	}
	return out
}

func (s *stack) call(method, path string, body any, headers map[string]string) response {
	s.t.Helper()
	return s.do(s.key, method, path, body, headers)
}

func (s *stack) expect(r response, status int, code string) response {
	s.t.Helper()
	if r.status != status || (code != "" && r.errorCode() != code) {
		s.t.Fatalf("got %d %s, want %d %s (body %v)", r.status, r.errorCode(), status, code, r.body)
	}
	return r
}

// prefs stores preferences consenting to both rails with the card as default.
func (s *stack) prefs(customer string, overrides map[string]any) {
	s.t.Helper()
	body := map[string]any{
		"default_option_id":           "card",
		"min_savings_threshold_minor": "100",
		"consented_rails":             []string{"card", "stablecoin"},
		"consent_funding_step":        true,
		"allowed_rails":               nil,
		"require_purchase_protection": false,
		"avoid_credit":                false,
		"liquidity_reserves":          []any{},
		"point_valuations": []map[string]any{{
			"program_id": "pts_demo", "valuation_version": "val-1", "micro_usd_per_point": "10000", "stability": "stable",
		}},
	}
	for k, v := range overrides {
		body[k] = v
	}
	s.expect(s.call("PUT", "/v1/customers/"+customer+"/preferences", body, nil), 200, "")
}

func (s *stack) evaluate(customer, basket string, headers map[string]string) response {
	s.t.Helper()
	merchant := "mer_demo_outfitter"
	if basket == "pantry-restock" || basket == "coffee-box" {
		merchant = "mer_demo_pantry"
	}
	return s.call("POST", "/v1/evaluations", map[string]any{
		"customer_id": customer, "merchant_id": merchant, "basket_ref": basket,
	}, headers)
}

// amountDue reads the engine's amount due for an option out of an evaluation.
func amountDue(r response, optionID string) map[string]any {
	for _, o := range lookup(r.body, "result", "options").([]any) {
		opt := o.(map[string]any)
		if opt["option_id"] == optionID {
			return lookup(opt, "breakdown", "amount_due").(map[string]any)
		}
	}
	return nil
}

func (s *stack) approve(evaluation response, optionID string) map[string]any {
	return map[string]any{
		"option_id": optionID,
		"approval": map[string]any{
			"customer_id":     evaluation.str("customer_id"),
			"approved_at":     s.clock.Now().Format(time.RFC3339),
			"approved_amount": amountDue(evaluation, optionID),
			"reference":       ids.New("apr"),
		},
	}
}
