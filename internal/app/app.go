// Package app orchestrates the Odometer core: it owns the priced ledger, the
// per-session budget, and the budget.json contract, and applies live messages
// from the OpenCode client. It is UI-agnostic so it can be unit tested
// without a display.
package app

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/budget"
	"github.com/vivek9102/opencode-odometer/internal/ledger"
	"github.com/vivek9102/opencode-odometer/internal/opencode"
	"github.com/vivek9102/opencode-odometer/internal/plugin"
	"github.com/vivek9102/opencode-odometer/internal/prices"
	"github.com/vivek9102/opencode-odometer/internal/spool"
)

// RatePoint records spend delta at a point in time for live burn rate calculation.
type RatePoint struct {
	Time time.Time
	Cost float64
}

// State is the persisted odometer_state.json payload: the ledger plus the
// budget bookkeeping.
type State struct {
	Ledger          *ledger.Store      `json:"ledger"`
	BudgetBaselines map[string]float64 `json:"budget_baselines"`
	BudgetAllow     map[string]float64 `json:"budget_allow"`
	GraceUsed       map[string]int     `json:"grace_used"`
	SeenModels      map[string]bool    `json:"seen_models,omitempty"`
	// IgnoredPrices are models the user chose not to price. Persisted because
	// pricing re-flags an unknown model on every message, so a non-persistent
	// dismissal would reappear on the next turn.
	IgnoredPrices map[string]bool `json:"ignored_prices,omitempty"`
	Seeded        bool            `json:"seeded"`
	// SpoolOffset is how far the plugin event spool has been consumed, so a
	// restart resumes instead of replaying the whole file.
	SpoolOffset int64  `json:"spool_offset,omitempty"`
	View        string `json:"view"`
	Compact     bool   `json:"compact"`
	Dock        string `json:"dock"`
	Docked      bool   `json:"docked"`
	Pos         []int  `json:"pos,omitempty"`
	LastUpdated int64  `json:"last_updated"`
}

// Config carries file paths into the app.
type Config struct {
	PricesFile  string
	OverlayFile string
	StateFile   string
	BudgetFile  string
	GraceFile   string
	LogFile     string
	MaxMessages int
	OpenCodeURL string
}

// Client is the subset of the OpenCode client the app needs. It is an
// interface so tests can substitute a stub.
type Client interface {
	Session() ([]opencode.Session, error)
	Messages(sessionID string) ([]opencode.Message, error)
	Subscribe(handler func(opencode.Event) error) error
}

// App ties together pricing, the ledger, the budget and the plugin contract.
type App struct {
	Prices   *prices.Book
	Budget   *budget.Budget
	Ledger   *ledger.Store
	Contract *plugin.Contract
	Client   Client
	// Spool reads events the plugin appends from inside OpenCode. It is the
	// ingest path that works when OpenCode serves its API in-process and
	// binds no TCP port, which is the default.
	Spool *spool.Reader
	cfg   Config

	mu           sync.RWMutex
	syncMu       sync.Mutex
	state        State
	graceUsed    map[string]int
	lastActivity time.Time
	connected    bool
	serverURL    string
	spoolSeen    time.Time

	logMu       sync.Mutex
	throttleMu  sync.Mutex
	logThrottle map[string]time.Time
	tripHealed  bool
	// loadFailed blocks persistence when the existing state could not be
	// read, so a transient failure cannot erase the ledger.
	loadFailed bool

	sessionUpdatedMu sync.Mutex
	sessionUpdated   map[string]int64

	rateMu     sync.Mutex
	rateWindow []RatePoint
}

