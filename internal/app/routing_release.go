package app

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Read the selection itself: its acknowledgement may predate the source tag.
// Legacy fallback/restoration files are recognised only in a switch policy.
func (a *App) releaseBudgetModel(sid string, policy OpenPolicy) (bool, error) {
	if sid == "" {
		return true, nil
	}
	var choice ModelSwitch
	raw, err := os.ReadFile(filepath.Join(a.experienceDir(), "switch-session-"+sid+".json"))
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if json.Unmarshal(raw, &choice) != nil {
		return false, nil
	}
	budgetOwned := choice.Source == "budget" || choice.Source == "budget_restore" ||
		(choice.Source == "" && policy.Rule == "switch" && policy.FallbackModel != "" &&
			(choice.Key == policy.OriginalModel || choice.Key == policy.FallbackModel))
	if !budgetOwned {
		return false, nil
	}
	return true, a.ClearModelSwitch(sid)
}

// Upgrade the old untagged restoration pins once, after loading policies.
// Active fallbacks and deliberate tagged manual choices remain in force.
func (a *App) releaseInactiveBudgetModels() {
	a.mu.RLock()
	policies := make([]OpenPolicy, 0, len(a.state.OpenPolicies))
	for _, p := range a.state.OpenPolicies {
		if !p.Enabled || p.Stage == "original" {
			policies = append(policies, p)
		}
	}
	a.mu.RUnlock()
	for _, p := range policies {
		if _, err := a.releaseBudgetModel(p.SessionID, p); err != nil {
			a.LogEvent("budget routing release failed: %v", err)
		}
	}
}
