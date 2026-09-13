package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vivek9102/opencode-odometer/internal/ledger"
	"github.com/vivek9102/opencode-odometer/internal/prices"
)

func TestViewModeDefaultsToTrip(t *testing.T) {
	a, _, _ := newTestApp(t)
	if got := a.ViewMode(); got != "TRIP" {
		t.Fatalf("ViewMode = %q, want TRIP", got)
	}
	a.SetViewMode("TOTAL")
	if got := a.ViewMode(); got != "TOTAL" {
		t.Fatalf("ViewMode = %q, want TOTAL", got)
	}
}

func TestSetLimit(tt *testing.T) {
	a, _, _ := newTestApp(tt)
	a.SetLimit(2.5)
	if got := a.Budget.Limit(); got != 2.5 {
		tt.Fatalf("Limit = %v, want 2.5", got)
	}
	a.SetLimit(-1)
	if got := a.Budget.Limit(); got != 0 {
		tt.Fatalf("Limit after negative = %v, want 0", got)
	}
}

func TestSetMode(tt *testing.T) {
	a, _, _ := newTestApp(tt)
	a.SetMode("hard")
	if got := a.Budget.Mode().String(); got != "hard" {
		tt.Fatalf("Mode = %q, want hard", got)
	}
	// An unknown raw mode is stored but sanitized (warn/soft/hard) on read.
	a.SetMode("bogus")
	if got := a.Budget.Mode().String(); got != "soft" {
		tt.Fatalf("Mode() after bogus = %q, want soft (sanitized)", got)
	}
}

func TestSetEnabledRebases(tt *testing.T) {
	a, _, _ := newTestApp(tt)
	if a.Budget.Enabled() {
		tt.Fatal("budget should start disabled")
	}
	a.SetEnabled(true)
	if !a.Budget.Enabled() {
		tt.Fatal("budget should be enabled")
	}
}

func TestCycleDock(tt *testing.T) {
	a, _, _ := newTestApp(tt)
	first := a.CycleDock()
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		seen[a.Dock()] = true
		a.CycleDock()
	}
	if first == "" || len(seen) == 0 {
		tt.Fatal("dock cycle should produce a position")
	}
}

func TestReloadPricesKeepsHistoryAndPricesFutureMessages(tt *testing.T) {
	a, _, _ := newTestApp(tt)
	if !a.ApplyMessage(asst("m1", "s1", "acme", "sonnet", 0)) {
		tt.Fatal("expected new message to change ledger")
	}
	before := a.Ledger.TotalCost
	if before <= 0 {
		tt.Fatalf("expected nonzero cost from test price table, got %v", before)
	}

	// Overwrite the price file with a different output rate and reload.
	pf := a.cfg.PricesFile
	raw := fmt.Sprintf(`{
	  "reference_model": "acme/sonnet",
	  "models": {
	    "acme/sonnet": {"name":"Sonnet","input":3.0,"output":30.0,"cache_read":0.3,"cache_write":3.75}
	  }
	}`)
	if err := os.WriteFile(pf, []byte(raw), 0o644); err != nil {
		tt.Fatal(err)
	}
	if err := a.ReloadPrices(); err != nil {
		tt.Fatal(err)
	}
	afterReload := a.Ledger.TotalCost
	if afterReload != before {
		tt.Fatalf("reload rewrote history: before %v, after %v", before, afterReload)
	}

	if !a.ApplyMessage(asst("m2", "s1", "acme", "sonnet", 0)) {
		tt.Fatal("expected future message to change ledger")
	}
	newMessageCost := a.Ledger.Messages["m2"].Cost
	if newMessageCost <= before {
		tt.Fatalf("future message did not use new rates: old message %v, new message %v", before, newMessageCost)
	}
}

