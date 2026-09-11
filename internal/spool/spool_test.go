package spool

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write: %v", err)
	}
}

const msgA = `{"type":"message","id":"m1","sessionID":"s1","providerID":"acme","modelID":"sonnet","tokens":{"input":10,"output":5,"cache":{"read":1,"write":2}}}` + "\n"
const msgB = `{"type":"message","id":"m2","sessionID":"s1","providerID":"acme","modelID":"opus","tokens":{"input":20,"output":7,"cache":{"read":0,"write":0}}}` + "\n"

// TestReadsOnlyNewRecords is the core behaviour: the reader tails the file and
// never re-delivers what it has already consumed.
func TestReadsOnlyNewRecords(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	path := filepath.Join(dir, FileName)

	// Missing file is not an error: the plugin may not have run yet.
	recs, err := r.Read()
	if err != nil || len(recs) != 0 {
		t.Fatalf("missing file: got %d recs, err %v", len(recs), err)
	}

	write(t, path, msgA)
	recs, err = r.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(recs) != 1 || recs[0].ID != "m1" {
		t.Fatalf("expected m1, got %+v", recs)
	}
	if recs[0].Tokens.Input != 10 || recs[0].Tokens.Cache.Write != 2 {
		t.Errorf("tokens not parsed: %+v", recs[0].Tokens)
	}

	// Nothing new appended.
	if recs, _ := r.Read(); len(recs) != 0 {
		t.Errorf("re-read returned %d records, want 0", len(recs))
	}

	write(t, path, msgB)
	recs, _ = r.Read()
	if len(recs) != 1 || recs[0].ID != "m2" {
		t.Fatalf("expected only m2, got %+v", recs)
	}
}

// TestPartialLineIsNotConsumed guards against reading a record the plugin was
// still writing: it must be picked up whole on the next poll.
func TestPartialLineIsNotConsumed(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	path := filepath.Join(dir, FileName)

	write(t, path, msgA)
	// A record without its terminating newline yet.
	write(t, path, `{"type":"message","id":"m2","tokens":{"input":1`)

	recs, _ := r.Read()
	if len(recs) != 1 || recs[0].ID != "m1" {
		t.Fatalf("expected only the complete record, got %+v", recs)
	}

	// Completing the line delivers it intact.
	write(t, path, `,"output":2,"cache":{"read":0,"write":0}}}`+"\n")
	recs, _ = r.Read()
	if len(recs) != 1 || recs[0].ID != "m2" {
		t.Fatalf("expected m2 after completion, got %+v", recs)
	}
	if recs[0].Tokens.Output != 2 {
		t.Errorf("tokens = %+v, want output 2", recs[0].Tokens)
	}
}

// TestTruncationRestarts covers the plugin rotating the spool once it grows
// too large: the reader must not seek past the new end and stall forever.
func TestTruncationRestarts(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	path := filepath.Join(dir, FileName)

	write(t, path, msgA+msgB)
	if recs, _ := r.Read(); len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}

	// Rotate: the plugin deletes the file and starts again.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	write(t, path, msgA)

	recs, _ := r.Read()
	if len(recs) != 1 || recs[0].ID != "m1" {
		t.Fatalf("after truncation expected m1, got %+v", recs)
	}
}

// TestMalformedLineIsSkipped: one bad line must not block the spool forever.
func TestMalformedLineIsSkipped(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	path := filepath.Join(dir, FileName)

	write(t, path, "this is not json\n"+msgA)
	recs, _ := r.Read()
	if len(recs) != 1 || recs[0].ID != "m1" {
		t.Fatalf("expected to skip the bad line and return m1, got %+v", recs)
	}
}

// TestOffsetResumes verifies a restart does not replay the whole spool.
func TestOffsetResumes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	write(t, path, msgA+msgB)

	first := New(dir)
	if recs, _ := first.Read(); len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	saved := first.Offset()

	// A fresh reader restoring the saved offset sees nothing new.
	resumed := New(dir)
	resumed.SetOffset(saved)
	if recs, _ := resumed.Read(); len(recs) != 0 {
		t.Errorf("resumed reader replayed %d records", len(recs))
	}
}

// TestRemovalRecord: retries/undos must be distinguishable so their spend can
// be netted back out.
func TestRemovalRecord(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	path := filepath.Join(dir, FileName)

	write(t, path, `{"type":"message.removed","id":"m1"}`+"\n")
	recs, _ := r.Read()
	if len(recs) != 1 || recs[0].Type != "message.removed" || recs[0].ID != "m1" {
		t.Fatalf("removal record not parsed: %+v", recs)
	}
}
