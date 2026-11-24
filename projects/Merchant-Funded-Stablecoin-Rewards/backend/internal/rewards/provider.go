package rewards

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"unowallet/rewards/internal/money"
)

var (
	ErrProviderTimeout = errors.New("payout provider request timed out; outcome unknown")
	ErrProviderNotFound = errors.New("payout not found at provider")
)

type ProviderPayoutRequest struct {
	IdempotencyKey     string
	LogicalPayoutID    string
	AttemptID          string
	DestinationAddress string
	Asset              string
	Network            string
	NetAmountBase      money.Amount
	FeeBase            money.Amount
	SourceAmountMinor  money.Amount
	Scenario           string // Sandbox scenario: "", "delayed", "timeout_succeeded", "timeout_failed", "reject", "insufficient_gas"
}

type ProviderPayoutResponse struct {
	ProviderRef   string
	Status        PayoutStatus // submitted, pending, succeeded, failed
	TxHash        string
	Confirmations int
	FailureReason string
}

type ProviderCustodySnapshot struct {
	Asset           string       `json:"asset"`
	Network         string       `json:"network"`
	Decimals        int          `json:"decimals"`
	HeldBalanceBase money.Amount `json:"held_balance_base"`
	Source          string       `json:"source"`
}

// PayoutProvider abstracts stablecoin payout submission, status reconciliation, and custody balance reporting.
// Network calls must always execute outside database transactions.
type PayoutProvider interface {
	SubmitPayout(ctx context.Context, req ProviderPayoutRequest) (ProviderPayoutResponse, error)
	GetPayoutStatus(ctx context.Context, idempotencyKey, providerRef string) (ProviderPayoutResponse, error)
	GetCustodySnapshot(ctx context.Context, tenantID string, expectedLedgerUSD money.Amount) (ProviderCustodySnapshot, error)
}

// SandboxProvider is a deterministic in-memory payout & custody provider simulator.
// It supports normal settlement, pending/delayed confirmation, timeout with hidden success/failure
// (for testing outcome_unknown reconciliation), clean rejections, and custody drift injection.
type SandboxProvider struct {
	mu           sync.Mutex
	payouts      map[string]*sandboxPayoutRecord // keyed by idempotencyKey
	byRef        map[string]*sandboxPayoutRecord // keyed by providerRef
	custodyDrift map[string]money.Amount         // tenantID -> artificial USD minor unit drift
	seq          int
}

type sandboxPayoutRecord struct {
	resp      ProviderPayoutResponse
	pollCount int
	scenario  string
}

func NewSandboxProvider() *SandboxProvider {
	return &SandboxProvider{
		payouts:      map[string]*sandboxPayoutRecord{},
		byRef:        map[string]*sandboxPayoutRecord{},
		custodyDrift: map[string]money.Amount{},
	}
}

func (p *SandboxProvider) SetCustodyDrift(tenantID string, driftUSDMinor money.Amount) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.custodyDrift[tenantID] = driftUSDMinor
}

