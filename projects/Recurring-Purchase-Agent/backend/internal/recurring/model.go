package recurring

import (
	"fmt"
	"time"
)

// RuleStatus governs whether a recurring rule may schedule or execute new occurrences.
type RuleStatus string

const (
	RuleStatusActive   RuleStatus = "active"
	RuleStatusPaused   RuleStatus = "paused"
	RuleStatusCanceled RuleStatus = "canceled"
)

// ApprovalMode tracks build milestones C1 (reminders), C2 (assisted), and C3 (limited delegation).
type ApprovalMode string

const (
	ApprovalModeReminderOnly      ApprovalMode = "C1_REMINDER_ONLY"
	ApprovalModeAssistedReorder   ApprovalMode = "C2_ASSISTED_REORDER"
	ApprovalModeLimitedDelegation ApprovalMode = "C3_LIMITED_DELEGATION"
)

// ScheduleKind strictly separates calendar schedules from minimum elapsed intervals.
type ScheduleKind string

const (
	ScheduleKindCalendar ScheduleKind = "calendar"
	ScheduleKindInterval ScheduleKind = "interval"
)

// CalendarCadence defines monthly or weekly calendar recurrence.
type CalendarCadence string

const (
	CalendarCadenceWeekly  CalendarCadence = "weekly"
	CalendarCadenceMonthly CalendarCadence = "monthly"
)

// DSTPolicy defines explicit behavior for daylight-saving gaps and repeated hours.
type DSTPolicy string

const (
	// DSTPolicyShiftForwardFirst moves spring-forward gaps to the next valid instant
	// after the gap, and picks the first occurrence on fall-back repeated hours.
	DSTPolicyShiftForwardFirst DSTPolicy = "shift_forward_first"
	// DSTPolicyShiftForwardSecond moves spring-forward gaps to the next valid instant
	// after the gap, and picks the second (standard-time) occurrence on fall-back repeated hours.
	DSTPolicyShiftForwardSecond DSTPolicy = "shift_forward_second"
)

// LeapDayPolicy defines how day-of-month 29/30/31 behaves in shorter months or non-leap years.
type LeapDayPolicy string

const (
	LeapDayPolicyClampMonthEnd LeapDayPolicy = "clamp_month_end"
	LeapDayPolicyCarryNextDay  LeapDayPolicy = "carry_march_first"
)

// SubstitutionPolicy controls how out-of-stock or changed-pack substitutions are handled.
type SubstitutionPolicy string

const (
	SubstitutionPolicyNone               SubstitutionPolicy = "none"
	SubstitutionPolicyApprovalRequired   SubstitutionPolicy = "approval_required"
	SubstitutionPolicyPreauthorizedExact SubstitutionPolicy = "preauthorized_exact"
)

// OccurrenceStatus represents the scheduling & execution workflow state of one occurrence.
type OccurrenceStatus string

const (
	OccurrenceScheduled           OccurrenceStatus = "scheduled"
	OccurrenceQuoting             OccurrenceStatus = "quoting"
	OccurrenceAwaitingApproval    OccurrenceStatus = "awaiting_approval"
	OccurrenceApproved            OccurrenceStatus = "approved"
	OccurrenceExecuting           OccurrenceStatus = "executing"
	OccurrenceCompleted           OccurrenceStatus = "completed"
	OccurrenceSkipped             OccurrenceStatus = "skipped"
	OccurrenceExpired             OccurrenceStatus = "expired"
	OccurrenceCanceled            OccurrenceStatus = "canceled"
	OccurrenceFailed              OccurrenceStatus = "failed"
	OccurrenceNeedsReconciliation OccurrenceStatus = "needs_reconciliation"
)

// PaymentStatus models payment lifecycle independently from scheduling and fulfillment.
type PaymentStatus string

const (
	PaymentUninitiated       PaymentStatus = "uninitiated"
	PaymentPending           PaymentStatus = "pending"
	PaymentAuthorized        PaymentStatus = "authorized"
	PaymentSettled           PaymentStatus = "settled"
	PaymentTimeoutUnknown    PaymentStatus = "timeout_unknown"
	PaymentFailed            PaymentStatus = "failed"
	PaymentRefunded          PaymentStatus = "refunded"
	PaymentPartiallyRefunded PaymentStatus = "partially_refunded"
)

