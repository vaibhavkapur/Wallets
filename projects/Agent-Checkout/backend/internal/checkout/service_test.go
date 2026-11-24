package checkout

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var ctx = context.Background()

func setup(t *testing.T) *Service {
	t.Helper()
	store, e := NewFileStore("")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(store.Close)
	return NewService(store)
}
func ready(t *testing.T, s *Service, tenant, scenario string) (Checkout, Consent) {
	t.Helper()
	in, e := s.CreateIntent(ctx, tenant, IntentInput{"studio-pro", "Midnight", 1, 20000, "94103"})
	if e != nil {
		t.Fatal(e)
	}
	c, e := s.Quote(ctx, tenant, in.ID)
	if e != nil {
		t.Fatal(e)
	}
	if scenario != "success" {
		c, e = s.Select(ctx, tenant, c.ID, SelectionInput{c.Version, c.Quote.MerchantID, c.CardID, scenario})
		if e != nil {
			t.Fatal(e)
		}
	}
	a, e := s.Approve(ctx, tenant, c.ID, c.Version)
	if e != nil {
		t.Fatal(e)
	}
	return c, a
}
func getState(t *testing.T, s *Service, tenant string) *State {
	t.Helper()
	st, e := s.Store.View(ctx, tenant)
	if e != nil {
		t.Fatal(e)
	}
	return st
}
func work(t *testing.T, s *Service, tenant string) {
	t.Helper()
	worked, e := s.Work(ctx, tenant)
	if e != nil || !worked {
		t.Fatalf("work=%v error=%v", worked, e)
	}
}
func execute(t *testing.T, s *Service, tenant string, c Checkout, a Consent) {
	t.Helper()
	if _, e := s.Execute(ctx, tenant, c.ID, a.ID, "purchase-key-0001"); e != nil {
		t.Fatal(e)
	}
}
func TestConcurrentExecutionAndWorkerExactlyOnce(t *testing.T) {
	s := setup(t)
	c, a := ready(t, s, "alice", "success")
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Execute(ctx, "alice", c.ID, a.ID, "same-key-0001"); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Work(ctx, "alice"); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	st := getState(t, s, "alice")
	if len(st.Attempts) != 1 || len(st.Orders) != 1 || len(st.Provider) != 1 || st.Reserved != 0 || st.Spent != c.Quote.Total {
		t.Fatalf("duplicate or inconsistent spending: %#v", st)
	}
	if _, e := s.Execute(ctx, "alice", c.ID, a.ID, "different-key-0001"); e == nil {
		t.Fatal("second attempt accepted")
	}
	if len(st.Ledger) != 2 {
		t.Fatalf("want reserve and capture; got %d", len(st.Ledger))
	}
}
func TestConcurrentBudgetReservation(t *testing.T) {
	s := setup(t)
	c1, a1 := ready(t, s, "alice", "success")
	c2, a2 := ready(t, s, "alice", "success")
	s.Store.Update(ctx, "alice", func(st *State) error { st.Limit = 20000; return nil })
	var successes atomic.Int64
	var wg sync.WaitGroup
	for i, pair := range []struct {
		c Checkout
		a Consent
	}{{c1, a1}, {c2, a2}} {
		wg.Add(1)
		go func(i int, c Checkout, a Consent) {
			defer wg.Done()
			if _, e := s.Execute(ctx, "alice", c.ID, a.ID, fmt.Sprintf("budget-key-%d", i)); e == nil {
				successes.Add(1)
			}
		}(i, pair.c, pair.a)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("want one reservation, got %d", successes.Load())
	}
	st := getState(t, s, "alice")
	if st.Reserved != c1.Quote.Total || len(st.Attempts) != 1 {
		t.Fatal("budget double spent")
	}
}
func TestApprovalInvariants(t *testing.T) {
	for _, scenario := range []string{"expired", "revoked", "changed_card", "tampered_cart", "wrong_approval", "expired_quote", "unsupported_card", "paused", "price_changed", "out_of_stock"} {
		t.Run(scenario, func(t *testing.T) {
			s := setup(t)
			merchantScenario := "success"
			if scenario == "price_changed" || scenario == "out_of_stock" {
				merchantScenario = scenario
			}
			c, a := ready(t, s, "alice", merchantScenario)
			switch scenario {
			case "expired":
				s.Now = func() time.Time { return time.Now().UTC().Add(6 * time.Minute) }
			case "revoked":
				if e := s.Revoke(ctx, "alice", c.ID); e != nil {
					t.Fatal(e)
				}
			case "changed_card":
				if _, e := s.Select(ctx, "alice", c.ID, SelectionInput{c.Version, c.Quote.MerchantID, "travel", "success"}); e != nil {
					t.Fatal(e)
				}
			case "tampered_cart":
				s.Store.Update(ctx, "alice", func(st *State) error {
					x := st.Checkouts[c.ID]
					x.Intent.Quantity = 3
					st.Checkouts[c.ID] = x
					return nil
				})
			case "wrong_approval":
				a.ID = "approval_fake"
			case "expired_quote":
				s.Now = func() time.Time { return time.Now().UTC().Add(20 * time.Minute) }
			case "unsupported_card":
				s.Store.Update(ctx, "alice", func(st *State) error { st.Cards[0].Enrolled = false; return nil })
			case "paused":
				s.Pause(ctx, "alice", true)
			}
			if _, e := s.Execute(ctx, "alice", c.ID, a.ID, "invalid-key-001"); e == nil {
				t.Fatal("invalid purchase accepted")
			}
			st := getState(t, s, "alice")
			if st.Reserved != 0 || len(st.Attempts) != 0 || len(st.Provider) != 0 {
				t.Fatal("rejected purchase had side effects")
			}
		})
	}
}
func TestIssuerChallenge(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(fmt.Sprint(success), func(t *testing.T) {
			s := setup(t)
			c, a := ready(t, s, "alice", "challenge")
			execute(t, s, "alice", c, a)
			work(t, s, "alice")
			st := getState(t, s, "alice")
			attempt := st.Attempts[st.Checkouts[c.ID].AttemptID]
			if attempt.Status != "requires_customer_action" || st.Reserved != c.Quote.Total || len(st.Orders) != 0 {
				t.Fatal("challenge was not gated")
			}
			if e := s.Confirm(ctx, "bob", c.ID, true); e == nil {
				t.Fatal("cross-tenant confirmation accepted")
			}
			if e := s.Confirm(ctx, "alice", c.ID, success); e != nil {
				t.Fatal(e)
			}
			st = getState(t, s, "alice")
			if st.Reserved != 0 {
				t.Fatal("reservation not finalized")
			}
			if success && len(st.Orders) != 1 || !success && st.Spent != 0 {
				t.Fatal("wrong challenge result")
			}
		})
	}
}
func TestTimeoutReconciliationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, e := NewFileStore(path)
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(store)
	c, a := ready(t, s, "alice", "timeout")
	execute(t, s, "alice", c, a)
	work(t, s, "alice")
	st := getState(t, s, "alice")
	attemptID := st.Checkouts[c.ID].AttemptID
	if st.Attempts[attemptID].Status != "outcome_unknown" || st.Reserved != c.Quote.Total || st.Provider[attemptID].Status != "succeeded" {
		t.Fatal("unknown result was not held")
	}
	if _, e = s.Execute(ctx, "alice", c.ID, a.ID, "brand-new-key"); e == nil {
		t.Fatal("unknown result allowed another payment")
	}
	store.Close()
	store, e = NewFileStore(path)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	s = NewService(store)
	s.Now = func() time.Time { return time.Now().UTC().Add(15 * time.Second) }
	work(t, s, "alice")
	st = getState(t, s, "alice")
	if len(st.Provider) != 1 || len(st.Orders) != 1 || st.Reserved != 0 || st.Spent != c.Quote.Total {
		t.Fatal("restart reconciliation failed")
	}
	if e = s.Reconcile(ctx, "alice", c.ID); e != nil {
		t.Fatal(e)
	}
}
func TestRefundsAndCancellation(t *testing.T) {
	s := setup(t)
	c, a := ready(t, s, "alice", "success")
	execute(t, s, "alice", c, a)
	work(t, s, "alice")
	st := getState(t, s, "alice")
	oid := st.Checkouts[c.ID].OrderID
	r, e := s.Refund(ctx, "alice", oid, "refund-key-001", 5000, "Partial return", false)
	if e != nil {
		t.Fatal(e)
	}
	duplicate, e := s.Refund(ctx, "alice", oid, "refund-key-001", 5000, "Partial return", false)
	if e != nil || duplicate.ID != r.ID {
		t.Fatal("refund not idempotent")
	}
	if _, e = s.Refund(ctx, "alice", oid, "refund-key-002", 14000, "Too much", false); e == nil {
		t.Fatal("pending refund did not count toward cap")
	}
	work(t, s, "alice")
	st = getState(t, s, "alice")
	if st.Orders[oid].PaymentStatus != "partially_refunded" || st.Spent != c.Quote.Total-5000 {
		t.Fatal("partial refund accounting wrong")
	}
	if _, e = s.Refund(ctx, "alice", oid, "cancel-key-001", 0, "Cancel order", true); e != nil {
		t.Fatal(e)
	}
	work(t, s, "alice")
	st = getState(t, s, "alice")
	if st.Orders[oid].FulfillmentStatus != "canceled" || st.Orders[oid].PaymentStatus != "refunded" || st.Spent != 0 || len(st.Refunds) != 2 {
		t.Fatal("cancellation refund accounting wrong")
	}
}
func TestMerchantFailureIndependentOfPayment(t *testing.T) {
	s := setup(t)
	c, a := ready(t, s, "alice", "merchant_failure")
	execute(t, s, "alice", c, a)
	work(t, s, "alice")
	st := getState(t, s, "alice")
	o := st.Orders[st.Checkouts[c.ID].OrderID]
	if o.PaymentStatus != "succeeded" || o.FulfillmentStatus != "failed" || st.Spent != o.Total {
		t.Fatal("payment and order were conflated")
	}
	if _, e := s.Refund(ctx, "alice", o.ID, "failure-refund", o.Total, "Merchant failed", false); e != nil {
		t.Fatal(e)
	}
	work(t, s, "alice")
	if getState(t, s, "alice").Spent != 0 {
		t.Fatal("refund failed")
	}
}
func TestDeclineReleasesReservation(t *testing.T) {
	s := setup(t)
	c, a := ready(t, s, "alice", "declined")
	execute(t, s, "alice", c, a)
	work(t, s, "alice")
	st := getState(t, s, "alice")
	if st.Reserved != 0 || st.Spent != 0 || len(st.Orders) != 0 || st.Checkouts[c.ID].State != "canceled" {
		t.Fatal("decline state incorrect")
	}
}
func TestTenantIsolationAndIdempotencyMismatch(t *testing.T) {
	s := setup(t)
	c, a := ready(t, s, "alice", "success")
	if _, e := s.Execute(ctx, "bob", c.ID, a.ID, "tenant-key-001"); e == nil {
		t.Fatal("cross tenant execute")
	}
	if _, e := s.Select(ctx, "bob", c.ID, SelectionInput{}); e == nil {
		t.Fatal("cross tenant select")
	}
	execute(t, s, "alice", c, a)
	c2, a2 := ready(t, s, "alice", "success")
	if _, e := s.Execute(ctx, "alice", c2.ID, a2.ID, "purchase-key-0001"); e == nil {
		t.Fatal("key reused with different payload")
	}
	if _, e := s.Execute(ctx, "alice", c2.ID, a2.ID, ""); e == nil {
		t.Fatal("empty key accepted")
	}
	d, e := s.Dashboard(ctx, "bob")
	if e != nil || len(d.Checkouts) != 0 || len(d.Audit) != 0 {
		t.Fatal("tenant data leaked")
	}
}
func TestMoneyAndRewardRounding(t *testing.T) {
	for _, raw := range []string{`12.34`, `"12.34"`, `"-1"`, `"1000000001"`, `"9223372036854775808"`} {
		var amount Money
		if json.Unmarshal([]byte(raw), &amount) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	b, _ := json.Marshal(Money(18294))
	if string(b) != `"18294"` {
		t.Fatal(string(b))
	}
	r := rank(defaultCards(), Quote{Total: 18294, Networks: []string{"visa", "mastercard"}})
	if r[0].Reward != 548 || r[0].Card.ID != "everyday" || r[2].Eligible {
		t.Fatal("invalid reward ranking")
	}
}
func TestFileStoreRollbackAndLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	store, e := NewFileStore(path)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if other, e := NewFileStore(path); e == nil {
		other.Close()
		t.Fatal("second writer accepted")
	}
	_ = store.Update(ctx, "alice", func(s *State) error { s.Spent = 500; return fmt.Errorf("rollback") })
	st, e := store.View(ctx, "alice")
	if e != nil || st.Spent != 0 {
		t.Fatal("transaction not rolled back")
	}
	if e = store.Update(ctx, "alice", func(s *State) error { s.Spent = 500; return nil }); e != nil {
		t.Fatal(e)
	}
}
func TestPostgresPersistence(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	store, e := NewPostgresStore(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	tenant := id("integration")
	defer store.pool.Exec(ctx, "DELETE FROM checkout_tenants WHERE tenant_id=$1", tenant)
	s := NewService(store)
	c, a := ready(t, s, tenant, "success")
	execute(t, s, tenant, c, a)
	work(t, s, tenant)
	other, e := NewPostgresStore(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	st, e := other.View(ctx, tenant)
	if e != nil || len(st.Orders) != 1 || st.Spent != c.Quote.Total {
		t.Fatal("PostgreSQL persistence failed", e)
	}
}

func TestQueuedPaymentExpiresBeforeProviderSubmission(t *testing.T) {
	s := setup(t)
	c, a := ready(t, s, "alice", "success")
	execute(t, s, "alice", c, a)
	if e := s.Store.Update(ctx, "alice", func(st *State) error {
		consent := st.Consents[a.ID]
		consent.ExpiresAt = time.Now().Add(-time.Minute)
		st.Consents[a.ID] = consent
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	work(t, s, "alice")
	st := getState(t, s, "alice")
	if st.Spent != 0 || st.Reserved != 0 || len(st.Orders) != 0 {
		t.Fatal("expired queued approval resulted in a charge")
	}
}

func TestFulfillmentAndReturnWindow(t *testing.T) {
	s := setup(t)
	c, a := ready(t, s, "alice", "success")
	execute(t, s, "alice", c, a)
	work(t, s, "alice")
	oid := getState(t, s, "alice").Checkouts[c.ID].OrderID
	if e := s.AdvanceFulfillment(ctx, "alice", oid); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Refund(ctx, "alice", oid, "cancel-shipped", 0, "Cancel", true); e == nil {
		t.Fatal("shipped order canceled")
	}
	if e := s.AdvanceFulfillment(ctx, "alice", oid); e != nil {
		t.Fatal(e)
	}
	st := getState(t, s, "alice")
	if st.Orders[oid].DeliveredAt.IsZero() || st.Orders[oid].PaymentStatus != "succeeded" {
		t.Fatal("delivery altered payment or lost delivery date")
	}
	s.Now = func() time.Time { return time.Now().UTC().Add(31 * 24 * time.Hour) }
	if _, e := s.Refund(ctx, "alice", oid, "late-refund-key", 100, "Late return", false); e == nil {
		t.Fatal("late refund accepted")
	}
}
