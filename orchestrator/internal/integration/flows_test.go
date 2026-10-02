package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/api"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/contract"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/engine"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/ids"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/offline"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/quotes/quotestest"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/testutil"
)

func TestRecordedProviderContract(t *testing.T) {
	quotestest.RunProviderContract(t, recorded)
}

// Milestone O1: the recorded dataset evaluates to its independently checked
// expectations through the real engine.
func TestOfflineEvaluation(t *testing.T) {
	dir := filepath.Join(testutil.RepoRoot(), "fixtures", "recorded", "v1")
	report, err := offline.Run(context.Background(), engine.NewClient(engineURL, testutil.EngineToken), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range report.Cases {
		if !c.Passed {
			t.Errorf("%s: %s expected=%v actual=%v", c.Name, c.Error, c.Expected, c.Actual)
		}
	}
	if report.Total != 6 || report.Recommended != 5 || report.ByOutcome["insufficient_data"] != 1 {
		t.Errorf("unexpected report: %+v", report.ByOutcome)
	}
}

// The Go request types must carry every golden request without loss.
func TestGoldenRequestsRoundTripThroughGoTypes(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join(testutil.RepoRoot(), "fixtures", "golden", "v1", "*.json"))
	if len(paths) < 40 {
		t.Fatalf("expected the full golden set, found %d", len(paths))
	}
	checked := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var vector struct {
			Request       json.RawMessage `json:"request"`
			ExpectedError json.RawMessage `json:"expected_error"`
		}
		if err := json.Unmarshal(data, &vector); err != nil {
			t.Fatal(err)
		}
		if len(vector.ExpectedError) > 0 {
			continue
		}
		var req contract.Request
		if err := contract.DecodeStrict(vector.Request, &req); err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
			continue
		}
		out, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		var want, got any
		json.Unmarshal(vector.Request, &want) //nolint:errcheck
		json.Unmarshal(out, &got)             //nolint:errcheck
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: Go types changed the request", filepath.Base(path))
		}
		checked++
	}
	if checked < 40 {
		t.Errorf("only %d requests checked", checked)
	}
}

func TestAuthenticationAndScopes(t *testing.T) {
	s := newStack(t, "shadow")
	s.expect(s.do("", "GET", "/v1/meta", nil, nil), 401, "unauthorized")
	s.expect(s.do("uno_opt_notakey", "GET", "/v1/meta", nil, nil), 401, "unauthorized")

	readOnly := s.newKey(api.ScopeEvaluationsRead)
	s.expect(s.do(readOnly, "GET", "/v1/catalog/baskets", nil, nil), 200, "")
	s.expect(s.do(readOnly, "POST", "/v1/evaluations", map[string]any{"customer_id": "c", "merchant_id": "m", "basket_ref": "b"}, nil), 403, "forbidden")
	s.expect(s.do(readOnly, "POST", "/v1/outcomes", map[string]any{}, nil), 403, "forbidden")

	// Tenants are isolated: another tenant cannot read this tenant's evaluation.
	s.prefs("cus_auth", nil)
	ev := s.expect(s.evaluate("cus_auth", "trail-running-shoes", nil), 201, "")
	other := newStack(t, "shadow")
	other.expect(other.call("GET", "/v1/evaluations/"+ev.str("id"), nil, nil), 404, "not_found")
	s.expect(s.call("GET", "/v1/evaluations/"+ev.str("id"), nil, nil), 200, "")
}