// FulfillmentStatus models merchant order delivery independently from payment.
type FulfillmentStatus string

const (
	FulfillmentUnfulfilled FulfillmentStatus = "unfulfilled"
	FulfillmentConfirmed   FulfillmentStatus = "confirmed"
	FulfillmentDelayed     FulfillmentStatus = "delayed"
	FulfillmentShipped     FulfillmentStatus = "shipped"
	FulfillmentDelivered   FulfillmentStatus = "delivered"
	FulfillmentCanceled    FulfillmentStatus = "canceled"
)

// StockStatus represents merchant availability for the exact requested item and quantity.
type StockStatus string

const (
	StockInStock      StockStatus = "in_stock"
	StockPartialStock StockStatus = "partial_stock"
	StockOutOfStock   StockStatus = "out_of_stock"
)

// ProposalStatus tracks an immutable purchase proposal.
type ProposalStatus string

const (
	ProposalPending    ProposalStatus = "pending"
	ProposalApproved   ProposalStatus = "approved"
	ProposalRejected   ProposalStatus = "rejected"
	ProposalExpired    ProposalStatus = "expired"
	ProposalSuperseded ProposalStatus = "superseded"
)

// ConsentKind distinguishes one-time proposal approvals from delegated C3 mandates.
type ConsentKind string

const (
	ConsentKindProposalApproval ConsentKind = "proposal_approval"
	ConsentKindDelegatedMandate ConsentKind = "delegated_mandate"
)

// ReservationStatus tracks atomic budget reservations.
type ReservationStatus string

const (
	ReservationReserved          ReservationStatus = "reserved"
	ReservationSettled           ReservationStatus = "settled"
	ReservationReleased          ReservationStatus = "released"
	ReservationPartiallyRefunded ReservationStatus = "partially_refunded"
	ReservationUncertainHeld     ReservationStatus = "uncertain_held"
)

// ScheduleSpec captures calendar vs minimum-interval rules, IANA timezone, and DST policy.
type ScheduleSpec struct {
	Kind               ScheduleKind    `json:"kind"`
	Timezone           string          `json:"timezone"`
	DSTPolicy          DSTPolicy       `json:"dst_policy"`
	LeapDayPolicy      LeapDayPolicy   `json:"leap_day_policy"`
	LocalHour          int             `json:"local_hour"`
	LocalMinute        int             `json:"local_minute"`
	CalendarCadence    CalendarCadence `json:"calendar_cadence,omitempty"`
	DayOfWeek          *int            `json:"day_of_week,omitempty"`  // 0=Sunday .. 6=Saturday
	DayOfMonth         *int            `json:"day_of_month,omitempty"` // 1..31
	IntervalDays       int             `json:"interval_days,omitempty"`
	MinIntervalDays    int             `json:"min_interval_days"`
	ProposalTTLMinutes int             `json:"proposal_ttl_minutes"`
}

// ProductConstraint enforces exact product, pack-size, quantity, merchant, price, and substitution constraints.
type ProductConstraint struct {
	Category                   string             `json:"category"`
	SKU                        string             `json:"sku"`
	Title                      string             `json:"title"`
	PackSizeValue              int                `json:"pack_size_value"`
	PackSizeUnit               string             `json:"pack_size_unit"`
	Quantity                   int                `json:"quantity"`
	PreferredMerchantID        string             `json:"preferred_merchant_id"`
	MaxDeliveredPrice          Money              `json:"max_delivered_price"`
	MonthlyBudget              Money              `json:"monthly_budget"`
	SubstitutionPolicy         SubstitutionPolicy `json:"substitution_policy"`
	PreauthorizedSubstituteSKU []string           `json:"preauthorized_substitute_skus,omitempty"`
	PaymentMethodID            string             `json:"payment_method_id"`
	ShippingPostalCode         string             `json:"shipping_postal_code"`
}

