package money

import (
	"encoding/json"
	"math"
	"testing"
)

func TestAmountJSONDecimalStringSerialization(t *testing.T) {
	type payload struct {
		A Amount `json:"a"`
		B Amount `json:"b"`
	}
	in := payload{A: 123456789012345, B: -500}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"a":"123456789012345","b":"-500"}`
	if string(raw) != want {
		t.Fatalf("got %s, want %s", string(raw), want)
	}
	var out payload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("roundtrip mismatch: got %+v, want %+v", out, in)
	}

	// Must reject floating point numbers explicitly.
	var bad payload
	if err := json.Unmarshal([]byte(`{"a":12.34}`), &bad); err == nil {
		t.Fatal("expected error unmarshaling float into Amount")
	}
}

func TestCheckedArithmeticAndRounding(t *testing.T) {
	if _, err := CheckedAdd(Amount(math.MaxInt64), 1); err != ErrOverflow {
		t.Fatalf("expected ErrOverflow on add, got %v", err)
	}
	if _, err := CheckedSub(Amount(math.MinInt64), 1); err != ErrOverflow {
		t.Fatalf("expected ErrOverflow on sub, got %v", err)
	}
	if _, err := CheckedSubNonNegative(100, 101); err != ErrNegativeAmount {
		t.Fatalf("expected ErrNegativeAmount, got %v", err)
	}

	// Floor vs Ceil basis points rounding: 333 * 5% (500 bps) = 16.65 -> floor=16, ceil=17
	fl, err := MulBpsFloor(333, 500)
	if err != nil || fl != 16 {
		t.Fatalf("MulBpsFloor(333, 500) = %v, %v; want 16", fl, err)
	}
	cl, err := MulBpsCeil(333, 500)
	if err != nil || cl != 17 {
		t.Fatalf("MulBpsCeil(333, 500) = %v, %v; want 17", cl, err)
	}

	// Pro-rata floor: 1000 * 1 / 3 = 333
	pr, err := ProRataFloor(1000, 1, 3)
	if err != nil || pr != 333 {
		t.Fatalf("ProRataFloor = %v, %v; want 333", pr, err)
	}

	// USD cents -> USDC 6-decimal base units at 1:1 (10000 bps)
	usdc, err := ConvertUSDMinorToUSDCBase(1000, 10000) // $10.00 -> 10,000,000 base units
	if err != nil || usdc != 10_000_000 {
		t.Fatalf("ConvertUSDMinorToUSDCBase(1000, 10000) = %v, %v; want 10000000", usdc, err)
	}
	if got := FormatDecimal(usdc, DecimalsUSDC); got != "10.000000" {
		t.Fatalf("FormatDecimal = %q, want 10.000000", got)
	}
}

func TestValidatePayoutDestination(t *testing.T) {
	dec, err := ValidatePayoutDestination("USDC", "base", "0x71C7656EC7ab88b098defB751B7401B5f6d8976F")
	if err != nil || dec != DecimalsUSDC {
		t.Fatalf("valid destination failed: %v", err)
	}
	if _, err := ValidatePayoutDestination("USDT", "base", "0x71C7656EC7ab88b098defB751B7401B5f6d8976F"); err == nil {
		t.Fatal("expected unsupported asset error")
	}
	if _, err := ValidatePayoutDestination("USDC", "ethereum", "0x71C7656EC7ab88b098defB751B7401B5f6d8976F"); err == nil {
		t.Fatal("expected unsupported network error")
	}
	if _, err := ValidatePayoutDestination("USDC", "base", "not-an-address"); err == nil {
		t.Fatal("expected invalid address error")
	}
	if _, err := ValidatePayoutDestination("USDC", "base", "0x0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("expected zero address rejection")
	}
}
