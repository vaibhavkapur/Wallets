package rewards

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"unowallet/rewards/internal/money"
)

var (
	ErrNotFound            = errors.New("resource not found")
	ErrValidation          = errors.New("validation failed")
	ErrConflict            = errors.New("conflict")
	ErrUnauthorized        = errors.New("unauthorized")
	ErrPaused              = errors.New("product or campaign is currently paused")
	ErrInsufficientFunds   = errors.New("insufficient reconciled campaign funding")
	ErrCustomerCapExceeded = errors.New("customer campaign cap reached")
	ErrQuoteExpired        = errors.New("payout quote has expired")
	ErrQuoteConsumed       = errors.New("payout quote already consumed")
	ErrBeneficiaryMismatch = errors.New("beneficiary version mismatch or revoked; re-quote required")
	ErrBelowMinimumPayout  = errors.New("eligible balance is below disclosed minimum payout threshold")
	ErrOutcomeUnknown      = errors.New("previous payout attempt has unknown outcome; reconcile before retrying")
	ErrInvalidSignature    = errors.New("invalid webhook signature")
)

type Clock func() time.Time

type Service struct {
	store         Store
	provider      PayoutProvider
	webhookSecret string
	clockMu       sync.RWMutex
	clockOffset   time.Duration
	nowFn         Clock
}

func NewService(store Store, provider PayoutProvider, webhookSecret string) *Service {
	if webhookSecret == "" {
		webhookSecret = "local-webhook-secret"
	}
	return &Service{
		store:         store,
		provider:      provider,
		webhookSecret: webhookSecret,
		nowFn:         time.Now,
	}
}

func (svc *Service) Now() time.Time {
	svc.clockMu.RLock()
	offset := svc.clockOffset
	fn := svc.nowFn
	svc.clockMu.RUnlock()
	return fn().UTC().Add(offset)
}

func (svc *Service) SetClock(fn Clock) {
	svc.clockMu.Lock()
	defer svc.clockMu.Unlock()
	svc.nowFn = fn
}

func (svc *Service) AdvanceClock(d time.Duration) time.Time {
	svc.clockMu.Lock()
	svc.clockOffset += d
	svc.clockMu.Unlock()
	return svc.Now()
}

func (svc *Service) Store() Store { return svc.store }

func (svc *Service) Provider() PayoutProvider { return svc.provider }

// ComputeHMAC computes hex HMAC-SHA256 for signed merchant and provider webhooks.
func ComputeHMAC(secret string, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyHMAC checks timestamp freshness (within maxSkew) and constant-time HMAC equality.
func (svc *Service) VerifyHMAC(secret, timestamp, sig string, body []byte, maxSkew time.Duration) error {
	if secret == "" || timestamp == "" || sig == "" {
		return ErrInvalidSignature
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: invalid timestamp", ErrInvalidSignature)
	}
	if maxSkew > 0 {
		diff := svc.Now().Sub(time.Unix(ts, 0).UTC())
		if diff < -maxSkew || diff > maxSkew {
			return fmt.Errorf("%w: timestamp outside allowed window", ErrInvalidSignature)
		}
	}
	expected := ComputeHMAC(secret, timestamp, body)
	sig = strings.TrimPrefix(strings.TrimSpace(sig), "sha256=")
	if !hmac.Equal([]byte(strings.ToLower(expected)), []byte(strings.ToLower(sig))) {
		return ErrInvalidSignature
	}
	return nil
}

func secretHint(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

func nextID(s *State, prefix string) string {
	s.Sequence++
	return fmt.Sprintf("%s_%06d", prefix, s.Sequence)
}

func addAudit(s *State, role, actorID, action, entityID, detail string, now time.Time) {
	s.AuditLogs = append(s.AuditLogs, &AuditLog{
		ID:        nextID(s, "aud"),
		TenantID:  s.TenantID,
		ActorRole: role,
		ActorID:   actorID,
		Action:    action,
		EntityID:  entityID,
		Detail:    detail,
		CreatedAt: now,
	})
}

func addOutbox(s *State, eventType, aggregateID string, payload any, now time.Time) {
	s.Outbox = append(s.Outbox, &OutboxEvent{
		ID:          nextID(s, "out"),
		TenantID:    s.TenantID,
		EventType:   eventType,
		AggregateID: aggregateID,
		Payload:     payload,
		Status:      "pending",
		CreatedAt:   now,
	})
}

func openException(s *State, category string, severity ExceptionSeverity, entityType, entityID, asset string, decimals int, expected, actual money.Amount, summary string, now time.Time) *ReconciliationException {
	// Avoid opening duplicate open exceptions for identical (category, entityID).
	for _, ex := range s.Exceptions {
		if ex.Status == ExceptionOpen && ex.Category == category && ex.EntityID == entityID {
			ex.ExpectedBase = expected
			ex.ActualBase = actual
			ex.DeltaBase = actual - expected
			ex.Summary = summary
			return ex
		}
	}
	ex := &ReconciliationException{
		ID:           nextID(s, "exc"),
		TenantID:     s.TenantID,
		Category:     category,
		Severity:     severity,
		Status:       ExceptionOpen,
		EntityType:   entityType,
		EntityID:     entityID,
		Asset:        asset,
		Decimals:     decimals,
		ExpectedBase: expected,
		ActualBase:   actual,
		DeltaBase:    actual - expected,
		Summary:      summary,
		CreatedAt:    now,
	}
	s.Exceptions[ex.ID] = ex
	return ex
}

// --- Merchant Onboarding ---

type OnboardMerchantInput struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	WebhookSecret string `json:"webhook_secret"`
	TermsAccepted bool   `json:"terms_accepted"`
}

func (svc *Service) OnboardMerchant(ctx context.Context, tenantID string, in OnboardMerchantInput) (*Merchant, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("%w: merchant name is required", ErrValidation)
	}
	if !in.TermsAccepted {
		return nil, fmt.Errorf("%w: merchant must accept prefunding, fee, and return terms", ErrValidation)
	}
	secret := strings.TrimSpace(in.WebhookSecret)
	if secret == "" {
		secret = "whsec_" + strings.ToLower(strings.ReplaceAll(in.Name, " ", "_"))
	}
	now := svc.Now()
	var out *Merchant
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		id := strings.TrimSpace(in.ID)
		if id == "" {
			id = nextID(s, "mer")
		}
		if _, exists := s.Merchants[id]; exists {
			return fmt.Errorf("%w: merchant %s already exists", ErrConflict, id)
		}
		m := &Merchant{
			ID:                 id,
			TenantID:           tenantID,
			Name:               strings.TrimSpace(in.Name),
			Status:             "active",
			WebhookSecret:      secret,
			WebhookSecretHint:  secretHint(secret),
			SettlementCurrency: money.AssetUSD,
			TermsAcceptedAt:    now,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		s.Merchants[id] = m
		s.MerchantSecrets[id] = secret
		addAudit(s, "operator", "system", "merchant.onboarded", id, m.Name, now)
		cp := *m
		out = &cp
		return nil
	})
	return out, err
}

func (svc *Service) GetMerchantWebhookSecret(ctx context.Context, tenantID, merchantID string) (string, error) {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return "", err
	}
	if _, ok := s.Merchants[merchantID]; !ok {
		return "", fmt.Errorf("%w: merchant %s", ErrNotFound, merchantID)
	}
	return s.MerchantSecrets[merchantID], nil
}

func (svc *Service) ListMerchants(ctx context.Context, tenantID string) ([]*Merchant, error) {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]*Merchant, 0, len(s.Merchants))
	for _, m := range s.Merchants {
		cp := *m
		cp.WebhookSecret = "" // Redact secret in reads
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// --- Campaigns & Prefunding ---

type CreateCampaignInput struct {
	CampaignID          string          `json:"campaign_id,omitempty"`
	MerchantID          string          `json:"merchant_id"`
	Name                string          `json:"name"`
	RewardType          RewardType      `json:"reward_type"`
	FixedRewardMinor    money.Amount    `json:"fixed_reward_minor"`
	PercentageBps       int64           `json:"percentage_bps"`
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
}

type CampaignView struct {
	CampaignVersion
	MerchantName        string       `json:"merchant_name"`
	FundedMinor         money.Amount `json:"funded_minor"`
	AvailableMinor      money.Amount `json:"available_minor"`
	ReservedMinor       money.Amount `json:"reserved_minor"`
	LossReserveMinor    money.Amount `json:"loss_reserve_minor"`
	PromisedRewardMinor money.Amount `json:"promised_reward_minor"`
	PaidRewardMinor     money.Amount `json:"paid_reward_minor"`
}

func (svc *Service) CreateCampaign(ctx context.Context, tenantID string, in CreateCampaignInput) (*CampaignView, error) {
	if strings.TrimSpace(in.MerchantID) == "" || strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("%w: merchant_id and name are required", ErrValidation)
	}
	if in.BudgetCapMinor <= 0 || in.CustomerCapMinor <= 0 {
		return nil, fmt.Errorf("%w: budget_cap_minor and customer_cap_minor must be positive", ErrValidation)
	}
	if in.RewardType != RewardTypeFixed && in.RewardType != RewardTypePercentage {
		return nil, fmt.Errorf("%w: reward_type must be fixed or percentage", ErrValidation)
	}
	if in.RewardType == RewardTypeFixed && in.FixedRewardMinor <= 0 {
		return nil, fmt.Errorf("%w: fixed_reward_minor must be positive", ErrValidation)
	}
	if in.RewardType == RewardTypePercentage && (in.PercentageBps <= 0 || in.PercentageBps > 5000) {
		return nil, fmt.Errorf("%w: percentage_bps must be between 1 and 5000 (50%%)", ErrValidation)
	}
	if in.PlatformFeeBps < 0 || in.PlatformFeeBps > 3000 || in.LossReserveBps < 0 || in.LossReserveBps > 3000 {
		return nil, fmt.Errorf("%w: platform_fee_bps and loss_reserve_bps must be 0..3000", ErrValidation)
	}
	if in.ReturnWindowSeconds < 0 {
		return nil, fmt.Errorf("%w: return_window_seconds cannot be negative", ErrValidation)
	}
	if in.ReturnTreatment == "" {
		in.ReturnTreatment = ReturnProRata
	}
	if in.ReturnTreatment != ReturnProRata && in.ReturnTreatment != ReturnFullReversal {
		return nil, fmt.Errorf("%w: invalid return_treatment", ErrValidation)
	}
	now := svc.Now()
	if in.StartsAt.IsZero() {
		in.StartsAt = now.Add(-1 * time.Minute)
	}
	if in.EndsAt.IsZero() {
		in.EndsAt = now.Add(90 * 24 * time.Hour)
	}
	if !in.EndsAt.After(in.StartsAt) {
		return nil, fmt.Errorf("%w: ends_at must be after starts_at", ErrValidation)
	}

	var out *CampaignView
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		m, ok := s.Merchants[in.MerchantID]
		if !ok {
			return fmt.Errorf("%w: merchant %s", ErrNotFound, in.MerchantID)
		}
		campID := strings.TrimSpace(in.CampaignID)
		nextVersion := 1
		if campID == "" {
			campID = nextID(s, "cmp")
		} else if existingVerID, ok := s.ActiveCampaignVer[campID]; ok {
			prev := s.CampaignVersions[existingVerID]
			if prev.MerchantID != in.MerchantID {
				return fmt.Errorf("%w: campaign belongs to different merchant", ErrUnauthorized)
			}
			nextVersion = prev.Version + 1
		}

		skus := make([]string, 0, len(in.EligibleSKUs))
		for _, sku := range in.EligibleSKUs {
			sku = strings.TrimSpace(sku)
			if sku != "" {
				skus = append(skus, sku)
			}
		}

		cv := &CampaignVersion{
			ID:                  fmt.Sprintf("%s_v%d", campID, nextVersion),
			TenantID:            tenantID,
			CampaignID:          campID,
			MerchantID:          in.MerchantID,
			Version:             nextVersion,
			Name:                strings.TrimSpace(in.Name),
			Status:              CampaignActive,
			RewardType:          in.RewardType,
			FixedRewardMinor:    in.FixedRewardMinor,
			PercentageBps:       in.PercentageBps,
			Currency:            money.AssetUSD,
			Decimals:            money.DecimalsUSD,
			BudgetCapMinor:      in.BudgetCapMinor,
			CustomerCapMinor:    in.CustomerCapMinor,
			PerOrderCapMinor:    in.PerOrderCapMinor,
			MinOrderMinor:       in.MinOrderMinor,
			EligibleSKUs:        skus,
			ReturnWindowSeconds: in.ReturnWindowSeconds,
			ReturnTreatment:     in.ReturnTreatment,
			PlatformFeeBps:      in.PlatformFeeBps,
			LossReserveBps:      in.LossReserveBps,
			StartsAt:            in.StartsAt.UTC(),
			EndsAt:              in.EndsAt.UTC(),
			CreatedAt:           now,
		}
		s.CampaignVersions[cv.ID] = cv
		s.ActiveCampaignVer[campID] = cv.ID
		addAudit(s, "merchant", m.ID, "campaign.version_created", cv.ID, fmt.Sprintf("v%d budget=%s", cv.Version, cv.BudgetCapMinor.String()), now)
		v := buildCampaignView(s, cv)
		out = &v
		return nil
	})
	return out, err
}

