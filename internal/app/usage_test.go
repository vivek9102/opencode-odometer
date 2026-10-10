package app

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/vivek9102/opencode-odometer/internal/ledger"
)

func csvRows(t *testing.T, path string) [][]string {
	t.Helper()
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	r, e := csv.NewReader(f).ReadAll()
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func checkCSVSum(t *testing.T, path string, expected float64) [][]string {
	t.Helper()
	rows := csvRows(t, path)
	var sum float64
	for _, r := range rows[1 : len(rows)-1] {
		v, e := strconv.ParseFloat(r[11], 64)
		if e != nil {
			t.Fatal(e)
		}
		sum += v
	}
	footer, e := strconv.ParseFloat(rows[len(rows)-1][11], 64)
	if e != nil {
		t.Fatal(e)
	}
	if math.Abs(sum-footer) > 1e-9 || math.Abs(footer-expected) > 1e-9 {
		t.Fatalf("rows %v footer %v expected %v", sum, footer, expected)
	}
	return rows
}

func TestPeriodCSVStreamingResetPruneAndRestart(t *testing.T) {
	a, _, _ := newTestApp(t)
	r := ledger.Record{MID: "stream", Provider: "example", Model: "chat", TokensIn: 100, Cost: 1, Timestamp: "2026-10-01 23:59:59"}
	a.Ledger.Put(r)
	a.ResetTrip()
	r.TokensIn = 130
	r.Cost = 1.3
	a.Ledger.Put(r)
	a.Ledger.Put(ledger.Record{MID: "new", Cost: .2, Timestamp: "2026-10-02 00:00:01"})
	path, e := a.ExportCSVFiltered(t.TempDir(), "TRIP")
	if e != nil {
		t.Fatal(e)
	}
	rows := checkCSVSum(t, path, .5)
	if rows[1][6] != "30" || rows[1][14] != "message_delta_since_reset" {
		t.Fatal("streaming baseline not reflected", rows)
	}
	a.cfg.MaxMessages = 1
	a.SaveState()
	restarted, e := New(a.cfg)
	if e != nil {
		t.Fatal(e)
	}
	path, e = restarted.ExportCSVFiltered(t.TempDir(), "TRIP")
	if e != nil {
		t.Fatal(e)
	}
	checkCSVSum(t, path, .5)
	path, e = restarted.ExportCSVFiltered(t.TempDir(), "TOTAL")
	if e != nil {
		t.Fatal(e)
	}
	checkCSVSum(t, path, 1.5)
	report, e := restarted.UsageReport("2026-10-01", "2026-10-01")
	if e != nil {
		t.Fatal(e)
	}
	if report.Cost != 1.3 || report.Tokens != 130 || report.Messages != 1 {
		t.Fatalf("pruned day changed %+v", report)
	}
	if restarted.Ledger.Put(r) {
		t.Fatal("history replay recounted archived message")
	}
	report, e = restarted.UsageReport("2026-10-02", "2026-10-02")
	if e != nil || report.Cost != .2 {
		t.Fatal("midnight boundary", report, e)
	}
}

func TestOpenCSVOnlyCountsLiveRootsAndChildrenOnce(t *testing.T) {
	a, _, _ := newTestApp(t)
	reportTUI(t, a, "one", "root", false)
	reportTUI(t, a, "two", "root", false)
	a.RefreshOpenSessions()
	a.SetSessionParent("child", "root")
	addOpenSpend(a, "root-message", "root", .4)
	addOpenSpend(a, "child-message", "child", .2)
	addOpenSpend(a, "elsewhere", "closed", 9)
	a.Ledger.Put(ledger.Record{MID: "before-tui", SessionID: "root", Cost: 3, Timestamp: "2020-01-01 00:00:00"})
	path, e := a.ExportCSVFiltered(t.TempDir(), "RUN")
	if e != nil {
		t.Fatal(e)
	}
	rows := checkCSVSum(t, path, .6)
	if len(rows) != 4 {
		t.Fatal("unrelated rows exported", rows)
	}
	reportTUI(t, a, "one", "root", true)
	reportTUI(t, a, "two", "root", true)
	a.RefreshOpenSessions()
	path, e = a.ExportCSVFiltered(t.TempDir(), "RUN")
	if e != nil {
		t.Fatal(e)
	}
	checkCSVSum(t, path, 0)
}

func TestUsageProvenanceCorrectionsAndLegacyCoverage(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.Ledger.PrunedCost = 4
	a.Ledger.Put(ledger.Record{MID: "known", Cost: 2, Estimated: true, Timestamp: "2026-10-05 10:00:00"})
	a.Ledger.Put(ledger.Record{MID: "free", Free: true, Saved: 3, Timestamp: "2026-10-06 11:00:00"})
	a.Ledger.Put(ledger.Record{MID: "unknown", Unknown: true, Timestamp: "2026-10-06 11:01:00"})
	a.Ledger.Put(ledger.Record{MID: "removed", Cost: 9, Timestamp: "2026-10-06 11:02:00"})
	a.Ledger.Remove("removed")
	r, e := a.UsageReport("2026-10-05", "2026-10-11")
	if e != nil {
		t.Fatal(e)
	}
	if r.Cost != 2 || r.Saved != 3 || r.UnknownMessages != 1 || r.EstimatedCost != 2 || r.FreeMessages != 1 || r.UnallocatedCost != 4 {
		t.Fatal(r)
	}
	path, e := a.ExportUsageCSV(t.TempDir(), r.Start, r.End)
	if e != nil {
		t.Fatal(e)
	}
	rows := csvRows(t, path)
	var sum float64
	for _, row := range rows[1 : len(rows)-2] {
		v, _ := strconv.ParseFloat(row[10], 64)
		sum += v
	}
	if sum != 2 || rows[len(rows)-2][0] != "PERIOD_TOTAL" || rows[len(rows)-1][0] != "COVERAGE_NOTE" {
		t.Fatal(rows)
	}
	path, e = a.ExportCSVFiltered(t.TempDir(), "TOTAL")
	if e != nil {
		t.Fatal(e)
	}
	checkCSVSum(t, path, 6)
	path, e = a.ExportCSVFiltered(t.TempDir(), "TRIP")
	if e != nil {
		t.Fatal(e)
	}
	rows = checkCSVSum(t, path, 6)
	if rows[1][1] != "LEGACY_PERIOD_SUMMARY" {
		t.Fatal("legacy baseline falsely labelled detailed", rows)
	}
	if _, e = a.UsageReport("bad", "2026-10-10"); e == nil {
		t.Fatal("invalid date accepted")
	}
	if _, e = a.UsageReport("2026-10-11", "2026-10-10"); e == nil {
		t.Fatal("reversed range accepted")
	}
	if _, e = a.ExportCSVFiltered(filepath.Join(t.TempDir(), "export"), "bad"); e == nil {
		t.Fatal("invalid scope accepted")
	}
}
