package app

import (
	"time"

	"github.com/vivek9102/opencode-odometer/internal/budget"
	"github.com/vivek9102/opencode-odometer/internal/prices"
)

// ViewMode reports whether the board shows TRIP or TOTAL spend.
func (a *App) ViewMode() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.state.View == "" {
		return "TRIP"
	}
	return a.state.View
}

// SetViewMode flips the board between TRIP and TOTAL.
func (a *App) SetViewMode(v string) {
	a.mu.Lock()
	a.state.View = v
	a.mu.Unlock()
	a.SaveState()
}

// ResetTrip moves the trip baseline to the current spend, zeroing the trip
// counter while leaving lifetime totals intact.
//
// It deliberately does NOT rebase the per-session budget. The trip counter and
// the session cap are independent: the trip is a display convenience ("what
// have I spent since I last looked"), whereas the cap is a safety control.
// Rebasing here meant pressing RESET TRIP silently disarmed enforcement for a
// session that was already over its limit, so a runaway session could be freed
// by a button that appears to be about the display only. Turning the limit
// off and back on explicitly starts a new counting period.
func (a *App) ResetTrip() {
	a.Ledger.ResetTrip()
	a.ResetRateWindow()
	a.SetViewMode("TRIP")
	a.PublishBudget()
	a.SaveState()
}

// ReloadPrices re-reads costs/overlay from disk so price edits apply to future
// messages. Completed ledger records retain the cost and source captured when
// they arrived; a maintenance action must never rewrite history silently.
func (a *App) ReloadPrices() error {
	if err := a.Prices.Reload(); err != nil {
		return err
	}
	a.SaveState()
	return nil
}

// RefreshPrices downloads the latest catalogue for future messages.
//
// Reload only re-read local files despite the UI calling the action "reload
// prices", so a user with a stale table had no way to update it from the app.
// Returns the number of models fetched.
func (a *App) RefreshPrices() (int, error) {
	n, err := a.Prices.Refresh()
	if err != nil {
		a.logEvent("price refresh failed: %v", err)
		return 0, err
	}
	a.SaveState()
	a.logEvent("price refresh ok models=%d", n)
	return n, nil
}

// reclassifyFreeModels repairs only the safe direction of a classification
// change: a persisted paid record whose current model entry is free. This is
// intentionally narrower than repriceLedger so an app upgrade cannot silently
// rewrite historical paid totals merely because the public catalogue changed.
func (a *App) reclassifyFreeModels() {
	a.Ledger.Lock()
	changed := false
	for mid, rec := range a.Ledger.Messages {
		if rec.Free {
			continue
		}
		key := prices.NormalizeKey(rec.Provider, rec.Model)
		// A user-authored free price is prospective, just like every other
		// local price change. This repair is only for built-in classification
		// improvements such as the sovereign/free naming convention.
		if a.Prices.IsLocal(key) {
			continue
		}
		if !a.Prices.IsFree(key) {
			continue
		}
		rec.Free = true
		rec.Cost = 0
		rec.Saved = a.Prices.ShadowCost(key, rec.TokensIn, rec.TokensOut, rec.CacheRead, rec.CacheWrite)
		rec.Unknown = false
		rec.Estimated = false
		rec.CostSource = "free model marker"
		a.Ledger.Messages[mid] = rec
		changed = true
	}
	if changed {
		a.Ledger.RecomputeLocked()
	}
	a.Ledger.Unlock()
}

// PricesAge reports how old the local catalogue is.
func (a *App) PricesAge() time.Duration { return a.Prices.CatalogAge() }

// PricesAreSeeded reports that prices come from the built-in snapshot and have
// not yet been refreshed from the network.
func (a *App) PricesAreSeeded() bool { return prices.IsSeeded(a.cfg.PricesFile) }

// MaybeRefreshPrices downloads a new catalogue when the local one is older
// than maxAge. Intended for a background tick: failure is not fatal, since a
// stale table still prices better than no table.
func (a *App) MaybeRefreshPrices(maxAge time.Duration) {
	// A table straight from the embedded snapshot is as old as the build, so
	// refresh it regardless of its file timestamp.
	if !a.PricesAreSeeded() {
		age := a.Prices.CatalogAge()
		if age > 0 && age < maxAge {
			return
		}
	}
	if _, err := a.RefreshPrices(); err != nil {
		a.logEvent("background price refresh skipped: %v", err)
	}
}

