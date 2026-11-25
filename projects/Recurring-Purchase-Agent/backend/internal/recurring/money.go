package recurring

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// MinorUnits stores currency amounts as integer minor units (e.g., USD cents or
// stablecoin base units) and always serializes as a JSON decimal string so
// TypeScript clients never silently convert large integers to IEEE-754 floats.
type MinorUnits int64

func (m MinorUnits) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatInt(int64(m), 10))
}

func (m *MinorUnits) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*m = 0
		return nil
	}
	if len(trimmed) < 2 || trimmed[0] != '"' || trimmed[len(trimmed)-1] != '"' {
		return errors.New("minor units must be serialized as a decimal string")
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("invalid minor units string: %w", err)
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return fmt.Errorf("minor units must be an integer decimal string: %w", err)
	}
	*m = MinorUnits(v)
	return nil
}

// Money represents an exact integer amount with explicit asset, network, and decimals.
type Money struct {
	AmountMinor MinorUnits `json:"amount_minor"`
	Asset       string     `json:"asset"`
	Network     string     `json:"network"`
	Decimals    int        `json:"decimals"`
}

// USD creates a Money value denominated in USD fiat minor units (cents).
func USD(cents int64) Money {
	return Money{
		AmountMinor: MinorUnits(cents),
		Asset:       "USD",
		Network:     "fiat",
		Decimals:    2,
	}
}

// Validate checks that Money has explicit asset, network, decimals, and non-negative value.
func (m Money) Validate(allowZero bool) error {
	if strings.TrimSpace(m.Asset) == "" {
		return errors.New("money asset is required")
	}
	if strings.TrimSpace(m.Network) == "" {
		return errors.New("money network is required")
	}
	if m.Decimals < 0 || m.Decimals > 18 {
		return errors.New("money decimals must be between 0 and 18")
	}
	if m.AmountMinor < 0 || (!allowZero && m.AmountMinor == 0) {
		return errors.New("money amount must be positive")
	}
	return nil
}

// SameUnit verifies two Money values share identical asset, network, and decimals.
func (m Money) SameUnit(other Money) error {
	if m.Asset != other.Asset || m.Network != other.Network || m.Decimals != other.Decimals {
		return fmt.Errorf("money unit mismatch: %s/%s(%d) vs %s/%s(%d)",
			m.Asset, m.Network, m.Decimals, other.Asset, other.Network, other.Decimals)
	}
	return nil
}

// Add performs overflow-checked integer addition.
func (m Money) Add(other Money) (Money, error) {
	if err := m.SameUnit(other); err != nil {
		return Money{}, err
	}
	a, b := int64(m.AmountMinor), int64(other.AmountMinor)
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return Money{}, errors.New("money addition overflow")
	}
	return Money{
		AmountMinor: MinorUnits(a + b),
		Asset:       m.Asset,
		Network:     m.Network,
		Decimals:    m.Decimals,
	}, nil
}

// Sub performs overflow-checked integer subtraction.
func (m Money) Sub(other Money) (Money, error) {
	if err := m.SameUnit(other); err != nil {
		return Money{}, err
	}
	a, b := int64(m.AmountMinor), int64(other.AmountMinor)
	if (b < 0 && a > math.MaxInt64+b) || (b > 0 && a < math.MinInt64+b) {
		return Money{}, errors.New("money subtraction overflow")
	}
	return Money{
		AmountMinor: MinorUnits(a - b),
		Asset:       m.Asset,
		Network:     m.Network,
		Decimals:    m.Decimals,
	}, nil
}

// MulInt performs overflow-checked multiplication by an integer quantity.
func (m Money) MulInt(qty int64) (Money, error) {
	a := int64(m.AmountMinor)
	if a != 0 && qty != 0 {
		prod := new(big.Int).Mul(big.NewInt(a), big.NewInt(qty))
		if !prod.IsInt64() {
			return Money{}, errors.New("money multiplication overflow")
		}
		return Money{
			AmountMinor: MinorUnits(prod.Int64()),
			Asset:       m.Asset,
			Network:     m.Network,
			Decimals:    m.Decimals,
		}, nil
	}
	return Money{
		AmountMinor: 0,
		Asset:       m.Asset,
		Network:     m.Network,
		Decimals:    m.Decimals,
	}, nil
}

// MulBpsHalfUp multiplies Money by basis points (10000 = 100%) using explicit
// half-up rounding on exact big.Int arithmetic, then checks int64 bounds.
func (m Money) MulBpsHalfUp(bps int64) (Money, error) {
	if bps < 0 {
		return Money{}, errors.New("basis points must be non-negative")
	}
	num := new(big.Int).Mul(big.NewInt(int64(m.AmountMinor)), big.NewInt(bps))
	denom := big.NewInt(10000)
	half := big.NewInt(5000)
	if num.Sign() >= 0 {
		num.Add(num, half)
	} else {
		num.Sub(num, half)
	}
	quot := new(big.Int).Quo(num, denom)
	if !quot.IsInt64() {
		return Money{}, errors.New("money basis-point scaling overflow")
	}
	return Money{
		AmountMinor: MinorUnits(quot.Int64()),
		Asset:       m.Asset,
		Network:     m.Network,
		Decimals:    m.Decimals,
	}, nil
}

// FormatDisplay renders a human-readable string (e.g. "$24.99") using integer math only.
func (m Money) FormatDisplay() string {
	neg := m.AmountMinor < 0
	absVal := int64(m.AmountMinor)
	if neg {
		absVal = -absVal
	}
	if m.Decimals == 0 {
		prefix := ""
		if neg {
			prefix = "-"
		}
		return fmt.Sprintf("%s%d %s", prefix, absVal, m.Asset)
	}
	scale := int64(1)
	for i := 0; i < m.Decimals; i++ {
		scale *= 10
	}
	whole := absVal / scale
	frac := absVal % scale
	sign := ""
	if neg {
		sign = "-"
	}
	if m.Asset == "USD" && m.Decimals == 2 {
		return fmt.Sprintf("%s$%d.%02d", sign, whole, frac)
	}
	format := fmt.Sprintf("%%s%%d.%%0%dd %%s", m.Decimals)
	return fmt.Sprintf(format, sign, whole, frac, m.Asset)
}
