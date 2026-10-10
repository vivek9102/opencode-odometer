package ledger

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Record is a single priced assistant message.
type Record struct {
	MID        string  `json:"mid"`
	SessionID  string  `json:"session_id"`
	Provider   string  `json:"provider"`
	Model      string  `json:"model"`
	Free       bool    `json:"free"`
	TokensIn   int64   `json:"in"`
	TokensOut  int64   `json:"out"`
	Reasoning  int64   `json:"reasoning"`
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
	Cost       float64 `json:"cost"`
	Saved      float64 `json:"saved"`
	// CostSource records whether this message used an OpenCode-reported cost,
	// a local override, the catalog, or an estimate. Keeping it on each record
	// makes historical totals explainable after the price table changes.
	CostSource   string  `json:"cost_source,omitempty"`
	ReportedCost float64 `json:"reported_cost,omitempty"`
	Estimated    bool    `json:"estimated,omitempty"`
	Unknown      bool    `json:"unknown,omitempty"`
	Timestamp    string  `json:"timestamp"`
	// Finish is the finish reason when known ("stop", "aborted", "error", ...).
	Finish string `json:"finish,omitempty"`
}

// Store is the priced message ledger and derived totals.
type Store struct {
	mu            sync.RWMutex
	Messages      map[string]Record       `json:"messages"`
	TotalCost     float64                 `json:"total_cost"`
	TotalSaved    float64                 `json:"total_saved"`
	TripCost      float64                 `json:"trip_cost"`
	TripSaved     float64                 `json:"trip_saved"`
	TripBase      float64                 `json:"trip_base"`
	TripSavedBase float64                 `json:"trip_saved_base"`
	PrunedCost    float64                 `json:"pruned_cost"`
	PrunedSaved   float64                 `json:"pruned_saved"`
	PerModel      map[string]*ModelStat   `json:"per_model"`
	PerSession    map[string]*SessionStat `json:"per_session"`
	Archived      map[string]UsageBucket  `json:"daily_archive,omitempty"`
	PrunedIDs     map[string]bool         `json:"pruned_ids,omitempty"`
	TripStarted   string                  `json:"trip_started,omitempty"`
	TripBaseline  map[string]Record       `json:"trip_records,omitempty"`
	TripArchived  Record                  `json:"trip_archived,omitempty"`
}

// ModelStat aggregates spend for a single "provider/model" key.
type ModelStat struct {
	Msgs       int64   `json:"msgs"`
	TokensIn   int64   `json:"in"`
	TokensOut  int64   `json:"out"`
	CacheRead  int64   `json:"cr"`
	CacheWrite int64   `json:"cw"`
	Cost       float64 `json:"cost"`
	Saved      float64 `json:"saved"`
	Free       bool    `json:"free"`
	Unknown    bool    `json:"unknown"`
	Estimated  bool    `json:"estimated,omitempty"`
	Source     string  `json:"source,omitempty"`
}

// SessionStat aggregates spend for a single session.
type SessionStat struct {
	Cost   float64 `json:"cost"`
	Msgs   int64   `json:"msgs"`
	Tokens int64   `json:"tokens"`
	Last   string  `json:"last"`
	Model  string  `json:"model"`
}

// ModelItem bundles a model key with its aggregated stat for safe UI rendering.
type ModelItem struct {
	Key  string
	Stat ModelStat
}

// New returns an empty store.
func New() *Store {
	return &Store{
		Messages:   map[string]Record{},
		PerModel:   map[string]*ModelStat{},
		PerSession: map[string]*SessionStat{},
	}
}

// Lock acquires the write lock for manual multi-operation sequences.
func (s *Store) Lock() { s.mu.Lock() }

// Unlock releases the write lock.
func (s *Store) Unlock() { s.mu.Unlock() }

// RLock acquires the read lock.
func (s *Store) RLock() { s.mu.RLock() }

// RUnlock releases the read lock.
func (s *Store) RUnlock() { s.mu.RUnlock() }

