package app

import (
	"context"
	"log"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/opencode"
)

// SyncHistory synchronizes the message ledger with OpenCode's REST API.
// If isInitialSeed is true (e.g. first run without prior state), the trip baseline
// is established from total cost. Otherwise, missing/updated turns are ingested
// incrementally while preserving current trip settings and baselines.
func (a *App) SyncHistory(isInitialSeed bool) (int, error) {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()

	sessions, err := a.Client.Session()
	if err != nil {
		return 0, err
	}
	synced := 0
	for _, s := range sessions {
		lastUpdated := s.Time.Updated
		if lastUpdated == 0 {
			lastUpdated = s.Time.Created
		}
		a.sessionUpdatedMu.Lock()
		if a.sessionUpdated == nil {
			a.sessionUpdated = make(map[string]int64)
		}
		a.sessionUpdated[s.ID] = lastUpdated
		a.sessionUpdatedMu.Unlock()

		msgs, err := a.Client.Messages(s.ID)
		if err != nil {
			log.Printf("[sync] session %s: %v", s.ID, err)
			continue
		}
		for _, m := range msgs {
			if a.ApplyMessage(m) {
				synced++
			}
		}
	}

	if isInitialSeed {
		a.mu.Lock()
		a.state.Seeded = true
		a.mu.Unlock()

		// existing history is context, not this trip
		a.Ledger.ResetTrip()
	}

	a.absorbAndPublish()
	a.SaveState()
	totCost, _ := a.Ledger.GetTotals("TOTAL")
	trpCost, _ := a.Ledger.GetTotals("TRIP")
	a.LogEvent("sync complete sessions=%d messages_synced=%d is_initial_seed=%v total_cost=%.6f trip_cost=%.6f",
		len(sessions), synced, isInitialSeed, totCost, trpCost)
	return synced, nil
}

// SeedHistory backfills the ledger from existing sessions the first time the
// app runs. Existing history is context, not this trip, so the trip baseline
// is reset after seeding.
func (a *App) SeedHistory() error {
	_, err := a.SyncHistory(true)
	return err
}

// Run subscribes to the OpenCode SSE stream, applying each message and
// republishing the budget. It blocks until the stream closes or onErr is
// invoked. onMsg is called (on the streaming goroutine) after each applied
// message; the UI uses it to refresh.
func (a *App) Run(onMsg func(bool)) error {
	return a.RunContext(context.Background(), onMsg)
}

// RunContext subscribes to the OpenCode SSE stream with context cancellation support.
func (a *App) RunContext(ctx context.Context, onMsg func(bool)) error {
	lastApplied := time.Now().Add(-time.Minute)

	// Auto-seed or catch up history on connection
	if !a.Seeded() {
		if err := a.SeedHistory(); err != nil {
			log.Printf("[run] seed history: %v", err)
		}
	} else {
		_, _ = a.SyncHistory(false)
		a.PublishBudget()
	}

	type contextSubscriber interface {
		SubscribeWithContext(context.Context, func(opencode.Event) error) error
	}

	handler := func(ev opencode.Event) error {
		// Log every decodable event so the user can see live what the app is
		// listening to and receiving (see Config.LogFile).
		if ev.MessageID != "" {
			a.LogEvent("event message.removed session=%s msg=%s", ev.SessionID, ev.MessageID)
		} else if ev.Message != nil {
			a.LogEvent("event %s session=%s msg=%s provider=%s model=%s in=%d out=%d cache_r=%d cache_w=%d",
				ev.Type, ev.Message.SessionID, ev.Message.ID,
				ev.Message.Provider, ev.Message.Model,
				ev.Message.Tokens.Input, ev.Message.Tokens.Output,
				ev.Message.Tokens.Cache.Read, ev.Message.Tokens.Cache.Write)
		} else if ev.Type != "" {
			a.LogEvent("event %s session=%s", ev.Type, ev.SessionID)
		}

		// message.removed: drop the recorded message so a retry/undo does not
		// double-count its spend.
		if ev.MessageID != "" {
			_, removed := a.Ledger.Remove(ev.MessageID)
			if removed {
				a.absorbAndPublish()
				a.SaveState()
				if onMsg != nil {
					onMsg(true)
				}
			}
			return nil
		}

		// Refresh session messages when any session-related event arrives
		if ev.SessionID != "" && ev.Message == nil && ev.Type != "server.heartbeat" && ev.Type != "server.connected" {
			go func(sid string) {
				if msgs, err := a.Client.Messages(sid); err == nil {
					anyChanged := false
					for _, m := range msgs {
						if a.ApplyMessage(m) {
							anyChanged = true
						}
					}
					if anyChanged {
						a.absorbAndPublish()
						a.SaveState()
						if onMsg != nil {
							onMsg(true)
						}
					}
				}
			}(ev.SessionID)
		}

		if ev.Message == nil {
			return nil
		}

		changed := a.ApplyMessage(*ev.Message)
		// Fold grace claims, save state, and republish budget
		if changed {
			a.absorbAndPublish()
			a.SaveState()
			lastApplied = time.Now()
		} else if time.Since(lastApplied) > time.Second {
			a.absorbAndPublish()
			lastApplied = time.Now()
		}
		if onMsg != nil {
			onMsg(changed)
		}
		return nil
	}

	url := a.Rediscover()
	a.SetConnected(true, url)
	defer a.SetConnected(false, "")

	if cs, ok := a.Client.(contextSubscriber); ok {
		return cs.SubscribeWithContext(ctx, handler)
	}
	return a.Client.Subscribe(handler)
}

