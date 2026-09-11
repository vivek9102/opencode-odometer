package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vivek9102/opencode-odometer/internal/opencode"
)

// stubClient implements the app.Client interface.
type stubClient struct {
	sessions  []opencode.Session
	messages  map[string][]opencode.Message
	subscribe func(func(opencode.Event) error) error
}

func (s *stubClient) Session() ([]opencode.Session, error) {
	return s.sessions, nil
}

func (s *stubClient) Messages(id string) ([]opencode.Message, error) {
	return s.messages[id], nil
}

func (s *stubClient) Subscribe(h func(opencode.Event) error) error {
	if s.subscribe != nil {
		return s.subscribe(h)
	}
	return nil
}

const testPrices = `{
  "reference_model": "acme/sonnet",
  "models": {
    "acme/sonnet": {"name":"Sonnet","input":3.0,"output":15.0,"cache_read":0.3,"cache_write":3.75},
    "acme/free-model": {"name":"Free","free":true,"input":0.0,"output":0.0,"cache_read":0.0,"cache_write":0.0}
  }
}`

func newTestApp(t *testing.T) (*App, string, string) {
	t.Helper()
	dir := t.TempDir()
	pricesFile := filepath.Join(dir, "prices.json")
	if err := os.WriteFile(pricesFile, []byte(testPrices), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{
		PricesFile:  pricesFile,
		StateFile:   filepath.Join(dir, "state.json"),
		BudgetFile:  filepath.Join(dir, "budget.json"),
		GraceFile:   filepath.Join(dir, "grace.json"),
		MaxMessages: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, filepath.Join(dir, "budget.json"), filepath.Join(dir, "state.json")
}

func asst(id, sid, prov, model string, cost float64) opencode.Message {
	return opencode.Message{
		ID: id, SessionID: sid, Provider: prov, Model: model,
		Tokens: opencode.TokenUsage{
			Input: 1_000_000, Output: 100_000, Reasoning: 0,
			Cache: opencode.CacheUsage{Read: 1_000_000, Write: 1_000_000},
		},
		Cost: cost,
	}
}

func readBudget(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read budget: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse budget: %v", err)
	}
	return doc
}

// zeroMsg is a turn that produced no billable tokens, which is how abandoned
// tabs appear in the ledger.
func zeroMsg(id, sid string) opencode.Message {
	return opencode.Message{ID: id, SessionID: sid, Provider: "acme", Model: "sonnet"}
}

// TestPublishBudgetDropsDeadSessions covers the abandoned-tab pruning: a real
// run accumulated 60 published sessions of which 59 were at zero cost, burying
// the one session that could actually be blocked.
func TestPublishBudgetDropsDeadSessions(t *testing.T) {
	a, budgetFile, _ := newTestApp(t)
	a.state.Seeded = true

	a.ApplyMessage(asst("m1", "live", "acme", "sonnet", 0))
	for i := 0; i < 5; i++ {
		a.ApplyMessage(zeroMsg(fmt.Sprintf("z%d", i), fmt.Sprintf("dead%d", i)))
	}
	a.PublishBudget()

	doc := readBudget(t, budgetFile)
	sessions, _ := doc["sessions"].(map[string]any)
	if _, ok := sessions["live"]; !ok {
		t.Fatal("session with spend must be published")
	}
	if len(sessions) != 1 {
		t.Errorf("published %d sessions, want only the live one: %v", len(sessions), sessions)
	}
}

// TestPublishBudgetKeepsZeroCostSessionWithState guards the pruning: a session
// may sit at zero cost yet still carry an override or a spent grace turn.
// Dropping it would silently hand it a clean slate.
func TestPublishBudgetKeepsZeroCostSessionWithState(t *testing.T) {
	a, budgetFile, _ := newTestApp(t)
	a.state.Seeded = true

	a.ApplyMessage(zeroMsg("z1", "granted"))
	a.ApplyMessage(zeroMsg("z2", "graced"))
	a.ApplyMessage(zeroMsg("z3", "plain"))

	a.mu.Lock()
	a.state.BudgetAllow["granted"] = 5
	a.graceUsed["graced"] = 1
	a.mu.Unlock()

	a.PublishBudget()

	doc := readBudget(t, budgetFile)
	sessions, _ := doc["sessions"].(map[string]any)
	for _, sid := range []string{"granted", "graced"} {
		if _, ok := sessions[sid]; !ok {
			t.Errorf("session %q carries enforcement state and must survive pruning", sid)
		}
	}
	if _, ok := sessions["plain"]; ok {
		t.Error("zero-cost session with no state should be pruned")
	}
}

// TestIgnorePriceIsPersistent covers the dismissal: pricing re-flags an unknown
// model on every message, so a non-persistent ignore would reappear next turn.
func TestIgnorePriceIsPersistent(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.state.Seeded = true
	a.ApplyMessage(asst("m1", "s1", "mystery", "model-x", 0))

	if len(a.PendingPriceSuggestions()) != 1 {
		t.Fatalf("unpriced model should be suggested, got %+v", a.PendingPriceSuggestions())
	}
	a.IgnorePrice("mystery/model-x")
	if got := a.PendingPriceSuggestions(); len(got) != 0 {
		t.Fatalf("ignored model must not be suggested, got %+v", got)
	}
	// Another turn on the same model must not resurrect the prompt.
	a.ApplyMessage(asst("m2", "s1", "mystery", "model-x", 0))
	if got := a.PendingPriceSuggestions(); len(got) != 0 {
		t.Errorf("ignore did not survive a further message: %+v", got)
	}
}

func TestApplyMessagePricesAndLedger(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.state.Seeded = true
	m := asst("m1", "s1", "acme", "sonnet", 0)
	// 1M input @3 + 100k output @15 + 1M cache_read @0.3 + 1M cache_write @3.75
	// = 3 + 1.5 + 0.3 + 3.75 = 8.55
	if !a.ApplyMessage(m) {
		t.Fatal("expected change")
	}
	if got := a.Ledger.TotalCost; got != 8.55 {
		t.Errorf("total cost = %v, want 8.55", got)
	}
	if a.Ledger.Messages["m1"].Model != "sonnet" {
		t.Errorf("record model = %v", a.Ledger.Messages["m1"].Model)
	}
	// applying the same message again is idempotent
	if a.ApplyMessage(m) {
		t.Error("re-apply of same tokens should be idempotent")
	}
}

func TestApplyMessageFreeModelSaves(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.state.Seeded = true
	m := asst("m1", "s1", "acme", "free-model", 0)
	a.ApplyMessage(m)
	if a.Ledger.TotalCost != 0 {
		t.Errorf("free model cost = %v, want 0", a.Ledger.TotalCost)
	}
	// shadow cost at the reference (sonnet) rate > 0
	if a.Ledger.Messages["m1"].Saved <= 0 {
		t.Errorf("expected positive savings, got %v", a.Ledger.Messages["m1"].Saved)
	}
}

func TestPublishBudgetSchema(t *testing.T) {
	a, budgetPath, _ := newTestApp(t)
	a.Budget.Cfg.Enabled = true
	a.Budget.Cfg.SessionLimitUSD = 10.0
	a.state.Seeded = true
	a.Rebase(false)
	a.ApplyMessage(asst("m1", "s1", "acme", "sonnet", 8.55))
	a.PublishBudget()

	doc := readBudget(t, budgetPath)
	if doc["enabled"] != true {
		t.Errorf("enabled = %v", doc["enabled"])
	}
	ss := doc["sessions"].(map[string]any)
	s1 := ss["s1"].(map[string]any)
	if s1["state"] != "warn" { // 8.55/10 = 0.855 -> warn
		t.Errorf("s1 state = %v, want warn", s1["state"])
	}
	if s1["enforced"] != true {
		t.Errorf("enforced = %v, want true", s1["enforced"])
	}
	if s1["cost"] != 8.55 {
		t.Errorf("s1 cost = %v, want 8.55", s1["cost"])
	}
}

func TestBudgetBaselineGrandfathersSpend(t *testing.T) {
	a, budgetPath, _ := newTestApp(t)
	a.Budget.Cfg.SessionLimitUSD = 1.0
	a.state.Seeded = true
	// session already spent $9 via apply
	a.ApplyMessage(asst("m1", "s1", "acme", "sonnet", 9.0))
	// enable budget now: baseline captured at $9
	a.Budget.Cfg.Enabled = true
	a.Rebase(false)
	a.PublishBudget()
	doc := readBudget(t, budgetPath)
	s1 := doc["sessions"].(map[string]any)["s1"].(map[string]any)
	if s1["state"] != "ok" {
		t.Errorf("expected ok after enable (grandfathered), got %v", s1["state"])
	}
	if s1["cost"] != 0.0 {
		t.Errorf("effective cost should be 0 after baseline, got %v", s1["cost"])
	}
}

func TestSeedHistorySetsTripBaseline(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.Client = &stubClient{
		sessions: []opencode.Session{{ID: "s1"}},
		messages: map[string][]opencode.Message{
			"s1": {asst("m1", "s1", "acme", "sonnet", 5.0)},
		},
	}
	if err := a.SeedHistory(); err != nil {
		t.Fatalf("SeedHistory: %v", err)
	}
	if !a.state.Seeded {
		t.Error("seeded not set")
	}
	// the message is re-priced from tokens by ApplyMessage: 8.55
	if a.Ledger.TotalCost != 8.55 {
		t.Errorf("total cost = %v, want 8.55", a.Ledger.TotalCost)
	}
	if a.Ledger.TripCost != 0 {
		t.Errorf("trip cost after seed = %v, want 0", a.Ledger.TripCost)
	}
}

func TestAllowMoreRaisesCeiling(t *testing.T) {
	a, budgetPath, _ := newTestApp(t)
	a.Budget.Cfg.Enabled = true
	a.Budget.Cfg.SessionLimitUSD = 10.0
	a.state.Seeded = true
	a.Rebase(false)
	a.ApplyMessage(asst("m1", "s1", "acme", "sonnet", 15.0)) // over
	a.AllowMore("s1", 15.0, 10.0)
	a.PublishBudget()
	doc := readBudget(t, budgetPath)
	s1 := doc["sessions"].(map[string]any)["s1"].(map[string]any)
	if s1["state"] != "ok" {
		t.Errorf("expected ok after allow-more, got %v", s1["state"])
	}
}

func TestSaveLoadStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pricesFile := filepath.Join(dir, "prices.json")
	if err := os.WriteFile(pricesFile, []byte(testPrices), 0o644); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(dir, "state.json")
	budgetFile := filepath.Join(dir, "budget.json")

	mk := func() *App {
		a, err := New(Config{
			PricesFile:  pricesFile,
			StateFile:   stateFile,
			BudgetFile:  budgetFile,
			GraceFile:   filepath.Join(dir, "grace.json"),
			MaxMessages: 100,
		})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	a := mk()
	a.state.Seeded = true
	a.ApplyMessage(asst("m1", "s1", "acme", "sonnet", 8.55))
	a.SaveState()

	a2 := mk() // reloads state.json
	if a2.Ledger.TotalCost != 8.55 {
		t.Errorf("total cost after reload = %v, want 8.55", a2.Ledger.TotalCost)
	}
	if a2.Ledger.Messages["m1"].Model != "sonnet" {
		t.Errorf("reloaded record = %+v", a2.Ledger.Messages["m1"])
	}
}

// TestLoadStateLegacyFlat ensures the old flat on-disk schema (ledger fields
// at the top level, no nested "ledger" key) is migrated so historical data is
// not lost.
func TestLoadStateLegacyFlat(t *testing.T) {
	dir := t.TempDir()
	pricesFile := filepath.Join(dir, "prices.json")
	if err := os.WriteFile(pricesFile, []byte(testPrices), 0o644); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(dir, "state.json")
	budgetFile := filepath.Join(dir, "budget.json")

	flat := `{
	  "total_cost": 377.275,
	  "trip_cost": 1.0,
	  "total_saved": 2.5,
	  "seeded": true,
	  "view": "TRIP",
	  "compact": false,
	  "dock": "bottom-center",
	  "pruned_cost": 3.25,
	  "messages": {
	    "msg_1": {"mid":"msg_1","session_id":"ses_x","provider":"acme",
	      "model":"sonnet","free":false,"in":100,"out":50,
	      "reasoning":0,"cache_read":20,"cache_write":5,
	      "cost":1.25,"saved":0.0,"timestamp":"2026-04-20 15:50:18"},
	    "msg_2": {"mid":"msg_2","session_id":"ses_x","provider":"acme",
	      "model":"sonnet","free":false,"in":200,"out":100,
	      "reasoning":0,"cache_read":40,"cache_write":10,
	      "cost":2.5,"saved":0.0,"timestamp":"2026-04-21 09:00:00"}
	  }
	}`
	if err := os.WriteFile(stateFile, []byte(flat), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := New(Config{
		PricesFile:  pricesFile,
		StateFile:   stateFile,
		BudgetFile:  budgetFile,
		GraceFile:   filepath.Join(dir, "grace.json"),
		MaxMessages: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(a.Ledger.Messages) != 2 {
		t.Fatalf("expected 2 messages after flat migration, got %d", len(a.Ledger.Messages))
	}
	if a.Ledger.TotalCost != 7.0 { // messages 3.75 + pruned 3.25
		t.Errorf("total cost = %v, want 7.0", a.Ledger.TotalCost)
	}
	if a.Ledger.PrunedCost != 3.25 {
		t.Errorf("pruned cost = %v, want 3.25", a.Ledger.PrunedCost)
	}
	if a.state.View != "TRIP" || !a.state.Seeded {
		t.Errorf("state fields not migrated: view=%q seeded=%v", a.state.View, a.state.Seeded)
	}
}

func TestSyncRecentIncremental(t *testing.T) {
	a, _, _ := newTestApp(t)
	stub := &stubClient{
		sessions: []opencode.Session{
			{ID: "s1", Time: struct {
				Created int64 `json:"created"`
				Updated int64 `json:"updated"`
			}{Created: 1000, Updated: 1000}},
		},
		messages: map[string][]opencode.Message{
			"s1": {asst("m1", "s1", "acme", "sonnet", 0)},
		},
	}
	a.Client = stub

	// Initial seed
	if err := a.SeedHistory(); err != nil {
		t.Fatalf("SeedHistory: %v", err)
	}
	if a.Ledger.TotalCost != 8.55 || a.Ledger.TripCost != 0 {
		t.Fatalf("after seed: total=%v trip=%v", a.Ledger.TotalCost, a.Ledger.TripCost)
	}

	// First Poll without updates should do nothing
	a.Poll()
	if a.Ledger.TotalCost != 8.55 {
		t.Errorf("unexpected cost change: %v", a.Ledger.TotalCost)
	}

	// Update session s1 with a new message m2
	stub.sessions[0].Time.Updated = 2000
	stub.messages["s1"] = append(stub.messages["s1"], asst("m2", "s1", "acme", "sonnet", 0))

	// Poll should detect the update and ingest m2
	a.Poll()
	// 2 messages @ 8.55 = 17.10 total, trip = 8.55
	if a.Ledger.TotalCost != 17.10 {
		t.Errorf("total cost = %v, want 17.10", a.Ledger.TotalCost)
	}
	if a.Ledger.TripCost != 8.55 {
		t.Errorf("trip cost = %v, want 8.55", a.Ledger.TripCost)
	}
	if a.BurnRate() <= 0 {
		t.Errorf("burn rate should be positive, got %v", a.BurnRate())
	}
}

