package app

import (
	"sort"
	"strings"
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
	// Ratio compares equal input/output tokens. It is an explicit price
	// comparison rather than a promise about the next request's token mix.
	Ratio      float64 `json:"ratio"`
	Unknown    bool    `json:"unknown"`
	Estimated  bool    `json:"estimated"`
	Source     string  `json:"source"`
	Category   string  `json:"category"`
	Current    bool    `json:"current"`
	Cheaper    bool    `json:"cheaper"`
	Comparable bool    `json:"comparable"`
}

// CheaperThan includes every configured cheaper or free option, without a
// capability floor or result cap. Specialised models carry a category label.
func (a *App) CheaperThan(key string) []Alternative {
	var out []Alternative
	for _, m := range a.ModelChoices(key) {
		if !m.Current && m.Cheaper {
			out = append(out, m)
		}
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
		if a.ChatRoot(rec.SessionID) != a.ChatRoot(sid) {
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
