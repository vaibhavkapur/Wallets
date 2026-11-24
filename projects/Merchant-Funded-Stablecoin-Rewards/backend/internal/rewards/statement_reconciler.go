package rewards

import (
	"fmt"
	"sort"

	"unowallet/rewards/internal/money"
)

// StatementRecord represents one normalized ledger or provider settlement row.
type StatementRecord struct {
	ReferenceID string       `json:"reference_id"`
	Asset       string       `json:"asset"`
	Network     string       `json:"network"`
	Decimals    int          `json:"decimals"`
	AmountBase  money.Amount `json:"amount_base"`
	FeeBase     money.Amount `json:"fee_base"`
	Status      string       `json:"status"` // settled, pending, failed
}

type StatementBatchInput struct {
	BatchID         string            `json:"batch_id"`
	LedgerRecords   []StatementRecord `json:"ledger_records"`
	ProviderRecords []StatementRecord `json:"provider_records"`
}

type StatementDiscrepancy struct {
	Kind           string       `json:"kind"` // missing_in_provider, missing_in_ledger, amount_mismatch, fee_mismatch, status_mismatch, asset_mismatch
	ReferenceID    string       `json:"reference_id"`
	Asset          string       `json:"asset"`
	Network        string       `json:"network"`
	LedgerAmount   money.Amount `json:"ledger_amount"`
	ProviderAmount money.Amount `json:"provider_amount"`
	DeltaAmount    money.Amount `json:"delta_amount"`
	Detail         string       `json:"detail"`
}

type StatementReconciliationReport struct {
	BatchID          string                 `json:"batch_id"`
	LedgerCount      int                    `json:"ledger_count"`
	ProviderCount    int                    `json:"provider_count"`
	MatchedCount     int                    `json:"matched_count"`
	DiscrepancyCount int                    `json:"discrepancy_count"`
	Discrepancies    []StatementDiscrepancy `json:"discrepancies"`
}

// ReconcileStatementBatch is the pure, deterministic Go reference batch reconciler.
// It strictly produces discrepancy reports and never mutates balances or initiates payouts.
func ReconcileStatementBatch(in StatementBatchInput) StatementReconciliationReport {
	ledgerMap := make(map[string]StatementRecord, len(in.LedgerRecords))
	for _, r := range in.LedgerRecords {
		ledgerMap[r.ReferenceID] = r
	}
	provMap := make(map[string]StatementRecord, len(in.ProviderRecords))
	for _, r := range in.ProviderRecords {
		provMap[r.ReferenceID] = r
	}

	allRefs := make([]string, 0, len(ledgerMap)+len(provMap))
	seen := make(map[string]bool, len(ledgerMap)+len(provMap))
	for ref := range ledgerMap {
		if !seen[ref] {
			seen[ref] = true
			allRefs = append(allRefs, ref)
		}
	}
	for ref := range provMap {
		if !seen[ref] {
			seen[ref] = true
			allRefs = append(allRefs, ref)
		}
	}
	sort.Strings(allRefs)

	discrepancies := make([]StatementDiscrepancy, 0)
	matched := 0

	for _, ref := range allRefs {
		l, hasL := ledgerMap[ref]
		p, hasP := provMap[ref]
		if hasL && !hasP {
			discrepancies = append(discrepancies, StatementDiscrepancy{
				Kind:           "missing_in_provider",
				ReferenceID:    ref,
				Asset:          l.Asset,
				Network:        l.Network,
				LedgerAmount:   l.AmountBase,
				ProviderAmount: 0,
				DeltaAmount:    -l.AmountBase,
				Detail:         fmt.Sprintf("reference %s present in ledger (%s) but missing in provider statement", ref, l.AmountBase.String()),
			})
			continue
		}
		if !hasL && hasP {
			discrepancies = append(discrepancies, StatementDiscrepancy{
				Kind:           "missing_in_ledger",
				ReferenceID:    ref,
				Asset:          p.Asset,
				Network:        p.Network,
				LedgerAmount:   0,
				ProviderAmount: p.AmountBase,
				DeltaAmount:    p.AmountBase,
				Detail:         fmt.Sprintf("reference %s present in provider statement (%s) but missing in ledger", ref, p.AmountBase.String()),
			})
			continue
		}

		rowHasIssue := false
		if l.Asset != p.Asset || l.Network != p.Network {
			rowHasIssue = true
			discrepancies = append(discrepancies, StatementDiscrepancy{
				Kind:           "asset_mismatch",
				ReferenceID:    ref,
				Asset:          l.Asset,
				Network:        l.Network,
				LedgerAmount:   l.AmountBase,
				ProviderAmount: p.AmountBase,
				DeltaAmount:    p.AmountBase - l.AmountBase,
				Detail:         fmt.Sprintf("asset/network mismatch: ledger=%s/%s provider=%s/%s", l.Asset, l.Network, p.Asset, p.Network),
			})
		}
		if l.AmountBase != p.AmountBase {
			rowHasIssue = true
			discrepancies = append(discrepancies, StatementDiscrepancy{
				Kind:           "amount_mismatch",
				ReferenceID:    ref,
				Asset:          l.Asset,
				Network:        l.Network,
				LedgerAmount:   l.AmountBase,
				ProviderAmount: p.AmountBase,
				DeltaAmount:    p.AmountBase - l.AmountBase,
				Detail:         fmt.Sprintf("amount mismatch: ledger=%s provider=%s", l.AmountBase.String(), p.AmountBase.String()),
			})
		}
		if l.FeeBase != p.FeeBase {
			rowHasIssue = true
			discrepancies = append(discrepancies, StatementDiscrepancy{
				Kind:           "fee_mismatch",
				ReferenceID:    ref,
				Asset:          l.Asset,
				Network:        l.Network,
				LedgerAmount:   l.FeeBase,
				ProviderAmount: p.FeeBase,
				DeltaAmount:    p.FeeBase - l.FeeBase,
				Detail:         fmt.Sprintf("fee mismatch: ledger=%s provider=%s", l.FeeBase.String(), p.FeeBase.String()),
			})
		}
		if l.Status != p.Status {
			rowHasIssue = true
			discrepancies = append(discrepancies, StatementDiscrepancy{
				Kind:           "status_mismatch",
				ReferenceID:    ref,
				Asset:          l.Asset,
				Network:        l.Network,
				LedgerAmount:   l.AmountBase,
				ProviderAmount: p.AmountBase,
				DeltaAmount:    p.AmountBase - l.AmountBase,
				Detail:         fmt.Sprintf("status mismatch: ledger=%s provider=%s", l.Status, p.Status),
			})
		}
		if !rowHasIssue {
			matched++
		}
	}

	return StatementReconciliationReport{
		BatchID:          in.BatchID,
		LedgerCount:      len(in.LedgerRecords),
		ProviderCount:    len(in.ProviderRecords),
		MatchedCount:     matched,
		DiscrepancyCount: len(discrepancies),
		Discrepancies:    discrepancies,
	}
}
