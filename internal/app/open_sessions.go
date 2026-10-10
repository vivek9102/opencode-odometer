package app

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

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
	SessionID        string          `json:"session_id"`
	Enabled          bool            `json:"enabled"`
	Limit            float64         `json:"limit"`
	Mode             string          `json:"mode"`
	Baseline         float64         `json:"baseline"`
	Rule             string          `json:"rule,omitempty"`
	OriginalModel    string          `json:"original_model,omitempty"`
	FallbackModel    string          `json:"fallback_model,omitempty"`
	FallbackLimit    float64         `json:"fallback_limit,omitempty"`
	Then             string          `json:"then,omitempty"`
	Stage            string          `json:"stage,omitempty"`
	Stopped          bool            `json:"stopped,omitempty"`
	ManualStop       bool            `json:"manual_stop,omitempty"`
	CancellationID   string          `json:"cancellation_id,omitempty"`
	OriginalIDs      map[string]bool `json:"original_ids,omitempty"`
	FallbackBaseline float64         `json:"fallback_baseline,omitempty"`
	TransitionID     string          `json:"transition_id,omitempty"`
	TransitionAt     int64           `json:"transition_at,omitempty"`
	Detail           string          `json:"detail,omitempty"`
	Event            string          `json:"event,omitempty"`
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
	Rule           string  `json:"rule"`
	OriginalModel  string  `json:"original_model"`
	FallbackModel  string  `json:"fallback_model"`
	FallbackLimit  float64 `json:"fallback_limit"`
	FallbackSpent  float64 `json:"fallback_spent"`
	FallbackFree   bool    `json:"fallback_free"`
	Then           string  `json:"then"`
	Stage          string  `json:"stage"`
	OnFallback     bool    `json:"on_fallback"`
	Stopped        bool    `json:"stopped"`
	ManualStop     bool    `json:"manual_stop"`
	Detail         string  `json:"detail"`
	RuleEvent      string  `json:"rule_event"`
}

