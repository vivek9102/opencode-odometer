package app

import (
	"fmt"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/ledger"
)

type UsageReport struct {
	Start           string               `json:"start"`
	End             string               `json:"end"`
	Calendar        string               `json:"calendar"`
	FirstDate       string               `json:"first_date"`
	Rows            []ledger.UsageBucket `json:"rows"`
	Cost            float64              `json:"cost"`
	Saved           float64              `json:"saved"`
	EstimatedCost   float64              `json:"estimated_cost"`
	UnknownMessages int64                `json:"unknown_messages"`
	Messages        int64                `json:"messages"`
	FreeMessages    int64                `json:"free_messages"`
	Tokens          int64                `json:"tokens"`
	UnallocatedCost float64              `json:"unallocated_cost"`
	UndatedMessages int64                `json:"undated_messages"`
	Storage         StorageSummary       `json:"storage"`
}

func (a *App) UsageReport(start, end string) (UsageReport, error) {
	from, err := time.Parse("2006-01-02", start)
	if err != nil {
		return UsageReport{}, fmt.Errorf("choose a valid start date")
	}
	to, err := time.Parse("2006-01-02", end)
	if err != nil || to.Before(from) || to.Sub(from) > 3660*24*time.Hour {
		return UsageReport{}, fmt.Errorf("choose an end date within ten years of the start")
	}
	report := UsageReport{Start: start, End: end, Rows: []ledger.UsageBucket{}, Calendar: "Local calendar dates captured at ingestion; weeks start Monday", Storage: a.StorageUsage()}
	cp := a.Ledger.Copy()
	var archivedCost float64
	for _, b := range cp.Archived {
		archivedCost += b.Cost
	}
	report.UnallocatedCost = cp.PrunedCost - archivedCost
	if report.UnallocatedCost < 1e-7 {
		report.UnallocatedCost = 0
	}
	for _, b := range cp.Usage() {
		if b.Date == "unknown" {
			report.UndatedMessages += b.Messages
			continue
		}
		if report.FirstDate == "" || b.Date < report.FirstDate {
			report.FirstDate = b.Date
		}
		if b.Date < start || b.Date > end {
			continue
		}
		report.Rows = append(report.Rows, b)
		report.Cost += b.Cost
		report.Saved += b.Saved
		report.Messages += b.Messages
		report.Tokens += b.Input + b.Output + b.CacheRead + b.CacheWrite
		if b.Free {
			report.FreeMessages += b.Messages
		}
		if b.Estimated {
			report.EstimatedCost += b.Cost
		}
		if b.Unknown {
			report.UnknownMessages += b.Messages
		}
	}
	return report, nil
}

func (a *App) ExportUsageCSV(dir, start, end string) (string, error) {
	report, err := a.UsageReport(start, end)
	if err != nil {
		return "", err
	}
	rows := [][]string{{"date", "provider", "model", "messages", "free", "tokens_in", "tokens_out", "reasoning", "cache_read", "cache_write", "cost_usd", "saved_estimate_usd", "estimated", "unknown_price", "cost_source"}}
	for _, r := range report.Rows {
		rows = append(rows, []string{r.Date, csvText(r.Provider), csvText(r.Model), fmt.Sprint(r.Messages), fmt.Sprint(r.Free), fmt.Sprint(r.Input), fmt.Sprint(r.Output), fmt.Sprint(r.Reasoning), fmt.Sprint(r.CacheRead), fmt.Sprint(r.CacheWrite), money(r.Cost), money(r.Saved), fmt.Sprint(r.Estimated), fmt.Sprint(r.Unknown), csvText(r.Source)})
	}
	rows = append(rows, []string{"PERIOD_TOTAL", start, end, fmt.Sprint(report.Messages), "", "", "", "", "", "", money(report.Cost), money(report.Saved), "", "", report.Calendar})
	if report.UnallocatedCost > 0 || report.UndatedMessages > 0 {
		note := make([]string, 15)
		note[0] = "COVERAGE_NOTE"
		note[14] = fmt.Sprintf("Older undated/pruned details cannot be assigned to this range; lifetime unallocated cost $%.6f; undated messages %d", report.UnallocatedCost, report.UndatedMessages)
		rows = append(rows, note)
	}
	return writeCSV(dir, "opencode_usage_"+start+"_"+end, rows)
}