func TestShadowModeEvaluations(t *testing.T) {
	s := newStack(t, "shadow")
	meta := s.expect(s.call("GET", "/v1/meta", nil, nil), 200, "")
	if meta.str("mode") != "shadow" {
		t.Fatalf("mode = %s", meta.str("mode"))
	}

	catalog := s.expect(s.call("GET", "/v1/catalog/baskets", nil, nil), 200, "")
	if n := len(catalog.body["baskets"].([]any)); n != 6 {
		t.Fatalf("catalog has %d baskets", n)
	}

	// Without stored preferences only the card is consented, so stablecoin is excluded.
	unconsented := s.expect(s.evaluate("cus_new", "trail-running-shoes", nil), 201, "")
	if unconsented.str("result", "outcome") != "preferred" || unconsented.str("result", "rationale") != "only_eligible_option" {
		t.Errorf("unconsented customer: %s / %s", unconsented.str("result", "outcome"), unconsented.str("result", "rationale"))
	}
	if !strings.Contains(unconsented.str("warnings"), "") && unconsented.str("mode") != "shadow" {
		t.Errorf("mode = %s", unconsented.str("mode"))
	}

	// With consent and the card as default, stablecoin wins the trail shoes by $1.08.
	s.prefs("cus_1", nil)
	ev := s.expect(s.evaluate("cus_1", "trail-running-shoes", map[string]string{"Idempotency-Key": "idem-shoes-1"}), 201, "")
	if ev.str("result", "preferred_option_id") != "usdc" || ev.str("result", "rationale") != "savings_meet_threshold" {
		t.Fatalf("unexpected result: %v", ev.body["result"])
	}
	if ev.str("disclosure") != "recommendation_only" || ev.str("status") != "current" || ev.str("mode") != "shadow" {
		t.Errorf("view: disclosure=%s status=%s mode=%s", ev.str("disclosure"), ev.str("status"), ev.str("mode"))
	}
	if ev.str("versions", "formula") == "" || ev.str("versions", "policy") != "policy-2026-10-02.1" || !strings.HasPrefix(ev.str("versions", "preference"), "pref_") {
		t.Errorf("versions: %v", ev.body["versions"])
	}
	if ev.str("result", "savings_vs_baseline", "estimated_total_micro_usd") != "1081200" {
		t.Errorf("savings: %v", lookup(ev.body, "result", "savings_vs_baseline"))
	}
	if got := s.expect(s.call("GET", "/v1/evaluations/"+ev.str("id"), nil, nil), 200, ""); !reflect.DeepEqual(got.body, ev.body) {
		t.Error("GET returned a different evaluation than POST")
	}

	// Idempotency: same key and body replays; same key and a different body conflicts.
	replay := s.expect(s.evaluate("cus_1", "trail-running-shoes", map[string]string{"Idempotency-Key": "idem-shoes-1"}), 200, "")
	if replay.str("id") != ev.str("id") || replay.headers.Get("Idempotent-Replay") != "true" {
		t.Error("idempotent replay did not return the original evaluation")
	}
	s.expect(s.evaluate("cus_1", "camp-stove", map[string]string{"Idempotency-Key": "idem-shoes-1"}), 409, "idempotency_key_reused")

	// A raised threshold keeps the default; preferences are versioned.
	s.prefs("cus_1", map[string]any{"min_savings_threshold_minor": "200"})
	kept := s.expect(s.evaluate("cus_1", "trail-running-shoes", nil), 201, "")
	if kept.str("result", "preferred_option_id") != "card" || kept.str("result", "rationale") != "savings_below_threshold" {
		t.Errorf("threshold not applied: %v", kept.body["result"])
	}
	prefs := s.expect(s.call("GET", "/v1/customers/cus_1/preferences", nil, nil), 200, "")
	if prefs.str("version") != "2" || prefs.str("preferences", "min_savings_threshold_minor") != "200" {
		t.Errorf("preferences: %v", prefs.body)
	}
	s.expect(s.call("PUT", "/v1/customers/cus_1/preferences", map[string]any{"min_savings_threshold_minor": "1e3"}, nil), 400, "invalid_request")
	s.expect(s.call("PUT", "/v1/customers/cus_1/preferences", map[string]any{"min_savings_threshold_minor": "100", "consented_rails": []string{"cash"}}, nil), 422, "invalid_preferences")

	// A default the provider does not offer is reported, not guessed.
	s.prefs("cus_2", map[string]any{"default_option_id": "debit"})
	nodefault := s.expect(s.evaluate("cus_2", "trail-running-shoes", nil), 201, "")
	if nodefault.str("warnings") != "[default_method_not_offered]" || nodefault.str("result", "rationale") != "lowest_cost_no_default" {
		t.Errorf("missing default: warnings=%v rationale=%s", nodefault.body["warnings"], nodefault.str("result", "rationale"))
	}

	// Liquidity reserves apply to the matching asset.
	s.prefs("cus_3", map[string]any{"liquidity_reserves": []map[string]any{{
		"asset": map[string]any{"code": "USDC", "network": "base", "decimals": 6}, "base_units": "200000000"}}})
	reserved := s.expect(s.evaluate("cus_3", "trail-running-shoes", nil), 201, "")
	if reserved.str("result", "rationale") != "only_eligible_option" {
		t.Errorf("reserve not applied: %v", reserved.body["result"])
	}

	// Nothing can be executed in shadow mode, and no saving is realized.
	s.expect(s.call("POST", "/v1/evaluations/"+ev.str("id")+"/execution-handoffs", s.approve(ev, "usdc"), map[string]string{"Idempotency-Key": "h1"}), 409, "shadow_mode")
	report := s.expect(s.call("POST", "/v1/outcomes", map[string]any{
		"evaluation_id": ev.str("id"), "source": "customer_report", "event_id": "sel-1", "type": "payment",
		"status": "succeeded", "option_id": "usdc", "occurred_at": s.clock.Now().Format(time.RFC3339),
	}, nil), 201, "")
	if report.str("derived", "savings_basis") != "recommendation_only" || report.str("derived", "matches_recommendation") != "true" {
		t.Errorf("shadow outcome: %v", report.body["derived"])
	}
	if report.str("derived", "realized_immediate_savings_micro_usd") != "" {
		t.Error("a shadow observation must not realize savings")
	}

	// Refresh produces a new, linked evaluation; the original is unchanged.
	refreshed := s.expect(s.call("POST", "/v1/evaluations/"+ev.str("id")+"/refresh", nil, nil), 201, "")
	if refreshed.str("refreshed_from") != ev.str("id") || refreshed.str("id") == ev.str("id") {
		t.Errorf("refresh: %v", refreshed.body)
	}
	s.expect(s.call("POST", "/v1/evaluations", map[string]any{"customer_id": "cus_1", "merchant_id": "mer_demo_outfitter", "basket_ref": "nope"}, nil), 404, "not_found")
	s.expect(s.call("POST", "/v1/evaluations", map[string]any{"customer_id": "cus_1", "merchant_id": "mer_demo_outfitter", "basket_ref": "x", "extra": 1}, nil), 400, "invalid_request")

	summary := s.expect(s.call("GET", "/v1/reports/summary", nil, nil), 200, "")
	if summary.str("evaluations", "total") != "6" || summary.str("evaluations", "by_mode", "shadow") != "6" {
		t.Errorf("summary: %v", summary.body["evaluations"])
	}
	if summary.str("outcomes", "shadow_selections") != "1" || summary.str("outcomes", "shadow_followed_recommendation") != "1" {
		t.Errorf("summary outcomes: %v", summary.body["outcomes"])
	}
	if summary.str("evaluations", "exclusion_codes", "not_consented") != "1" || summary.str("evaluations", "exclusion_codes", "insufficient_funds") != "1" {
		t.Errorf("exclusion review data: %v", lookup(summary.body, "evaluations", "exclusion_codes"))
	}
}

