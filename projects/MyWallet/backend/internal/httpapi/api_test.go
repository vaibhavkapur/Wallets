package httpapi

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"mywallet/backend/internal/domain"
	"mywallet/backend/internal/ids"
	"mywallet/backend/internal/provider/fake"
)

func TestSignInRestoresSameAccount(t *testing.T) {
	e := newEnv(t)
	subj := e.newSubject()
	r1 := e.do(http.MethodPost, "/v1/auth/apple", "", map[string]any{"identity_token": "dev:" + subj}, nil)
	expectStatus(t, r1, http.StatusCreated)
	r2 := e.do(http.MethodPost, "/v1/auth/apple", "", map[string]any{"identity_token": "dev:" + subj}, nil)
	expectStatus(t, r2, http.StatusOK)
	if r1.str("user.id") != r2.str("user.id") {
		t.Fatal("second sign-in created a different user")
	}
	if r1.str("session_token") == r2.str("session_token") {
		t.Fatal("sessions must be distinct")
	}
	me := e.do(http.MethodGet, "/v1/me", r2.str("session_token"), nil, nil)
	expectStatus(t, me, http.StatusOK)

	bad := e.do(http.MethodPost, "/v1/auth/apple", "", map[string]any{"identity_token": "garbage"}, nil)
	expectStatus(t, bad, http.StatusUnauthorized)
	noauth := e.do(http.MethodGet, "/v1/cards", "", nil, nil)
	expectStatus(t, noauth, http.StatusUnauthorized)
}

func TestSignOutRevokesSession(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())
	expectStatus(t, e.do(http.MethodPost, "/v1/auth/sign-out", tok, nil, nil), http.StatusNoContent)
	expectStatus(t, e.do(http.MethodGet, "/v1/cards", tok, nil, nil), http.StatusUnauthorized)
}

func TestAddCardHappyPathAndReplay(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())

	empty := e.do(http.MethodGet, "/v1/cards", tok, nil, nil)
	expectStatus(t, empty, http.StatusOK)
	if n := len(empty.get("cards").([]any)); n != 0 {
		t.Fatalf("expected empty wallet, got %d", n)
	}

	key := ids.New("key")
	r := e.startSetup(tok, key)
	expectStatus(t, r, http.StatusCreated)
	if r.str("status") != "collecting" {
		t.Fatalf("status %s", r.str("status"))
	}
	if r.str("client_session.setup_client_secret") == "" || r.str("client_session.ephemeral_key_secret") == "" ||
		r.str("client_session.customer_ref") == "" || r.str("client_session.publishable_key") == "" {
		t.Fatalf("client session incomplete: %s", r.Raw)
	}
	id := r.str("id")

	// Repeated tap / network retry with the same key returns the same attempt.
	replay := e.startSetup(tok, key)
	expectStatus(t, replay, http.StatusOK)
	if replay.str("id") != id {
		t.Fatal("replay created a second attempt")
	}
	if e.pv.Calls("create_setup") != 1 {
		t.Fatalf("provider setup created %d times", e.pv.Calls("create_setup"))
	}

	// GET never leaks setup credentials.
	g := e.do(http.MethodGet, "/v1/card-setups/"+id, tok, nil, nil)
	expectStatus(t, g, http.StatusOK)
	if strings.Contains(string(g.Raw), "client_session") {
		t.Fatal("GET exposed client session")
	}

	// Cardholder completes the sheet; the app polls the verified status.
	if _, err := e.pv.CompleteSetup(e.setupRef(id), fake.Visa); err != nil {
		t.Fatal(err)
	}
	g = e.do(http.MethodGet, "/v1/card-setups/"+id, tok, nil, nil)
	if g.str("status") != "saved" || g.str("card.brand") != "visa" || g.str("card.last4") != "4242" {
		t.Fatalf("unexpected setup: %s", g.Raw)
	}
	if g.get("card.is_preferred") != true {
		t.Fatal("first card should become preferred")
	}

	// Replaying the create after success returns the saved result and no secrets.
	replay = e.startSetup(tok, key)
	if replay.str("status") != "saved" || replay.get("client_session") != nil {
		t.Fatalf("terminal replay: %s", replay.Raw)
	}

	// "Relaunch": the wallet list shows the same masked card.
	list := e.do(http.MethodGet, "/v1/cards", tok, nil, nil)
	cards := list.get("cards").([]any)
	if len(cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(cards))
	}
	c := cards[0].(map[string]any)
	if c["last4"] != "4242" || c["state"] != "saved" || c["exp_month"].(float64) != 12 {
		t.Fatalf("card: %v", c)
	}
	for k := range c {
		if strings.Contains(k, "number") || strings.Contains(k, "cvc") || strings.Contains(k, "secret") {
			t.Fatalf("card payload exposes %s", k)
		}
	}
}

