package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/opencode"
)

type StorageSummary struct {
	LedgerBytes        int64 `json:"ledger_bytes"`
	TelemetryBytes     int64 `json:"telemetry_bytes"`
	ProtocolBytes      int64 `json:"protocol_bytes"`
	ExportBytes        int64 `json:"export_bytes"`
	OtherBytes         int64 `json:"other_bytes"`
	RetainedMessages   int   `json:"retained_messages"`
	ArchivedDays       int   `json:"archived_days"`
	ArchivedMessageIDs int   `json:"archived_message_ids"`
}

func (a *App) StorageUsage() StorageSummary {
	var s StorageSummary
	dir := a.experienceDir()
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.IsDir() || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, filepath.Base(a.cfg.StateFile)):
			s.LedgerBytes += info.Size()
		case strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.1") || name == "events.jsonl":
			s.TelemetryBytes += info.Size()
		case protocolFile(name):
			s.ProtocolBytes += info.Size()
		default:
			s.OtherBytes += info.Size()
		}
	}
	exports, _ := os.ReadDir(filepath.Join(dir, "exports"))
	for _, entry := range exports {
		if !entry.Type().IsRegular() {
			continue
		}
		if info, err := entry.Info(); err == nil {
			s.ExportBytes += info.Size()
		}
	}
	cp := a.Ledger.Copy()
	s.RetainedMessages = len(cp.Messages)
	s.ArchivedMessageIDs = len(cp.PrunedIDs)
	days := map[string]bool{}
	for _, b := range cp.Archived {
		days[b.Date] = true
	}
	s.ArchivedDays = len(days)
	return s
}

func protocolFile(name string) bool {
	for _, prefix := range []string{"models-", "tui-", "switch-", "continue-", "abort-", "stop-tui-", "fallback-error-"} {
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".json") {
			return true
		}
	}
	return false
}

// CleanupProtocolFiles only removes old terminal acknowledgements and closed
// presence/inventory files. Budgets, ledgers, choices, commands, exports and
// unread telemetry are never eligible. Unknown/malformed data is retained.
func (a *App) CleanupProtocolFiles(now time.Time) int {
	dir, err := filepath.Abs(a.experienceDir())
	if err != nil {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	protected := map[string]bool{}
	a.mu.RLock()
	for _, p := range a.state.OpenPolicies {
		protected[p.TransitionID] = true
		protected[p.CancellationID] = true
	}
	for id := range a.openTUIs {
		protected[id] = true
	}
	a.mu.RUnlock()
	// Every outstanding command and durable model choice protects its own
	// acknowledgement and owner, regardless of age or process availability.
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !protocolFile(name) || strings.Contains(name, "-status-") {
			continue
		}
		if strings.HasPrefix(name, "models-") || strings.HasPrefix(name, "tui-") {
			continue
		}
		var ref struct {
			ID       string `json:"id"`
			Instance string `json:"instance"`
		}
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		if json.Unmarshal(raw, &ref) == nil {
			if ref.ID != "" {
				protected[ref.ID] = true
			}
			if ref.Instance != "" {
				protected[ref.Instance] = true
			}
		}
	}
	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !protocolFile(name) {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < 7*24*time.Hour {
			continue
		}
		path := filepath.Join(dir, name)
		if filepath.Dir(path) != dir {
			continue
		}
		var row struct {
			ID       string          `json:"id"`
			Instance string          `json:"instance"`
			PID      int             `json:"pid"`
			Status   string          `json:"status"`
			Closed   bool            `json:"closed"`
			Updated  int64           `json:"updated"`
			Result   json.RawMessage `json:"result"`
			Error    string          `json:"error"`
		}
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, &row) != nil || row.ID != "" && protected[row.ID] || row.Instance != "" && protected[row.Instance] {
			continue
		}
		eligible := false
		switch {
		case strings.HasPrefix(name, "models-"):
			eligible = row.PID > 0 && row.Updated > 0 && now.Unix()-row.Updated > 7*86400 && !opencode.ProcessAlive(row.PID)
		case strings.HasPrefix(name, "tui-"):
			eligible = row.Updated > 0 && now.Unix()-row.Updated > 7*86400 && (row.Closed || row.PID > 0 && !opencode.ProcessAlive(row.PID))
		case strings.HasPrefix(name, "switch-status-") || strings.HasPrefix(name, "abort-status-") || strings.HasPrefix(name, "continue-status-"):
			eligible = row.ID != "" && (row.Status == "confirmed" || row.Status == "completed" || row.Status == "cancelled" || row.Status == "failed" || row.Status == "expired" || row.Status == "stopped")
		case strings.HasPrefix(name, "continue-tui-status-"):
			eligible = row.ID != "" && (len(row.Result) > 0 || row.Error != "")
		}
		if eligible {
			if os.Remove(path) == nil {
				removed++
			}
		}
	}
	return removed
}

func (a *App) maybeCleanupProtocol() {
	a.cleanupMu.Lock()
	defer a.cleanupMu.Unlock()
	now := time.Now()
	if now.Sub(a.cleanupAt) < time.Hour {
		return
	}
	a.cleanupAt = now
	if n := a.CleanupProtocolFiles(now); n > 0 {
		a.LogEvent("removed %d old terminal protocol files", n)
	}
}
