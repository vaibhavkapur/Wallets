// Package money implements exact, non-negative base-unit amounts.
//
// Amounts are integer base units (e.g. USDC has 6 decimals, so 1 USDC is
// "1000000"). They are serialized as decimal strings in JSON so that no
// client silently converts them to floating point. All arithmetic is checked:
// overflow above MaxBaseUnits and underflow below zero are errors, never
// silent wraparound. No floating-point value ever participates.
package money

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// MaxDigits is the longest decimal representation accepted (2^128-1 has 39 digits).
const MaxDigits = 39

var (
	// MaxBaseUnits is 2^128-1, chosen to match the Rust gateway's u128 arithmetic
	// so that both services agree on representable amounts.
	MaxBaseUnits = func() *big.Int {
		v := new(big.Int).Lsh(big.NewInt(1), 128)
		return v.Sub(v, big.NewInt(1))
	}()

	ErrInvalidFormat = errors.New("money: invalid amount format")
	ErrOverflow      = errors.New("money: amount exceeds maximum representable value")
	ErrNegative      = errors.New("money: amount would be negative")
)

// Amount is an immutable non-negative integer amount of base units.
// The zero value is a valid zero amount.
type Amount struct {
	v *big.Int
}

// Zero returns the zero amount.
func Zero() Amount { return Amount{} }

// FromUint64 builds an amount from an unsigned integer.
func FromUint64(u uint64) Amount {
	return Amount{v: new(big.Int).SetUint64(u)}
}

// Parse parses a canonical decimal string: ASCII digits only, no sign, no
// whitespace, no leading zeros (other than "0" itself), at most MaxDigits
// digits and not above MaxBaseUnits.
func Parse(s string) (Amount, error) {
	if len(s) == 0 || len(s) > MaxDigits {
		return Amount{}, fmt.Errorf("%w: %q", ErrInvalidFormat, s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return Amount{}, fmt.Errorf("%w: %q", ErrInvalidFormat, s)
		}
	}
	if len(s) > 1 && s[0] == '0' {
		return Amount{}, fmt.Errorf("%w: leading zeros in %q", ErrInvalidFormat, s)
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return Amount{}, fmt.Errorf("%w: %q", ErrInvalidFormat, s)
	}
	if v.Cmp(MaxBaseUnits) > 0 {
		return Amount{}, ErrOverflow
	}
	return Amount{v: v}, nil
}

// MustParse is for constants in tests and fixtures only.
func MustParse(s string) Amount {
	a, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return a
}

func (a Amount) big() *big.Int {
	if a.v == nil {
		return new(big.Int)
	}
	return a.v
}

// String returns the canonical decimal representation.
func (a Amount) String() string { return a.big().String() }

// IsZero reports whether the amount is zero.
func (a Amount) IsZero() bool { return a.big().Sign() == 0 }

// Cmp compares a and b: -1 if a<b, 0 if equal, +1 if a>b.
func (a Amount) Cmp(b Amount) int { return a.big().Cmp(b.big()) }

// Add returns a+b or ErrOverflow.
func (a Amount) Add(b Amount) (Amount, error) {
	r := new(big.Int).Add(a.big(), b.big())
	if r.Cmp(MaxBaseUnits) > 0 {
		return Amount{}, ErrOverflow
	}
	return Amount{v: r}, nil
}

// Sub returns a-b or ErrNegative when b>a.
func (a Amount) Sub(b Amount) (Amount, error) {
	if a.Cmp(b) < 0 {
		return Amount{}, ErrNegative
	}
	return Amount{v: new(big.Int).Sub(a.big(), b.big())}, nil
}

// Min returns the smaller of a and b.
func Min(a, b Amount) Amount {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

// Sum adds all amounts with overflow checking.
func Sum(xs ...Amount) (Amount, error) {
	total := Zero()
	var err error
	for _, x := range xs {
		if total, err = total.Add(x); err != nil {
			return Amount{}, err
		}
	}
	return total, nil
}

// MarshalJSON encodes the amount as a JSON string.
func (a Amount) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

// UnmarshalJSON accepts only a JSON string in canonical form. Numbers are
// rejected to prevent precision loss in clients that produced them.
func (a *Amount) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("%w: amounts must be JSON strings", ErrInvalidFormat)
	}
	parsed, err := Parse(s)
	if err != nil {
		return err
	}
	*a = parsed
	return nil
}

// Value implements driver.Valuer, storing the amount as text for NUMERIC columns.
func (a Amount) Value() (driver.Value, error) {
	return a.String(), nil
}

// Scan implements sql.Scanner from text, bytes, or integer representations.
func (a *Amount) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*a = Zero()
		return nil
	case string:
		p, err := Parse(v)
		if err != nil {
			return err
		}
		*a = p
		return nil
	case []byte:
		p, err := Parse(string(v))
		if err != nil {
			return err
		}
		*a = p
		return nil
	case int64:
		if v < 0 {
			return ErrNegative
		}
		*a = FromUint64(uint64(v))
		return nil
	default:
		return fmt.Errorf("%w: unsupported scan type %T", ErrInvalidFormat, src)
	}
}