// Put upserts a priced record by message id. It reports whether the record
// was new or changed. Cost/provenance changes count too: some providers write
// token totals before the final non-zero reported cost arrives.
func (s *Store) Put(rec Record) bool {
	if rec.MID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Archived rows are immutable. History backfill must not charge them again.
	if s.PrunedIDs[rec.MID] {
		return false
	}

	prev, exists := s.Messages[rec.MID]
	changed := !exists ||
		prev.TokensIn != rec.TokensIn ||
		prev.TokensOut != rec.TokensOut ||
		prev.CacheRead != rec.CacheRead ||
		prev.CacheWrite != rec.CacheWrite ||
		prev.Cost != rec.Cost ||
		prev.Saved != rec.Saved ||
		prev.Free != rec.Free ||
		prev.Unknown != rec.Unknown ||
		prev.Estimated != rec.Estimated ||
		prev.CostSource != rec.CostSource ||
		prev.ReportedCost != rec.ReportedCost
	changed = changed || prev.Reasoning != rec.Reasoning || prev.Provider != rec.Provider || prev.Model != rec.Model || prev.Timestamp != rec.Timestamp
	s.Messages[rec.MID] = rec
	if changed {
		s.recomputeLocked()
	}
	return changed
}

// Remove deletes a message by ID and recomputes all totals, netting the
// message's spend out of the ledger. Used on message.removed events (retries
// and undos must not double-count). Returns the removed record and whether a
// record was actually removed.
func (s *Store) Remove(mid string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.Messages[mid]
	if !ok {
		return Record{}, false
	}
	delete(s.Messages, mid)
	s.recomputeLocked()
	return rec, true
}

// Recompute rebuilds all totals from the message store.
func (s *Store) Recompute() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recomputeLocked()
}

// RecomputeLocked rebuilds all totals while holding the write lock.
func (s *Store) RecomputeLocked() {
	s.recomputeLocked()
}

// ResetTrip moves the trip baseline to current spend and zeroes the trip counter.
func (s *Store) ResetTrip() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.TripBase = s.TotalCost
	s.TripSavedBase = s.TotalSaved
	s.TripStarted = time.Now().Format(time.RFC3339)
	s.TripBaseline = make(map[string]Record, len(s.Messages))
	for id, rec := range s.Messages {
		s.TripBaseline[id] = rec
	}
	s.TripArchived = Record{}
	s.TripCost = 0
	s.TripSaved = 0
	s.recomputeLocked()
}

// tripEpsilon is the threshold below which a trip delta is treated as zero.
//
// Totals are sums of thousands of floats, so the same ledger can differ by
// ~1e-13 between runs purely from accumulation order. Without this, a
// baseline captured on one run never exactly cancels the total on the next,
// and TRIP shows a meaningless residue like 0.00000000000045 — or, worse,
// real spend hides behind a stale baseline. A tenth of a micro-dollar is far
// below the 5-decimal display resolution, so clamping here cannot hide
// anything a user could see.
const tripEpsilon = 1e-7

