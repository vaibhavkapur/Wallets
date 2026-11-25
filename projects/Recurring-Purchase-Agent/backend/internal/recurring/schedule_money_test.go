package recurring

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestMoneyDecimalStringAndCheckedArithmetic(t *testing.T) {
	m := USD(2499)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Must serialize amount_minor as a JSON string, never a bare number.
	wantSubstr := `"amount_minor":"2499"`
	if string(b) == "" || !containsString(string(b), wantSubstr) {
		t.Fatalf("expected %s in %s", wantSubstr, string(b))
	}

	// Reject bare numeric minor units.
	var bad Money
	if err := json.Unmarshal([]byte(`{"amount_minor":2499,"asset":"USD","network":"fiat","decimals":2}`), &bad); err == nil {
		t.Fatalf("expected bare number to fail unmarshal")
	}

	// Large integer (> 2^53) round-trips without float loss.
	bigVal := Money{AmountMinor: MinorUnits(9007199254740995), Asset: "USDC", Network: "base", Decimals: 6}
	encoded, err := json.Marshal(bigVal)
	if err != nil {
		t.Fatalf("marshal big: %v", err)
	}
	var decoded Money
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal big: %v", err)
	}
	if decoded.AmountMinor != 9007199254740995 {
		t.Fatalf("expected exact 9007199254740995, got %d", decoded.AmountMinor)
	}

	// Overflow detection.
	if _, err := USD(math.MaxInt64).Add(USD(1)); err == nil {
		t.Fatalf("expected overflow error on MaxInt64 + 1")
	}
	if _, err := USD(math.MaxInt64).MulInt(2); err == nil {
		t.Fatalf("expected overflow error on MaxInt64 * 2")
	}

	// Half-up basis points rounding: 100 cents * 825 bps = 8.25 -> 8; 105 cents * 825 bps = 8.6625 -> 9.
	tax1, err := USD(100).MulBpsHalfUp(825)
	if err != nil || tax1.AmountMinor != 8 {
		t.Fatalf("expected 8 cents, got %v (err=%v)", tax1.AmountMinor, err)
	}
	tax2, err := USD(105).MulBpsHalfUp(825)
	if err != nil || tax2.AmountMinor != 9 {
		t.Fatalf("expected 9 cents, got %v (err=%v)", tax2.AmountMinor, err)
	}
}

func TestScheduleDSTGapAndRepeatedHours(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}

	// 1. Spring-forward gap: 2026-03-08 02:30 does not exist in America/New_York (jumps 02:00 -> 03:00 EDT).
	gap := ResolveLocalTime(loc, 2026, time.March, 8, 2, 30, DSTPolicyShiftForwardFirst)
	if !gap.DSTGap {
		t.Fatalf("expected DSTGap=true for 2026-03-08 02:30 America/New_York")
	}
	// First valid instant after gap is 03:00 EDT = 07:00 UTC.
	wantGapUTC := time.Date(2026, time.March, 8, 7, 0, 0, 0, time.UTC)
	if !gap.UTC.Equal(wantGapUTC) {
		t.Fatalf("expected %s, got %s (%s)", wantGapUTC, gap.UTC, gap.LocalDisplay)
	}

	// 2. Fall-back repeated hour: 2026-11-01 01:30 occurs twice (EDT = 05:30 UTC, EST = 06:30 UTC).
	first := ResolveLocalTime(loc, 2026, time.November, 1, 1, 30, DSTPolicyShiftForwardFirst)
	if !first.DSTRepeated || first.PickedSecond {
		t.Fatalf("expected repeated first occurrence, got %+v", first)
	}
	wantFirstUTC := time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC)
	if !first.UTC.Equal(wantFirstUTC) {
		t.Fatalf("expected first %s, got %s", wantFirstUTC, first.UTC)
	}

	second := ResolveLocalTime(loc, 2026, time.November, 1, 1, 30, DSTPolicyShiftForwardSecond)
	if !second.DSTRepeated || !second.PickedSecond {
		t.Fatalf("expected repeated second occurrence, got %+v", second)
	}
	wantSecondUTC := time.Date(2026, time.November, 1, 6, 30, 0, 0, time.UTC)
	if !second.UTC.Equal(wantSecondUTC) {
		t.Fatalf("expected second %s, got %s", wantSecondUTC, second.UTC)
	}
}

func TestScheduleLeapDatesAndMonthEnd(t *testing.T) {
	// Feb 29 in non-leap year 2027: clamp vs carry March 1.
	y, m, d := ResolveCalendarDate(2027, time.February, 29, LeapDayPolicyClampMonthEnd)
	if y != 2027 || m != time.February || d != 28 {
		t.Fatalf("expected 2027-02-28, got %d-%02d-%02d", y, m, d)
	}
	y, m, d = ResolveCalendarDate(2027, time.February, 29, LeapDayPolicyCarryNextDay)
	if y != 2027 || m != time.March || d != 1 {
		t.Fatalf("expected 2027-03-01, got %d-%02d-%02d", y, m, d)
	}
	// Feb 29 in leap year 2028: exact Feb 29.
	y, m, d = ResolveCalendarDate(2028, time.February, 29, LeapDayPolicyClampMonthEnd)
	if y != 2028 || m != time.February || d != 29 {
		t.Fatalf("expected 2028-02-29, got %d-%02d-%02d", y, m, d)
	}
	// Day 31 in April clamps to April 30.
	y, m, d = ResolveCalendarDate(2026, time.April, 31, LeapDayPolicyClampMonthEnd)
	if y != 2026 || m != time.April || d != 30 {
		t.Fatalf("expected 2026-04-30, got %d-%02d-%02d", y, m, d)
	}
}

func TestScheduleKindStrictSeparation(t *testing.T) {
	day := 15
	mixed := ScheduleSpec{
		Kind:            ScheduleKindCalendar,
		Timezone:        "UTC",
		CalendarCadence: CalendarCadenceMonthly,
		DayOfMonth:      &day,
		IntervalDays:    14, // illegal on calendar schedule
		LocalHour:       9,
	}
	if err := ValidateScheduleSpec(&mixed); err == nil {
		t.Fatalf("expected error mixing calendar and interval fields")
	}

	mixed2 := ScheduleSpec{
		Kind:            ScheduleKindInterval,
		Timezone:        "UTC",
		CalendarCadence: CalendarCadenceWeekly, // illegal on interval schedule
		IntervalDays:    14,
		LocalHour:       9,
	}
	if err := ValidateScheduleSpec(&mixed2); err == nil {
		t.Fatalf("expected error mixing interval and calendar_cadence")
	}
}

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || func() bool {
		for i := 0; i <= len(haystack)-len(needle); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	}())
}
