package app_test

import (
	"github.com/vivek9102/opencode-odometer/internal/opencode"
	"testing"
	"time"
)

func TestCompletedChargeSilentForHistoryAndDuplicates(t *testing.T) {
	a := newTestApp(t)
	a.SetSeeded(true)
	msg := opencode.Message{ID: "old", SessionID: "s1", Provider: "acme", Model: "cheap", Cost: .3, Finish: "stop", TimeCreated: time.Now().Add(-time.Hour).UnixMilli()}
	a.ApplyMessage(msg)
	if a.LastCharge().Sequence != 0 {
		t.Fatal("history produced a charge tick")
	}
	msg.ID = "live"
	msg.TimeCreated = time.Now().UnixMilli()
	msg.Finish = ""
	msg.Cost = .1
	a.ApplyMessage(msg)
	if a.LastCharge().Sequence != 0 {
		t.Fatal("partial stream produced a completed charge tick")
	}
	msg.Cost = .3
	msg.Finish = "stop"
	a.ApplyMessage(msg)
	if c := a.LastCharge(); c.Sequence != 1 || c.Cost != .3 {
		t.Fatalf("completed charge: %v", c)
	}
	a.ApplyMessage(msg)
	if a.LastCharge().Sequence != 1 {
		t.Fatal("replayed completion produced another charge tick")
	}
}
