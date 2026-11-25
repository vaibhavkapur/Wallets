package recurring

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Clock abstracts wall-clock time so tests and sandbox controls can advance time deterministically.
type Clock interface {
	Now() time.Time
}

// RealClock returns the current UTC system time.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

// FakeClock is a thread-safe controllable clock for deterministic testing and sandbox demos.
type FakeClock struct {
	mu  sync.RWMutex
	now time.Time
}

func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start.UTC()}
}

func (c *FakeClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t.UTC()
}

func (c *FakeClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// ValidateScheduleSpec enforces strict separation between calendar schedules and
// minimum-interval schedules, valid IANA timezones, and explicit DST/leap policies.
func ValidateScheduleSpec(spec *ScheduleSpec) error {
	if spec == nil {
		return problem(400, "invalid_schedule", "Schedule is required.")
	}
	if spec.Timezone == "" {
		return problem(400, "invalid_timezone", "An IANA timezone (e.g. America/New_York) is required.")
	}
	if _, err := time.LoadLocation(spec.Timezone); err != nil {
		return problem(400, "invalid_timezone", fmt.Sprintf("Unknown IANA timezone %q.", spec.Timezone))
	}
	if spec.LocalHour < 0 || spec.LocalHour > 23 || spec.LocalMinute < 0 || spec.LocalMinute > 59 {
		return problem(400, "invalid_time", "Local hour must be 0-23 and local minute must be 0-59.")
	}
	if spec.DSTPolicy == "" {
		spec.DSTPolicy = DSTPolicyShiftForwardFirst
	}
	if spec.DSTPolicy != DSTPolicyShiftForwardFirst && spec.DSTPolicy != DSTPolicyShiftForwardSecond {
		return problem(400, "invalid_dst_policy", "Unsupported DST policy.")
	}
	if spec.LeapDayPolicy == "" {
		spec.LeapDayPolicy = LeapDayPolicyClampMonthEnd
	}
	if spec.LeapDayPolicy != LeapDayPolicyClampMonthEnd && spec.LeapDayPolicy != LeapDayPolicyCarryNextDay {
		return problem(400, "invalid_leap_policy", "Unsupported leap-day policy.")
	}
	if spec.MinIntervalDays < 0 || spec.MinIntervalDays > 365 {
		return problem(400, "invalid_min_interval", "Minimum interval must be between 0 and 365 days.")
	}
	if spec.ProposalTTLMinutes == 0 {
		spec.ProposalTTLMinutes = 24 * 60
	}
	if spec.ProposalTTLMinutes < 5 || spec.ProposalTTLMinutes > 10080 {
		return problem(400, "invalid_proposal_ttl", "Proposal expiry must be between 5 minutes and 7 days.")
	}

	switch spec.Kind {
	case ScheduleKindCalendar:
		if spec.IntervalDays != 0 {
			return problem(400, "schedule_kind_conflict", "Calendar schedules must not set interval_days; use calendar or interval explicitly.")
		}
		switch spec.CalendarCadence {
		case CalendarCadenceWeekly:
			if spec.DayOfWeek == nil || *spec.DayOfWeek < 0 || *spec.DayOfWeek > 6 {
				return problem(400, "invalid_day_of_week", "Weekly calendar schedules require day_of_week between 0 (Sunday) and 6 (Saturday).")
			}
			if spec.DayOfMonth != nil {
				return problem(400, "schedule_kind_conflict", "Weekly calendar schedules must not set day_of_month.")
			}
		case CalendarCadenceMonthly:
			if spec.DayOfMonth == nil || *spec.DayOfMonth < 1 || *spec.DayOfMonth > 31 {
				return problem(400, "invalid_day_of_month", "Monthly calendar schedules require day_of_month between 1 and 31.")
			}
			if spec.DayOfWeek != nil {
				return problem(400, "schedule_kind_conflict", "Monthly calendar schedules must not set day_of_week.")
			}
		default:
			return problem(400, "invalid_cadence", "Calendar schedules require calendar_cadence of weekly or monthly.")
		}
	case ScheduleKindInterval:
		if spec.CalendarCadence != "" || spec.DayOfWeek != nil || spec.DayOfMonth != nil {
			return problem(400, "schedule_kind_conflict", "Interval schedules must not set calendar_cadence, day_of_week, or day_of_month.")
		}
		if spec.IntervalDays < 1 || spec.IntervalDays > 365 {
			return problem(400, "invalid_interval_days", "Interval schedules require interval_days between 1 and 365.")
		}
		if spec.MinIntervalDays == 0 {
			spec.MinIntervalDays = spec.IntervalDays
		}
	default:
		return problem(400, "invalid_schedule_kind", "Schedule kind must be 'calendar' or 'interval'.")
	}
	return nil
}

// ResolvedLocalTime captures the UTC instant and metadata for a local schedule point.
type ResolvedLocalTime struct {
	UTC          time.Time
	LocalDisplay string
	DSTGap       bool
	DSTRepeated  bool
	PickedSecond bool
}

// ResolveLocalTime resolves a local civil timestamp (year, month, day, hour, minute) in loc
// according to dstPolicy. It handles normal times, spring-forward DST gaps, and fall-back
// repeated hours deterministically.
func ResolveLocalTime(loc *time.Location, year int, month time.Month, day, hour, minute int, policy DSTPolicy) ResolvedLocalTime {
	naiveUTC := time.Date(year, month, day, hour, minute, 0, 0, time.UTC)

	// Sample offsets around the target day to discover all active UTC offsets.
	offsets := map[int]struct{}{}
	for _, delta := range []time.Duration{-24 * time.Hour, -6 * time.Hour, 0, 6 * time.Hour, 24 * time.Hour} {
		_, off := naiveUTC.Add(delta).In(loc).Zone()
		offsets[off] = struct{}{}
	}

	var matches []time.Time
	for off := range offsets {
		cand := naiveUTC.Add(-time.Duration(off) * time.Second)
		inLoc := cand.In(loc)
		if inLoc.Year() == year && inLoc.Month() == month && inLoc.Day() == day &&
			inLoc.Hour() == hour && inLoc.Minute() == minute && inLoc.Second() == 0 {
			matches = append(matches, cand.UTC())
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Before(matches[j]) })

	if len(matches) == 1 {
		u := matches[0]
		return ResolvedLocalTime{
			UTC:          u,
			LocalDisplay: u.In(loc).Format("2006-01-02 15:04 MST"),
		}
	}
	if len(matches) >= 2 {
		picked := matches[0]
		pickedSecond := false
		if policy == DSTPolicyShiftForwardSecond {
			picked = matches[len(matches)-1]
			pickedSecond = true
		}
		return ResolvedLocalTime{
			UTC:          picked,
			LocalDisplay: picked.In(loc).Format("2006-01-02 15:04 MST"),
			DSTRepeated:  true,
			PickedSecond: pickedSecond,
		}
	}

	// Zero matches => spring-forward DST gap (e.g., 02:30 when clocks jump 02:00 -> 03:00).
	// Find the exact first valid UTC second after the spring-forward transition.
	_, offBefore := naiveUTC.Add(-12 * time.Hour).In(loc).Zone()
	_, offAfter := naiveUTC.Add(12 * time.Hour).In(loc).Zone()

	lo := naiveUTC.Add(-time.Duration(offBefore)*time.Second - 2*time.Hour).Unix()
	hi := naiveUTC.Add(-time.Duration(offAfter)*time.Second + 2*time.Hour).Unix()
	if lo > hi {
		lo, hi = hi, lo
	}
	// Binary search for the earliest instant where zone offset == offAfter.
	for lo < hi {
		mid := lo + (hi-lo)/2
		_, offMid := time.Unix(mid, 0).UTC().In(loc).Zone()
		if offMid == offAfter {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	resolved := time.Unix(lo, 0).UTC()
	return ResolvedLocalTime{
		UTC:          resolved,
		LocalDisplay: resolved.In(loc).Format("2006-01-02 15:04 MST"),
		DSTGap:       true,
	}
}

func isLeapYear(y int) bool {
	return (y%4 == 0 && y%100 != 0) || (y%400 == 0)
}

func daysInMonth(year int, month time.Month) int {
	switch month {
	case time.April, time.June, time.September, time.November:
		return 30
	case time.February:
		if isLeapYear(year) {
			return 29
		}
		return 28
	default:
		return 31
	}
}

// ResolveCalendarDate resolves a target day-of-month (1..31) in a given year/month,
// honoring leap-day and month-end policies.
func ResolveCalendarDate(year int, month time.Month, targetDay int, leapPolicy LeapDayPolicy) (int, time.Month, int) {
	dim := daysInMonth(year, month)
	if targetDay <= dim {
		return year, month, targetDay
	}
	if month == time.February && targetDay == 29 && leapPolicy == LeapDayPolicyCarryNextDay {
		return year, time.March, 1
	}
	return year, month, dim
}

// NextRunAfter computes the next scheduled UTC instant strictly after `afterUTC`.
func NextRunAfter(spec ScheduleSpec, afterUTC time.Time) (ResolvedLocalTime, error) {
	loc, err := time.LoadLocation(spec.Timezone)
	if err != nil {
		return ResolvedLocalTime{}, err
	}
	afterLocal := afterUTC.In(loc)

	switch spec.Kind {
	case ScheduleKindInterval:
		// Advance by interval_days from the reference local date, preserving intended local hour/minute.
		base := time.Date(afterLocal.Year(), afterLocal.Month(), afterLocal.Day(), 0, 0, 0, 0, time.UTC)
		for offset := 0; offset <= spec.IntervalDays+3; offset++ {
			day := base.AddDate(0, 0, offset)
			cand := ResolveLocalTime(loc, day.Year(), day.Month(), day.Day(), spec.LocalHour, spec.LocalMinute, spec.DSTPolicy)
			if offset == 0 && cand.UTC.After(afterUTC) {
				// For initial scheduling right after creation, allow today if time hasn't passed,
				// otherwise move forward by IntervalDays.
				continue
			}
			if offset >= spec.IntervalDays && cand.UTC.After(afterUTC) {
				return cand, nil
			}
		}
		target := base.AddDate(0, 0, spec.IntervalDays)
		return ResolveLocalTime(loc, target.Year(), target.Month(), target.Day(), spec.LocalHour, spec.LocalMinute, spec.DSTPolicy), nil

	case ScheduleKindCalendar:
		if spec.CalendarCadence == CalendarCadenceWeekly {
			targetDow := time.Weekday(*spec.DayOfWeek)
			base := time.Date(afterLocal.Year(), afterLocal.Month(), afterLocal.Day(), 0, 0, 0, 0, time.UTC)
			for delta := 0; delta <= 14; delta++ {
				d := base.AddDate(0, 0, delta)
				if d.Weekday() != targetDow {
					continue
				}
				cand := ResolveLocalTime(loc, d.Year(), d.Month(), d.Day(), spec.LocalHour, spec.LocalMinute, spec.DSTPolicy)
				if cand.UTC.After(afterUTC) {
					return cand, nil
				}
			}
		}

		if spec.CalendarCadence == CalendarCadenceMonthly {
			targetDay := *spec.DayOfMonth
			y, m := afterLocal.Year(), afterLocal.Month()
			for step := 0; step < 24; step++ {
				ry, rm, rd := ResolveCalendarDate(y, m, targetDay, spec.LeapDayPolicy)
				cand := ResolveLocalTime(loc, ry, rm, rd, spec.LocalHour, spec.LocalMinute, spec.DSTPolicy)
				if cand.UTC.After(afterUTC) {
					return cand, nil
				}
				m++
				if m > time.December {
					m = time.January
					y++
				}
			}
		}
	}

	return ResolvedLocalTime{}, fmt.Errorf("unable to compute next run for schedule kind %q", spec.Kind)
}

// ComputeInitialRun computes the first scheduled UTC instant for a newly created rule.
func ComputeInitialRun(spec ScheduleSpec, nowUTC time.Time) (ResolvedLocalTime, error) {
	if spec.Kind == ScheduleKindInterval {
		// For interval schedules, schedule first run on the soonest upcoming local HH:MM
		// (today if still in the future, else tomorrow) so customers see an immediate cycle.
		loc, err := time.LoadLocation(spec.Timezone)
		if err != nil {
			return ResolvedLocalTime{}, err
		}
		localNow := nowUTC.In(loc)
		today := ResolveLocalTime(loc, localNow.Year(), localNow.Month(), localNow.Day(), spec.LocalHour, spec.LocalMinute, spec.DSTPolicy)
		if today.UTC.After(nowUTC) {
			return today, nil
		}
		tomorrow := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
		return ResolveLocalTime(loc, tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), spec.LocalHour, spec.LocalMinute, spec.DSTPolicy), nil
	}
	return NextRunAfter(spec, nowUTC)
}

// DueWindow summarizes all due slots between a rule's stored NextRunAtUTC and nowUTC.
// If an outage spans multiple slots, MissedSlotsCount > 1 and only ONE latest slot
// is returned for recovery so catch-up purchases are never generated automatically.
type DueWindow struct {
	HasDue           bool
	LatestDueUTC     time.Time
	LatestLocalDisp  string
	MissedSlotsCount int
	NextFutureUTC    time.Time
}

// ComputeDueWindow evaluates whether a rule is due at nowUTC and coalesces any multi-slot outage.
func ComputeDueWindow(spec ScheduleSpec, storedNextUTC, nowUTC time.Time) (DueWindow, error) {
	if storedNextUTC.After(nowUTC) {
		return DueWindow{HasDue: false, NextFutureUTC: storedNextUTC}, nil
	}
	loc, err := time.LoadLocation(spec.Timezone)
	if err != nil {
		return DueWindow{}, err
	}

	count := 1
	latest := storedNextUTC.UTC()
	cursor := latest
	for guard := 0; guard < 1000; guard++ {
		nextRes, err := NextRunAfter(spec, cursor)
		if err != nil {
			return DueWindow{}, err
		}
		if nextRes.UTC.After(nowUTC) {
			return DueWindow{
				HasDue:           true,
				LatestDueUTC:     latest,
				LatestLocalDisp:  latest.In(loc).Format("2006-01-02 15:04 MST"),
				MissedSlotsCount: count,
				NextFutureUTC:    nextRes.UTC,
			}, nil
		}
		latest = nextRes.UTC
		cursor = latest
		count++
	}
	nextRes, err := NextRunAfter(spec, nowUTC)
	if err != nil {
		return DueWindow{}, err
	}
	return DueWindow{
		HasDue:           true,
		LatestDueUTC:     latest,
		LatestLocalDisp:  latest.In(loc).Format("2006-01-02 15:04 MST"),
		MissedSlotsCount: count,
		NextFutureUTC:    nextRes.UTC,
	}, nil
}

// SlotKey produces a deterministic logical identifier for a rule's scheduled instant.
func SlotKey(spec ScheduleSpec, scheduledUTC time.Time) string {
	return fmt.Sprintf("%s:%s", spec.Kind, scheduledUTC.UTC().Format("2006-01-02T15:04:05Z"))
}

// BudgetPeriodKey returns YYYY-MM in the customer's rule timezone for monthly budget accounting.
func BudgetPeriodKey(timezone string, atUTC time.Time) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return atUTC.UTC().Format("2006-01")
	}
	return atUTC.In(loc).Format("2006-01")
}