// New loads prices, state and budget config.
func New(cfg Config) (*App, error) {
	a := &App{cfg: cfg}
	if cfg.MaxMessages <= 0 {
		cfg.MaxMessages = 20000
	}
	a.cfg = cfg

	pb, err := prices.New(cfg.PricesFile, cfg.OverlayFile)
	if err != nil {
		// A missing/corrupt price table must not brick the app; start empty
		// and warn (mirrors the original, which also keeps running).
		log.Printf("[prices] %v (continuing with empty table)", err)
		pb = &prices.Book{
			Models:         map[string]prices.Rate{},
			ReferenceModel: prices.FallbackReference,
			Unknown:        map[string]bool{},
		}
	}
	a.Prices = pb

	b := budget.New()
	b.Load(cfg.BudgetFile)
	a.Budget = b

	a.Contract = plugin.New(cfg.BudgetFile, cfg.GraceFile)
	a.Client = opencode.New(cfg.OpenCodeURL)
	if cfg.StateFile != "" {
		a.Spool = newSpool(filepath.Dir(cfg.StateFile))
	}
	a.logThrottle = map[string]time.Time{}
	if c, ok := a.Client.(interface{ BaseURL() string }); ok {
		a.serverURL = c.BaseURL()
	}

	a.Ledger = ledger.New()
	a.sessionUpdated = make(map[string]int64)
	a.state = State{
		Ledger:          a.Ledger,
		BudgetBaselines: map[string]float64{},
		BudgetAllow:     map[string]float64{},
		GraceUsed:       map[string]int{},
		SeenModels:      map[string]bool{},
		IgnoredPrices:   map[string]bool{},
		View:            "TRIP",
		Compact:         true,
		Dock:            "bottom-center",
		// Docked by default: a first run must land just above the taskbar at
		// bottom-centre, not float at an arbitrary spot.
		Docked: true,
	}
	a.graceUsed = a.state.GraceUsed
	a.loadState()
	// Repair records that an older classifier charged even though their model
	// key is now recognized as free. Do not broadly reprice paid history here.
	a.reclassifyFreeModels()
	a.healTripBaseline()

	// Enforcement always starts OFF.
	//
	// The limit is a deliberate act, not a background default: leaving it
	// armed across restarts meant a session could be blocked by a cap the
	// user had forgotten setting, with no obvious way back. Ticking the box
	// starts a fresh count from zero (see SetEnabled), which makes the
	// control mean exactly what it says.
	a.Budget.Cfg.Enabled = false
	a.mu.Lock()
	a.state.BudgetBaselines = map[string]float64{}
	a.state.BudgetAllow = map[string]float64{}
	a.graceUsed = map[string]int{}
	a.state.GraceUsed = a.graceUsed
	a.mu.Unlock()

	// A repaired baseline must reach disk immediately: otherwise the next
	// start reloads the broken value and TRIP reads zero again until some
	// unrelated change happens to trigger a save.
	a.SaveState()

	// Publish the disarmed state now. budget.json is the plugin's only source
	// of truth and still holds the previous run's settings, so until it is
	// rewritten the plugin would keep enforcing a limit the UI shows as off.
	// PublishBudget skips unseeded ledgers, so write the header directly.
	if err := a.Contract.WriteBudget(plugin.BudgetFile{
		Enabled:           false,
		SessionLimitUSD:   a.Budget.Limit(),
		WarnAtPercent:     a.Budget.Cfg.WarnAtPercent,
		BlockWhenExceeded: a.Budget.Cfg.BlockWhenExceeded,
		Mode:              string(a.Budget.Mode()),
		HardStopAt:        a.Budget.HardStopAt(),
		GraceTurns:        a.Budget.GraceTurns(),
		Sessions:          map[string]plugin.Session{},
	}); err != nil {
		log.Printf("[budget] initial publish: %v", err)
	}

	return a, nil
}

