package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/grant"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/money"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/policy"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/workers"
)

func amt(s string) money.Amount { return money.MustParse(s) }

func TestHappyPathSettlesAndWritesReceipt(t *testing.T) {
	e := newEnv(t)
	tn := e.provision("acme", "10000000", defaultPolicy())
	task := e.createTask(tn, "3000000")

	br, ar, r := e.pay(tn, task.ID, "idem-1", "250000")
	e.must(r, 201)
	if ar.AmountMax.String() != "250000" {
		t.Fatalf("amount max %s", ar.AmountMax)
	}
	// Grant is a valid EdDSA JWT bound to the quote.
	claims, err := grant.Verify(testSignerPub(), ar.GrantToken)
	if err != nil {
		t.Fatalf("verify grant: %v", err)
	}
	if claims.PayTo != payTo || claims.AmountMax != "250000" || claims.TaskID != task.ID || claims.RequirementsSHA != quote("250000").RequirementsHash {
		t.Fatalf("grant claims not bound correctly: %+v", claims)
	}
	b := e.balance(tn)
	if b.Available.String() != "9750000" || b.Reserved.String() != "250000" {
		t.Fatalf("after reserve: %+v", b)
	}

	e.must(e.submitted(br.Attempt.ID, ar.GrantID), 200)
	var out OutcomeResponse
	e.must(e.outcome(br.Attempt.ID, domain.PaymentSettled, "250000", &domain.ServiceOutcome{HTTPStatus: 200, Delivered: true, BodyBytes: 42}), 200).into(&out)
	if out.Attempt.Status != domain.PaymentSettled || out.Receipt == nil || !out.Receipt.Service.Delivered {
		t.Fatalf("outcome: %+v", out)
	}
	b = e.balance(tn)
	if b.Available.String() != "9750000" || b.Reserved.String() != "0" {
		t.Fatalf("after settle: %+v", b)
	}

	// Agent can read its receipts and budget; budget reflects committed spend.
	var budget TaskBudgetResponse
	e.must(e.call("GET", "/v1/tasks/"+task.ID+"/budget", tn.agentKey, nil), 200).into(&budget)
	if budget.Committed.String() != "250000" || budget.Remaining.String() != "2750000" || !budget.Spendable {
		t.Fatalf("budget: %+v", budget)
	}
	var rc struct {
		Receipts []domain.Receipt `json:"receipts"`
	}
	e.must(e.call("GET", "/v1/tasks/"+task.ID+"/receipts", tn.agentKey, nil), 200).into(&rc)
	if len(rc.Receipts) != 1 || rc.Receipts[0].PolicyVersionID != tn.policyID || rc.Receipts[0].SubmissionRef == "" {
		t.Fatalf("receipts: %+v", rc.Receipts)
	}
}

func testSignerPub() []byte {
	b, _ := grant.ParseSeed(testSigner.PublicKeyBase64())
	return b
}

func TestPartialSettlementReturnsUnusedHold(t *testing.T) {
	e := newEnv(t)
	pol := defaultPolicy()
	pol.FeeAllowance = amt("1000")
	tn := e.provision("acme", "1000000", pol)
	task := e.createTask(tn, "1000000")
	br, ar, r := e.pay(tn, task.ID, "i", "500000")
	e.must(r, 201)
	if b := e.balance(tn); b.Reserved.String() != "501000" {
		t.Fatalf("hold should include fee allowance: %+v", b)
	}
	e.must(e.submitted(br.Attempt.ID, ar.GrantID), 200)
	var req OutcomeRequest
	req.Payment.Status = domain.PaymentSettled
	req.Payment.SettledAmount = amt("400000")
	req.Payment.FeeActual = amt("250")
	e.must(e.call("POST", "/internal/v1/paid-requests/"+br.Attempt.ID+"/outcome", internalToken, req), 200)
	if b := e.balance(tn); b.Available.String() != "599750" || b.Reserved.String() != "0" {
		t.Fatalf("after partial settle: %+v", b)
	}
	// Settling above the reservation is refused.
	br2, ar2, _ := e.pay(tn, task.ID, "i2", "100000")
	e.must(e.submitted(br2.Attempt.ID, ar2.GrantID), 200)
	e.must(e.outcome(br2.Attempt.ID, domain.PaymentSettled, "100001", nil), 409)
}