func TestSetupRequiresIdempotencyKeyAndConsent(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())
	body := map[string]any{"consent": map[string]any{"accepted": true, "version": "2026-10"}}
	expectStatus(t, e.do(http.MethodPost, "/v1/card-setups", tok, body, nil), http.StatusBadRequest)
	noConsent := map[string]any{"consent": map[string]any{"accepted": false, "version": "2026-10"}}
	expectStatus(t, e.do(http.MethodPost, "/v1/card-setups", tok, noConsent, map[string]string{"Idempotency-Key": "k1"}), http.StatusBadRequest)
}

func TestSetupStateTracking(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())

	// Additional authentication requested.
	r := e.startSetup(tok, ids.New("key"))
	ref := e.setupRef(r.str("id"))
	expectStatus(t, e.do(http.MethodPost, "/dev/card-setups/"+r.str("id")+"/outcome", tok, map[string]any{"outcome": "requires_action"}, nil), http.StatusOK)
	if s := e.do(http.MethodGet, "/v1/card-setups/"+r.str("id"), tok, nil, nil).str("status"); s != "authentication_required" {
		t.Fatalf("status %s", s)
	}

	// Decline surfaces a failure code while collecting continues.
	expectStatus(t, e.do(http.MethodPost, "/dev/card-setups/"+r.str("id")+"/outcome", tok, map[string]any{"outcome": "declined", "code": "insufficient_funds"}, nil), http.StatusOK)
	g := e.do(http.MethodGet, "/v1/card-setups/"+r.str("id"), tok, nil, nil)
	if g.str("status") != "collecting" || g.str("failure_code") != "insufficient_funds" {
		t.Fatalf("decline: %s", g.Raw)
	}

	// User dismisses after failure: attempt is failed, and the provider setup canceled.
	c := e.do(http.MethodPost, "/v1/card-setups/"+r.str("id")+"/close", tok, map[string]any{"outcome": "failed"}, nil)
	if c.str("status") != "failed" || c.str("failure_code") != "insufficient_funds" {
		t.Fatalf("close: %s", c.Raw)
	}
	setup, _ := e.pv.GetSetup(context.Background(), ref)
	if setup.State != "canceled" {
		t.Fatalf("provider setup should be canceled, got %s", setup.State)
	}

	// Processing: tracked, and the worker finishes it when the provider does.
	r2 := e.startSetup(tok, ids.New("key"))
	expectStatus(t, e.do(http.MethodPost, "/dev/card-setups/"+r2.str("id")+"/outcome", tok, map[string]any{"outcome": "processing"}, nil), http.StatusOK)
	if s := e.do(http.MethodGet, "/v1/card-setups/"+r2.str("id"), tok, nil, nil).str("status"); s != "processing" {
		t.Fatalf("status %s", s)
	}
	if _, err := e.st.ActiveOperation(context.Background(), domain.OpReconcileSetup, r2.str("id")); err != nil {
		t.Fatalf("expected reconcile operation: %v", err)
	}
	if _, err := e.pv.CompleteSetup(e.setupRef(r2.str("id")), fake.Visa); err != nil {
		t.Fatal(err)
	}
	e.worker.Tick(context.Background())
	a, _ := e.st.GetSetupAttemptByID(context.Background(), r2.str("id"))
	if a.Status != domain.SetupSaved {
		t.Fatalf("worker should have saved the card, status %s", a.Status)
	}
	if _, err := e.st.ActiveOperation(context.Background(), domain.OpReconcileSetup, r2.str("id")); err == nil {
		t.Fatal("reconcile operation should be completed")
	}

	// Cancel without failure.
	r3 := e.startSetup(tok, ids.New("key"))
	c3 := e.do(http.MethodPost, "/v1/card-setups/"+r3.str("id")+"/close", tok, map[string]any{}, nil)
	if c3.str("status") != "canceled" {
		t.Fatalf("cancel: %s", c3.Raw)
	}

	// Closing after the provider already succeeded must save, not cancel.
	r4 := e.startSetup(tok, ids.New("key"))
	if _, err := e.pv.CompleteSetup(e.setupRef(r4.str("id")), fake.Card{Brand: "mastercard", Last4: "4444", ExpMonth: 1, ExpYear: 2031}); err != nil {
		t.Fatal(err)
	}
	c4 := e.do(http.MethodPost, "/v1/card-setups/"+r4.str("id")+"/close", tok, map[string]any{"outcome": "canceled"}, nil)
	if c4.str("status") != "saved" || c4.str("card.last4") != "4444" {
		t.Fatalf("close-after-success: %s", c4.Raw)
	}
}