// loadState restores the ledger and budget bookkeeping from disk.
//
// Two on-disk schemas are accepted: the current nested form
// ("ledger": { ... }) and the legacy flat form written by earlier builds
// (ledger fields promoted to the top level, alongside the state fields).
// The legacy form is migrated transparently so historical data survives.
func (a *App) loadState() {
	raw, err := os.ReadFile(a.cfg.StateFile)
	if err != nil {
		if !os.IsNotExist(err) {
			// The file exists but could not be read (locked, permissions, a
			// transient IO error). Continuing would start from an empty
			// ledger and then overwrite the real one on the next save, which
			// silently destroys the entire spend history. Refuse instead.
			a.loadFailed = true
			log.Printf("[state] cannot read %s: %v (refusing to overwrite)",
				a.cfg.StateFile, err)
		}
		return
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		// Same reasoning: a corrupt or half-written file must not be
		// replaced with an empty one. Keep a copy so the data can be
		// recovered by hand.
		a.loadFailed = true
		log.Printf("[state] parse %s: %v (refusing to overwrite)", a.cfg.StateFile, err)
		_ = os.WriteFile(a.cfg.StateFile+".unreadable", raw, 0o644)
		return
	}

	// Nested (current) schema: st.Ledger is populated.
	if st.Ledger != nil && st.Ledger.Messages != nil && len(st.Ledger.Messages) > 0 {
		a.applyLedger(st.Ledger)
	} else {
		// Legacy flat schema: unmarshal the whole file as a ledger (its
		// fields sit at the top level) and pull the state fields off it.
		var flat struct {
			ledger.Store
			BudgetBaselines map[string]float64 `json:"budget_baselines"`
			BudgetAllow     map[string]float64 `json:"budget_allow"`
			GraceUsed       map[string]int     `json:"grace_used"`
			Seeded          bool               `json:"seeded"`
			View            string             `json:"view"`
			Compact         bool               `json:"compact"`
			Dock            string             `json:"dock"`
			Pos             []int              `json:"pos,omitempty"`
		}
		if err := json.Unmarshal(raw, &flat); err == nil && len(flat.Messages) > 0 {
			a.Ledger.Lock()
			a.Ledger.Messages = flat.Messages
			if a.Ledger.Messages == nil {
				a.Ledger.Messages = map[string]ledger.Record{}
			}
			for k, rec := range a.Ledger.Messages {
				if rec.MID == "" {
					rec.MID = k
					a.Ledger.Messages[k] = rec
				}
			}
			a.Ledger.TripBase = flat.TripBase
			a.Ledger.TripSavedBase = flat.TripSavedBase
			a.Ledger.PrunedCost = flat.PrunedCost
			a.Ledger.PrunedSaved = flat.PrunedSaved
			a.Ledger.Unlock()

			st.Seeded = flat.Seeded
			st.BudgetBaselines = flat.BudgetBaselines
			st.BudgetAllow = flat.BudgetAllow
			st.GraceUsed = flat.GraceUsed
			st.View = flat.View
			st.Compact = flat.Compact
			st.Dock = flat.Dock
			st.Pos = flat.Pos
		}
	}

	a.mu.Lock()
	a.state.Seeded = st.Seeded
	a.state.BudgetBaselines = st.BudgetBaselines
	if a.state.BudgetBaselines == nil {
		a.state.BudgetBaselines = map[string]float64{}
	}
	a.state.BudgetAllow = st.BudgetAllow
	if a.state.BudgetAllow == nil {
		a.state.BudgetAllow = map[string]float64{}
	}
	a.state.GraceUsed = st.GraceUsed
	if a.state.GraceUsed == nil {
		a.state.GraceUsed = map[string]int{}
	}
	a.state.IgnoredPrices = st.IgnoredPrices
	if a.state.IgnoredPrices == nil {
		a.state.IgnoredPrices = map[string]bool{}
	}
	a.state.SeenModels = st.SeenModels
	if a.state.SeenModels == nil {
		a.state.SeenModels = map[string]bool{}
	}
	a.state.SpoolOffset = st.SpoolOffset
	if a.Spool != nil {
		a.Spool.SetOffset(st.SpoolOffset)
	}
	a.graceUsed = a.state.GraceUsed
	a.state.View = st.View
	a.state.Compact = st.Compact
	a.state.Dock = st.Dock
	if a.state.Dock == "" {
		a.state.Dock = "bottom-center"
	}
	a.state.Docked = st.Docked
	a.state.Pos = st.Pos
	// A floating position is only meaningful if one was actually saved.
	// Older builds could record docked=false with no usable coordinates (or
	// coordinates from a monitor that is no longer attached), which left the
	// bar parked wherever it last happened to be instead of docking.
	if !a.state.Docked && len(a.state.Pos) != 2 {
		a.state.Docked = true
	}
	a.mu.Unlock()

	a.Ledger.Recompute()
	a.healTripBaseline()
}