// RecurringRuleVersion is an immutable snapshot of a recurring rule at a given version.
type RecurringRuleVersion struct {
	RuleID           string            `json:"rule_id"`
	TenantID         string            `json:"tenant_id"`
	Version          int               `json:"version"`
	Name             string            `json:"name"`
	Status           RuleStatus        `json:"status"`
	ApprovalMode     ApprovalMode      `json:"approval_mode"`
	Schedule         ScheduleSpec      `json:"schedule"`
	Product          ProductConstraint `json:"product"`
	NextRunAtUTC     time.Time         `json:"next_run_at_utc"`
	LastCompletedUTC *time.Time        `json:"last_completed_utc,omitempty"`
	SkippedSlotKeys  []string          `json:"skipped_slot_keys,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	PauseReason      string            `json:"pause_reason,omitempty"`
}

// RecurringRule holds the current active pointer and version history for a rule.
type RecurringRule struct {
	RuleID         string                 `json:"rule_id"`
	TenantID       string                 `json:"tenant_id"`
	CurrentVersion int                    `json:"current_version"`
	Current        RecurringRuleVersion   `json:"current"`
	Versions       []RecurringRuleVersion `json:"versions"`
}

// MerchantQuote is an immutable fresh quote from a merchant or ACP/UCP adapter.
type MerchantQuote struct {
	QuoteID            string      `json:"quote_id"`
	OccurrenceID       string      `json:"occurrence_id"`
	MerchantID         string      `json:"merchant_id"`
	MerchantName       string      `json:"merchant_name"`
	RequestedSKU       string      `json:"requested_sku"`
	QuotedSKU          string      `json:"quoted_sku"`
	Title              string      `json:"title"`
	PackSizeValue      int         `json:"pack_size_value"`
	PackSizeUnit       string      `json:"pack_size_unit"`
	Quantity           int         `json:"quantity"`
	AvailableQuantity  int         `json:"available_quantity"`
	StockStatus        StockStatus `json:"stock_status"`
	ItemSubtotal       Money       `json:"item_subtotal"`
	ShippingFee        Money       `json:"shipping_fee"`
	TaxAmount          Money       `json:"tax_amount"`
	DeliveredTotal     Money       `json:"delivered_total"`
	IsSubstitution     bool        `json:"is_substitution"`
	PackSizeChanged    bool        `json:"pack_size_changed"`
	SubstitutionReason string      `json:"substitution_reason,omitempty"`
	QuotedAt           time.Time   `json:"quoted_at"`
	ExpiresAt          time.Time   `json:"expires_at"`
	Digest             string      `json:"digest"`
}

// PurchaseProposal binds an occurrence, rule version, and quote into an immutable review object.
type PurchaseProposal struct {
	ProposalID                   string         `json:"proposal_id"`
	TenantID                     string         `json:"tenant_id"`
	RuleID                       string         `json:"rule_id"`
	RuleVersion                  int            `json:"rule_version"`
	OccurrenceID                 string         `json:"occurrence_id"`
	QuoteID                      string         `json:"quote_id"`
	QuoteDigest                  string         `json:"quote_digest"`
	MerchantID                   string         `json:"merchant_id"`
	MerchantName                 string         `json:"merchant_name"`
	SKU                          string         `json:"sku"`
	Title                        string         `json:"title"`
	PackSizeLabel                string         `json:"pack_size_label"`
	Quantity                     int            `json:"quantity"`
	DeliveredTotal               Money          `json:"delivered_total"`
	PaymentMethodID              string         `json:"payment_method_id"`
	PaymentMethodLabel           string         `json:"payment_method_label"`
	RequiresSubstitutionApproval bool           `json:"requires_substitution_approval"`
	SubstitutionSummary          string         `json:"substitution_summary,omitempty"`
	PolicyWarnings               []string       `json:"policy_warnings,omitempty"`
	PolicyBlocked                bool           `json:"policy_blocked"`
	BlockReason                  string         `json:"block_reason,omitempty"`
	ReminderOnly                 bool           `json:"reminder_only"`
	OutageRecovery               bool           `json:"outage_recovery"`
	MissedSlotsCount             int            `json:"missed_slots_count,omitempty"`
	Status                       ProposalStatus `json:"status"`
	CreatedAt                    time.Time      `json:"created_at"`
	ExpiresAt                    time.Time      `json:"expires_at"`
	DecidedAt                    *time.Time     `json:"decided_at,omitempty"`
}

// ConsentRecord records immutable customer approval or a C3 delegated mandate.
type ConsentRecord struct {
	ConsentID          string      `json:"consent_id"`
	TenantID           string      `json:"tenant_id"`
	Kind               ConsentKind `json:"kind"`
	RuleID             string      `json:"rule_id"`
	RuleVersion        int         `json:"rule_version"`
	ProposalID         string      `json:"proposal_id,omitempty"`
	QuoteDigest        string      `json:"quote_digest,omitempty"`
	MerchantID         string      `json:"merchant_id"`
	SKU                string      `json:"sku"`
	MaxAmount          Money       `json:"max_amount"`
	PaymentMethodID    string      `json:"payment_method_id"`
	ProviderMandateRef string      `json:"provider_mandate_ref,omitempty"`
	GrantedBy          string      `json:"granted_by"`
	GrantedAt          time.Time   `json:"granted_at"`
	ExpiresAt          time.Time   `json:"expires_at"`
	RevokedAt          *time.Time  `json:"revoked_at,omitempty"`
	SignatureDigest    string      `json:"signature_digest"`
}

// BudgetReservation atomically reserves monthly spend including fees.
type BudgetReservation struct {
	ReservationID string            `json:"reservation_id"`
	TenantID      string            `json:"tenant_id"`
	RuleID        string            `json:"rule_id"`
	OccurrenceID  string            `json:"occurrence_id"`
	BudgetPeriod  string            `json:"budget_period"` // YYYY-MM in rule timezone
	Reserved      Money             `json:"reserved"`
	Settled       Money             `json:"settled"`
	Refunded      Money             `json:"refunded"`
	Status        ReservationStatus `json:"status"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// CheckoutReference links an occurrence to its checkout handoff, payment, and fulfillment records.
type CheckoutReference struct {
	CheckoutID        string            `json:"checkout_id"`
	OccurrenceID      string            `json:"occurrence_id"`
	IdempotencyKey    string            `json:"idempotency_key"`
	Provider          string            `json:"provider"` // sandbox_direct | agent_checkout_http
	MerchantID        string            `json:"merchant_id"`
	MerchantOrderID   string            `json:"merchant_order_id,omitempty"`
	PaymentIntentID   string            `json:"payment_intent_id,omitempty"`
	PaymentMethodID   string            `json:"payment_method_id"`
	PaymentStatus     PaymentStatus     `json:"payment_status"`
	FulfillmentStatus FulfillmentStatus `json:"fulfillment_status"`
	ChargedAmount     Money             `json:"charged_amount"`
	RefundedAmount    Money             `json:"refunded_amount"`
	CancelRequested   bool              `json:"cancel_requested"`
	CancelOutcome     string            `json:"cancel_outcome,omitempty"`
	LastError         string            `json:"last_error,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	LastReconciledAt  *time.Time        `json:"last_reconciled_at,omitempty"`
}

// ScheduleOccurrence represents one unique logical schedule slot for a recurring rule.
type ScheduleOccurrence struct {
	OccurrenceID           string            `json:"occurrence_id"`
	TenantID               string            `json:"tenant_id"`
	RuleID                 string            `json:"rule_id"`
	RuleVersion            int               `json:"rule_version"`
	SlotKey                string            `json:"slot_key"` // deterministic unique logical key
	IdempotencyKey         string            `json:"idempotency_key"`
	ScheduledForUTC        time.Time         `json:"scheduled_for_utc"`
	ScheduledLocalDisplay  string            `json:"scheduled_local_display"`
	Status                 OccurrenceStatus  `json:"status"`
	PaymentStatus          PaymentStatus     `json:"payment_status"`
	FulfillmentStatus      FulfillmentStatus `json:"fulfillment_status"`
	ApprovalMode           ApprovalMode      `json:"approval_mode"`
	OutageRecovery         bool              `json:"outage_recovery"`
	MissedSlotsCount       int               `json:"missed_slots_count,omitempty"`
	PauseRacedSubmission   bool              `json:"pause_raced_submission,omitempty"`
	QuoteID                string            `json:"quote_id,omitempty"`
	ProposalID             string            `json:"proposal_id,omitempty"`
	ConsentID              string            `json:"consent_id,omitempty"`
	ReservationID          string            `json:"reservation_id,omitempty"`
	CheckoutID             string            `json:"checkout_id,omitempty"`
	StatusReason           string            `json:"status_reason,omitempty"`
	CreatedAt              time.Time         `json:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at"`
}

// NotificationEvent is a durable transactional outbox item for customer reminders and alerts.
type NotificationEvent struct {
	EventID      string     `json:"event_id"`
	TenantID     string     `json:"tenant_id"`
	RuleID       string     `json:"rule_id"`
	OccurrenceID string     `json:"occurrence_id,omitempty"`
	Kind         string     `json:"kind"`
	Channel      string     `json:"channel"`
	Status       string     `json:"status"` // pending | dispatched | failed
	Message      string     `json:"message"`
	Attempts     int        `json:"attempts"`
	CreatedAt    time.Time  `json:"created_at"`
	DispatchedAt *time.Time `json:"dispatched_at,omitempty"`
}

// WorkerJob represents a database-backed durable job with leases and bounded retries.
type WorkerJob struct {
	JobID         string     `json:"job_id"`
	TenantID      string     `json:"tenant_id"`
	Kind          string     `json:"kind"` // quote_occurrence | execute_occurrence | reconcile_checkout | dispatch_notification
	DedupeKey     string     `json:"dedupe_key"`
	ResourceID    string     `json:"resource_id"`
	Status        string     `json:"status"` // pending | leased | done | dead
	LeaseOwner    string     `json:"lease_owner,omitempty"`
	LeaseUntil    *time.Time `json:"lease_until,omitempty"`
	Attempts      int        `json:"attempts"`
	MaxAttempts   int        `json:"max_attempts"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// AuditEvent captures immutable audit evidence for every state transition and policy decision.
type AuditEvent struct {
	AuditID      string            `json:"audit_id"`
	TenantID     string            `json:"tenant_id"`
	Actor        string            `json:"actor"`
	Action       string            `json:"action"`
	ResourceType string            `json:"resource_type"`
	ResourceID   string            `json:"resource_id"`
	Detail       map[string]string `json:"detail,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

// GlobalPauseState is the product-level emergency pause control managed by operators.
type GlobalPauseState struct {
	Paused    bool      `json:"paused"`
	Reason    string    `json:"reason,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// SandboxFaultConfig enables deterministic testing and interactive demos of edge cases.
type SandboxFaultConfig struct {
	PriceMultiplierBps   int    `json:"price_multiplier_bps"` // 10000 = 1.00x; 13000 = +30% price spike
	ForcePackSizeValue   int    `json:"force_pack_size_value,omitempty"`
	ForceStockStatus     string `json:"force_stock_status,omitempty"` // "", "partial_stock", "out_of_stock"
	ForceAvailableQty    int    `json:"force_available_qty,omitempty"`
	ForceSubstituteSKU   string `json:"force_substitute_sku,omitempty"`
	ForcePaymentBehavior string `json:"force_payment_behavior,omitempty"` // "", "timeout_settled", "timeout_failed", "decline", "delayed_fulfillment"
	EnableC3Delegation   bool   `json:"enable_c3_delegation"`
}

// TenantState is the durable tenant-isolated aggregate state.
type TenantState struct {
	TenantID      string                        `json:"tenant_id"`
	Revision      int64                         `json:"revision"`
	Rules         map[string]*RecurringRule     `json:"rules"`
	Occurrences   map[string]*ScheduleOccurrence `json:"occurrences"`
	SlotIndex     map[string]string             `json:"slot_index"` // ruleID|slotKey -> occurrenceID
	Quotes        map[string]*MerchantQuote     `json:"quotes"`
	Proposals     map[string]*PurchaseProposal  `json:"proposals"`
	Consents      map[string]*ConsentRecord     `json:"consents"`
	Reservations  map[string]*BudgetReservation `json:"reservations"`
	Checkouts     map[string]*CheckoutReference `json:"checkouts"`
	Notifications []*NotificationEvent          `json:"notifications"`
	Jobs          map[string]*WorkerJob         `json:"jobs"`
	JobDedupe     map[string]string             `json:"job_dedupe"` // kind|dedupeKey -> jobID
	Audits        []*AuditEvent                 `json:"audits"`
	Faults        SandboxFaultConfig            `json:"faults"`
}

func newTenantState(tenantID string) *TenantState {
	return &TenantState{
		TenantID:      tenantID,
		Rules:         map[string]*RecurringRule{},
		Occurrences:   map[string]*ScheduleOccurrence{},
		SlotIndex:     map[string]string{},
		Quotes:        map[string]*MerchantQuote{},
		Proposals:     map[string]*PurchaseProposal{},
		Consents:      map[string]*ConsentRecord{},
		Reservations:  map[string]*BudgetReservation{},
		Checkouts:     map[string]*CheckoutReference{},
		Notifications: []*NotificationEvent{},
		Jobs:          map[string]*WorkerJob{},
		JobDedupe:     map[string]string{},
		Audits:        []*AuditEvent{},
		Faults: SandboxFaultConfig{
			PriceMultiplierBps: 10000,
		},
	}
}

// OccurrenceDetail bundles an occurrence with its linked quote, proposal, consent, reservation, and checkout.
type OccurrenceDetail struct {
	Occurrence  *ScheduleOccurrence `json:"occurrence"`
	Quote       *MerchantQuote      `json:"quote,omitempty"`
	Proposal    *PurchaseProposal   `json:"proposal,omitempty"`
	Consent     *ConsentRecord      `json:"consent,omitempty"`
	Reservation *BudgetReservation  `json:"reservation,omitempty"`
	Checkout    *CheckoutReference  `json:"checkout,omitempty"`
}

// Problem is a structured HTTP error response.
type Problem struct {
	Status  int
	Code    string
	Message string
}

func (p *Problem) Error() string {
	return fmt.Sprintf("%s: %s", p.Code, p.Message)
}

func problem(status int, code, msg string) *Problem {
	return &Problem{Status: status, Code: code, Message: msg}
}

// CatalogItem describes a canonical product in our narrow coffee & household essentials catalog.
type CatalogItem struct {
	SKU                   string            `json:"sku"`
	Category              string            `json:"category"`
	Brand                 string            `json:"brand"`
	Title                 string            `json:"title"`
	PackSizeValue         int               `json:"pack_size_value"`
	PackSizeUnit          string            `json:"pack_size_unit"`
	DefaultUnitPrice      Money             `json:"default_unit_price"`
	Merchants             []MerchantListing `json:"merchants"`
	SuggestedSubstitutes  []string          `json:"suggested_substitutes,omitempty"`
}

// MerchantListing links a catalog SKU to a supported merchant with baseline delivery fee & tax rate.
type MerchantListing struct {
	MerchantID   string `json:"merchant_id"`
	MerchantName string `json:"merchant_name"`
	UnitPrice    Money  `json:"unit_price"`
	ShippingFee  Money  `json:"shipping_fee"`
	TaxRateBps   int    `json:"tax_rate_bps"`
	Protocol     string `json:"protocol"` // ACP | UCP
}

// PaymentMethod represents a scoped payment instrument (never raw card PAN/CVC or wallet private keys).
type PaymentMethod struct {
	ID                 string `json:"id"`
	Label              string `json:"label"`
	Network            string `json:"network"`
	Last4              string `json:"last4"`
	Rail               string `json:"rail"` // card | stablecoin
	ScopedCredential   bool   `json:"scoped_credential"`
	SupportsDelegation bool   `json:"supports_delegation"`
}
