package app

import (
	"path/filepath"
	"testing"

	"github.com/vivek9102/opencode-odometer/internal/prices"
)

func TestPreviewPriceHierarchyAndNoLedgerMutation(t *testing.T) {
	a, _, _ := newTestApp(t)

	reported := a.PreviewPrice("acme/sonnet", 1_000_000, 1_000_000, 0, 0, 7.25)
	if reported.Cost != 7.25 || reported.Source != "OpenCode event" {
		t.Fatalf("reported event cost did not win over catalog: %+v", reported)
	}

	free := a.PreviewPrice("private/deepseek-sovereign", 1_000_000, 1_000_000, 0, 0, 99)
	if !free.Free || free.Cost != 0 {
		t.Fatalf("free marker did not win over reported cost: %+v", free)
	}

	overlay := filepath.Join(t.TempDir(), "prices.local.json")
	if err := a.Prices.SaveOverride(overlay, "acme/sonnet", prices.Rate{Input: 1, Output: 2}); err != nil {
		t.Fatal(err)
	}
	local := a.PreviewPrice("acme/sonnet", 1_000_000, 1_000_000, 0, 0, 99)
	if local.Cost != 3 || local.Source != "local override" {
		t.Fatalf("local override did not win over reported cost: %+v", local)
	}

	if len(a.Ledger.Messages) != 0 || a.Ledger.TotalCost != 0 {
		t.Fatalf("preview mutated real usage: messages=%d total=%v", len(a.Ledger.Messages), a.Ledger.TotalCost)
	}
}