func (p *SandboxProvider) SubmitPayout(_ context.Context, req ProviderPayoutRequest) (ProviderPayoutResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Idempotent deduplication by IdempotencyKey.
	if rec, ok := p.payouts[req.IdempotencyKey]; ok {
		if rec.scenario == "timeout_succeeded" || rec.scenario == "timeout_failed" {
			return ProviderPayoutResponse{}, ErrProviderTimeout
		}
		return rec.resp, nil
	}

	// Validate destination asset/network/address at provider boundary too.
	if _, err := money.ValidatePayoutDestination(req.Asset, req.Network, req.DestinationAddress); err != nil {
		resp := ProviderPayoutResponse{
			Status:        PayoutFailed,
			FailureReason: err.Error(),
		}
		return resp, nil
	}

	p.seq++
	ref := fmt.Sprintf("prov_pay_%06d", p.seq)
	h := sha256.Sum256([]byte(req.IdempotencyKey + "|" + req.DestinationAddress + "|" + req.NetAmountBase.String()))
	txHash := "0x" + hex.EncodeToString(h[:])

	switch req.Scenario {
	case "reject":
		resp := ProviderPayoutResponse{
			ProviderRef:   ref,
			Status:        PayoutFailed,
			FailureReason: "provider compliance screening rejected destination",
		}
		rec := &sandboxPayoutRecord{resp: resp, scenario: req.Scenario}
		p.payouts[req.IdempotencyKey] = rec
		p.byRef[ref] = rec
		return resp, nil

	case "insufficient_gas":
		resp := ProviderPayoutResponse{
			ProviderRef:   ref,
			Status:        PayoutFailed,
			FailureReason: "insufficient network fee coverage on base",
		}
		rec := &sandboxPayoutRecord{resp: resp, scenario: req.Scenario}
		p.payouts[req.IdempotencyKey] = rec
		p.byRef[ref] = rec
		return resp, nil

	case "delayed":
		resp := ProviderPayoutResponse{
			ProviderRef:   ref,
			Status:        PayoutPending,
			TxHash:        txHash,
			Confirmations: 1,
		}
		rec := &sandboxPayoutRecord{resp: resp, scenario: req.Scenario}
		p.payouts[req.IdempotencyKey] = rec
		p.byRef[ref] = rec
		return resp, nil

	case "timeout_succeeded":
		// Provider actually broadcast and settled the tx, but HTTP response timed out!
		// Reconciling via GetPayoutStatus will reveal it succeeded so we NEVER double-pay.
		resp := ProviderPayoutResponse{
			ProviderRef:   ref,
			Status:        PayoutSucceeded,
			TxHash:        txHash,
			Confirmations: 12,
		}
		rec := &sandboxPayoutRecord{resp: resp, scenario: req.Scenario}
		p.payouts[req.IdempotencyKey] = rec
		p.byRef[ref] = rec
		return ProviderPayoutResponse{}, ErrProviderTimeout

	case "timeout_failed":
		// Provider timed out and never broadcast the tx; reconciliation reveals clean failure.
		resp := ProviderPayoutResponse{
			ProviderRef:   ref,
			Status:        PayoutFailed,
			FailureReason: "provider gateway timeout prior to broadcast",
		}
		rec := &sandboxPayoutRecord{resp: resp, scenario: req.Scenario}
		p.payouts[req.IdempotencyKey] = rec
		p.byRef[ref] = rec
		return ProviderPayoutResponse{}, ErrProviderTimeout

	default:
		// Standard sandbox flow: settles immediately with 12 Base confirmations.
		resp := ProviderPayoutResponse{
			ProviderRef:   ref,
			Status:        PayoutSucceeded,
			TxHash:        txHash,
			Confirmations: 12,
		}
		rec := &sandboxPayoutRecord{resp: resp, scenario: req.Scenario}
		p.payouts[req.IdempotencyKey] = rec
		p.byRef[ref] = rec
		return resp, nil
	}
}

func (p *SandboxProvider) GetPayoutStatus(_ context.Context, idempotencyKey, providerRef string) (ProviderPayoutResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var rec *sandboxPayoutRecord
	if idempotencyKey != "" {
		rec = p.payouts[idempotencyKey]
	}
	if rec == nil && providerRef != "" {
		rec = p.byRef[providerRef]
	}
	if rec == nil {
		return ProviderPayoutResponse{}, ErrProviderNotFound
	}
	rec.pollCount++
	// Delayed payouts confirm after first status check.
	if rec.scenario == "delayed" && rec.resp.Status == PayoutPending && rec.pollCount >= 1 {
		rec.resp.Status = PayoutSucceeded
		rec.resp.Confirmations = 12
	}
	return rec.resp, nil
}

func (p *SandboxProvider) GetCustodySnapshot(_ context.Context, tenantID string, expectedLedgerUSD money.Amount) (ProviderCustodySnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	drift := p.custodyDrift[tenantID]
	held := expectedLedgerUSD + drift
	if held < 0 {
		held = 0
	}
	return ProviderCustodySnapshot{
		Asset:           money.AssetUSD,
		Network:         money.NetworkFiat,
		Decimals:        money.DecimalsUSD,
		HeldBalanceBase: held,
		Source:          "sandbox_custody_provider",
	}, nil
}