func TestAssistedHandoffsAndOutcomes(t *testing.T) {
	s := newStack(t, "assisted")
	s.prefs("cus_a", nil)
	ev := s.expect(s.evaluate("cus_a", "trail-running-shoes", nil), 201, "")
	path := "/v1/evaluations/" + ev.str("id") + "/execution-handoffs"

	s.expect(s.call("POST", path, s.approve(ev, "usdc"), nil), 400, "invalid_request") // no idempotency key

	wrongAmount := s.approve(ev, "usdc")
	wrongAmount["approval"].(map[string]any)["approved_amount"] = map[string]any{
		"asset": map[string]any{"code": "USDC", "network": "base", "decimals": 6}, "base_units": "134590001"}
	s.expect(s.call("POST", path, wrongAmount, map[string]string{"Idempotency-Key": "h-wrong"}), 409, "approval_mismatch")

	wrongCustomer := s.approve(ev, "usdc")
	wrongCustomer["approval"].(map[string]any)["customer_id"] = "someone_else"
	s.expect(s.call("POST", path, wrongCustomer, map[string]string{"Idempotency-Key": "h-cust"}), 409, "approval_mismatch")

	stale := s.approve(ev, "usdc")
	stale["approval"].(map[string]any)["approved_at"] = s.clock.Now().Add(-time.Hour).Format(time.RFC3339)
	s.expect(s.call("POST", path, stale, map[string]string{"Idempotency-Key": "h-stale"}), 409, "approval_out_of_window")

	s.expect(s.call("POST", path, map[string]any{"option_id": "paypal", "approval": s.approve(ev, "usdc")["approval"]},
		map[string]string{"Idempotency-Key": "h-unknown"}), 422, "unknown_option")

	// The customer may approve the non-preferred option; the choice is theirs.
	approvedCard := s.approve(ev, "card")
	handoff := s.expect(s.call("POST", path, approvedCard, map[string]string{"Idempotency-Key": "h-1"}), 201, "")
	if handoff.str("option_id") != "card" || handoff.str("status") != "created" || handoff.str("executable") != "true" {
		t.Fatalf("handoff: %v", handoff.body)
	}
	if strings.Contains(handoff.str("id"), "card") || strings.HasPrefix(handoff.str("id"), "hand_") == false {
		t.Errorf("handoff id %q", handoff.str("id"))
	}
	replay := s.expect(s.call("POST", path, approvedCard, map[string]string{"Idempotency-Key": "h-1"}), 200, "")
	if replay.str("id") != handoff.str("id") {
		t.Error("replay returned a different handoff")
	}
	s.expect(s.call("POST", path, s.approve(ev, "usdc"), map[string]string{"Idempotency-Key": "h-2"}), 409, "prior_attempt_unresolved")
	s.expect(s.call("GET", "/v1/execution-handoffs/"+handoff.str("id"), nil, nil), 200, "")
	if pending, _ := db.PendingOutbox(context.Background(), s.tenant); pending != 1 {
		t.Errorf("outbox pending = %d, want 1", pending)
	}

	// An excluded option can never be handed off.
	filter := s.expect(s.evaluate("cus_a", "water-filter", nil), 201, "")
	excluded := map[string]any{"option_id": "usdc", "approval": map[string]any{
		"customer_id": "cus_a", "approved_at": s.clock.Now().Format(time.RFC3339), "reference": "r",
		"approved_amount": map[string]any{"asset": map[string]any{"code": "USDC", "network": "base", "decimals": 6}, "base_units": "54550000"}}}
	s.expect(s.call("POST", "/v1/evaluations/"+filter.str("id")+"/execution-handoffs", excluded, map[string]string{"Idempotency-Key": "h-x"}), 409, "option_not_eligible")

	// Executor reports the card payment at exactly the estimated amount.
	outcome := func(eventID, status, optionID string, charged map[string]any, extra map[string]any) map[string]any {
		body := map[string]any{
			"evaluation_id": ev.str("id"), "handoff_id": handoff.str("id"), "source": "executor", "event_id": eventID,
			"type": "payment", "status": status, "option_id": optionID, "occurred_at": s.clock.Now().Format(time.RFC3339),
		}
		if charged != nil {
			body["charged"] = charged
		}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}
	s.expect(s.call("POST", "/v1/outcomes", outcome("pay-1", "succeeded", "usdc", amountDue(ev, "usdc"), nil), nil), 409, "method_mismatch")
	paid := s.expect(s.call("POST", "/v1/outcomes", outcome("pay-1", "succeeded", "card", amountDue(ev, "card"), nil), nil), 201, "")
	derived := paid.body["derived"].(map[string]any)
	// The customer used the baseline (card), so nothing was saved against it.
	if derived["savings_basis"] != "baseline_method_used" || derived["charge_difference_base_units"] != "0" || derived["matches_recommendation"] != false {
		t.Errorf("derived: %v", derived)
	}
	if derived["estimated_rewards_micro_usd"] != "2768800" {
		t.Errorf("estimated rewards must be reported separately: %v", derived)
	}
	if got := s.expect(s.call("GET", "/v1/execution-handoffs/"+handoff.str("id"), nil, nil), 200, ""); got.str("status") != "succeeded" || got.str("executable") != "false" {
		t.Errorf("handoff after payment: %v", got.body)
	}
	s.expect(s.call("POST", "/v1/outcomes", outcome("pay-1", "succeeded", "card", amountDue(ev, "card"), nil), nil), 200, "")
	s.expect(s.call("POST", "/v1/outcomes", outcome("pay-1", "failed", "card", nil, nil), nil), 409, "event_id_reused")
	s.expect(s.call("POST", "/v1/outcomes", outcome("pay-2", "failed", "card", nil, nil), nil), 409, "conflicting_outcome")

	// Refunds: partial, then one that would exceed the charge.
	refund := func(eventID, units string) map[string]any {
		return outcome(eventID, "succeeded", "card", nil, map[string]any{"type": "refund", "refund": map[string]any{
			"amount":          map[string]any{"asset": map[string]any{"code": "USD", "decimals": 2}, "base_units": units},
			"conversion_cost": nil, "initiated_at": s.clock.Now().Format(time.RFC3339),
			"completed_at": s.clock.Now().Add(72 * time.Hour).Format(time.RFC3339)}})
	}
	partial := s.expect(s.call("POST", "/v1/outcomes", refund("ref-1", "845"), nil), 201, "")
	if partial.str("derived", "refunded_total_base_units") != "845" || partial.str("derived", "fully_refunded") != "false" || partial.str("derived", "conversion_cost_known") != "false" || partial.str("derived", "seconds_to_complete") != "259200" {
		t.Errorf("refund derived: %v", partial.body["derived"])
	}
	s.expect(s.call("POST", "/v1/outcomes", refund("ref-2", "13000"), nil), 409, "refund_exceeds_charge")
	full := s.expect(s.call("POST", "/v1/outcomes", refund("ref-3", "12999"), nil), 201, "")
	if full.str("derived", "fully_refunded") != "true" {
		t.Errorf("full refund: %v", full.body["derived"])
	}

	// A settled attempt no longer blocks a new one; here the customer takes the recommendation.
	ev2 := s.expect(s.evaluate("cus_a", "trail-running-shoes", nil), 201, "")
	h2 := s.expect(s.call("POST", "/v1/evaluations/"+ev2.str("id")+"/execution-handoffs", s.approve(ev2, "usdc"), map[string]string{"Idempotency-Key": "h-3"}), 201, "")
	paid2 := s.expect(s.call("POST", "/v1/outcomes", map[string]any{
		"evaluation_id": ev2.str("id"), "handoff_id": h2.str("id"), "source": "provider", "event_id": "pay-3",
		"type": "payment", "status": "succeeded", "option_id": "usdc", "occurred_at": s.clock.Now().Format(time.RFC3339),
		"charged": amountDue(ev2, "usdc"),
	}, nil), 201, "")
	d2 := paid2.body["derived"].(map[string]any)
	if d2["savings_basis"] != "realized_vs_estimated_baseline" || d2["realized_immediate_savings_micro_usd"] != "3850000" {
		t.Errorf("realized savings: %v", d2)
	}
	if cf := d2["counterfactual"].(map[string]any); cf["observed"] != false || cf["baseline_option_id"] != "card" || cf["baseline_immediate_cost_micro_usd"] != "138440000" {
		t.Errorf("counterfactual must be explicit and unobserved: %v", cf)
	}

	// A charge that differs from the estimate is reported, not scored as a saving.
	ev3 := s.expect(s.evaluate("cus_a", "coffee-box", nil), 201, "")
	h3 := s.expect(s.call("POST", "/v1/evaluations/"+ev3.str("id")+"/execution-handoffs", s.approve(ev3, "usdc"), map[string]string{"Idempotency-Key": "h-4"}), 201, "")
	differs := s.expect(s.call("POST", "/v1/outcomes", map[string]any{
		"evaluation_id": ev3.str("id"), "handoff_id": h3.str("id"), "source": "provider", "event_id": "pay-4",
		"type": "payment", "status": "succeeded", "option_id": "usdc", "occurred_at": s.clock.Now().Format(time.RFC3339),
		"charged": map[string]any{"asset": map[string]any{"code": "USDC", "network": "base", "decimals": 6}, "base_units": "34060000"},
	}, nil), 201, "")
	if differs.str("derived", "savings_basis") != "charge_differs_from_estimate" || differs.str("derived", "charge_difference_base_units") != "10000" {
		t.Errorf("charge difference: %v", differs.body["derived"])
	}

	summary := s.expect(s.call("GET", "/v1/reports/summary", nil, nil), 200, "")
	if summary.str("outcomes", "realized_immediate_savings_micro_usd") != "3850000" || summary.str("outcomes", "realized_savings_count") != "1" ||
		summary.str("outcomes", "charge_differs_from_estimate") != "1" || summary.str("outcomes", "refunds") != "2" ||
		summary.str("handoffs", "chose_preferred") != "2" || summary.str("handoffs", "chose_baseline") != "1" {
		t.Errorf("summary: %v", summary.body)
	}
	if summary.str("outcomes", "estimated_rewards_micro_usd") != "2768800" {
		t.Errorf("estimated rewards must be reported separately from realized savings: %v", summary.body["outcomes"])
	}
}

