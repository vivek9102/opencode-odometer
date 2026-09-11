package budget

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func newEnabledBudget(t *testing.T, limit float64, mode Mode) *Budget {
	t.Helper()
	b := New()
	b.Cfg.Enabled = true
	b.Cfg.SessionLimitUSD = limit
	b.Cfg.WarnAtPercent = 80
	b.Cfg.Mode = mode
	b.Cfg.HardStopAt = 1.5
	b.Cfg.GraceTurns = 1
	return b
}

func almostEq(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestStates(t *testing.T) {
	b := newEnabledBudget(t, 1.0, ModeSoft)
	if s, _ := b.Status(0.10); s != StateOK {
		t.Errorf("0.10 -> %v, want ok", s)
	}
	if s, _ := b.Status(0.85); s != StateWarn {
		t.Errorf("0.85 -> %v, want warn", s)
	}
	if s, _ := b.Status(1.20); s != StateOver {
		t.Errorf("1.20 -> %v, want over", s)
	}
}

func TestStatusMeasuresEvenWhenDisabled(t *testing.T) {
	// status() is a measurement; Enabled governs enforcement only.
	b := New()
	b.Cfg.Enabled = false
	b.Cfg.SessionLimitUSD = 1.0
	b.Cfg.WarnAtPercent = 80
	state, frac := b.Status(9999.0)
	if state != StateOver {
		t.Errorf("expected over, got %v", state)
	}
	if frac <= 0 {
		t.Errorf("expected positive fraction, got %v", frac)
	}
	if b.Blocks() {
		t.Error("disabled budget must not block")
	}
}

func TestDisabledNeverBlocks(t *testing.T) {
	b := New()
	b.Cfg.Enabled = false
	if b.Blocks() {
		t.Error("disabled budget blocked")
	}
}

func TestZeroLimitNeverBlocks(t *testing.T) {
	b := newEnabledBudget(t, 0, ModeHard)
	if s, frac := b.Status(50.0); s != StateOK || frac != 0 {
		t.Errorf("got (%v,%v), want (ok,0)", s, frac)
	}
}

func TestAtomicSaveIsValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.json")
	b := newEnabledBudget(t, 1.0, ModeSoft)
	if err := b.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if doc["enabled"] != true {
		t.Error("enabled not persisted")
	}
	if _, ok := doc["updated"]; !ok {
		t.Error("updated timestamp missing")
	}
}

func TestLoadMergesOverDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.json")
	b1 := New()
	b1.Cfg.Enabled = true
	b1.Cfg.SessionLimitUSD = 25.0
	b1.Cfg.Mode = ModeHard
	if err := b1.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b2 := New()
	b2.Load(path)
	if !b2.Enabled() {
		t.Error("enabled not loaded")
	}
	if b2.Limit() != 25.0 {
		t.Errorf("limit = %v, want 25", b2.Limit())
	}
	if b2.Mode() != ModeHard {
		t.Errorf("mode = %v, want hard", b2.Mode())
	}
	// fields not in the file keep defaults
	if b2.Cfg.WarnAtPercent != DefaultWarnPct {
		t.Errorf("warn pct lost default: %v", b2.Cfg.WarnAtPercent)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	b := New()
	b.Load(filepath.Join(t.TempDir(), "nope.json"))
	if b.Enabled() {
		t.Error("expected disabled default")
	}
	if b.Mode() != ModeSoft {
		t.Error("expected soft default")
	}
}

func TestModeDefaultsToSoft(t *testing.T) {
	b := New()
	if b.Mode() != ModeSoft {
		t.Errorf("got %v, want soft", b.Mode())
	}
}

func TestInvalidModeFallsBackToSoft(t *testing.T) {
	b := New()
	b.Cfg.Mode = "banana"
	if b.Mode() != ModeSoft {
		t.Errorf("got %v, want soft", b.Mode())
	}
}

func TestWarnModeNeverBlocks(t *testing.T) {
	b := newEnabledBudget(t, 10.0, ModeWarn)
	if b.Blocks() {
		t.Error("warn must never block")
	}
	if s, _ := b.Status(20.0); s != StateOver {
		t.Errorf("expected over, got %v", s) // still measures
	}
}

func TestMeasurementIndependentOfEnforcement(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		for _, mode := range []Mode{ModeWarn, ModeSoft, ModeHard} {
			b := New()
			b.Cfg.Enabled = enabled
			b.Cfg.SessionLimitUSD = 10.0
			b.Cfg.WarnAtPercent = 80
			b.Cfg.Mode = mode
			state, frac := b.Status(15.0) // 1.5x of a $10 limit
			if state != StateOver {
				t.Errorf("enabled=%v mode=%v: got %v, want over", enabled, mode, state)
			}
			almostEq(t, frac, 1.5)
		}
	}
}

func TestHardAndSoftBlock(t *testing.T) {
	for _, mode := range []Mode{ModeSoft, ModeHard} {
		b := newEnabledBudget(t, 1.0, mode)
		if !b.Blocks() {
			t.Errorf("%v should block", mode)
		}
	}
}

func TestGraceOnlyInSoftMode(t *testing.T) {
	b := newEnabledBudget(t, 10.0, ModeSoft)
	if b.GraceTurns() != 1 {
		t.Errorf("soft grace = %v, want 1", b.GraceTurns())
	}
	b.Cfg.Mode = ModeHard
	if b.GraceTurns() != 0 {
		t.Error("hard must have 0 grace")
	}
	b.Cfg.Mode = ModeWarn
	if b.GraceTurns() != 0 {
		t.Error("warn must have 0 grace")
	}
}

func TestDisabledNeverBlocksRegardlessOfMode(t *testing.T) {
	b := New()
	b.Cfg.Enabled = false
	b.Cfg.SessionLimitUSD = 10.0
	b.Cfg.Mode = ModeHard
	if b.Blocks() {
		t.Error("disabled hard budget blocked")
	}
}

func TestBlockWhenExceededFalseDisablesBlocking(t *testing.T) {
	b := newEnabledBudget(t, 1.0, ModeHard)
	b.Cfg.BlockWhenExceeded = false
	if b.Blocks() {
		t.Error("block_when_exceeded=false still blocked")
	}
}

func TestHardStopMultiplierSane(t *testing.T) {
	b := newEnabledBudget(t, 1.0, ModeSoft)
	b.Cfg.HardStopAt = 0.5 // below 1x makes no sense
	if b.HardStopAt() < 1.0 {
		t.Errorf("hard stop clamped: %v", b.HardStopAt())
	}
	b.Cfg.HardStopAt = 1.5
	if b.HardStopAt() != 1.5 {
		t.Errorf("hard stop = %v, want 1.5", b.HardStopAt())
	}
}

// Grace / baseline helpers are exercised via the Session helper, mirroring
// the original's publish-time computation.
