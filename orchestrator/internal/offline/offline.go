// Package offline runs a recorded dataset through the engine and compares
// each result with its independently checked expectation (milestone O1).
package offline

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/engine"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/quotes"
)

// Project reduces an engine result to the fields a dataset expectation pins
// down. It selects and reorders fields; it performs no arithmetic.
func Project(resultJSON []byte) (map[string]any, error) {
	var r struct {
		Outcome           string   `json:"outcome"`
		Rationale         string   `json:"rationale"`
		PreferredOptionID *string  `json:"preferred_option_id"`
		TiedOptionIDs     []string `json:"tied_option_ids"`
		LowestCostIDs     []string `json:"lowest_cost_option_ids"`
		ExpiresAt         *string  `json:"expires_at"`
		Savings           *struct {
			Total string `json:"estimated_total_micro_usd"`
		} `json:"savings_vs_baseline"`
		Sensitivities []struct {
			Code string `json:"code"`
		} `json:"sensitivities"`
		Options []struct {
			OptionID   string `json:"option_id"`
			Status     string `json:"status"`
			Exclusions []struct {
				Code string `json:"code"`
			} `json:"exclusions"`
			Breakdown *struct {
				Immediate string `json:"immediate_cost_micro_usd"`
				Rewards   string `json:"estimated_rewards_total_micro_usd"`
				Net       string `json:"estimated_net_cost_micro_usd"`
				AmountDue struct {
					BaseUnits string `json:"base_units"`
				} `json:"amount_due"`
			} `json:"breakdown"`
		} `json:"options"`
	}
	if err := json.Unmarshal(resultJSON, &r); err != nil {
		return nil, err
	}
	uniqueSorted := func(values []string) []any {
		sort.Strings(values)
		out := []any{}
		for i, v := range values {
			if i == 0 || v != values[i-1] {
				out = append(out, v)
			}
		}
		return out
	}
	list := func(values []string) []any {
		out := []any{}
		for _, v := range values {
			out = append(out, v)
		}
		return out
	}
	optional := func(s *string) any {
		if s == nil {
			return nil
		}
		return *s
	}

	options := map[string]any{}
	for _, o := range r.Options {
		var codes []string
		for _, x := range o.Exclusions {
			codes = append(codes, x.Code)
		}
		entry := map[string]any{"status": o.Status, "exclusion_codes": uniqueSorted(codes)}
		if b := o.Breakdown; b != nil {
			entry["immediate_cost_micro_usd"] = b.Immediate
			entry["estimated_rewards_micro_usd"] = b.Rewards
			entry["estimated_net_cost_micro_usd"] = b.Net
			entry["amount_due_base_units"] = b.AmountDue.BaseUnits
		}
		options[o.OptionID] = entry
	}
	var sensitivities []string
	for _, s := range r.Sensitivities {
		sensitivities = append(sensitivities, s.Code)
	}
	var savings any
	if r.Savings != nil {
		savings = r.Savings.Total
	}
	return map[string]any{
		"outcome":                           r.Outcome,
		"rationale":                         r.Rationale,
		"preferred_option_id":               optional(r.PreferredOptionID),
		"tied_option_ids":                   list(r.TiedOptionIDs),
		"lowest_cost_option_ids":            list(r.LowestCostIDs),
		"savings_estimated_total_micro_usd": savings,
		"expires_at":                        optional(r.ExpiresAt),
		"sensitivity_codes":                 uniqueSorted(sensitivities),
		"options":                           options,
	}, nil
}

// CaseResult is the verdict for one scenario.
type CaseResult struct {
	Name     string         `json:"name"`
	Passed   bool           `json:"passed"`
	Outcome  string         `json:"outcome,omitempty"`
	Error    string         `json:"error,omitempty"`
	Expected map[string]any `json:"expected,omitempty"`
	Actual   map[string]any `json:"actual,omitempty"`
}

// Report summarises an offline run.
type Report struct {
	Dataset   string         `json:"dataset"`
	Total     int            `json:"total"`
	Passed    int            `json:"passed"`
	Failed    int            `json:"failed"`
	ByOutcome map[string]int `json:"by_outcome"`
	// Coverage is the share of baskets that produced a preferred option or a tie.
	Recommended int          `json:"recommended"`
	Cases       []CaseResult `json:"cases"`
}

// Run evaluates every scenario in dir and compares it with its expectation.
func Run(ctx context.Context, evaluator engine.Evaluator, dir string) (*Report, error) {
	scenarios, err := quotes.LoadScenarios(dir)
	if err != nil {
		return nil, err
	}
	if len(scenarios) == 0 {
		return nil, fmt.Errorf("offline: no scenarios in %s", dir)
	}
	report := &Report{Dataset: dir, ByOutcome: map[string]int{}}
	for _, s := range scenarios {
		report.Total++
		result := CaseResult{Name: s.Name}
		request := s.Request
		quotes.Normalize(request.Options)
		evaluation, err := evaluator.Evaluate(ctx, &request)
		if err != nil {
			result.Error = err.Error()
		} else if len(s.Expected) == 0 {
			result.Error = "scenario has no expected result"
		} else {
			actual, err := Project(evaluation.ResultJSON)
			var expected map[string]any
			if err == nil {
				err = json.Unmarshal(s.Expected, &expected)
			}
			switch {
			case err != nil:
				result.Error = err.Error()
			case reflect.DeepEqual(actual, expected):
				result.Passed = true
				result.Outcome = evaluation.Result.Outcome
			default:
				result.Outcome = evaluation.Result.Outcome
				result.Expected, result.Actual = expected, actual
			}
		}
		if result.Passed {
			report.Passed++
			report.ByOutcome[result.Outcome]++
			if result.Outcome == "preferred" || result.Outcome == "tie" {
				report.Recommended++
			}
		} else {
			report.Failed++
		}
		report.Cases = append(report.Cases, result)
	}
	return report, nil
}
