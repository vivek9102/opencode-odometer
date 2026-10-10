package app

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/vivek9102/opencode-odometer/internal/ledger"
)

// OriginalIDs assigns messages to a stage, rather than subtracting a moving
// global total. Delayed updates and widget restarts retain their attribution.
func (a *App) policySpendLocked(e OpenTUI, p OpenPolicy, records []ledger.Record) (float64, float64) {
	original, fallback := 0.0, 0.0
	start := time.UnixMilli(e.Started).Format("2006-01-02 15:04:05")
	for _, r := range records {
		if e.SessionID == "" || budgetRoot(r.SessionID, a.state.SessionParents) != e.SessionID || r.Timestamp < start {
			continue
		}
		isOriginal, known := p.OriginalIDs[r.MID]
		if !known {
			isOriginal = p.Stage != "fallback"
		}
		if isOriginal {
			original += r.Cost
		} else {
			fallback += r.Cost
		}
	}
	return round6(math.Max(0, original-p.Baseline)), round6(math.Max(0, fallback-p.FallbackBaseline))
}

type BudgetRule struct {
	Rule          string  `json:"rule"`
	FallbackModel string  `json:"fallback_model"`
	FallbackLimit float64 `json:"fallback_limit"`
	Then          string  `json:"then"`
}

func newRuleEvent() string { return uuid.NewString() }

func (a *App) SetOpenSessionRule(id string, rule BudgetRule) error {
	if rule.Rule != "stop" && rule.Rule != "switch" {
		return fmt.Errorf("choose Stop or Switch")
	}
	if rule.Then != "stop" && rule.Then != "go" {
		return fmt.Errorf("choose Stop or Keep going")
	}
	if math.IsNaN(rule.FallbackLimit) || math.IsInf(rule.FallbackLimit, 0) || rule.FallbackLimit < 0 {
		return fmt.Errorf("enter a finite fallback allowance")
	}
	rows := a.OpenSessions()
	var row OpenSessionRow
	for _, r := range rows {
		if r.ID == id {
			row = r
		}
	}
	if row.ID == "" {
		return fmt.Errorf("this OpenCode TUI has closed")
	}
	original := row.OriginalModel
	if original == "" {
		original = row.Model
	}
	if rule.Rule == "switch" {
		found := false
		for _, m := range a.FallbackChoices(id) {
			if m.Key == rule.FallbackModel {
				found = true
				if !m.Free && round4(rule.FallbackLimit) <= 0 {
					return fmt.Errorf("set a paid fallback allowance greater than zero")
				}
			}
		}
		if !found {
			return fmt.Errorf("choose an available, compatible cheaper model")
		}
	}
	a.mu.Lock()
	p := a.state.OpenPolicies[id]
	if p.Stage == "switching" {
		a.mu.Unlock()
		return fmt.Errorf("wait for the model switch to finish before changing the fallback")
	}
	if p.Stage == "fallback" && rule.Rule == "switch" && rule.FallbackModel != p.FallbackModel {
		a.mu.Unlock()
		return fmt.Errorf("raise or clear the original limit before choosing another fallback")
	}
	p.Rule, p.FallbackModel, p.FallbackLimit, p.Then, p.OriginalModel = rule.Rule, rule.FallbackModel, round4(rule.FallbackLimit), rule.Then, original
	p.ManualStop, p.CancellationID = false, ""
	if p.Stage == "" || p.Stage == "original" {
		p.TransitionAt = 0
	}
	if p.Stage != "fallback" || rule.Rule == "switch" {
		p.Stopped = false
	}
	if p.Stage == "fallback" && rule.Rule == "stop" {
		p.Stopped = true
		p.Detail = "Budget reached. Further paid requests are blocked."
		p.Event = newRuleEvent()
	}
	a.state.OpenPolicies[id] = p
	a.mu.Unlock()
	a.PublishBudget()
	a.SaveState()
	a.ProcessBudgetRules()
	a.PublishBudget()
	return nil
}

