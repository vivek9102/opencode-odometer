// Package wservice exposes the Odometer backend to the Wails (HTML/JS)
// frontend. It binds the app core (pricing, ledger, budget, SSE client) to
// JavaScript, runs the live ingest loop, and pushes snapshots to the UI.
package wservice

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/app"
	"github.com/vivek9102/opencode-odometer/internal/ledger"
	"github.com/vivek9102/opencode-odometer/internal/prices"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ModelRow is one per-model aggregate row for the board table.
type ModelRow struct {
	Key     string  `json:"key"`
	Msgs    int64   `json:"msgs"`
	In      int64   `json:"in"`
	Out     int64   `json:"out"`
	Cache   int64   `json:"cache"`
	Cost    float64 `json:"cost"`
	Saved   float64 `json:"saved"`
	Free    bool    `json:"free"`
	Unknown bool    `json:"unknown"`
	// Estimated marks a rate borrowed from the same model under a different
	// provider (e.g. anthropic/claude-opus-5 used to price
	// companyhub/claude-opus-5). The cost is a best guess, and the UI says so
	// rather than presenting it as authoritative.
	Estimated bool   `json:"estimated"`
	Source    string `json:"source,omitempty"`
}

// ModelOption represents an available model in the model switch picker.
type ModelOption struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Provider disambiguates identical model names offered by several
	// providers at different prices.
	Provider    string  `json:"provider"`
	Tier        string  `json:"tier"`
	InputCost   float64 `json:"input_cost"`
	OutputCost  float64 `json:"output_cost"`
	Free        bool    `json:"free"`
	Description string  `json:"description"`
}

// Snapshot is the full UI state the frontend renders on each refresh.
type Snapshot struct {
	View      string  `json:"view"`
	Cost      float64 `json:"cost"`
	Saved     float64 `json:"saved"`
	TripCost  float64 `json:"trip_cost"`
	TotalCost float64 `json:"total_cost"`
	Rate      float64 `json:"rate"`
	Model     string  `json:"model"`
	Tokens    int64   `json:"tokens"`
	Seeded    bool    `json:"seeded"`
	Active    bool    `json:"active"`

	BudgetEnabled bool    `json:"budget_enabled"`
	Limit         float64 `json:"limit"`
	Exceeded      bool    `json:"exceeded"`
	Mode          string  `json:"mode"`
	HardStopAt    float64 `json:"hard_stop_at"`
	Fraction      float64 `json:"fraction"`
	BudgetLabel   string  `json:"budget_label"`

	// BudgetState is the session verdict: "ok", "warn" or "over". This is the
	// same measurement the plugin enforces on, so the UI and the enforcement
	// can no longer disagree.
	BudgetState string `json:"budget_state"`
	// SessionCost is the active session's spend against the cap (post-baseline).
	SessionCost    float64 `json:"session_cost"`
	SessionID      string  `json:"session_id"`
	GraceRemaining int     `json:"grace_remaining"`
	PastHardStop   bool    `json:"past_hard_stop"`
	// Enforced reports whether the current settings ever refuse a turn, so
	// "warn only" can be stated rather than implied.
	Enforced bool `json:"enforced"`
	// SessionCount and OtherCost surface the spend the gauge does NOT cover.
	// The bar tracks one session because that is what blocks, so combined
	// burn across tabs would otherwise vanish from the UI entirely.
	SessionCount int     `json:"session_count"`
	OtherCost    float64 `json:"other_cost"`
	// WarnAt is the amber threshold as a fraction. The frontend hardcoded
	// 0.8 while WarnAtPercent is configurable.
	WarnAt float64 `json:"warn_at"`

	// UnpricedModels counts models in use with no known rate, and
	// UnpricedTokens the tokens they consumed. Unpriced spend contributes $0,
	// so without this the odometer reports a confident "$0.0000" that is
	// indistinguishable from having genuinely spent nothing.
	UnpricedModels int   `json:"unpriced_models"`
	UnpricedTokens int64 `json:"unpriced_tokens"`

	// Connected reports whether the OpenCode event stream is attached. Without
	// it, an unreachable server is indistinguishable from an idle one.
	Connected bool   `json:"connected"`
	ServerURL string `json:"server_url"`
	// Link is "live", "idle" or "offline". The plugin only writes on an
	// assistant message, so a quiet link is normal and must not be reported as
	// a fault; only prolonged silence means the data path is really gone.
	Link    string `json:"link"`
	IdleFor string `json:"idle_for"`
	// EstimatedModels counts models priced from another provider's identical
	// model. Shown as a single quiet note rather than a nag per row.
	EstimatedModels int `json:"estimated_models"`
	// Via names the active ingest path: "stream" (HTTP/SSE) or "plugin"
	// (events spooled from inside OpenCode).
	Via string `json:"via"`

	Dock    string     `json:"dock"`
	Docked  bool       `json:"docked"`
	Compact bool       `json:"compact"`
	Rows    []ModelRow `json:"rows"`
}