func TestExpiryPauseAndReconciliation(t *testing.T) {
	s := newStack(t, "assisted")
	s.prefs("cus_e", nil)
	ev := s.expect(s.evaluate("cus_e", "camp-stove", nil), 201, "")
	path := "/v1/evaluations/" + ev.str("id") + "/execution-handoffs"

	// Pause stops new evaluations and makes existing handoffs non-executable.
	handoff := s.expect(s.call("POST", path, s.approve(ev, "card"), map[string]string{"Idempotency-Key": "p-1"}), 201, "")
	if err := db.SetPaused(context.Background(), s.tenant, true, "incident", s.clock.Now()); err != nil {
		t.Fatal(err)
	}
	s.expect(s.evaluate("cus_e", "camp-stove", nil), 503, "paused")
	if got := s.expect(s.call("GET", "/v1/execution-handoffs/"+handoff.str("id"), nil, nil), 200, ""); got.str("executable") != "false" || got.str("not_executable_reason") != "paused" {
		t.Errorf("paused handoff: %v", got.body)
	}
	if err := db.SetPaused(context.Background(), s.tenant, false, "", s.clock.Now()); err != nil {
		t.Fatal(err)
	}

	// Past expiry: the evaluation reads as expired, the handoff is not
	// executable, and a new approval cannot be handed off.
	s.clock.Advance(11 * time.Minute)
	if got := s.expect(s.call("GET", "/v1/evaluations/"+ev.str("id"), nil, nil), 200, ""); got.str("status") != "expired" {
		t.Errorf("evaluation status = %s", got.str("status"))
	}
	if got := s.expect(s.call("GET", "/v1/execution-handoffs/"+handoff.str("id"), nil, nil), 200, ""); got.str("not_executable_reason") != "expired" {
		t.Errorf("expired handoff: %v", got.body)
	}
	s.expect(s.call("POST", path, s.approve(ev, "card"), map[string]string{"Idempotency-Key": "p-2"}), 409, "evaluation_expired")

	// The expired attempt has no outcome, so the sweep marks it unknown and a
	// fresh evaluation still cannot switch rails until it is reconciled.
	if n, err := s.svc.Sweep(context.Background(), time.Minute); err != nil || n != 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	fresh := s.expect(s.evaluate("cus_e", "camp-stove", nil), 201, "")
	freshPath := "/v1/evaluations/" + fresh.str("id") + "/execution-handoffs"
	s.expect(s.call("POST", freshPath, s.approve(fresh, "usdc"), map[string]string{"Idempotency-Key": "p-3"}), 409, "prior_attempt_unresolved")

	// The executor reports it never charged; the customer can proceed on a new approval.
	s.expect(s.call("POST", "/v1/outcomes", map[string]any{
		"evaluation_id": ev.str("id"), "handoff_id": handoff.str("id"), "source": "executor", "event_id": "rec-1",
		"type": "payment", "status": "failed", "option_id": "card", "occurred_at": s.clock.Now().Format(time.RFC3339),
	}, nil), 201, "")
	s.expect(s.call("POST", freshPath, s.approve(fresh, "usdc"), map[string]string{"Idempotency-Key": "p-4"}), 201, "")
}

