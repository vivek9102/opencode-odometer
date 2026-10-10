// Package plugin implements the on-disk contract between the Odometer and
// the bundled OpenCode plugin (plugin/odometer.js).
//
// The Odometer owns budget.json (a single writer) and writes a precomputed
// verdict per session. The plugin only reads that verdict and refuses to
// start a new turn when a session is over; it never prices anything. Keeping
// the decision on the Odometer side means pricing logic lives in exactly one
// place and the plugin stays tiny.
//
// Grace turns are the one thing the plugin must record. Because budget.json
// has a single writer (us), the plugin appends claims to a separate
// grace_claims.json which we fold into state on our next poll.
package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Session is one session's published verdict.
type Session struct {
	Strict          bool    `json:"strict,omitempty"`
	ManualStop      bool    `json:"manual_stop,omitempty"`
	CancellationID  string  `json:"cancellation_id,omitempty"`
	Stage           string  `json:"stage,omitempty"`
	Generation      string  `json:"generation,omitempty"`
	OriginalModel   string  `json:"original_model,omitempty"`
	FallbackModel   string  `json:"fallback_model,omitempty"`
	BudgetSessionID string  `json:"budget_session_id,omitempty"`
	Cost            float64 `json:"cost"`
	State           string  `json:"state"`
	Fraction        float64 `json:"fraction"`
	Limit           float64 `json:"limit"`
	Mode            string  `json:"mode"`
	GraceRemaining  int     `json:"grace_remaining"`
	HardStopAt      float64 `json:"hard_stop_at"`
	PastHardStop    bool    `json:"past_hard_stop"`
	Enforced        bool    `json:"enforced"`
}

// BudgetFile is the full document written to budget.json.
type BudgetFile struct {
	Accounted map[string]string `json:"accounted,omitempty"`
	// FreeModels comes from the same price book as accounting and the picker.
	// Missing/unknown prices are never permission to bypass a spending cap.
	FreeModels        []string           `json:"free_models,omitempty"`
	Enabled           bool               `json:"enabled"`
	SessionLimitUSD   float64            `json:"session_limit_usd"`
	WarnAtPercent     int                `json:"warn_at_percent"`
	BlockWhenExceeded bool               `json:"block_when_exceeded"`
	Mode              string             `json:"mode"`
	HardStopAt        float64            `json:"hard_stop_at"`
	GraceTurns        int                `json:"grace_turns"`
	Sessions          map[string]Session `json:"sessions"`
	Updated           int64              `json:"updated"`
}

// Contract writes and reads the budget ledger shared with the plugin.
// The file has a single writer (the Odometer), so writes are atomic.
type Contract struct {
	BudgetFile string
	GraceFile  string
	// CommandFile is how the odometer asks the plugin to act on its behalf.
	// The plugin holds the OpenCode client, so operations such as aborting a
	// session work even when no HTTP port is listening.
	CommandFile string
}

// New returns a Contract pointing at the given data files.
func New(budgetFile, graceFile string) *Contract {
	c := &Contract{BudgetFile: budgetFile, GraceFile: graceFile}
	if budgetFile != "" {
		c.CommandFile = filepath.Join(filepath.Dir(budgetFile), "commands.json")
	}
	return c
}

// Command is a request for the plugin to perform an action inside OpenCode.
type Command struct {
	Action    string `json:"action"`
	SessionID string `json:"sessionID"`
	Issued    int64  `json:"issued"`
}

// RequestAbort asks the plugin to cancel a session. Returns an error if the
// request could not be queued.
func (c *Contract) RequestAbort(sessionID string) error {
	if c.CommandFile == "" {
		return fmt.Errorf("no command file configured")
	}
	cmds := []Command{{
		Action:    "abort",
		SessionID: sessionID,
		Issued:    time.Now().Unix(),
	}}
	raw, err := json.Marshal(cmds)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", c.CommandFile, os.Getpid())
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.CommandFile)
}

// WriteBudget persists the budget document atomically so the plugin never
// reads a half-written file.
func (c *Contract) WriteBudget(b BudgetFile) error {
	b.Updated = time.Now().Unix()
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", c.BudgetFile, os.Getpid())
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return os.WriteFile(c.BudgetFile, append(raw, '\n'), 0o644)
	}
	if err := os.Rename(tmp, c.BudgetFile); err != nil {
		_ = os.Remove(c.BudgetFile)
		if err2 := os.Rename(tmp, c.BudgetFile); err2 != nil {
			_ = os.Remove(tmp)
			return os.WriteFile(c.BudgetFile, append(raw, '\n'), 0o644)
		}
	}
	return nil
}

// AbsorbGraceClaims reads any grace claims the plugin recorded, folding them
// into the `used` map (session id -> count used), then clears the side file.
// It reports whether any claims were absorbed.
func (c *Contract) AbsorbGraceClaims(used map[string]int) (bool, error) {
	if !fileExists(c.GraceFile) {
		return false, nil
	}
	raw, err := os.ReadFile(c.GraceFile)
	if err != nil {
		return false, err
	}
	var claims map[string]int
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &claims); err != nil {
			// Corrupt/transient write: ignore and clear.
			_ = os.Remove(c.GraceFile)
			return false, nil
		}
	}
	if len(claims) == 0 {
		_ = os.Remove(c.GraceFile)
		return false, nil
	}
	for sid, n := range claims {
		used[sid] += n
	}
	_ = os.Remove(c.GraceFile)
	return true, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