func (svc *Service) SetCampaignStatus(ctx context.Context, tenantID, campaignID string, status CampaignStatus, actor string) (*CampaignView, error) {
	if status != CampaignActive && status != CampaignPaused && status != CampaignEnded {
		return nil, fmt.Errorf("%w: invalid campaign status", ErrValidation)
	}
	now := svc.Now()
	var out *CampaignView
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		verID, ok := s.ActiveCampaignVer[campaignID]
		if !ok {
			return fmt.Errorf("%w: campaign %s", ErrNotFound, campaignID)
		}
		cv := s.CampaignVersions[verID]
		cv.Status = status
		addAudit(s, "operator", actor, "campaign.status_updated", cv.ID, string(status), now)
		v := buildCampaignView(s, cv)
		out = &v
		return nil
	})
	return out, err
}

type FundCampaignInput struct {
	CampaignID  string       `json:"campaign_id"`
	ProviderRef string       `json:"provider_ref"`
	AmountMinor money.Amount `json:"amount_minor"`
	Reconciled  bool         `json:"reconciled"` // If false, funding stays pending and cannot back rewards yet
}

func (svc *Service) FundCampaign(ctx context.Context, tenantID string, in FundCampaignInput) (*FundingEvent, error) {
	if strings.TrimSpace(in.CampaignID) == "" || strings.TrimSpace(in.ProviderRef) == "" {
		return nil, fmt.Errorf("%w: campaign_id and provider_ref are required", ErrValidation)
	}
	if in.AmountMinor <= 0 {
		return nil, fmt.Errorf("%w: amount_minor must be positive", ErrValidation)
	}
	now := svc.Now()
	var out *FundingEvent
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		verID, ok := s.ActiveCampaignVer[in.CampaignID]
		if !ok {
			return fmt.Errorf("%w: campaign %s", ErrNotFound, in.CampaignID)
		}
		cv := s.CampaignVersions[verID]

		// Idempotent funding by provider_ref.
		if existingID, ok := s.FundingByProviderRef[in.ProviderRef]; ok {
			existing := s.FundingEvents[existingID]
			if existing.Status == FundingPending && in.Reconciled {
				existing.Status = FundingReconciled
				t := now
				existing.ReconciledAt = &t
				if err := postFundingReconciledJournal(s, existing, now); err != nil {
					return err
				}
				addAudit(s, "operator", "funding", "funding.reconciled", existing.ID, existing.AmountBase.String(), now)
			}
			cp := *existing
			out = &cp
			return nil
		}

		fe := &FundingEvent{
			ID:          nextID(s, "fnd"),
			TenantID:    tenantID,
			MerchantID:  cv.MerchantID,
			CampaignID:  cv.CampaignID,
			ProviderRef: in.ProviderRef,
			Asset:       money.AssetUSD,
			Network:     money.NetworkFiat,
			Decimals:    money.DecimalsUSD,
			AmountBase:  in.AmountMinor,
			Status:      FundingPending,
			CreatedAt:   now,
		}
		if in.Reconciled {
			fe.Status = FundingReconciled
			t := now
			fe.ReconciledAt = &t
			if err := postFundingReconciledJournal(s, fe, now); err != nil {
				return err
			}
		}
		s.FundingEvents[fe.ID] = fe
		s.FundingByProviderRef[fe.ProviderRef] = fe.ID
		addAudit(s, "merchant", cv.MerchantID, "funding.recorded", fe.ID, fmt.Sprintf("status=%s amount=%s", fe.Status, fe.AmountBase.String()), now)
		cp := *fe
		out = &cp
		return nil
	})
	return out, err
}

func postFundingReconciledJournal(s *State, fe *FundingEvent, now time.Time) error {
	_, err := PostJournal(
		s,
		"funding:"+fe.ID,
		"funding_reconciled",
		fe.ID,
		fmt.Sprintf("Merchant prefunding reconciled for campaign %s (%s)", fe.CampaignID, fe.ProviderRef),
		[]JournalPosting{
			{AccountCode: AcctProviderCustody, ScopeID: "global", Asset: fe.Asset, Network: fe.Network, Decimals: fe.Decimals, Direction: Debit, Amount: fe.AmountBase},
			{AccountCode: AcctCampaignAvailable, ScopeID: fe.CampaignID, Asset: fe.Asset, Network: fe.Network, Decimals: fe.Decimals, Direction: Credit, Amount: fe.AmountBase},
		},
		now,
	)
	return err
}

func buildCampaignView(s *State, cv *CampaignVersion) CampaignView {
	var funded, promised, paid money.Amount
	for _, fe := range s.FundingEvents {
		if fe.CampaignID == cv.CampaignID && fe.Status == FundingReconciled {
			funded += fe.AmountBase
		}
	}
	for _, r := range s.Rewards {
		if r.CampaignID == cv.CampaignID {
			if r.State != RewardReversed {
				promised += r.NetAmountMinor
			}
			if r.State == RewardPaid {
				paid += r.NetAmountMinor
			}
		}
	}
	merchantName := cv.MerchantID
	if m, ok := s.Merchants[cv.MerchantID]; ok {
		merchantName = m.Name
	}
	return CampaignView{
		CampaignVersion:     *cv,
		MerchantName:        merchantName,
		FundedMinor:         funded,
		AvailableMinor:      AccountBalance(s, AcctCampaignAvailable, cv.CampaignID, money.AssetUSD, money.NetworkFiat),
		ReservedMinor:       AccountBalance(s, AcctCampaignReserved, cv.CampaignID, money.AssetUSD, money.NetworkFiat),
		LossReserveMinor:    AccountBalance(s, AcctLossReserve, cv.CampaignID, money.AssetUSD, money.NetworkFiat),
		PromisedRewardMinor: promised,
		PaidRewardMinor:     paid,
	}
}

func (svc *Service) ListCampaigns(ctx context.Context, tenantID, merchantID string) ([]CampaignView, error) {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]CampaignView, 0, len(s.ActiveCampaignVer))
	for _, verID := range s.ActiveCampaignVer {
		cv := s.CampaignVersions[verID]
		if merchantID != "" && cv.MerchantID != merchantID {
			continue
		}
		out = append(out, buildCampaignView(s, cv))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CampaignID < out[j].CampaignID })
	return out, nil
}

// --- Merchant Event Ingestion (Orders, Fulfillment, Refunds, Chargebacks) ---

type MerchantEventType string

const (
	EventOrderPaid       MerchantEventType = "order.paid"
	EventOrderFulfilled  MerchantEventType = "order.fulfilled"
	EventOrderRefunded   MerchantEventType = "order.refunded"
	EventOrderChargeback MerchantEventType = "order.chargeback"
)

type MerchantEventInput struct {
	EventID            string            `json:"event_id"`
	EventType          MerchantEventType `json:"event_type"`
	MerchantID         string            `json:"merchant_id"`
	CampaignID         string            `json:"campaign_id,omitempty"`
	OrderID            string            `json:"order_id"`
	CustomerID         string            `json:"customer_id,omitempty"`
	OrderSubtotalMinor money.Amount      `json:"order_subtotal_minor,omitempty"`
	RefundAmountMinor  money.Amount      `json:"refund_amount_minor,omitempty"`
	Currency           string            `json:"currency,omitempty"`
	Fulfilled          bool              `json:"fulfilled,omitempty"`
	Items              []OrderItem       `json:"items,omitempty"`
	OccurredAt         *time.Time        `json:"occurred_at,omitempty"`
}

type MerchantEventResult struct {
	Deduplicated bool                     `json:"deduplicated"`
	Attribution  *PurchaseAttribution     `json:"attribution,omitempty"`
	Reward       *RewardAccrual           `json:"reward,omitempty"`
	Exception    *ReconciliationException `json:"exception,omitempty"`
}

