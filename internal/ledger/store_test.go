package ledger

import (
	"math"
	"testing"
)

func rec(mid, sid, prov, model string, cost float64, ts string) Record {
	return Record{
		MID: mid, SessionID: sid, Provider: prov, Model: model,
		TokensIn: 100, TokensOut: 50, CacheRead: 20, CacheWrite: 5,
		Cost: cost, Timestamp: ts,
	}
}

func almost(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPutIdempotentOnSameTokens(t *testing.T) {
	s := New()
	r := rec("m1", "s1", "acme", "sonnet", 1.0, "t1")
	if !s.Put(r) {
		t.Error("first insert should report changed")
	}
	if s.Put(r) {
		t.Error("same token counts must be idempotent (not changed)")
	}
	// changing only the cost (same token counts) is idempotent — totals are
	// recomputed by the caller, matching the original's change detection
	r2 := r
	r2.Cost = 2.0
	if s.Put(r2) {
		t.Error("same token counts must be idempotent regardless of cost")
	}
	// a real token update is reported as changed
	r3 := r
	r3.TokensIn = 200
	if !s.Put(r3) {
		t.Error("changed tokens should report changed")
	}
	if len(s.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(s.Messages))
	}
}

func TestRecomputeTotals(t *testing.T) {
	s := New()
	s.Put(rec("m1", "s1", "acme", "sonnet", 1.0, "2026-01-01 10:00:00"))
	s.Put(rec("m2", "s1", "acme", "sonnet", 2.0, "2026-01-01 10:01:00"))
	s.Put(rec("m3", "s2", "hub", "cheap", 0.5, "2026-01-01 10:02:00"))
	s.Recompute()

	almost(t, s.TotalCost, 3.5)
	almost(t, s.TotalSaved, 0)

	// per-model
	if s.PerModel["acme/sonnet"].Cost != 3.0 {
		t.Errorf("acme/sonnet cost = %v, want 3", s.PerModel["acme/sonnet"].Cost)
	}
	if s.PerModel["acme/sonnet"].Msgs != 2 {
		t.Errorf("acme/sonnet msgs = %v, want 2", s.PerModel["acme/sonnet"].Msgs)
	}

	// per-session
	if s.PerSession["s1"].Cost != 3.0 {
		t.Errorf("s1 cost = %v, want 3", s.PerSession["s1"].Cost)
	}
	if s.PerSession["s1"].Msgs != 2 {
		t.Errorf("s1 msgs = %v, want 2", s.PerSession["s1"].Msgs)
	}
	// session model tracks the most recent message
	if s.PerSession["s1"].Model != "acme/sonnet" {
		t.Errorf("s1 model = %q", s.PerSession["s1"].Model)
	}
}

func TestRecomputeCarriesPrunedCost(t *testing.T) {
	s := New()
	s.Put(rec("m1", "s1", "acme", "sonnet", 5.0, "2026-01-01 10:00:00"))
	s.Put(rec("m2", "s1", "acme", "sonnet", 7.0, "2026-01-01 10:01:00"))
	dropped := s.Prune(1)
	if dropped != 1 {
		t.Fatalf("prune dropped %d, want 1", dropped)
	}
	if s.PrunedCost != 5.0 {
		t.Errorf("pruned cost = %v, want 5", s.PrunedCost)
	}
	if len(s.Messages) != 1 {
		t.Fatalf("expected 1 left, got %d", len(s.Messages))
	}
	s.Recompute()
	// total must include pruned cost
	almost(t, s.TotalCost, 12.0)
}

func TestPruneFoldsSaved(t *testing.T) {
	s := New()
	s.Put(rec("m1", "s1", "acme", "free-model", 0.0, "t1"))
	r2 := rec("m2", "s1", "acme", "free-model", 0.0, "t2")
	r2.Saved = 3.0
	s.Put(r2)
	s.Prune(1)
	if s.PrunedSaved != 0.0 {
		t.Errorf("pruned saved = %v, want 0 (oldest)", s.PrunedSaved)
	}
}

func TestTripBaseline(t *testing.T) {
	s := New()
	s.Put(rec("m1", "s1", "acme", "sonnet", 5.0, "t1"))
	s.Recompute()
	// snapshot current total as the trip base
	s.TripBase = s.TotalCost
	s.Recompute()
	if s.TripCost != 0 {
		t.Errorf("trip cost after reset = %v, want 0", s.TripCost)
	}
	s.Put(rec("m2", "s1", "acme", "sonnet", 2.0, "t2"))
	s.Recompute()
	if s.TripCost != 2.0 {
		t.Errorf("trip cost = %v, want 2", s.TripCost)
	}
	if s.TotalCost != 7.0 {
		t.Errorf("total cost = %v, want 7", s.TotalCost)
	}
}

