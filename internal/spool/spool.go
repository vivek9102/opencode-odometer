// Package spool reads the append-only event file written by the OpenCode
// plugin.
//
// Why this exists: OpenCode's TUI runs its API over an in-process transport
// unless it is started with an explicit --port, so by default nothing listens
// on TCP and the odometer has no server to poll. The plugin, however, runs
// *inside* OpenCode and already receives every assistant message. Rather than
// requiring the user to launch a headless server, the plugin appends what it
// already has to a small JSONL file and the odometer tails it.
//
// The file is a spool, not a database: the odometer owns the priced ledger and
// simply consumes records in order, remembering how far it has read.
package spool

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// FileName is the spool written by the plugin, inside the odometer data dir.
const FileName = "events.jsonl"

// Cache is one message's token usage as the plugin observed it.
type Cache struct {
	Read  int64 `json:"read"`
	Write int64 `json:"write"`
}

// Tokens mirrors the assistant message token block.
type Tokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cache     Cache `json:"cache"`
}

// Record is a single spooled assistant message.
type Record struct {
	// Type distinguishes a normal upsert ("message") from a removal
	// ("message.removed"), which must net the spend back out.
	Type        string  `json:"type"`
	ID          string  `json:"id"`
	SessionID   string  `json:"sessionID"`
	ParentID    string  `json:"parentID,omitempty"`
	Provider    string  `json:"providerID"`
	Model       string  `json:"modelID"`
	Tokens      Tokens  `json:"tokens"`
	Cost        float64 `json:"cost"`
	Finish      string  `json:"finish"`
	TimeCreated int64   `json:"timeCreated"`
}

// Reader tails the spool file, remembering its position between polls.
//
// It is resilient to the two things that actually happen in practice: the
// plugin truncating/rotating the file, and a partial final line caught
// mid-write.
type Reader struct {
	mu     sync.Mutex
	path   string
	offset int64
}

// New returns a Reader for the spool inside dir.
func New(dir string) *Reader {
	return &Reader{path: filepath.Join(dir, FileName)}
}

// Path reports the spool location.
func (r *Reader) Path() string { return r.path }

// Offset reports how many bytes have been consumed.
func (r *Reader) Offset() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.offset
}

// SetOffset restores a persisted read position, so a restart does not replay
// the whole spool. Records are keyed by message id and upserted, so a replay
// would be harmless but wasteful.
func (r *Reader) SetOffset(off int64) {
	r.mu.Lock()
	r.offset = off
	r.mu.Unlock()
}

// Read returns records appended since the last call.
//
// Only whole lines are consumed: if the file ends mid-record (the plugin was
// interrupted between write and newline) the offset stops before it, and the
// remainder is picked up on the next poll.
func (r *Reader) Read() ([]Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	f, err := os.Open(r.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil // plugin has not written anything yet
		}
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	// Truncated or rotated: start over rather than seeking past the end.
	if fi.Size() < r.offset {
		r.offset = 0
	}
	if fi.Size() == r.offset {
		return nil, nil
	}
	if _, err := f.Seek(r.offset, io.SeekStart); err != nil {
		return nil, err
	}

	var (
		out      []Record
		consumed = r.offset
	)
	rd := bufio.NewReaderSize(f, 64*1024)
	for {
		// ReadBytes keeps the delimiter, so a line returned without a
		// trailing newline is by definition incomplete: the plugin was caught
		// between writing the record and terminating it. Such a line must NOT
		// advance the offset, or the finished record is skipped on the next
		// poll and its spend is lost forever.
		line, err := rd.ReadBytes('\n')
		complete := err == nil

		if complete {
			consumed += int64(len(line))
			if trimmed := trimSpace(line); len(trimmed) > 0 {
				var rec Record
				// A malformed line is skipped rather than stalling the spool
				// forever; the plugin writes one JSON object per line.
				if json.Unmarshal(trimmed, &rec) == nil && rec.ID != "" {
					out = append(out, rec)
				}
			}
			continue
		}

		if errors.Is(err, io.EOF) {
			// Trailing partial line (if any) is deliberately left unconsumed.
			break
		}
		r.offset = consumed
		return out, err
	}

	r.offset = consumed
	return out, nil
}

func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && isSpace(b[start]) {
		start++
	}
	end := len(b)
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}