func (svc *Service) IngestMerchantEvent(ctx context.Context, tenantID string, in MerchantEventInput) (*MerchantEventResult, error) {
	if strings.TrimSpace(in.EventID) == "" || strings.TrimSpace(in.MerchantID) == "" || strings.TrimSpace(in.OrderID) == "" {
		return nil, fmt.Errorf("%w: event_id, merchant_id, and order_id are required", ErrValidation)
	}
	now := svc.Now()
	if in.OccurredAt != nil && !in.OccurredAt.IsZero() {
		// Use server clock for ledger consistency while accepting valid timestamps.
	}

	var result *MerchantEventResult
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		if _, ok := s.Merchants[in.MerchantID]; !ok {
			return fmt.Errorf("%w: merchant %s", ErrNotFound, in.MerchantID)
		}
		eventKey := in.MerchantID + ":" + in.EventID
		orderKey := in.MerchantID + ":" + in.OrderID

		// Strict event idempotency: duplicate events never create duplicate obligations or double reversals.
		if s.ProcessedEventIDs[eventKey] {
			res := &MerchantEventResult{Deduplicated: true}
			if attrID, ok := s.AttributionByOrder[orderKey]; ok {
				cpA := *s.Attributions[attrID]
				res.Attribution = &cpA
				for _, r := range s.Rewards {
					if r.AttributionID == attrID {
						cpR := *r
						res.Reward = &cpR
						break
					}
				}
			}
			result = res
			return nil
		}

		switch in.EventType {
		case EventOrderPaid:
			res, err := handleOrderPaid(s, in, eventKey, orderKey, now)
			if err != nil {
				return err
			}
			s.ProcessedEventIDs[eventKey] = true
			result = res
			return nil

		case EventOrderFulfilled:
			res, err := handleOrderFulfilled(s, in, orderKey, now)
			if err != nil {
				return err
			}
			s.ProcessedEventIDs[eventKey] = true
			result = res
			return nil

		case EventOrderRefunded:
			res, err := handleOrderRefund(s, in, orderKey, false, now)
			if err != nil {
				return err
			}
			s.ProcessedEventIDs[eventKey] = true
			result = res
			return nil

		case EventOrderChargeback:
			res, err := handleOrderRefund(s, in, orderKey, true, now)
			if err != nil {
				return err
			}
			s.ProcessedEventIDs[eventKey] = true
			result = res
			return nil

		default:
			return fmt.Errorf("%w: unsupported event_type %s", ErrValidation, in.EventType)
		}
	})
	return result, err
}