// Concurrent requests must never spend the same available balance twice.
func TestConcurrentOverspendIsImpossible(t *testing.T) {
	e := newEnv(t)
	pol := defaultPolicy()
	pol.PerTaskMax = amt("5000000")
	tn := e.provision("acme", "5000000", pol) // balance 5 USDC
	task := e.createTask(tn, "3000000")       // task cap 3 USDC
	const workers = 40
	const each = "400000" // 7 fit in 3,000,000 (2,800,000)

	var authorized, denied int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, r := e.pay(tn, task.ID, fmt.Sprintf("c-%d", i), each)
			switch r.status {
			case 201:
				atomic.AddInt64(&authorized, 1)
			case 403:
				if r.errCode() != string(policy.CodeTaskBudget) {
					t.Errorf("unexpected denial %s: %s", r.errCode(), r.body)
				}
				atomic.AddInt64(&denied, 1)
			default:
				t.Errorf("unexpected status %d: %s", r.status, r.body)
			}
		}(i)
	}
	wg.Wait()
	if authorized != 7 || denied != workers-7 {
		t.Fatalf("authorized=%d denied=%d", authorized, denied)
	}
	b := e.balance(tn)
	if b.Reserved.String() != "2800000" || b.Available.String() != "2200000" {
		t.Fatalf("balance %+v", b)
	}
}

func TestConcurrentOverspendAcrossTasksRespectsAgentDaily(t *testing.T) {
	e := newEnv(t)
	pol := defaultPolicy()
	pol.PerTaskMax = amt("1000000")
	pol.AgentDailyMax = amt("1000000") // only 2 x 500000 per day across all tasks
	pol.OrgDailyMax = amt("50000000")
	tn := e.provision("acme", "50000000", pol)
	var tasks []domain.Task
	for i := 0; i < 5; i++ {
		tasks = append(tasks, e.createTask(tn, "5000000"))
	}
	var ok int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, r := e.pay(tn, tasks[i%5].ID, fmt.Sprintf("k-%d", i), "500000")
			if r.status == 201 {
				atomic.AddInt64(&ok, 1)
			} else if r.errCode() != string(policy.CodeAgentDaily) && r.status != 200 {
				t.Errorf("unexpected: %d %s", r.status, r.body)
			}
		}(i)
	}
	wg.Wait()
	if ok != 2 {
		t.Fatalf("authorized %d, want 2", ok)
	}
	e.balance(tn)
}

func TestIdempotentReplayNeverPaysTwice(t *testing.T) {
	e := newEnv(t)
	tn := e.provision("acme", "10000000", defaultPolicy())
	task := e.createTask(tn, "3000000")
	br, ar, r := e.pay(tn, task.ID, "same-key", "100000")
	e.must(r, 201)
	e.must(e.submitted(br.Attempt.ID, ar.GrantID), 200)
	e.must(e.outcome(br.Attempt.ID, domain.PaymentSettled, "100000", nil), 200)

	var replay BeginResponse
	e.must(e.begin(tn, task.ID, "same-key", endpoint+"/weather"), 200).into(&replay)
	if !replay.Replay || replay.Attempt.ID != br.Attempt.ID || replay.Receipt == nil || replay.Attempt.Status != domain.PaymentSettled {
		t.Fatalf("replay: %+v", replay)
	}
	if b := e.balance(tn); b.Reserved.String() != "0" || b.Available.String() != "9900000" {
		t.Fatalf("balance %+v", b)
	}
	// Authorizing the replayed (already settled) attempt is a conflict, not a new hold.
	e.must(e.authorize(br.Attempt.ID, quote("100000")), 409)
}