// Rediscover asks the client to re-resolve the OpenCode address and returns
// the address now in use. OpenCode picks a fresh port per run, so the address
// must be re-checked on every reconnect rather than fixed at startup.
func (a *App) Rediscover() string {
	c, ok := a.Client.(interface{ Rediscover() (string, bool) })
	if !ok {
		return ""
	}
	url, changed := c.Rediscover()
	if changed {
		a.LogEvent("opencode server resolved to %s", url)
	}
	a.mu.Lock()
	if url != "" {
		a.serverURL = url
	}
	a.mu.Unlock()
	return url
}

// SyncRecent incrementally ingests newly created or updated sessions and messages.
func (a *App) SyncRecent() (int, error) {
	if !a.Seeded() {
		return a.SyncHistory(true)
	}

	a.syncMu.Lock()
	defer a.syncMu.Unlock()

	sessions, err := a.Client.Session()
	if err != nil {
		return 0, err
	}

	a.sessionUpdatedMu.Lock()
	if a.sessionUpdated == nil {
		a.sessionUpdated = make(map[string]int64)
	}
	a.sessionUpdatedMu.Unlock()

	synced := 0
	anyChanged := false
	for _, s := range sessions {
		lastUpdated := s.Time.Updated
		if lastUpdated == 0 {
			lastUpdated = s.Time.Created
		}

		a.sessionUpdatedMu.Lock()
		prevUpdated, seen := a.sessionUpdated[s.ID]
		shouldFetch := !seen || lastUpdated > prevUpdated
		a.sessionUpdatedMu.Unlock()

		if shouldFetch {
			msgs, err := a.Client.Messages(s.ID)
			if err != nil {
				continue
			}
			for _, m := range msgs {
				if a.ApplyMessage(m) {
					synced++
					anyChanged = true
				}
			}
			a.sessionUpdatedMu.Lock()
			a.sessionUpdated[s.ID] = lastUpdated
			a.sessionUpdatedMu.Unlock()
		}
	}

	if anyChanged {
		a.absorbAndPublish()
		a.SaveState()
		totCost, _ := a.Ledger.GetTotals("TOTAL")
		trpCost, _ := a.Ledger.GetTotals("TRIP")
		a.LogEvent("sync_recent complete messages_synced=%d total_cost=%.6f trip_cost=%.6f",
			synced, totCost, trpCost)
	}
	return synced, nil
}

// Poll is a fallback that a UI can call on a timer (e.g. 1s tick) to detect
// newly updated sessions/turns, absorb grace claims, and re-publish state.
func (a *App) Poll() {
	// Always drain the plugin spool first: it is the only ingest path that
	// works when OpenCode serves its API in-process (no TCP port), which is
	// the default for the TUI.
	a.DrainSpool()

	// When the plugin is feeding us and no stream is attached, there is no
	// server to poll — every REST call would just fail. Skip them rather than
	// attempting a connection per tick.
	if connected, _ := a.Connected(); !connected && a.SpoolActive() {
		absorbed, _ := a.Contract.AbsorbGraceClaims(a.graceUsed)
		if absorbed {
			a.SaveState()
		}
		// Republish regardless of whether anything changed. The plugin treats
		// a budget.json older than two minutes as a dead odometer and stops
		// enforcing, so an idle session must still refresh the file — and the
		// UI reads the same verdict, so a stale file also made the board
		// disagree with reality.
		a.PublishBudget()
		return
	}

	if !a.Seeded() {
		_ = a.SeedHistory()
	} else {
		_, _ = a.SyncRecent()
	}
	absorbed, _ := a.Contract.AbsorbGraceClaims(a.graceUsed)
	if absorbed {
		a.PublishBudget()
		a.SaveState()
	}
}

func (a *App) absorbAndPublish() {
	absorbed, _ := a.Contract.AbsorbGraceClaims(a.graceUsed)
	if absorbed {
		a.SaveState()
	}
	a.PublishBudget()
}

// ReconnectDelay is how long Run's caller should wait before retrying a dead
// stream (server not running yet).
const ReconnectDelay = time.Second

// Seeded reports whether history has been backfilled at least once.
func (a *App) Seeded() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Seeded
}

// SetSeeded sets whether history has been seeded.
func (a *App) SetSeeded(s bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Seeded = s
}

// ResetRateWindow clears the live burn rate calculation window.
func (a *App) ResetRateWindow() {
	a.rateMu.Lock()
	defer a.rateMu.Unlock()
	a.rateWindow = nil
}

// BurnRate reports spend per hour computed from a rolling 10-minute window of
// live spend deltas (scaled x6 to hourly figure), exactly matching Python.
func (a *App) BurnRate() float64 {
	a.rateMu.Lock()
	defer a.rateMu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	valid := make([]RatePoint, 0, len(a.rateWindow))
	total := 0.0
	for _, pt := range a.rateWindow {
		if pt.Time.After(cutoff) {
			valid = append(valid, pt)
			total += pt.Cost
		}
	}
	a.rateWindow = valid
	return total * 6.0
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