func handleOrderPaid(s *State, in MerchantEventInput, eventKey, orderKey string, now time.Time) (*MerchantEventResult, error) {
	if s.Pause.IssuancePaused {
		return nil, fmt.Errorf("%w: reward issuance is paused globally", ErrPaused)
	}
	if strings.TrimSpace(in.CustomerID) == "" || strings.TrimSpace(in.CampaignID) == "" {
		return nil, fmt.Errorf("%w: customer_id and campaign_id are required for order.paid", ErrValidation)
	}
	if in.Currency != "" && strings.ToUpper(in.Currency) != money.AssetUSD {
		return nil, fmt.Errorf("%w: only USD order currency is supported in MVP", ErrValidation)
	}

	// Duplicate order attribution check (different event_id for same merchant+order_id).
	if existingAttrID, ok := s.AttributionByOrder[orderKey]; ok {
		cpA := *s.Attributions[existingAttrID]
		var cpR *RewardAccrual
		for _, r := range s.Rewards {
			if r.AttributionID == existingAttrID {
				cp := *r
				cpR = &cp
				break
			}
		}
		return &MerchantEventResult{Deduplicated: true, Attribution: &cpA, Reward: cpR}, nil
	}

	verID, ok := s.ActiveCampaignVer[in.CampaignID]
	if !ok {
		return nil, fmt.Errorf("%w: campaign %s", ErrNotFound, in.CampaignID)
	}
	cv := s.CampaignVersions[verID]
	if cv.MerchantID != in.MerchantID {
		return nil, fmt.Errorf("%w: campaign %s does not belong to merchant %s", ErrUnauthorized, in.CampaignID, in.MerchantID)
	}
	if cv.Status != CampaignActive {
		return nil, fmt.Errorf("%w: campaign %s is %s", ErrPaused, cv.CampaignID, cv.Status)
	}
	if now.Before(cv.StartsAt) || now.After(cv.EndsAt) {
		return nil, fmt.Errorf("%w: order outside campaign active window", ErrValidation)
	}

	// Compute eligible subtotal based on SKU allowlist.
	eligibleMinor := in.OrderSubtotalMinor
	if len(cv.EligibleSKUs) > 0 {
		allowed := map[string]bool{}
		for _, sku := range cv.EligibleSKUs {
			allowed[sku] = true
		}
		eligibleMinor = 0
		for _, item := range in.Items {
			if allowed[item.SKU] && item.SubtotalMinor > 0 {
				var err error
				eligibleMinor, err = money.CheckedAdd(eligibleMinor, item.SubtotalMinor)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if eligibleMinor <= 0 || eligibleMinor < cv.MinOrderMinor {
		return nil, fmt.Errorf("%w: eligible order subtotal %s is below campaign minimum %s", ErrValidation, eligibleMinor.String(), cv.MinOrderMinor.String())
	}

	// Compute gross reward according to immutable CampaignVersion rules.
	var rewardMinor money.Amount
	var err error
	if cv.RewardType == RewardTypeFixed {
		rewardMinor = cv.FixedRewardMinor
	} else {
		rewardMinor, err = money.MulBpsFloor(eligibleMinor, cv.PercentageBps)
		if err != nil {
			return nil, err
		}
	}
	if cv.PerOrderCapMinor > 0 && rewardMinor > cv.PerOrderCapMinor {
		rewardMinor = cv.PerOrderCapMinor
	}
	if rewardMinor <= 0 {
		return nil, fmt.Errorf("%w: computed reward is zero", ErrValidation)
	}

	// Enforce per-customer campaign cap across all non-reversed accruals in this campaign.
	var customerUsed money.Amount
	for _, r := range s.Rewards {
		if r.CampaignID == cv.CampaignID && r.CustomerID == in.CustomerID && r.State != RewardReversed {
			customerUsed, err = money.CheckedAdd(customerUsed, r.NetAmountMinor)
			if err != nil {
				return nil, err
			}
		}
	}
	remainingCustCap, err := money.CheckedSub(cv.CustomerCapMinor, customerUsed)
	if err != nil {
		return nil, err
	}
	if remainingCustCap <= 0 {
		return nil, ErrCustomerCapExceeded
	}
	if rewardMinor > remainingCustCap {
		rewardMinor = remainingCustCap
	}

	// Calculate platform fee and loss reserve (ceil so funding never under-covers).
	feeMinor, err := money.MulBpsCeil(rewardMinor, cv.PlatformFeeBps)
	if err != nil {
		return nil, err
	}
	lossMinor, err := money.MulBpsCeil(rewardMinor, cv.LossReserveBps)
	if err != nil {
		return nil, err
	}
	totalRequired, err := money.CheckedAdd(rewardMinor, feeMinor)
	if err != nil {
		return nil, err
	}
	totalRequired, err = money.CheckedAdd(totalRequired, lossMinor)
	if err != nil {
		return nil, err
	}

	// Enforce campaign budget cap across all active/converted reservations.
	var totalCampaignCommitted money.Amount
	for _, res := range s.Reservations {
		if res.CampaignID == cv.CampaignID {
			activeAmt := res.TotalReservedMinor() - res.ReleasedMinor
			if activeAmt > 0 {
				totalCampaignCommitted, err = money.CheckedAdd(totalCampaignCommitted, activeAmt)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if totalCampaignCommitted+totalRequired > cv.BudgetCapMinor {
		openException(s, "insufficient_campaign_funding", SeverityWarning, "campaign", cv.CampaignID, money.AssetUSD, money.DecimalsUSD,
			totalCampaignCommitted+totalRequired, cv.BudgetCapMinor,
			fmt.Sprintf("Order %s exceeded campaign budget cap (%s)", in.OrderID, cv.BudgetCapMinor.String()), now)
		return nil, fmt.Errorf("%w: campaign budget cap exceeded", ErrInsufficientFunds)
	}

	// Enforce reconciled prefunding coverage: available ledger balance MUST cover reward + fee + loss reserve.
	avail := AccountBalance(s, AcctCampaignAvailable, cv.CampaignID, money.AssetUSD, money.NetworkFiat)
	if avail < totalRequired {
		openException(s, "insufficient_campaign_funding", SeverityCritical, "campaign", cv.CampaignID, money.AssetUSD, money.DecimalsUSD,
			totalRequired, avail,
			fmt.Sprintf("Order %s rejected: reconciled campaign funding (%s) < required reservation (%s)", in.OrderID, avail.String(), totalRequired.String()), now)
		return nil, fmt.Errorf("%w: available=%s required=%s", ErrInsufficientFunds, avail.String(), totalRequired.String())
	}

	// Create attribution, atomic reservation, pending reward accrual, and balanced journal entry.
	attr := &PurchaseAttribution{
		ID:                 nextID(s, "att"),
		TenantID:           s.TenantID,
		MerchantID:         in.MerchantID,
		CampaignID:         cv.CampaignID,
		CampaignVersionID:  cv.ID,
		EventID:            in.EventID,
		OrderID:            in.OrderID,
		CustomerID:         in.CustomerID,
		OrderSubtotalMinor: in.OrderSubtotalMinor,
		EligibleMinor:      eligibleMinor,
		Currency:           money.AssetUSD,
		PaymentSettled:     true,
		Fulfilled:          in.Fulfilled,
		Items:              in.Items,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	var eligibleAt *time.Time
	if in.Fulfilled {
		ft := now
		attr.FulfilledAt = &ft
		et := now.Add(time.Duration(cv.ReturnWindowSeconds) * time.Second)
		eligibleAt = &et
	}

	res := &CampaignReservation{
		ID:                nextID(s, "res"),
		TenantID:          s.TenantID,
		CampaignID:        cv.CampaignID,
		CampaignVersionID: cv.ID,
		AttributionID:     attr.ID,
		RewardMinor:       rewardMinor,
		PlatformFeeMinor:  feeMinor,
		LossReserveMinor:  lossMinor,
		Status:            ReservationActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	mName := in.MerchantID
	if m, ok := s.Merchants[in.MerchantID]; ok {
		mName = m.Name
	}

	rew := &RewardAccrual{
		ID:                  nextID(s, "rew"),
		TenantID:            s.TenantID,
		CustomerID:          in.CustomerID,
		MerchantID:          in.MerchantID,
		MerchantName:        mName,
		CampaignID:          cv.CampaignID,
		CampaignName:        cv.Name,
		CampaignVersionID:   cv.ID,
		AttributionID:       attr.ID,
		ReservationID:       res.ID,
		OrderID:             in.OrderID,
		Asset:               money.AssetUSD,
		Decimals:            money.DecimalsUSD,
		OriginalAmountMinor: rewardMinor,
		NetAmountMinor:      rewardMinor,
		PlatformFeeMinor:    feeMinor,
		LossReserveMinor:    lossMinor,
		State:               RewardPending,
		PaymentSettled:      true,
		Fulfilled:           in.Fulfilled,
		EligibleAt:          eligibleAt,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	postings := []JournalPosting{
		{AccountCode: AcctCampaignAvailable, ScopeID: cv.CampaignID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: totalRequired},
		{AccountCode: AcctRewardPending, ScopeID: in.CustomerID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Credit, Amount: rewardMinor},
	}
	if feeMinor+lossMinor > 0 {
		postings = append(postings, JournalPosting{
			AccountCode: AcctCampaignReserved, ScopeID: cv.CampaignID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Credit, Amount: feeMinor + lossMinor,
		})
	}
	if _, err := PostJournal(s, "accrual:"+rew.ID, "reward_accrued", rew.ID,
		fmt.Sprintf("Reserved campaign %s for order %s (reward=%s fee=%s reserve=%s)", cv.CampaignID, in.OrderID, rewardMinor, feeMinor, lossMinor),
		postings, now); err != nil {
		return nil, err
	}

	s.Attributions[attr.ID] = attr
	s.AttributionByEvent[eventKey] = attr.ID
	s.AttributionByOrder[orderKey] = attr.ID
	s.Reservations[res.ID] = res
	s.Rewards[rew.ID] = rew
	addOutbox(s, "reward.accrued", rew.ID, map[string]any{"reward_id": rew.ID, "order_id": in.OrderID, "customer_id": in.CustomerID}, now)

	cpA := *attr
	cpR := *rew
	return &MerchantEventResult{Attribution: &cpA, Reward: &cpR}, nil
}

func handleOrderFulfilled(s *State, in MerchantEventInput, orderKey string, now time.Time) (*MerchantEventResult, error) {
	attrID, ok := s.AttributionByOrder[orderKey]
	if !ok {
		return nil, fmt.Errorf("%w: attribution for order %s", ErrNotFound, in.OrderID)
	}
	attr := s.Attributions[attrID]
	cv := s.CampaignVersions[attr.CampaignVersionID]

	if !attr.Fulfilled {
		attr.Fulfilled = true
		ft := now
		attr.FulfilledAt = &ft
		attr.UpdatedAt = now
	}
	var rew *RewardAccrual
	for _, r := range s.Rewards {
		if r.AttributionID == attr.ID {
			rew = r
			break
		}
	}
	if rew != nil && rew.State == RewardPending {
		rew.Fulfilled = true
		et := attr.FulfilledAt.Add(time.Duration(cv.ReturnWindowSeconds) * time.Second)
		rew.EligibleAt = &et
		rew.UpdatedAt = now
	}

	cpA := *attr
	var cpR *RewardAccrual
	if rew != nil {
		c := *rew
		cpR = &c
	}
	return &MerchantEventResult{Attribution: &cpA, Reward: cpR}, nil
}

func handleOrderRefund(s *State, in MerchantEventInput, orderKey string, isChargeback bool, now time.Time) (*MerchantEventResult, error) {
	attrID, ok := s.AttributionByOrder[orderKey]
	if !ok {
		return nil, fmt.Errorf("%w: attribution for order %s", ErrNotFound, in.OrderID)
	}
	attr := s.Attributions[attrID]
	cv := s.CampaignVersions[attr.CampaignVersionID]

	refundAmt := in.RefundAmountMinor
	if refundAmt <= 0 {
		refundAmt = attr.EligibleMinor - attr.RefundedMinor
	}
	if refundAmt <= 0 {
		return nil, fmt.Errorf("%w: order is already fully refunded", ErrValidation)
	}
	remainingEligible := attr.EligibleMinor - attr.RefundedMinor
	if refundAmt > remainingEligible {
		refundAmt = remainingEligible
	}
	attr.RefundedMinor += refundAmt
	attr.UpdatedAt = now

	var rew *RewardAccrual
	for _, r := range s.Rewards {
		if r.AttributionID == attr.ID {
			rew = r
			break
		}
	}
	if rew == nil {
		cpA := *attr
		return &MerchantEventResult{Attribution: &cpA}, nil
	}
	res := s.Reservations[rew.ReservationID]

	// Determine target remaining reward based on campaign return treatment.
	var targetNet money.Amount
	if cv.ReturnTreatment == ReturnFullReversal || attr.RefundedMinor >= attr.EligibleMinor {
		targetNet = 0
	} else {
		keptOrder := attr.EligibleMinor - attr.RefundedMinor
		var err error
		targetNet, err = money.ProRataFloor(rew.OriginalAmountMinor, keptOrder, attr.EligibleMinor)
		if err != nil {
			return nil, err
		}
	}
	reverseReward := rew.NetAmountMinor - targetNet
	if reverseReward < 0 {
		reverseReward = 0
	}

	// Case A: Reward already paid or payout in-flight -> CANNOT claw back an irreversible stablecoin transfer!
	if rew.State == RewardPaid || rew.State == RewardPayoutRequested {
		rew.PostPayoutDisputed = true
		rew.UpdatedAt = now
		lossAmount := reverseReward
		if lossAmount == 0 {
			lossAmount = rew.NetAmountMinor
		}

		// Allocate available campaign loss reserve to cover merchant loss, and log operator exception.
		availReserve := AccountBalance(s, AcctLossReserve, cv.CampaignID, money.AssetUSD, money.NetworkFiat)
		covered := lossAmount
		if covered > availReserve {
			covered = availReserve
		}
		if covered > 0 {
			if _, err := PostJournal(s, "chargeback_reserve:"+in.EventID, "post_payout_loss_reserve_draw", rew.ID,
				fmt.Sprintf("Draw loss reserve for post-payout dispute on order %s (covered=%s of %s)", in.OrderID, covered, lossAmount),
				[]JournalPosting{
					{AccountCode: AcctLossReserve, ScopeID: cv.CampaignID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: covered},
					{AccountCode: AcctCampaignAvailable, ScopeID: cv.CampaignID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Credit, Amount: covered},
				}, now); err != nil {
				return nil, err
			}
		}

		sev := SeverityWarning
		if lossAmount > covered {
			sev = SeverityCritical
		}
		kind := "late refund"
		if isChargeback {
			kind = "chargeback"
		}
		exc := openException(s, "post_payout_chargeback", sev, "reward", rew.ID, money.AssetUSD, money.DecimalsUSD,
			lossAmount, covered,
			fmt.Sprintf("Post-payout %s on order %s (%s): irreversible payout cannot be clawed back; loss_reserve covered %s, uncovered contractual shortfall %s",
				kind, in.OrderID, rew.State, covered.String(), (lossAmount - covered).String()),
			now)
		addAudit(s, "merchant", in.MerchantID, "reward.post_payout_dispute", rew.ID, exc.Summary, now)

		cpA := *attr
		cpR := *rew
		cpE := *exc
		return &MerchantEventResult{Attribution: &cpA, Reward: &cpR, Exception: &cpE}, nil
	}

	// Case B: Reward is pending or eligible (not yet paid) -> reverse cleanly in ledger!
	if reverseReward == 0 || rew.State == RewardReversed {
		cpA := *attr
		cpR := *rew
		return &MerchantEventResult{Attribution: &cpA, Reward: &cpR}, nil
	}

	// Proportionally release platform fee and loss reserve.
	targetFee, err := money.MulBpsCeil(targetNet, cv.PlatformFeeBps)
	if err != nil {
		return nil, err
	}
	targetLoss, err := money.MulBpsCeil(targetNet, cv.LossReserveBps)
	if err != nil {
		return nil, err
	}
	releaseFee := rew.PlatformFeeMinor - targetFee
	if releaseFee < 0 {
		releaseFee = 0
	}
	releaseLoss := rew.LossReserveMinor - targetLoss
	if releaseLoss < 0 {
		releaseLoss = 0
	}
	totalRelease := reverseReward + releaseFee + releaseLoss

	var postings []JournalPosting
	if rew.State == RewardPending {
		postings = append(postings, JournalPosting{
			AccountCode: AcctRewardPending, ScopeID: rew.CustomerID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: reverseReward,
		})
		if releaseFee+releaseLoss > 0 {
			postings = append(postings, JournalPosting{
				AccountCode: AcctCampaignReserved, ScopeID: cv.CampaignID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: releaseFee + releaseLoss,
			})
		}
	} else {
		// Reward was eligible (revenue & loss reserve already moved out of campaign_reserved)
		postings = append(postings, JournalPosting{
			AccountCode: AcctRewardPayable, ScopeID: rew.CustomerID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: reverseReward,
		})
		if releaseFee > 0 {
			postings = append(postings, JournalPosting{
				AccountCode: AcctPlatformRevenue, ScopeID: "global", Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: releaseFee,
			})
		}
		if releaseLoss > 0 {
			postings = append(postings, JournalPosting{
				AccountCode: AcctLossReserve, ScopeID: cv.CampaignID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: releaseLoss,
			})
		}
	}
	postings = append(postings, JournalPosting{
		AccountCode: AcctCampaignAvailable, ScopeID: cv.CampaignID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Credit, Amount: totalRelease,
	})

	if _, err := PostJournal(s, "reversal:"+in.EventID, "reward_reversed", rew.ID,
		fmt.Sprintf("Reversed %s reward (+%s fee, +%s reserve) for refund on order %s", reverseReward, releaseFee, releaseLoss, in.OrderID),
		postings, now); err != nil {
		return nil, err
	}

	rew.ReversedAmountMinor += reverseReward
	rew.NetAmountMinor = targetNet
	rew.PlatformFeeMinor = targetFee
	rew.LossReserveMinor = targetLoss
	if rew.NetAmountMinor == 0 {
		rew.State = RewardReversed
	}
	rew.UpdatedAt = now

	res.ReleasedMinor += totalRelease
	if rew.NetAmountMinor == 0 {
		res.Status = ReservationReleased
	} else {
		res.Status = ReservationPartiallyReleased
	}
	res.UpdatedAt = now

	cpA := *attr
	cpR := *rew
	return &MerchantEventResult{Attribution: &cpA, Reward: &cpR}, nil
}

// --- Customer Rewards & Statements ---

type CustomerRewardsSummary struct {
	CustomerID        string              `json:"customer_id"`
	Asset             string              `json:"asset"`
	Decimals          int                 `json:"decimals"`
	PendingMinor      money.Amount        `json:"pending_minor"`
	EligibleMinor     money.Amount        `json:"eligible_minor"`
	PayoutReqMinor    money.Amount        `json:"payout_requested_minor"`
	PaidMinor         money.Amount        `json:"paid_minor"`
	ReversedMinor     money.Amount        `json:"reversed_minor"`
	MinPayoutMinor    money.Amount        `json:"min_payout_minor"`
	PayoutAsset       string              `json:"payout_asset"`
	PayoutNetwork     string              `json:"payout_network"`
	PayoutDecimals    int                 `json:"payout_decimals"`
	PayoutFeeUSDCBase money.Amount        `json:"payout_fee_usdc_base"`
	ActiveBeneficiary *BeneficiaryVersion `json:"active_beneficiary,omitempty"`
	Rewards           []*RewardAccrual    `json:"rewards"`
}

func (svc *Service) ListCustomerRewards(ctx context.Context, tenantID, customerID string) (*CustomerRewardsSummary, error) {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	summary := &CustomerRewardsSummary{
		CustomerID:        customerID,
		Asset:             money.AssetUSD,
		Decimals:          money.DecimalsUSD,
		MinPayoutMinor:    s.MinPayoutMinor,
		PayoutAsset:       money.AssetUSDC,
		PayoutNetwork:     money.NetworkBase,
		PayoutDecimals:    money.DecimalsUSDC,
		PayoutFeeUSDCBase: s.PayoutFeeUSDCBase,
		Rewards:           []*RewardAccrual{},
	}
	if bID, ok := s.ActiveBeneficiary[customerID]; ok {
		if b, ok := s.Beneficiaries[bID]; ok {
			cp := *b
			summary.ActiveBeneficiary = &cp
		}
	}
	for _, r := range s.Rewards {
		if customerID != "" && r.CustomerID != customerID {
			continue
		}
		cp := *r
		summary.Rewards = append(summary.Rewards, &cp)
		switch r.State {
		case RewardPending:
			summary.PendingMinor += r.NetAmountMinor
		case RewardEligible:
			summary.EligibleMinor += r.NetAmountMinor
		case RewardPayoutRequested:
			summary.PayoutReqMinor += r.NetAmountMinor
		case RewardPaid:
			summary.PaidMinor += r.NetAmountMinor
		}
		summary.ReversedMinor += r.ReversedAmountMinor
	}
	sort.Slice(summary.Rewards, func(i, j int) bool {
		if !summary.Rewards[i].CreatedAt.Equal(summary.Rewards[j].CreatedAt) {
			return summary.Rewards[i].CreatedAt.After(summary.Rewards[j].CreatedAt)
		}
		return summary.Rewards[i].ID > summary.Rewards[j].ID
	})
	return summary, nil
}

// --- Beneficiary Enrollment & Immutable Versioning ---

type RegisterBeneficiaryInput struct {
	CustomerID        string `json:"customer_id"`
	Asset             string `json:"asset"`
	Network           string `json:"network"`
	Address           string `json:"address"`
	RecoveryDisclosed bool   `json:"recovery_disclosed"`
	AutoVerify        bool   `json:"auto_verify"` // Sandbox helper simulating provider address verification
}

func (svc *Service) RegisterBeneficiary(ctx context.Context, tenantID string, in RegisterBeneficiaryInput) (*BeneficiaryVersion, error) {
	if strings.TrimSpace(in.CustomerID) == "" {
		return nil, fmt.Errorf("%w: customer_id is required", ErrValidation)
	}
	if !in.RecoveryDisclosed {
		return nil, fmt.Errorf("%w: customer must acknowledge wallet access & recovery disclosure", ErrValidation)
	}
	decimals, err := money.ValidatePayoutDestination(in.Asset, in.Network, in.Address)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	now := svc.Now()
	var out *BeneficiaryVersion
	err = svc.store.Update(ctx, tenantID, func(s *State) error {
		nextVer := 1
		if prevID, ok := s.ActiveBeneficiary[in.CustomerID]; ok {
			prev := s.Beneficiaries[prevID]
			nextVer = prev.Version + 1
			// Revoke previous version so any quote bound to old version cannot silently use new destination!
			prev.VerificationStatus = BeneficiaryRevoked
			rt := now
			prev.RevokedAt = &rt
		}

		bv := &BeneficiaryVersion{
			ID:                 fmt.Sprintf("ben_%s_v%d", in.CustomerID, nextVer),
			TenantID:           tenantID,
			CustomerID:         in.CustomerID,
			Version:            nextVer,
			Asset:              strings.ToUpper(strings.TrimSpace(in.Asset)),
			Network:            strings.ToLower(strings.TrimSpace(in.Network)),
			Decimals:           decimals,
			Address:            strings.TrimSpace(in.Address),
			VerificationStatus: BeneficiaryPending,
			RecoveryDisclosed:  true,
			CreatedAt:          now,
		}
		if in.AutoVerify {
			bv.VerificationStatus = BeneficiaryVerified
			vt := now
			bv.VerifiedAt = &vt
		}
		s.Beneficiaries[bv.ID] = bv
		s.ActiveBeneficiary[in.CustomerID] = bv.ID
		addAudit(s, "customer", in.CustomerID, "beneficiary.registered", bv.ID,
			fmt.Sprintf("v%d %s/%s %s status=%s", bv.Version, bv.Asset, bv.Network, bv.Address, bv.VerificationStatus), now)
		cp := *bv
		out = &cp
		return nil
	})
	return out, err
}

func (svc *Service) VerifyBeneficiary(ctx context.Context, tenantID, customerID, beneficiaryVersionID string) (*BeneficiaryVersion, error) {
	now := svc.Now()
	var out *BeneficiaryVersion
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		bv, ok := s.Beneficiaries[beneficiaryVersionID]
		if !ok || bv.CustomerID != customerID {
			return fmt.Errorf("%w: beneficiary %s", ErrNotFound, beneficiaryVersionID)
		}
		if bv.VerificationStatus == BeneficiaryRevoked {
			return fmt.Errorf("%w: cannot verify revoked beneficiary version", ErrBeneficiaryMismatch)
		}
		bv.VerificationStatus = BeneficiaryVerified
		vt := now
		bv.VerifiedAt = &vt
		addAudit(s, "customer", customerID, "beneficiary.verified", bv.ID, bv.Address, now)
		cp := *bv
		out = &cp
		return nil
	})
	return out, err
}

func (svc *Service) ListBeneficiaries(ctx context.Context, tenantID, customerID string) ([]*BeneficiaryVersion, error) {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]*BeneficiaryVersion, 0)
	for _, b := range s.Beneficiaries {
		if customerID != "" && b.CustomerID != customerID {
			continue
		}
		cp := *b
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

// --- Payout Quotes & Two-Phase Payout Authorization ---

type CreatePayoutQuoteInput struct {
	CustomerID           string   `json:"customer_id"`
	BeneficiaryVersionID string   `json:"beneficiary_version_id,omitempty"`
	RewardIDs            []string `json:"reward_ids,omitempty"` // Optional subset; empty selects all eligible rewards
	TTLSeconds           int64    `json:"ttl_seconds,omitempty"`
}

func (svc *Service) CreatePayoutQuote(ctx context.Context, tenantID string, in CreatePayoutQuoteInput) (*PayoutQuote, error) {
	if strings.TrimSpace(in.CustomerID) == "" {
		return nil, fmt.Errorf("%w: customer_id is required", ErrValidation)
	}
	ttl := in.TTLSeconds
	if ttl <= 0 {
		ttl = 300 // 5 minute quote validity
	}
	now := svc.Now()
	var out *PayoutQuote
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		if s.Pause.PayoutsPaused {
			return fmt.Errorf("%w: payouts are paused globally", ErrPaused)
		}
		benID := strings.TrimSpace(in.BeneficiaryVersionID)
		if benID == "" {
			benID = s.ActiveBeneficiary[in.CustomerID]
		}
		bv, ok := s.Beneficiaries[benID]
		if !ok || bv.CustomerID != in.CustomerID {
			return fmt.Errorf("%w: customer must enroll a payout beneficiary first", ErrValidation)
		}
		if bv.VerificationStatus != BeneficiaryVerified || s.ActiveBeneficiary[in.CustomerID] != bv.ID {
			return fmt.Errorf("%w: beneficiary %s must be active and verified", ErrBeneficiaryMismatch, bv.ID)
		}

		selected := []*RewardAccrual{}
		if len(in.RewardIDs) > 0 {
			seen := map[string]bool{}
			for _, id := range in.RewardIDs {
				if seen[id] {
					continue
				}
				seen[id] = true
				r, ok := s.Rewards[id]
				if !ok || r.CustomerID != in.CustomerID {
					return fmt.Errorf("%w: reward %s not found for customer", ErrNotFound, id)
				}
				if r.State != RewardEligible || r.NetAmountMinor <= 0 {
					return fmt.Errorf("%w: reward %s is in state %s, not eligible", ErrConflict, id, r.State)
				}
				selected = append(selected, r)
			}
		} else {
			for _, r := range s.Rewards {
				if r.CustomerID == in.CustomerID && r.State == RewardEligible && r.NetAmountMinor > 0 {
					selected = append(selected, r)
				}
			}
		}
		sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })

		var sourceUSD money.Amount
		rewardIDs := make([]string, 0, len(selected))
		for _, r := range selected {
			var err error
			sourceUSD, err = money.CheckedAdd(sourceUSD, r.NetAmountMinor)
			if err != nil {
				return err
			}
			rewardIDs = append(rewardIDs, r.ID)
		}
		if sourceUSD < s.MinPayoutMinor {
			return fmt.Errorf("%w: eligible=%s min=%s", ErrBelowMinimumPayout, sourceUSD.String(), s.MinPayoutMinor.String())
		}

		grossUSDC, err := money.ConvertUSDMinorToUSDCBase(sourceUSD, s.FXRateBps)
		if err != nil {
			return err
		}
		feeUSDC := s.PayoutFeeUSDCBase
		netUSDC := grossUSDC
		if s.FeeBearer == "customer" {
			netUSDC, err = money.CheckedSubNonNegative(grossUSDC, feeUSDC)
			if err != nil || netUSDC <= 0 {
				return fmt.Errorf("%w: payout amount does not cover network fee", ErrBelowMinimumPayout)
			}
		}

		q := &PayoutQuote{
			ID:                   nextID(s, "quo"),
			TenantID:             tenantID,
			CustomerID:           in.CustomerID,
			BeneficiaryVersionID: bv.ID,
			DestinationAddress:   bv.Address,
			RewardIDs:            rewardIDs,
			SourceAsset:          money.AssetUSD,
			SourceDecimals:       money.DecimalsUSD,
			SourceAmountBase:     sourceUSD,
			PayoutAsset:          bv.Asset,
			PayoutNetwork:        bv.Network,
			PayoutDecimals:       bv.Decimals,
			GrossPayoutBase:      grossUSDC,
			FeeBase:              feeUSDC,
			NetPayoutBase:        netUSDC,
			FeeBearer:            s.FeeBearer,
			FXRateBps:            s.FXRateBps,
			MinThresholdMinor:    s.MinPayoutMinor,
			Disclosure:           fmt.Sprintf("Irreversible %s transfer on %s to %s. Network/provider fee (%s %s) borne by %s. Conversion/redemption depends on your wallet or off-ramp provider.", bv.Asset, bv.Network, bv.Address, money.FormatDecimal(feeUSDC, bv.Decimals), bv.Asset, s.FeeBearer),
			ExpiresAt:            now.Add(time.Duration(ttl) * time.Second),
			CreatedAt:            now,
		}
		s.Quotes[q.ID] = q
		cp := *q
		out = &cp
		return nil
	})
	return out, err
}

type CreatePayoutInput struct {
	CustomerID           string `json:"customer_id"`
	QuoteID              string `json:"quote_id"`
	BeneficiaryVersionID string `json:"beneficiary_version_id"`
	IdempotencyKey       string `json:"idempotency_key"`
	TermsApproved        bool   `json:"terms_approved"`
	SandboxScenario      string `json:"sandbox_scenario,omitempty"`
}

// AuthorizePayout executes a two-phase payout:
// Phase 1 (DB Tx): validate quote, beneficiary version, expiry, lock rewards -> payout_requested,
//                  post balanced ledger clearing entry, write outbox, commit.
// Phase 2 (No DB Tx): invoke external PayoutProvider with IdempotencyKey.
// Phase 3 (DB Tx): persist provider outcome (succeeded/pending/failed/outcome_unknown).
func (svc *Service) AuthorizePayout(ctx context.Context, tenantID string, in CreatePayoutInput) (*PayoutAttempt, error) {
	if strings.TrimSpace(in.CustomerID) == "" || strings.TrimSpace(in.QuoteID) == "" || strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, fmt.Errorf("%w: customer_id, quote_id, and idempotency_key are required", ErrValidation)
	}
	if !in.TermsApproved {
		return nil, fmt.Errorf("%w: customer must approve payout terms", ErrValidation)
	}

	now := svc.Now()
	var prepared *PayoutAttempt
	var alreadyProcessed bool

	// Phase 1: Durable transactional reservation (NO provider call inside!).
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		if existingID, ok := s.PayoutByIdempotency[in.IdempotencyKey]; ok {
			cp := *s.PayoutAttempts[existingID]
			prepared = &cp
			alreadyProcessed = true
			return nil
		}
		if s.Pause.PayoutsPaused {
			return fmt.Errorf("%w: payouts are paused globally", ErrPaused)
		}

		q, ok := s.Quotes[in.QuoteID]
		if !ok || q.CustomerID != in.CustomerID {
			return fmt.Errorf("%w: quote %s", ErrNotFound, in.QuoteID)
		}
		if q.ConsumedAt != nil {
			return ErrQuoteConsumed
		}
		if now.After(q.ExpiresAt) {
			return ErrQuoteExpired
		}

		// Strict beneficiary version binding: reject if caller passed a different version or customer changed beneficiary after quote.
		if in.BeneficiaryVersionID != "" && in.BeneficiaryVersionID != q.BeneficiaryVersionID {
			return fmt.Errorf("%w: caller passed %s but quote bound to %s", ErrBeneficiaryMismatch, in.BeneficiaryVersionID, q.BeneficiaryVersionID)
		}
		bv, ok := s.Beneficiaries[q.BeneficiaryVersionID]
		if !ok || bv.VerificationStatus != BeneficiaryVerified || s.ActiveBeneficiary[in.CustomerID] != bv.ID {
			return fmt.Errorf("%w: beneficiary %s is no longer active/verified; create a new quote", ErrBeneficiaryMismatch, q.BeneficiaryVersionID)
		}

		// Verify all quoted rewards are still eligible and sum matches quote.
		var sumUSD money.Amount
		for _, rid := range q.RewardIDs {
			r, ok := s.Rewards[rid]
			if !ok || r.CustomerID != in.CustomerID || r.State != RewardEligible {
				return fmt.Errorf("%w: reward %s is no longer eligible", ErrConflict, rid)
			}
			sumUSD += r.NetAmountMinor
		}
		if sumUSD != q.SourceAmountBase {
			return fmt.Errorf("%w: eligible balance changed since quote; re-quote required", ErrConflict)
		}
		payableBal := AccountBalance(s, AcctRewardPayable, in.CustomerID, money.AssetUSD, money.NetworkFiat)
		if payableBal < q.SourceAmountBase {
			return fmt.Errorf("%w: ledger payable balance %s < quote %s", ErrInsufficientFunds, payableBal, q.SourceAmountBase)
		}

		logicalID := nextID(s, "pay")
		attempt := &PayoutAttempt{
			ID:                   nextID(s, "pat"),
			TenantID:             tenantID,
			LogicalPayoutID:      logicalID,
			AttemptNumber:        1,
			CustomerID:           in.CustomerID,
			QuoteID:              q.ID,
			BeneficiaryVersionID: bv.ID,
			DestinationAddress:   bv.Address,
			RewardIDs:            append([]string(nil), q.RewardIDs...),
			IdempotencyKey:       in.IdempotencyKey,
			SourceAsset:          q.SourceAsset,
			SourceAmountMinor:    q.SourceAmountBase,
			PayoutAsset:          q.PayoutAsset,
			PayoutNetwork:        q.PayoutNetwork,
			PayoutDecimals:       q.PayoutDecimals,
			GrossPayoutBase:      q.GrossPayoutBase,
			FeeBase:              q.FeeBase,
			NetPayoutBase:        q.NetPayoutBase,
			FeeBearer:            q.FeeBearer,
			Status:               PayoutPrepared,
			SandboxScenario:      in.SandboxScenario,
			CreatedAt:            now,
			UpdatedAt:            now,
		}

		// Post balanced USD clearing journal: reward_payable -> payout_clearing.
		if _, err := PostJournal(s, "payout_prepare:"+attempt.ID, "payout_prepared", attempt.ID,
			fmt.Sprintf("Prepared payout %s to %s (%s %s)", logicalID, bv.Address, money.FormatDecimal(q.NetPayoutBase, q.PayoutDecimals), q.PayoutAsset),
			[]JournalPosting{
				{AccountCode: AcctRewardPayable, ScopeID: in.CustomerID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: q.SourceAmountBase},
				{AccountCode: AcctPayoutClearing, ScopeID: logicalID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Credit, Amount: q.SourceAmountBase},
			}, now); err != nil {
			return err
		}

		ct := now
		q.ConsumedAt = &ct
		for _, rid := range q.RewardIDs {
			r := s.Rewards[rid]
			r.State = RewardPayoutRequested
			r.PayoutID = logicalID
			r.UpdatedAt = now
		}

		s.PayoutAttempts[attempt.ID] = attempt
		s.PayoutByIdempotency[in.IdempotencyKey] = attempt.ID
		s.PayoutByLogicalID[logicalID] = []string{attempt.ID}
		addOutbox(s, "payout.prepared", attempt.ID, map[string]any{"attempt_id": attempt.ID, "logical_payout_id": logicalID}, now)
		addAudit(s, "customer", in.CustomerID, "payout.authorized", logicalID,
			fmt.Sprintf("quote=%s beneficiary=%s net=%s %s", q.ID, bv.ID, attempt.NetPayoutBase, attempt.PayoutAsset), now)

		cp := *attempt
		prepared = &cp
		return nil
	})
	if err != nil {
		return nil, err
	}
	if alreadyProcessed {
		return prepared, nil
	}

	// Phase 2 & 3: Submit to provider outside DB transaction and record result.
	return svc.submitPreparedAttempt(ctx, tenantID, prepared)
}

