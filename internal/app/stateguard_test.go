package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vivek9102/opencode-odometer/internal/app"
)

func newAppAt(t *testing.T, dir, stateFile string) (*app.App, error) {
	t.Helper()
	return app.New(app.Config{
		PricesFile: priceFixture(t, dir),
		StateFile:  stateFile,
		BudgetFile: filepath.Join(dir, "budget.json"),
		GraceFile:  filepath.Join(dir, "grace.json"),
	})
}

// TestCorruptStateIsNotOverwritten is a regression test for real data loss.
//
// A state file that cannot be parsed used to be treated as "no state": the app
// started with an empty ledger and the next save replaced months of spend
// history with whatever the current run had collected. A 6906-message ledger
// became 20 that way. An unreadable file must be preserved, not silently
// replaced.
func TestCorruptStateIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")

	// Truncated JSON, as produced by a half-written or mangled file.
	const corrupt = `{"ledger":{"messages":{"m1":{"cost":1.5`
	if err := os.WriteFile(stateFile, []byte(corrupt), 0o644); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	a, err := newAppAt(t, dir, stateFile)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	// Normal operation must not clobber the file.
	spend(a, "new1", "s1", "expensive", 1000, 1000)
	a.SaveState()

	got, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != corrupt {
		t.Errorf("corrupt state was overwritten:\n got: %s\nwant: %s", got, corrupt)
	}

	// The salvaged copy makes manual recovery possible.
	if _, err := os.Stat(stateFile + ".unreadable"); err != nil {
		t.Errorf("expected a .unreadable copy to be kept: %v", err)
	}
}

// TestValidStateStillSaves guards against the fix being too aggressive: a
// readable file must continue to persist normally.
func TestValidStateStillSaves(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")

	a, err := newAppAt(t, dir, stateFile)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	spend(a, "m1", "s1", "expensive", 1_000_000, 1_000_000)
	a.SaveState()

	raw, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("state not written: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("state file is empty")
	}

	// Reload must recover the ledger rather than start blank.
	b, err := newAppAt(t, dir, stateFile)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if n := len(b.Ledger.GetMessagesSnapshot()); n != 1 {
		t.Errorf("reloaded %d messages, want 1", n)
	}
	total, _ := b.Ledger.GetTotals("TOTAL")
	if total <= 0 {
		t.Errorf("reloaded total = %v, want > 0", total)
	}
}

// TestMissingStateStartsFresh: a genuinely absent file is a first run, which
// must still work.
func TestMissingStateStartsFresh(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")

	a, err := newAppAt(t, dir, stateFile)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	spend(a, "m1", "s1", "expensive", 1000, 1000)
	a.SaveState()

	if _, err := os.Stat(stateFile); err != nil {
		t.Errorf("first run should create state: %v", err)
	}
}

// TestLargeShrinkKeepsBackup: a save that collapses the file keeps the old one,
// so even an unforeseen bug leaves a recovery path.
func TestLargeShrinkKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")

	a, err := newAppAt(t, dir, stateFile)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	for i := 0; i < 60; i++ {
		spend(a, "m"+string(rune('A'+i%26))+string(rune('a'+i/26)),
			"s1", "expensive", 1_000_000, 1_000_000)
	}
	a.SaveState()
	big, err := os.Stat(stateFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// Simulate a collapse: drop everything, then save.
	for _, rec := range a.Ledger.GetMessagesSnapshot() {
		a.Ledger.Remove(rec.MID)
	}
	a.SaveState()

	small, err := os.Stat(stateFile)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if small.Size() >= big.Size()/2 {
		t.Skip("file did not shrink enough to trigger the safeguard")
	}
	if _, err := os.Stat(stateFile + ".prev"); err != nil {
		t.Errorf("expected .prev backup after a large shrink: %v", err)
	}
}
