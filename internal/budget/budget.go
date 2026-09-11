// Package budget implements the per-session spend cap that the OpenCode
// plugin enforces.
//
// Enforcement is tiered rather than binary, because "slightly over on a task
// I am about to finish" and "an agent loop is burning money unattended" are
// different problems:
//
//	warn  - never blocks; toast only (pure visibility)
//	soft  - blocks at 100%, but grants ONE grace turn first, then
//	        hard-stops at hardStopAt x limit   [default]
//	hard  - blocks at 100% immediately, no grace
//
// A key invariant: status() is a pure MEASUREMENT independent of Enabled.
// Enforcement is a separate question (Blocks()). Callers that act on the
// state must gate on Blocks() (refusing turns) or Enabled (colouring the UI).
package budget

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Mode identifies the enforcement policy.
type Mode string

// Enforcement modes.
const (
	ModeWarn Mode = "warn"
	ModeSoft Mode = "soft"
	ModeHard Mode = "hard"
)

// State is the measurement for a session's spend.
type State string

// Session states.
const (
	StateOK   State = "ok"
	StateWarn State = "warn"
	StateOver State = "over"
)

// Defaults for the budget configuration.
const (
	DefaultLimit      = 5.0
	DefaultWarnPct    = 80
	DefaultMode       = ModeSoft
	DefaultHardStopAt = 1.5
	DefaultGraceTurns = 1
)

// Config holds the tunable budget settings.
type Config struct {
	Enabled           bool    `json:"enabled"`
	SessionLimitUSD   float64 `json:"session_limit_usd"`
	WarnAtPercent     int     `json:"warn_at_percent"`
	BlockWhenExceeded bool    `json:"block_when_exceeded"`
	Mode              Mode    `json:"mode"`
	HardStopAt        float64 `json:"hard_stop_at"`
	GraceTurns        int     `json:"grace_turns"`
}

// DefaultConfig returns the shipped defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:           false,
		SessionLimitUSD:   DefaultLimit,
		WarnAtPercent:     DefaultWarnPct,
		BlockWhenExceeded: true,
		Mode:              DefaultMode,
		HardStopAt:        DefaultHardStopAt,
		GraceTurns:        DefaultGraceTurns,
	}
}

// Budget is a per-session spend limit, shared with the OpenCode plugin via
// budget.json. It owns persistence of its config only; the live session
// ledger is written by the caller through a separate contract.
type Budget struct {
	mu  sync.RWMutex
	Cfg Config
}

// New returns a Budget with defaults.
func New() *Budget {
	return &Budget{Cfg: DefaultConfig()}
}

// Load reads config from a budget.json file, merging over defaults. Missing
// or corrupt files leave defaults in place.
func (b *Budget) Load(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	// Unmarshal into a map so we can tell which keys are actually present.
	var disk map[string]any
	if err := json.Unmarshal(raw, &disk); err != nil {
		return
	}
	b.Cfg = DefaultConfig()
	if v, ok := disk["enabled"].(bool); ok {
		b.Cfg.Enabled = v
	}
	if v, ok := disk["session_limit_usd"].(float64); ok {
		b.Cfg.SessionLimitUSD = v
	}
	if v, ok := disk["warn_at_percent"].(float64); ok {
		b.Cfg.WarnAtPercent = int(v)
	}
	if v, ok := disk["block_when_exceeded"].(bool); ok {
		b.Cfg.BlockWhenExceeded = v
	}
	if v, ok := disk["mode"].(string); ok {
		b.Cfg.Mode = sanitizeMode(Mode(v))
	}
	if v, ok := disk["hard_stop_at"].(float64); ok {
		b.Cfg.HardStopAt = v
	}
	if v, ok := disk["grace_turns"].(float64); ok {
		b.Cfg.GraceTurns = int(v)
	}
}

// Save persists the config as JSON (the session ledger is written by the
// caller via the plugin contract, keeping a single writer on that file).
func (b *Budget) Save(path string) error {
	b.mu.RLock()
	doc := map[string]any{
		"_comment":            "Budget configuration for OpenCode Odometer.",
		"enabled":             b.Cfg.Enabled,
		"session_limit_usd":   b.Cfg.SessionLimitUSD,
		"warn_at_percent":     b.Cfg.WarnAtPercent,
		"block_when_exceeded": b.Cfg.BlockWhenExceeded,
		"mode":                string(b.Cfg.Mode),
		"hard_stop_at":        b.Cfg.HardStopAt,
		"grace_turns":         b.Cfg.GraceTurns,
		"updated":             time.Now().Unix(),
	}
	b.mu.RUnlock()
	tmp := path + ".tmp"
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic: reader never sees a half file
}

// sanitizeMode coerces any mode string into one of the valid modes.
func sanitizeMode(m Mode) Mode {
	switch m {
	case ModeWarn, ModeSoft, ModeHard:
		return m
	default:
		return ModeSoft
	}
}

// Mode returns the sanitized enforcement mode.
func (b *Budget) Mode() Mode {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return sanitizeMode(b.Cfg.Mode)
}

// Enabled reports whether the limit switch is on.
func (b *Budget) Enabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Cfg.Enabled
}

// Limit returns the session cap in USD.
func (b *Budget) Limit() float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Cfg.SessionLimitUSD
}

// HardStopAt returns the multiple of the limit at which even `soft` refuses
// to grant grace. Always at least 1.0.
func (b *Budget) HardStopAt() float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.Cfg.HardStopAt < 1.0 {
		return 1.0
	}
	return b.Cfg.HardStopAt
}

// GraceTurns returns how many one-shot grace turns a session gets on first
// breach. Only `soft` mode ever grants grace.
func (b *Budget) GraceTurns() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if sanitizeMode(b.Cfg.Mode) != ModeSoft {
		return 0
	}
	if b.Cfg.GraceTurns < 0 {
		return 0
	}
	return b.Cfg.GraceTurns
}

// Blocks reports whether this configuration ever refuses a turn.
func (b *Budget) Blocks() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Cfg.Enabled && sanitizeMode(b.Cfg.Mode) != ModeWarn && b.Cfg.BlockWhenExceeded
}

// Status is a pure measurement of a session's spend against the limit.
// It returns the state and the fraction of the limit spent.
func (b *Budget) Status(cost float64) (State, float64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.Cfg.SessionLimitUSD <= 0 {
		return StateOK, 0.0
	}
	frac := cost / b.Cfg.SessionLimitUSD
	if frac >= 1.0 {
		return StateOver, frac
	}
	if frac >= float64(b.Cfg.WarnAtPercent)/100.0 {
		return StateWarn, frac
	}
	return StateOK, frac
}

// String returns a human-readable mode label.
func (m Mode) String() string { return string(m) }
