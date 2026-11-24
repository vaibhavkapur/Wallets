package rewards

import (
	"time"

	"unowallet/rewards/internal/money"
)

// Reward lifecycle states from plan.md.
type RewardState string

const (
	RewardPending         RewardState = "pending"
	RewardEligible        RewardState = "eligible"
	RewardPayoutRequested RewardState = "payout_requested"
	RewardPaid            RewardState = "paid"
	RewardReversed        RewardState = "reversed"
)

// Payout attempt states tracked separately from reward states per plan.md.
type PayoutStatus string

const (
	PayoutPrepared       PayoutStatus = "prepared"
	PayoutSubmitted      PayoutStatus = "submitted"
	PayoutPending        PayoutStatus = "pending"
	PayoutSucceeded      PayoutStatus = "succeeded"
	PayoutFailed         PayoutStatus = "failed"
	PayoutOutcomeUnknown PayoutStatus = "outcome_unknown"
)

type CampaignStatus string

const (
	CampaignDraft  CampaignStatus = "draft"
	CampaignActive CampaignStatus = "active"
	CampaignPaused CampaignStatus = "paused"
	CampaignEnded  CampaignStatus = "ended"
)

type RewardType string

const (
	RewardTypeFixed      RewardType = "fixed"
	RewardTypePercentage RewardType = "percentage"
)

type ReturnTreatment string

const (
	ReturnProRata      ReturnTreatment = "pro_rata"
	ReturnFullReversal ReturnTreatment = "full_reversal"
)

type FundingStatus string

const (
	FundingPending    FundingStatus = "pending"
	FundingReconciled FundingStatus = "reconciled"
	FundingRejected   FundingStatus = "rejected"
)

type ReservationStatus string

const (
	ReservationActive            ReservationStatus = "reserved"
	ReservationPartiallyReleased ReservationStatus = "partially_released"
	ReservationConverted         ReservationStatus = "converted"
	ReservationReleased          ReservationStatus = "released"
)

type BeneficiaryStatus string

const (
	BeneficiaryPending  BeneficiaryStatus = "pending"
	BeneficiaryVerified BeneficiaryStatus = "verified"
	BeneficiaryRevoked  BeneficiaryStatus = "revoked"
)

type ExceptionSeverity string

const (
	SeverityInfo     ExceptionSeverity = "info"
	SeverityWarning  ExceptionSeverity = "warning"
	SeverityCritical ExceptionSeverity = "critical"
)

type ExceptionStatus string

const (
	ExceptionOpen         ExceptionStatus = "open"
	ExceptionAcknowledged ExceptionStatus = "acknowledged"
	ExceptionResolved     ExceptionStatus = "resolved"
)