func (svc *Service) submitPreparedAttempt(ctx context.Context, tenantID string, attempt *PayoutAttempt) (*PayoutAttempt, error) {
	resp, provErr := svc.provider.SubmitPayout(ctx, ProviderPayoutRequest{
		IdempotencyKey:     attempt.IdempotencyKey,
		LogicalPayoutID:    attempt.LogicalPayoutID,
		AttemptID:          attempt.ID,
		DestinationAddress: attempt.DestinationAddress,
		Asset:              attempt.PayoutAsset,
		Network:            attempt.PayoutNetwork,
		NetAmountBase:      attempt.NetPayoutBase,
		FeeBase:            attempt.FeeBase,
		SourceAmountMinor:  attempt.SourceAmountMinor,
		Scenario:           attempt.SandboxScenario,
	})

	now := svc.Now()
	var finalAttempt *PayoutAttempt
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		att := s.PayoutAttempts[attempt.ID]
		if att == nil {
			return ErrNotFound
		}
		// Mark outbox event processed.
		for _, o := range s.Outbox {
			if o.AggregateID == att.ID && o.EventType == "payout.prepared" && o.Status == "pending" {
				o.Status = "processed"
				o.Attempts++
				pt := now
				o.ProcessedAt = &pt
			}
		}

		if provErr != nil {
			// Timeout or transport error -> outcome_unknown! NEVER release clearing or retry without reconciling.
			att.Status = PayoutOutcomeUnknown
			att.FailureReason = provErr.Error()
			att.UpdatedAt = now
			openException(s, "unknown_payout_outcome", SeverityCritical, "payout_attempt", att.ID, att.PayoutAsset, att.PayoutDecimals,
				att.NetPayoutBase, 0,
				fmt.Sprintf("Payout %s (attempt %s) timed out at provider; funds remain locked in clearing pending reconciliation", att.LogicalPayoutID, att.ID), now)
			cp := *att
			finalAttempt = &cp
			return nil
		}

		if err := applyProviderPayoutOutcome(s, att, resp, now); err != nil {
			return err
		}
		cp := *att
		finalAttempt = &cp
		return nil
	})
	return finalAttempt, err
}

