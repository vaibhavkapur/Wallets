package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrOverflow        = errors.New("integer arithmetic overflow")
	ErrNegativeAmount  = errors.New("negative monetary amount not allowed")
	ErrInvalidAmount   = errors.New("invalid decimal string amount")
	ErrInvalidAsset    = errors.New("unsupported asset or network")
	ErrInvalidAddress  = errors.New("invalid destination address for network")
	ErrDivisionByZero  = errors.New("division by zero")
	evmAddressRegex    = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
)

// Supported MVP payout asset/network configuration.
const (
	AssetUSD       = "USD"
	NetworkFiat    = "fiat"
	DecimalsUSD    = 2
	AssetUSDC      = "USDC"
	NetworkBase    = "base"
	DecimalsUSDC   = 6
)

// Amount represents an exact integer quantity in minor or base units.
// It always serializes to and from JSON as a base-10 decimal string so
// TypeScript/JavaScript clients never silently coerce values to IEEE-754 floats.
type Amount int64

func (a Amount) Int64() int64 { return int64(a) }

func (a Amount) String() string { return strconv.FormatInt(int64(a), 10) }

func (a Amount) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatInt(int64(a), 10))
}

func (a *Amount) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		*a = 0
		return nil
	}
	// Accept canonical decimal string; also reject floats/non-integers.
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidAmount, err)
		}
		v, err := ParseAmount(raw)
		if err != nil {
			return err
		}
		*a = v
		return nil
	}
	// Reject fractional numbers explicitly.
	if strings.ContainsAny(s, ".eE") {
		return fmt.Errorf("%w: floating point not permitted (%s)", ErrInvalidAmount, s)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	*a = Amount(v)
	return nil
}

// ParseAmount parses a base-10 integer string into an Amount.
func ParseAmount(s string) (Amount, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ErrInvalidAmount
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	return Amount(v), nil
}