func TestStaleSetupsExpire(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())
	r := e.startSetup(tok, ids.New("key"))
	if err := e.enroll.ExpireStaleSetups(context.Background(), 0, 0, 10); err != nil {
		t.Fatal(err)
	}
	g := e.do(http.MethodGet, "/v1/card-setups/"+r.str("id"), tok, nil, nil)
	if g.str("status") != "canceled" || g.str("failure_code") != "expired" {
		t.Fatalf("expire: %s", g.Raw)
	}
}

func TestWebhookDedupeAndSingleCard(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())
	r := e.startSetup(tok, ids.New("key"))
	ref := e.setupRef(r.str("id"))
	if _, err := e.pv.CompleteSetup(ref, fake.Visa); err != nil {
		t.Fatal(err)
	}

	payload, sig := fake.Event("evt_1", "setup_intent.succeeded", ref)
	bad := e.rawPost("/v1/webhooks/provider", payload, map[string]string{"Stripe-Signature": "nope"})
	expectStatus(t, bad, http.StatusBadRequest)

	// Concurrent duplicate deliveries plus client polls: exactly one card.
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			e.rawPost("/v1/webhooks/provider", payload, map[string]string{"Stripe-Signature": sig})
		}()
		go func() {
			defer wg.Done()
			e.do(http.MethodGet, "/v1/card-setups/"+r.str("id"), tok, nil, nil)
		}()
	}
	wg.Wait()
	dup := e.rawPost("/v1/webhooks/provider", payload, map[string]string{"Stripe-Signature": sig})
	expectStatus(t, dup, http.StatusOK)
	if dup.str("status") != "duplicate" {
		t.Fatalf("expected duplicate, got %s", dup.Raw)
	}
	cards := e.do(http.MethodGet, "/v1/cards", tok, nil, nil).get("cards").([]any)
	if len(cards) != 1 {
		t.Fatalf("expected exactly 1 card, got %d", len(cards))
	}

	// Unknown events are acknowledged without side effects.
	p2, s2 := fake.Event("evt_2", "charge.succeeded", "ch_x")
	expectStatus(t, e.rawPost("/v1/webhooks/provider", p2, map[string]string{"Stripe-Signature": s2}), http.StatusOK)
}