func (s *Store) recomputeLocked() {
	per := map[string]*ModelStat{}
	perSession := map[string]*SessionStat{}

	// Iterate in a stable id order. Go randomises map iteration, so summing
	// directly over s.Messages makes TotalCost vary in its last bits between
	// runs; that drift is what made TRIP stick at ~4e-13 instead of 0.
	ids := make([]string, 0, len(s.Messages))
	for id := range s.Messages {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	total, savedTotal := 0.0, 0.0
	for _, id := range ids {
		rec := s.Messages[id]
		if rec.SessionID != "" {
			st := perSession[rec.SessionID]
			if st == nil {
				st = &SessionStat{}
				perSession[rec.SessionID] = st
			}
			st.Cost += rec.Cost
			st.Msgs++
			st.Tokens += rec.TokensIn + rec.TokensOut + rec.CacheRead + rec.CacheWrite
			if rec.Timestamp > st.Last {
				st.Last = rec.Timestamp
				st.Model = rec.Provider + "/" + rec.Model
			}
		}

		key := rec.Provider + "/" + rec.Model
		total += rec.Cost
		savedTotal += rec.Saved
		st := per[key]
		if st == nil {
			st = &ModelStat{
				Free: rec.Free, Unknown: rec.Unknown,
				Estimated: rec.Estimated, Source: rec.CostSource,
			}
			per[key] = st
		} else {
			st.Unknown = st.Unknown || rec.Unknown
			st.Estimated = st.Estimated || rec.Estimated
			if st.Source != rec.CostSource {
				st.Source = "mixed"
			}
		}
		st.Msgs++
		st.TokensIn += rec.TokensIn
		st.TokensOut += rec.TokensOut
		st.CacheRead += rec.CacheRead
		st.CacheWrite += rec.CacheWrite
		st.Cost += rec.Cost
		st.Saved += rec.Saved
	}

	// spend from pruned messages still counts toward lifetime totals
	total += s.PrunedCost
	savedTotal += s.PrunedSaved

	s.PerModel = per
	s.PerSession = perSession
	s.TotalCost = total
	s.TotalSaved = savedTotal
	// trip = everything since the last reset baseline, clamped at 0
	s.TripCost = clampEpsilon(total - s.TripBase)
	s.TripSaved = clampEpsilon(savedTotal - s.TripSavedBase)
}

// clampEpsilon floors a trip delta at zero and flattens float residue.
func clampEpsilon(v float64) float64 {
	if v < tripEpsilon {
		return 0
	}
	return v
}

// Prune drops the oldest messages beyond max, folding their spend into the
// carried-forward base so lifetime totals stay correct. Returns the number
// dropped.
func (s *Store) Prune(max int) int {
	return s.PruneKeeping(max, nil)
}

// PruneKeeping retains records needed to enforce live budgets. The ledger may
// exceed max while protected conversations are open; closing releases them.
func (s *Store) PruneKeeping(max int, keep map[string]bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.Messages) <= max {
		return 0
	}
	ids := make([]string, 0, len(s.Messages))
	for id := range s.Messages {
		if !keep[id] {
			ids = append(ids, id)
		}
	}
	// oldest first by (timestamp, mid) — stable, deterministic
	sort.Slice(ids, func(i, j int) bool {
		a, b := s.Messages[ids[i]], s.Messages[ids[j]]
		if a.Timestamp != b.Timestamp {
			return a.Timestamp < b.Timestamp
		}
		return a.MID < b.MID
	})

	drop := len(s.Messages) - max
	if drop > len(ids) {
		drop = len(ids)
	}
	for _, id := range ids[:drop] {
		rec := s.Messages[id]
		if s.Archived == nil {
			s.Archived = map[string]UsageBucket{}
		}
		if s.PrunedIDs == nil {
			s.PrunedIDs = map[string]bool{}
		}
		bucket := bucketFor(rec)
		key := bucketKey(bucket)
		previous := s.Archived[key]
		previous.Date, previous.Provider, previous.Model, previous.Source = bucket.Date, bucket.Provider, bucket.Model, bucket.Source
		previous.Free, previous.Estimated, previous.Unknown = bucket.Free, bucket.Estimated, bucket.Unknown
		previous.add(bucket)
		s.Archived[key] = previous
		s.PrunedIDs[id] = true
		if s.TripStarted != "" {
			delta := recordDelta(rec, s.TripBaseline[id])
			addRecord(&s.TripArchived, delta)
			delete(s.TripBaseline, id)
		}
		s.PrunedCost += rec.Cost
		s.PrunedSaved += rec.Saved
		delete(s.Messages, id)
	}
	s.recomputeLocked()
	return drop
}

// Key returns the "provider/model" key for a record.
func Key(rec Record) string { return rec.Provider + "/" + rec.Model }

// Human renders a token count compactly (1.2K, 3.4M, 1.0B).
func Human(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	default:
		return strconv.FormatInt(n, 10)
	}
}