// Service is the object bound into the JS frontend.
type Service struct {
	App        *app.App
	Ctx        context.Context
	appCompact bool
	screenW    int
	screenH    int
	stopCh     chan struct{}
	cancel     context.CancelFunc

	backoffMu  sync.Mutex
	backoffCur time.Duration

	alertMu          sync.Mutex
	alertActive      bool
	alertPrevCompact bool
}

// Dimensions of the budget alert window, mirroring the original dialog.
const (
	alertWidth  = 580
	alertHeight = 560
)

// New returns a Service.
func New() *Service {
	return &Service{appCompact: true, stopCh: make(chan struct{})}
}

// Stop terminates the background loops.
func (s *Service) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
}

// SetContext stores the Wails context for event emission / window control.
func (s *Service) SetContext(ctx context.Context) { s.Ctx = ctx }

// SetScreenSize is called by the frontend so dock snapping can use the real
// monitor dimensions (JS window.screen is the simplest reliable source).
func (s *Service) SetScreenSize(w, h int) { s.screenW, s.screenH = w, h }

// DebugLog lets the frontend write into the ingest log, so UI behaviour that
// cannot be observed from Go can still be traced in the field.
func (s *Service) DebugLog(msg string) {
	if s.App != nil {
		s.App.LogEvent("[ui] %s", msg)
	}
}

