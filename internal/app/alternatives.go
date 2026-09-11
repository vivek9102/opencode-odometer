package app

import (
	"sort"
	"strings"

	"github.com/vivek9102/opencode-odometer/internal/prices"
)

// Alternative is a cheaper model suggested as a substitute for the one that
// is currently burning the budget.
type Alternative struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Provider is the part of Key before the slash. The display name from the
	// price table omits it, but the same model is offered by several providers
	// at different prices, so a bare name is ambiguous when choosing.
	Provider string  `json:"provider"`
	Input    float64 `json:"input"`
	Output   float64 `json:"output"`
	Free     bool    `json:"free"`
	// Ratio is the alternative's output price as a fraction of the current
	// model's, so "38% of current" can be shown rather than a bare price.
	Ratio float64 `json:"ratio"`
}

// notSubstitutes are models too weak or too specialised to recommend as a
// coding substitute, whatever their price. Suggesting an embedding model or a
// nano tier as an Opus replacement is worse than suggesting nothing.
var notSubstitutes = []string{
	"embed", "image", "-vl-", "nano", "gemma", "e5-",
	"titan", "ocr", "asr", "realtime", "translate", "guard", "rerank",
}

// floorRatio excludes models priced under this fraction of the current one:
// anything that much cheaper is a different class of model, not a substitute.
const floorRatio = 0.05

// CheaperThan returns credible cheaper substitutes for a model key.
//
// Free models are always offered first (they end the spend outright), then
// paid models ranked closest-in-capability first. Alternatives from the same
// provider are preferred because those are the ones actually selectable in
// the user's OpenCode configuration.
func (a *App) CheaperThan(key string) []Alternative {
	if key == "" {
		return nil
	}
	a.Prices.RLockModels()
	models := make(map[string]prices.Rate, len(a.Prices.Models))
	for k, v := range a.Prices.Models {
		models[k] = v
	}
	a.Prices.RUnlockModels()

	cur, ok := models[key]
	if !ok {
		cur = a.Prices.Entry(key)
	}
	base := cur.Output
	if base <= 0 {
		return nil
	}

	provider := key
	if i := strings.Index(key, "/"); i >= 0 {
		provider = key[:i]
	}

	// Only suggest models the user has actually seen in use or that share the
	// current provider; a global catalogue dump is noise.
	a.mu.RLock()
	seen := make(map[string]bool, len(a.state.SeenModels))
	for k := range a.state.SeenModels {
		seen[k] = true
	}
	a.mu.RUnlock()

	var free, paid []Alternative
	for k, m := range models {
		if k == key {
			continue
		}
		low := strings.ToLower(k)
		skip := false
		for _, bad := range notSubstitutes {
			if strings.Contains(low, bad) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		sameProvider := strings.HasPrefix(k, provider+"/")
		if !sameProvider && !seen[k] {
			continue
		}

		name := k
		if m.Name != "" {
			name = m.Name
		}
		altProvider := k
		if i := strings.Index(k, "/"); i >= 0 {
			altProvider = k[:i]
		}
		if m.Free {
			free = append(free, Alternative{
				Key: k, Name: name, Provider: altProvider, Free: true, Ratio: 0,
			})
			continue
		}
		if m.Output <= 0 || m.Output >= base {
			continue
		}
		if m.Output/base < floorRatio {
			continue
		}
		paid = append(paid, Alternative{
			Key: k, Name: name, Provider: altProvider,
			Input: m.Input, Output: m.Output,
			Ratio: m.Output / base,
		})
	}

	sort.Slice(free, func(i, j int) bool { return free[i].Key < free[j].Key })
	// closest in capability first: the most expensive of the cheaper options
	sort.Slice(paid, func(i, j int) bool { return paid[i].Ratio > paid[j].Ratio })

	out := append(free, paid...)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// TopSpenders returns the models that drove a session's cost, highest first.
// This answers "what spent it" when the limit is hit.
func (a *App) TopSpenders(sid string, n int) []Alternative {
	if sid == "" {
		return nil
	}
	totals := map[string]float64{}
	for _, rec := range a.Ledger.GetMessagesSnapshot() {
		if rec.SessionID != sid {
			continue
		}
		totals[rec.Provider+"/"+rec.Model] += rec.Cost
	}
	out := make([]Alternative, 0, len(totals))
	for k, v := range totals {
		prov := k
		if i := strings.Index(k, "/"); i >= 0 {
			prov = k[:i]
		}
		out = append(out, Alternative{Key: k, Name: k, Provider: prov, Output: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Output != out[j].Output {
			return out[i].Output > out[j].Output
		}
		return out[i].Key < out[j].Key
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}
