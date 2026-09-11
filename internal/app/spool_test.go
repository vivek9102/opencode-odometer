package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vivek9102/opencode-odometer/internal/app"
	"github.com/vivek9102/opencode-odometer/internal/opencode"
)

// spoolLine writes one plugin event record.
func spoolLine(t *testing.T, path, id string, in, out int64) {
	t.Helper()
	rec := map[string]any{
		"type": "message", "id": id, "sessionID": "s1",
		"providerID": "acme", "modelID": "expensive",
		"tokens": map[string]any{
			"input": in, "output": out,
			"cache": map[string]any{"read": 0, "write": 0},
		},
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open spool: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write spool: %v", err)
	}
}

// TestSpoolSeedPreservesTripTotals is a regression test for real data loss.
//
// DrainSpool used to call ResetTrip() on its first batch, mirroring the HTTP
// seed. But the spool is a rolling window of recent turns, not a full history,
// so the baseline was rebased onto a partial total: a ledger of 6561 messages
// / $378 collapsed to 358 / $60, and the trip figure was destroyed. Seeding
// must mark the ledger seeded (to un-gate the burn rate) WITHOUT touching the
// trip baseline.
func TestSpoolSeedPreservesTripTotals(t *testing.T) {
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

	// Existing history, as if restored from disk.
	spend(a, "old1", "s1", "expensive", 1_000_000, 1_000_000) // $30
	spend(a, "old2", "s1", "expensive", 1_000_000, 1_000_000) // $30
	totalBefore, _ := a.Ledger.GetTotals("TOTAL")
	tripBefore, _ := a.Ledger.GetTotals("TRIP")
	if totalBefore <= 0 || tripBefore <= 0 {
		t.Fatalf("precondition: total=%v trip=%v", totalBefore, tripBefore)
	}

	// The plugin backfills only its recent window.
	spoolLine(t, filepath.Join(dir, "events.jsonl"), "new1", 1_000_000, 1_000_000)
	a.DrainSpool()

	totalAfter, _ := a.Ledger.GetTotals("TOTAL")
	tripAfter, _ := a.Ledger.GetTotals("TRIP")

	if totalAfter <= totalBefore {
		t.Errorf("total must grow with new spend: before=%v after=%v",
			totalBefore, totalAfter)
	}
	if tripAfter <= tripBefore {
		t.Errorf("trip must not be reset by spool seeding: before=%v after=%v",
			tripBefore, tripAfter)
	}
	if len(a.Ledger.GetMessagesSnapshot()) != 3 {
		t.Errorf("expected 3 messages, got %d", len(a.Ledger.GetMessagesSnapshot()))
	}

	// Seeding must be recorded, or the burn rate stays gated off forever.
	if !a.Seeded() {
		t.Error("first spool batch should mark the ledger seeded")
	}
}

// TestSpoolFeedsBurnRate covers the other half: once seeded, live spend
// arriving via the spool must register on the burn rate. It read $0.00/hr
// permanently because seeding required an HTTP connection that never existed
// in the default in-process mode.
func TestSpoolFeedsBurnRate(t *testing.T) {
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
	spoolPath := filepath.Join(dir, "events.jsonl")

	// First batch seeds; backfilled history must not spike the rate.
	spoolLine(t, spoolPath, "hist", 1_000_000, 1_000_000)
	a.DrainSpool()
	if rate := a.BurnRate(); rate != 0 {
		t.Errorf("backfill must not count as live spend, rate=%v", rate)
	}

	// Subsequent live turns do.
	spoolLine(t, spoolPath, "live", 1_000_000, 1_000_000)
	a.DrainSpool()
	if rate := a.BurnRate(); rate <= 0 {
		t.Errorf("live spool spend should drive the burn rate, got %v", rate)
	}
}

// TestSpoolRemovalNetsOutSpend: a retry/undo must reduce the total.
func TestSpoolRemovalNetsOutSpend(t *testing.T) {
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
	spoolPath := filepath.Join(dir, "events.jsonl")

	spoolLine(t, spoolPath, "m1", 1_000_000, 1_000_000)
	a.DrainSpool()
	withMsg, _ := a.Ledger.GetTotals("TOTAL")

	f, err := os.OpenFile(spoolPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	f.WriteString(`{"type":"message.removed","id":"m1"}` + "\n")
	f.Close()

	a.DrainSpool()
	afterRemoval, _ := a.Ledger.GetTotals("TOTAL")
	if afterRemoval >= withMsg {
		t.Errorf("removal should net out spend: %v -> %v", withMsg, afterRemoval)
	}
}

// TestEnableClearsPriorSpend documents the session-limit contract: ticking the
// box starts the count at zero, so a limit never retroactively blocks work
// already done, and unticking clears the tally entirely.
func TestEnableClearsPriorSpend(t *testing.T) {
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

	// Enforcement must start off: a limit is a deliberate act.
	if a.Budget.Enabled() {
		t.Error("session limit should be off on a fresh start")
	}

	spend(a, "m1", "s1", "expensive", 1_000_000, 1_000_000) // $30, before enabling

	a.Budget.Cfg.SessionLimitUSD = 5
	a.SetEnabled(true)
	if sb := a.ActiveSessionBudget(); sb.State != "ok" {
		t.Errorf("enabling must start from zero, got state=%q cost=%v", sb.State, sb.Cost)
	}

	// New spend counts against the cap.
	spend(a, "m2", "s1", "expensive", 1_000_000, 1_000_000)
	if sb := a.ActiveSessionBudget(); sb.State != "over" {
		t.Errorf("spend after enabling should count, got %q", sb.State)
	}

	// Unticking clears the tally, so re-ticking is a clean slate.
	a.SetEnabled(false)
	a.SetEnabled(true)
	if sb := a.ActiveSessionBudget(); sb.State != "ok" {
		t.Errorf("re-enabling should start clean, got state=%q cost=%v", sb.State, sb.Cost)
	}
}

var _ = opencode.Message{}