func TestPruneDoesNothingBelowCap(t *testing.T) {
	s := New()
	for i := 0; i < 5; i++ {
		s.Put(rec(string(rune('a'+i)), "s1", "acme", "x", float64(i), "t"))
	}
	if s.Prune(5) != 0 {
		t.Error("prune should not drop under cap")
	}
	if s.Prune(2) != 3 {
		t.Error("prune should drop 3 above cap")
	}
}

func TestRemoveNetsOutSpend(t *testing.T) {
	s := New()
	s.Put(rec("m1", "s1", "acme", "sonnet", 1.0, "2026-01-01 10:00:00"))
	s.Put(rec("m2", "s1", "acme", "sonnet", 2.0, "2026-01-01 10:01:00"))
	s.Recompute()
	almost(t, s.TotalCost, 3.0)

	// removing m1 must net its cost out of every aggregate
	removed, ok := s.Remove("m1")
	if !ok || removed.Cost != 1.0 {
		t.Fatalf("remove m1: ok=%v cost=%v", ok, removed.Cost)
	}
	almost(t, s.TotalCost, 2.0)
	if s.PerModel["acme/sonnet"].Cost != 2.0 {
		t.Errorf("per-model after remove = %v, want 2", s.PerModel["acme/sonnet"].Cost)
	}
	if s.PerSession["s1"].Cost != 2.0 {
		t.Errorf("per-session after remove = %v, want 2", s.PerSession["s1"].Cost)
	}

	// removing an unknown id is a no-op
	if _, ok := s.Remove("nope"); ok {
		t.Error("removing unknown id should report not-ok")
	}
}

func TestHuman(t *testing.T) {
	cases := map[int64]string{
		5:          "5",
		1500:       "1.5K",
		3400000:    "3.4M",
		1000000000: "1.0B",
	}
	for in, want := range cases {
		if got := Human(in); got != want {
			t.Errorf("Human(%d) = %s, want %s", in, got, want)
		}
	}
}

// TestTripResidueIsClamped reproduces the bug where the odometer showed
// $00000.00000 for TRIP while the log recorded real spend.
//
// Totals are sums of thousands of floats. The baseline captured by ResetTrip
// and the total recomputed on the next run differed in their last bits, so
// TripCost settled at ~4.5e-13 instead of 0 — and a real, tiny amount of new
// spend was indistinguishable from that residue.
func TestTripResidueIsClamped(t *testing.T) {
	s := New()
	for i, c := range []float64{0.1, 0.2, 0.3, 0.7, 1.3, 2.9} {
		s.Put(rec("m"+string(rune('a'+i)), "s1", "acme", "sonnet", c, "t1"))
	}

	// Reproduce the observed on-disk state: a TripBase persisted by an earlier
	// run that differs from the freshly recomputed total by float drift. The
	// live state file had total=378.50862081800034 against
	// trip_base=378.5086208179999, leaving TripCost at 4.5e-13, which rendered
	// as $00000.00000 and made real spend look like zero.
	s.Lock()
	s.TripBase = s.TotalCost - 4.5e-13
	s.TripSavedBase = s.TotalSaved - 4.5e-13
	s.Unlock()
	s.Recompute()

	if s.TripCost != 0 {
		t.Errorf("float residue must clamp to 0, got %v", s.TripCost)
	}
	if s.TripSaved != 0 {
		t.Errorf("float residue must clamp to 0, got %v", s.TripSaved)
	}

	// A baseline above the total (drift in the other direction) must not go
	// negative either.
	s.Lock()
	s.TripBase = s.TotalCost + 4.5e-13
	s.Unlock()
	s.Recompute()
	if s.TripCost != 0 {
		t.Errorf("negative residue must clamp to 0, got %v", s.TripCost)
	}

	// Spend above the epsilon must still be reported in full.
	s.ResetTrip()
	s.Put(rec("new", "s1", "acme", "sonnet", 0.5, "t2"))
	almost(t, s.TripCost, 0.5)
}

// TestTotalsAreDeterministic guards the ordering fix: Go randomises map
// iteration, so summing over the message map directly made TotalCost vary in
// its last bits between runs. That drift is what defeated the trip baseline.
func TestTotalsAreDeterministic(t *testing.T) {
	build := func() *Store {
		s := New()
		for i := 0; i < 300; i++ {
			r := rec("m"+string(rune(i)), "s1", "acme", "sonnet", 0.1+float64(i)*0.0007, "t1")
			s.Put(r)
		}
		return s
	}
	want := build().TotalCost
	for i := 0; i < 25; i++ {
		if got := build().TotalCost; got != want {
			t.Fatalf("total differs between runs: %v vs %v", got, want)
		}
	}
}
