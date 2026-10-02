package money

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseCanonical(t *testing.T) {
	good := []string{"0", "1", "1000000", "340282366920938463463374607431768211455"}
	for _, s := range good {
		a, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", s, err)
		}
		if a.String() != s {
			t.Fatalf("round trip %q -> %q", s, a.String())
		}
	}
	bad := map[string]error{
		"":    ErrInvalidFormat,
		"-1":  ErrInvalidFormat,
		"+1":  ErrInvalidFormat,
		"01":  ErrInvalidFormat,
		"1.5": ErrInvalidFormat,
		"1e6": ErrInvalidFormat,
		" 1":  ErrInvalidFormat,
		"１":   ErrInvalidFormat, // full-width digit
		"340282366920938463463374607431768211456":  ErrOverflow,
		"1000000000000000000000000000000000000000": ErrInvalidFormat, // 40 digits
	}
	for s, want := range bad {
		_, err := Parse(s)
		if !errors.Is(err, want) {
			t.Fatalf("Parse(%q) = %v, want %v", s, err, want)
		}
	}
}

func TestCheckedArithmetic(t *testing.T) {
	max := MustParse("340282366920938463463374607431768211455")
	if _, err := max.Add(FromUint64(1)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
	if _, err := FromUint64(1).Sub(FromUint64(2)); !errors.Is(err, ErrNegative) {
		t.Fatalf("expected negative, got %v", err)
	}
	s, err := Sum(FromUint64(1), FromUint64(2), FromUint64(3))
	if err != nil || s.String() != "6" {
		t.Fatalf("Sum = %v, %v", s, err)
	}
	if Zero().Cmp(Amount{}) != 0 {
		t.Fatal("zero values must compare equal")
	}
}

func TestJSONStringsOnly(t *testing.T) {
	type wrapper struct {
		A Amount `json:"a"`
	}
	var w wrapper
	if err := json.Unmarshal([]byte(`{"a":"123"}`), &w); err != nil || w.A.String() != "123" {
		t.Fatalf("unmarshal string: %v %v", w, err)
	}
	if err := json.Unmarshal([]byte(`{"a":123}`), &w); err == nil {
		t.Fatal("JSON numbers must be rejected")
	}
	out, _ := json.Marshal(wrapper{A: FromUint64(5)})
	if string(out) != `{"a":"5"}` {
		t.Fatalf("marshal = %s", out)
	}
}

func TestSQLRoundTrip(t *testing.T) {
	var a Amount
	if err := a.Scan("42"); err != nil || a.String() != "42" {
		t.Fatalf("scan string: %v %v", a, err)
	}
	if err := a.Scan([]byte("7")); err != nil || a.String() != "7" {
		t.Fatalf("scan bytes: %v %v", a, err)
	}
	if err := a.Scan(int64(-1)); !errors.Is(err, ErrNegative) {
		t.Fatalf("negative scan: %v", err)
	}
	v, _ := FromUint64(9).Value()
	if v != "9" {
		t.Fatalf("Value = %v", v)
	}
}