// RefreshOpenSessions removes closed/dead TUIs. A delayed heartbeat from a
// still-running process must not delete its allowance during a model/tool call.
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
		if entry.Closed || !opencode.ProcessAliveAt(entry.PID, entry.Updated) {
			delete(a.openTUIs, entry.ID)
			continue
		}
		entry.SessionID = budgetRoot(entry.SessionID, a.state.SessionParents)
		// Home/route refreshes can temporarily report no conversation. Keep the
		// last binding until a different non-empty conversation is confirmed.
		if entry.SessionID == "" {
			entry.SessionID = budgetRoot(a.openTUIs[entry.ID].SessionID, a.state.SessionParents)
			if entry.SessionID == "" {
				entry.SessionID = budgetRoot(a.state.OpenPolicies[entry.ID].SessionID, a.state.SessionParents)
			}
		}
		if now-entry.Updated > 10 {
			entry.Active = false
		}
		a.openTUIs[entry.ID] = entry
		// Moving to a different conversation starts an unarmed budget. Going
		// from the startup home screen to its first chat preserves a set cap.
		if p, ok := a.state.OpenPolicies[entry.ID]; ok && p.SessionID != entry.SessionID {
			if p.SessionID != "" && budgetRoot(p.SessionID, a.state.SessionParents) != entry.SessionID {
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
		if !opencode.ProcessAliveAt(entry.PID, entry.Updated) {
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
		cost, fbSpent := a.policySpendLocked(entry, p, records)
		fraction := 0.0
		state := "ok"
		if p.Limit > 0 {
			fraction = cost / p.Limit
		}
		if fraction >= .75 {
			state = "warn"
		}
		if fraction >= 1 {
			state = "over"
		}
		if !p.Enabled || p.Limit <= 0 {
			state = "ok"
			fraction = 0
		}
		stage := p.Stage
		if stage == "" {
			stage = "original"
		}
		fb := stage == "fallback"
		free := a.Prices.Entry(p.FallbackModel).Free && !a.Prices.Entry(p.FallbackModel).Unknown
		stopped := p.Stopped || (!fb && state == "over" && p.Rule != "switch") || (fb && !free && p.Then != "go" && p.FallbackLimit > 0 && fbSpent >= p.FallbackLimit)
		if stopped {
			state = "over"
		}
		if fb && !stopped {
			state = "fallback"
		}
		if stage == "switching" && !stopped {
			state = "switching"
		}
		rule := p.Rule
		if rule == "" {
			rule = "stop"
		}
		then := p.Then
		if then == "" {
			then = "stop"
		}
		rows = append(rows, OpenSessionRow{OpenTUI: entry, Cost: cost, Spent: spent, Enabled: p.Enabled && p.Limit > 0, Limit: p.Limit, Mode: "hard", State: state, Fraction: fraction, PastHardStop: stopped, Enforced: p.Enabled && p.Limit > 0 && !paused,
			Rule: rule, OriginalModel: p.OriginalModel, FallbackModel: p.FallbackModel, FallbackLimit: p.FallbackLimit, FallbackSpent: fbSpent, FallbackFree: free, Then: then, Stage: stage, OnFallback: fb, Stopped: stopped, ManualStop: p.ManualStop, Detail: p.Detail, RuleEvent: p.Event})
	}
	rank := func(r OpenSessionRow) int {
		if r.Stopped || r.State == "over" {
			return 0
		}
		if r.OnFallback || r.Stage == "switching" {
			return 1
		}
		if r.State == "warn" {
			return 2
		}
		if r.Enabled {
			return 3
		}
		if r.SessionID != "" {
			return 4
		}
		return 5
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

// OpenSessionRecords scopes both exports and display totals to currently open
// TUI roots and their children, counting shared conversations once.
func (a *App) OpenSessionRecords(rows []OpenSessionRow, records []ledger.Record) []ledger.Record {
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
	selected := []ledger.Record{}
	a.mu.RLock()
	for _, rec := range records {
		if start, ok := starts[budgetRoot(rec.SessionID, a.state.SessionParents)]; ok && rec.Timestamp >= start {
			selected = append(selected, rec)
		}
	}
	a.mu.RUnlock()
	return selected
}

// OpenSessionTotals returns scoped dollars and the recent hourly burn rate.
func (a *App) OpenSessionTotals(rows []OpenSessionRow) (float64, float64) {
	ids := map[string]bool{}
	cost := 0.0
	for _, rec := range a.OpenSessionRecords(rows, a.Ledger.GetMessagesSnapshot()) {
		ids[rec.MID] = true
		cost += rec.Cost
	}
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
	// Accept older clients' soft value, but all open-TUI limits are now hard.
	if mode != "hard" && mode != "soft" {
		return fmt.Errorf("choose a valid limit mode")
	}
	mode = "hard"
	if enabled && round4(limit) <= 0 {
		return fmt.Errorf("enter a limit greater than zero")
	}
	a.RefreshOpenSessions()
	// Invalidate an outstanding continuation before cancelling its stream. A
	// return to the original model must not race a late fallback submission.
	var before OpenSessionRow
	for _, r := range a.OpenSessions() {
		if r.ID == id {
			before = r
		}
	}
	returnOriginal := !enabled || before.Cost < round4(limit)
	restore := returnOriginal && (before.OnFallback || before.Stage == "switching")
	if restore {
		if err := a.CancelOpenSessionContinuation(id); err != nil {
			return err
		}
		if before.Active {
			if err := a.stopOpenTUI(before); err != nil {
				return err
			}
		}
	}
	a.mu.Lock()
	entry, ok := a.openTUIs[id]
	if !ok {
		a.mu.Unlock()
		return fmt.Errorf("this OpenCode TUI has closed")
	}
	p := a.state.OpenPolicies[id]
	if enabled && !p.Enabled {
		// Opening an allowance starts at zero. Editing an enabled allowance
		// keeps its baseline; history and aggregate counters are unaffected.
		original, fallback := a.policySpendLocked(entry, p, a.Ledger.GetMessagesSnapshot())
		p.Baseline += original
		p.FallbackBaseline += fallback
	}
	if p.OriginalModel == "" {
		p.OriginalModel = entry.Model
	}
	p.SessionID, p.Limit, p.Mode, p.Enabled = entry.SessionID, round4(limit), mode, enabled
	if !enabled {
		p.Limit = 0
	}
	cost, fb := a.policySpendLocked(entry, p, a.Ledger.GetMessagesSnapshot())
	if !enabled || cost < p.Limit {
		p.FallbackBaseline += fb
		p.Stopped, p.ManualStop, p.TransitionID, p.CancellationID, p.Stage, p.Detail = false, false, "", "", "original", ""
		p.TransitionAt = 0
		if before.Stopped {
			p.Detail = "Session resumed."
			p.Event = newRuleEvent()
		}
	} else {
		p.ManualStop, p.CancellationID = false, ""
		if p.Stage == "" || p.Stage == "original" {
			p.Stopped = false
		}
	}
	a.state.OpenPolicies[id] = p
	a.mu.Unlock()
	a.PublishBudget()
	a.SaveState()
	if returnOriginal {
		released, err := a.releaseBudgetModel(entry.SessionID, p)
		if err != nil {
			return err
		}
		// Disabled budgets follow OpenCode immediately. An increased allowance
		// can restore once, without persisting a pin over later picker choices.
		if restore && enabled && released && p.OriginalModel != "" && entry.SessionID != "" {
			if _, err := a.requestModelSwitch(entry.SessionID, p.OriginalModel, "budget_restore"); err != nil {
				a.setRuleFailure(id, "Could not restore the original model: "+err.Error())
				return err
			}
		}
	}
	a.ProcessBudgetRules()
	a.PublishBudget()
	return nil
}

func (a *App) IncreaseOpenSessionBudget(id string, amount float64) error {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 {
		return fmt.Errorf("increase must be positive")
	}
	for _, row := range a.OpenSessions() {
		if row.ID == id {
			return a.ResumeOpenSession(id, amount)
		}
	}
	return fmt.Errorf("this OpenCode TUI has closed")
}

// Caller holds a.mu. Only displayed chats and their delegated children are
// published. A shared chat open in two TUIs uses the stricter verdict.
func (a *App) publishOpenBudgetsLocked() {
	doc := plugin.BudgetFile{FreeModels: a.freeModelKeys(), Enabled: !a.Preferences().Paused, WarnAtPercent: 75, BlockWhenExceeded: true, Mode: "hard", HardStopAt: 1, GraceTurns: 0, Sessions: map[string]plugin.Session{}}
	doc.Accounted = map[string]string{}
	for _, rec := range a.Ledger.GetMessagesSnapshot() {
		for _, entry := range a.openTUIs {
			if budgetRoot(rec.SessionID, a.state.SessionParents) == entry.SessionID {
				doc.Accounted[rec.MID] = fmt.Sprintf("%d/%d/%d/%d/%d/%s", rec.TokensIn, rec.TokensOut, rec.Reasoning, rec.CacheRead, rec.CacheWrite, rec.Finish)
				break
			}
		}
	}
	for _, row := range a.openRowsLocked() {
		if row.SessionID == "" || !row.Enabled {
			continue
		}
		verdict := plugin.Session{BudgetSessionID: row.ID, Cost: row.Cost, State: row.State, Fraction: row.Fraction, Limit: row.Limit, Mode: "hard", HardStopAt: 1, PastHardStop: row.Stopped, Enforced: row.Enforced, Strict: true, ManualStop: row.ManualStop, CancellationID: a.state.OpenPolicies[row.ID].CancellationID, Stage: row.Stage, Generation: a.state.OpenPolicies[row.ID].TransitionID, OriginalModel: row.OriginalModel, FallbackModel: row.FallbackModel}
		if row.Stage == "switching" || row.Stopped {
			verdict.State = "over"
		}
		if row.OnFallback {
			verdict.Cost, verdict.Limit = row.FallbackSpent, row.FallbackLimit
			verdict.Fraction = 0
			if row.FallbackLimit > 0 {
				verdict.Fraction = row.FallbackSpent / row.FallbackLimit
			}
			if !row.Stopped {
				verdict.State = "ok"
			}
		}
		old, exists := doc.Sessions[row.SessionID]
		if !exists || verdict.State == "over" && old.State != "over" || verdict.State == old.State && verdict.Fraction > old.Fraction {
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
