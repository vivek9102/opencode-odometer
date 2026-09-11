package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteBudgetIsValidJSON(t *testing.T) {
	dir := t.TempDir()
	c := New(filepath.Join(dir, "budget.json"), filepath.Join(dir, "grace.json"))
	b := BudgetFile{
		Enabled: true, SessionLimitUSD: 10.0, WarnAtPercent: 80,
		BlockWhenExceeded: true, Mode: "soft", HardStopAt: 1.5, GraceTurns: 1,
		Sessions: map[string]Session{
			"s1": {Cost: 12.0, State: "over", Fraction: 1.2, Limit: 10.0,
				Mode: "soft", GraceRemaining: 0, HardStopAt: 1.5,
				PastHardStop: false, Enforced: true},
		},
	}
	if err := c.WriteBudget(b); err != nil {
		t.Fatalf("WriteBudget: %v", err)
	}
	raw, err := os.ReadFile(c.BudgetFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		Enabled  bool               `json:"enabled"`
		Mode     string             `json:"mode"`
		Sessions map[string]Session `json:"sessions"`
		Updated  int64              `json:"updated"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if !doc.Enabled || doc.Mode != "soft" {
		t.Errorf("bad doc: %+v", doc)
	}
	if _, ok := doc.Sessions["s1"]; !ok {
		t.Error("session s1 missing")
	}
	if doc.Updated == 0 {
		t.Error("updated timestamp missing")
	}
	// no .tmp leftover
	if fileExists(c.BudgetFile + ".tmp") {
		t.Error("temp file left behind")
	}
}

func TestAbsorbGraceClaims(t *testing.T) {
	dir := t.TempDir()
	c := New(filepath.Join(dir, "budget.json"), filepath.Join(dir, "grace.json"))

	claims := map[string]int{"s1": 2, "s2": 3}
	raw, _ := json.Marshal(claims)
	if err := os.WriteFile(c.GraceFile, raw, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	used := map[string]int{"s1": 1}
	ok, err := c.AbsorbGraceClaims(used)
	if err != nil {
		t.Fatalf("Absorb: %v", err)
	}
	if !ok {
		t.Error("expected claims absorbed")
	}
	if used["s1"] != 3 || used["s2"] != 3 {
		t.Errorf("used = %v", used)
	}
	if fileExists(c.GraceFile) {
		t.Error("grace file not cleared after absorb")
	}
}

func TestAbsorbGraceClaimsNoFile(t *testing.T) {
	dir := t.TempDir()
	c := New(filepath.Join(dir, "b.json"), filepath.Join(dir, "g.json"))
	ok, err := c.AbsorbGraceClaims(map[string]int{})
	if err != nil || ok {
		t.Errorf("no file: ok=%v err=%v", ok, err)
	}
}

func TestAbsorbGraceClaimsClearsCorrupt(t *testing.T) {
	dir := t.TempDir()
	c := New(filepath.Join(dir, "b.json"), filepath.Join(dir, "g.json"))
	if err := os.WriteFile(c.GraceFile, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ok, err := c.AbsorbGraceClaims(map[string]int{})
	if err != nil {
		t.Fatalf("Absorb: %v", err)
	}
	if ok {
		t.Error("corrupt claims should not absorb")
	}
	if fileExists(c.GraceFile) {
		t.Error("corrupt grace file should be cleared")
	}
}

func TestWriteBudgetOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	c := New(filepath.Join(dir, "budget.json"), filepath.Join(dir, "g.json"))
	for i := 0; i < 5; i++ {
		b := BudgetFile{Enabled: true, Sessions: map[string]Session{
			"x": {Cost: float64(i)},
		}}
		if err := c.WriteBudget(b); err != nil {
			t.Fatal(err)
		}
	}
	// final content reflects the last write with no temp residue
	raw, _ := os.ReadFile(c.BudgetFile)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	sessions := doc["sessions"].(map[string]any)
	x := sessions["x"].(map[string]any)
	if x["cost"] != float64(4) {
		t.Errorf("expected last-write cost 4, got %v", x["cost"])
	}
	if fileExists(c.BudgetFile + ".tmp") {
		t.Error("temp file left behind")
	}
}
