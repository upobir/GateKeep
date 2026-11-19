package db

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	UnitSeconds = "seconds"
	UnitMinutes = "minutes"
	UnitHours   = "hours"
	UnitDays    = "days"
	UnitCalls   = "calls"

	maxRecentCount = 1_000_000
)

// RecentWindow is the last n units of finished tool calls that stay on the
// approval queue. A time unit snaps to its own boundary: the window starts at
// the beginning of the current unit and includes the previous n-1 units.
type RecentWindow struct {
	Count int
	Unit  string
}

func DefaultRecentWindow() RecentWindow {
	return RecentWindow{Count: 1, Unit: UnitHours}
}

func (w RecentWindow) Valid() bool {
	if w.Count < 1 || w.Count > maxRecentCount {
		return false
	}
	switch w.Unit {
	case UnitSeconds, UnitMinutes, UnitHours, UnitDays, UnitCalls:
		return true
	default:
		return false
	}
}

func ParseRecentWindow(value string) (RecentWindow, error) {
	countText, unit, ok := strings.Cut(strings.TrimSpace(value), " ")
	if !ok {
		return RecentWindow{}, fmt.Errorf("recent window must be a count and a unit")
	}
	count, err := strconv.Atoi(countText)
	if err != nil {
		return RecentWindow{}, fmt.Errorf("recent window count must be a positive integer")
	}
	window := RecentWindow{Count: count, Unit: unit}
	if !window.Valid() {
		return RecentWindow{}, fmt.Errorf("recent window must be a positive count of seconds, minutes, hours, days, or calls")
	}
	return window, nil
}

func (w RecentWindow) String() string {
	return strconv.Itoa(w.Count) + " " + w.Unit
}

// Start is the inclusive beginning of a time window in now's location.
// The second result is false when the window counts tool calls instead of time.
func (w RecentWindow) Start(now time.Time) (time.Time, bool) {
	if !w.Valid() || w.Unit == UnitCalls {
		return time.Time{}, false
	}
	loc := now.Location()
	year, month, day := now.Date()
	switch w.Unit {
	case UnitSeconds:
		snapped := time.Date(year, month, day, now.Hour(), now.Minute(), now.Second(), 0, loc)
		return snapped.Add(-time.Duration(w.Count-1) * time.Second), true
	case UnitMinutes:
		snapped := time.Date(year, month, day, now.Hour(), now.Minute(), 0, 0, loc)
		return snapped.Add(-time.Duration(w.Count-1) * time.Minute), true
	case UnitHours:
		snapped := time.Date(year, month, day, now.Hour(), 0, 0, 0, loc)
		return snapped.Add(-time.Duration(w.Count-1) * time.Hour), true
	default:
		snapped := time.Date(year, month, day, 0, 0, 0, 0, loc)
		return snapped.AddDate(0, 0, -(w.Count - 1)), true
	}
}