func (s *Service) snapshot() *Snapshot {
	a := s.App
	if a == nil || a.Ledger == nil {
		return &Snapshot{
			View:    "TRIP",
			Dock:    "bottom-center",
			Compact: s.appCompact,
			Mode:    "soft",
			Rows:    []ModelRow{},
		}
	}
	view := a.ViewMode()
	viewCost, viewSaved := a.Ledger.GetTotals(view)
	tripCost, _ := a.Ledger.GetTotals("TRIP")
	totalCost, _ := a.Ledger.GetTotals("TOTAL")

	// The budget bar must report the SESSION verdict, because that is what the
	// plugin blocks on. Showing trip spend here meant the bar could read 0%
	// while a turn was actually being refused, or read "over" while nothing
	// was enforced.
	limit := a.Budget.Limit()
	sess := a.ActiveSessionBudget()
	// Fraction ships UNCLAMPED so the UI can distinguish "over, grace still
	// available" from "past the hard stop". The bar clamps its own width.
	frac := sess.Fraction
	label := budgetLabel(a, sess)

	// Spend on every session other than the one the gauge tracks.
	allSessions := a.Ledger.GetPerSessionSnapshot()
	otherCost := 0.0
	for id, st := range allSessions {
		if id != sess.SessionID {
			otherCost += st.Cost
		}
	}
	warnAt := float64(a.Budget.Cfg.WarnAtPercent) / 100.0
	if warnAt <= 0 || warnAt >= 1 {
		warnAt = 0.8
	}
	items := a.Ledger.GetModelItems()
	rows := make([]ModelRow, 0, len(items))
	estimated := 0
	unpricedModels := 0
	var unpricedTokens int64
	for _, it := range items {
		m := it.Stat
		row := ModelRow{
			Key: it.Key, Msgs: m.Msgs, In: m.TokensIn, Out: m.TokensOut,
			Cache: m.CacheRead + m.CacheWrite, Cost: m.Cost, Saved: m.Saved,
			Free: m.Free, Unknown: m.Unknown,
		}
		if m.Unknown {
			unpricedModels++
			unpricedTokens += m.TokensIn + m.TokensOut + m.CacheRead + m.CacheWrite
		}
		if !m.Free && !m.Unknown {
			if e := a.Prices.Entry(it.Key); e.Source != "" {
				row.Estimated = true
				row.Source = e.Source
				estimated++
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Cost > rows[j].Cost })

	// The stream is only one way in. When OpenCode runs its API in-process
	// there is no port to attach to, yet the plugin is still feeding us, so
	// treat a live spool as connected — otherwise the UI reads "offline"
	// while the numbers are visibly moving.
	connected, serverURL := a.Connected()
	via := "stream"
	// link is one of: live | idle | offline.
	//
	// The plugin only writes on an assistant message, so any pause for reading
	// or typing looks identical to a broken pipe if the only question asked is
	// "was there data recently". Reporting a fault during normal thinking time
	// trains the user to ignore the indicator.
	link := "offline"
	switch {
	case connected:
		link = "live"
	case a.SpoolActive():
		connected = true
		via = "plugin"
		serverURL = "plugin (in-process)"
		link = "live"
	case a.SpoolIdle():
		via = "plugin"
		serverURL = "plugin (in-process)"
		link = "idle"
	case a.PluginInstalled() && !a.SpoolLastSeen().IsZero():
		// Installed and has worked before, but silent long enough that
		// OpenCode has probably been closed.
		via = "plugin"
		link = "offline"
	}
	idleFor := ""
	if seen := a.SpoolLastSeen(); !seen.IsZero() {
		idleFor = time.Since(seen).Round(time.Second).String()
	}

	return &Snapshot{
		View: view, Cost: viewCost, Saved: viewSaved,
		TripCost: tripCost, TotalCost: totalCost,
		Rate: a.BurnRate(),
		Model: modelName(a.Ledger.ActiveModel()), Tokens: a.Ledger.TotalTokens(),
		Seeded: a.Seeded(), Active: a.LastActivity().Add(6 * time.Second).After(time.Now()),
		BudgetEnabled: a.Budget.Enabled(), Limit: limit,
		Exceeded:    a.Budget.Enabled() && sess.State == "over",
		Mode:        string(a.Budget.Mode()), HardStopAt: a.Budget.HardStopAt(),
		Fraction:    frac, BudgetLabel: label,
		BudgetState: sess.State, SessionCost: sess.Cost, SessionID: sess.SessionID,
		GraceRemaining: sess.GraceRemaining, PastHardStop: sess.PastHardStop,
		Enforced:       a.Budget.Blocks(),
		SessionCount:   len(allSessions), OtherCost: otherCost, WarnAt: warnAt,
		UnpricedModels: unpricedModels, UnpricedTokens: unpricedTokens,
		Connected:      connected, ServerURL: serverURL, Via: via,
		Link:           link, IdleFor: idleFor,
		EstimatedModels: estimated,
		Dock: a.Dock(), Docked: a.DockMode(), Compact: s.appCompact, Rows: rows,
	}
}

// budgetLabel renders the budget readout, mirroring the original: it states
// the spend, the percentage, and — crucially — what will happen next
// (grace left, hard stop, warn-only), instead of a bare ratio.
func budgetLabel(a *app.App, sess app.SessionBudget) string {
	limit := a.Budget.Limit()
	if !a.Budget.Enabled() {
		// Show what WOULD be counted: a limit you are about to switch on
		// should not read "off" when the session is already over it.
		if limit > 0 && sess.Cost > 0 {
			return fmt.Sprintf("off  ($%.3f / $%g)", sess.Cost, limit)
		}
		return "off"
	}
	if limit <= 0 {
		return "no limit set"
	}

	suffix := ""
	if sess.State == "over" {
		switch {
		case !a.Budget.Blocks():
			suffix = "  (warn only)"
		case sess.PastHardStop:
			suffix = "  HARD STOP"
		case sess.GraceRemaining > 0:
			suffix = fmt.Sprintf("  +%d grace", sess.GraceRemaining)
		default:
			suffix = "  BLOCKED"
		}
	}
	return fmt.Sprintf("$%.3f / $%g  %.0f%%%s",
		sess.Cost, limit, sess.Fraction*100, suffix)
}

// Snapshot returns the current UI state for the frontend.
func (s *Service) Snapshot() *Snapshot { return s.snapshot() }

// -------- controls --------

// SetLimit sets the per-session limit in dollars.
func (s *Service) SetLimit(f float64) {
	if s.App != nil {
		s.App.SetLimit(f)
	}
}

// SetMode sets enforcement strictness: warn, soft or hard.
func (s *Service) SetMode(m string) {
	if s.App != nil {
		s.App.SetMode(m)
	}
}

// SetEnabled toggles whether the session limit is enforced.
func (s *Service) SetEnabled(on bool) {
	if s.App != nil {
		s.App.SetEnabled(on)
	}
}

// ResetTrip zeroes the trip counter and clears exceeded state for the new trip.
func (s *Service) ResetTrip() {
	if s.App != nil {
		s.App.ResetTrip()
		s.refresh()
	}
}

// RefreshPrices downloads the latest catalogue from models.dev and reprices.
// Returns a short status string for the UI, since a silent refresh gives the
// user no way to tell a successful update from a failed one.
func (s *Service) RefreshPrices() string {
	if s.App == nil {
		return "error: app not initialized"
	}
	n, err := s.App.RefreshPrices()
	if err != nil {
		return "error: " + err.Error()
	}
	s.refresh()
	return fmt.Sprintf("Updated %d model prices", n)
}

// PricesAgeHours reports how old the local price table is, so the UI can say
// when it was last updated rather than implying it is current.
//
// Returns -1 when the table is the built-in snapshot: it has no meaningful
// age, and reporting one made a fresh install claim its prices were a year old.
func (s *Service) PricesAgeHours() float64 {
	if s.App == nil {
		return 0
	}
	if s.App.PricesAreSeeded() {
		return -1
	}
	return s.App.PricesAge().Hours()
}

// ReloadPrices reloads and reprices from disk.
func (s *Service) ReloadPrices() error {
	if s.App != nil {
		return s.App.ReloadPrices()
	}
	return nil
}

// ToggleViewMode flips TRIP/TOTAL.
func (s *Service) ToggleViewMode() {
	if s.App != nil {
		s.App.SetViewMode(flip(s.App.ViewMode()))
		s.refresh()
	}
}

// CycleDock advances to the next dock position, enables docked mode, and repositions.
func (s *Service) CycleDock() string {
	if s.App == nil {
		return "bottom-center"
	}
	d := s.App.CycleDock()
	s.applyPosition()
	s.refresh()
	return d
}

// SetDock pins the widget to a named dock position and repositions it.
func (s *Service) SetDock(d string) string {
	if s.App == nil {
		return "bottom-center"
	}
	out := s.App.SetDock(d)
	s.applyPosition()
	s.refresh()
	return out
}

// Docks lists the available dock positions for the context menu.
func (s *Service) Docks() []string { return app.Docks() }

// ToggleDock switches between docked mode (pinned to screen edge) and floating mode.
func (s *Service) ToggleDock() bool {
	if s.App == nil {
		return false
	}
	newMode := !s.App.DockMode()
	s.App.SetDockMode(newMode)
	s.applyPosition()
	s.refresh()
	return newMode
}

// SavePosition saves the window floating coordinates.
func (s *Service) SavePosition(x, y int) {
	if s.App != nil {
		s.App.SetPos(x, y)
		s.refresh()
	}
}

// PendingPriceSuggestions returns unconfigured seen models.
func (s *Service) PendingPriceSuggestions() []prices.PriceSuggestion {
	if s.App == nil {
		return nil
	}
	return s.App.PendingPriceSuggestions()
}

// ApplyPriceOverride persists a user override to prices.local.json.
func (s *Service) ApplyPriceOverride(key string, rate prices.Rate) error {
	if s.App == nil {
		return fmt.Errorf("app not initialized")
	}
	err := s.App.SaveOverride(key, rate)
	if err == nil {
		s.refresh()
	}
	return err
}

// SkipPrice acknowledges an unknown price without persisting an override.
func (s *Service) SkipPrice(key string) {
	if s.App != nil && s.App.Prices != nil {
		s.App.IgnorePrice(key)
		s.refresh()
	}
}

// AvailableModels returns real cheaper substitutes for the model currently
// driving spend, derived from the loaded price table.
//
// The previous implementation returned a hardcoded list of invented models and
// invented prices, which is worse than useless: it advises a switch to
// something that may not exist at a price that is not real.
func (s *Service) AvailableModels() []ModelOption {
	if s.App == nil {
		return []ModelOption{}
	}
	current := s.App.Ledger.ActiveModel()
	alts := s.App.CheaperThan(current)
	out := make([]ModelOption, 0, len(alts))
	for _, a := range alts {
		price := fmt.Sprintf("%.0f%% of current output price", a.Ratio*100)
		tier := "CHEAPER"
		if a.Free {
			price = "Free - no budget impact"
			tier = "FREE"
		}
		// Lead with what the model is FOR. A ranked price list still leaves
		// the user guessing whether the cheap option can do the job, which is
		// the actual question when swapping mid-task.
		desc := price
		if use := bestFor(a.Key); use != "" {
			desc = use + " - " + price
		}
		out = append(out, ModelOption{
			Key: a.Key, Name: a.Name, Provider: a.Provider, Tier: tier,
			InputCost: a.Input, OutputCost: a.Output,
			Free: a.Free, Description: desc,
		})
	}
	return out
}

// bestFor labels a model with the work it suits, keyed on family substrings so
// new point releases inherit the label without a table update.
//
// These are coarse by design: the goal is "safe to swap for this kind of task",
// not a benchmark ranking.
func bestFor(key string) string {
	k := strings.ToLower(key)
	switch {
	case strings.Contains(k, "opus"):
		return "Deep logic, tricky bugs"
	case strings.Contains(k, "sonnet"):
		return "General coding, refactors"
	case strings.Contains(k, "haiku"):
		return "Quick edits, linting"
	case strings.Contains(k, "coder"), strings.Contains(k, "devstral"), strings.Contains(k, "codestral"):
		return "Code generation, tool use"
	case strings.Contains(k, "o4"), strings.Contains(k, "o3"), strings.Contains(k, "reason"):
		return "Step-by-step reasoning"
	case strings.Contains(k, "flash"), strings.Contains(k, "mini"), strings.Contains(k, "lite"):
		return "Fast, simple tasks"
	case strings.Contains(k, "gemini"):
		return "Large files, long context"
	case strings.Contains(k, "gpt"):
		return "General coding, tests"
	case strings.Contains(k, "qwen"), strings.Contains(k, "deepseek"), strings.Contains(k, "kimi"),
		strings.Contains(k, "glm"), strings.Contains(k, "llama"), strings.Contains(k, "mistral"):
		return "Everyday coding"
	}
	return ""
}

// AdviceInfo is the payload for the "limit reached" screen: what was spent,
// what spent it, and what to do about it.
type AdviceInfo struct {
	SessionID string            `json:"session_id"`
	Cost      float64           `json:"cost"`
	Limit     float64           `json:"limit"`
	Mode      string            `json:"mode"`
	Drivers   []app.Alternative `json:"drivers"`
	Cheaper   []app.Alternative `json:"cheaper"`
	Current   string            `json:"current"`

	// Context for the decision: deciding whether to raise a limit needs to
	// know how much work is behind the number, not just the number.
	Tokens    int64   `json:"tokens"`
	Msgs      int64   `json:"msgs"`
	Rate      float64 `json:"rate"`
	Saved     float64 `json:"saved"`
	TripCost  float64 `json:"trip_cost"`
	TotalCost float64 `json:"total_cost"`
	Fraction  float64 `json:"fraction"`
	State     string  `json:"state"`
	Enforced  bool    `json:"enforced"`
	Grace     int     `json:"grace_remaining"`
	Since     string  `json:"since"`
}

// Advice returns the data behind the limit-reached screen.
func (s *Service) Advice() *AdviceInfo {
	if s.App == nil {
		return &AdviceInfo{}
	}
	sess := s.App.ActiveSessionBudget()
	current := s.App.Ledger.ActiveModel()
	drivers := s.App.TopSpenders(sess.SessionID, 3)
	if len(drivers) > 0 && drivers[0].Key != "" {
		// Base suggestions on what actually spent the money, not merely the
		// last model to reply.
		current = drivers[0].Key
	}
	// Per-session volume, so the spend has context.
	var tokens, msgs int64
	var since string
	if st, ok := s.App.Ledger.GetPerSessionSnapshot()[sess.SessionID]; ok {
		tokens = st.Tokens
		msgs = st.Msgs
		since = st.Last
	}
	var saved float64
	for _, d := range drivers {
		_ = d
	}
	tripCost, tripSaved := s.App.Ledger.GetTotals("TRIP")
	totalCost, _ := s.App.Ledger.GetTotals("TOTAL")
	saved = tripSaved

	return &AdviceInfo{
		SessionID: sess.SessionID,
		Cost:      sess.Cost,
		Limit:     s.App.Budget.Limit(),
		Mode:      string(s.App.Budget.Mode()),
		Drivers:   drivers,
		Cheaper:   s.App.CheaperThan(current),
		Current:   current,
		Tokens:    tokens,
		Msgs:      msgs,
		Rate:      s.App.BurnRate(),
		Saved:     saved,
		TripCost:  tripCost,
		TotalCost: totalCost,
		Fraction:  sess.Fraction,
		State:     sess.State,
		Enforced:  s.App.Budget.Blocks(),
		Grace:     sess.GraceRemaining,
		Since:     since,
	}
}

// ShowAlert brings the odometer forward as a dedicated budget window.
//
// Wails v2 is single-window (there is no runtime call to spawn another), so
// the Python behaviour of a separate always-on-top dialog is reproduced by
// resizing this window to dialog proportions, centring it, pinning it above
// other apps and un-minimising it. From the user's point of view a window
// appears in the middle of the screen demanding a decision, which is the
// point.
func (s *Service) ShowAlert() {
	if s.Ctx == nil {
		return
	}
	s.alertMu.Lock()
	if !s.alertActive {
		// Remember the layout so dismissing restores exactly what was there.
		s.alertPrevCompact = s.appCompact
		s.alertActive = true
	}
	s.alertMu.Unlock()

	s.appCompact = false
	runtime.WindowSetSize(s.Ctx, alertWidth, alertHeight)
	runtime.WindowCenter(s.Ctx)
	runtime.WindowSetAlwaysOnTop(s.Ctx, true)
	runtime.WindowUnminimise(s.Ctx)
	runtime.WindowShow(s.Ctx)
	s.refresh()
}

// DismissAlert restores the layout that was in use before the budget window
// took over.
func (s *Service) DismissAlert() {
	if s.Ctx == nil {
		return
	}
	s.alertMu.Lock()
	if !s.alertActive {
		s.alertMu.Unlock()
		return
	}
	s.alertActive = false
	prev := s.alertPrevCompact
	s.alertMu.Unlock()

	// The compact bar is always-on-top by design; the board is not.
	runtime.WindowSetAlwaysOnTop(s.Ctx, prev)
	s.appCompact = prev
	if s.App != nil {
		s.App.SetCompact(prev)
	}
	runtime.WindowSetSize(s.Ctx, windowWidth(prev), windowHeight(prev))
	s.applyPosition()
	s.refresh()
}

// Quit saves state and closes the app. A frameless window has no close
// button, and the original had a right-click "Quit"; without this the only
// way out is Task Manager.
func (s *Service) Quit() {
	if s.App != nil {
		s.App.SaveState()
	}
	if s.Ctx != nil {
		runtime.Quit(s.Ctx)
	}
}

// Minimise hides the window to keep it out of the way without losing state.
func (s *Service) Minimise() {
	if s.Ctx != nil {
		runtime.WindowMinimise(s.Ctx)
	}
}

// RaiseLimit doubles the limit.
func (s *Service) RaiseLimit() {
	if s.App != nil {
		s.App.RaiseLimit()
		s.refresh()
	}
}

// Disable turns enforcement off.
func (s *Service) Disable() {
	if s.App != nil {
		s.App.Disable()
		s.refresh()
	}
}

// ActiveSession returns the session with the most recent activity (the one a
// full-stop would abort). Empty if the ledger is empty.
func (s *Service) ActiveSession() string {
	if s.App == nil || s.App.Ledger == nil {
		return ""
	}
	return s.App.Ledger.ActiveSession()
}

// AbortCurrent cancels the most recently active session via the server's
// abort endpoint (the same path the Esc key / plugin uses).
func (s *Service) AbortCurrent() error {
	if s.App == nil {
		return fmt.Errorf("app not initialized")
	}
	sid := s.ActiveSession()
	if sid == "" {
		s.App.LogEvent("abort failed: no active session")
		return fmt.Errorf("no active session to abort")
	}
	// Prefer the HTTP endpoint when a server is actually reachable.
	if connected, _ := s.App.Connected(); connected {
		if ab, ok := s.App.Client.(interface{ Abort(string) error }); ok {
			s.App.LogEvent("abort via http session=%s", sid)
			if err := ab.Abort(sid); err == nil {
				s.App.LogEvent("abort succeeded session=%s", sid)
				return nil
			} else {
				s.App.LogEvent("abort http failed session=%s err=%v", sid, err)
			}
		}
	}

	// Otherwise ask the plugin. In the default in-process mode there is no
	// port to call, but the plugin holds a live OpenCode client and can abort
	// on our behalf.
	s.App.LogEvent("abort via plugin session=%s", sid)
	if err := s.App.Contract.RequestAbort(sid); err != nil {
		s.App.LogEvent("abort request failed session=%s err=%v", sid, err)
		return fmt.Errorf("could not reach OpenCode to abort: %w", err)
	}
	return nil
}

// AllowMoreSession grants the active session an override ceiling (cost must
// exceed its current baseline before the limit is reached again). It lets a
// user "let this one finish" from the UI.
func (s *Service) AllowMoreSession(cost, limit float64) error {
	if s.App == nil {
		return fmt.Errorf("app not initialized")
	}
	sid := s.ActiveSession()
	if sid == "" {
		return fmt.Errorf("no active session")
	}
	// The ceiling is absolute, so it must be anchored to what the session has
	// actually spent. The caller cannot know that figure, and passing 0 makes
	// the override a no-op precisely when the session is blocked.
	sess := s.App.ActiveSessionBudget()
	if cost <= 0 {
		cost = sess.Cost
	}
	if limit <= 0 {
		limit = s.App.Budget.Limit()
	}
	s.App.AllowMore(sid, cost, limit)
	s.refresh()
	return nil
}

// SetCompact toggles compact vs expanded UI layout and resizes the window.
func (s *Service) SetCompact(on bool) {
	if s.appCompact == on {
		// Nothing to do. The frontend re-asserts the current layout on every
		// init, and repeating the resize/reposition work makes the window
		// visibly jump.
		return
	}
	s.appCompact = on
	if s.App != nil {
		s.App.LogEvent("SetCompact on=%v", on)
		s.App.SetCompact(on)
	}
	if s.Ctx != nil {
		runtime.WindowSetSize(s.Ctx, windowWidth(on), windowHeight(on))
		if on {
			s.applyPosition()
		} else {
			runtime.WindowCenter(s.Ctx)
		}
	}
	s.refresh()
}

// UnlockSession grants the session enough headroom to continue past the cap.
//
// This does NOT switch the model. The odometer observes OpenCode; it has no
// channel to change a session's model, so the picker is guidance only and
// modelKey is recorded for the log. The method was previously named
// SwitchSessionModel, which promised a capability that was never implemented.
func (s *Service) UnlockSession(sessionID, modelKey string) error {
	if s.App == nil {
		return fmt.Errorf("app not initialized")
	}
	if sessionID == "" {
		sessionID = s.ActiveSession()
	}
	if sessionID == "" {
		return fmt.Errorf("no active session")
	}
	sess := s.App.ActiveSessionBudget()
	s.App.LogEvent("unlock_session session=%s noted_model=%s cost=%.6f", sessionID, modelKey, sess.Cost)
	s.App.AllowMore(sessionID, sess.Cost, s.App.Budget.Limit())
	s.refresh()
	return nil
}

// ExportCsv writes the ledger to a CSV file under the app data dir and
// returns the absolute path it was saved to.
func (s *Service) ExportCsv() string {
	dir := filepath.Join(dataDir(), "exports")
	_ = os.MkdirAll(dir, 0o755)
	path, err := s.App.ExportCSV(dir)
	if err != nil {
		return "error: " + err.Error()
	}
	return path
}

// applyPosition positions the window respecting floating coordinates or dock mode.
func (s *Service) applyPosition() {
	if s.Ctx == nil || s.App == nil {
		return
	}
	if !s.appCompact {
		runtime.WindowCenter(s.Ctx)
		return
	}
	if s.App.DockMode() {
		s.applyDock()
		return
	}
	pos := s.App.Pos()
	if len(pos) == 2 && pos[0] >= 0 && pos[1] >= 0 {
		// Saved coordinates come from WindowGetPosition, which is absolute,
		// whereas WindowSetPosition is work-area relative on Windows.
		// Convert, or a window saved near the taskbar creeps down the screen
		// on every restart.
		x, y := pos[0], pos[1]
		if wa, ok := primaryWorkArea(); ok {
			x -= wa.Left
			y -= wa.Top
		}
		runtime.WindowSetPosition(s.Ctx, x, y)
		return
	}
	// Default spawn position: bottom-center, just above taskbar
	s.applyDock()
}

// Dock repositions the frameless window according to the current dock key.
func (s *Service) applyDock() {
	if s.Ctx == nil {
		return
	}
	w, h := windowWidth(s.appCompact), windowHeight(s.appCompact)

	// Prefer the OS work area: it already excludes the taskbar, whatever its
	// height, edge or auto-hide setting. Only fall back to the raw screen size
	// (minus a conservative margin) when the platform cannot tell us.
	left, top := 0, 0
	availW, availH := 0, 0
	if wa, ok := primaryWorkArea(); ok {
		left, top = wa.Left, wa.Top
		availW, availH = wa.width(), wa.height()
	} else {
		sw, sh := s.screenW, s.screenH
		if s.Ctx != nil {
			if screens, err := runtime.ScreenGetAll(s.Ctx); err == nil && len(screens) > 0 {
				for _, sc := range screens {
					if sc.IsPrimary {
						sw, sh = sc.Size.Width, sc.Size.Height
						break
					}
				}
				if sw <= 0 {
					sw, sh = screens[0].Size.Width, screens[0].Size.Height
				}
			}
		}
		if sw <= 0 || sh <= 0 {
			sw, sh = 1536, 864
		}
		const panel = 48 // approximate taskbar height when unknown
		availW, availH = sw, sh-panel
	}

	// Wails' WindowSetPosition is WORK-AREA RELATIVE on Windows: it adds the
	// monitor's work rect origin internally. Adding `left`/`top` here too
	// would double-count the offset, so coordinates below are relative to the
	// work area, with (0,0) meaning its top-left corner.
	_ = left
	_ = top

	const margin = 14
	// Sit just above the taskbar rather than flush against it.
	const bottomGap = 4

	bottomY := availH - h - bottomGap
	centerX := (availW - w) / 2

	x, y := centerX, bottomY
	switch s.App.Dock() {
	case "top-right":
		x, y = availW-w-margin, margin
	case "top-left":
		x, y = margin, margin
	case "top-center":
		x, y = centerX, margin
	case "bottom-left":
		x, y = margin, bottomY
	case "bottom-right":
		x, y = availW-w-margin, bottomY
	default: // bottom-center
		x, y = centerX, bottomY
	}

	// Clamp inside the usable area.
	if x < 0 {
		x = 0
	}
	if maxX := availW - w; x > maxX {
		x = maxX
	}
	if y < 0 {
		y = 0
	}
	if maxY := availH - h; y > maxY {
		y = maxY
	}

	runtime.WindowSetPosition(s.Ctx, x, y)

	// Verify and correct.
	//
	// WindowSetPosition is work-area relative and, on a scaled display, the
	// window does not land exactly where asked: Wails applies DPI scaling to
	// the size but not the coordinates, and the frameless window carries an
	// invisible resize border. Rather than model those quirks (they differ by
	// Windows version and DPI), measure the real result and apply the
	// difference. One corrective move is imperceptible and self-correcting on
	// any monitor.
	wantLeft, wantTop := left+x, top+y
	if gotLeft, gotTop, ok := s.windowRect(); ok {
		dx, dy := wantLeft-gotLeft, wantTop-gotTop
		if dx != 0 || dy != 0 {
			runtime.WindowSetPosition(s.Ctx, x+dx, y+dy)
		}
	}
}

// ---------------- ingest / refresh loop ----------------

// Start runs the SSE ingest loop in the background, pushing a refresh event to
// the frontend whenever the ledger changes.
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	a := s.App

	// Restore the saved layout before positioning. The window is created at
	// the expanded size, so without this the first paint is a centred board
	// even when the user left it as the docked bar.
	s.appCompact = a.Compact()
	a.LogEvent("layout restore compact=%v dock=%s docked=%v", s.appCompact, a.Dock(), a.DockMode())
	if s.Ctx != nil {
		runtime.WindowSetSize(s.Ctx,
			windowWidth(s.appCompact), windowHeight(s.appCompact))
	}

	// Push initial cached snapshot immediately
	s.refresh()
	s.applyPosition()

	// Initial sync in background so startup doesn't block window rendering
	go func() {
		if !a.Seeded() {
			_ = a.SeedHistory()
		} else {
			_, _ = a.SyncHistory(false)
		}
		s.refresh()
	}()

	go func() {
		for {
			select {
			case <-s.stopCh:
				return
			case <-ctx.Done():
				return
			default:
			}
			connected, _ := a.Connected()
			if connected {
				s.resetBackoff()
			}
			if err := a.RunContext(ctx, func(_ bool) { s.refresh() }); err != nil {
				if ctx.Err() == nil {
					// A closed OpenCode produces this every few seconds; log
					// it at most once a minute so the log stays readable and
					// bounded. The UI shows the disconnected state live.
					a.LogThrottled("stream-disconnect", time.Minute,
						"stream disconnected: %v", err)
					log.Printf("stream disconnected: %v", err)
				}
			}
			s.refresh()
			select {
			case <-s.stopCh:
				return
			case <-ctx.Done():
				return
			case <-time.After(s.backoff()):
			}
			// The server may have restarted on a different port.
			a.Rediscover()
			// Catch up any turns completed during disconnect
			_, _ = a.SyncHistory(false)
			s.refresh()
		}
	}()

	// 1-second ticker for live burn-rate decay, pulsing dot, and grace absorption (mirrors Python tick)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.Poll()
				s.refresh()
			}
		}
	}()

	// Keep the price table current. Rates drift (~2% of entries changed over
	// four days in a sample), and a hand-regenerated table goes stale
	// silently. Runs off the UI path so a slow or absent network never delays
	// startup, and failure leaves the existing table in place.
	go func() {
		select {
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Second):
		}
		a.MaybeRefreshPrices(24 * time.Hour)

		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.MaybeRefreshPrices(24 * time.Hour)
			}
		}
	}()
}

