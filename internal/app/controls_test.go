package app

import (
	"fmt"
	"os"
	"testing"
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

func TestReloadPricesReprices(tt *testing.T) {
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
	after := a.Ledger.TotalCost
	if after <= before {
		tt.Fatalf("reload should reprice at new output rate: before %v, after %v", before, after)
	}
}