func (a *App) ResumeOpenSession(id string, amount float64) error {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 {
		return fmt.Errorf("increase must be positive")
	}
	a.RefreshOpenSessions()
	// Undo must not leave an active fallback stream running while publishing
	// the original stage. Its durable selection applies to the next request.
	for _, row := range a.OpenSessions() {
		if row.ID == id && (row.OnFallback || row.Stage == "switching") && row.Active {
			if err := a.RequestOpenSessionAbort(id); err != nil {
				return err
			}
		}
	}
	a.mu.Lock()
	e, ok := a.openTUIs[id]
	if !ok {
		a.mu.Unlock()
		return fmt.Errorf("this OpenCode TUI has closed")
	}
	p := a.state.OpenPolicies[id]
	// Preserve historical fallback charges, opening a new allowance only.
	originalCost, fb := a.policySpendLocked(e, p, a.Ledger.GetMessagesSnapshot())
	restoreModel := p.Stage == "fallback" || p.Stage == "switching"
	p.FallbackBaseline += fb
	p.Limit = round4(p.Limit + amount)
	p.Enabled = true
	p.Mode = "hard"
	p.ManualStop = false
	p.CancellationID = ""
	p.Stopped = originalCost >= p.Limit
	p.Stage = "original"
	p.TransitionID = ""
	p.TransitionAt = 0
	p.Detail = "Budget increased; counted spending is preserved."
	if p.Stopped {
		p.Detail = "Budget increased, but counted spending still exceeds it. Increase the allowance again to resume."
	}
	p.Event = uuid.NewString()
	a.state.OpenPolicies[id] = p
	a.mu.Unlock()
	if restoreModel && p.OriginalModel != "" && e.SessionID != "" && !p.Stopped {
		_, err := a.RequestModelSwitch(e.SessionID, p.OriginalModel)
		if err != nil {
			a.setRuleFailure(id, "Budget increased, but restoring the original model failed: "+err.Error())
			return err
		}
	}
	a.PublishBudget()
	a.SaveState()
	return nil
}

// STOP SESSION cancels activity like Esc. It only suppresses continuation of
// that cancelled task; it does not change the allowance or latch a budget stop.
func (a *App) CancelOpenSessionContinuation(id string) error {
	a.RefreshOpenSessions()
	a.mu.Lock()
	e, ok := a.openTUIs[id]
	if !ok || e.SessionID == "" {
		a.mu.Unlock()
		return fmt.Errorf("this TUI has no running conversation")
	}
	p := a.state.OpenPolicies[id]
	p.SessionID = e.SessionID
	p.ManualStop = true
	p.CancellationID = uuid.NewString()
	p.TransitionID = ""
	if p.Stage == "switching" {
		p.Stage = "original"
	}
	a.state.OpenPolicies[id] = p
	a.mu.Unlock()
	a.PublishBudget()
	a.SaveState()
	return nil
}

type continuationStatus struct {
	ID          string   `json:"id"`
	Status      string   `json:"status"`
	Detail      string   `json:"detail"`
	OriginalIDs []string `json:"original_ids"`
}

func (a *App) setRuleFailure(id, detail string) {
	a.mu.Lock()
	p, ok := a.state.OpenPolicies[id]
	if ok {
		p.Stopped = true
		p.Detail = detail
		p.Event = uuid.NewString()
		a.state.OpenPolicies[id] = p
	}
	a.mu.Unlock()
	a.PublishBudget()
	a.SaveState()
}