// healTripBaseline repairs a baseline left slightly above the recomputed total
// by an older build.
//
// Totals used to be summed in map order, so each run produced a marginally
// different figure. A baseline written by one run could exceed the total
// computed by the next, which pinned TRIP at zero and made live spend appear
// to vanish. Clamping the stored baseline back to the total restores counting
// without touching lifetime figures.
func (a *App) healTripBaseline() {
	a.Ledger.Lock()
	defer a.Ledger.Unlock()

	healed := false
	if a.Ledger.TripBase > a.Ledger.TotalCost {
		a.Ledger.TripBase = a.Ledger.TotalCost
		healed = true
	}
	if a.Ledger.TripSavedBase > a.Ledger.TotalSaved {
		a.Ledger.TripSavedBase = a.Ledger.TotalSaved
		healed = true
	}
	if healed {
		a.Ledger.RecomputeLocked()
		log.Printf("[state] trip baseline exceeded total; clamped")
	}
	a.tripHealed = healed
}

func (a *App) applyLedger(st *ledger.Store) {
	a.Ledger.Lock()
	defer a.Ledger.Unlock()

	a.Ledger.Messages = st.Messages
	if a.Ledger.Messages == nil {
		a.Ledger.Messages = map[string]ledger.Record{}
	}
	for k, rec := range a.Ledger.Messages {
		if rec.MID == "" {
			rec.MID = k
			a.Ledger.Messages[k] = rec
		}
	}
	a.Ledger.TripBase = st.TripBase
	a.Ledger.TripSavedBase = st.TripSavedBase
	a.Ledger.PrunedCost = st.PrunedCost
	a.Ledger.PrunedSaved = st.PrunedSaved
}

// SaveState persists the ledger and budget bookkeeping atomically.
//
// It refuses to write if the existing state could not be read at startup.
// Saving in that situation replaces a full history with whatever this run
// happened to collect, which is how a 6906-message ledger became 20.
func (a *App) SaveState() {
	a.mu.RLock()
	blocked := a.loadFailed
	a.mu.RUnlock()
	if blocked {
		a.LogThrottled("save-blocked", time.Minute,
			"state save skipped: existing state could not be loaded")
		return
	}

	a.Ledger.Prune(a.cfg.MaxMessages)

	a.mu.Lock()
	a.state.BudgetBaselines = a.baselinesLocked()
	a.state.BudgetAllow = a.allowLocked()
	a.state.GraceUsed = a.graceUsedLocked()
	stCopy := a.state
	stCopy.Ledger = a.Ledger.Copy()
	a.mu.Unlock()

	raw, err := json.MarshalIndent(stCopy, "", "  ")
	if err != nil {
		log.Printf("[state] marshal: %v", err)
		return
	}
	// Never let a save shrink the ledger dramatically without keeping the
	// previous file. A large drop is legitimate after a prune or a manual
	// reset, but it is also exactly what a bug looks like, and the cost of a
	// spare copy is trivial next to losing months of spend history.
	if prev, err := os.Stat(a.cfg.StateFile); err == nil {
		if int64(len(raw)) < prev.Size()/2 {
			_ = copyFile(a.cfg.StateFile, a.cfg.StateFile+".prev")
			log.Printf("[state] size dropped %d -> %d bytes; kept %s.prev",
				prev.Size(), len(raw), a.cfg.StateFile)
		}
	}

	tmp := a.cfg.StateFile + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		log.Printf("[state] write: %v", err)
		return
	}
	if err := os.Rename(tmp, a.cfg.StateFile); err != nil {
		log.Printf("[state] rename: %v", err)
	}
}

// copyFile duplicates a file, used to retain a recoverable copy of state.
func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o644)
}

// maxPublishedSessions caps how many sessions appear in budget.json. The
// plugin re-reads the file on every turn and only ever looks up the current
// session, so an unbounded ledger is pure cost.
const maxPublishedSessions = 60

