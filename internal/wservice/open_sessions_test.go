package wservice

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/app"
	"github.com/vivek9102/opencode-odometer/internal/ledger"
)

func TestOpenSessionSelectionAndClosedFallback(t *testing.T) {
	dir := t.TempDir()
	core, err := app.New(app.Config{StateFile: filepath.Join(dir, "state.json"), BudgetFile: filepath.Join(dir, "budget.json"), GraceFile: filepath.Join(dir, "grace.json"), OpenSessionsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	svc := New()
	svc.App = core
	report := func(id string, closed bool) {
		entry := app.OpenTUI{ID: id, Name: "test-" + id, PID: os.Getpid(), Started: time.Now().Add(-time.Minute).UnixMilli(), Updated: time.Now().Unix(), SessionID: "chat-" + id, Closed: closed}
		raw, _ := json.Marshal(entry)
		if err := os.WriteFile(filepath.Join(dir, "tui-"+id+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	report("one", false)
	report("two", false)
	if err := svc.SelectOpenSession("two"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetOpenSessionBudget("two", 1, "hard", true); err != nil {
		t.Fatal(err)
	}
	for _, r := range []ledger.Record{
		{MID: "one", SessionID: "chat-one", Provider: "mock", Model: "one", Cost: .2},
		{MID: "two", SessionID: "chat-two", Provider: "mock", Model: "two", Cost: 1.1},
		{MID: "history", SessionID: "closed-chat", Provider: "mock", Model: "old", Cost: 9},
	} {
		r.Timestamp = time.Now().Format("2006-01-02 15:04:05")
		core.Ledger.Put(r)
	}
	snap := svc.Snapshot()
	if snap.SelectedSession != "two" || snap.SessionID != "chat-two" || snap.SessionCount != 2 || snap.BudgetState != "over" || len(snap.Rows) != 3 {
		t.Fatalf("selected target/global usage incorrect: %+v", snap)
	}
	if snap.OpenCost < 1.299999 || snap.OpenCost > 1.300001 {
		t.Fatalf("dock included closed history: %v", snap.OpenCost)
	}
	svc.SetCounter("RUN")
	run := svc.Snapshot()
	if run.View != "RUN" || run.Cost != run.OpenCost || run.Limit != 1 || run.SessionCost != 1.1 {
		t.Fatalf("RUN differs from dock or changed the selected policy: %+v", run)
	}
	svc.SetCounter("TOTAL")
	total := svc.Snapshot()
	if total.Cost < 10.299999 || total.SessionCost != run.SessionCost || total.Limit != run.Limit {
		t.Fatal("counter changed policy or lost closed history")
	}
	report("two", true)
	snap = svc.Snapshot()
	if snap.SelectedSession != "one" || snap.BudgetEnabled || snap.SessionID != "chat-one" || snap.SessionCount != 1 {
		t.Fatalf("closed selection inherited a cap: %+v", snap)
	}
	if err := svc.SelectOpenSession("two"); err == nil {
		t.Fatal("closed target accepted")
	}
}
