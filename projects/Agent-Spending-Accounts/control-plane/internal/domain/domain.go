// Package domain defines the core records of the control plane. These are the
// authoritative shapes stored in PostgreSQL and exposed (in part) through the API.
package domain

import (
	"encoding/json"
	"time"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/money"
)

// --- Identity ---------------------------------------------------------------

type OrgStatus string

const (
	OrgActive OrgStatus = "active"
	OrgPaused OrgStatus = "paused" // owner or operator pause: no new spending
)

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    OrgStatus `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OwnerKey is an owner API credential. Only the SHA-256 hash is stored.
type OwnerKey struct {
	ID        string     `json:"id"`
	OrgID     string     `json:"organization_id"`
	KeyHash   string     `json:"-"`
	Label     string     `json:"label"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

type AgentStatus string

const (
	AgentActive  AgentStatus = "active"
	AgentRevoked AgentStatus = "revoked"
)

// AgentPrincipal is a software agent identity acting under an owner's policy.
type AgentPrincipal struct {
	ID              string      `json:"id"`
	OrgID           string      `json:"organization_id"`
	Name            string      `json:"name"`
	Status          AgentStatus `json:"status"`
	KeyHash         string      `json:"-"`
	PolicyVersionID string      `json:"policy_version_id,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	RevokedAt       *time.Time  `json:"revoked_at,omitempty"`
}

// --- Policy -----------------------------------------------------------------

// AssetSpec pins the single asset/network/scheme a policy permits.
type AssetSpec struct {
	Asset    string `json:"asset"`    // token contract address or symbol as used by the scheme
	Network  string `json:"network"`  // e.g. "base-sepolia"
	Decimals int    `json:"decimals"` // for display only; never used in authorization math
	Scheme   string `json:"scheme"`   // e.g. "exact"
}

// EndpointRule allowlists a resource endpoint and the beneficiaries it may be
// paid. A model cannot add recipients; only the owner can via a new policy version.
type EndpointRule struct {
	// URLPrefix must be an absolute https URL prefix (scheme + host[:port] [+ path prefix]).
	URLPrefix string `json:"url_prefix"`
	// Methods allowed, uppercase. Empty means GET only.
	Methods []string `json:"methods,omitempty"`
	// Recipients are the approved payTo addresses (compared case-insensitively).
	Recipients []string `json:"recipients"`
}

// BudgetPolicyVersion is immutable once created. Reservations, grants and
// receipts reference the exact version that authorized them.
type BudgetPolicyVersion struct {
	ID            string         `json:"id"`
	OrgID         string         `json:"organization_id"`
	AgentID       string         `json:"agent_id"`
	Version       int            `json:"version"`
	Asset         AssetSpec      `json:"asset"`
	PerRequestMax money.Amount   `json:"per_request_max"`
	PerTaskMax    money.Amount   `json:"per_task_max"`
	AgentDailyMax money.Amount   `json:"agent_daily_max"`
	OrgDailyMax   money.Amount   `json:"org_daily_max"`
	FeeAllowance  money.Amount   `json:"fee_allowance"` // reserved per request on top of the quoted amount
	Endpoints     []EndpointRule `json:"endpoints"`
	// MaxTaskDuration bounds task deadlines set by agents.
	MaxTaskDuration time.Duration `json:"-"`
	MaxTaskSeconds  int64         `json:"max_task_seconds"`
	// GrantTTL bounds the lifetime of execution grants (seconds).
	GrantTTLSeconds int64     `json:"grant_ttl_seconds"`
	CreatedAt       time.Time `json:"created_at"`
}

// --- Tasks ------------------------------------------------------------------

type TaskStatus string

const (
	TaskActive TaskStatus = "active"
	TaskClosed TaskStatus = "closed"
)

type Task struct {
	ID              string       `json:"id"`
	OrgID           string       `json:"organization_id"`
	AgentID         string       `json:"agent_id"`
	PolicyVersionID string       `json:"policy_version_id"`
	Description     string       `json:"description"`
	Status          TaskStatus   `json:"status"`
	Budget          money.Amount `json:"budget"` // agent-requested, capped by PerTaskMax
	Deadline        time.Time    `json:"deadline"`
	CreatedAt       time.Time    `json:"created_at"`
	ClosedAt        *time.Time   `json:"closed_at,omitempty"`
}

// --- Funds ------------------------------------------------------------------

// AccountBalance is the denormalized owner-funded balance per org and asset.
// It is updated only inside the same transaction as the ledger entries.
type AccountBalance struct {
	OrgID     string       `json:"organization_id"`
	Asset     string       `json:"asset"`
	Network   string       `json:"network"`
	Available money.Amount `json:"available"` // spendable, excludes Reserved
	Reserved  money.Amount `json:"reserved"`  // held by open reservations
	UpdatedAt time.Time    `json:"updated_at"`
}

// --- Reservation & grant ----------------------------------------------------

type ReservationStatus string

const (
	ReservationHeld      ReservationStatus = "held"
	ReservationCommitted ReservationStatus = "committed"
	ReservationReleased  ReservationStatus = "released"
)

// Reservation is the atomic hold across task, agent-daily, org-daily and
// balance budgets. Amount is the maximum authorized cost; FeeAllowance is
// held in addition.
type Reservation struct {
	ID               string            `json:"id"`
	OrgID            string            `json:"organization_id"`
	AgentID          string            `json:"agent_id"`
	TaskID           string            `json:"task_id"`
	PolicyVersionID  string            `json:"policy_version_id"`
	PaymentAttemptID string            `json:"payment_attempt_id"`
	Amount           money.Amount      `json:"amount"`
	FeeAllowance     money.Amount      `json:"fee_allowance"`
	Status           ReservationStatus `json:"status"`
	CommittedAmount  money.Amount      `json:"committed_amount"`
	CommittedFee     money.Amount      `json:"committed_fee"`
	ExpiresAt        time.Time         `json:"expires_at"`
	CreatedAt        time.Time         `json:"created_at"`
	ResolvedAt       *time.Time        `json:"resolved_at,omitempty"`
	ResolutionReason string            `json:"resolution_reason,omitempty"`
}

// ExecutionGrant records issuance and consumption of a single-use grant. The
// signed token itself is never persisted.
type ExecutionGrant struct {
	ID               string     `json:"id"` // jti
	ReservationID    string     `json:"reservation_id"`
	PaymentAttemptID string     `json:"payment_attempt_id"`
	ExpiresAt        time.Time  `json:"expires_at"`
	IssuedAt         time.Time  `json:"issued_at"`
	ConsumedAt       *time.Time `json:"consumed_at,omitempty"`
}

// --- Payment ----------------------------------------------------------------

// ResourceQuote is the payment requirement the resource server presented
// (x402 "accepts" entry) that was selected and validated.
type ResourceQuote struct {
	Scheme            string          `json:"scheme"`
	Network           string          `json:"network"`
	Asset             string          `json:"asset"`
	PayTo             string          `json:"pay_to"`
	MaxAmountRequired money.Amount    `json:"max_amount_required"`
	Resource          string          `json:"resource"`
	Description       string          `json:"description,omitempty"`
	MimeType          string          `json:"mime_type,omitempty"`
	MaxTimeoutSeconds int             `json:"max_timeout_seconds"`
	Extra             json.RawMessage `json:"extra,omitempty"`
	// RequirementsHash is SHA-256 (hex) over the canonical quote fields; the
	// grant binds to it and the gateway re-checks it before signing.
	RequirementsHash string `json:"requirements_hash"`
}

type PaymentStatus string

const (
	PaymentInitiated  PaymentStatus = "initiated"  // begin called, no reservation yet
	PaymentRejected   PaymentStatus = "rejected"   // policy rejected; nothing reserved
	PaymentAuthorized PaymentStatus = "authorized" // reservation held, grant issued, not yet submitted
	PaymentSubmitted  PaymentStatus = "submitted"  // gateway is about to / did send payment (write-ahead)
	PaymentSettled    PaymentStatus = "settled"
	PaymentFailed     PaymentStatus = "failed"  // definitively not paid
	PaymentUnknown    PaymentStatus = "unknown" // submitted, outcome lost; awaiting reconciliation
	PaymentExpired    PaymentStatus = "expired" // authorized grant expired unused
)

type PaymentAttempt struct {
	ID              string         `json:"id"`
	OrgID           string         `json:"organization_id"`
	AgentID         string         `json:"agent_id"`
	TaskID          string         `json:"task_id"`
	PolicyVersionID string         `json:"policy_version_id"`
	ReservationID   string         `json:"reservation_id,omitempty"`
	GrantID         string         `json:"grant_id,omitempty"`
	IdempotencyKey  string         `json:"idempotency_key"`
	Method          string         `json:"method"`
	URL             string         `json:"url"`
	Quote           *ResourceQuote `json:"quote,omitempty"`
	Status          PaymentStatus  `json:"status"`
	RejectReason    string         `json:"reject_reason,omitempty"`
	SubmissionRef   string         `json:"submission_ref,omitempty"` // tx hash / facilitator id
	PaymentHash     string         `json:"payment_hash,omitempty"`   // hash of the X-PAYMENT header
	PayerRef        string         `json:"payer_ref,omitempty"`
	SettledAmount   money.Amount   `json:"settled_amount"`
	FeeActual       money.Amount   `json:"fee_actual"`
	Error           string         `json:"error,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// ServiceOutcome records whether the paid resource delivered a usable result.
// It is deliberately separate from settlement.
type ServiceOutcome struct {
	HTTPStatus  int    `json:"http_status"`
	Delivered   bool   `json:"delivered"`
	ContentType string `json:"content_type,omitempty"`
	BodySHA256  string `json:"body_sha256,omitempty"`
	BodyBytes   int64  `json:"body_bytes"`
	Truncated   bool   `json:"truncated"`
	Error       string `json:"error,omitempty"`
}

// Receipt links task, resource, quote, policy version, payment, and service
// response into a single evidence record.
type Receipt struct {
	ID               string          `json:"id"`
	OrgID            string          `json:"organization_id"`
	TaskID           string          `json:"task_id"`
	AgentID          string          `json:"agent_id"`
	PaymentAttemptID string          `json:"payment_attempt_id"`
	ReservationID    string          `json:"reservation_id"`
	PolicyVersionID  string          `json:"policy_version_id"`
	Resource         string          `json:"resource"`
	Quote            ResourceQuote   `json:"quote"`
	PaymentStatus    PaymentStatus   `json:"payment_status"`
	SubmissionRef    string          `json:"submission_ref,omitempty"`
	SettledAmount    money.Amount    `json:"settled_amount"`
	FeeActual        money.Amount    `json:"fee_actual"`
	Service          *ServiceOutcome `json:"service,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

// --- Ledger & audit ---------------------------------------------------------

type EntryDirection string

const (
	Debit  EntryDirection = "debit"
	Credit EntryDirection = "credit"
)

// LedgerEntry is one leg of a balanced journal. Account names:
//
//	funding:external            owner deposits/withdrawals counterparty
//	org:<id>:available          spendable customer funds (liability)
//	org:<id>:reserved           funds held by open reservations
//	settlement:<network>        funds paid out to resource beneficiaries
type LedgerEntry struct {
	ID        string         `json:"id"`
	OrgID     string         `json:"organization_id"`
	JournalID string         `json:"journal_id"`
	Account   string         `json:"account"`
	Direction EntryDirection `json:"direction"`
	Amount    money.Amount   `json:"amount"`
	Asset     string         `json:"asset"`
	Network   string         `json:"network"`
	Memo      string         `json:"memo"`
	CreatedAt time.Time      `json:"created_at"`
}

type AuditEvent struct {
	ID          string          `json:"id"`
	OrgID       string          `json:"organization_id"`
	Actor       string          `json:"actor"` // owner:<id>, agent:<id>, gateway, worker, operator
	Action      string          `json:"action"`
	SubjectType string          `json:"subject_type"`
	SubjectID   string          `json:"subject_id"`
	Details     json.RawMessage `json:"details,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// OutboxMessage is written in the same transaction as the state change it
// describes and published asynchronously by a worker.
type OutboxMessage struct {
	ID          string          `json:"id"`
	Topic       string          `json:"topic"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   time.Time       `json:"created_at"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
}
