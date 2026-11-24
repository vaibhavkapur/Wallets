// Package fake is an in-memory provider for tests and local development.
// It is explicitly labeled as a fixture: nothing it returns is a real card.
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"mywallet/backend/internal/ids"
	"mywallet/backend/internal/provider"
)

// WebhookSecret is the shared secret the fake expects in the signature header.
const WebhookSecret = "fake-webhook-secret"

// Card is a fixture card the test or developer "enters" into the sheet.
type Card struct {
	Brand    string
	Last4    string
	ExpMonth int
	ExpYear  int
}

// Visa is the default fixture card.
var Visa = Card{Brand: "visa", Last4: "4242", ExpMonth: 12, ExpYear: 2030}

type customer struct {
	id      string
	deleted bool
}

type setup struct {
	provider.Setup
}

type paymentMethod struct {
	provider.PaymentMethod
}

// Provider is the fake. All methods are safe for concurrent use.
type Provider struct {
	mu        sync.Mutex
	customers map[string]*customer
	setups    map[string]*setup
	methods   map[string]*paymentMethod
	idem      map[string]string
	calls     map[string]int

	// Outage, when set, makes every mutating provider call return
	// provider.ErrUnavailable. Reads keep working.
	Outage bool
	// RejectCustomerCreate simulates a provider rejecting new customers.
	RejectCustomerCreate bool
}

// New returns an empty fake.
func New() *Provider {
	return &Provider{customers: map[string]*customer{}, setups: map[string]*setup{}, methods: map[string]*paymentMethod{},
		idem: map[string]string{}, calls: map[string]int{}}
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return "fake" }

// Calls returns how many times an operation ran (for tests).
func (p *Provider) Calls(op string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[op]
}

func (p *Provider) count(op string) { p.calls[op]++ }

// CreateCustomer implements provider.Provider.
func (p *Provider) CreateCustomer(ctx context.Context, userID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("create_customer")
	if p.Outage {
		return "", provider.ErrUnavailable
	}
	if p.RejectCustomerCreate {
		return "", errors.New("fake: customer creation rejected")
	}
	id := ids.New("fcus")
	p.customers[id] = &customer{id: id}
	return id, nil
}

// CreateSetup implements provider.Provider.
func (p *Provider) CreateSetup(ctx context.Context, customerRef, idemKey string) (provider.Setup, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("create_setup")
	if p.Outage {
		return provider.Setup{}, provider.ErrUnavailable
	}
	if existing, ok := p.idem[idemKey]; ok {
		return p.setups[existing].Setup, nil
	}
	c, ok := p.customers[customerRef]
	if !ok || c.deleted {
		return provider.Setup{}, provider.ErrNotFound
	}
	s := &setup{provider.Setup{ID: ids.New("fseti"), CustomerRef: customerRef, State: provider.SetupRequiresPaymentMethod}}
	s.ClientSecret = s.ID + "_secret_" + ids.New("x")
	p.setups[s.ID] = s
	p.idem[idemKey] = s.ID
	return s.Setup, nil
}

// ClientSession implements provider.Provider.
func (p *Provider) ClientSession(ctx context.Context, customerRef, setupID string) (provider.ClientSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("client_session")
	if p.Outage {
		return provider.ClientSession{}, provider.ErrUnavailable
	}
	s, ok := p.setups[setupID]
	if !ok {
		return provider.ClientSession{}, provider.ErrNotFound
	}
	if s.CustomerRef != customerRef {
		return provider.ClientSession{}, fmt.Errorf("fake: setup does not belong to customer")
	}
	return provider.ClientSession{Setup: s.Setup, CustomerRef: customerRef, EphemeralKeySecret: "fek_" + ids.New("k"),
		EphemeralKeyExpiry: time.Now().Add(time.Hour).UTC(), PublishableKey: "pk_test_fixture"}, nil
}

// GetSetup implements provider.Provider.
func (p *Provider) GetSetup(ctx context.Context, setupID string) (provider.Setup, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("get_setup")
	s, ok := p.setups[setupID]
	if !ok {
		return provider.Setup{}, provider.ErrNotFound
	}
	return s.Setup, nil
}

// CancelSetup implements provider.Provider.
func (p *Provider) CancelSetup(ctx context.Context, setupID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("cancel_setup")
	if p.Outage {
		return provider.ErrUnavailable
	}
	s, ok := p.setups[setupID]
	if !ok {
		return provider.ErrNotFound
	}
	if s.State != provider.SetupSucceeded && s.State != provider.SetupCanceled {
		s.State = provider.SetupCanceled
	}
	return nil
}

// GetPaymentMethod implements provider.Provider.
func (p *Provider) GetPaymentMethod(ctx context.Context, id string) (provider.PaymentMethod, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("get_payment_method")
	pm, ok := p.methods[id]
	if !ok {
		return provider.PaymentMethod{}, provider.ErrNotFound
	}
	return pm.PaymentMethod, nil
}

