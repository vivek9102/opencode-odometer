package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vivek9102/opencode-odometer/internal/app"
	"github.com/vivek9102/opencode-odometer/internal/opencode"
	"github.com/vivek9102/opencode-odometer/internal/prices"
)

// priceFixture writes a small price table covering a costly model and a range
// of cheaper alternatives, including one that must never be recommended.
func priceFixture(t *testing.T, dir string) string {
	t.Helper()
	doc := map[string]any{
		"reference_model": "acme/expensive",
		"models": map[string]prices.Rate{
			"acme/expensive":  {Name: "Expensive", Input: 5, Output: 25},
			"acme/midrange":   {Name: "Midrange", Input: 2, Output: 10},
			"acme/cheap":      {Name: "Cheap", Input: 0.5, Output: 2},
			"acme/free-tier":  {Name: "Free Tier", Free: true},
			"acme/text-embed": {Name: "Embeddings", Input: 0.01, Output: 0.02},
			"acme/tiny-nano":  {Name: "Nano", Input: 0.01, Output: 0.05},
			"other/unrelated": {Name: "Other Provider", Input: 1, Output: 4},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal prices: %v", err)
	}
	p := filepath.Join(dir, "prices.json")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatalf("write prices: %v", err)
	}
	return p
}

func newTestApp(t *testing.T) *app.App {
	t.Helper()
	dir := t.TempDir()
	a, err := app.New(app.Config{
		PricesFile: priceFixture(t, dir),
		StateFile:  filepath.Join(dir, "state.json"),
		BudgetFile: filepath.Join(dir, "budget.json"),
		GraceFile:  filepath.Join(dir, "grace.json"),
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	return a
}

func spend(a *app.App, id, sid, model string, in, out int64) {
	a.ApplyMessage(opencode.Message{
		ID: id, SessionID: sid, Provider: "acme", Model: model,
		Tokens:      opencode.TokenUsage{Input: in, Output: out},
		TimeCreated: 1,
	})
}

// TestCheaperThanRanksRealAlternatives replaces the previous hardcoded model
// list: suggestions come from configured models and put free options first.
func TestCheaperThanRanksRealAlternatives(t *testing.T) {
	a := newTestApp(t)
	spend(a, "m1", "s1", "expensive", 1000, 1000)

	inventory(t, a, []app.ConfiguredModel{{Key: "acme/expensive"}, {Key: "acme/midrange"}, {Key: "acme/cheap"}, {Key: "acme/free-tier"}, {Key: "acme/text-embed"}, {Key: "acme/tiny-nano"}})
	alts := a.CheaperThan("acme/expensive")
	if len(alts) == 0 {
		t.Fatal("expected cheaper alternatives")
	}

	if !alts[0].Free {
		t.Errorf("free models must be offered first, got %q", alts[0].Key)
	}

	seen := map[string]bool{}
	for _, x := range alts {
		seen[x.Key] = true
		if x.Key == "acme/expensive" {
			t.Error("must not suggest the current model")
		}
	}
	if !seen["acme/cheap"] || !seen["acme/midrange"] {
		t.Errorf("expected cheaper paid models in %v", seen)
	}
	// All configured options remain visible, with specialised categories.
	if !seen["acme/text-embed"] || !seen["acme/tiny-nano"] {
		t.Errorf("configured specialised and nano models must remain visible: %v", seen)
	}

	// Paid alternatives sort by comparison price, cheapest first.
	var paid []app.Alternative
	for _, x := range alts {
		if !x.Free {
			paid = append(paid, x)
		}
	}
	for i := 1; i < len(paid); i++ {
		if paid[i-1].Ratio > paid[i].Ratio {
			t.Errorf("paid alternatives out of order: %v", paid)
			break
		}
	}
}

// TestTopSpendersIdentifiesDrivers backs the "WHAT SPENT IT" panel.
func TestTopSpendersIdentifiesDrivers(t *testing.T) {
	a := newTestApp(t)
	spend(a, "m1", "s1", "expensive", 1_000_000, 1_000_000) // $30
	spend(a, "m2", "s1", "cheap", 1_000_000, 1_000_000)     // $2.5
	spend(a, "m3", "s2", "midrange", 1_000_000, 1_000_000)  // other session

	drivers := a.TopSpenders("s1", 3)
	if len(drivers) != 2 {
		t.Fatalf("expected 2 drivers for s1, got %d (%v)", len(drivers), drivers)
	}
	if drivers[0].Key != "acme/expensive" {
		t.Errorf("largest driver = %q, want acme/expensive", drivers[0].Key)
	}
	if drivers[0].Output <= drivers[1].Output {
		t.Error("drivers must be sorted by spend, highest first")
	}
}

// TestActiveSessionBudgetMatchesEnforcement is the important one: the UI used
// to show TRIP spend against the limit while the plugin blocked on SESSION
// spend, so the bar could read 0% while a turn was actually being refused.
func TestActiveSessionBudgetMatchesEnforcement(t *testing.T) {
	a := newTestApp(t)
	a.Budget.Cfg.Enabled = true
	a.Budget.Cfg.SessionLimitUSD = 10
	a.Budget.Cfg.Mode = "hard"
	a.SetSeeded(true)

	// $30 in one session: well over the $10 cap.
	spend(a, "m1", "s1", "expensive", 1_000_000, 1_000_000)

	// A trip reset zeroes TRIP but must not clear the session's breach.
	a.ResetTrip()

	sb := a.ActiveSessionBudget()
	if sb.SessionID != "s1" {
		t.Errorf("active session = %q, want s1", sb.SessionID)
	}
	if sb.State != "over" {
		t.Errorf("session state = %q, want over (trip reset must not hide a breach)", sb.State)
	}

	tripCost, _ := a.Ledger.GetTotals("TRIP")
	if tripCost != 0 {
		t.Errorf("trip cost = %v, want 0 after reset", tripCost)
	}

	// The published contract must agree with what the UI derives.
	a.PublishBudget()
	raw, err := os.ReadFile(a.Contract.BudgetFile)
	if err != nil {
		t.Fatalf("read budget.json: %v", err)
	}
	var doc struct {
		Sessions map[string]struct {
			State string  `json:"state"`
			Cost  float64 `json:"cost"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse budget.json: %v", err)
	}
	published, ok := doc.Sessions["s1"]
	if !ok {
		t.Fatal("session s1 missing from budget.json")
	}
	if published.State != sb.State {
		t.Errorf("published state %q != UI state %q", published.State, sb.State)
	}
}

// TestBaselineGrandfathersPriorSpend: enabling a limit must not retroactively
// block a session that had already spent more than the cap.
func TestBaselineGrandfathersPriorSpend(t *testing.T) {
	a := newTestApp(t)
	a.SetSeeded(true)
	spend(a, "m1", "s1", "expensive", 1_000_000, 1_000_000) // $30 before the limit

	a.Budget.Cfg.SessionLimitUSD = 10
	a.Budget.Cfg.Enabled = true
	a.Rebase(true) // snapshot current spend as the baseline

	if sb := a.ActiveSessionBudget(); sb.State != "ok" {
		t.Errorf("state = %q, want ok: spend before enabling must be grandfathered", sb.State)
	}

	// New spend past the cap does count.
	spend(a, "m2", "s1", "expensive", 1_000_000, 1_000_000)
	if sb := a.ActiveSessionBudget(); sb.State != "over" {
		t.Errorf("state = %q, want over after new spend exceeds the cap", sb.State)
	}
}