func TestCrossAccountAccessFails(t *testing.T) {
	e := newEnv(t)
	tokA, _ := e.signIn(e.newSubject())
	tokB, _ := e.signIn(e.newSubject())
	setup := e.startSetup(tokA, ids.New("key"))
	cardA := e.addCard(tokA, fake.Visa)

	expectStatus(t, e.do(http.MethodGet, "/v1/card-setups/"+setup.str("id"), tokB, nil, nil), http.StatusNotFound)
	expectStatus(t, e.do(http.MethodPost, "/v1/card-setups/"+setup.str("id")+"/close", tokB, map[string]any{}, nil), http.StatusNotFound)
	expectStatus(t, e.do(http.MethodGet, "/v1/cards/"+cardA, tokB, nil, nil), http.StatusNotFound)
	expectStatus(t, e.do(http.MethodPatch, "/v1/cards/"+cardA, tokB, map[string]any{"nickname": "mine"}, nil), http.StatusNotFound)
	expectStatus(t, e.do(http.MethodDelete, "/v1/cards/"+cardA, tokB, nil, nil), http.StatusNotFound)
	expectStatus(t, e.do(http.MethodPost, "/dev/card-setups/"+setup.str("id")+"/complete", tokB, map[string]any{}, nil), http.StatusNotFound)

	if n := len(e.do(http.MethodGet, "/v1/cards", tokB, nil, nil).get("cards").([]any)); n != 0 {
		t.Fatalf("B sees %d cards", n)
	}
	// A's card is untouched.
	got := e.do(http.MethodGet, "/v1/cards/"+cardA, tokA, nil, nil)
	if got.str("state") != "saved" || got.get("nickname") != nil {
		t.Fatalf("A's card changed: %s", got.Raw)
	}
}

func TestCustomerMismatchIsRejected(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())
	r := e.startSetup(tok, ids.New("key"))
	if err := e.pv.AttachForeignMethod(e.setupRef(r.str("id")), "fcus_someone_else"); err != nil {
		t.Fatal(err)
	}
	g := e.do(http.MethodGet, "/v1/card-setups/"+r.str("id"), tok, nil, nil)
	if g.str("status") != "failed" || g.str("failure_code") != "customer_mismatch" {
		t.Fatalf("mismatch: %s", g.Raw)
	}
	if n := len(e.do(http.MethodGet, "/v1/cards", tok, nil, nil).get("cards").([]any)); n != 0 {
		t.Fatalf("mismatched method was saved")
	}
}

func TestPatchCardNicknameAndPreferred(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())
	c1 := e.addCard(tok, fake.Visa)
	c2 := e.addCard(tok, fake.Card{Brand: "amex", Last4: "0005", ExpMonth: 6, ExpYear: 2029})

	r := e.do(http.MethodPatch, "/v1/cards/"+c1, tok, map[string]any{"nickname": "  Everyday  "}, nil)
	expectStatus(t, r, http.StatusOK)
	if r.str("nickname") != "Everyday" {
		t.Fatalf("nickname %q", r.str("nickname"))
	}
	r = e.do(http.MethodPatch, "/v1/cards/"+c1, tok, map[string]any{"nickname": nil}, nil)
	if r.get("nickname") != nil {
		t.Fatal("nickname should clear")
	}
	expectStatus(t, e.do(http.MethodPatch, "/v1/cards/"+c1, tok, map[string]any{"nickname": strings.Repeat("x", 41)}, nil), http.StatusBadRequest)
	expectStatus(t, e.do(http.MethodPatch, "/v1/cards/"+c1, tok, map[string]any{}, nil), http.StatusBadRequest)
	expectStatus(t, e.do(http.MethodPatch, "/v1/cards/"+c1, tok, map[string]any{"brand": "visa"}, nil), http.StatusBadRequest)

	// Preferred moves atomically between cards.
	r = e.do(http.MethodPatch, "/v1/cards/"+c2, tok, map[string]any{"is_preferred": true}, nil)
	expectStatus(t, r, http.StatusOK)
	cards := e.do(http.MethodGet, "/v1/cards", tok, nil, nil).get("cards").([]any)
	pref := 0
	for _, c := range cards {
		m := c.(map[string]any)
		if m["is_preferred"] == true {
			pref++
			if m["id"] != c2 {
				t.Fatal("wrong preferred card")
			}
		}
	}
	if pref != 1 || cards[0].(map[string]any)["id"] != c2 {
		t.Fatalf("preferred count %d, order %v", pref, cards)
	}
}

