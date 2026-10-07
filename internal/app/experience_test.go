package app_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/app"
	"github.com/vivek9102/opencode-odometer/internal/plugin"
	"github.com/vivek9102/opencode-odometer/internal/prices"
)

func inventory(t *testing.T, a *app.App, models []app.ConfiguredModel) {
	t.Helper()
	b, _ := json.Marshal(app.ModelInventory{Models: models})
	if err := os.WriteFile(filepath.Join(filepath.Dir(a.Contract.BudgetFile), "models.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetPublishesConfirmedFreeModelsWithoutResettingSpend(t *testing.T) {
	a := newTestApp(t)
	a.SetSeeded(true)
	a.SetLimit(1)
	a.SetEnabled(true)
	spend(a, "paid", "root", "expensive", 1000000, 1000000)
	before := a.ActiveSessionBudget().Cost
	inventory(t, a, []app.ConfiguredModel{{Key: "custom/free-tier", Category: "chat"}, {Key: "custom/unpriced", Category: "chat", Rate: &app.ModelPrice{}}, {Key: "custom/embedding-free", Category: "embedding"}, {Key: "acme/expensive", Category: "chat"}})
	a.PublishBudget()
	read := func() plugin.BudgetFile {
		var doc plugin.BudgetFile
		b, err := os.ReadFile(a.Contract.BudgetFile)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	doc := read()
	if len(doc.FreeModels) != 1 || doc.FreeModels[0] != "custom/free-tier" {
		t.Fatalf("wrong free exemptions: %v", doc.FreeModels)
	}
	if !doc.Enabled || doc.Sessions["root"].Cost != before || doc.Sessions["root"].Limit != 1 {
		t.Fatalf("budget was reset: %+v", doc)
	}
	if err := a.SaveOverride("custom/free-tier", prices.Rate{Input: 1, Output: 2}); err != nil {
		t.Fatal(err)
	}
	a.PublishBudget()
	if got := read().FreeModels; len(got) != 0 {
		t.Fatalf("paid local override was exempted: %v", got)
	}
}

func TestConfiguredAlternativesIncludeTinyAndFreeModels(t *testing.T) {
	a := newTestApp(t)
	models := []app.ConfiguredModel{{Key: "acme/expensive"}, {Key: "acme/tiny-nano"}, {Key: "acme/free-tier"}, {Key: "custom/google/gemma-4-31b-it"}}
	for i := 0; i < 12; i++ {
		models = append(models, app.ConfiguredModel{Key: fmt.Sprintf("custom/model-%d-free", i)})
	}
	inventory(t, a, models)
	all := a.ModelChoices("acme/expensive")
	if len(all) != len(models) {
		t.Fatalf("got %d configured choices, want %d", len(all), len(models))
	}
	cheaper := a.CheaperThan("acme/expensive")
	if len(cheaper) != 14 {
		t.Fatalf("want nano and all 13 free models, got %v", cheaper)
	}
	for _, m := range all {
		if m.Key == "custom/google/gemma-4-31b-it" && m.Provider != "custom" {
			t.Fatal("nested model ID lost provider")
		}
	}
	if len(a.CheaperThan("custom/unknown")) != 13 {
		t.Fatal("unknown current price must still offer every known free model")
	}
}

func TestCheaperComparisonIncludesInputCost(t *testing.T) {
	a := newTestApp(t)
	inventory(t, a, []app.ConfiguredModel{{Key: "acme/expensive"}, {Key: "acme/high-input", Rate: &app.ModelPrice{Input: 100, Output: 1}}})
	if got := a.CheaperThan("acme/expensive"); len(got) != 0 {
		t.Fatalf("output-only comparison suggests higher overall price: %v", got)
	}
}

func TestPauseAndStartupPreferencePreserveBudgetSpend(t *testing.T) {
	a := newTestApp(t)
	a.SetSeeded(true)
	a.SetLimit(1)
	a.SetEnabled(true)
	spend(a, "m1", "s1", "cheap", 100000, 100000)
	before := a.ActiveSessionBudget().Cost
	prefs := a.Preferences()
	prefs.AutoStart = false
	prefs.Paused = true
	if err := a.SetPreferences(prefs); err != nil {
		t.Fatal(err)
	}
	a.PublishBudget()
	raw, _ := os.ReadFile(a.Contract.BudgetFile)
	var doc struct{ Enabled bool }
	_ = json.Unmarshal(raw, &doc)
	if doc.Enabled {
		t.Fatal("pause did not suspend plugin enforcement")
	}
	prefs.Paused = false
	if err := a.SetPreferences(prefs); err != nil {
		t.Fatal(err)
	}
	if a.ActiveSessionBudget().Cost != before {
		t.Fatal("resuming enforcement reset spend")
	}
}

func TestModelSwitchSurvivesChatOwnershipEvictionAndProcessRestart(t *testing.T) {
	a := newTestApp(t)
	dir := filepath.Dir(a.Contract.BudgetFile)
	for _, inv := range []app.ModelInventory{
		{PID: 991, Updated: time.Now().Unix(), Sessions: []string{"s1"}, Models: []app.ConfiguredModel{{Key: "custom/google/gemma-4"}}},
		{PID: 992, Updated: time.Now().Unix(), Sessions: []string{"other"}, Models: []app.ConfiguredModel{{Key: "custom/google/gemma-4"}}},
	} {
		b, _ := json.Marshal(map[string]any{"pid": inv.PID, "updated": inv.Updated, "sessions": inv.Sessions, "models": inv.Models, "switch_protocol": 3})
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("models-%d.json", inv.PID)), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	req, err := a.RequestModelSwitch("recent-chat-not-in-bounded-history", "custom/google/gemma-4")
	if err != nil {
		t.Fatal(err)
	}
	var got app.ModelSwitch
	b, err := os.ReadFile(filepath.Join(dir, "switch-session-recent-chat-not-in-bounded-history.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &got)
	if got.Key != req.Key || got.SessionID != "recent-chat-not-in-bounded-history" {
		t.Fatalf("wrong route: %v", got)
	}
	if a.ModelSwitchStatus(req.ID).Status != "queued" {
		t.Fatal("durable selection was not acknowledged")
	}
	if a.PendingModelSwitch(req.SessionID).ID != req.ID {
		t.Fatal("reopening the picker lost the saved selection")
	}
	if !req.Persistent {
		t.Fatal("chat choice is still one-shot")
	}
	newer, err := a.RequestModelSwitch(got.SessionID, got.Key)
	if err != nil {
		t.Fatal(err)
	}
	if a.ModelSwitchStatus(req.ID).Status != "cancelled" || a.ModelSwitchStatus(newer.ID).Status != "queued" {
		t.Fatal("replacing a selection did not update its status")
	}
	for _, pid := range []int{991, 992} {
		if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("switch-%d.json", pid))); !os.IsNotExist(err) {
			t.Fatal("selection still depends on a process ID")
		}
	}
	if err := a.ClearModelSwitch(req.SessionID); err != nil {
		t.Fatal(err)
	}
	if a.PendingModelSwitch(req.SessionID).ID != "" || a.ModelSwitchStatus(newer.ID).Status != "cancelled" {
		t.Fatal("Follow OpenCode did not clear the chat choice")
	}
}