// maxLogBytes caps the ingest log. It used to grow without limit — a machine
// left with OpenCode closed wrote a reconnect failure every 3s and reached
// 7.5 MB. On overflow the log is rotated to a single .1 backup.
const maxLogBytes = 5 << 20 // 5 MiB

// LogEvent appends a line to the ingest log (opencode_odometer_events.log
// in the data dir) so a user can see exactly which events were received and
// how each was priced. Enabled only when Config.LogFile is set.
func (a *App) LogEvent(format string, args ...any) {
	if a.cfg.LogFile == "" {
		return
	}
	a.logMu.Lock()
	defer a.logMu.Unlock()

	if fi, err := os.Stat(a.cfg.LogFile); err == nil && fi.Size() > maxLogBytes {
		_ = os.Remove(a.cfg.LogFile + ".1")
		_ = os.Rename(a.cfg.LogFile, a.cfg.LogFile+".1")
	}

	f, err := os.OpenFile(a.cfg.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

// LogThrottled logs at most once per interval for a given key. Reconnect
// failures repeat every few seconds for as long as OpenCode is closed, which
// is normal rather than noteworthy; logging each one buries the events that
// matter and is what let the log reach 7.5 MB.
func (a *App) LogThrottled(key string, interval time.Duration, format string, args ...any) {
	a.throttleMu.Lock()
	last, seen := a.logThrottle[key]
	now := time.Now()
	if seen && now.Sub(last) < interval {
		a.throttleMu.Unlock()
		return
	}
	if a.logThrottle == nil {
		a.logThrottle = map[string]time.Time{}
	}
	a.logThrottle[key] = now
	a.throttleMu.Unlock()

	a.LogEvent(format, args...)
}

// Connected reports whether the OpenCode event stream is currently attached,
// plus the address in use. The UI shows this so an unreachable server is
// visible instead of looking like "no spend".
func (a *App) Connected() (bool, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected, a.serverURL
}

// SetConnected records stream connectivity and the resolved server address.
func (a *App) SetConnected(on bool, url string) {
	a.mu.Lock()
	a.connected = on
	if url != "" {
		a.serverURL = url
	}
	a.mu.Unlock()
}

func (a *App) logEvent(format string, args ...any) {
	a.LogEvent(format, args...)
}

// ApplyMessage prices a live message and upserts it into the ledger, then
// recomputes and republishes the budget. Returns whether the ledger changed.
func (a *App) ApplyMessage(m opencode.Message) bool {
	key := prices.NormalizeKey(m.Provider, m.Model)
	prov := m.Provider
	mod := m.Model
	if strings.Contains(key, "/") {
		parts := strings.SplitN(key, "/", 2)
		prov = parts[0]
		mod = parts[1]
	}

	quote := a.PreviewPrice(key, m.Tokens.Input, m.Tokens.Output, m.Tokens.Cache.Read, m.Tokens.Cache.Write, m.Cost)

	// OpenCode and the plugin replay completed messages during backfill. If the
	// token totals and reported cost are unchanged, retain the price decision
	// captured on the original record. Otherwise changing a local price today
	// would silently rewrite yesterday's spend the next time that message was
	// replayed. A genuinely updated/in-progress message still uses current
	// pricing, and a newly reported non-zero event cost is still accepted.
	a.Ledger.RLock()
	previous, existed := a.Ledger.Messages[m.ID]
	a.Ledger.RUnlock()
	preserveHistoricalPrice := existed && previous.TokensIn == m.Tokens.Input &&
		previous.TokensOut == m.Tokens.Output &&
		previous.CacheRead == m.Tokens.Cache.Read &&
		previous.CacheWrite == m.Tokens.Cache.Write &&
		previous.ReportedCost == m.Cost
	if preserveHistoricalPrice {
		quote.Cost = previous.Cost
		quote.Source = previous.CostSource
		quote.Free = previous.Free
		quote.Unknown = previous.Unknown
		quote.Estimated = previous.Estimated
	}
	free := quote.Free
	cost := quote.Cost
	saved := 0.0
	if free {
		saved = a.Prices.ShadowCost(key, m.Tokens.Input, m.Tokens.Output, m.Tokens.Cache.Read, m.Tokens.Cache.Write)
	}
	if preserveHistoricalPrice {
		saved = previous.Saved
	}

	a.Ledger.RLock()
	prevCost := 0.0
	if prev, ok := a.Ledger.Messages[m.ID]; ok {
		prevCost = prev.Cost
	}
	a.Ledger.RUnlock()

	gained := cost - prevCost
	if gained > 0 && a.Seeded() {
		a.rateMu.Lock()
		a.rateWindow = append(a.rateWindow, RatePoint{Time: time.Now(), Cost: gained})
		a.rateMu.Unlock()
	}

	ts := time.Now().Format("2006-01-02 15:04:05")
	if m.TimeCreated > 0 {
		sec := m.TimeCreated / 1000
		nsec := (m.TimeCreated % 1000) * 1e6
		ts = time.Unix(sec, nsec).Format("2006-01-02 15:04:05")
	}

	rec := ledger.Record{
		MID:          m.ID,
		SessionID:    m.SessionID,
		Provider:     prov,
		Model:        mod,
		Free:         free,
		TokensIn:     m.Tokens.Input,
		TokensOut:    m.Tokens.Output,
		Reasoning:    m.Tokens.Reasoning,
		CacheRead:    m.Tokens.Cache.Read,
		CacheWrite:   m.Tokens.Cache.Write,
		Cost:         cost,
		Saved:        saved,
		Unknown:      quote.Unknown,
		Estimated:    quote.Estimated,
		CostSource:   quote.Source,
		ReportedCost: quote.ReportedCost,
		Timestamp:    ts,
		Finish:       m.Finish,
	}

	a.mu.Lock()
	if a.state.SeenModels == nil {
		a.state.SeenModels = map[string]bool{}
	}
	a.state.SeenModels[key] = true
	a.mu.Unlock()

	changed := a.Ledger.Put(rec)
	if changed {
		// Log only a materially changed record. Polling re-reads every message
		// each tick, so logging identical records would bury real activity.
		a.logEvent("price msg=%s key=%q source=%q unknown=%v free=%v in=%d out=%d cache=%d cost=%.6f saved=%.6f",
			m.ID, key, quote.Source, quote.Unknown, free,
			m.Tokens.Input, m.Tokens.Output,
			m.Tokens.Cache.Read+m.Tokens.Cache.Write, cost, saved)

		a.mu.Lock()
		a.lastActivity = time.Now()
		a.mu.Unlock()
	}
	return changed
}

// PendingPriceSuggestions returns prices awaiting confirmation and saved local
// prices, so users can revisit an unknown-provider price at any time.
func (a *App) PendingPriceSuggestions() []prices.PriceSuggestion {
	a.mu.RLock()
	var seen []string
	for k := range a.state.SeenModels {
		if a.state.IgnoredPrices[k] {
			continue
		}
		seen = append(seen, k)
	}
	a.mu.RUnlock()
	return a.Prices.PendingSuggestions(seen)
}

// IgnorePrice stops asking about a model. An unknown stays at $0; a borrowed
// cross-provider estimate continues to be shown as estimated.
func (a *App) IgnorePrice(key string) {
	a.mu.Lock()
	if a.state.IgnoredPrices == nil {
		a.state.IgnoredPrices = map[string]bool{}
	}
	a.state.IgnoredPrices[key] = true
	a.mu.Unlock()
	a.Prices.ForgetUnknown(key)
	a.SaveState()
}

// SaveOverride persists an explicit user price for future messages. Completed
// ledger records are immutable: a later price correction must not rewrite what
// the odometer showed at the time.
func (a *App) SaveOverride(key string, rate prices.Rate) error {
	overlay := a.cfg.OverlayFile
	if overlay == "" {
		overlay = "prices.local.json"
	}
	if err := a.Prices.SaveOverride(overlay, key, rate); err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.state.IgnoredPrices, key)
	a.mu.Unlock()
	a.SaveState()
	return nil
}

// LastActivity records the most recent message time for the UI.
func (a *App) LastActivity() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastActivity
}