func TestCrossTenantIsolation(t *testing.T) {
	e := newEnv(t)
	a := e.provision("org-a", "10000000", defaultPolicy())
	b := e.provision("org-b", "10000000", defaultPolicy())
	taskA := e.createTask(a, "1000000")

	// Agent B cannot see or spend task A.
	e.must(e.call("GET", "/v1/tasks/"+taskA.ID+"/budget", b.agentKey, nil), 404)
	e.must(e.begin(b, taskA.ID, "x", endpoint+"/weather"), 404)
	// Owner B cannot see agent A, or A's tasks/attempts.
	e.must(e.call("GET", "/v1/agents/"+a.agentID, b.ownerKey, nil), 404)
	e.must(e.call("PUT", "/v1/agents/"+a.agentID+"/policy", b.ownerKey, defaultPolicy()), 404)
	var tasks struct {
		Tasks []domain.Task `json:"tasks"`
	}
	e.must(e.call("GET", "/v1/tasks", b.ownerKey, nil), 200).into(&tasks)
	if len(tasks.Tasks) != 0 {
		t.Fatalf("owner B sees %d tasks of org A", len(tasks.Tasks))
	}
	// Spending by A does not touch B's balance.
	_, _, r := e.pay(a, taskA.ID, "p", "500000")
	e.must(r, 201)
	if bb := e.balance(b); bb.Reserved.String() != "0" || bb.Available.String() != "10000000" {
		t.Fatalf("org B balance changed: %+v", bb)
	}
}

func TestChallengeValidationRejectsWrongBeneficiaryAssetNetworkScheme(t *testing.T) {
	e := newEnv(t)
	tn := e.provision("acme", "10000000", defaultPolicy())
	task := e.createTask(tn, "3000000")
	cases := map[string]func(q *domain.ResourceQuote){
		string(policy.CodeRecipientNotApproved): func(q *domain.ResourceQuote) { q.PayTo = "0x000000000000000000000000000000000000dEaD" },
		string(policy.CodeWrongAsset):           func(q *domain.ResourceQuote) { q.Asset = "0x1111111111111111111111111111111111111111" },
		string(policy.CodeWrongNetwork):         func(q *domain.ResourceQuote) { q.Network = "base" },
		string(policy.CodeUnsupportedScheme):    func(q *domain.ResourceQuote) { q.Scheme = "upto" },
		string(policy.CodePerRequestLimit):      func(q *domain.ResourceQuote) { q.MaxAmountRequired = amt("1000001") },
		string(policy.CodeResourceMismatch):     func(q *domain.ResourceQuote) { q.Resource = "https://evil.test/x" },
	}
	i := 0
	for want, mutate := range cases {
		i++
		var br BeginResponse
		e.must(e.begin(tn, task.ID, "m-"+want, endpoint+"/weather"), 201).into(&br)
		q := quote("100000")
		mutate(&q)
		q.RequirementsHash = grant.RequirementsHash(q)
		r := e.authorize(br.Attempt.ID, q)
		if r.status != 403 || r.errCode() != want {
			t.Fatalf("%s: got %d %s", want, r.status, r.body)
		}
		var a domain.PaymentAttempt
		e.must(e.call("GET", "/v1/payment-attempts/"+br.Attempt.ID, tn.agentKey, nil), 200).into(&a)
		if a.Status != domain.PaymentRejected || a.RejectReason != want {
			t.Fatalf("attempt should be rejected with %s: %+v", want, a)
		}
	}
	if b := e.balance(tn); !b.Reserved.IsZero() {
		t.Fatalf("rejected quotes must hold nothing: %+v", b)
	}
	// Endpoint outside the allowlist and SSRF-ish URLs are rejected at begin.
	for _, u := range []string{"https://evil.test/weather", "https://api.example-data.test/v2/x", "https://api.example-data.test/v1x", "https://user:pw@api.example-data.test/v1/w", "http://127.0.0.1/v1", "ftp://api.example-data.test/v1"} {
		r := e.begin(tn, task.ID, "u-"+u, u)
		if r.status != 403 {
			t.Fatalf("%s: got %d %s", u, r.status, r.body)
		}
	}
	// Hash mismatch between gateway and control plane is a hard error.
	var br BeginResponse
	e.must(e.begin(tn, task.ID, "hash", endpoint+"/weather"), 201).into(&br)
	q := quote("100000")
	q.RequirementsHash = "deadbeef"
	e.must(e.authorize(br.Attempt.ID, q), 400)
}

