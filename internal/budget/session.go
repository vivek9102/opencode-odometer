package budget

// Session computes the budget verdict for a single session from its raw
// spend. It encapsulates the two pure-measurement helpers that in the
// original lived on the app host:
//
//   - EffCost: spend since the baseline recorded when the limit was enabled
//     (so enabling a limit never retroactively blocks already-spent sessions)
//   - RemainingGrace: how many grace turns are still available
//
// Both are pure functions of configuration and per-session state.
type Session struct {
	Budget    *Budget
	Baseline  float64 // session cost when the limit was enabled
	Allow     float64 // user-granted override ceiling (0 = none)
	GraceUsed int     // grace turns already claimed by the plugin
}

// EffCost returns the spend that counts toward the cap: raw cost minus the
// baseline, never negative. Spend accrued before enabling the limit is
// grandfathersed out.
func (s *Session) EffCost(rawCost float64) float64 {
	v := rawCost - s.Baseline
	if v < 0 {
		return 0
	}
	return v
}

// State reports the measurement for a session, honouring a user-granted
// Allow override (which clears the session back to "ok" past that point).
func (s *Session) State(effCost float64) (State, float64) {
	// Allow overrides reset the state: if the user granted this much, the
	// session is treated as "ok" regardless of fraction.
	if s.Allow > 0 && s.Allow >= effCost {
		return StateOK, 0
	}
	return s.Budget.Status(effCost)
}

// RemainingGrace computes how many grace turns are left for this session.
// Grace is offered only in `soft` mode, once per session, and only below the
// hard ceiling. Past the ceiling a runaway loop must stop regardless of how
// many grace turns are nominally left.
func (s *Session) RemainingGrace(effCost float64) int {
	state, frac := s.Budget.Status(effCost)
	if s.Allow > 0 && s.Allow >= effCost {
		return 0
	}
	if state != StateOver || s.Budget.Mode() != ModeSoft {
		return 0
	}
	if frac >= s.Budget.HardStopAt() {
		return 0
	}
	rem := s.Budget.GraceTurns() - s.GraceUsed
	if rem < 0 {
		return 0
	}
	return rem
}

// PastHardStop reports whether the session has crossed the hard ceiling.
func (s *Session) PastHardStop(effCost float64) bool {
	if s.Budget.Limit() <= 0 {
		return false
	}
	return effCost/s.Budget.Limit() >= s.Budget.HardStopAt()
}
