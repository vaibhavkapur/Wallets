package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseUnitsMatchesEngineRules(t *testing.T) {
	for _, ok := range []string{"0", "1", "10000", strings.Repeat("9", 38), "170141183460469231731687303715884105727"} {
		if _, err := ParseUnits(ok); err != nil {
			t.Errorf("%q should parse: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-1", "+1", "01", "1.0", "1e3", " 1", "170141183460469231731687303715884105728", strings.Repeat("9", 40)} {
		if _, err := ParseUnits(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestUnitsRejectJSONNumbers(t *testing.T) {
	var a Amount
	if err := json.Unmarshal([]byte(`{"asset":{"code":"USD","decimals":2},"base_units":100}`), &a); err == nil {
		t.Error("a JSON number must be rejected")
	}
	if err := json.Unmarshal([]byte(`{"asset":{"code":"USD","decimals":2},"base_units":"100"}`), &a); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(a)
	if err != nil || string(out) != `{"asset":{"code":"USD","decimals":2},"base_units":"100"}` {
		t.Errorf("marshal: %s %v", out, err)
	}
	if _, err := json.Marshal(Units("01")); err == nil {
		t.Error("a non-canonical value must not be emitted")
	}
}

func TestAssetEquality(t *testing.T) {
	base := "base"
	eth := "ethereum"
	usd := AssetRef{Code: "USD", Decimals: 2}
	if !usd.Equal(AssetRef{Code: "USD", Decimals: 2}) || usd.Equal(AssetRef{Code: "USD", Decimals: 6}) {
		t.Error("fiat equality")
	}
	a := AssetRef{Code: "USDC", Network: &base, Decimals: 6}
	if !a.Equal(AssetRef{Code: "USDC", Network: &base, Decimals: 6}) || a.Equal(AssetRef{Code: "USDC", Network: &eth, Decimals: 6}) || a.Equal(usd) {
		t.Error("stablecoin equality")
	}
}

func TestDecodeStrict(t *testing.T) {
	var b Basket
	if err := DecodeStrict([]byte(`{"basket_id":"b","merchant_id":"m","basket_hash":"h","extra":1}`), &b); err == nil {
		t.Error("unknown fields must be rejected")
	}
	if err := DecodeStrict([]byte(`{"basket_id":"b","merchant_id":"m","basket_hash":"h"} {}`), &b); err == nil {
		t.Error("trailing data must be rejected")
	}
}