func TestOverrideKeepsHistoryReplayStableAndPricesFutureMessages(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.cfg.OverlayFile = filepath.Join(t.TempDir(), "prices.local.json")

	old := asst("old", "s1", "companyhub", "sonnet", 0)
	if !a.ApplyMessage(old) {
		t.Fatal("expected first message to enter ledger")
	}
	before := a.Ledger.TotalCost

	newRate := prices.Rate{
		Name: "CompanyHub Sonnet", Family: "", Input: 6, Output: 30,
		CacheRead: 0.6, CacheWrite: 7.5,
	}
	if err := a.SaveOverride("companyhub/sonnet", newRate); err != nil {
		t.Fatal(err)
	}
	if a.Ledger.TotalCost != before {
		t.Fatalf("saving a price rewrote history: before=%v after=%v", before, a.Ledger.TotalCost)
	}

	// Backfill replays completed messages; it must not apply today's price to
	// a message already recorded under yesterday's rate.
	if a.ApplyMessage(old) {
		t.Fatal("unchanged historical replay should not alter the ledger")
	}
	if a.Ledger.TotalCost != before {
		t.Fatalf("historical replay changed total: before=%v after=%v", before, a.Ledger.TotalCost)
	}

	if !a.ApplyMessage(asst("new", "s1", "companyhub", "sonnet", 0)) {
		t.Fatal("expected future message to enter ledger")
	}
	if got := a.Ledger.Messages["new"].Cost; got <= before {
		t.Fatalf("future message did not use updated rates: old=%v new=%v", before, got)
	}
}

func TestLocalFreeOverrideDoesNotReclassifyHistory(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.cfg.OverlayFile = filepath.Join(t.TempDir(), "prices.local.json")
	a.ApplyMessage(asst("old", "s1", "companyhub", "sonnet", 0))
	before := a.Ledger.Messages["old"]

	if err := a.SaveOverride("companyhub/sonnet", prices.Rate{Name: "Sonnet", Free: true}); err != nil {
		t.Fatal(err)
	}
	a.reclassifyFreeModels()
	if got := a.Ledger.Messages["old"]; got.Free || got.Cost != before.Cost {
		t.Fatalf("local free setting rewrote history: before=%+v after=%+v", before, got)
	}
}

func TestClearPriceOverridesKeepsHistoryAndRestoresFutureDefault(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.cfg.OverlayFile = filepath.Join(t.TempDir(), "prices.local.json")
	override := prices.Rate{Name: "Sonnet", Input: 30, Output: 150, CacheRead: 3, CacheWrite: 37.5}
	if err := a.SaveOverride("companyhub/sonnet", override); err != nil {
		t.Fatal(err)
	}
	a.ApplyMessage(asst("custom", "s1", "companyhub", "sonnet", 0))
	customCost := a.Ledger.TotalCost

	if err := a.ClearPriceOverrides(); err != nil {
		t.Fatal(err)
	}
	if a.Ledger.TotalCost != customCost {
		t.Fatalf("reset rewrote historical custom spend: before=%v after=%v", customCost, a.Ledger.TotalCost)
	}
	a.ApplyMessage(asst("default", "s1", "companyhub", "sonnet", 0))
	if got := a.Ledger.Messages["default"].Cost; got >= customCost {
		t.Fatalf("future message did not return to lower catalog estimate: default=%v custom=%v", got, customCost)
	}
}

func TestReclassifyFreeModelsDoesNotRepricePaidHistory(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.Ledger.Put(ledger.Record{
		MID: "free", Provider: "companyhub", Model: "deepseek-v4-flash-sovereign",
		TokensIn: 1_000_000, TokensOut: 1_000_000, Cost: 99,
	})
	a.Ledger.Put(ledger.Record{
		MID: "paid", Provider: "acme", Model: "sonnet",
		TokensIn: 1_000_000, TokensOut: 1_000_000, Cost: 7,
	})

	a.reclassifyFreeModels()

	free := a.Ledger.Messages["free"]
	if !free.Free || free.Cost != 0 || free.Saved <= 0 {
		t.Errorf("free record was not repaired: %+v", free)
	}
	paid := a.Ledger.Messages["paid"]
	if paid.Free || paid.Cost != 7 {
		t.Errorf("paid history was unexpectedly repriced: %+v", paid)
	}
}