func applyProviderPayoutOutcome(s *State, att *PayoutAttempt, resp ProviderPayoutResponse, now time.Time) error {
	if resp.ProviderRef != "" {
		att.ProviderRef = resp.ProviderRef
	}
	if resp.TxHash != "" {
		att.TxHash = resp.TxHash
	}
	if resp.Confirmations > att.Confirmations {
		att.Confirmations = resp.Confirmations
	}

	// Idempotent guard: once terminal (succeeded or failed), detect conflicting callbacks.
	if att.Status == PayoutSucceeded {
		if resp.Status == PayoutFailed {
			openException(s, "duplicate_callback_conflict", SeverityCritical, "payout_attempt", att.ID, att.PayoutAsset, att.PayoutDecimals,
				att.NetPayoutBase, 0,
				fmt.Sprintf("Conflicting provider status %s received for already-succeeded payout %s", resp.Status, att.ID), now)
		}
		return nil
	}
	if att.Status == PayoutFailed {
		if resp.Status == PayoutSucceeded {
			openException(s, "late_confirmation_after_failure", SeverityCritical, "payout_attempt", att.ID, att.PayoutAsset, att.PayoutDecimals,
				0, att.NetPayoutBase,
				fmt.Sprintf("Late confirmation received for previously-failed payout %s (tx=%s); manual reconciliation required", att.ID, resp.TxHash), now)
		}
		return nil
	}

	switch resp.Status {
	case PayoutSubmitted, PayoutPending:
		att.Status = resp.Status
		att.UpdatedAt = now
		return nil

	case PayoutSucceeded:
		// Post balanced settlement journal across both USD and USDC/base legs!
		postings := []JournalPosting{
			// USD leg: clear payout liability and reduce provider USD custody
			{AccountCode: AcctPayoutClearing, ScopeID: att.LogicalPayoutID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: att.SourceAmountMinor},
			{AccountCode: AcctProviderCustody, ScopeID: "global", Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Credit, Amount: att.SourceAmountMinor},
			// USDC leg: record on-chain settlement and explicit fee
			{AccountCode: AcctFXClearingUSDC, ScopeID: "global", Asset: att.PayoutAsset, Network: att.PayoutNetwork, Decimals: att.PayoutDecimals, Direction: Debit, Amount: att.GrossPayoutBase},
			{AccountCode: AcctExternalPayoutSettled, ScopeID: att.CustomerID, Asset: att.PayoutAsset, Network: att.PayoutNetwork, Decimals: att.PayoutDecimals, Direction: Credit, Amount: att.NetPayoutBase},
		}
		if att.FeeBase > 0 {
			if att.FeeBearer == "customer" {
				postings = append(postings, JournalPosting{
					AccountCode: AcctProviderFeesPayable, ScopeID: "global", Asset: att.PayoutAsset, Network: att.PayoutNetwork, Decimals: att.PayoutDecimals, Direction: Credit, Amount: att.FeeBase,
				})
			} else {
				postings = append(postings,
					JournalPosting{AccountCode: AcctPayoutFeeExpense, ScopeID: "global", Asset: att.PayoutAsset, Network: att.PayoutNetwork, Decimals: att.PayoutDecimals, Direction: Debit, Amount: att.FeeBase},
					JournalPosting{AccountCode: AcctProviderFeesPayable, ScopeID: "global", Asset: att.PayoutAsset, Network: att.PayoutNetwork, Decimals: att.PayoutDecimals, Direction: Credit, Amount: att.FeeBase},
				)
			}
		}
		if _, err := PostJournal(s, "payout_settled:"+att.LogicalPayoutID, "payout_settled", att.ID,
			fmt.Sprintf("Settled payout %s on %s tx=%s", att.LogicalPayoutID, att.PayoutNetwork, att.TxHash),
			postings, now); err != nil {
			return err
		}

		att.Status = PayoutSucceeded
		att.FailureReason = ""
		att.UpdatedAt = now
		for i, rid := range att.RewardIDs {
			if r, ok := s.Rewards[rid]; ok {
				r.State = RewardPaid
				r.PayoutID = att.LogicalPayoutID
				if i == 0 {
					r.PayoutFeeBase = att.FeeBase
				}
				r.UpdatedAt = now
			}
		}
		// Resolve any open unknown_payout_outcome exception for this attempt.
		for _, ex := range s.Exceptions {
			if ex.EntityID == att.ID && ex.Category == "unknown_payout_outcome" && ex.Status == ExceptionOpen {
				ex.Status = ExceptionResolved
				ex.ResolvedBy = "reconciliation_worker"
				ex.ResolutionNote = "Confirmed succeeded via provider status lookup: tx=" + att.TxHash
				rt := now
				ex.ResolvedAt = &rt
			}
		}
		return nil

	case PayoutFailed:
		// Confirmed failure: reverse payout clearing back to customer reward_payable and return rewards to eligible.
		if _, err := PostJournal(s, "payout_failed:"+att.ID, "payout_failed_reversal", att.ID,
			fmt.Sprintf("Reversed clearing for confirmed failed payout attempt %s (%s)", att.ID, resp.FailureReason),
			[]JournalPosting{
				{AccountCode: AcctPayoutClearing, ScopeID: att.LogicalPayoutID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: att.SourceAmountMinor},
				{AccountCode: AcctRewardPayable, ScopeID: att.CustomerID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Credit, Amount: att.SourceAmountMinor},
			}, now); err != nil {
			return err
		}
		att.Status = PayoutFailed
		att.FailureReason = resp.FailureReason
		att.UpdatedAt = now
		for _, rid := range att.RewardIDs {
			if r, ok := s.Rewards[rid]; ok && r.State == RewardPayoutRequested {
				r.State = RewardEligible
				r.UpdatedAt = now
			}
		}
		for _, ex := range s.Exceptions {
			if ex.EntityID == att.ID && ex.Category == "unknown_payout_outcome" && ex.Status == ExceptionOpen {
				ex.Status = ExceptionResolved
				ex.ResolvedBy = "reconciliation_worker"
				ex.ResolutionNote = "Confirmed failed at provider; funds restored to eligible payable balance: " + resp.FailureReason
				rt := now
				ex.ResolvedAt = &rt
			}
		}
		return nil
	}
	return nil
}

