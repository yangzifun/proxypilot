package api

import (
	"testing"
	"time"
)

func TestTrafficPeriodDefaultMonthly(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	start := trafficPeriodStart(now, 1, 0)
	if want := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC); !start.Equal(want) {
		t.Fatalf("period start = %s, want %s", start, want)
	}
	next := trafficNextReset(start, 1, 0)
	if want := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("next reset = %s, want %s", next, want)
	}
}

func TestTrafficPeriodCustomReset(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, loc)
	start := trafficPeriodStart(now, 15, 8)
	if want := time.Date(2026, 10, 15, 8, 0, 0, 0, loc); !start.Equal(want) {
		t.Fatalf("period start = %s, want %s", start, want)
	}
	next := trafficNextReset(start, 15, 8)
	if want := time.Date(2026, 11, 15, 8, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("next reset = %s, want %s", next, want)
	}

	now = time.Date(2026, 11, 15, 9, 0, 0, 0, loc)
	start = trafficPeriodStart(now, 15, 8)
	if want := time.Date(2026, 11, 15, 8, 0, 0, 0, loc); !start.Equal(want) {
		t.Fatalf("after reset period start = %s, want %s", start, want)
	}
}