// DetachPaymentMethod implements provider.Provider.
func (p *Provider) DetachPaymentMethod(ctx context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("detach")
	if p.Outage {
		return provider.ErrUnavailable
	}
	pm, ok := p.methods[id]
	if !ok {
		return provider.ErrAlreadyDetached
	}
	if pm.CustomerRef == "" {
		return provider.ErrAlreadyDetached
	}
	pm.CustomerRef = ""
	return nil
}

// DeleteCustomer implements provider.Provider.
func (p *Provider) DeleteCustomer(ctx context.Context, customerRef string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("delete_customer")
	if p.Outage {
		return provider.ErrUnavailable
	}
	c, ok := p.customers[customerRef]
	if !ok {
		return nil
	}
	c.deleted = true
	for _, pm := range p.methods {
		if pm.CustomerRef == customerRef {
			pm.CustomerRef = ""
		}
	}
	return nil
}

// ParseWebhook implements provider.Provider. The header must equal
// WebhookSecret; the payload is {"id","type","data":{"object":{"id"}}}.
func (p *Provider) ParseWebhook(payload []byte, header string, now time.Time) (provider.Event, error) {
	if header != WebhookSecret {
		return provider.Event{}, provider.ErrInvalidSignature
	}
	var ev struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return provider.Event{}, err
	}
	var obj struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(ev.Data.Object, &obj)
	return provider.Event{ID: ev.ID, Type: ev.Type, ObjectID: obj.ID, Object: ev.Data.Object}, nil
}

// --- simulation controls ----------------------------------------------------

// CompleteSetup simulates the cardholder finishing the native sheet with a
// fixture card. The payment method is attached to the setup's customer.
func (p *Provider) CompleteSetup(setupID string, card Card) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.setups[setupID]
	if !ok {
		return "", provider.ErrNotFound
	}
	if s.State == provider.SetupCanceled {
		return "", errors.New("fake: setup is canceled")
	}
	pm := &paymentMethod{provider.PaymentMethod{ID: ids.New("fpm"), CustomerRef: s.CustomerRef,
		Brand: card.Brand, Last4: card.Last4, ExpMonth: card.ExpMonth, ExpYear: card.ExpYear}}
	p.methods[pm.ID] = pm
	s.PaymentMethodRef = pm.ID
	s.State = provider.SetupSucceeded
	s.LastErrorCode = ""
	return pm.ID, nil
}

// SetSetupState forces a provider state, e.g. requires_action or processing.
func (p *Provider) SetSetupState(setupID string, state provider.SetupState, errCode string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.setups[setupID]
	if !ok {
		return provider.ErrNotFound
	}
	s.State = state
	s.LastErrorCode = errCode
	return nil
}

// AttachForeignMethod creates a payment method on a different customer and
// points the setup at it, simulating a tampered or mismatched result.
func (p *Provider) AttachForeignMethod(setupID string, otherCustomerRef string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.setups[setupID]
	if !ok {
		return provider.ErrNotFound
	}
	pm := &paymentMethod{provider.PaymentMethod{ID: ids.New("fpm"), CustomerRef: otherCustomerRef, Brand: "visa", Last4: "0000", ExpMonth: 1, ExpYear: 2031}}
	p.methods[pm.ID] = pm
	s.PaymentMethodRef = pm.ID
	s.State = provider.SetupSucceeded
	return nil
}

// MethodAttached reports whether a payment method is still attached.
func (p *Provider) MethodAttached(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	pm, ok := p.methods[id]
	return ok && pm.CustomerRef != ""
}

// CustomerDeleted reports whether a customer has been deleted.
func (p *Provider) CustomerDeleted(ref string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.customers[ref]
	return ok && c.deleted
}

// Hooks adapts the fake to the API's development-only fixture endpoints.
type Hooks struct{ P *Provider }

// CompleteSetup implements httpapi.DevHooks.
func (h Hooks) CompleteSetup(setupID, brand, last4 string, expMonth, expYear int) (string, error) {
	return h.P.CompleteSetup(setupID, Card{Brand: brand, Last4: last4, ExpMonth: expMonth, ExpYear: expYear})
}

// SetSetupOutcome implements httpapi.DevHooks.
func (h Hooks) SetSetupOutcome(setupID, outcome, code string) error {
	switch outcome {
	case "requires_action":
		return h.P.SetSetupState(setupID, provider.SetupRequiresAction, "")
	case "processing":
		return h.P.SetSetupState(setupID, provider.SetupProcessing, "")
	case "declined":
		if code == "" {
			code = "card_declined"
		}
		return h.P.SetSetupState(setupID, provider.SetupRequiresPaymentMethod, code)
	case "canceled":
		return h.P.SetSetupState(setupID, provider.SetupCanceled, "")
	default:
		return fmt.Errorf("fake: unknown outcome %q", outcome)
	}
}

// Event builds a signed fake webhook delivery.
func Event(id, typ, objectID string) (payload []byte, header string) {
	b, _ := json.Marshal(map[string]any{"id": id, "type": typ, "data": map[string]any{"object": map[string]any{"id": objectID}}})
	return b, WebhookSecret
}
