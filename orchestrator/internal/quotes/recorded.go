package quotes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/contract"
)

// Scenario is one recorded basket: the request that was captured, catalog
// metadata, and the independently checked expected result.
type Scenario struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Synthetic   bool             `json:"synthetic"`
	Catalog     *BasketInfo      `json:"catalog,omitempty"`
	Request     contract.Request `json:"request"`
	Expected    json.RawMessage  `json:"expected,omitempty"`
	// RawRequest preserves the request exactly as recorded.
	RawRequest json.RawMessage `json:"-"`
}

// LoadScenarios reads every *.json scenario in dir, in name order.
func LoadScenarios(dir string) ([]Scenario, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	out := make([]Scenario, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Request json.RawMessage `json:"request"`
		}
		var s Scenario
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := contract.DecodeStrict(data, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		s.RawRequest = raw.Request
		out = append(out, s)
	}
	return out, nil
}

// Recorded replays recorded quotes. Each call re-stamps the recorded
// timestamps relative to the request time, so a replayed quote has the same
// remaining validity it had when it was captured.
type Recorded struct {
	scenarios map[string]Scenario
	order     []string
}

// NewRecorded loads a recorded dataset directory.
func NewRecorded(dir string) (*Recorded, error) {
	scenarios, err := LoadScenarios(dir)
	if err != nil {
		return nil, err
	}
	r := &Recorded{scenarios: map[string]Scenario{}}
	for _, s := range scenarios {
		if s.Catalog == nil {
			return nil, fmt.Errorf("quotes: recorded scenario %q has no catalog entry", s.Name)
		}
		key := basketKey(s.Catalog.MerchantID, s.Catalog.BasketRef)
		if _, dup := r.scenarios[key]; dup {
			return nil, fmt.Errorf("quotes: duplicate recorded basket %s", key)
		}
		r.scenarios[key] = s
		r.order = append(r.order, key)
	}
	if len(r.order) == 0 {
		return nil, fmt.Errorf("quotes: no recorded scenarios in %s", dir)
	}
	return r, nil
}

func basketKey(merchantID, basketRef string) string { return merchantID + "\x00" + basketRef }

func (r *Recorded) Name() string { return "recorded" }

func (r *Recorded) Catalog(context.Context) ([]BasketInfo, error) {
	out := make([]BasketInfo, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, *r.scenarios[key].Catalog)
	}
	return out, nil
}

func (r *Recorded) Quote(_ context.Context, req Request) (*Quotes, error) {
	s, ok := r.scenarios[basketKey(req.MerchantID, req.BasketRef)]
	if !ok {
		return nil, ErrUnknownBasket
	}
	// Deep copy through JSON so callers can never mutate the dataset.
	var options []contract.PaymentOption
	data, err := json.Marshal(s.Request.Options)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &options); err != nil {
		return nil, err
	}
	shift := req.Now.Sub(s.Request.EvaluatedAt)
	for i := range options {
		q := &options[i].Quote
		q.ObtainedAt = q.ObtainedAt.Add(shift)
		q.ValidUntil = q.ValidUntil.Add(shift)
		if step := options[i].FundingStep; step != nil {
			step.ValidUntil = step.ValidUntil.Add(shift)
		}
	}
	Normalize(options)
	return &Quotes{
		Provider:   r.Name(),
		BasketHash: s.Request.Basket.BasketHash,
		Info:       *s.Catalog,
		Options:    options,
	}, nil
}

// Funds returns the recorded balance for the option. A live adapter would read
// the wallet or issuer here on every evaluation.
func (r *Recorded) Funds(_ context.Context, req Request, option contract.PaymentOption) (contract.Funds, error) {
	s, ok := r.scenarios[basketKey(req.MerchantID, req.BasketRef)]
	if !ok {
		return contract.Funds{}, ErrUnknownBasket
	}
	for _, o := range s.Request.Options {
		if o.OptionID == option.OptionID {
			return o.Funds, nil
		}
	}
	return contract.Funds{Status: "unknown"}, nil
}

// Cached wraps a provider with a small in-memory quote cache. An entry is
// served only while every quote in it remains valid for at least MinRemaining.
// Funds are never taken from the cache: callers must read Balances.
type Cached struct {
	Provider
	MinRemaining time.Duration
	entries      *cacheMap
}

// NewCached returns a caching wrapper around p.
func NewCached(p Provider, minRemaining time.Duration) *Cached {
	return &Cached{Provider: p, MinRemaining: minRemaining, entries: newCacheMap(256)}
}

func (c *Cached) Quote(ctx context.Context, req Request) (*Quotes, error) {
	key := req.TenantID + "\x00" + req.CustomerID + "\x00" + basketKey(req.MerchantID, req.BasketRef)
	if !req.Fresh {
		if data, ok := c.entries.get(key); ok {
			var cached Quotes
			if json.Unmarshal(data, &cached) == nil && usableUntil(&cached).After(req.Now.Add(c.MinRemaining)) {
				stripFunds(cached.Options)
				return &cached, nil
			}
		}
	}
	fresh, err := c.Provider.Quote(ctx, req)
	if err != nil {
		return nil, err
	}
	if data, err := json.Marshal(fresh); err == nil {
		c.entries.put(key, data)
	}
	return fresh, nil
}

// usableUntil is the earliest expiry among the quotes.
func usableUntil(q *Quotes) time.Time {
	var earliest time.Time
	for i, o := range q.Options {
		if i == 0 || o.Quote.ValidUntil.Before(earliest) {
			earliest = o.Quote.ValidUntil
		}
		if o.FundingStep != nil && o.FundingStep.ValidUntil.Before(earliest) {
			earliest = o.FundingStep.ValidUntil
		}
	}
	return earliest
}

// stripFunds marks funds unknown so that a cached balance cannot be used even
// if a caller forgets to read Balances.
func stripFunds(options []contract.PaymentOption) {
	for i := range options {
		options[i].Funds = contract.Funds{Status: "unknown"}
	}
}