func TestGrantReplayAndExpiry(t *testing.T) {
	e := newEnv(t)
	pol := defaultPolicy()
	pol.GrantTTLSeconds = 5
	tn := e.provision("acme", "10000000", pol)
	task := e.createTask(tn, "3000000")

	// Replay: a consumed grant cannot be submitted again.
	br, ar, r := e.pay(tn, task.ID, "r1", "100000")
	e.must(r, 201)
	e.must(e.submitted(br.Attempt.ID, ar.GrantID), 200)
	e.must(e.submitted(br.Attempt.ID, ar.GrantID), 409)
	// Wrong grant id for the attempt is forbidden.
	br2, _, _ := e.pay(tn, task.ID, "r2", "100000")
	e.must(e.submitted(br2.Attempt.ID, ar.GrantID), 403)

	// Expiry: advance past TTL; submission is refused and the worker releases.
	e.clock.advance(6 * time.Second)
	br3, _, _ := e.pay(tn, task.ID, "r3", "100000") // fresh grant issued at new time
	e.clock.advance(6 * time.Second)
	e.must(e.submitted(br3.Attempt.ID, ar.GrantID), 403)
	if b := e.balance(tn); b.Reserved.String() != "300000" {
		t.Fatalf("expected three holds, got %+v", b)
	}
	runner := &workers.Runner{Store: e.st, Cfg: workers.Defaults(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	runner.Cfg.ExpiryMargin = time.Second
	runner.Tick(context.Background())
	// br (submitted) must NOT be released by expiry recovery; br2 and br3 (never submitted) must be.
	var a domain.PaymentAttempt
	e.must(e.call("GET", "/v1/payment-attempts/"+br.Attempt.ID, tn.agentKey, nil), 200).into(&a)
	if a.Status != domain.PaymentSubmitted {
		t.Fatalf("submitted attempt was altered by recovery: %s", a.Status)
	}
	e.must(e.call("GET", "/v1/payment-attempts/"+br2.Attempt.ID, tn.agentKey, nil), 200).into(&a)
	if a.Status != domain.PaymentExpired {
		t.Fatalf("unsubmitted expired attempt should be expired: %s", a.Status)
	}
	if b := e.balance(tn); b.Reserved.String() != "100000" {
		t.Fatalf("only the submitted hold should remain: %+v", b)
	}
}

type fakeVerifier struct{ res workers.VerifyResult }

func (f fakeVerifier) Lookup(context.Context, domain.PaymentAttempt) (workers.VerifyResult, error) {
	return f.res, nil
}

func TestUnknownOutcomeKeepsHoldUntilReconciled(t *testing.T) {
	e := newEnv(t)
	tn := e.provision("acme", "10000000", defaultPolicy())
	task := e.createTask(tn, "3000000")

	// Attempt 1: gateway reports unknown (timeout after submission).
	br1, ar1, _ := e.pay(tn, task.ID, "u1", "100000")
	e.must(e.submitted(br1.Attempt.ID, ar1.GrantID), 200)
	e.must(e.outcome(br1.Attempt.ID, domain.PaymentUnknown, "0", nil), 200)
	// Attempt 2: gateway wrote the submission record and crashed.
	br2, ar2, _ := e.pay(tn, task.ID, "u2", "100000")
	e.must(e.submitted(br2.Attempt.ID, ar2.GrantID), 200)
	// Unknown without submission is refused (nothing could have been paid).
	br3, _, _ := e.pay(tn, task.ID, "u3", "100000")
	e.must(e.outcome(br3.Attempt.ID, domain.PaymentUnknown, "0", nil), 409)
	e.must(e.outcome(br3.Attempt.ID, domain.PaymentSettled, "100000", nil), 409)
	e.must(e.outcome(br3.Attempt.ID, domain.PaymentFailed, "0", nil), 200)

	if b := e.balance(tn); b.Reserved.String() != "200000" {
		t.Fatalf("unknown outcomes must keep holds: %+v", b)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Provider has no record yet and the release window has not passed: nothing changes.
	runner := &workers.Runner{Store: e.st, Cfg: workers.Defaults(), Log: log, Verifier: fakeVerifier{workers.VerifyResult{Status: workers.VerifyNotFound}}}
	runner.Cfg.SubmittedGrace = 0
	e.clock.advance(time.Second)
	runner.Tick(context.Background())
	if b := e.balance(tn); b.Reserved.String() != "200000" {
		t.Fatalf("holds released without evidence: %+v", b)
	}
	var a domain.PaymentAttempt
	e.must(e.call("GET", "/v1/payment-attempts/"+br2.Attempt.ID, tn.agentKey, nil), 200).into(&a)
	if a.Status != domain.PaymentUnknown {
		t.Fatalf("stale submitted attempt should be promoted to unknown: %s", a.Status)
	}
	// Provider confirms settlement: commit.
	runner.Verifier = fakeVerifier{workers.VerifyResult{Status: workers.VerifySettled, SubmissionRef: "0xabc", SettledAmount: amt("100000")}}
	runner.Tick(context.Background())
	b := e.balance(tn)
	if b.Reserved.String() != "0" || b.Available.String() != "9800000" {
		t.Fatalf("after reconciliation: %+v", b)
	}
	e.must(e.call("GET", "/v1/payment-attempts/"+br1.Attempt.ID, tn.agentKey, nil), 200).into(&a)
	if a.Status != domain.PaymentSettled || a.SubmissionRef != "0xabc" {
		t.Fatalf("reconciled attempt: %+v", a)
	}
}

func TestOwnerRevocationAndPauseDuringExecution(t *testing.T) {
	e := newEnv(t)
	tn := e.provision("acme", "10000000", defaultPolicy())
	task := e.createTask(tn, "3000000")
	br, ar, _ := e.pay(tn, task.ID, "v1", "100000")
	e.must(e.submitted(br.Attempt.ID, ar.GrantID), 200)

	// Owner revokes the agent while the payment is in flight.
	e.must(e.call("POST", "/v1/agents/"+tn.agentID+"/revoke", tn.ownerKey, nil), 200)
	// New spending is impossible (credential rejected).
	if r := e.begin(tn, task.ID, "v2", endpoint+"/weather"); r.status != 401 {
		t.Fatalf("revoked agent should be rejected: %d %s", r.status, r.body)
	}
	e.must(e.call("POST", "/v1/tasks", tn.agentKey, map[string]any{"description": "x"}), 401)
	// The in-flight payment is still tracked to resolution.
	e.must(e.outcome(br.Attempt.ID, domain.PaymentSettled, "100000", nil), 200)
	if b := e.balance(tn); b.Reserved.String() != "0" || b.Available.String() != "9900000" {
		t.Fatalf("balance %+v", b)
	}

	// Pause blocks a second, still-active agent.
	tn2 := e.provision("acme-2", "10000000", defaultPolicy())
	task2 := e.createTask(tn2, "3000000")
	e.must(e.call("POST", "/v1/organization/pause", tn2.ownerKey, nil), 200)
	if r := e.begin(tn2, task2.ID, "p1", endpoint+"/weather"); r.status != 403 || r.errCode() != string(policy.CodeOrgPaused) {
		t.Fatalf("paused org: %d %s", r.status, r.body)
	}
	e.must(e.call("POST", "/v1/organization/resume", tn2.ownerKey, nil), 200)
	_, _, r := e.pay(tn2, task2.ID, "p2", "100000")
	e.must(r, 201)
}

func TestInsufficientBalanceIncludingFees(t *testing.T) {
	e := newEnv(t)
	pol := defaultPolicy()
	pol.FeeAllowance = amt("5000")
	tn := e.provision("acme", "100000", pol) // exactly the quoted amount, no room for fee
	task := e.createTask(tn, "3000000")
	_, _, r := e.pay(tn, task.ID, "f1", "100000")
	if r.status != 403 || r.errCode() != string(policy.CodeInsufficientBalance) {
		t.Fatalf("expected insufficient_balance: %d %s", r.status, r.body)
	}
	_, _, r = e.pay(tn, task.ID, "f2", "95000")
	e.must(r, 201)
	if b := e.balance(tn); b.Available.String() != "0" || b.Reserved.String() != "100000" {
		t.Fatalf("balance %+v", b)
	}
	// Reserved funds cannot be withdrawn.
	e.must(e.call("POST", "/v1/funding/withdrawals", tn.ownerKey, map[string]any{"asset": testAsset, "network": testNetwork, "amount": "1", "reference": "w"}), 409)
}

func TestPolicyChangeClosesOldTasksAndAgentsCannotWidenLimits(t *testing.T) {
	e := newEnv(t)
	tn := e.provision("acme", "10000000", defaultPolicy())
	// Agent asks for more than per_task_max: capped, not granted.
	task := e.createTask(tn, "999999999")
	if task.Budget.String() != "5000000" {
		t.Fatalf("task budget should be capped at per_task_max, got %s", task.Budget)
	}
	// Owner issues a new policy version; the in-flight task is on the old one.
	pol := defaultPolicy()
	pol.PerRequestMax = amt("10")
	pol.PerTaskMax = amt("10")
	pol.AgentDailyMax = amt("10")
	pol.OrgDailyMax = amt("10")
	e.must(e.call("PUT", "/v1/agents/"+tn.agentID+"/policy", tn.ownerKey, pol), 201)
	r := e.begin(tn, task.ID, "old", endpoint+"/weather")
	if r.status != 403 || r.errCode() != string(policy.CodeTaskClosed) {
		t.Fatalf("old task should be closed by policy change: %d %s", r.status, r.body)
	}
	// Tasks created under the new version obey the new (tiny) limits.
	task2 := e.createTask(tn, "1000000")
	if task2.Budget.String() != "10" {
		t.Fatalf("new task budget %s", task2.Budget)
	}
	_, _, r = e.pay(tn, task2.ID, "new", "11")
	if r.errCode() != string(policy.CodePerRequestLimit) {
		t.Fatalf("expected per-request denial: %d %s", r.status, r.body)
	}
	// Owner-side validation refuses inconsistent policies and missing recipients.
	bad := defaultPolicy()
	bad.Endpoints[0].Recipients = nil
	e.must(e.call("PUT", "/v1/agents/"+tn.agentID+"/policy", tn.ownerKey, bad), 400)
	bad = defaultPolicy()
	bad.PerRequestMax = amt("99999999999")
	e.must(e.call("PUT", "/v1/agents/"+tn.agentID+"/policy", tn.ownerKey, bad), 400)
}

func TestInternalEndpointsRequireInternalTokenAndAmountsAreStrings(t *testing.T) {
	e := newEnv(t)
	tn := e.provision("acme", "1000", defaultPolicy())
	task := e.createTask(tn, "1000")
	e.must(e.call("POST", "/internal/v1/paid-requests/begin", tn.ownerKey, BeginRequest{AgentKey: tn.agentKey, TaskID: task.ID, IdempotencyKey: "x", URL: endpoint}), 401)
	e.must(e.call("POST", "/internal/v1/paid-requests/begin", tn.agentKey, BeginRequest{AgentKey: tn.agentKey, TaskID: task.ID, IdempotencyKey: "x", URL: endpoint}), 401)
	e.must(e.call("GET", "/internal/v1/grant-public-key", "", nil), 401)
	// JSON numbers for amounts are rejected.
	e.must(e.call("POST", "/v1/funding/deposits", tn.ownerKey, map[string]any{"asset": testAsset, "network": testNetwork, "amount": 100, "reference": "n"}), 400)
	e.must(e.call("POST", "/v1/funding/deposits", tn.ownerKey, map[string]any{"asset": testAsset, "network": testNetwork, "amount": "1.5", "reference": "n"}), 400)
	e.must(e.call("POST", "/v1/funding/deposits", tn.ownerKey, map[string]any{"asset": testAsset, "network": testNetwork, "amount": "-5", "reference": "n"}), 400)
}