// RetryPayout safely retries a logical payout ONLY after all prior attempts are confirmed failed
// (never while an attempt is outcome_unknown, pending, or succeeded), reusing the logical_payout_id.
type RetryPayoutInput struct {
	LogicalPayoutID string `json:"logical_payout_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	SandboxScenario string `json:"sandbox_scenario,omitempty"`
}

func (svc *Service) RetryPayout(ctx context.Context, tenantID, customerID string, in RetryPayoutInput) (*PayoutAttempt, error) {
	if strings.TrimSpace(in.LogicalPayoutID) == "" || strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, fmt.Errorf("%w: logical_payout_id and idempotency_key are required", ErrValidation)
	}
	now := svc.Now()
	var nextAttempt *PayoutAttempt
	var already bool

	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		if existingID, ok := s.PayoutByIdempotency[in.IdempotencyKey]; ok {
			cp := *s.PayoutAttempts[existingID]
			nextAttempt = &cp
			already = true
			return nil
		}
		if s.Pause.PayoutsPaused {
			return fmt.Errorf("%w: payouts are paused", ErrPaused)
		}
		attemptIDs := s.PayoutByLogicalID[in.LogicalPayoutID]
		if len(attemptIDs) == 0 {
			return fmt.Errorf("%w: logical payout %s", ErrNotFound, in.LogicalPayoutID)
		}
		latest := s.PayoutAttempts[attemptIDs[len(attemptIDs)-1]]
		if customerID != "" && latest.CustomerID != customerID {
			return ErrUnauthorized
		}
		if latest.Status == PayoutOutcomeUnknown {
			return ErrOutcomeUnknown
		}
		if latest.Status != PayoutFailed {
			return fmt.Errorf("%w: cannot retry payout in status %s", ErrConflict, latest.Status)
		}

		// Beneficiary binding check: retry CANNOT silently use a newly edited beneficiary!
		bv := s.Beneficiaries[latest.BeneficiaryVersionID]
		if bv == nil || bv.VerificationStatus != BeneficiaryVerified || s.ActiveBeneficiary[latest.CustomerID] != bv.ID {
			return fmt.Errorf("%w: beneficiary changed since original payout; create a new quote", ErrBeneficiaryMismatch)
		}

		// Ensure all rewards are still eligible and re-reserve clearing.
		for _, rid := range latest.RewardIDs {
			r := s.Rewards[rid]
			if r == nil || r.State != RewardEligible {
				return fmt.Errorf("%w: reward %s no longer eligible", ErrConflict, rid)
			}
		}

		att := &PayoutAttempt{
			ID:                   nextID(s, "pat"),
			TenantID:             tenantID,
			LogicalPayoutID:      latest.LogicalPayoutID,
			AttemptNumber:        latest.AttemptNumber + 1,
			CustomerID:           latest.CustomerID,
			QuoteID:              latest.QuoteID,
			BeneficiaryVersionID: latest.BeneficiaryVersionID,
			DestinationAddress:   latest.DestinationAddress,
			RewardIDs:            append([]string(nil), latest.RewardIDs...),
			IdempotencyKey:       in.IdempotencyKey,
			SourceAsset:          latest.SourceAsset,
			SourceAmountMinor:    latest.SourceAmountMinor,
			PayoutAsset:          latest.PayoutAsset,
			PayoutNetwork:        latest.PayoutNetwork,
			PayoutDecimals:       latest.PayoutDecimals,
			GrossPayoutBase:      latest.GrossPayoutBase,
			FeeBase:              latest.FeeBase,
			NetPayoutBase:        latest.NetPayoutBase,
			FeeBearer:            latest.FeeBearer,
			Status:               PayoutPrepared,
			SandboxScenario:      in.SandboxScenario,
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		if _, err := PostJournal(s, "payout_prepare:"+att.ID, "payout_prepared", att.ID,
			fmt.Sprintf("Retry #%d prepared for logical payout %s", att.AttemptNumber, att.LogicalPayoutID),
			[]JournalPosting{
				{AccountCode: AcctRewardPayable, ScopeID: att.CustomerID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: att.SourceAmountMinor},
				{AccountCode: AcctPayoutClearing, ScopeID: att.LogicalPayoutID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Credit, Amount: att.SourceAmountMinor},
			}, now); err != nil {
			return err
		}
		for _, rid := range att.RewardIDs {
			r := s.Rewards[rid]
			r.State = RewardPayoutRequested
			r.UpdatedAt = now
		}
		s.PayoutAttempts[att.ID] = att
		s.PayoutByIdempotency[in.IdempotencyKey] = att.ID
		s.PayoutByLogicalID[att.LogicalPayoutID] = append(s.PayoutByLogicalID[att.LogicalPayoutID], att.ID)
		cp := *att
		nextAttempt = &cp
		return nil
	})
	if err != nil {
		return nil, err
	}
	if already {
		return nextAttempt, nil
	}
	return svc.submitPreparedAttempt(ctx, tenantID, nextAttempt)
}

type PayoutDetail struct {
	LogicalPayoutID string           `json:"logical_payout_id"`
	LatestAttempt   *PayoutAttempt   `json:"latest_attempt"`
	Attempts        []*PayoutAttempt `json:"attempts"`
}

func (svc *Service) GetPayout(ctx context.Context, tenantID, id, customerID string) (*PayoutDetail, error) {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	logicalID := id
	if att, ok := s.PayoutAttempts[id]; ok {
		logicalID = att.LogicalPayoutID
	}
	ids := s.PayoutByLogicalID[logicalID]
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: payout %s", ErrNotFound, id)
	}
	attempts := make([]*PayoutAttempt, 0, len(ids))
	for _, aid := range ids {
		cp := *s.PayoutAttempts[aid]
		if customerID != "" && cp.CustomerID != customerID {
			return nil, ErrUnauthorized
		}
		attempts = append(attempts, &cp)
	}
	return &PayoutDetail{
		LogicalPayoutID: logicalID,
		LatestAttempt:   attempts[len(attempts)-1],
		Attempts:        attempts,
	}, nil
}

func (svc *Service) ListPayouts(ctx context.Context, tenantID, customerID string) ([]*PayoutAttempt, error) {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]*PayoutAttempt, 0)
	for _, att := range s.PayoutAttempts {
		if customerID != "" && att.CustomerID != customerID {
			continue
		}
		cp := *att
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// --- Provider Webhook Callback Handling ---

type ProviderWebhookInput struct {
	CallbackID    string       `json:"callback_id"`
	ProviderRef   string       `json:"provider_ref"`
	AttemptID     string       `json:"attempt_id,omitempty"`
	Status        PayoutStatus `json:"status"`
	TxHash        string       `json:"tx_hash,omitempty"`
	Confirmations int          `json:"confirmations,omitempty"`
	FailureReason string       `json:"failure_reason,omitempty"`
}

func (svc *Service) ProcessProviderWebhook(ctx context.Context, tenantID string, in ProviderWebhookInput) (*PayoutAttempt, bool, error) {
	if strings.TrimSpace(in.CallbackID) == "" {
		return nil, false, fmt.Errorf("%w: callback_id is required", ErrValidation)
	}
	now := svc.Now()
	var out *PayoutAttempt
	var dedup bool
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		if _, seen := s.ProcessedCallbacks[in.CallbackID]; seen {
			dedup = true
			for _, a := range s.PayoutAttempts {
				if a.ID == in.AttemptID || (in.ProviderRef != "" && a.ProviderRef == in.ProviderRef) {
					cp := *a
					out = &cp
					break
				}
			}
			return nil
		}

		var att *PayoutAttempt
		if in.AttemptID != "" {
			att = s.PayoutAttempts[in.AttemptID]
		}
		if att == nil && in.ProviderRef != "" {
			for _, a := range s.PayoutAttempts {
				if a.ProviderRef == in.ProviderRef {
					att = a
					break
				}
			}
		}
		if att == nil {
			return fmt.Errorf("%w: payout attempt for ref %s", ErrNotFound, in.ProviderRef)
		}

		if err := applyProviderPayoutOutcome(s, att, ProviderPayoutResponse{
			ProviderRef:   in.ProviderRef,
			Status:        in.Status,
			TxHash:        in.TxHash,
			Confirmations: in.Confirmations,
			FailureReason: in.FailureReason,
		}, now); err != nil {
			return err
		}
		s.ProcessedCallbacks[in.CallbackID] = string(in.Status)
		cp := *att
		out = &cp
		return nil
	})
	return out, dedup, err
}

// --- Operator Controls, Reconciliation & Audit ---

func (svc *Service) SetPauseControl(ctx context.Context, tenantID string, issuancePaused, payoutsPaused bool, reason, actor string) (*PauseControl, error) {
	now := svc.Now()
	var out *PauseControl
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		s.Pause = PauseControl{
			IssuancePaused: issuancePaused,
			PayoutsPaused:  payoutsPaused,
			Reason:         strings.TrimSpace(reason),
			UpdatedBy:      actor,
			UpdatedAt:      &now,
		}
		addAudit(s, "operator", actor, "system.pause_updated", tenantID,
			fmt.Sprintf("issuance_paused=%v payouts_paused=%v reason=%s", issuancePaused, payoutsPaused, reason), now)
		cp := s.Pause
		out = &cp
		return nil
	})
	return out, err
}

func (svc *Service) ListExceptions(ctx context.Context, tenantID string, onlyOpen bool) ([]*ReconciliationException, error) {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]*ReconciliationException, 0, len(s.Exceptions))
	for _, ex := range s.Exceptions {
		if onlyOpen && ex.Status == ExceptionResolved {
			continue
		}
		cp := *ex
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (svc *Service) ResolveException(ctx context.Context, tenantID, exceptionID, actor, note string) (*ReconciliationException, error) {
	now := svc.Now()
	var out *ReconciliationException
	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		ex, ok := s.Exceptions[exceptionID]
		if !ok {
			return fmt.Errorf("%w: exception %s", ErrNotFound, exceptionID)
		}
		ex.Status = ExceptionResolved
		ex.ResolvedBy = actor
		ex.ResolutionNote = strings.TrimSpace(note)
		rt := now
		ex.ResolvedAt = &rt
		addAudit(s, "operator", actor, "exception.resolved", ex.ID, ex.ResolutionNote, now)
		cp := *ex
		out = &cp
		return nil
	})
	return out, err
}

func (svc *Service) GetLedgerSummary(ctx context.Context, tenantID string) (*LedgerSummary, error) {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	sum := BuildLedgerSummary(s)
	return &sum, nil
}

func (svc *Service) ListJournalEntries(ctx context.Context, tenantID string) ([]*JournalEntry, error) {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]*JournalEntry, len(s.JournalEntries))
	for i := range s.JournalEntries {
		cp := *s.JournalEntries[len(s.JournalEntries)-1-i]
		out[i] = &cp
	}
	return out, nil
}

func (svc *Service) GetSystemStatus(ctx context.Context, tenantID string) (map[string]any, error) {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	openExc := 0
	for _, ex := range s.Exceptions {
		if ex.Status != ExceptionResolved {
			openExc++
		}
	}
	return map[string]any{
		"tenant_id":            tenantID,
		"server_time":          svc.Now(),
		"pause":                s.Pause,
		"merchant_count":       len(s.Merchants),
		"campaign_count":       len(s.ActiveCampaignVer),
		"reward_count":         len(s.Rewards),
		"payout_attempt_count": len(s.PayoutAttempts),
		"open_exceptions":      openExc,
		"last_reconciled_at":   s.LastReconciledAt,
	}, nil
}

// SeedPilotData populates three pilot merchants, prefunded campaigns, sample verified beneficiary,
// and sample order lifecycle states when the tenant store is empty.
func (svc *Service) SeedPilotData(ctx context.Context, tenantID string) error {
	s, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return err
	}
	if len(s.Merchants) > 0 {
		return nil
	}

	pilots := []struct {
		id         string
		name       string
		secret     string
		campID     string
		campName   string
		rewardType RewardType
		fixed      money.Amount
		bps        int64
		budget     money.Amount
		custCap    money.Amount
		skus       []string
		retSec     int64
		feeBps     int64
		lossBps    int64
	}{
		{
			id: "mer_roast", name: "Roast & Co. Coffee", secret: "whsec_roast_pilot_1001",
			campID: "cmp_roast_spring", campName: "5% Back on Whole Bean Bags",
			rewardType: RewardTypePercentage, bps: 500, budget: 250000, custCap: 5000,
			skus: []string{"BEAN-ETH-12OZ", "BEAN-COL-12OZ", "GRIND-ESP-2LB"}, retSec: 604800, feeBps: 500, lossBps: 200,
		},
		{
			id: "mer_sound", name: "Soundhouse Audio", secret: "whsec_sound_pilot_2002",
			campID: "cmp_sound_launch", campName: "$15 Flat Cashback on Studio Gear",
			rewardType: RewardTypeFixed, fixed: 1500, budget: 500000, custCap: 4500,
			skus: []string{"STUDIO-PRO-01", "MIC-COND-09"}, retSec: 1209600, feeBps: 600, lossBps: 300,
		},
		{
			id: "mer_trail", name: "Northstar Trail Supply", secret: "whsec_trail_pilot_3003",
			campID: "cmp_trail_gear", campName: "8% Back on Trail Packs",
			rewardType: RewardTypePercentage, bps: 800, budget: 300000, custCap: 8000,
			skus: []string{}, retSec: 86400, feeBps: 400, lossBps: 250,
		},
	}

	for _, p := range pilots {
		if _, err := svc.OnboardMerchant(ctx, tenantID, OnboardMerchantInput{
			ID: p.id, Name: p.name, WebhookSecret: p.secret, TermsAccepted: true,
		}); err != nil {
			return err
		}
		if _, err := svc.CreateCampaign(ctx, tenantID, CreateCampaignInput{
			CampaignID:          p.campID,
			MerchantID:          p.id,
			Name:                p.campName,
			RewardType:          p.rewardType,
			FixedRewardMinor:    p.fixed,
			PercentageBps:       p.bps,
			BudgetCapMinor:      p.budget,
			CustomerCapMinor:    p.custCap,
			PerOrderCapMinor:    2500,
			MinOrderMinor:       2000,
			EligibleSKUs:        p.skus,
			ReturnWindowSeconds: p.retSec,
			ReturnTreatment:     ReturnProRata,
			PlatformFeeBps:      p.feeBps,
			LossReserveBps:      p.lossBps,
		}); err != nil {
			return err
		}
		if _, err := svc.FundCampaign(ctx, tenantID, FundCampaignInput{
			CampaignID:  p.campID,
			ProviderRef: "ach_prefund_" + p.campID,
			AmountMinor: p.budget,
			Reconciled:  true,
		}); err != nil {
			return err
		}
	}

	// Register a verified Base USDC beneficiary for demo customer cust_alex.
	if _, err := svc.RegisterBeneficiary(ctx, tenantID, RegisterBeneficiaryInput{
		CustomerID:        "cust_alex",
		Asset:             money.AssetUSDC,
		Network:           money.NetworkBase,
		Address:           "0x71C7656EC7ab88b098defB751B7401B5f6d8976F",
		RecoveryDisclosed: true,
		AutoVerify:        true,
	}); err != nil {
		return err
	}

	// Seed one order already fulfilled whose short return window we make immediately eligible,
	// plus one pending order still inside return window.
	if _, err := svc.IngestMerchantEvent(ctx, tenantID, MerchantEventInput{
		EventID:            "evt_seed_001",
		EventType:          EventOrderPaid,
		MerchantID:         "mer_trail",
		CampaignID:         "cmp_trail_gear",
		OrderID:            "ORD-9001",
		CustomerID:         "cust_alex",
		OrderSubtotalMinor: 12500, // $125.00 -> 8% = $10.00 reward
		Currency:           money.AssetUSD,
		Fulfilled:          true,
		Items:              []OrderItem{{SKU: "PACK-35L", Quantity: 1, SubtotalMinor: 12500}},
	}); err != nil {
		return err
	}
	if _, err := svc.IngestMerchantEvent(ctx, tenantID, MerchantEventInput{
		EventID:            "evt_seed_002",
		EventType:          EventOrderPaid,
		MerchantID:         "mer_sound",
		CampaignID:         "cmp_sound_launch",
		OrderID:            "ORD-9002",
		CustomerID:         "cust_alex",
		OrderSubtotalMinor: 18900, // $189.00 -> $15.00 flat reward
		Currency:           money.AssetUSD,
		Fulfilled:          false,
		Items:              []OrderItem{{SKU: "STUDIO-PRO-01", Quantity: 1, SubtotalMinor: 18900}},
	}); err != nil {
		return err
	}
	// Backdate ORD-9001 eligible_at so customer immediately sees an eligible reward ready for payout demo!
	_ = svc.store.Update(ctx, tenantID, func(st *State) error {
		past := svc.Now().Add(-1 * time.Hour)
		for _, r := range st.Rewards {
			if r.OrderID == "ORD-9001" {
				r.EligibleAt = &past
			}
		}
		return nil
	})
	_, _ = svc.RunWorkerTick(ctx, tenantID)
	return nil
}
