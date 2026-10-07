package app_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/app"
)

func TestStopRoutesToChatOwnerAndWaitsForAcknowledgement(t *testing.T) {
	for _, status := range []string{"stopped", "failed"} {
		t.Run(status, func(t *testing.T) {
			a := newTestApp(t)
			a.SetSessionParent("child", "root")
			dir := filepath.Dir(a.Contract.BudgetFile)
			for pid, sessions := range map[int][]string{991: {"child"}, 992: {"other"}} {
				b, _ := json.Marshal(app.ModelInventory{AbortProtocol: 1, PID: pid, Instance: fmt.Sprintf("instance-%d", pid), Sessions: sessions, Updated: time.Now().Unix()})
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("models-%d.json", pid)), b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			received := make(chan map[string]any, 1)
			go func() {
				until := time.Now().Add(3 * time.Second)
				for time.Now().Before(until) {
					files, _ := filepath.Glob(filepath.Join(dir, "abort-991-*.json"))
					if len(files) > 0 {
						var req map[string]any
						b, err := os.ReadFile(files[0])
						if err != nil || json.Unmarshal(b, &req) != nil {
							continue
						}
						id, ok := req["id"].(string)
						if !ok || id == "" {
							continue
						}
						received <- req
						b, _ = json.Marshal(map[string]any{"id": req["id"], "status": status, "detail": "SDK rejected stop"})
						_ = os.WriteFile(filepath.Join(dir, "abort-status-"+id+".json"), b, 0600)
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
			}()
			err := a.RequestChatAbort("child")
			if status == "stopped" && err != nil {
				t.Fatal(err)
			}
			if status == "failed" && (err == nil || !strings.Contains(err.Error(), "SDK rejected stop")) {
				t.Fatalf("unconfirmed stop reported success: %v", err)
			}
			select {
			case req := <-received:
				if req["session_id"] != "root" || req["instance"] != "instance-991" {
					t.Fatalf("wrong target: %v", req)
				}
			default:
				t.Fatal("no request received")
			}
			files, _ := filepath.Glob(filepath.Join(dir, "abort-*.json"))
			if len(files) != 0 {
				t.Fatalf("stop files left behind: %v", files)
			}
		})
	}
}

func TestStopRejectsOldPluginInsteadOfSilentlyQueuing(t *testing.T) {
	a := newTestApp(t)
	b, _ := json.Marshal(app.ModelInventory{PID: 991, SwitchProtocol: 3, Instance: "old", Sessions: []string{"root"}, Updated: time.Now().Unix()})
	_ = os.WriteFile(filepath.Join(filepath.Dir(a.Contract.BudgetFile), "models-991.json"), b, 0600)
	if err := a.RequestChatAbort("root"); err == nil || !strings.Contains(err.Error(), "restart OpenCode") {
		t.Fatalf("old plugin accepted unacknowledged stop: %v", err)
	}
}
