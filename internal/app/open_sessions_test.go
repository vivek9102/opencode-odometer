package app

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/ledger"
	"github.com/vivek9102/opencode-odometer/internal/plugin"
)

func reportTUI(t *testing.T, a *App, id, sid string, closed bool) OpenTUI {
	t.Helper()
	e := OpenTUI{ID: id, Name: "project-" + id, PID: os.Getpid(), Started: time.Now().Add(-10 * time.Second).UnixMilli(), Updated: time.Now().Unix(), SessionID: sid, Closed: closed, Model: "acme/sonnet"}
	b, _ := json.Marshal(e)
	if err := os.WriteFile(filepath.Join(a.experienceDir(), "tui-"+id+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return e
}

func openVerdicts(t *testing.T, a *App) plugin.BudgetFile {
	t.Helper()
	a.PublishBudget()
	raw, err := os.ReadFile(a.Contract.BudgetFile)
	if err != nil {
		t.Fatal(err)
	}
	var doc plugin.BudgetFile
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func addOpenSpend(a *App, mid, sid string, cost float64) {
	a.Ledger.Put(ledger.Record{MID: mid, SessionID: sid, Provider: "acme", Model: "sonnet", Cost: cost, Timestamp: time.Now().Format("2006-01-02 15:04:05")})
}

func TestOpenSessionSpendSurvivesPruningAndWidgetRestart(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.cfg.MaxMessages = 1
	reportTUI(t, a, "one", "chat-one", false)
	if err := a.SetOpenSessionBudget("one", 1, "hard", true); err != nil {
		t.Fatal(err)
	}
	addOpenSpend(a, "first", "chat-one", .6)
	addOpenSpend(a, "second", "chat-one", .6)
	addOpenSpend(a, "unrelated", "saved-chat", 5)
	a.SaveState()
	if row := a.OpenSessions()[0]; row.Cost != 1.2 || row.State != "over" {
		t.Fatalf("prune changed live verdict: %+v", row)
	}
	restarted, err := New(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if row := restarted.OpenSessions()[0]; row.Cost != 1.2 || row.State != "over" {
		t.Fatalf("restart changed live verdict: %+v", row)
	}
	reportTUI(t, restarted, "one", "chat-one", true)
	restarted.RefreshOpenSessions()
	restarted.SaveState()
	if len(restarted.Ledger.GetMessagesSnapshot()) != 1 {
		t.Fatal("closed TUI kept records protected")
	}
	if math.Abs(restarted.Ledger.TotalCost-6.2) > 1e-9 {
		t.Fatalf("history totals changed: %v", restarted.Ledger.TotalCost)
	}
}

func TestOpenSessionsOnlyDoesNotRestoreGlobalDefault(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.SetLimit(1)
	a.SetEnabled(true)
	a.cfg.OpenSessionsOnly = true
	restarted, err := New(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if doc := openVerdicts(t, restarted); len(doc.Sessions) != 0 || doc.SessionLimitUSD != 0 {
		t.Fatalf("legacy default leaked: %+v", doc)
	}
	reportTUI(t, restarted, "new", "new-chat", false)
	if restarted.OpenSessions()[0].Enabled {
		t.Fatal("new TUI inherited an old global limit")
	}
}

func TestOpenDockTotalsDeduplicateChatsAndExcludeClosedRate(t *testing.T) {
	a, _, _ := newTestApp(t)
	reportTUI(t, a, "one", "shared", false)
	reportTUI(t, a, "two", "shared", false)
	reportTUI(t, a, "other", "other", false)
	a.state.SessionParents = map[string]string{"child": "shared"}
	addOpenSpend(a, "root", "shared", .5)
	addOpenSpend(a, "child", "child", .25)
	addOpenSpend(a, "other", "other", 1)
	addOpenSpend(a, "closed", "closed", 9)
	a.Ledger.Put(ledger.Record{MID: "history", SessionID: "shared", Cost: 7, Timestamp: time.Now().Add(-time.Hour).Format("2006-01-02 15:04:05")})
	a.rateWindow = []RatePoint{
		{MessageID: "root", Time: time.Now(), Cost: .5},
		{MessageID: "child", Time: time.Now(), Cost: .25},
		{MessageID: "other", Time: time.Now(), Cost: 1},
		{MessageID: "closed", Time: time.Now(), Cost: 9},
		{MessageID: "history", Time: time.Now(), Cost: 7},
		{MessageID: "root", Time: time.Now().Add(-11 * time.Minute), Cost: 20},
	}
	check := func(wantCost, wantRate float64) {
		t.Helper()
		cost, rate := a.OpenSessionTotals(a.OpenSessions())
		if math.Abs(cost-wantCost) > 1e-9 || math.Abs(rate-wantRate) > 1e-9 {
			t.Fatalf("dock cost/rate = %v/%v, want %v/%v", cost, rate, wantCost, wantRate)
		}
	}
	check(1.75, 10.5)
	reportTUI(t, a, "one", "shared", true)
	check(1.75, 10.5) // Its second open TUI still owns this chat's spend.
	reportTUI(t, a, "two", "shared", true)
	check(1, 6)
	reportTUI(t, a, "other", "other", true)
	check(0, 0)
}

func TestOpenSessionsIndependentBudgetsAndCleanup(t *testing.T) {
	a, _, _ := newTestApp(t)
	reportTUI(t, a, "one", "chat-one", false)
	reportTUI(t, a, "two", "chat-two", false)
	reportTUI(t, a, "idle", "", false)
	// Saved chats and late backfill must never become open rows or live spend.
	a.Ledger.Put(ledger.Record{MID: "old", SessionID: "chat-one", Cost: 9, Timestamp: time.Now().Add(-time.Hour).Format("2006-01-02 15:04:05")})
	addOpenSpend(a, "history", "saved-chat", 7)
	rows := a.OpenSessions()
	if len(rows) != 3 {
		t.Fatalf("rows: %+v", rows)
	}
	for _, r := range rows {
		if r.Enabled || r.Spent != 0 {
			t.Fatalf("new TUI has history/default limit: %+v", r)
		}
	}
	if err := a.SetOpenSessionBudget("one", 1, "hard", true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetOpenSessionBudget("two", 2, "soft", true); err != nil {
		t.Fatal(err)
	}
	addOpenSpend(a, "m1", "chat-one", .8)
	addOpenSpend(a, "m2", "chat-two", .3)
	doc := openVerdicts(t, a)
	if doc.Sessions["chat-one"].State != "warn" || doc.Sessions["chat-two"].Limit != 2 || doc.Sessions["chat-two"].Mode != "soft" {
		t.Fatalf("independent verdicts: %+v", doc)
	}
	a.SetSessionParent("child", "chat-one")
	addOpenSpend(a, "m3", "child", .4)
	doc = openVerdicts(t, a)
	if doc.Sessions["child"].Cost != 1.2 || doc.Sessions["child"].State != "over" || doc.Sessions["child"].BudgetSessionID != "one" {
		t.Fatalf("child escaped cap: %+v", doc)
	}
	if _, ok := doc.Sessions["saved-chat"]; ok {
		t.Fatal("historical chat got a budget")
	}
	if a.OpenSessions()[0].ID != "one" {
		t.Fatal("worst session not first")
	}
	if err := a.IncreaseOpenSessionBudget("one", .5); err != nil {
		t.Fatal(err)
	}
	if v := openVerdicts(t, a).Sessions["chat-one"]; v.Limit != 1.5 || v.Cost != 1.2 || v.State != "warn" {
		t.Fatalf("raise rebased spend: %+v", v)
	}
	before, _ := a.Ledger.GetTotals("TOTAL")
	reportTUI(t, a, "one", "chat-one", true)
	doc = openVerdicts(t, a)
	if len(a.OpenSessions()) != 2 {
		t.Fatal("closed TUI still present")
	}
	if _, ok := doc.Sessions["chat-one"]; ok {
		t.Fatal("closed TUI limit still enforced")
	}
	if _, ok := a.state.OpenPolicies["one"]; ok {
		t.Fatal("closed TUI policy retained")
	}
	if after, _ := a.Ledger.GetTotals("TOTAL"); after != before {
		t.Fatal("closing TUI lost history")
	}
	if err := a.IncreaseOpenSessionBudget("one", .5); err == nil {
		t.Fatal("stale row accepted mutation")
	}
}

func TestOpenSessionRestartHomeBindingAndExpiry(t *testing.T) {
	a, _, _ := newTestApp(t)
	reportTUI(t, a, "home", "", false)
	if err := a.SetOpenSessionBudget("home", 1, "hard", true); err != nil {
		t.Fatal(err)
	}
	reportTUI(t, a, "home", "new-chat", false)
	a.RefreshOpenSessions()
	addOpenSpend(a, "m1", "new-chat", .4)
	a.SaveState()
	restarted, err := New(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rows := restarted.OpenSessions(); len(rows) != 1 || !rows[0].Enabled || rows[0].Cost != .4 {
		t.Fatalf("restart lost open budget: %+v", rows)
	}
	if err := restarted.SetOpenSessionBudget("home", 2, "soft", true); err != nil {
		t.Fatal(err)
	}
	if r := restarted.OpenSessions()[0]; r.Cost != .4 {
		t.Fatal("editing reset spend")
	}
	// A new conversation in the same TUI starts without the prior cap.
	reportTUI(t, restarted, "home", "different-chat", false)
	if r := restarted.OpenSessions()[0]; r.Enabled || r.Limit != 0 {
		t.Fatalf("new chat inherited cap: %+v", r)
	}
	if err := restarted.SetOpenSessionBudget("home", 1, "hard", true); err != nil {
		t.Fatal(err)
	}
	e := reportTUI(t, restarted, "home", "different-chat", false)
	e.Updated = time.Now().Add(-11 * time.Second).Unix()
	raw, _ := json.Marshal(e)
	os.WriteFile(filepath.Join(a.experienceDir(), "tui-home.json"), raw, 0600)
	if rows := restarted.OpenSessions(); len(rows) != 0 {
		t.Fatalf("expired TUI retained: %+v", rows)
	}
	if len(restarted.state.OpenPolicies) != 0 {
		t.Fatal("expired TUI retained cap")
	}
	reportTUI(t, restarted, "fresh", "", false)
	if rows := restarted.OpenSessions(); len(rows) != 1 || rows[0].Enabled {
		t.Fatal("new TUI inherited cap")
	}
}

func TestOpenSessionValidationAndDeadProcess(t *testing.T) {
	a, _, _ := newTestApp(t)
	reportTUI(t, a, "one", "chat-one", false)
	for _, limit := range []float64{-1, math.NaN(), math.Inf(1), 0} {
		if err := a.SetOpenSessionBudget("one", limit, "hard", true); err == nil {
			t.Fatalf("accepted invalid limit %v", limit)
		}
	}
	if err := a.SetOpenSessionBudget("one", 1, "invalid", true); err == nil {
		t.Fatal("invalid mode accepted")
	}
	e := reportTUI(t, a, "dead", "chat-dead", false)
	e.PID = 2147483647
	raw, _ := json.Marshal(e)
	os.WriteFile(filepath.Join(a.experienceDir(), "tui-dead.json"), raw, 0600)
	if rows := a.OpenSessions(); len(rows) != 1 || rows[0].ID != "one" {
		t.Fatalf("dead process retained: %+v", rows)
	}
}