func TestConcurrentHandoffsOpenExactlyOneAttempt(t *testing.T) {
	s := newStack(t, "assisted")
	s.prefs("cus_c", nil)
	ev := s.expect(s.evaluate("cus_c", "trail-running-shoes", nil), 201, "")
	path := "/v1/evaluations/" + ev.str("id") + "/execution-handoffs"

	const n = 12
	var wg sync.WaitGroup
	results := make([]response, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			option := "usdc"
			if i%2 == 0 {
				option = "card"
			}
			results[i] = s.call("POST", path, s.approve(ev, option), map[string]string{"Idempotency-Key": ids.New("k")})
		}()
	}
	wg.Wait()
	createdCount, blocked := 0, 0
	for _, r := range results {
		switch {
		case r.status == 201:
			createdCount++
		case r.status == 409 && r.errorCode() == "prior_attempt_unresolved":
			blocked++
		default:
			t.Errorf("unexpected %d %s", r.status, r.errorCode())
		}
	}
	if createdCount != 1 || blocked != n-1 {
		t.Errorf("created=%d blocked=%d", createdCount, blocked)
	}
}

func TestSnapshotsAreAppendOnly(t *testing.T) {
	s := newStack(t, "shadow")
	ev := s.expect(s.evaluate("cus_i", "camp-stove", nil), 201, "")
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	for _, stmt := range []string{
		`UPDATE evaluations SET outcome = 'tie' WHERE id = $1`,
		`DELETE FROM evaluations WHERE id = $1`,
		`UPDATE rail_quotes SET quote_id = 'x' WHERE evaluation_id = $1`,
		`DELETE FROM basket_snapshots WHERE id = (SELECT basket_snapshot_id FROM evaluations WHERE id = $1)`,
	} {
		if _, err := conn.Exec(context.Background(), stmt, ev.str("id")); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: expected the append-only trigger, got %v", stmt, err)
		}
	}
}
