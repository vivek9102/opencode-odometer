//go:build opencode_v2

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func reportV2Tabs(t *testing.T, a *App, terminal, focused string, tabs ...string) {
	t.Helper()
	e := OpenTUI{ID: terminal, Name: "terminal-" + terminal, PID: os.Getpid(), Started: time.Now().Add(-time.Minute).UnixMilli(), Updated: time.Now().Unix(), SessionID: focused, APIMajor: 2, DraftStarted: time.Now().Add(-time.Second).UnixMilli(), Tabs: []OpenTUI{}}
	for _, sid := range tabs {
		e.Tabs = append(e.Tabs, OpenTUI{SessionID: sid, Started: time.Now().Add(-time.Minute).UnixMilli(), Model: "acme/sonnet", Title: "Same title", Focused: sid == focused})
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a.experienceDir(), "tui-"+terminal+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestV2TabsHaveIndependentStableConversationBudgets(t *testing.T) {
	a, _, _ := newTestApp(t)
	reportV2Tabs(t, a, "terminal-one", "chat-a", "chat-a", "chat-b")
	idA, nameA := v2ConversationIdentity("chat-a")
	if idA != "v2-6b6e7f77762ce8de" || nameA != "keen-robin-6b6e7f77" {
		t.Fatal("Go and terminal identities disagree")
	}
	idB, _ := v2ConversationIdentity("chat-b")
	if len(a.OpenSessions()) != 2 {
		t.Fatal("inactive tab not listed")
	}
	if err := a.SetOpenSessionBudget(idA, .2, "hard", true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetOpenSessionBudget(idB, .5, "hard", true); err != nil {
		t.Fatal(err)
	}
	addOpenSpend(a, "charge-a", "chat-a", .1)
	addOpenSpend(a, "charge-b", "chat-b", .3)
	reportV2Tabs(t, a, "terminal-one", "chat-b", "chat-a", "chat-b", "chat-c")
	reportV2Tabs(t, a, "terminal-two", "chat-a", "chat-a", "chat-b")
	rows := a.OpenSessions()
	if len(rows) != 3 {
		t.Fatalf("same chat duplicated across terminals: %d", len(rows))
	}
	for _, r := range rows {
		switch r.ID {
		case idA:
			if r.Name != nameA || r.Limit != .2 || r.Cost != .1 {
				t.Fatalf("A changed: %+v", r)
			}
		case idB:
			if r.Limit != .5 || r.Cost != .3 {
				t.Fatalf("B changed: %+v", r)
			}
		default:
			if r.Enabled {
				t.Fatal("new conversation inherited budget")
			}
		}
	}
	doc := openVerdicts(t, a)
	if doc.Sessions["chat-a"].Limit != .2 || doc.Sessions["chat-b"].Limit != .5 {
		t.Fatal("verdicts not scoped to conversation")
	}
	reportV2Tabs(t, a, "terminal-one", "", "chat-a", "chat-b", "chat-c")
	for _, r := range a.OpenSessions() {
		if r.ID == idA && r.Limit != .2 {
			t.Fatal("new-session screen cleared old budget")
		}
	}
}

func TestV2ReopenKeepsBudgetAfterPruningAndWidgetRestart(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.cfg.MaxMessages = 1
	reportV2Tabs(t, a, "first", "chat-a", "chat-a")
	id, name := v2ConversationIdentity("chat-a")
	if err := a.SetOpenSessionBudget(id, 1, "hard", true); err != nil {
		t.Fatal(err)
	}
	addOpenSpend(a, "charge-a", "chat-a", .6)
	addOpenSpend(a, "charge-b", "chat-a", .6)
	// Closing every terminal removes rows, but not the conversation policy.
	os.Remove(filepath.Join(a.experienceDir(), "tui-first.json"))
	a.RefreshOpenSessions()
	a.SaveState()
	if len(a.OpenSessions()) != 0 {
		t.Fatal("closed conversation displayed")
	}
	if doc := openVerdicts(t, a); doc.Sessions["chat-a"].State != "over" {
		t.Fatal("closing tabs released an exhausted budget")
	}
	if len(a.Ledger.GetMessagesSnapshot()) != 1 {
		t.Fatal("closed policy prevented pruning")
	}
	restarted, err := New(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	reportV2Tabs(t, restarted, "second", "chat-a", "chat-a")
	row := restarted.OpenSessions()[0]
	if row.ID != id || row.Name != name || row.Limit != 1 || row.Cost != 1.2 || !row.Stopped {
		t.Fatalf("reopen lost identity/budget/spend: %+v", row)
	}
	if err = restarted.SetOpenSessionBudget(id, 2, "hard", true); err != nil {
		t.Fatal(err)
	}
	if row = restarted.OpenSessions()[0]; row.Cost != 1.2 || row.Stopped {
		t.Fatalf("editing cap lost accounting: %+v", row)
	}
}

func TestV2StartupDraftTransfersOnlyToNewConversation(t *testing.T) {
	a, _, _ := newTestApp(t)
	reportV2Tabs(t, a, "first", "", "old-chat")
	if err := a.SetOpenSessionBudget("first", .2, "hard", true); err != nil {
		t.Fatal(err)
	}
	reportV2Tabs(t, a, "first", "old-chat", "old-chat")
	id, _ := v2ConversationIdentity("old-chat")
	for _, r := range a.OpenSessions() {
		if r.ID == id && r.Enabled {
			t.Fatal("draft applied to existing chat")
		}
	}
	// Simulate creation after the home-screen draft was armed.
	reportV2Tabs(t, a, "first", "", "old-chat")
	if err := a.SetOpenSessionBudget("first", .2, "hard", true); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(a.experienceDir(), "tui-first.json"))
	var e OpenTUI
	json.Unmarshal(raw, &e)
	e.SessionID = "new-chat"
	e.Tabs = append(e.Tabs, OpenTUI{SessionID: "new-chat", Started: time.Now().UnixMilli(), Model: "acme/sonnet", Focused: true})
	raw, _ = json.Marshal(e)
	os.WriteFile(filepath.Join(a.experienceDir(), "tui-first.json"), raw, 0600)
	newID, _ := v2ConversationIdentity("new-chat")
	for _, r := range a.OpenSessions() {
		if r.ID == newID && (!r.Enabled || r.Limit != .2) {
			t.Fatalf("new draft not transferred: %+v", r)
		}
	}
}