// CheckedAdd adds two amounts, failing on int64 overflow.
func CheckedAdd(a, b Amount) (Amount, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// CheckedSub subtracts b from a, failing on int64 overflow.
func CheckedSub(a, b Amount) (Amount, error) {
	if (b < 0 && a > math.MaxInt64+b) || (b > 0 && a < math.MinInt64+b) {
		return 0, ErrOverflow
	}
	return a - b, nil
}

// CheckedSubNonNegative subtracts b from a and fails if result < 0.
func CheckedSubNonNegative(a, b Amount) (Amount, error) {
	res, err := CheckedSub(a, b)
	if err != nil {
		return 0, err
	}
	if res < 0 {
		return 0, ErrNegativeAmount
	}
	return res, nil
}

// MulBpsFloor multiplies amount by basis points (1 bps = 0.01%, 10000 bps = 100%)
// using big.Int intermediate math and explicit floor rounding (never over-promises).
func MulBpsFloor(amount Amount, bps int64) (Amount, error) {
	if amount < 0 || bps < 0 {
		return 0, ErrNegativeAmount
	}
	num := new(big.Int).Mul(big.NewInt(int64(amount)), big.NewInt(bps))
	den := big.NewInt(10000)
	quo := new(big.Int).Quo(num, den)
	if !quo.IsInt64() {
		return 0, ErrOverflow
	}
	return Amount(quo.Int64()), nil
}

// MulBpsCeil multiplies amount by basis points using explicit ceiling rounding
// (used for conservative fee/reserve allocation so funding never under-covers).
func MulBpsCeil(amount Amount, bps int64) (Amount, error) {
	if amount < 0 || bps < 0 {
		return 0, ErrNegativeAmount
	}
	if amount == 0 || bps == 0 {
		return 0, nil
	}
	num := new(big.Int).Mul(big.NewInt(int64(amount)), big.NewInt(bps))
	den := big.NewInt(10000)
	// ceil(num / den) = (num + den - 1) / den
	num.Add(num, new(big.Int).Sub(den, big.NewInt(1)))
	quo := new(big.Int).Quo(num, den)
	if !quo.IsInt64() {
		return 0, ErrOverflow
	}
	return Amount(quo.Int64()), nil
}

// ProRataFloor computes floor(total * part / whole) using exact big.Int arithmetic.
func ProRataFloor(total, part, whole Amount) (Amount, error) {
	if total < 0 || part < 0 || whole < 0 {
		return 0, ErrNegativeAmount
	}
	if whole == 0 {
		return 0, ErrDivisionByZero
	}
	if part >= whole {
		return total, nil
	}
	num := new(big.Int).Mul(big.NewInt(int64(total)), big.NewInt(int64(part)))
	quo := new(big.Int).Quo(num, big.NewInt(int64(whole)))
	if !quo.IsInt64() {
		return 0, ErrOverflow
	}
	return Amount(quo.Int64()), nil
}

// ProRataCeil computes ceil(total * part / whole) using exact big.Int arithmetic
// (used for partial return clawbacks so remaining reward never exceeds pro-rata entitlement).
func ProRataCeil(total, part, whole Amount) (Amount, error) {
	if total < 0 || part < 0 || whole < 0 {
		return 0, ErrNegativeAmount
	}
	if whole == 0 {
		return 0, ErrDivisionByZero
	}
	if part == 0 || total == 0 {
		return 0, nil
	}
	if part >= whole {
		return total, nil
	}
	num := new(big.Int).Mul(big.NewInt(int64(total)), big.NewInt(int64(part)))
	w := big.NewInt(int64(whole))
	num.Add(num, new(big.Int).Sub(w, big.NewInt(1)))
	quo := new(big.Int).Quo(num, w)
	if !quo.IsInt64() {
		return 0, ErrOverflow
	}
	return Amount(quo.Int64()), nil
}

// ConvertUSDMinorToUSDCBase converts USD cents (2 decimals) to USDC base units (6 decimals)
// at a given FX rate in bps (10000 = 1.0000 USDC per USD), using exact floor rounding.
func ConvertUSDMinorToUSDCBase(usdMinor Amount, fxRateBps int64) (Amount, error) {
	if usdMinor < 0 || fxRateBps <= 0 {
		return 0, ErrNegativeAmount
	}
	// 1 USD minor unit (0.01 USD) * 10^(6-2) = 10,000 USDC base units at 1:1.
	// With fxRateBps (10000 = 1.0000), scale factor = 10^(6-2) = 10000, divided by 10000 bps -> exact!
	// General formula: floor(usdMinor * 10^(6-2) * fxRateBps / 10000)
	num := new(big.Int).Mul(big.NewInt(int64(usdMinor)), big.NewInt(10000))
	num.Mul(num, big.NewInt(fxRateBps))
	quo := new(big.Int).Quo(num, big.NewInt(10000))
	if !quo.IsInt64() {
		return 0, ErrOverflow
	}
	return Amount(quo.Int64()), nil
}

// FormatDecimal formats an integer base amount with the given number of decimals (e.g. 1250, 2 -> "12.50").
func FormatDecimal(amount Amount, decimals int) string {
	neg := amount < 0
	v := int64(amount)
	if neg {
		if v == math.MinInt64 {
			// Handle MinInt64 safely via big.Int
			b := new(big.Int).Neg(big.NewInt(v))
			return "-" + formatBigDecimal(b, decimals)
		}
		v = -v
	}
	out := formatBigDecimal(big.NewInt(v), decimals)
	if neg {
		return "-" + out
	}
	return out
}

func formatBigDecimal(v *big.Int, decimals int) string {
	if decimals <= 0 {
		return v.String()
	}
	s := v.String()
	if len(s) <= decimals {
		return "0." + strings.Repeat("0", decimals-len(s)) + s
	}
	split := len(s) - decimals
	return s[:split] + "." + s[split:]
}

// ValidatePayoutDestination validates asset, network, and address format for the supported MVP rail.
func ValidatePayoutDestination(asset, network, address string) (int, error) {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	network = strings.ToLower(strings.TrimSpace(network))
	address = strings.TrimSpace(address)

	if asset != AssetUSDC || network != NetworkBase {
		return 0, fmt.Errorf("%w: MVP supports only %s on %s (got %s on %s)", ErrInvalidAsset, AssetUSDC, NetworkBase, asset, network)
	}
	if !evmAddressRegex.MatchString(address) {
		return 0, fmt.Errorf("%w: Base network requires a 42-character 0x EVM address", ErrInvalidAddress)
	}
	// Reject the zero address explicitly.
	if strings.EqualFold(address, "0x0000000000000000000000000000000000000000") {
		return 0, fmt.Errorf("%w: zero address is not permitted", ErrInvalidAddress)
	}
	return DecimalsUSDC, nil
}
