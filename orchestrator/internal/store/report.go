package store

import (
	"context"
	"time"
)

// Summary aggregates what the release gates ask to be measured. Realized
// amounts and estimates are kept in separate fields and are never summed.
type Summary struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	Evaluations struct {
		Total     int            `json:"total"`
		ByOutcome map[string]int `json:"by_outcome"`
		ByMode    map[string]int `json:"by_mode"`
		// Recommended is the number of evaluations that produced a preferred
		// option or a tie; Coverage is Recommended / Total.
		Recommended            int            `json:"recommended"`
		RecommendedNonBaseline int            `json:"recommended_non_baseline"`
		ExclusionCodes         map[string]int `json:"exclusion_codes"`
		Warnings               map[string]int `json:"warnings"`
	} `json:"evaluations"`

	Handoffs struct {
		Total           int            `json:"total"`
		ByStatus        map[string]int `json:"by_status"`
		ChosePreferred  int            `json:"chose_preferred"`
		ChoseBaseline   int            `json:"chose_baseline"`
		UnresolvedCount int            `json:"unresolved"`
	} `json:"handoffs"`

	Outcomes struct {
		PaymentsSucceeded int `json:"payments_succeeded"`
		PaymentsFailed    int `json:"payments_failed"`
		PaymentsUnknown   int `json:"payments_unknown"`
		Refunds           int `json:"refunds"`
		// ChargeDiffers counts settled payments whose charge was not the estimate.
		ChargeDiffers int `json:"charge_differs_from_estimate"`
		// RealizedImmediateSavingsMicroUSD is the sum over settled, executed
		// payments, each measured against the estimated baseline recorded at
		// evaluation time. The baseline payment was never observed.
		RealizedImmediateSavingsMicroUSD string `json:"realized_immediate_savings_micro_usd"`
		RealizedSavingsCount             int    `json:"realized_savings_count"`
		// EstimatedRewardsMicroUSD is an estimate of future reward value on
		// settled payments. It is not a realized saving.
		EstimatedRewardsMicroUSD string `json:"estimated_rewards_micro_usd"`
		// ShadowSelections counts customer-reported selections in shadow mode.
		ShadowSelections          int `json:"shadow_selections"`
		ShadowFollowedRecommended int `json:"shadow_followed_recommendation"`
	} `json:"outcomes"`
}

func (s *Store) countBy(ctx context.Context, into map[string]int, sql string, args ...any) error {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return err
		}
		into[key] = n
	}
	return rows.Err()
}

// Summarize computes the summary for one tenant over [from, to).
func (s *Store) Summarize(ctx context.Context, tenantID string, from, to time.Time) (*Summary, error) {
	out := &Summary{From: from, To: to}
	ev := &out.Evaluations
	ev.ByOutcome, ev.ByMode, ev.ExclusionCodes, ev.Warnings = map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	out.Handoffs.ByStatus = map[string]int{}

	const window = ` tenant_id = $1 AND created_at >= $2 AND created_at < $3 `
	if err := s.countBy(ctx, ev.ByOutcome, `SELECT outcome, count(*) FROM evaluations WHERE`+window+`GROUP BY outcome`, tenantID, from, to); err != nil {
		return nil, err
	}
	if err := s.countBy(ctx, ev.ByMode, `SELECT mode, count(*) FROM evaluations WHERE`+window+`GROUP BY mode`, tenantID, from, to); err != nil {
		return nil, err
	}
	for outcome, n := range ev.ByOutcome {
		ev.Total += n
		if outcome == "preferred" || outcome == "tie" {
			ev.Recommended += n
		}
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM evaluations WHERE`+window+`
		AND outcome = 'preferred' AND baseline_option_id IS NOT NULL AND preferred_option_id <> baseline_option_id`,
		tenantID, from, to).Scan(&ev.RecommendedNonBaseline); err != nil {
		return nil, err
	}
	if err := s.countBy(ctx, ev.ExclusionCodes, `SELECT x->>'code', count(*)
		FROM evaluations e, jsonb_array_elements(e.engine_result->'options') o, jsonb_array_elements(o->'exclusions') x
		WHERE e.tenant_id = $1 AND e.created_at >= $2 AND e.created_at < $3 GROUP BY 1`, tenantID, from, to); err != nil {
		return nil, err
	}
	if err := s.countBy(ctx, ev.Warnings, `SELECT w, count(*) FROM evaluations e, unnest(e.warnings) w
		WHERE e.tenant_id = $1 AND e.created_at >= $2 AND e.created_at < $3 GROUP BY 1`, tenantID, from, to); err != nil {
		return nil, err
	}

	h := &out.Handoffs
	if err := s.countBy(ctx, h.ByStatus, `SELECT status, count(*) FROM execution_handoffs WHERE`+window+`GROUP BY status`, tenantID, from, to); err != nil {
		return nil, err
	}
	for status, n := range h.ByStatus {
		h.Total += n
		if status == HandoffCreated || status == HandoffUnknown {
			h.UnresolvedCount += n
		}
	}
	if err := s.pool.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE h.option_id = e.preferred_option_id),
			count(*) FILTER (WHERE h.option_id = e.baseline_option_id)
		FROM execution_handoffs h JOIN evaluations e ON e.id = h.evaluation_id
		WHERE h.tenant_id = $1 AND h.created_at >= $2 AND h.created_at < $3`, tenantID, from, to).
		Scan(&h.ChosePreferred, &h.ChoseBaseline); err != nil {
		return nil, err
	}

	o := &out.Outcomes
	err := s.pool.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE type = 'payment' AND status = 'succeeded' AND handoff_id IS NOT NULL),
			count(*) FILTER (WHERE type = 'payment' AND status = 'failed' AND handoff_id IS NOT NULL),
			count(*) FILTER (WHERE type = 'payment' AND status = 'unknown' AND handoff_id IS NOT NULL),
			count(*) FILTER (WHERE type = 'refund'),
			count(*) FILTER (WHERE type = 'payment' AND status = 'succeeded'
				AND COALESCE(derived->>'charge_difference_base_units', '0') <> '0'),
			COALESCE(sum((derived->>'realized_immediate_savings_micro_usd')::numeric), 0)::text,
			count(*) FILTER (WHERE derived->>'realized_immediate_savings_micro_usd' IS NOT NULL),
			COALESCE(sum((derived->>'estimated_rewards_micro_usd')::numeric)
				FILTER (WHERE type = 'payment' AND status = 'succeeded' AND handoff_id IS NOT NULL), 0)::text,
			count(*) FILTER (WHERE handoff_id IS NULL AND type = 'payment'),
			count(*) FILTER (WHERE handoff_id IS NULL AND type = 'payment' AND (derived->>'matches_recommendation')::boolean)
		FROM observed_outcomes
		WHERE tenant_id = $1 AND recorded_at >= $2 AND recorded_at < $3`, tenantID, from, to).
		Scan(&o.PaymentsSucceeded, &o.PaymentsFailed, &o.PaymentsUnknown, &o.Refunds, &o.ChargeDiffers,
			&o.RealizedImmediateSavingsMicroUSD, &o.RealizedSavingsCount, &o.EstimatedRewardsMicroUSD,
			&o.ShadowSelections, &o.ShadowFollowedRecommended)
	if err != nil {
		return nil, err
	}
	return out, nil
}
