package app

import (
	"math"
	"testing"
	"time"
)

func TestBurnRateWindowed(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.state.Seeded = true
	now := time.Now()

	// two rate deltas in the 10-minute window totaling $2.00
	a.rateMu.Lock()
	a.rateWindow = []RatePoint{
		{Time: now.Add(-5 * time.Minute), Cost: 1.0},
		{Time: now.Add(-2 * time.Minute), Cost: 1.0},
	}
	a.rateMu.Unlock()

	// $2.00 in 10 minutes -> $2.00 * 6 = $12/hour
	rate := a.BurnRate()
	if math.Abs(rate-12.0) > 0.01 {
		t.Errorf("burn rate = %v, want 12.00", rate)
	}
}

func TestBurnRateIdle(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.state.Seeded = true
	// rate deltas outside the 10-minute window contribute nothing
	a.rateMu.Lock()
	a.rateWindow = []RatePoint{
		{Time: time.Now().Add(-15 * time.Minute), Cost: 50.0},
	}
	a.rateMu.Unlock()

	if rate := a.BurnRate(); rate != 0 {
		t.Errorf("burn rate should be 0 when idle, got %v", rate)
	}
}
