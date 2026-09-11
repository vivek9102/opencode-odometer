package budget

import "testing"

func session(b *Budget, baseline float64) *Session {
	return &Session{Budget: b, Baseline: baseline}
}

func TestGraceOfferedOnFirstBreach(t *testing.T) {
	b := newEnabledBudget(t, 10.0, ModeSoft)
	s := session(b, 0)
	if got := s.RemainingGrace(11.0); got != 1 {
		t.Errorf("grace = %v, want 1", got)
	}
}

func TestGraceExhaustedAfterUse(t *testing.T) {
	b := newEnabledBudget(t, 10.0, ModeSoft)
	s := &Session{Budget: b, GraceUsed: 1}
	if got := s.RemainingGrace(11.0); got != 0 {
		t.Errorf("grace = %v, want 0", got)
	}
}

func TestNoGracePastHardCeiling(t *testing.T) {
	b := newEnabledBudget(t, 10.0, ModeSoft)
	s := session(b, 0)
	// 16 / 10 = 1.6x, above the 1.5x hard stop
	if got := s.RemainingGrace(16.0); got != 0 {
		t.Errorf("grace = %v, want 0", got)
	}
	if !s.PastHardStop(16.0) {
		t.Error("expected past hard stop")
	}
}

func TestNoGraceInHardMode(t *testing.T) {
	b := newEnabledBudget(t, 10.0, ModeHard)
	s := session(b, 0)
	if got := s.RemainingGrace(11.0); got != 0 {
		t.Errorf("grace = %v, want 0", got)
	}
}

func TestNoGraceWhenUnderLimit(t *testing.T) {
	b := newEnabledBudget(t, 10.0, ModeSoft)
	s := session(b, 0)
	if got := s.RemainingGrace(5.0); got != 0 {
		t.Errorf("grace = %v, want 0", got)
	}
}

func TestAllowOverrideClearsState(t *testing.T) {
	b := newEnabledBudget(t, 10.0, ModeHard)
	s := &Session{Budget: b, Allow: 20.0}
	state, _ := s.State(15.0)
	if state != StateOK {
		t.Errorf("expected ok under allow override, got %v", state)
	}
	if got := s.RemainingGrace(15.0); got != 0 {
		t.Errorf("grace under allow = %v, want 0", got)
	}
}

func TestEnablingDoesNotBlockExistingSpend(t *testing.T) {
	// Session had $17.27 spent before enable; baseline captures it.
	b := newEnabledBudget(t, 1.0, ModeHard)
	s := &Session{Budget: b, Baseline: 17.27}
	eff := s.EffCost(17.27)
	state, _ := s.State(eff)
	if state != StateOK {
		t.Errorf("expected ok at baseline, got %v", state)
	}
}

func TestCountsOnlySpendAfterEnable(t *testing.T) {
	b := newEnabledBudget(t, 1.0, ModeHard)
	s := &Session{Budget: b, Baseline: 17.27}
	if st, _ := s.State(s.EffCost(17.77)); st != StateOK { // +0.50
		t.Errorf("+0.50 -> %v, want ok", st)
	}
	if st, _ := s.State(s.EffCost(18.12)); st != StateWarn { // +0.85
		t.Errorf("+0.85 -> %v, want warn", st)
	}
	if st, _ := s.State(s.EffCost(18.40)); st != StateOver { // +1.13
		t.Errorf("+1.13 -> %v, want over", st)
	}
}

func TestNewSessionCountsFromZero(t *testing.T) {
	b := newEnabledBudget(t, 1.0, ModeHard)
	s := session(b, 0)
	if st, _ := s.State(s.EffCost(0.10)); st != StateOK {
		t.Errorf("0.10 -> %v, want ok", st)
	}
	if st, _ := s.State(s.EffCost(1.50)); st != StateOver {
		t.Errorf("1.50 -> %v, want over", st)
	}
}

func TestBaselineNeverGoesNegative(t *testing.T) {
	b := newEnabledBudget(t, 1.0, ModeHard)
	s := &Session{Budget: b, Baseline: 17.27}
	// re-priced session can dip below its baseline; must clamp at 0
	if got := s.EffCost(5.0); got != 0 {
		t.Errorf("effective cost = %v, want 0", got)
	}
}