// ActiveModel returns the "provider/model" of the most recent message, or an
// empty string if the ledger is empty.
func (s *Store) ActiveModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var best, bestTime, bestMID string
	for _, rec := range s.Messages {
		if rec.TokensIn == 0 && rec.TokensOut == 0 && rec.CacheRead == 0 && rec.CacheWrite == 0 {
			continue
		}
		if best == "" || rec.Timestamp > bestTime || (rec.Timestamp == bestTime && rec.MID > bestMID) {
			best = Key(rec)
			bestTime = rec.Timestamp
			bestMID = rec.MID
		}
	}
	if best == "" {
		for _, rec := range s.Messages {
			if best == "" || rec.Timestamp > bestTime || (rec.Timestamp == bestTime && rec.MID > bestMID) {
				best = Key(rec)
				bestTime = rec.Timestamp
				bestMID = rec.MID
			}
		}
	}
	return best
}

// TotalTokens calculates sum of input + output tokens across all models.
func (s *Store) TotalTokens() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int64
	for _, m := range s.PerModel {
		n += m.TokensIn + m.TokensOut
	}
	return n
}

// GetModelItems returns a safe, detached snapshot of per-model statistics.
func (s *Store) GetModelItems() []ModelItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]ModelItem, 0, len(s.PerModel))
	for k, v := range s.PerModel {
		items = append(items, ModelItem{Key: k, Stat: *v})
	}
	return items
}

// GetPerSessionSnapshot returns a safe, detached map of session statistics.
func (s *Store) GetPerSessionSnapshot() map[string]SessionStat {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cp := make(map[string]SessionStat, len(s.PerSession))
	for k, v := range s.PerSession {
		cp[k] = *v
	}
	return cp
}

// ActiveSession returns the session ID with the most recent activity.
func (s *Store) ActiveSession() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var best, bestLast string
	for sid, st := range s.PerSession {
		if best == "" || st.Last > bestLast || (st.Last == bestLast && sid > best) {
			bestLast = st.Last
			best = sid
		}
	}
	return best
}

// GetTotals returns current view cost and saved amount.
func (s *Store) GetTotals(view string) (cost, saved float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if view == "TRIP" {
		return s.TripCost, s.TripSaved
	}
	return s.TotalCost, s.TotalSaved
}

// GetMessagesSnapshot returns a cloned list of all records (e.g. for CSV export).
func (s *Store) GetMessagesSnapshot() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]Record, 0, len(s.Messages))
	for _, r := range s.Messages {
		res = append(res, r)
	}
	return res
}

// Copy creates a deep, detached copy of the store for safe serialization.
func (s *Store) Copy() *Store {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cp := &Store{
		TotalCost:     s.TotalCost,
		TotalSaved:    s.TotalSaved,
		TripCost:      s.TripCost,
		TripSaved:     s.TripSaved,
		TripBase:      s.TripBase,
		TripSavedBase: s.TripSavedBase,
		PrunedCost:    s.PrunedCost,
		PrunedSaved:   s.PrunedSaved,
		Messages:      make(map[string]Record, len(s.Messages)),
		PerModel:      make(map[string]*ModelStat, len(s.PerModel)),
		PerSession:    make(map[string]*SessionStat, len(s.PerSession)),
		Archived:      make(map[string]UsageBucket, len(s.Archived)),
		PrunedIDs:     make(map[string]bool, len(s.PrunedIDs)),
		TripStarted:   s.TripStarted,
		TripBaseline:  make(map[string]Record, len(s.TripBaseline)),
		TripArchived:  s.TripArchived,
	}
	for k, v := range s.Messages {
		cp.Messages[k] = v
	}
	for k, v := range s.Archived {
		cp.Archived[k] = v
	}
	for k, v := range s.PrunedIDs {
		cp.PrunedIDs[k] = v
	}
	for k, v := range s.TripBaseline {
		cp.TripBaseline[k] = v
	}
	for k, v := range s.PerModel {
		val := *v
		cp.PerModel[k] = &val
	}
	for k, v := range s.PerSession {
		val := *v
		cp.PerSession[k] = &val
	}
	return cp
}

// TotalCostStr renders the lifetime spend summary for the board header.
func (s *Store) TotalCostStr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("Total: $%.4f   Saved: $%.4f", s.TotalCost, s.TotalSaved)
}

// HumanTokens compact-renders a token count.
func HumanTokens(n int64) string { return Human(n) }