func TestRemoveCardPendingUntilProviderConfirms(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())
	c1 := e.addCard(tok, fake.Visa)
	card, _ := e.st.GetCardByID(context.Background(), c1)

	e.pv.Outage = true
	r := e.do(http.MethodDelete, "/v1/cards/"+c1, tok, nil, nil)
	expectStatus(t, r, http.StatusAccepted)
	if r.str("state") != "removal_pending" {
		t.Fatalf("state %s", r.str("state"))
	}
	if !e.pv.MethodAttached(card.PaymentMethodRef) {
		t.Fatal("provider should still hold the card")
	}
	// The pending card remains visible as pending; it is not hidden.
	cards := e.do(http.MethodGet, "/v1/cards", tok, nil, nil).get("cards").([]any)
	if len(cards) != 1 || cards[0].(map[string]any)["state"] != "removal_pending" {
		t.Fatalf("cards: %v", cards)
	}
	// Repeated delete is idempotent.
	expectStatus(t, e.do(http.MethodDelete, "/v1/cards/"+c1, tok, nil, nil), http.StatusAccepted)

	// Worker retries fail while the outage persists.
	e.worker.Tick(context.Background())
	op, err := e.st.ActiveOperation(context.Background(), domain.OpDetachCard, c1)
	if err != nil || op.Attempts < 1 {
		t.Fatalf("expected a failed attempt recorded: %+v %v", op, err)
	}

	e.pv.Outage = false
	e.tickAfterBackoff()
	if e.pv.MethodAttached(card.PaymentMethodRef) {
		t.Fatal("provider should have detached")
	}
	cards = e.do(http.MethodGet, "/v1/cards", tok, nil, nil).get("cards").([]any)
	if len(cards) != 0 {
		t.Fatalf("removed card still listed: %v", cards)
	}
	final := e.do(http.MethodDelete, "/v1/cards/"+c1, tok, nil, nil)
	expectStatus(t, final, http.StatusOK)
	if final.str("state") != "removed" {
		t.Fatalf("state %s", final.str("state"))
	}
	if _, err := e.st.ActiveOperation(context.Background(), domain.OpDetachCard, c1); err == nil {
		t.Fatal("detach operation should be complete")
	}
}

func TestRemoveCardImmediateWhenProviderUp(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())
	c1 := e.addCard(tok, fake.Visa)
	r := e.do(http.MethodDelete, "/v1/cards/"+c1, tok, nil, nil)
	expectStatus(t, r, http.StatusOK)
	if r.str("state") != "removed" {
		t.Fatalf("state %s", r.str("state"))
	}
	// Detached webhook after the fact is harmless.
	card, _ := e.st.GetCardByID(context.Background(), c1)
	p, s := fake.Event("evt_d", "payment_method.detached", card.PaymentMethodRef)
	expectStatus(t, e.rawPost("/v1/webhooks/provider", p, map[string]string{"Stripe-Signature": s}), http.StatusOK)
}

func TestDetachedWebhookCompletesPendingRemoval(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())
	c1 := e.addCard(tok, fake.Visa)
	card, _ := e.st.GetCardByID(context.Background(), c1)
	e.pv.Outage = true
	expectStatus(t, e.do(http.MethodDelete, "/v1/cards/"+c1, tok, nil, nil), http.StatusAccepted)
	e.pv.Outage = false
	// Provider confirms detachment via webhook (e.g. done in the dashboard).
	_ = e.pv.DetachPaymentMethod(context.Background(), card.PaymentMethodRef)
	p, s := fake.Event("evt_d2", "payment_method.detached", card.PaymentMethodRef)
	expectStatus(t, e.rawPost("/v1/webhooks/provider", p, map[string]string{"Stripe-Signature": s}), http.StatusOK)
	got := e.do(http.MethodGet, "/v1/cards/"+c1, tok, nil, nil)
	if got.str("state") != "removed" {
		t.Fatalf("state %s", got.str("state"))
	}
	if _, err := e.st.ActiveOperation(context.Background(), domain.OpDetachCard, c1); err == nil {
		t.Fatal("detach operation should be complete")
	}
}

