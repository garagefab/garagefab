// Package store verifies that stored timestamps are wall-clock UTC (spec §8: clock changes).
package store

import (
	"strings"
	"testing"
	"time"
)

// TestFormatTime_WallClockUTC verifies that timestamps are always serialized in UTC and
// round-trip regardless of the caller's local zone (spec §8).
func TestFormatTime_WallClockUTC(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*3600)
	tm := time.Date(2026, 1, 2, 3, 4, 5, 0, loc)

	got := formatTime(tm)
	if !strings.HasSuffix(got, "Z") {
		t.Fatalf("expected a UTC (Z-suffixed) timestamp, got %q", got)
	}
	parsed := parseTime(got)
	if !parsed.Equal(tm) {
		t.Fatalf("round-trip mismatch: want %s, got %s", tm.UTC(), parsed.UTC())
	}
	if parsed.Location() != time.UTC {
		t.Fatalf("expected parsed time in UTC, got %s", parsed.Location())
	}
}
