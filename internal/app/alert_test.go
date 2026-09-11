package app_test

import (
	"path/filepath"
	"testing"

	"github.com/vivek9102/opencode-odometer/internal/app"
)

// TestBreachDrivesAlertState verifies the condition the UI uses to raise the
// budget window: enabled + enforced + state "over".
//
// The alert is what makes a limit useful — a toast behind the editor is easy
// to miss — so the signal that triggers it is worth pinning down.
func TestBreachDrivesAlertState(t *testing.T) {
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
	a.SetSeeded(true)
	a.Budget.Cfg.SessionLimitUSD = 5
	a.Budget.Cfg.Mode = "hard"
	a.SetEnabled(true)

	// Under the cap: no alert.
	spend(a, "m1", "s1", "cheap", 100_000, 100_000)
	if sb := a.ActiveSessionBudget(); sb.State == "over" {
		t.Fatalf("should not be over yet, cost=%v", sb.Cost)
	}

	// Cross it: the UI must now have everything it needs to raise the window.
	spend(a, "m2", "s1", "expensive", 1_000_000, 1_000_000)
	sb := a.ActiveSessionBudget()
	if sb.State != "over" {
		t.Fatalf("expected over, got %q (cost=%v)", sb.State, sb.Cost)
	}
	if !a.Budget.Blocks() {
		t.Error("hard mode must report as enforcing, or the alert is suppressed")
	}
	if sb.SessionID == "" {
		t.Error("alert needs a session id to avoid re-prompting for the same session")
	}

	// The screen's content must be populated, or it opens empty.
	adv := a.TopSpenders(sb.SessionID, 3)
	if len(adv) == 0 {
		t.Error("expected spend drivers for the alert")
	}
	if len(a.CheaperThan("acme/expensive")) == 0 {
		t.Error("expected cheaper alternatives for the alert")
	}

	// Raising the limit must clear the breach, so the window can be dismissed
	// and does not immediately re-open.
	a.RaiseLimit()
	a.RaiseLimit()
	if sb := a.ActiveSessionBudget(); sb.State == "over" {
		t.Errorf("raising the limit should clear the breach, still %q", sb.State)
	}
}

// TestAllowMoreClearsBreach covers the "let this one finish" action.
func TestAllowMoreClearsBreach(t *testing.T) {
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
	a.SetSeeded(true)
	a.Budget.Cfg.SessionLimitUSD = 5
	a.Budget.Cfg.Mode = "hard"
	a.SetEnabled(true)

	spend(a, "m1", "s1", "expensive", 1_000_000, 1_000_000)
	sb := a.ActiveSessionBudget()
	if sb.State != "over" {
		t.Fatalf("expected over, got %q", sb.State)
	}

	a.AllowMore(sb.SessionID, sb.Cost, a.Budget.Limit())
	if got := a.ActiveSessionBudget(); got.State == "over" {
		t.Errorf("allow-more should release the session, still %q", got.State)
	}
}

// TestDisableClearsBreach: turning enforcement off must release the session
// immediately, so the alert's escape hatch actually works.
func TestDisableClearsBreach(t *testing.T) {
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
	a.SetSeeded(true)
	a.Budget.Cfg.SessionLimitUSD = 5
	a.Budget.Cfg.Mode = "hard"
	a.SetEnabled(true)
	spend(a, "m1", "s1", "expensive", 1_000_000, 1_000_000)

	if a.ActiveSessionBudget().State != "over" {
		t.Fatal("precondition: session should be over")
	}

	a.SetEnabled(false)
	if a.Budget.Blocks() {
		t.Error("disabled enforcement must not block")
	}
	if got := a.ActiveSessionBudget(); got.State == "over" {
		t.Errorf("disabling should clear the tally, still %q", got.State)
	}
}