// PublishBudget writes the verdict the plugin enforces on. Only recently
// active sessions are published to keep the file bounded.
func (a *App) PublishBudget() {
	a.mu.Lock()
	defer a.mu.Unlock()

	// First fold in any grace claims the plugin recorded.
	if _, err := a.Contract.AbsorbGraceClaims(a.graceUsed); err == nil {
	}
	if !a.state.Seeded {
		return
	}

	doc := plugin.BudgetFile{
		Enabled:           a.Budget.Enabled(),
		SessionLimitUSD:   a.Budget.Limit(),
		WarnAtPercent:     a.Budget.Cfg.WarnAtPercent,
		BlockWhenExceeded: a.Budget.Cfg.BlockWhenExceeded,
		Mode:              string(a.Budget.Mode()),
		HardStopAt:        a.Budget.HardStopAt(),
		GraceTurns:        a.Budget.GraceTurns(),
		Sessions:          map[string]plugin.Session{},
	}

	baselines := a.state.BudgetBaselines
	allow := a.state.BudgetAllow
	hardX := a.Budget.HardStopAt()

	sessions := a.Ledger.GetPerSessionSnapshot()

	// Drop sessions that can never be blocked. Abandoned tabs accumulate
	// indefinitely (a real run showed 59 of 60 published sessions sitting at
	// zero), which bloats the contract the plugin re-reads on every turn and
	// buries the one session that matters.
	//
	// A zero-cost session is only droppable if it also carries no enforcement
	// state: an override or a spent grace turn must survive, or removing the
	// entry would silently hand the session a clean slate.
	live := make(map[string]ledger.SessionStat, len(sessions))
	for sid, st := range sessions {
		if st.Cost > 0 || allow[sid] > 0 || a.graceUsed[sid] > 0 || baselines[sid] > 0 {
			live[sid] = st
		}
	}
	sessions = live

	// Keep budget.json bounded: publish only the most recently active
	// sessions, newest first.
	if len(sessions) > maxPublishedSessions {
		type entry struct {
			id   string
			last string
		}
		ordered := make([]entry, 0, len(sessions))
		for sid, st := range sessions {
			ordered = append(ordered, entry{sid, st.Last})
		}
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].last != ordered[j].last {
				return ordered[i].last > ordered[j].last // newest first
			}
			return ordered[i].id < ordered[j].id
		})
		keep := make(map[string]ledger.SessionStat, maxPublishedSessions)
		for _, e := range ordered[:maxPublishedSessions] {
			keep[e.id] = sessions[e.id]
		}
		sessions = keep
	}

	for sid, st := range sessions {
		s := &budget.Session{
			Budget:    a.Budget,
			Baseline:  baselines[sid],
			Allow:     allow[sid],
			GraceUsed: a.graceUsed[sid],
		}
		eff := s.EffCost(st.Cost)
		state, frac := s.State(eff)
		remaining := s.RemainingGrace(eff)

		doc.Sessions[sid] = plugin.Session{
			Cost:           round6(eff),
			State:          string(state),
			Fraction:       round4(frac),
			Limit:          a.Budget.Limit(),
			Mode:           string(a.Budget.Mode()),
			GraceRemaining: remaining,
			HardStopAt:     hardX,
			PastHardStop:   s.PastHardStop(eff),
			Enforced:       a.Budget.Blocks(),
		}
	}

	if err := a.Contract.WriteBudget(doc); err != nil {
		log.Printf("[budget] save: %v", err)
	}
}

