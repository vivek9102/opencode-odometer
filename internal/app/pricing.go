package app

import (
	"strings"

	"github.com/vivek9102/opencode-odometer/internal/prices"
)

// PricePreview explains the effective cost and its provenance. It is also the
// return type for the UI's no-charge pricing simulator.
type PricePreview struct {
	Key          string  `json:"key"`
	Cost         float64 `json:"cost"`
	Source       string  `json:"source"`
	Estimated    bool    `json:"estimated"`
	Free         bool    `json:"free"`
	Unknown      bool    `json:"unknown"`
	ReportedCost float64 `json:"reported_cost"`
	InputRate    float64 `json:"input_rate"`
	OutputRate   float64 `json:"output_rate"`
	CacheRead    float64 `json:"cache_read_rate"`
	CacheWrite   float64 `json:"cache_write_rate"`
}

// PreviewPrice applies the accounting hierarchy without mutating the ledger:
// explicit free/local override, trusted non-zero event cost, exact catalog,
// cross-provider estimate, then unknown.
func (a *App) PreviewPrice(key string, in, out, cacheRead, cacheWrite int64, reportedCost float64) PricePreview {
	key = prices.NormalizeKey("", key)
	rate := a.Prices.Entry(key)
	calculated := a.Prices.Cost(key, in, out, cacheRead, cacheWrite)
	preview := PricePreview{
		Key: key, Cost: calculated, ReportedCost: reportedCost,
		Free: rate.Free, Unknown: rate.Unknown,
		InputRate: rate.Input, OutputRate: rate.Output,
		CacheRead: rate.CacheRead, CacheWrite: rate.CacheWrite,
	}

	switch {
	case rate.Free:
		preview.Cost = 0
		if a.Prices.IsLocal(key) {
			preview.Source = "local override (free)"
		} else {
			preview.Source = "free model marker"
		}
	case a.Prices.IsLocal(key):
		preview.Source = "local override"
	case reportedCost > 0:
		preview.Cost = reportedCost
		preview.Source = "OpenCode event"
		preview.Unknown = false
	case rate.Unknown:
		preview.Cost = 0
		preview.Source = "unpriced"
	case strings.HasPrefix(rate.Source, "estimate from "):
		preview.Source = rate.Source
		preview.Estimated = true
	default:
		preview.Source = "price catalog"
	}

	return preview
}
