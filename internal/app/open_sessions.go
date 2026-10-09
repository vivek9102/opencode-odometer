package app

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/budget"
	"github.com/vivek9102/opencode-odometer/internal/ledger"
	"github.com/vivek9102/opencode-odometer/internal/opencode"
	"github.com/vivek9102/opencode-odometer/internal/plugin"
)

// OpenTUI is presence reported by the TUI, never inferred from saved history.
type OpenTUI struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PID       int    `json:"pid"`
	Started   int64  `json:"started"`
	Updated   int64  `json:"updated"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Active    bool   `json:"active"`
	Closed    bool   `json:"closed"`
}

type OpenPolicy struct {
	SessionID string  `json:"session_id"`
	Enabled   bool    `json:"enabled"`
	Limit     float64 `json:"limit"`
	Mode      string  `json:"mode"`
	Baseline  float64 `json:"baseline"`
}

type OpenSessionRow struct {
	OpenTUI
	Cost           float64 `json:"cost"`
	Spent          float64 `json:"spent"`
	Enabled        bool    `json:"enabled"`
	Limit          float64 `json:"limit"`
	Mode           string  `json:"mode"`
	State          string  `json:"state"`
	Fraction       float64 `json:"fraction"`
	GraceRemaining int     `json:"grace_remaining"`
	PastHardStop   bool    `json:"past_hard_stop"`
	Enforced       bool    `json:"enforced"`
}

// RefreshOpenSessions expires dead/crashed TUIs but retains healthy idle ones.
// A tombstone handles normal exit; PID plus a lease handles forced exit.
func (a *App) RefreshOpenSessions() {
	files, _ := filepath.Glob(filepath.Join(a.experienceDir(), "tui-*.json"))
	now := time.Now().Unix()
	a.mu.Lock()
	if a.openTUIs == nil {
		a.openTUIs = map[string]OpenTUI{}
	}
	if a.state.OpenPolicies == nil {
		a.state.OpenPolicies = map[string]OpenPolicy{}
	}
	changed := false
	for _, file := range files {
		var entry OpenTUI
		raw, err := os.ReadFile(file)
		if err != nil || json.Unmarshal(raw, &entry) != nil || entry.ID == "" || entry.PID <= 0 || entry.Started <= 0 || entry.Name == "" || filepath.Base(file) != "tui-"+entry.ID+".json" {
			continue
		}
		if !a.state.OpenSessionsMode {
			a.state.OpenSessionsMode = true
			changed = true
		}
		if entry.Closed || now-entry.Updated > 10 || !opencode.ProcessAlive(entry.PID) {
			delete(a.openTUIs, entry.ID)
			continue
		}
		entry.SessionID = budgetRoot(entry.SessionID, a.state.SessionParents)
		a.openTUIs[entry.ID] = entry
		// Moving to a different conversation starts an unarmed budget. Going
		// from the startup home screen to its first chat preserves a set cap.
		if p, ok := a.state.OpenPolicies[entry.ID]; ok && p.SessionID != entry.SessionID {
			if p.SessionID != "" {
				delete(a.state.OpenPolicies, entry.ID)
				delete(a.graceUsed, entry.ID)
			} else {
				p.SessionID = entry.SessionID
				a.state.OpenPolicies[entry.ID] = p
			}
			changed = true
		}
	}
	for id, entry := range a.openTUIs {
		if now-entry.Updated > 10 || !opencode.ProcessAlive(entry.PID) {
			delete(a.openTUIs, id)
		}
	}
	for id := range a.state.OpenPolicies {
		if _, ok := a.openTUIs[id]; !ok {
			delete(a.state.OpenPolicies, id)
			delete(a.graceUsed, id)
			changed = true
		}
	}
	a.mu.Unlock()
	if changed {
		a.SaveState()
	}
}

// Caller holds a.mu. Historical backfill must not turn a new TUI into an
// already-spent session, even when the backfill arrives after registration.
func (a *App) openSpendLocked(entry OpenTUI, records []ledger.Record) float64 {
	start := time.UnixMilli(entry.Started).Format("2006-01-02 15:04:05")
	cost := 0.0
	for _, rec := range records {
		if entry.SessionID != "" && budgetRoot(rec.SessionID, a.state.SessionParents) == entry.SessionID && rec.Timestamp >= start {
			cost += rec.Cost
		}
	}
	return round6(cost)
}

// Caller holds a.mu. Active spend must not disappear during history pruning.
func (a *App) openRecordsLocked() map[string]bool {
	if len(a.openTUIs) == 0 {
		return nil
	}
	keep := map[string]bool{}
	for _, rec := range a.Ledger.GetMessagesSnapshot() {
		for _, entry := range a.openTUIs {
			if entry.SessionID != "" && budgetRoot(rec.SessionID, a.state.SessionParents) == entry.SessionID && rec.Timestamp >= time.UnixMilli(entry.Started).Format("2006-01-02 15:04:05") {
				keep[rec.MID] = true
				break
			}
		}
	}
	return keep
}

func (a *App) openRowsLocked() []OpenSessionRow {
	records := a.Ledger.GetMessagesSnapshot()
	rows := make([]OpenSessionRow, 0, len(a.openTUIs))
	paused := a.Preferences().Paused
	for id, entry := range a.openTUIs {
		p := a.state.OpenPolicies[id]
		if p.Mode == "" {
			p.Mode = "hard"
		}
		spent := a.openSpendLocked(entry, records)
		b := budget.New()
		b.Cfg = budget.Config{Enabled: p.Enabled, SessionLimitUSD: p.Limit, Mode: budget.Mode(p.Mode), WarnAtPercent: 75, BlockWhenExceeded: true, HardStopAt: 1.5, GraceTurns: 1}
		s := budget.Session{Budget: b, Baseline: p.Baseline, GraceUsed: a.graceUsed[id]}
		cost := s.EffCost(spent)
		state, fraction := s.State(cost)
		if !p.Enabled || p.Limit <= 0 {
			state = "ok"
			fraction = 0
		}
		rows = append(rows, OpenSessionRow{OpenTUI: entry, Cost: cost, Spent: spent, Enabled: p.Enabled && p.Limit > 0, Limit: p.Limit, Mode: p.Mode, State: string(state), Fraction: fraction, GraceRemaining: s.RemainingGrace(cost), PastHardStop: s.PastHardStop(cost), Enforced: p.Enabled && p.Limit > 0 && !paused})
	}
	rank := func(r OpenSessionRow) int {
		if r.State == "over" {
			return 0
		}
		if r.State == "warn" {
			return 1
		}
		if r.Enabled {
			return 2
		}
		if r.SessionID != "" {
			return 3
		}
		return 4
	}
	sort.Slice(rows, func(i, j int) bool {
		x, y := rows[i], rows[j]
		if rank(x) != rank(y) {
			return rank(x) < rank(y)
		}
		if x.Fraction != y.Fraction {
			return x.Fraction > y.Fraction
		}
		if x.Name != y.Name {
			return x.Name < y.Name
		}
		return x.ID < y.ID
	})
	return rows
}

func (a *App) OpenSessions() []OpenSessionRow {
	a.RefreshOpenSessions()
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.openRowsLocked()
}

// OpenSessionTotals counts each message once, even when the same conversation
// is displayed by several TUIs. Closed conversations also leave the dock rate.
func (a *App) OpenSessionTotals(rows []OpenSessionRow) (float64, float64) {
	starts := map[string]string{}
	for _, row := range rows {
		if row.SessionID == "" {
			continue
		}
		start := time.UnixMilli(row.Started).Format("2006-01-02 15:04:05")
		if previous, ok := starts[row.SessionID]; !ok || start < previous {
			starts[row.SessionID] = start
		}
	}
	ids := map[string]bool{}
	cost := 0.0
	a.mu.RLock()
	for _, rec := range a.Ledger.GetMessagesSnapshot() {
		if start, ok := starts[budgetRoot(rec.SessionID, a.state.SessionParents)]; ok && rec.Timestamp >= start {
			ids[rec.MID] = true
			cost += rec.Cost
		}
	}
	a.mu.RUnlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	rate := 0.0
	a.rateMu.Lock()
	for _, point := range a.rateWindow {
		if point.Time.After(cutoff) && ids[point.MessageID] {
			rate += point.Cost
		}
	}
	a.rateMu.Unlock()
	return round6(cost), rate * 6
}

func (a *App) SetOpenSessionBudget(id string, limit float64, mode string, enabled bool) error {
	if math.IsNaN(limit) || math.IsInf(limit, 0) || limit < 0 {
		return fmt.Errorf("enter a finite, non-negative limit")
	}
	if mode != "hard" && mode != "soft" {
		return fmt.Errorf("choose hard or soft")
	}
	if enabled && round4(limit) <= 0 {
		return fmt.Errorf("enter a limit greater than zero")
	}
	a.RefreshOpenSessions()
	a.mu.Lock()
	entry, ok := a.openTUIs[id]
	if !ok {
		a.mu.Unlock()
		return fmt.Errorf("this OpenCode TUI has closed")
	}
	p := a.state.OpenPolicies[id]
	if enabled && !p.Enabled {
		p.Baseline = a.openSpendLocked(entry, a.Ledger.GetMessagesSnapshot())
		delete(a.graceUsed, id)
	}
	if !enabled {
		p.Baseline = 0
		delete(a.graceUsed, id)
	}
	p.SessionID, p.Limit, p.Mode, p.Enabled = entry.SessionID, round4(limit), mode, enabled
	a.state.OpenPolicies[id] = p
	a.mu.Unlock()
	a.PublishBudget()
	a.SaveState()
	return nil
}

func (a *App) IncreaseOpenSessionBudget(id string, amount float64) error {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 {
		return fmt.Errorf("increase must be positive")
	}
	for _, row := range a.OpenSessions() {
		if row.ID == id {
			return a.SetOpenSessionBudget(id, row.Limit+amount, row.Mode, true)
		}
	}
	return fmt.Errorf("this OpenCode TUI has closed")
}

// Caller holds a.mu. Only displayed chats and their delegated children are
// published. A shared chat open in two TUIs uses the stricter verdict.
func (a *App) publishOpenBudgetsLocked() {
	doc := plugin.BudgetFile{FreeModels: a.freeModelKeys(), Enabled: !a.Preferences().Paused, WarnAtPercent: 75, BlockWhenExceeded: true, Mode: "hard", HardStopAt: 1.5, GraceTurns: 1, Sessions: map[string]plugin.Session{}}
	for _, row := range a.openRowsLocked() {
		if row.SessionID == "" || !row.Enabled {
			continue
		}
		verdict := plugin.Session{BudgetSessionID: row.ID, Cost: row.Cost, State: row.State, Fraction: row.Fraction, Limit: row.Limit, Mode: row.Mode, GraceRemaining: row.GraceRemaining, HardStopAt: 1.5, PastHardStop: row.PastHardStop, Enforced: row.Enforced}
		old, exists := doc.Sessions[row.SessionID]
		if !exists || verdict.Fraction > old.Fraction || (verdict.Fraction == old.Fraction && verdict.Mode == "hard") {
			doc.Sessions[row.SessionID] = verdict
		}
	}
	for sid := range a.state.SessionParents {
		if v, ok := doc.Sessions[budgetRoot(sid, a.state.SessionParents)]; ok {
			doc.Sessions[sid] = v
		}
	}
	if err := a.Contract.WriteBudget(doc); err != nil {
		a.LogEvent("open-session budget publish failed: %v", err)
	}
}
