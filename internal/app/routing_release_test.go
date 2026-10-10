package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBudgetClearReleasesFallbackAndLegacyPinButPreservesManual(t *testing.T) {
	for _, source := range []string{"budget", "budget_restore", "", "manual"} {
		t.Run("source-"+source, func(t *testing.T) {
			a := ruleFixture(t)
			setRule(t, a, "acme/cheap", "stop")
			addOpenSpend(a, "original", "chat-one", .2)
			a.ProcessBudgetRules()
			acknowledge(t, a, "prepared", "original")
			addOpenSpend(a, "fallback", "chat-one", .06)
			a.ProcessBudgetRules()
			if !a.OpenSessions()[0].Stopped {
				t.Fatal("fallback did not stop")
			}
			choice := ModelSwitch{ID: "fixture-pin", SessionID: "chat-one", Key: "acme/sonnet", Source: source, Persistent: true}
			b, _ := json.Marshal(choice)
			path := filepath.Join(a.experienceDir(), "switch-session-chat-one.json")
			if e := os.WriteFile(path, b, 0600); e != nil {
				t.Fatal(e)
			}
			// Another conversation's manual choice must also be untouched.
			other := filepath.Join(a.experienceDir(), "switch-session-chat-two.json")
			if e := os.WriteFile(other, b, 0600); e != nil {
				t.Fatal(e)
			}
			if e := a.SetOpenSessionBudget("one", 0, "hard", false); e != nil {
				t.Fatal(e)
			}
			_, err := os.Stat(path)
			if source == "manual" && err != nil {
				t.Fatal("manual choice cleared", err)
			}
			if source != "manual" && !os.IsNotExist(err) {
				t.Fatal("budget pin survived", err)
			}
			if _, err = os.Stat(other); err != nil {
				t.Fatal("another conversation changed", err)
			}
			if row := a.OpenSessions()[0]; row.Stopped || row.OnFallback || row.Enabled {
				t.Fatal("clear did not release verdict", row)
			}
		})
	}
}

func TestRestartReleasesLegacyRestorationOnly(t *testing.T) {
	for _, source := range []string{"", "manual"} {
		t.Run("source-"+source, func(t *testing.T) {
			a := ruleFixture(t)
			setRule(t, a, "acme/cheap", "stop")
			choice := ModelSwitch{ID: "old-restoration", SessionID: "chat-one", Key: "acme/sonnet", Persistent: true, Source: source}
			b, _ := json.Marshal(choice)
			path := filepath.Join(a.experienceDir(), "switch-session-chat-one.json")
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			a.SaveState()
			if _, err := New(a.cfg); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(path)
			if source == "" && !os.IsNotExist(err) || source == "manual" && err != nil {
				t.Fatal("incorrect upgrade routing treatment", source, err)
			}
		})
	}
}