// SessionBudget is the budget verdict for one session, as both the UI and the
// plugin see it. Deriving the UI from the same computation as the published
// contract is what keeps the bar honest.
type SessionBudget struct {
	SessionID      string  `json:"session_id"`
	Cost           float64 `json:"cost"`
	State          string  `json:"state"`
	Fraction       float64 `json:"fraction"`
	GraceRemaining int     `json:"grace_remaining"`
	PastHardStop   bool    `json:"past_hard_stop"`
}

// ActiveSessionBudget returns the verdict for the most recently active
// session — the one a limit would actually block.
func (a *App) ActiveSessionBudget() SessionBudget {
	sid := a.Ledger.ActiveSession()
	if sid == "" {
		return SessionBudget{State: "ok"}
	}
	sessions := a.Ledger.GetPerSessionSnapshot()
	st, ok := sessions[sid]
	if !ok {
		return SessionBudget{SessionID: sid, State: "ok"}
	}

	a.mu.RLock()
	s := &budget.Session{
		Budget:    a.Budget,
		Baseline:  a.state.BudgetBaselines[sid],
		Allow:     a.state.BudgetAllow[sid],
		GraceUsed: a.graceUsed[sid],
	}
	a.mu.RUnlock()

	eff := s.EffCost(st.Cost)
	state, frac := s.State(eff)

	// State is a pure measurement and stays meaningful when the limit is off
	// (the UI shows what *would* be counted). But a disabled limit must never
	// present as a breach: that is what drives the alert window and the
	// blocked-turn messaging, so reporting "over" here would raise a dialog
	// about a limit nobody is enforcing.
	if !a.Budget.Enabled() {
		state = "ok"
	}

	return SessionBudget{
		SessionID:      sid,
		Cost:           eff,
		State:          string(state),
		Fraction:       frac,
		GraceRemaining: s.RemainingGrace(eff),
		PastHardStop:   s.PastHardStop(eff),
	}
}