type Merchant struct {
	ID                 string    `json:"id"`
	TenantID           string    `json:"tenant_id"`
	Name               string    `json:"name"`
	Status             string    `json:"status"` // active, paused, suspended
	WebhookSecret      string    `json:"-"`      // Never serialized in API responses or ordinary logs
	WebhookSecretHint  string    `json:"webhook_secret_hint"`
	SettlementCurrency string    `json:"settlement_currency"`
	TermsAcceptedAt    time.Time `json:"terms_accepted_at"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// CampaignVersion is an immutable rule version; orders bind to a specific CampaignVersion ID.
type CampaignVersion struct {
	ID                  string          `json:"id"`
	TenantID            string          `json:"tenant_id"`
	CampaignID          string          `json:"campaign_id"`
	MerchantID          string          `json:"merchant_id"`
	Version             int             `json:"version"`
	Name                string          `json:"name"`
	Status              CampaignStatus  `json:"status"`
	RewardType          RewardType      `json:"reward_type"`
	FixedRewardMinor    money.Amount    `json:"fixed_reward_minor"`
	PercentageBps       int64           `json:"percentage_bps"`
	Currency            string          `json:"currency"`
	Decimals            int             `json:"decimals"`
	BudgetCapMinor      money.Amount    `json:"budget_cap_minor"`
	CustomerCapMinor    money.Amount    `json:"customer_cap_minor"`
	PerOrderCapMinor    money.Amount    `json:"per_order_cap_minor"`
	MinOrderMinor       money.Amount    `json:"min_order_minor"`
	EligibleSKUs        []string        `json:"eligible_skus"`
	ReturnWindowSeconds int64           `json:"return_window_seconds"`
	ReturnTreatment     ReturnTreatment `json:"return_treatment"`
	PlatformFeeBps      int64           `json:"platform_fee_bps"`
	LossReserveBps      int64           `json:"loss_reserve_bps"`
	StartsAt            time.Time       `json:"starts_at"`
	EndsAt              time.Time       `json:"ends_at"`
	CreatedAt           time.Time       `json:"created_at"`
}

// FundingEvent records merchant prefunding into the campaign reserve.
type FundingEvent struct {
	ID           string        `json:"id"`
	TenantID     string        `json:"tenant_id"`
	MerchantID   string        `json:"merchant_id"`
	CampaignID   string        `json:"campaign_id"`
	ProviderRef  string        `json:"provider_ref"`
	Asset        string        `json:"asset"`
	Network      string        `json:"network"`
	Decimals     int           `json:"decimals"`
	AmountBase   money.Amount  `json:"amount_base"`
	Status       FundingStatus `json:"status"`
	ReconciledAt *time.Time    `json:"reconciled_at,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
}

type OrderItem struct {
	SKU           string       `json:"sku"`
	Quantity      int          `json:"quantity"`
	SubtotalMinor money.Amount `json:"subtotal_minor"`
}

