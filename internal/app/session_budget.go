package app

import "github.com/vivek9102/opencode-odometer/internal/ledger"

// SetSessionParent preserves OpenCode's chat tree. Delegated work consumes
// the same budget as its parent instead of silently starting a new counter.
func (a *App) SetSessionParent(sid, parent string) bool {
	if sid == "" || sid == parent {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.SessionParents == nil {
		a.state.SessionParents = map[string]string{}
	}
	if old, exists := a.state.SessionParents[sid]; exists && old == parent {
		return false
	}
	// Reject malformed cycles rather than merging unrelated chats.
	for id, visited := parent, map[string]bool{sid: true}; id != ""; id = a.state.SessionParents[id] {
		if visited[id] {
			return false
		}
		visited[id] = true
	}
	a.state.SessionParents[sid] = parent
	return true
}

func budgetRoot(sid string, parents map[string]string) string {
	seen := map[string]bool{}
	for parents[sid] != "" && !seen[sid] {
		seen[sid] = true
		sid = parents[sid]
	}
	return sid
}

// Caller holds a.mu. Each alias has the whole chat's spend and baseline,
// including empty child sessions which must already enforce an exhausted cap.
func (a *App) budgetSnapshotLocked() (map[string]ledger.SessionStat, map[string]float64) {
	sessions := a.Ledger.GetPerSessionSnapshot()
	groups := map[string]ledger.SessionStat{}
	baselines := map[string]float64{}
	for sid, cost := range a.state.BudgetBaselines {
		baselines[budgetRoot(sid, a.state.SessionParents)] += cost
	}
	for sid, st := range sessions {
		root := budgetRoot(sid, a.state.SessionParents)
		group := groups[root]
		group.Cost += st.Cost
		group.Msgs += st.Msgs
		group.Tokens += st.Tokens
		if st.Last > group.Last {
			group.Last = st.Last
			group.Model = st.Model
		}
		groups[root] = group
	}
	for sid := range a.state.SessionParents {
		sessions[sid] = ledger.SessionStat{}
	}
	for root := range groups {
		sessions[root] = ledger.SessionStat{}
	}
	for sid := range sessions {
		sessions[sid] = groups[budgetRoot(sid, a.state.SessionParents)]
	}
	return sessions, baselines
}

func (a *App) ActiveChat() string {
	sid := a.Ledger.ActiveSession()
	a.mu.RLock()
	defer a.mu.RUnlock()
	return budgetRoot(sid, a.state.SessionParents)
}

func (a *App) ChatRoot(sid string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return budgetRoot(sid, a.state.SessionParents)
}

func (a *App) ChatStats(sid string) ledger.SessionStat {
	a.mu.RLock()
	defer a.mu.RUnlock()
	sessions, _ := a.budgetSnapshotLocked()
	return sessions[budgetRoot(sid, a.state.SessionParents)]
}
