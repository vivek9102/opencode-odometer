package app

import (
	"os"
	"path/filepath"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/opencode"
	"github.com/vivek9102/opencode-odometer/internal/spool"
)

// spoolFresh is how recently the plugin must have appended for the spool to
// count as actively streaming.
const spoolFresh = 90 * time.Second

// spoolIdle is how long the spool may stay quiet before the link is treated as
// genuinely gone rather than merely idle.
//
// The plugin only writes on an assistant message, so any pause for reading or
// typing exceeds spoolFresh. Reporting "offline" after 90 seconds cried wolf
// on every normal pause; a link that is installed and working but quiet is
// idle, and only prolonged silence means the data path is actually broken.
const spoolIdle = 15 * time.Minute

// DrainSpool ingests everything the plugin has appended since the last call
// and reports how many ledger records changed.
//
// This is the path that works when OpenCode runs its API in-process (the
// default), where there is no TCP port to poll. The plugin already receives
// every assistant message inside OpenCode, so it appends them here and the
// odometer prices them exactly as it prices anything from the HTTP API.
func (a *App) DrainSpool() int {
	if a.Spool == nil {
		return 0
	}
	recs, err := a.Spool.Read()
	if err != nil {
		a.LogThrottled("spool-read", time.Minute, "spool read: %v", err)
	}
	if len(recs) == 0 {
		return 0
	}

	// Mark the ledger seeded on the first spool batch.
	//
	// This un-gates the burn rate: ApplyMessage only records a rate sample
	// once seeded, and seeding used to require an HTTP connection, so with
	// the plugin as the only ingest path the rate stayed at $0.00/hr no
	// matter how much was being spent.
	//
	// It deliberately does NOT reset the trip. The spool is a rolling window
	// of recent turns, not a full history, so resetting the baseline against
	// it rebases the trip onto a partial total and destroys the running
	// figures. Only an explicit RESET TRIP (or a genuine full HTTP seed)
	// moves the baseline.
	seeding := !a.Seeded()

	changed := 0
	for _, rec := range recs {
		if rec.Type == "session" {
			if a.SetSessionParent(rec.ID, rec.ParentID) { changed++ }
			continue
		}
		if rec.Type == "message.removed" {
			// A retry/undo must net its spend back out.
			if _, removed := a.Ledger.Remove(rec.ID); removed {
				changed++
			}
			continue
		}
		if a.ApplyMessage(opencode.Message{
			ID:        rec.ID,
			SessionID: rec.SessionID,
			Provider:  rec.Provider,
			Model:     rec.Model,
			Tokens: opencode.TokenUsage{
				Input:     rec.Tokens.Input,
				Output:    rec.Tokens.Output,
				Reasoning: rec.Tokens.Reasoning,
				Cache: opencode.CacheUsage{
					Read:  rec.Tokens.Cache.Read,
					Write: rec.Tokens.Cache.Write,
				},
			},
			Cost:        rec.Cost,
			Finish:      rec.Finish,
			TimeCreated: rec.TimeCreated,
		}) {
			changed++
		}
	}

	a.mu.Lock()
	a.spoolSeen = time.Now()
	a.state.SpoolOffset = a.Spool.Offset()
	a.mu.Unlock()

	if seeding {
		a.mu.Lock()
		a.state.Seeded = true
		a.mu.Unlock()
		// Backfilled history is not live spend, so it must not register as a
		// burn-rate spike.
		a.ResetRateWindow()
		a.absorbAndPublish()
		a.SaveState()
		a.LogEvent("spool seeded from plugin backfill records=%d changed=%d", len(recs), changed)
		return changed
	}

	if changed > 0 {
		a.absorbAndPublish()
		a.SaveState()
		a.LogEvent("spool ingested records=%d changed=%d", len(recs), changed)
	}
	return changed
}

// SpoolActive reports whether the plugin has appended recently, which means
// OpenCode is running and feeding us even though no TCP port is open.
func (a *App) SpoolActive() bool {
	a.mu.RLock()
	seen := a.spoolSeen
	a.mu.RUnlock()
	if seen.IsZero() {
		return false
	}
	return time.Since(seen) < spoolFresh
}

// SpoolIdle reports a link that has delivered data but is currently quiet:
// the plugin is in place and working, there is simply no activity to report.
// Distinguished from offline so a normal pause is not shown as a fault.
func (a *App) SpoolIdle() bool {
	a.mu.RLock()
	seen := a.spoolSeen
	a.mu.RUnlock()
	if seen.IsZero() {
		return false
	}
	since := time.Since(seen)
	return since >= spoolFresh && since < spoolIdle
}

// SpoolLastSeen reports when the plugin last appended. Zero if never.
func (a *App) SpoolLastSeen() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.spoolSeen
}

// PluginInstalled reports whether the plugin file is present, which separates
// "installed but quiet" from "no data path at all".
func (a *App) PluginInstalled() bool {
	dir := pluginDir()
	if dir == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, PluginFileName))
	return err == nil && fi.Size() > 0
}

// spoolReader builds the reader for the configured data directory.
func newSpool(dir string) *spool.Reader {
	if dir == "" {
		return nil
	}
	return spool.New(dir)
}
