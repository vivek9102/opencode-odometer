package app

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ExportCSV writes priced messages to a CSV file and returns the path.
func (a *App) ExportCSV(dir string) (string, error) {
	return a.ExportCSVFiltered(dir, a.ViewMode())
}

// ExportCSVFiltered writes priced messages to a CSV file respecting view mode (TRIP / TOTAL).
func (a *App) ExportCSVFiltered(dir, view string) (string, error) {
	msgs := a.Ledger.GetMessagesSnapshot()
	if len(msgs) == 0 {
		return "", fmt.Errorf("nothing recorded yet")
	}
	tag := "total"
	if view == "TRIP" {
		tag = "trip"
	}
	fn := filepath.Join(dir, fmt.Sprintf("opencode_cost_%s_%s.csv", tag, time.Now().Format("2006-01-02_1504")))
	f, err := os.Create(fn)
	if err != nil {
		return "", err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write([]string{"timestamp", "message_id", "session_id", "provider", "model", "free",
		"tokens_in", "tokens_out", "reasoning", "cache_read", "cache_write", "cost_usd", "saved_usd", "finish"}); err != nil {
		return "", err
	}

	type sortable struct {
		key string
		rec [14]string
	}
	rows := make([]sortable, 0, len(msgs))
	for _, r := range msgs {
		freeStr := "no"
		if r.Free {
			freeStr = "yes"
		}
		row := [14]string{r.Timestamp, r.MID, r.SessionID, r.Provider, r.Model, freeStr,
			fmt.Sprintf("%d", r.TokensIn), fmt.Sprintf("%d", r.TokensOut),
			fmt.Sprintf("%d", r.Reasoning), fmt.Sprintf("%d", r.CacheRead),
			fmt.Sprintf("%d", r.CacheWrite), fmt.Sprintf("%.6f", r.Cost),
			fmt.Sprintf("%.6f", r.Saved), r.Finish}
		rows = append(rows, sortable{key: r.Timestamp, rec: row})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	for _, r := range rows {
		if err := w.Write(r.rec[:]); err != nil {
			return "", err
		}
	}
	vCost, vSaved := a.Ledger.GetTotals(view)
	_ = w.Write([]string{"VIEW_TOTAL", view, "", "", "", "", "", "", "", "", "",
		fmt.Sprintf("%.6f", vCost), fmt.Sprintf("%.6f", vSaved), ""})
	return fn, nil
}