// ProcessBudgetRules runs after usage ingestion. A two-phase handshake stops
// the old turn and captures its message IDs before arming the fallback.
func (a *App) ProcessBudgetRules() {
	if a.Preferences().Paused {
		return
	}
	a.RefreshOpenSessions()
	// Only a new user message carrying the current cancellation token may
	// release the automatic-continuation suppression. Old files cannot undo
	// a later Esc/STOP cancellation.
	files, _ := filepath.Glob(filepath.Join(a.experienceDir(), "continue-reset-*.json"))
	resetChanged := false
	for _, file := range files {
		var reset struct {
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
			Token     string `json:"token"`
		}
		b, err := os.ReadFile(file)
		if err != nil || json.Unmarshal(b, &reset) != nil {
			continue
		}
		a.mu.Lock()
		p, ok := a.state.OpenPolicies[reset.ID]
		if ok && p.SessionID == reset.SessionID && p.CancellationID != "" && p.CancellationID == reset.Token {
			p.ManualStop, p.CancellationID = false, ""
			a.state.OpenPolicies[reset.ID] = p
			resetChanged = true
		}
		a.mu.Unlock()
		_ = os.Remove(file)
	}
	if resetChanged {
		a.SaveState()
		a.PublishBudget()
	}
	// Provider failures apply to later user turns as well as the automatic
	// continuation, including after Esc invalidated its generation.
	failures, _ := filepath.Glob(filepath.Join(a.experienceDir(), "fallback-error-*.json"))
	for _, file := range failures {
		var failure struct {
			ID         string `json:"id"`
			SessionID  string `json:"session_id"`
			Generation string `json:"generation"`
			Key        string `json:"key"`
			Detail     string `json:"detail"`
		}
		b, err := os.ReadFile(file)
		if err != nil || json.Unmarshal(b, &failure) != nil {
			continue
		}
		a.mu.RLock()
		p, exists := a.state.OpenPolicies[failure.ID]
		valid := exists && p.Stage == "fallback" && !p.ManualStop && !p.Stopped && p.SessionID == failure.SessionID && p.FallbackModel == failure.Key && p.TransitionID == failure.Generation
		a.mu.RUnlock()
		if valid {
			a.setRuleFailure(failure.ID, failure.Detail)
		}
		_ = os.Remove(file)
	}
	a.mu.Lock()
	assigned := false
	for id, e := range a.openTUIs {
		p := a.state.OpenPolicies[id]
		if p.OriginalIDs == nil {
			continue
		}
		for _, rec := range a.Ledger.GetMessagesSnapshot() {
			if budgetRoot(rec.SessionID, a.state.SessionParents) == e.SessionID {
				if _, known := p.OriginalIDs[rec.MID]; !known {
					p.OriginalIDs[rec.MID] = p.Stage != "fallback"
					assigned = true
				}
			}
		}
		a.state.OpenPolicies[id] = p
	}
	a.mu.Unlock()
	if assigned {
		a.SaveState()
	}
	rows := a.OpenSessions()
	for _, r := range rows {
		if !r.Enabled || r.ManualStop {
			continue
		}
		a.mu.RLock()
		p := a.state.OpenPolicies[r.ID]
		a.mu.RUnlock()
		// v4 latched a transient discovery miss before issuing any command.
		// Recover only that pre-dispatch failure; never retry an uncertain turn.
		if p.Stopped && p.Rule == "switch" && r.Stage == "original" && p.TransitionID == "" && p.Detail == "Fallback unavailable in this TUI. Restart OpenCode to load automatic continuation." {
			a.mu.Lock()
			p.Stopped, p.TransitionAt, p.Detail, p.Event = false, 0, "", ""
			a.state.OpenPolicies[r.ID] = p
			a.mu.Unlock()
			a.SaveState()
		}
		if p.TransitionID != "" {
			var ack continuationStatus
			b, _ := os.ReadFile(filepath.Join(a.experienceDir(), "continue-status-"+p.TransitionID+".json"))
			_ = json.Unmarshal(b, &ack)
			if ack.ID == p.TransitionID {
				if ack.Status == "failed" {
					if !p.Stopped {
						a.setRuleFailure(r.ID, ack.Detail)
					}
					continue
				}
				if ack.Status == "prepared" && p.Stage == "switching" && !p.Stopped {
					a.mu.Lock()
					live := a.state.OpenPolicies[r.ID]
					if live.TransitionID == p.TransitionID && !live.ManualStop && !live.Stopped {
						if live.OriginalIDs == nil {
							live.OriginalIDs = map[string]bool{}
						}
						for _, mid := range ack.OriginalIDs {
							if _, exists := live.OriginalIDs[mid]; !exists {
								live.OriginalIDs[mid] = true
							}
						}
						live.Stage = "fallback"
						live.Stopped = false
						live.Detail = "Fallback ready; awaiting continuation."
						a.state.OpenPolicies[r.ID] = live
					}
					a.mu.Unlock()
					a.SaveState()
					a.PublishBudget()
				}
				if !p.Stopped && (ack.Status == "confirmed" || ack.Status == "completed") && p.Detail != ack.Detail {
					a.mu.Lock()
					live := a.state.OpenPolicies[r.ID]
					if live.TransitionID == p.TransitionID && !live.ManualStop && !live.Stopped {
						live.Detail = ack.Detail
						live.Event = p.TransitionID
						a.state.OpenPolicies[r.ID] = live
					}
					a.mu.Unlock()
					a.SaveState()
				}
			}
			timeout := int64(90)
			if ack.Status == "dispatching" && p.Stage == "fallback" {
				timeout = 360
			}
			if !p.Stopped && time.Now().Unix()-p.TransitionAt > timeout && (p.Stage == "switching" || ack.Status != "confirmed" && ack.Status != "completed") {
				a.setRuleFailure(r.ID, "Automatic continuation timed out. The session remains paused; no automatic retry was sent.")
			}
		}
		if r.Stage == "original" && r.Cost >= r.Limit && !p.Stopped {
			if p.Rule == "switch" {
				a.startFallback(r)
			} else {
				a.setRuleFailure(r.ID, "Budget reached. Further paid requests are blocked.")
			}
		}
		if r.OnFallback && r.Stopped && !p.Stopped {
			a.setRuleFailure(r.ID, "Fallback allowance reached. Further paid requests are blocked.")
		}
	}
}

