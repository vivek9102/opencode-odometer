package ledger

import (
	"encoding/json"
	"sort"
	"time"
)

// UsageBucket keeps accounting facts, not conversation text or credentials.
// Days use the local calendar date captured on message ingestion.
type UsageBucket struct {
	Date       string  `json:"date"`
	Provider   string  `json:"provider"`
	Model      string  `json:"model"`
	Source     string  `json:"source"`
	Free       bool    `json:"free"`
	Estimated  bool    `json:"estimated"`
	Unknown    bool    `json:"unknown"`
	Messages   int64   `json:"messages"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	Reasoning  int64   `json:"reasoning"`
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
	Cost       float64 `json:"cost"`
	Saved      float64 `json:"saved"`
}

func bucketFor(r Record) UsageBucket {
	date := "unknown"
	if len(r.Timestamp) >= 10 {
		if _, err := time.Parse("2006-01-02", r.Timestamp[:10]); err == nil {
			date = r.Timestamp[:10]
		}
	}
	return UsageBucket{Date: date, Provider: r.Provider, Model: r.Model, Source: r.CostSource,
		Free: r.Free, Estimated: r.Estimated, Unknown: r.Unknown, Messages: 1, Input: r.TokensIn,
		Output: r.TokensOut, Reasoning: r.Reasoning, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite, Cost: r.Cost, Saved: r.Saved}
}

func bucketKey(b UsageBucket) string {
	key, _ := json.Marshal([]any{b.Date, b.Provider, b.Model, b.Source, b.Free, b.Estimated, b.Unknown})
	return string(key)
}

func (b *UsageBucket) add(other UsageBucket) {
	b.Messages += other.Messages
	b.Input += other.Input
	b.Output += other.Output
	b.Reasoning += other.Reasoning
	b.CacheRead += other.CacheRead
	b.CacheWrite += other.CacheWrite
	b.Cost += other.Cost
	b.Saved += other.Saved
}

// Usage combines retained detail and archived daily facts from one snapshot.
func (s *Store) Usage() []UsageBucket {
	cp := s.Copy()
	for _, rec := range cp.Messages {
		b := bucketFor(rec)
		key := bucketKey(b)
		old, ok := cp.Archived[key]
		if !ok {
			old = b
			old.Messages = 0
			old.Input = 0
			old.Output = 0
			old.Reasoning = 0
			old.CacheRead = 0
			old.CacheWrite = 0
			old.Cost = 0
			old.Saved = 0
		}
		old.add(b)
		cp.Archived[key] = old
	}
	rows := make([]UsageBucket, 0, len(cp.Archived))
	for _, b := range cp.Archived {
		rows = append(rows, b)
	}
	sort.Slice(rows, func(i, j int) bool { return bucketKey(rows[i]) < bucketKey(rows[j]) })
	return rows
}

func addRecord(a *Record, b Record) {
	a.TokensIn += b.TokensIn
	a.TokensOut += b.TokensOut
	a.Reasoning += b.Reasoning
	a.CacheRead += b.CacheRead
	a.CacheWrite += b.CacheWrite
	a.Cost += b.Cost
	a.Saved += b.Saved
}

func recordDelta(rec, before Record) Record {
	rec.TokensIn -= before.TokensIn
	rec.TokensOut -= before.TokensOut
	rec.Reasoning -= before.Reasoning
	rec.CacheRead -= before.CacheRead
	rec.CacheWrite -= before.CacheWrite
	rec.Cost -= before.Cost
	rec.Saved -= before.Saved
	return rec
}

// TripRecords exports deltas, including updates to a message already streaming
// at reset and negative corrections when a pre-reset message is removed.
// Legacy scalar baselines cannot reconstruct those deltas; emit an explicit
// period summary until the next reset, rather than claiming all rows are new.
func (s *Store) TripRecords() []Record {
	cp := s.Copy()
	if cp.TripStarted == "" {
		return []Record{{MID: "LEGACY_PERIOD_SUMMARY", Cost: cp.TripCost, Saved: cp.TripSaved, Finish: "Detailed reset baseline unavailable; reset the counter to start detailed period exports."}}
	}
	rows := []Record{}
	for id, rec := range cp.Messages {
		delta := recordDelta(rec, cp.TripBaseline[id])
		if delta.Cost != 0 || delta.Saved != 0 || delta.TokensIn != 0 || delta.TokensOut != 0 || delta.CacheRead != 0 || delta.CacheWrite != 0 || delta.Reasoning != 0 {
			rows = append(rows, delta)
		}
	}
	for id, before := range cp.TripBaseline {
		if _, ok := cp.Messages[id]; !ok {
			r := recordDelta(Record{MID: id, SessionID: before.SessionID, Provider: before.Provider, Model: before.Model, Timestamp: before.Timestamp, Finish: "Removed message correction"}, before)
			rows = append(rows, r)
		}
	}
	if cp.TripArchived != (Record{}) {
		r := cp.TripArchived
		r.MID = "ARCHIVED_PERIOD_SUMMARY"
		r.Finish = "Pruned message deltas since reset"
		rows = append(rows, r)
	}
	var cost, saved float64
	for _, r := range rows {
		cost += r.Cost
		saved += r.Saved
	}
	if abs(cp.TripCost-cost) > 1e-12 || abs(cp.TripSaved-saved) > 1e-12 {
		rows = append(rows, Record{MID: "COUNTER_ADJUSTMENT", Cost: cp.TripCost - cost, Saved: cp.TripSaved - saved, Finish: "Counter floors negative period totals at zero; legacy baseline corrections"})
	}
	return rows
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
