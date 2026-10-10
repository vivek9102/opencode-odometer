package app

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/ledger"
)

func (a *App) ExportCSV(dir string) (string, error) { return a.ExportCSVFiltered(dir, a.ViewMode()) }

// Rows and footer share a snapshot and scope. Archived values are summaries.
func (a *App) ExportCSVFiltered(dir, view string) (string, error) {
	if view != "RUN" && view != "TRIP" && view != "TOTAL" {
		return "", fmt.Errorf("unknown export scope")
	}
	cp := a.Ledger.Copy()
	rows := cp.GetMessagesSnapshot()
	if view == "TRIP" {
		rows = cp.TripRecords()
	}
	if view == "RUN" {
		rows = a.OpenSessionRecords(a.OpenSessions(), rows)
	}
	if view == "TOTAL" {
		var knownCost, knownSaved float64
		for _, b := range cp.Archived {
			knownCost += b.Cost
			knownSaved += b.Saved
			rows = append(rows, ledger.Record{MID: "ARCHIVED_DAY_SUMMARY", Timestamp: b.Date, Provider: b.Provider, Model: b.Model, Free: b.Free, Estimated: b.Estimated, Unknown: b.Unknown, CostSource: b.Source, Cost: b.Cost, Saved: b.Saved, TokensIn: b.Input, TokensOut: b.Output, Reasoning: b.Reasoning, CacheRead: b.CacheRead, CacheWrite: b.CacheWrite, Finish: fmt.Sprintf("%d archived messages", b.Messages)})
		}
		if cp.PrunedCost-knownCost > 1e-7 || cp.PrunedSaved-knownSaved > 1e-7 {
			rows = append(rows, ledger.Record{MID: "LEGACY_ARCHIVE_SUMMARY", Cost: cp.PrunedCost - knownCost, Saved: cp.PrunedSaved - knownSaved, Finish: "Date/model detail was pruned before daily archives were available"})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Timestamp != rows[j].Timestamp {
			return rows[i].Timestamp < rows[j].Timestamp
		}
		return rows[i].MID < rows[j].MID
	})
	header := []string{"timestamp", "message_id", "session_id", "provider", "model", "free", "tokens_in", "tokens_out", "reasoning", "cache_read", "cache_write", "cost_usd", "saved_usd", "finish", "row_kind", "cost_source", "estimated", "unknown_price"}
	data := [][]string{header}
	var cost, saved float64
	for _, r := range rows {
		kind := "message"
		if strings.HasSuffix(r.MID, "SUMMARY") || r.MID == "COUNTER_ADJUSTMENT" {
			kind = "summary"
		}
		if view == "TRIP" && kind == "message" {
			kind = "message_delta_since_reset"
		}
		free := "no"
		if r.Free {
			free = "yes"
		}
		data = append(data, []string{csvText(r.Timestamp), csvText(r.MID), csvText(r.SessionID), csvText(r.Provider), csvText(r.Model), free, fmt.Sprint(r.TokensIn), fmt.Sprint(r.TokensOut), fmt.Sprint(r.Reasoning), fmt.Sprint(r.CacheRead), fmt.Sprint(r.CacheWrite), money(r.Cost), money(r.Saved), csvText(r.Finish), kind, csvText(r.CostSource), fmt.Sprint(r.Estimated), fmt.Sprint(r.Unknown)})
		cost += r.Cost
		saved += r.Saved
	}
	footer := make([]string, len(header))
	footer[0] = "VIEW_TOTAL"
	footer[1] = view
	footer[11] = money(cost)
	footer[12] = money(saved)
	footer[14] = "total"
	data = append(data, footer)
	return writeCSV(dir, "opencode_cost_"+strings.ToLower(view), data)
}

func money(v float64) string { return fmt.Sprintf("%.12f", v) }
func csvText(v string) string {
	if v != "" && strings.ContainsAny(v[:1], "=+-@") {
		return "'" + v
	}
	return v
}
func writeCSV(dir, prefix string, rows [][]string) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, prefix+"_"+time.Now().Format("2006-01-02_150405")+"_*.csv")
	if err != nil {
		return "", err
	}
	w := csv.NewWriter(file)
	err = w.WriteAll(rows)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return filepath.Clean(file.Name()), nil
}
