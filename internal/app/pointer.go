package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// PointerFilePath returns the location of the discovery file read by the plugin (~/.opencode-odometer.json).
//
// OPENCODE_ODOMETER_POINTER overrides it. Tests must set that: the pointer
// lives at a fixed path in the user's home, so a test that writes the real one
// redirects the running plugin at a temp directory and silently stops the
// live odometer from receiving any spend.
func PointerFilePath() string {
	if p := os.Getenv("OPENCODE_ODOMETER_POINTER"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".opencode-odometer.json")
}

// PointerPayload is the structure written to ~/.opencode-odometer.json.
type PointerPayload struct {
	Comment    string `json:"_comment"`
	DataDir    string `json:"data_dir"`
	Budget     string `json:"budget"`
	Grace      string `json:"grace"`
	AppDir     string `json:"app_dir"`
	Executable string `json:"executable,omitempty"`
	PID        int    `json:"pid"`
	Updated    int64  `json:"updated"`
}

// WritePointer writes ~/.opencode-odometer.json so the OpenCode plugin
// can discover where budget.json and grace_claims.json live.
func WritePointer(dataDir, budgetFile, graceFile, appDir, executable string) error {
	p := PointerFilePath()
	payload := PointerPayload{
		Comment:    "Written by OpenCode Odometer so the plugin can locate budget.json. Safe to delete.",
		DataDir:    dataDir,
		Budget:     budgetFile,
		Grace:      graceFile,
		AppDir:     appDir,
		Executable: executable,
		PID:        os.Getpid(),
		Updated:    time.Now().Unix(),
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", p, os.Getpid())
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