// PurchaseAttribution links a verified merchant order event to a campaign version.
// Payment settlement and order fulfillment are tracked explicitly and separately.
type PurchaseAttribution struct {
	ID                 string       `json:"id"`
	TenantID           string       `json:"tenant_id"`
	MerchantID         string       `json:"merchant_id"`
	CampaignID         string       `json:"campaign_id"`
	CampaignVersionID  string       `json:"campaign_version_id"`
	EventID            string       `json:"event_id"`
	OrderID            string       `json:"order_id"`
	CustomerID         string       `json:"customer_id"`
	OrderSubtotalMinor money.Amount `json:"order_subtotal_minor"`
	EligibleMinor      money.Amount `json:"eligible_minor"`
	RefundedMinor      money.Amount `json:"refunded_minor"`
	Currency           string       `json:"currency"`
	PaymentSettled     bool         `json:"payment_settled"`
	Fulfilled          bool         `json:"fulfilled"`
	FulfilledAt        *time.Time   `json:"fulfilled_at,omitempty"`
	Items              []OrderItem  `json:"items"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
}

// CampaignReservation atomically locks campaign budget for reward + platform fee + loss reserve.
type CampaignReservation struct {
	ID                string            `json:"id"`
	TenantID          string            `json:"tenant_id"`
	CampaignID        string            `json:"campaign_id"`
	CampaignVersionID string            `json:"campaign_version_id"`
	AttributionID     string            `json:"attribution_id"`
	RewardMinor       money.Amount      `json:"reward_minor"`
	PlatformFeeMinor  money.Amount      `json:"platform_fee_minor"`
	LossReserveMinor  money.Amount      `json:"loss_reserve_minor"`
	ReleasedMinor     money.Amount      `json:"released_minor"`
	Status            ReservationStatus `json:"status"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

func (r CampaignReservation) TotalReservedMinor() money.Amount {
	return r.RewardMinor + r.PlatformFeeMinor + r.LossReserveMinor
}

// RewardAccrual represents a customer's cashback balance tied to a source order.
type RewardAccrual struct {
	ID                  string       `json:"id"`
	TenantID            string       `json:"tenant_id"`
	CustomerID          string       `json:"customer_id"`
	MerchantID          string       `json:"merchant_id"`
	MerchantName        string       `json:"merchant_name"`
	CampaignID          string       `json:"campaign_id"`
	CampaignName        string       `json:"campaign_name"`
	CampaignVersionID   string       `json:"campaign_version_id"`
	AttributionID       string       `json:"attribution_id"`
	ReservationID       string       `json:"reservation_id"`
	OrderID             string       `json:"order_id"`
	Asset               string       `json:"asset"`
	Decimals            int          `json:"decimals"`
	OriginalAmountMinor money.Amount `json:"original_amount_minor"`
	ReversedAmountMinor money.Amount `json:"reversed_amount_minor"`
	NetAmountMinor      money.Amount `json:"net_amount_minor"`
	PlatformFeeMinor    money.Amount `json:"platform_fee_minor"`
	LossReserveMinor    money.Amount `json:"loss_reserve_minor"`
	State               RewardState  `json:"state"`
	PaymentSettled      bool         `json:"payment_settled"`
	Fulfilled           bool         `json:"fulfilled"`
	EligibleAt          *time.Time   `json:"eligible_at,omitempty"`
	PayoutID            string       `json:"payout_id,omitempty"`
	PayoutFeeBase       money.Amount `json:"payout_fee_base,omitempty"`
	PostPayoutDisputed  bool         `json:"post_payout_disputed,omitempty"`
	CreatedAt           time.Time    `json:"created_at"`
	UpdatedAt           time.Time    `json:"updated_at"`
}

// BeneficiaryVersion is an immutable versioned customer payout destination.
// Payouts bind to a specific BeneficiaryVersion ID and never silently redirect on edit.
type BeneficiaryVersion struct {
	ID                 string            `json:"id"`
	TenantID           string            `json:"tenant_id"`
	CustomerID         string            `json:"customer_id"`
	Version            int               `json:"version"`
	Asset              string            `json:"asset"`
	Network            string            `json:"network"`
	Decimals           int               `json:"decimals"`
	Address            string            `json:"address"`
	VerificationStatus BeneficiaryStatus `json:"verification_status"`
	RecoveryDisclosed  bool              `json:"recovery_disclosed"`
	VerifiedAt         *time.Time        `json:"verified_at,omitempty"`
	RevokedAt          *time.Time        `json:"revoked_at,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
}

// PayoutQuote locks conversion rate, destination, fees, and expiry before payout authorization.
type PayoutQuote struct {
	ID                   string       `json:"id"`
	TenantID             string       `json:"tenant_id"`
	CustomerID           string       `json:"customer_id"`
	BeneficiaryVersionID string       `json:"beneficiary_version_id"`
	DestinationAddress   string       `json:"destination_address"`
	RewardIDs            []string     `json:"reward_ids"`
	SourceAsset          string       `json:"source_asset"`
	SourceDecimals       int          `json:"source_decimals"`
	SourceAmountBase     money.Amount `json:"source_amount_base"`
	PayoutAsset          string       `json:"payout_asset"`
	PayoutNetwork        string       `json:"payout_network"`
	PayoutDecimals       int          `json:"payout_decimals"`
	GrossPayoutBase      money.Amount `json:"gross_payout_base"`
	FeeBase              money.Amount `json:"fee_base"`
	NetPayoutBase        money.Amount `json:"net_payout_base"`
	FeeBearer            string       `json:"fee_bearer"` // customer, platform, merchant
	FXRateBps            int64        `json:"fx_rate_bps"`
	MinThresholdMinor    money.Amount `json:"min_threshold_minor"`
	Disclosure           string       `json:"disclosure"`
	ExpiresAt            time.Time    `json:"expires_at"`
	ConsumedAt           *time.Time   `json:"consumed_at,omitempty"`
	CreatedAt            time.Time    `json:"created_at"`
}

// PayoutAttempt tracks each submission attempt for a logical payout.
type PayoutAttempt struct {
	ID                   string       `json:"id"`
	TenantID             string       `json:"tenant_id"`
	LogicalPayoutID      string       `json:"logical_payout_id"`
	AttemptNumber        int          `json:"attempt_number"`
	CustomerID           string       `json:"customer_id"`
	QuoteID              string       `json:"quote_id"`
	BeneficiaryVersionID string       `json:"beneficiary_version_id"`
	DestinationAddress   string       `json:"destination_address"`
	RewardIDs            []string     `json:"reward_ids"`
	IdempotencyKey       string       `json:"idempotency_key"`
	SourceAsset          string       `json:"source_asset"`
	SourceAmountMinor    money.Amount `json:"source_amount_minor"`
	PayoutAsset          string       `json:"payout_asset"`
	PayoutNetwork        string       `json:"payout_network"`
	PayoutDecimals       int          `json:"payout_decimals"`
	GrossPayoutBase      money.Amount `json:"gross_payout_base"`
	FeeBase              money.Amount `json:"fee_base"`
	NetPayoutBase        money.Amount `json:"net_payout_base"`
	FeeBearer            string       `json:"fee_bearer"`
	Status               PayoutStatus `json:"status"`
	ProviderRef          string       `json:"provider_ref,omitempty"`
	TxHash               string       `json:"tx_hash,omitempty"`
	Confirmations        int          `json:"confirmations"`
	FailureReason        string       `json:"failure_reason,omitempty"`
	SandboxScenario      string       `json:"sandbox_scenario,omitempty"`
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt            time.Time    `json:"updated_at"`
}

// ReconciliationException represents an operator-actionable discrepancy.
type ReconciliationException struct {
	ID             string            `json:"id"`
	TenantID       string            `json:"tenant_id"`
	Category       string            `json:"category"`
	Severity       ExceptionSeverity `json:"severity"`
	Status         ExceptionStatus   `json:"status"`
	EntityType     string            `json:"entity_type"`
	EntityID       string            `json:"entity_id"`
	Asset          string            `json:"asset"`
	Decimals       int               `json:"decimals"`
	ExpectedBase   money.Amount      `json:"expected_base"`
	ActualBase     money.Amount      `json:"actual_base"`
	DeltaBase      money.Amount      `json:"delta_base"`
	Summary        string            `json:"summary"`
	ResolutionNote string            `json:"resolution_note,omitempty"`
	ResolvedBy     string            `json:"resolved_by,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	ResolvedAt     *time.Time        `json:"resolved_at,omitempty"`
}

type OutboxEvent struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	EventType   string     `json:"event_type"`
	AggregateID string     `json:"aggregate_id"`
	Payload     any        `json:"payload"`
	Status      string     `json:"status"` // pending, processed, failed
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"last_error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

type AuditLog struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	ActorRole string    `json:"actor_role"`
	ActorID   string    `json:"actor_id"`
	Action    string    `json:"action"`
	EntityID  string    `json:"entity_id"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

type PauseControl struct {
	IssuancePaused bool       `json:"issuance_paused"`
	PayoutsPaused  bool       `json:"payouts_paused"`
	Reason         string     `json:"reason,omitempty"`
	UpdatedBy      string     `json:"updated_by,omitempty"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
}