// SetMode switches enforcement strictness (warn/soft/hard) and republishes.
func (a *App) SetMode(m string) {
	a.Budget.Cfg.Mode = budget.Mode(m)
	a.PublishBudget()
	a.SaveState()
}

// SetLimit changes the cap without clearing the spend already counted.
func (a *App) SetLimit(f float64) {
	if f < 0 {
		f = 0
	}
	a.Budget.Cfg.SessionLimitUSD = round4(f)
	a.PublishBudget()
	a.SaveState()
}

// SetEnabled toggles whether the session limit is enforced.
//
// Ticking the box starts the count at zero: everything spent beforehand is
// grandfathered, so switching a limit on never retroactively blocks work
// already done. Unticking clears the count and any granted overrides, so the
// next tick is a genuinely clean slate rather than a resumption of an old
// tally.
func (a *App) SetEnabled(on bool) {
	if a.Budget.Enabled() == on {
		return
	}
	a.Budget.Cfg.Enabled = on
	if on {
		a.Rebase(true)
	} else {
		a.mu.Lock()
		a.state.BudgetBaselines = map[string]float64{}
		a.state.BudgetAllow = map[string]float64{}
		a.graceUsed = map[string]int{}
		a.state.GraceUsed = a.graceUsed
		a.mu.Unlock()
	}
	a.PublishBudget()
	a.SaveState()
}

// Dock cycle order for the window-positioning control.
//
// "top-center" was implemented in the positioning code but missing from this
// list, so the middle docks were unreachable from the UI. Both centre
// positions are included and the order walks the screen predictably.
var docks = []string{
	"bottom-center", "bottom-right", "bottom-left",
	"top-center", "top-right", "top-left",
}

// Docks returns the available dock positions, for menus.
func Docks() []string { return append([]string(nil), docks...) }

// CycleDock advances to the next dock position, enables docked mode, and returns the new one.
func (a *App) CycleDock() string {
	a.mu.Lock()
	cur := a.state.Dock
	next := docks[0]
	for i, d := range docks {
		if d == cur {
			next = docks[(i+1)%len(docks)]
			break
		}
	}
	a.state.Dock = next
	a.state.Docked = true
	a.mu.Unlock()
	a.SaveState()
	return next
}

// SetDock pins the widget to a named dock position.
func (a *App) SetDock(d string) string {
	valid := false
	for _, k := range docks {
		if k == d {
			valid = true
			break
		}
	}
	if !valid {
		return a.Dock()
	}
	a.mu.Lock()
	a.state.Dock = d
	a.state.Docked = true
	a.state.Pos = nil
	a.mu.Unlock()
	a.SaveState()
	return d
}

// Dock reports the current dock position.
func (a *App) Dock() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.state.Dock == "" {
		return docks[0]
	}
	return a.state.Dock
}

// DockMode reports whether the widget is in docked mode (vs floating mode).
func (a *App) DockMode() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Docked
}

// SetDockMode toggles docked mode vs floating mode.
func (a *App) SetDockMode(docked bool) {
	a.mu.Lock()
	a.state.Docked = docked
	a.mu.Unlock()
	a.SaveState()
}

// Compact reports whether the widget should start as the small bar rather
// than the expanded board.
func (a *App) Compact() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Compact
}

// SetCompact records the compact/expanded choice so it survives a restart.
func (a *App) SetCompact(on bool) {
	a.mu.Lock()
	a.state.Compact = on
	a.mu.Unlock()
	a.SaveState()
}

// Pos returns the persisted [x, y] window coordinates for floating mode.
func (a *App) Pos() []int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.state.Pos) == 2 {
		return []int{a.state.Pos[0], a.state.Pos[1]}
	}
	return nil
}

// SetPos saves the window floating position and sets floating mode.
func (a *App) SetPos(x, y int) {
	a.mu.Lock()
	a.state.Pos = []int{x, y}
	a.state.Docked = false
	a.mu.Unlock()
	a.SaveState()
}
