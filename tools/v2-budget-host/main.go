// Headless accounting host for the isolated V2 smoke. No window, plugin
// installer, credential access or HTTP client is started by this helper.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/app"
)

func main() {
	if !app.IntegrationV2 {
		panic("build this smoke host with -tags opencode_v2")
	}
	dir := os.Getenv("OPENCODE_ODOMETER_DIR")
	if dir == "" {
		panic("isolated data directory required")
	}
	a, err := app.New(app.Config{PricesFile: filepath.Join(dir, "prices.json"), StateFile: filepath.Join(dir, "odometer_state.json"), BudgetFile: filepath.Join(dir, "budget.json"), GraceFile: filepath.Join(dir, "grace.json"), MaxMessages: 1000})
	if err != nil {
		panic(err)
	}
	for {
		a.Poll()
		var command struct {
			ID    string         `json:"id"`
			Limit float64        `json:"limit"`
			Rule  app.BudgetRule `json:"rule"`
			Clear bool           `json:"clear"`
			Stop  bool           `json:"stop"`
		}
		file := filepath.Join(dir, "smoke-command.json")
		if raw, err := os.ReadFile(file); err == nil {
			if err := json.Unmarshal(raw, &command); err != nil {
				panic(err)
			}
			if command.Stop {
				if err := a.RequestOpenSessionAbort(command.ID); err != nil {
					panic(err)
				}
			} else if err := a.SetOpenSessionBudget(command.ID, command.Limit, "hard", !command.Clear); err != nil {
				panic(err)
			}
			if !command.Clear && command.Rule.Rule != "" {
				if err := a.SetOpenSessionRule(command.ID, command.Rule); err != nil {
					panic(err)
				}
			}
			os.Remove(file)
		}
		raw, err := json.Marshal(a.OpenSessions())
		if err != nil {
			panic(err)
		}
		out := filepath.Join(dir, "smoke-rows.json")
		if err := os.WriteFile(out+".tmp", raw, 0600); err != nil {
			panic(err)
		}
		if err := os.Rename(out+".tmp", out); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