// State is the tenant-isolated durable aggregate.
type State struct {
	TenantID             string                              `json:"tenant_id"`
	Pause                PauseControl                        `json:"pause"`
	MinPayoutMinor       money.Amount                        `json:"min_payout_minor"`
	PayoutFeeUSDCBase    money.Amount                        `json:"payout_fee_usdc_base"`
	FeeBearer            string                              `json:"fee_bearer"`
	FXRateBps            int64                               `json:"fx_rate_bps"`
	Merchants            map[string]*Merchant                `json:"merchants"`
	MerchantSecrets      map[string]string                   `json:"merchant_secrets"`
	CampaignVersions     map[string]*CampaignVersion         `json:"campaign_versions"`
	ActiveCampaignVer    map[string]string                   `json:"active_campaign_ver"` // campaign_id -> version_id
	FundingEvents        map[string]*FundingEvent            `json:"funding_events"`
	FundingByProviderRef map[string]string                   `json:"funding_by_provider_ref"`
	Attributions         map[string]*PurchaseAttribution     `json:"attributions"`
	AttributionByEvent   map[string]string                   `json:"attribution_by_event"` // merchant_id:event_id -> attribution_id
	AttributionByOrder   map[string]string                   `json:"attribution_by_order"` // merchant_id:order_id -> attribution_id
	ProcessedEventIDs    map[string]bool                     `json:"processed_event_ids"`  // merchant_id:event_id -> true
	Reservations         map[string]*CampaignReservation     `json:"reservations"`
	Rewards              map[string]*RewardAccrual           `json:"rewards"`
	Beneficiaries        map[string]*BeneficiaryVersion      `json:"beneficiaries"`
	ActiveBeneficiary    map[string]string                   `json:"active_beneficiary"` // customer_id -> beneficiary_version_id
	Quotes               map[string]*PayoutQuote             `json:"quotes"`
	PayoutAttempts       map[string]*PayoutAttempt           `json:"payout_attempts"`
	PayoutByIdempotency  map[string]string                   `json:"payout_by_idempotency"`
	PayoutByLogicalID    map[string][]string                 `json:"payout_by_logical_id"`
	ProcessedCallbacks   map[string]string                   `json:"processed_callbacks"` // callback_id -> status
	LedgerAccounts       map[string]*LedgerAccount           `json:"ledger_accounts"`
	JournalEntries       []*JournalEntry                     `json:"journal_entries"`
	JournalByIdempotency map[string]string                   `json:"journal_by_idempotency"`
	Exceptions           map[string]*ReconciliationException `json:"exceptions"`
	Outbox               []*OutboxEvent                      `json:"outbox"`
	AuditLogs            []*AuditLog                         `json:"audit_logs"`
	Sequence             int64                               `json:"sequence"`
	LastReconciledAt     *time.Time                          `json:"last_reconciled_at,omitempty"`
}

