package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type abortRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Instance  string `json:"instance"`
	Issued    int64  `json:"issued"`
}

type abortStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// RequestChatAbort targets the plugin that owns this chat, and returns only
// after OpenCode acknowledges cancellation. A shared, unacknowledged command
// could previously be consumed by a different OpenCode process.
func (a *App) RequestChatAbort(sid string) error {
	return a.requestChatAbort(sid, 4*time.Second)
}

func (a *App) requestChatAbort(sid string, timeout time.Duration) error {
	return a.requestChatAbortAt(sid, 0, timeout)
}

func (a *App) RequestOpenSessionAbort(id string) error {
	for _, row := range a.OpenSessions() {
		if row.ID == id {
			if row.SessionID == "" {
				return fmt.Errorf("this TUI has no running conversation")
			}
			return a.stopOpenTUI(row)
		}
	}
	return fmt.Errorf("this OpenCode TUI has closed")
}

func (a *App) stopOpenTUI(row OpenSessionRow) error {
	requestID := uuid.NewString()
	children := []string{}
	a.mu.RLock()
	for sid := range a.state.SessionParents {
		if sid != row.SessionID && budgetRoot(sid, a.state.SessionParents) == row.SessionID {
			children = append(children, sid)
		}
	}
	a.mu.RUnlock()
	req := struct {
		abortRequest
		Children []string `json:"children"`
	}{abortRequest: abortRequest{ID: requestID, SessionID: row.SessionID, Instance: row.ID, Issued: time.Now().Unix()}, Children: children}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	target := filepath.Join(a.experienceDir(), "stop-tui-"+row.ID+".json")
	statusFile := filepath.Join(a.experienceDir(), "abort-status-"+requestID+".json")
	if err = atomicExperienceFile(target, raw); err != nil {
		return err
	}
	defer os.Remove(target)
	defer os.Remove(statusFile)
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			return fmt.Errorf("OpenCode did not acknowledge Stop Session; press Esc in that TUI")
		case <-tick.C:
			var status abortStatus
			raw, err := os.ReadFile(statusFile)
			if err != nil || json.Unmarshal(raw, &status) != nil || status.ID != requestID {
				continue
			}
			if status.Status != "stopped" {
				return fmt.Errorf("%s", status.Detail)
			}
			return nil
		}
	}
}

func (a *App) requestChatAbortAt(sid string, preferredPID int, timeout time.Duration) error {
	if sid == "" {
		return fmt.Errorf("no active chat to stop")
	}
	var owner ModelInventory
	files, _ := filepath.Glob(filepath.Join(a.experienceDir(), "models-*.json"))
	for _, file := range files {
		var inv ModelInventory
		b, err := os.ReadFile(file)
		if err != nil || json.Unmarshal(b, &inv) != nil || inv.AbortProtocol < 1 || inv.PID <= 0 || inv.Instance == "" || time.Now().Unix()-inv.Updated > 30 {
			continue
		}
		if preferredPID != 0 && inv.PID != preferredPID {
			continue
		}
		for _, observed := range inv.Sessions {
			if a.ChatRoot(observed) == a.ChatRoot(sid) && inv.Updated >= owner.Updated {
				owner = inv
				break
			}
		}
	}
	if owner.PID == 0 {
		return fmt.Errorf("restart OpenCode to load the updated Stop Session plugin, then send a message in this chat")
	}
	req := abortRequest{ID: uuid.NewString(), SessionID: a.ChatRoot(sid), Instance: owner.Instance, Issued: time.Now().Unix()}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	target := filepath.Join(a.experienceDir(), fmt.Sprintf("abort-%d-%s.json", owner.PID, req.ID))
	statusFile := filepath.Join(a.experienceDir(), "abort-status-"+req.ID+".json")
	defer os.Remove(target)
	defer os.Remove(statusFile)
	if err = atomicExperienceFile(target, b); err != nil {
		return err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-timer.C:
			return fmt.Errorf("OpenCode did not acknowledge Stop Session; press Esc there and restart OpenCode to reconnect the plugin")
		case <-tick.C:
			var status abortStatus
			b, err := os.ReadFile(statusFile)
			if err != nil || json.Unmarshal(b, &status) != nil || status.ID != req.ID {
				continue
			}
			if status.Status == "stopped" {
				a.LogEvent("abort acknowledged session=%s", req.SessionID)
				return nil
			}
			return fmt.Errorf("%s", status.Detail)
		}
	}
}