func TestDeleteAccountRecoverable(t *testing.T) {
	e := newEnv(t)
	subj := e.newSubject()
	tok, userID := e.signIn(subj)
	c1 := e.addCard(tok, fake.Visa)
	card, _ := e.st.GetCardByID(context.Background(), c1)
	pc, _ := e.st.GetProviderCustomer(context.Background(), nil, userID)

	e.pv.Outage = true
	r := e.do(http.MethodDelete, "/v1/account", tok, nil, nil)
	expectStatus(t, r, http.StatusAccepted)
	if r.str("status") != "deleting" {
		t.Fatalf("status %s", r.str("status"))
	}
	// Sessions are revoked immediately, and sign-in is refused while deleting.
	expectStatus(t, e.do(http.MethodGet, "/v1/cards", tok, nil, nil), http.StatusUnauthorized)
	again := e.do(http.MethodPost, "/v1/auth/apple", "", map[string]any{"identity_token": "dev:" + subj}, nil)
	expectStatus(t, again, http.StatusConflict)
	if !e.pv.MethodAttached(card.PaymentMethodRef) {
		t.Fatal("card should still be at provider during outage")
	}

	e.pv.Outage = false
	e.tickAfterBackoff()
	u, err := e.st.GetUser(context.Background(), nil, userID)
	if err != nil || u.Status != domain.UserDeleted || u.Email != nil || u.AppleSubject == "dev-"+subj {
		t.Fatalf("user not scrubbed: %+v %v", u, err)
	}
	if e.pv.MethodAttached(card.PaymentMethodRef) || !e.pv.CustomerDeleted(pc.CustomerRef) {
		t.Fatal("provider cleanup incomplete")
	}
	if _, err := e.st.GetProviderCustomer(context.Background(), nil, userID); err == nil {
		t.Fatal("provider customer mapping should be removed")
	}

	// A later sign-in with the same Apple subject is a brand-new account.
	fresh := e.do(http.MethodPost, "/v1/auth/apple", "", map[string]any{"identity_token": "dev:" + subj}, nil)
	expectStatus(t, fresh, http.StatusCreated)
	if fresh.str("user.id") == userID {
		t.Fatal("deleted account was resurrected")
	}
	if n := len(e.do(http.MethodGet, "/v1/cards", fresh.str("session_token"), nil, nil).get("cards").([]any)); n != 0 {
		t.Fatalf("new account sees %d cards", n)
	}
}

func TestDeleteAccountImmediate(t *testing.T) {
	e := newEnv(t)
	tok, userID := e.signIn(e.newSubject())
	e.addCard(tok, fake.Visa)
	r := e.do(http.MethodDelete, "/v1/account", tok, nil, nil)
	expectStatus(t, r, http.StatusOK)
	if r.str("status") != "deleted" {
		t.Fatalf("status %s", r.str("status"))
	}
	cards, _ := e.st.ListCardsIncludingRemoved(context.Background(), nil, userID)
	for _, c := range cards {
		if c.State != domain.CardRemoved {
			t.Fatalf("card %s state %s", c.ID, c.State)
		}
	}
}

func TestProviderOutageOnSetupStart(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.signIn(e.newSubject())
	e.pv.Outage = true
	r := e.do(http.MethodPost, "/v1/card-setups", tok, map[string]any{"consent": map[string]any{"accepted": true, "version": "2026-10"}},
		map[string]string{"Idempotency-Key": "k-outage"})
	expectStatus(t, r, http.StatusBadGateway)
	e.pv.Outage = false
	// A fresh key succeeds once the provider is back.
	expectStatus(t, e.startSetup(tok, "k-after"), http.StatusCreated)
}
