package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/vivek9102/opencode-odometer/internal/ledger"
	"github.com/vivek9102/opencode-odometer/internal/opencode"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func v2ConversationIdentity(sid string) (string, string) {
	sum := sha256.Sum256([]byte(sid))
	code := hex.EncodeToString(sum[:8])
	words := []string{"calm", "bright", "swift", "clear", "steady", "keen"}
	animals := []string{"fox", "owl", "panda", "lynx", "otter", "robin"}
	n := int(sum[0])
	return "v2-" + code, words[n%len(words)] + "-" + animals[n/len(words)%len(animals)] + "-" + code[:8]
}

// V2 presence belongs to a terminal; rows and policies belong to conversations.
// Only reported open tabs are displayed. Saved history never creates rows.
func (a *App) refreshV2OpenSessions() bool {
	files, _ := filepath.Glob(filepath.Join(a.experienceDir(), "tui-*.json"))
	terminals := []OpenTUI{}
	found := false
	for _, file := range files {
		var t OpenTUI
		raw, err := os.ReadFile(file)
		if err != nil || json.Unmarshal(raw, &t) != nil || t.APIMajor != 2 {
			continue
		}
		found = true
		if t.ID == "" || t.PID <= 0 || t.Started <= 0 || filepath.Base(file) != "tui-"+t.ID+".json" || t.Closed || !opencode.ProcessAliveAt(t.PID, t.Updated) {
			continue
		}
		terminals = append(terminals, t)
	}
	if !found {
		a.mu.RLock()
		for id := range a.state.OpenPolicies {
			if strings.HasPrefix(id, "v2-") {
				found = true
				break
			}
		}
		a.mu.RUnlock()
		if !found {
			return false
		}
	}
	a.mu.Lock()
	if a.state.OpenPolicies == nil {
		a.state.OpenPolicies = map[string]OpenPolicy{}
	}
	rows := map[string]OpenTUI{}
	changed := !a.state.OpenSessionsMode
	a.state.OpenSessionsMode = true
	for _, terminal := range terminals {
		tabs := terminal.Tabs
		// Compatibility with the first V2 preview, before it published tab lists.
		if tabs == nil && terminal.SessionID != "" {
			tabs = []OpenTUI{{SessionID: terminal.SessionID, Model: terminal.Model, Active: terminal.Active, Focused: true}}
		}
		for _, tab := range tabs {
			sid := budgetRoot(tab.SessionID, a.state.SessionParents)
			if sid == "" {
				continue
			}
			id, name := v2ConversationIdentity(sid)
			started := tab.Started
			if started <= 0 {
				started = 1
			}
			entry := OpenTUI{ID: id, Name: name, PID: terminal.PID, Started: started, Updated: terminal.Updated, SessionID: sid, Model: tab.Model, Active: tab.Active, APIMajor: 2, TerminalID: terminal.ID, Title: tab.Title, Focused: tab.Focused}
			if time.Now().Unix()-entry.Updated > 10 {
				entry.Active = false
			}
			if old, exists := rows[id]; exists {
				active := entry.Active || old.Active
				if old.Focused && !entry.Focused || old.Focused == entry.Focused && (old.Updated > entry.Updated || old.Updated == entry.Updated && old.TerminalID < entry.TerminalID) {
					entry = old
				}
				entry.Active = active
			}
			rows[id] = entry
			if _, exists := a.state.OpenPolicies[id]; !exists {
				// Preserve an already configured first-preview budget for its bound chat.
				// A home-screen draft can transfer only to a newly created conversation.
				p, old := a.state.OpenPolicies[terminal.ID]
				if old && (p.SessionID == sid || p.SessionID == "" && tab.Focused && tab.Started >= terminal.DraftStarted && terminal.DraftStarted > 0) {
					if p.SessionID != "" {
						original, fallback := a.historicalV2SpendLocked(sid, terminal.Started, p)
						p.Baseline += original
						p.FallbackBaseline += fallback
					}
					p.SessionID = sid
					a.state.OpenPolicies[id] = p
					delete(a.state.OpenPolicies, terminal.ID)
				} else {
					a.state.OpenPolicies[id] = OpenPolicy{SessionID: sid}
				}
				changed = true
			}
		}
		if terminal.SessionID == "" {
			draft := terminal
			draft.Name = "New session"
			draft.TerminalID = terminal.ID
			draft.Started = terminal.DraftStarted
			if draft.Started <= 0 {
				draft.Started = terminal.Started
			}
			draft.Tabs = nil
			draft.Active = false
			rows[draft.ID] = draft
		}
	}
	// Conversation policies persist independently of terminal lifetimes.
	// Discard only unused terminal drafts after their owner closes.
	for id, p := range a.state.OpenPolicies {
		if !strings.HasPrefix(id, "v2-") && p.SessionID == "" {
			if _, ok := rows[id]; !ok {
				delete(a.state.OpenPolicies, id)
				changed = true
			}
		}
	}
	a.openTUIs = rows
	a.mu.Unlock()
	if changed {
		a.SaveState()
	}
	return true
}

func (a *App) historicalV2SpendLocked(sid string, started int64, p OpenPolicy) (float64, float64) {
	cutoff := time.UnixMilli(started).Format("2006-01-02 15:04:05")
	original, fallback := 0.0, 0.0
	for _, r := range a.Ledger.GetMessagesSnapshot() {
		if budgetRoot(r.SessionID, a.state.SessionParents) == sid && r.Timestamp < cutoff {
			isOriginal, known := p.OriginalIDs[r.MID]
			if !known {
				isOriginal = p.Stage != "fallback"
			}
			if isOriginal {
				original += r.Cost
			} else {
				fallback += r.Cost
			}
		}
	}
	return original, fallback
}

// Closing a visual tab must not release its budget while OpenCode still runs
// background work. Publish its policy without adding a historical UI row.
func (a *App) v2BudgetEntriesLocked() map[string]OpenTUI {
	entries := make(map[string]OpenTUI, len(a.openTUIs))
	for id, e := range a.openTUIs {
		entries[id] = e
	}
	for id, p := range a.state.OpenPolicies {
		if strings.HasPrefix(id, "v2-") && p.Enabled && p.SessionID != "" {
			if _, open := entries[id]; !open {
				entries[id] = OpenTUI{ID: id, SessionID: p.SessionID, Started: 1, APIMajor: 2}
			}
		}
	}
	return entries
}

// Preserve closed conversations' budget accounting without retaining their
// entire message history. Called while saving, after ledger pruning.
func (a *App) archiveV2PolicySpendLocked(before []ledger.Record) {
	retained := map[string]bool{}
	for _, r := range a.Ledger.GetMessagesSnapshot() {
		retained[r.MID] = true
	}
	for _, r := range before {
		if retained[r.MID] {
			continue
		}
		sid := budgetRoot(r.SessionID, a.state.SessionParents)
		id, _ := v2ConversationIdentity(sid)
		p, ok := a.state.OpenPolicies[id]
		if !ok {
			continue
		}
		original, known := p.OriginalIDs[r.MID]
		if !known {
			original = p.Stage != "fallback"
		}
		if original {
			p.ArchivedOriginal += r.Cost
		} else {
			p.ArchivedFallback += r.Cost
		}
		delete(p.OriginalIDs, r.MID)
		a.state.OpenPolicies[id] = p
	}
}
