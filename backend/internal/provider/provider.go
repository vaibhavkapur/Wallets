// Package provider abstracts the payment provider that collects card
// details, holds the credentials, and runs the setup flow. The application
// only ever sees references and masked display fields.
package provider

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors a Provider implementation maps provider responses onto.
var (
	// ErrNotFound: the referenced object does not exist at the provider.
	ErrNotFound = errors.New("provider: not found")
	// ErrAlreadyDetached: the payment method is no longer attached to any
	// customer. Treated as a successful removal.
	ErrAlreadyDetached = errors.New("provider: already detached")
	// ErrUnavailable: transient failure (network, 5xx, rate limit). Retry.
	ErrUnavailable = errors.New("provider: unavailable")
	// ErrInvalidSignature: webhook payload could not be authenticated.
	ErrInvalidSignature = errors.New("provider: invalid webhook signature")
)

// SetupState is the provider-neutral state of a setup.
type SetupState string

const (
	SetupRequiresPaymentMethod SetupState = "requires_payment_method"
	SetupRequiresConfirmation  SetupState = "requires_confirmation"
	SetupRequiresAction        SetupState = "requires_action"
	SetupProcessing            SetupState = "processing"
	SetupSucceeded             SetupState = "succeeded"
	SetupCanceled              SetupState = "canceled"
)

// Setup is a provider setup object (Stripe: SetupIntent).
type Setup struct {
	ID               string
	CustomerRef      string
	PaymentMethodRef string
	State            SetupState
	// ClientSecret is only populated by CreateSetup and GetSetup, and is
	// never persisted or logged.
	ClientSecret string
	// LastErrorCode is the provider's last failure code, if any.
	LastErrorCode string
}

// PaymentMethod is the masked view of a saved card.
type PaymentMethod struct {
	ID          string
	CustomerRef string
	Brand       string
	Last4       string
	ExpMonth    int
	ExpYear     int
}

// ClientSession is what the native SDK needs to present card entry.
type ClientSession struct {
	Setup              Setup
	CustomerRef        string
	EphemeralKeySecret string
	EphemeralKeyExpiry time.Time
	PublishableKey     string
}

// Event is a verified webhook delivery.
type Event struct {
	ID       string
	Type     string
	ObjectID string
	// Object is the raw data.object payload for handlers that need more.
	Object []byte
}

// Provider is the set of operations the application needs.
type Provider interface {
	// Name identifies the provider in stored records (e.g. "stripe").
	Name() string
	// CreateCustomer creates the provider customer for an application user.
	CreateCustomer(ctx context.Context, userID string) (customerRef string, err error)
	// CreateSetup starts a setup for manual card entry under customerRef.
	// idemKey makes repeated calls return the same provider object.
	CreateSetup(ctx context.Context, customerRef, idemKey string) (Setup, error)
	// ClientSession returns short-lived credentials for the SDK. The setup
	// must already belong to customerRef.
	ClientSession(ctx context.Context, customerRef string, setupID string) (ClientSession, error)
	// GetSetup fetches the current setup state.
	GetSetup(ctx context.Context, setupID string) (Setup, error)
	// CancelSetup cancels a setup that has not succeeded.
	CancelSetup(ctx context.Context, setupID string) error
	// GetPaymentMethod fetches the masked card.
	GetPaymentMethod(ctx context.Context, id string) (PaymentMethod, error)
	// DetachPaymentMethod removes the card from its customer. Returns
	// ErrAlreadyDetached if it was already removed.
	DetachPaymentMethod(ctx context.Context, id string) error
	// DeleteCustomer removes the provider customer and its attached methods.
	DeleteCustomer(ctx context.Context, customerRef string) error
	// ParseWebhook authenticates a delivery and returns the event.
	ParseWebhook(payload []byte, signatureHeader string, now time.Time) (Event, error)
}