func NewState(tenantID string) *State {
	return &State{
		TenantID:             tenantID,
		MinPayoutMinor:       500,    // $5.00 disclosed minimum payout threshold
		PayoutFeeUSDCBase:    250000, // 0.25 USDC network/provider fee
		FeeBearer:            "customer",
		FXRateBps:            10000, // 1.0000 USDC per USD
		Merchants:            map[string]*Merchant{},
		MerchantSecrets:      map[string]string{},
		CampaignVersions:     map[string]*CampaignVersion{},
		ActiveCampaignVer:    map[string]string{},
		FundingEvents:        map[string]*FundingEvent{},
		FundingByProviderRef: map[string]string{},
		Attributions:         map[string]*PurchaseAttribution{},
		AttributionByEvent:   map[string]string{},
		AttributionByOrder:   map[string]string{},
		ProcessedEventIDs:    map[string]bool{},
		Reservations:         map[string]*CampaignReservation{},
		Rewards:              map[string]*RewardAccrual{},
		Beneficiaries:        map[string]*BeneficiaryVersion{},
		ActiveBeneficiary:    map[string]string{},
		Quotes:               map[string]*PayoutQuote{},
		PayoutAttempts:       map[string]*PayoutAttempt{},
		PayoutByIdempotency:  map[string]string{},
		PayoutByLogicalID:    map[string][]string{},
		ProcessedCallbacks:   map[string]string{},
		LedgerAccounts:       map[string]*LedgerAccount{},
		JournalEntries:       []*JournalEntry{},
		JournalByIdempotency: map[string]string{},
		Exceptions:           map[string]*ReconciliationException{},
		Outbox:               []*OutboxEvent{},
		AuditLogs:            []*AuditLog{},
	}
}