// refresh emits the latest snapshot to the frontend over a Wails event.
func (s *Service) refresh() {
	if s.Ctx != nil {
		runtime.EventsEmit(s.Ctx, "refresh", s.snapshot())
	}
}

// backoff returns the next reconnect delay, growing from 3s to 30s while the
// server stays unreachable. A fixed 3s retry against a closed OpenCode is what
// produced thousands of identical failures per hour.
func (s *Service) backoff() time.Duration {
	s.backoffMu.Lock()
	defer s.backoffMu.Unlock()
	if s.backoffCur < 3*time.Second {
		s.backoffCur = 3 * time.Second
	} else {
		s.backoffCur *= 2
		if s.backoffCur > 30*time.Second {
			s.backoffCur = 30 * time.Second
		}
	}
	return s.backoffCur
}

// resetBackoff returns the reconnect delay to its floor after a good connection.
func (s *Service) resetBackoff() {
	s.backoffMu.Lock()
	s.backoffCur = 0
	s.backoffMu.Unlock()
}

// ---------------- helpers ----------------

// modelName returns the bare model name (everything after the last '/'), so a
// value of "opencode/deepseek-v4-flash" displays as "deepseek-v4-flash".
func modelName(m string) string {
	if m == "" {
		return "idle"
	}
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	if m == "" {
		return "idle"
	}
	return m
}

func totalTokens(ld *ledger.Store) int64 {
	var n int64
	for _, m := range ld.PerModel {
		n += m.TokensIn + m.TokensOut
	}
	return n
}

func flip(v string) string {
	if v == "TRIP" {
		return "TOTAL"
	}
	return "TRIP"
}

func windowWidth(compact bool) int {
	if compact {
		return 360
	}
	return 620
}

func windowHeight(compact bool) int {
	if compact {
		return 46
	}
	return 580
}

// dataDir mirrors the main package's data location so exports land beside the
// rest of the app's state.
func dataDir() string {
	if d := os.Getenv("OPENCODE_ODOMETER_DIR"); d != "" {
		return d
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.Getenv("XDG_DATA_HOME")
	}
	if base == "" {
		home, _ := os.UserHomeDir()
		base = home
	}
	return filepath.Join(base, "OpenCodeOdometer")
}