// Rebase snapshots current per-session spend so enabling the limit (or
// changing it) never retroactively blocks a session, and resets grace.
func (a *App) Rebase(clearOverrides bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	baselines := map[string]float64{}
	sessions := a.Ledger.GetPerSessionSnapshot()
	for sid, st := range sessions {
		baselines[sid] = st.Cost
	}
	a.state.BudgetBaselines = baselines
	a.graceUsed = map[string]int{}
	a.state.GraceUsed = a.graceUsed
	if clearOverrides {
		a.state.BudgetAllow = map[string]float64{}
	}
}

// AllowMore grants a session an override ceiling past its current spend.
//
// The ceiling is absolute: budget.Session.State clears a session back to "ok"
// only while Allow >= effCost. Callers must therefore pass the session's
// current effective spend as cost — passing 0 makes the override a no-op
// exactly when it is needed, since a blocked session already has
// effCost >= limit.
func (a *App) AllowMore(sid string, cost float64, limit float64) {
	a.mu.Lock()
	a.state.BudgetAllow[sid] = cost + limit
	delete(a.graceUsed, sid)
	a.mu.Unlock()
	a.logEvent("allow_more session=%s cost=%.6f limit=%.6f ceiling=%.6f", sid, cost, limit, cost+limit)
	a.PublishBudget()
}

// RaiseLimit doubles the session limit.
func (a *App) RaiseLimit() {
	a.Budget.Cfg.SessionLimitUSD = round4(a.Budget.Limit() * 2)
	a.Rebase(true)
	a.PublishBudget()
}

// Disable turns enforcement off.
func (a *App) Disable() {
	a.Budget.Cfg.Enabled = false
	a.PublishBudget()
}

func (a *App) baselinesLocked() map[string]float64 {
	cp := make(map[string]float64, len(a.state.BudgetBaselines))
	for k, v := range a.state.BudgetBaselines {
		cp[k] = v
	}
	return cp
}

func (a *App) allowLocked() map[string]float64 {
	cp := make(map[string]float64, len(a.state.BudgetAllow))
	for k, v := range a.state.BudgetAllow {
		cp[k] = v
	}
	return cp
}

func (a *App) graceUsedLocked() map[string]int {
	cp := make(map[string]int, len(a.graceUsed))
	for k, v := range a.graceUsed {
		cp[k] = v
	}
	return cp
}

// GraceUsed returns a copy of the grace map.
func (a *App) GraceUsed() map[string]int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.graceUsedLocked()
}

func round6(f float64) float64 { return float64(int64(f*1e6+0.5)) / 1e6 }
func round4(f float64) float64 {
	return float64(int64(f*1e4+0.5)) / 1e4
}
