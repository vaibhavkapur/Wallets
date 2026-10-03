// Package domain holds the application records shared by the store, the
// enrollment service, and the HTTP API. None of these types ever carry a
// full card number or CVV; the provider holds the credentials and we keep
// references plus masked display fields.
package domain

import "time"

// UserStatus is the account lifecycle.
type UserStatus string

const (
	UserActive   UserStatus = "active"
	UserDeleting UserStatus = "deleting"
	UserDeleted  UserStatus = "deleted"
)

// User is an account established through Sign in with Apple.
type User struct {
	ID           string
	AppleSubject string
	Email        *string
	Status       UserStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}

// Session is an opaque bearer credential; only its hash is stored.
type Session struct {
	ID        string
	UserID    string
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// ProviderCustomer maps a user to the provider's customer object.
type ProviderCustomer struct {
	UserID      string
	Provider    string
	CustomerRef string
	CreatedAt   time.Time
}

// ConsentRecord records the user's agreement to save a card for a purpose.
type ConsentRecord struct {
	ID             string
	UserID         string
	Purpose        string
	ConsentVersion string
	CreatedAt      time.Time
}

// CardState is the lifecycle of a saved card reference.
type CardState string

const (
	CardSaved          CardState = "saved"
	CardRemovalPending CardState = "removal_pending"
	CardRemoved        CardState = "removed"
)

// SavedCard is a provider payment-method reference plus display metadata.
type SavedCard struct {
	ID               string
	UserID           string
	Provider         string
	PaymentMethodRef string
	Brand            string
	Last4            string
	ExpMonth         int
	ExpYear          int
	Nickname         *string
	IsPreferred      bool
	State            CardState
	CreatedAt        time.Time
	UpdatedAt        time.Time
	RemovedAt        *time.Time
}

// SetupStatus tracks one enrollment attempt.
type SetupStatus string

const (
	SetupCollecting             SetupStatus = "collecting"
	SetupAuthenticationRequired SetupStatus = "authentication_required"
	SetupProcessing             SetupStatus = "processing"
	SetupSaved                  SetupStatus = "saved"
	SetupFailed                 SetupStatus = "failed"
	SetupCanceled               SetupStatus = "canceled"
)

// Terminal reports whether the attempt can no longer change.
func (s SetupStatus) Terminal() bool {
	return s == SetupSaved || s == SetupFailed || s == SetupCanceled
}

// SetupAttempt is one attempt to save a card.
type SetupAttempt struct {
	ID             string
	UserID         string
	IdempotencyKey string
	ConsentID      string
	Provider       string
	SetupRef       *string
	Status         SetupStatus
	FailureCode    *string
	SavedCardID    *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// OperationKind names durable retryable work.
type OperationKind string

const (
	OpDetachCard     OperationKind = "detach_card"
	OpDeleteAccount  OperationKind = "delete_account"
	OpReconcileSetup OperationKind = "reconcile_setup"
)

// PendingOperation is a unit of work the retry worker drives to completion.
type PendingOperation struct {
	ID            string
	Kind          OperationKind
	SubjectID     string
	Attempts      int
	NextAttemptAt time.Time
	LastError     *string
	CreatedAt     time.Time
	CompletedAt   *time.Time
}
