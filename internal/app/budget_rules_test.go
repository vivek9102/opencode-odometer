package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ruleFixture(t *testing.T) *App {
	t.Helper()
	a, _, _ := newTestApp(t)
	reportTUI(t, a, "one", "chat-one", false)
	yes := true
	no := false
	inv := ModelInventory{PID: os.Getpid(), Instance: "test-owner", Updated: time.Now().Unix(), Sessions: []string{"chat-one"}, ContinueProtocol: 2, SwitchProtocol: 3, Models: []ConfiguredModel{
		{Key: "acme/sonnet", Name: "Sonnet", Category: "chat", ToolCall: &yes, Context: 200000},
		{Key: "acme/cheap", Name: "Cheap", Category: "chat", ToolCall: &yes, Context: 200000, Rate: &ModelPrice{Input: .1, Output: .4}},
		{Key: "acme/free-model", Name: "Free", Category: "chat", ToolCall: &yes, Context: 200000},
		{Key: "acme/unknown-tools", Name: "Unknown", Category: "chat", Rate: &ModelPrice{Input: .01, Output: .01}},
		{Key: "acme/no-tools", Name: "No tools", Category: "chat", ToolCall: &no, Rate: &ModelPrice{Input: .01, Output: .01}},
	}}
	b, _ := json.Marshal(inv)
	if err := os.WriteFile(filepath.Join(a.experienceDir(), "models-test.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.SetOpenSessionBudget("one", .1, "hard", true); err != nil {
		t.Fatal(err)
	}
	return a
}
func setRule(t *testing.T, a *App, key, then string) {
	t.Helper()
	if err := a.SetOpenSessionRule("one", BudgetRule{Rule: "switch", FallbackModel: key, FallbackLimit: .05, Then: then}); err != nil {
		t.Fatal(err)
	}
}
func acknowledge(t *testing.T, a *App, status string, ids ...string) {
	t.Helper()
	p := a.state.OpenPolicies["one"]
	b, _ := json.Marshal(continuationStatus{ID: p.TransitionID, Status: status, Detail: status, OriginalIDs: ids})
	if err := os.WriteFile(filepath.Join(a.experienceDir(), "continue-status-"+p.TransitionID+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	a.ProcessBudgetRules()
}

func TestFallbackStagesLateUsageRestartAndResume(t *testing.T) {
	a := ruleFixture(t)
	setRule(t, a, "acme/cheap", "stop")
	addOpenSpend(a, "original", "chat-one", .2)
	a.ProcessBudgetRules()
	p := a.state.OpenPolicies["one"]
	if p.Stage != "switching" || p.TransitionID == "" {
		t.Fatalf("not switching: %+v", p)
	}
	v := openVerdicts(t, a).Sessions["chat-one"]
	if v.State != "over" || !v.Strict || v.GraceRemaining != 0 || v.HardStopAt != 1 {
		t.Fatalf("old turn not blocked: %+v", v)
	}
	a.ProcessBudgetRules()
	if a.state.OpenPolicies["one"].TransitionID != p.TransitionID {
		t.Fatal("duplicate transition")
	}
	files, _ := filepath.Glob(filepath.Join(a.experienceDir(), "continue-*.json"))
	if len(files) != 1 {
		t.Fatal("duplicate continuation command", files)
	}
	acknowledge(t, a, "prepared", "original", "late-original")
	addOpenSpend(a, "fallback", "chat-one", .03)
	addOpenSpend(a, "original", "chat-one", .25)
	addOpenSpend(a, "late-original", "chat-one", .01)
	a.ProcessBudgetRules()
	r := a.OpenSessions()[0]
	if r.Cost != .26 || r.FallbackSpent != .03 || !r.OnFallback || r.Stopped {
		t.Fatalf("stage accounting: %+v", r)
	}
	acknowledge(t, a, "confirmed")
	a.SaveState()
	restarted, err := New(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r := restarted.OpenSessions()[0]; r.Cost != .26 || r.FallbackSpent != .03 || r.Stage != "fallback" {
		t.Fatalf("restart rebased: %+v", r)
	}
	restarted.ResetTrip()
	if r := restarted.OpenSessions()[0]; r.Cost != .26 || r.FallbackSpent != .03 {
		t.Fatal("trip reset changed policy")
	}
	addOpenSpend(restarted, "fallback", "chat-one", .09)
	restarted.ProcessBudgetRules()
	if r := restarted.OpenSessions()[0]; !r.Stopped || r.FallbackSpent != .09 {
		t.Fatalf("paid cap not enforced: %+v", r)
	}
	stopped := restarted.state.OpenPolicies["one"]
	for i := 0; i < 3; i++ {
		restarted.ProcessBudgetRules()
	}
	if after := restarted.state.OpenPolicies["one"]; after.Detail != stopped.Detail || after.Event != stopped.Event || !after.Stopped {
		t.Fatal("old confirmed acknowledgement overwrote fallback stop")
	}
	if err := restarted.ResumeOpenSession("one", .5); err != nil {
		t.Fatal(err)
	}
	r = restarted.OpenSessions()[0]
	if r.Stopped || r.Stage != "original" || r.Cost != .26 || r.Limit != .6 || r.FallbackSpent != 0 {
		t.Fatalf("resume: %+v", r)
	}
	total, _ := restarted.OpenSessionTotals(restarted.OpenSessions())
	if total != .35 {
		t.Fatal("resume lost historical spending", total)
	}
	reportTUI(t, restarted, "one", "chat-one", true)
	restarted.RefreshOpenSessions()
	if len(restarted.state.OpenPolicies) != 0 || len(openVerdicts(t, restarted).Sessions) != 0 {
		t.Fatal("closed TUI retained policy")
	}
}
func TestFallbackFreeKeepGoingAndManualStop(t *testing.T) {
	for _, key := range []string{"acme/free-model", "acme/cheap"} {
		t.Run(key, func(t *testing.T) {
			a := ruleFixture(t)
			setRule(t, a, key, "go")
			addOpenSpend(a, "orig", "chat-one", .2)
			a.ProcessBudgetRules()
			acknowledge(t, a, "prepared", "orig")
			acknowledge(t, a, "confirmed")
			if key == "acme/cheap" {
				addOpenSpend(a, "fb", "chat-one", 1)
			}
			a.ProcessBudgetRules()
			if a.OpenSessions()[0].Stopped {
				t.Fatal("free/keep-going stopped")
			}
			if err := a.CancelOpenSessionContinuation("one"); err != nil {
				t.Fatal(err)
			}
			a.ProcessBudgetRules()
			if r := a.OpenSessions()[0]; r.Stopped || !r.ManualStop || !r.OnFallback {
				t.Fatal("Esc-like cancellation changed the budget or fallback")
			}
			if v := openVerdicts(t, a).Sessions["chat-one"]; !v.ManualStop || v.Generation != "" {
				t.Fatal("manual stop allowed continuation")
			}
		})
	}
}

func TestLimitEditsReevaluateAndPreserveSpending(t *testing.T) {
	a := ruleFixture(t)
	addOpenSpend(a, "orig", "chat-one", .08)
	if err := a.SetOpenSessionBudget("one", .05, "hard", true); err != nil {
		t.Fatal(err)
	}
	if r := a.OpenSessions()[0]; !r.Stopped || r.Cost != .08 {
		t.Fatalf("lowering did not stop: %+v", r)
	}
	if err := a.SetOpenSessionBudget("one", .20, "hard", true); err != nil {
		t.Fatal(err)
	}
	if r := a.OpenSessions()[0]; r.Stopped || r.Cost != .08 || r.Detail != "Session resumed." {
		t.Fatalf("raising did not resume: %+v", r)
	}
	if err := a.SetOpenSessionBudget("one", 0, "hard", false); err != nil {
		t.Fatal(err)
	}
	if r := a.OpenSessions()[0]; r.Enabled || r.Limit != 0 || r.Cost != .08 {
		t.Fatalf("clear lost spending: %+v", r)
	}
	if err := a.SetOpenSessionBudget("one", .05, "hard", true); err != nil {
		t.Fatal(err)
	}
	if r := a.OpenSessions()[0]; r.Stopped || r.Cost != 0 || r.Spent != .08 {
		t.Fatalf("re-enabling did not open a fresh allowance: %+v", r)
	}
}

func TestLimitRestoresOriginalAndFreshFallbackWithoutLosingHistory(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(map[bool]string{false: "raise", true: "clear"}[clear], func(t *testing.T) {
			a := ruleFixture(t)
			setRule(t, a, "acme/cheap", "stop")
			addOpenSpend(a, "orig", "chat-one", .2)
			a.ProcessBudgetRules()
			acknowledge(t, a, "prepared", "orig")
			addOpenSpend(a, "fb", "chat-one", .03)
			a.ProcessBudgetRules()
			acknowledge(t, a, "confirmed")
			limit := .5
			if clear {
				limit = 0
			}
			if err := a.SetOpenSessionBudget("one", limit, "hard", !clear); err != nil {
				t.Fatal(err)
			}
			if r := a.OpenSessions()[0]; r.Stopped || r.OnFallback || r.Cost != .2 || r.FallbackSpent != 0 {
				t.Fatalf("bad reset: %+v", r)
			}
			if total, _ := a.OpenSessionTotals(a.OpenSessions()); total != .23 {
				t.Fatal("history lost", total)
			}
			pending := a.PendingModelSwitch("chat-one")
			if clear && pending.ID != "" {
				t.Fatal("disabled budget still pins a model", pending)
			}
			if !clear && (pending.Key != "acme/sonnet" || pending.Persistent || pending.Source != "budget_restore") {
				t.Fatal("restoration must be scoped to one request", pending)
			}
			if clear {
				if err := a.SetOpenSessionBudget("one", .5, "hard", true); err != nil {
					t.Fatal(err)
				}
			}
			addOpenSpend(a, "next-original", "chat-one", .6)
			a.ProcessBudgetRules()
			acknowledge(t, a, "prepared", "next-original")
			addOpenSpend(a, "next-fallback", "chat-one", .02)
			a.ProcessBudgetRules()
			want := .8
			if clear {
				want = .6
			}
			if r := a.OpenSessions()[0]; r.Cost != want || r.FallbackSpent != .02 {
				t.Fatalf("fresh fallback rebased history: %+v", r)
			}
		})
	}
}

func TestConfirmFallbackAtExhaustedLimitStartsImmediately(t *testing.T) {
	a := ruleFixture(t)
	addOpenSpend(a, "orig", "chat-one", .2)
	a.ProcessBudgetRules()
	if !a.OpenSessions()[0].Stopped {
		t.Fatal("fixture not stopped")
	}
	setRule(t, a, "acme/cheap", "stop")
	if r := a.OpenSessions()[0]; r.Stage != "switching" || r.Stopped {
		t.Fatalf("confirm did not re-evaluate: %+v", r)
	}
}

func TestCancellationPreservesAllowanceAndRejectsStaleResumeIntent(t *testing.T) {
	a := ruleFixture(t)
	setRule(t, a, "acme/cheap", "stop")
	addOpenSpend(a, "orig", "chat-one", .05)
	if err := a.CancelOpenSessionContinuation("one"); err != nil {
		t.Fatal(err)
	}
	first := a.state.OpenPolicies["one"].CancellationID
	if err := a.CancelOpenSessionContinuation("one"); err != nil {
		t.Fatal(err)
	}
	last := a.state.OpenPolicies["one"].CancellationID
	if r := a.OpenSessions()[0]; r.Stopped || r.Limit != .1 || r.Cost != .05 {
		t.Fatalf("cancel mutated allowance: %+v", r)
	}
	addOpenSpend(a, "orig", "chat-one", .2)
	a.ProcessBudgetRules()
	if a.state.OpenPolicies["one"].Stage == "switching" {
		t.Fatal("cancelled task automatically continued")
	}
	reset := func(token string) {
		b, _ := json.Marshal(map[string]string{"id": "one", "session_id": "chat-one", "token": token})
		if err := os.WriteFile(filepath.Join(a.experienceDir(), "continue-reset-one.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		a.ProcessBudgetRules()
	}
	reset(first)
	if !a.state.OpenPolicies["one"].ManualStop {
		t.Fatal("stale new-message intent released cancellation")
	}
	reset(last)
	if a.state.OpenPolicies["one"].ManualStop || a.state.OpenPolicies["one"].Stage != "switching" {
		t.Fatal("fresh explicit message did not restore normal workflow")
	}
}
func TestStopOpenTUIAcknowledgementKeepsAllowance(t *testing.T) {
	a := ruleFixture(t)
	addOpenSpend(a, "orig", "chat-one", .04)
	done := make(chan error, 1)
	go func() { done <- a.RequestOpenSessionAbort("one") }()
	requestPath := filepath.Join(a.experienceDir(), "stop-tui-one.json")
	deadline := time.Now().Add(2 * time.Second)
	var req abortRequest
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(requestPath)
		if err == nil && json.Unmarshal(b, &req) == nil && req.ID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if req.SessionID != "chat-one" || req.Instance != "one" {
		t.Fatalf("wrong abort target: %+v", req)
	}
	b, _ := json.Marshal(abortStatus{ID: req.ID, Status: "stopped"})
	if err := os.WriteFile(filepath.Join(a.experienceDir(), "abort-status-"+req.ID+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r := a.OpenSessions()[0]; r.Limit != .1 || r.Cost != .04 || r.Stopped || !r.ManualStop {
		t.Fatalf("stop changed allowance: %+v", r)
	}
}

func TestFallbackCapEditReevaluatesCurrentSpend(t *testing.T) {
	a := ruleFixture(t)
	setRule(t, a, "acme/cheap", "stop")
	addOpenSpend(a, "orig", "chat-one", .2)
	a.ProcessBudgetRules()
	acknowledge(t, a, "prepared", "orig")
	acknowledge(t, a, "confirmed")
	addOpenSpend(a, "fb", "chat-one", .08)
	a.ProcessBudgetRules()
	if !a.OpenSessions()[0].Stopped {
		t.Fatal("fallback cap did not stop")
	}
	if err := a.SetOpenSessionRule("one", BudgetRule{Rule: "switch", FallbackModel: "acme/cheap", FallbackLimit: .1, Then: "stop"}); err != nil {
		t.Fatal(err)
	}
	if r := a.OpenSessions()[0]; r.Stopped || r.FallbackSpent != .08 || r.Cost != .2 {
		t.Fatalf("fallback cap edit rebased or stayed stopped: %+v", r)
	}
	if err := a.SetOpenSessionRule("one", BudgetRule{Rule: "switch", FallbackModel: "acme/cheap", FallbackLimit: .02, Then: "go"}); err != nil {
		t.Fatal(err)
	}
	if a.OpenSessions()[0].Stopped {
		t.Fatal("explicit Keep going stayed stopped")
	}
}

func TestLaterFallbackProviderFailureAfterCancellation(t *testing.T) {
	a := ruleFixture(t)
	setRule(t, a, "acme/cheap", "stop")
	addOpenSpend(a, "orig", "chat-one", .2)
	a.ProcessBudgetRules()
	acknowledge(t, a, "prepared", "orig")
	acknowledge(t, a, "confirmed")
	oldGeneration := a.state.OpenPolicies["one"].TransitionID
	if err := a.CancelOpenSessionContinuation("one"); err != nil {
		t.Fatal(err)
	}
	p := a.state.OpenPolicies["one"]
	reset, _ := json.Marshal(map[string]string{"id": "one", "session_id": "chat-one", "token": p.CancellationID})
	if err := os.WriteFile(filepath.Join(a.experienceDir(), "continue-reset-one.json"), reset, 0600); err != nil {
		t.Fatal(err)
	}
	a.ProcessBudgetRules()
	writeFailure := func(generation string) {
		b, _ := json.Marshal(map[string]string{"id": "one", "session_id": "chat-one", "key": "acme/cheap", "generation": generation, "detail": "Fallback acme/cheap stopped: rate limit. No other provider was tried."})
		if err := os.WriteFile(filepath.Join(a.experienceDir(), "fallback-error-one.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		a.ProcessBudgetRules()
	}
	writeFailure(oldGeneration)
	if a.OpenSessions()[0].Stopped {
		t.Fatal("stale failure affected the new user turn")
	}
	writeFailure("")
	if r := a.OpenSessions()[0]; !r.Stopped || r.Detail != "Fallback acme/cheap stopped: rate limit. No other provider was tried." || r.Limit != .1 {
		t.Fatalf("later failure not surfaced: %+v", r)
	}
}

func TestFallbackFailureAndEligibility(t *testing.T) {
	a := ruleFixture(t)
	if err := a.SetOpenSessionRule("one", BudgetRule{Rule: "switch", FallbackModel: "acme/no-tools", Then: "stop", FallbackLimit: .05}); err == nil {
		t.Fatal("explicitly unsupported tools accepted")
	}
	if err := a.SetOpenSessionRule("one", BudgetRule{Rule: "switch", FallbackModel: "acme/unknown-tools", Then: "stop", FallbackLimit: .05}); err != nil {
		t.Fatal("missing metadata disabled a configured model", err)
	}
	setRule(t, a, "acme/cheap", "stop")
	reportTUI(t, a, "two", "chat-one", false)
	addOpenSpend(a, "orig", "chat-one", .2)
	a.ProcessBudgetRules()
	if !a.OpenSessions()[0].Stopped {
		t.Fatal("shared conversation automatically continued")
	}
	b := ruleFixture(t)
	setRule(t, b, "acme/cheap", "stop")
	addOpenSpend(b, "orig", "chat-one", .2)
	b.ProcessBudgetRules()
	acknowledge(t, b, "failed")
	generation := b.state.OpenPolicies["one"].TransitionID
	b.ProcessBudgetRules()
	if !b.OpenSessions()[0].Stopped || b.state.OpenPolicies["one"].TransitionID != generation {
		t.Fatal("failed continuation retried")
	}
	c := ruleFixture(t)
	addOpenSpend(c, "orig", "chat-one", 1)
	c.ProcessBudgetRules()
	if err := c.ResumeOpenSession("one", .5); err != nil {
		t.Fatal(err)
	}
	if !c.OpenSessions()[0].Stopped {
		t.Fatal("insufficient increase unpaused session")
	}
}

func TestFallbackPaidChoicesMatchModelPopup(t *testing.T) {
	a := ruleFixture(t)
	file := filepath.Join(a.experienceDir(), "models-test.json")
	raw, _ := os.ReadFile(file)
	var inv ModelInventory
	if err := json.Unmarshal(raw, &inv); err != nil {
		t.Fatal(err)
	}
	// The original model may disappear from a refreshed inventory. Its catalog
	// price still exists, so affordable paid alternatives must not disappear.
	inv.Models = inv.Models[1:]
	yes := true
	inv.Models = append(inv.Models, ConfiguredModel{Key: "acme/input-heavy", Name: "Input heavy", Category: "chat", ToolCall: &yes, Rate: &ModelPrice{Input: 4, Output: 1}})
	raw, _ = json.Marshal(inv)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	choices := map[string]FallbackChoice{}
	for _, m := range a.FallbackCandidates("one") {
		choices[m.Key] = m
	}
	for _, key := range []string{"acme/cheap", "acme/input-heavy"} {
		m := choices[key]
		if !m.Eligible || !m.Cheaper || m.Free || m.Input <= 0 || m.Output <= 0 {
			t.Fatalf("paid choice hidden or unpriced: %+v", m)
		}
	}
	if err := a.SetOpenSessionRule("one", BudgetRule{Rule: "switch", FallbackModel: "acme/input-heavy", FallbackLimit: .05, Then: "stop"}); err != nil {
		t.Fatal(err)
	}
}

func TestFallbackUsesOpenTUIProcessAndWaitsForDiscovery(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			a := ruleFixture(t)
			setRule(t, a, "acme/cheap", "stop")
			file := filepath.Join(a.experienceDir(), "models-test.json")
			raw, _ := os.ReadFile(file)
			var inv ModelInventory
			json.Unmarshal(raw, &inv)
			// The owning TUI is real, but its chat fell out of the bounded list.
			inv.Sessions = []string{"historical-chat"}
			inv.ContinueProtocol = 1
			raw, _ = json.Marshal(inv)
			os.WriteFile(file, raw, 0600)
			addOpenSpend(a, "orig", "chat-one", .2)
			if legacy {
				a.setRuleFailure("one", "Fallback unavailable in this TUI. Restart OpenCode to load automatic continuation.")
			}
			a.ProcessBudgetRules()
			p := a.state.OpenPolicies["one"]
			if p.Stopped || p.TransitionID != "" || p.TransitionAt == 0 {
				t.Fatalf("discovery miss permanently stopped or dispatched: %+v", p)
			}
			if openVerdicts(t, a).Sessions["chat-one"].State != "over" {
				t.Fatal("original paid work unblocked during discovery")
			}
			// A second TUI must not receive the command through shared history.
			other := inv
			other.PID, other.Instance, other.ContinueProtocol = inv.PID+1, "other", 2
			other.Sessions = []string{"chat-one"}
			b, _ := json.Marshal(other)
			os.WriteFile(filepath.Join(a.experienceDir(), "models-other.json"), b, 0600)
			a.ProcessBudgetRules()
			if a.state.OpenPolicies["one"].TransitionID != "" {
				t.Fatal("another TUI claimed this conversation")
			}
			inv.ContinueProtocol = 2
			raw, _ = json.Marshal(inv)
			os.WriteFile(file, raw, 0600)
			a.ProcessBudgetRules()
			p = a.state.OpenPolicies["one"]
			if p.Stopped || p.Stage != "switching" || p.TransitionID == "" {
				t.Fatalf("discovery did not continue automatically: %+v", p)
			}
			command := filepath.Join(a.experienceDir(), fmt.Sprintf("continue-%d-%s.json", inv.PID, p.TransitionID))
			if _, err := os.Stat(command); err != nil {
				t.Fatal("command not sent to owning TUI", err)
			}
		})
	}
	a := ruleFixture(t)
	setRule(t, a, "acme/cheap", "stop")
	os.Remove(filepath.Join(a.experienceDir(), "models-test.json"))
	addOpenSpend(a, "orig", "chat-one", .2)
	a.ProcessBudgetRules()
	p := a.state.OpenPolicies["one"]
	p.TransitionAt = time.Now().Add(-16 * time.Second).Unix()
	a.state.OpenPolicies["one"] = p
	a.ProcessBudgetRules()
	if !a.state.OpenPolicies["one"].Stopped {
		t.Fatal("unavailable connection waited without a bound")
	}
}

func TestContinuationTimeoutEmitsOnce(t *testing.T) {
	a := ruleFixture(t)
	setRule(t, a, "acme/cheap", "stop")
	addOpenSpend(a, "orig", "chat-one", .2)
	a.ProcessBudgetRules()
	p := a.state.OpenPolicies["one"]
	p.TransitionAt = time.Now().Add(-100 * time.Second).Unix()
	a.state.OpenPolicies["one"] = p
	a.ProcessBudgetRules()
	failed := a.state.OpenPolicies["one"]
	if !failed.Stopped || failed.Event == "" {
		t.Fatal("timeout did not produce the stop event")
	}
	for i := 0; i < 3; i++ {
		a.ProcessBudgetRules()
	}
	after := a.state.OpenPolicies["one"]
	if after.Event != failed.Event || after.TransitionID != failed.TransitionID {
		t.Fatal("stopped timeout re-emitted a toast or reissued continuation")
	}
}
