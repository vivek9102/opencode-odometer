package app

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/plugin"
)

func enabledDollarBudget(t *testing.T) *App {
	t.Helper()
	a, _, _ := newTestApp(t)
	a.SetSeeded(true)
	a.ApplyMessage(asst("history", "s1", "acme", "sonnet", 5))
	a.SetLimit(1)
	a.SetMode("hard")
	a.SetEnabled(true)
	a.ApplyMessage(asst("live", "s1", "acme", "sonnet", 0.4))
	a.PublishBudget()
	a.SaveState()
	return a
}

func TestBudgetParentMetadataIngestPreservesExistingPeriod(t *testing.T) {
	a := enabledDollarBudget(t)
	a.ApplyMessage(asst("existing-child", "child", "acme", "sonnet", .3))
	a.SetEnabled(false)
	a.SetEnabled(true)
	a.ApplyMessage(asst("parent-next", "s1", "acme", "sonnet", .2))
	a.ApplyMessage(asst("child-next", "child", "acme", "sonnet", .3))
	if err := os.WriteFile(filepath.Join(filepath.Dir(a.cfg.StateFile), "events.jsonl"), []byte("{\"type\":\"session\",\"id\":\"child\",\"parentID\":\"s1\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if a.DrainSpool() != 1 {
		t.Fatal("parent metadata was not ingested")
	}
	assertBudgetSpend(t, a, .5)
	a.graceUsed["child"] = 1
	a.PublishBudget()
	if a.graceUsed["s1"] != 1 || a.graceUsed["child"] != 0 {
		t.Fatal("ancestry discovery reset an existing grace claim")
	}
	a.SetSessionParent("unrelated", "")
	a.ApplyMessage(asst("unrelated-cost", "unrelated", "acme", "sonnet", .7))
	if got := a.ActiveSessionBudget().Cost; math.Abs(got-.7) > 1e-6 {
		t.Fatalf("independent chat inherited unrelated spend: %v", got)
	}
}

func TestBudgetChildSessionsShareParentSpendAcrossRestart(t *testing.T) {
	a := enabledDollarBudget(t)
	a.SetSessionParent("child", "s1")
	a.SetSessionParent("grandchild", "child")
	a.SetSessionParent("empty-child", "s1")
	a.ApplyMessage(asst("delegated", "child", "acme", "sonnet", 0.35))
	a.ApplyMessage(asst("delegated-again", "grandchild", "acme", "sonnet", 0.3))
	a.PublishBudget()
	a.SaveState()
	for _, instance := range []*App{a, func() *App {
		b, err := New(a.cfg)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}()} {
		got := instance.ActiveSessionBudget()
		if got.SessionID != "s1" || math.Abs(got.Cost-1.05) > 1e-6 || got.State != "over" {
			t.Fatalf("parent budget = %+v", got)
		}
		instance.PublishBudget()
		doc := readBudget(t, instance.cfg.BudgetFile)
		for _, sid := range []string{"s1", "child", "grandchild", "empty-child"} {
			entry := doc["sessions"].(map[string]any)[sid].(map[string]any)
			if math.Abs(entry["cost"].(float64)-1.05) > 1e-6 || entry["state"] != "over" || entry["budget_session_id"] != "s1" {
				t.Fatalf("%s budget = %+v", sid, entry)
			}
		}
	}
}

func assertBudgetSpend(t *testing.T, a *App, want float64) {
	t.Helper()
	if got := a.ActiveSessionBudget().Cost; math.Abs(got-want) > 1e-6 {
		t.Fatalf("budget spend = %v, want %v", got, want)
	}
	doc := readBudget(t, a.cfg.BudgetFile)
	s := doc["sessions"].(map[string]any)["s1"].(map[string]any)
	if got := s["cost"].(float64); math.Abs(got-want) > 1e-6 {
		t.Fatalf("published spend = %v, want %v", got, want)
	}
}

func TestBudgetSettingsPreserveAccumulatedSpend(t *testing.T) {
	for _, action := range []string{"same limit", "same enabled", "change limit", "double limit"} {
		t.Run(action, func(t *testing.T) {
			a := enabledDollarBudget(t)
			a.graceUsed["s1"] = 1
			switch action {
			case "same limit":
				a.SetLimit(1)
			case "same enabled":
				a.SetEnabled(true)
			case "change limit":
				a.SetLimit(1.5)
			case "double limit":
				a.RaiseLimit()
			}
			assertBudgetSpend(t, a, 0.4)
			if a.graceUsed["s1"] != 1 {
				t.Fatal("settings reset claimed grace")
			}
		})
	}
}

func TestBudgetRestartRetainsSpendAndReachesDollarLimit(t *testing.T) {
	a := enabledDollarBudget(t)
	a.graceUsed["s1"] = 1
	a.state.BudgetAllow["s1"] = 0.5
	a.SaveState()
	b, err := New(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Budget.Enabled() {
		t.Fatal("restart disabled the armed limit")
	}
	assertBudgetSpend(t, b, 0.4)
	if b.graceUsed["s1"] != 1 || b.state.BudgetAllow["s1"] != 0.5 {
		t.Fatal("restart lost grace/override bookkeeping")
	}
	b.ApplyMessage(asst("later", "s1", "acme", "sonnet", 0.61))
	b.PublishBudget()
	assertBudgetSpend(t, b, 1.01)
	if b.ActiveSessionBudget().State != "over" || !b.Budget.Blocks() {
		t.Fatal("$1 cap must enforce after continued spend")
	}
}

func TestBudgetPollRefreshesAfterIdleAndWithConnectedStream(t *testing.T) {
	for _, connected := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle plugin", true: "connected stream"}[connected], func(t *testing.T) {
			a := enabledDollarBudget(t)
			a.Client = &stubClient{}
			a.SetConnected(connected, "")
			a.spoolSeen = time.Now().Add(-5 * time.Minute)
			raw, err := os.ReadFile(a.cfg.BudgetFile)
			if err != nil {
				t.Fatal(err)
			}
			var doc plugin.BudgetFile
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			doc.Updated = time.Now().Add(-5 * time.Minute).Unix()
			raw, err = json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(a.cfg.BudgetFile, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			a.Poll()
			updated := readBudget(t, a.cfg.BudgetFile)["updated"].(float64)
			if time.Now().Unix()-int64(updated) > 2 {
				t.Fatal("running odometer left budget stale; plugin will skip enforcement")
			}
			assertBudgetSpend(t, a, 0.4)
		})
	}
}