func (a *App) startFallback(row OpenSessionRow) {
	// A shared conversation has one execution stream, so it cannot safely
	// continue under two different automatic rules.
	for _, r := range a.OpenSessions() {
		if r.ID != row.ID && r.SessionID == row.SessionID {
			a.setRuleFailure(row.ID, "This conversation is open in multiple TUIs. Use a separate conversation for automatic fallback.")
			return
		}
	}
	var owner ModelInventory
	files, _ := filepath.Glob(filepath.Join(a.experienceDir(), "models-*.json"))
	for _, file := range files {
		var inv ModelInventory
		b, _ := os.ReadFile(file)
		// Protocol 2 pins the actual open TUI chat. Protocol 1 may silently
		// ignore a command once history evicts it, despite fresh heartbeats.
		if json.Unmarshal(b, &inv) != nil || inv.ContinueProtocol < 2 || time.Now().Unix()-inv.Updated > 30 {
			continue
		}
		// The open TUI identifies its owning process. The inventory's bounded
		// chat history can omit this conversation or include another TUI's chat.
		if inv.PID != row.PID {
			continue
		}
		for _, m := range inv.Models {
			if m.Key == row.FallbackModel && m.Category == "chat" {
				owner = inv
			}
		}
	}
	if owner.PID == 0 {
		// Keep the exhausted verdict in place while discovery catches up. No
		// inference is submitted during this bounded wait, and STOP still wins.
		a.mu.Lock()
		p := a.state.OpenPolicies[row.ID]
		if p.ManualStop {
			a.mu.Unlock()
			return
		}
		changed := false
		if p.TransitionAt == 0 {
			p.TransitionAt = time.Now().Unix()
			p.Detail = "Waiting for this TUI's automatic fallback connection."
			a.state.OpenPolicies[row.ID] = p
			changed = true
		}
		expired := time.Now().Unix()-p.TransitionAt >= 15
		a.mu.Unlock()
		if expired {
			a.setRuleFailure(row.ID, "Automatic fallback connection unavailable in this TUI after 15 seconds. Restart OpenCode to load the updated plugin.")
		} else if changed {
			a.SaveState()
		}
		return
	}
	var choice FallbackChoice
	found := false
	for _, m := range a.FallbackChoices(row.ID) {
		if m.Key == row.FallbackModel {
			found = true
			choice = m
		}
	}
	if !found {
		a.setRuleFailure(row.ID, "Fallback is no longer available, compatible, or cheaper. The session remains paused.")
		return
	}
	id := uuid.NewString()
	children := []string{}
	a.mu.Lock()
	p := a.state.OpenPolicies[row.ID]
	if p.ManualStop || p.Stage == "switching" || p.Stage == "fallback" {
		a.mu.Unlock()
		return
	}
	p.Stage = "switching"
	p.TransitionID = id
	p.TransitionAt = time.Now().Unix()
	p.Detail = "Pausing original model before fallback."
	p.Stopped = false
	if p.OriginalModel == "" {
		p.OriginalModel = row.Model
	}
	if p.OriginalIDs == nil {
		p.OriginalIDs = map[string]bool{}
	}
	for _, rec := range a.Ledger.GetMessagesSnapshot() {
		if budgetRoot(rec.SessionID, a.state.SessionParents) == row.SessionID {
			if _, ok := p.OriginalIDs[rec.MID]; !ok {
				p.OriginalIDs[rec.MID] = true
			}
		}
	}
	for sid := range a.state.SessionParents {
		if sid != row.SessionID && budgetRoot(sid, a.state.SessionParents) == row.SessionID {
			children = append(children, sid)
		}
	}
	a.state.OpenPolicies[row.ID] = p
	a.mu.Unlock()
	a.SaveState()
	a.PublishBudget()
	req := struct {
		ID         string   `json:"id"`
		SessionID  string   `json:"session_id"`
		Instance   string   `json:"instance"`
		Key        string   `json:"key"`
		Issued     int64    `json:"issued"`
		Children   []string `json:"children"`
		Input      float64  `json:"input_price"`
		Output     float64  `json:"output_price"`
		Context    int64    `json:"context"`
		Modalities []string `json:"input_modalities"`
	}{id, row.SessionID, owner.Instance, row.FallbackModel, time.Now().Unix(), children, choice.Input, choice.Output, choice.Metadata.Context, choice.Metadata.InputModalities}
	b, _ := json.Marshal(req)
	if err := atomicExperienceFile(filepath.Join(a.experienceDir(), fmt.Sprintf("continue-%d-%s.json", owner.PID, id)), b); err != nil {
		a.setRuleFailure(row.ID, "Could not queue automatic fallback: "+err.Error())
	}
}
