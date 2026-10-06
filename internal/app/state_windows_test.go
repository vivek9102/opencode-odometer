package app

import (
	"os"
	"testing"
	"time"
)

func TestBudgetSaveSurvivesBriefWindowsReadLock(t *testing.T) {
	a := enabledDollarBudget(t)
	reader, err := os.Open(a.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	a.ApplyMessage(asst("after-read-lock", "s1", "acme", "sonnet", .2))
	done := make(chan struct{})
	go func() { a.SaveState(); close(done) }()
	time.Sleep(80 * time.Millisecond)
	reader.Close()
	<-done
	b, err := New(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	assertBudgetSpend(t, b, .6)
}
