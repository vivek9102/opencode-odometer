package prices

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	inR  = 3.0
	outR = 15.0
	crR  = 0.3
	cwR  = 3.75
)

func writePrices(t *testing.T, models map[string]Rate, reference string) string {
	t.Helper()
	doc := map[string]any{"models": models}
	if reference != "" {
		doc["reference_model"] = reference
	}
	path := filepath.Join(t.TempDir(), "prices.json")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func sonnetRates() map[string]Rate {
	return map[string]Rate{
		"acme/sonnet": {Name: "Sonnet", Input: inR, Output: outR, CacheRead: crR, CacheWrite: cwR},
		"acme/cheap":  {Name: "Cheap", Input: 0.1, Output: 0.4, CacheRead: 0.01, CacheWrite: 0.1},
	}
}

func newTestBook(t *testing.T) *Book {
	t.Helper()
	p := writePrices(t, sonnetRates(), "acme/sonnet")
	b, err := New(p, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func almostEqual(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestPendingSuggestionsOnlyUnknown pins the filter: the list drives a prompt,
// so a priced model appearing in it would nag the user about something that
// already works.
func TestPendingSuggestionsOnlyUnknown(t *testing.T) {
	b := newTestBook(t)
	// Touch the unknown key so the book records it, mirroring live use.
	_ = b.Entry("mystery/model-x")

	got := b.PendingSuggestions([]string{"acme/sonnet", "acme/cheap", "mystery/model-x"})
	if len(got) != 1 {
		t.Fatalf("got %d suggestions, want 1 (only the unknown): %+v", len(got), got)
	}
	if got[0].Key != "mystery/model-x" {
		t.Errorf("suggested %q, want the unknown model", got[0].Key)
	}
	if !got[0].IsUnknown {
		t.Error("suggestion should be flagged unknown")
	}
}

// TestPendingSuggestionsSkipsLocalOverrides ensures answering the prompt stops
// it being asked again.
func TestPendingSuggestionsSkipsLocalOverrides(t *testing.T) {
	p := writePrices(t, sonnetRates(), "acme/sonnet")
	overlay := filepath.Join(t.TempDir(), "prices.local.json")
	b, err := New(p, overlay)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = b.Entry("mystery/model-x")
	if len(b.PendingSuggestions([]string{"mystery/model-x"})) != 1 {
		t.Fatal("unknown model should be pending before an override is saved")
	}
	if err := b.SaveOverride(overlay, "mystery/model-x", Rate{Name: "x", Input: 1, Output: 2}); err != nil {
		t.Fatalf("SaveOverride: %v", err)
	}
	if got := b.PendingSuggestions([]string{"mystery/model-x"}); len(got) != 0 {
		t.Errorf("model with a saved override must not be suggested again: %+v", got)
	}
}

// TestPendingSuggestionEstimatesFromFamily covers the pre-filled rate: an empty
// box gives the user nothing to accept, so a family median is offered.
func TestPendingSuggestionEstimatesFromFamily(t *testing.T) {
	models := map[string]Rate{
		"acme/claude-a": {Name: "A", Family: "claude", Input: 2, Output: 10},
		"acme/claude-b": {Name: "B", Family: "claude", Input: 4, Output: 20},
		"acme/claude-c": {Name: "C", Family: "claude", Input: 6, Output: 30},
	}
	p := writePrices(t, models, "acme/claude-a")
	b, err := New(p, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = b.Entry("other/claude-unlisted")

	got := b.PendingSuggestions([]string{"other/claude-unlisted"})
	if len(got) != 1 {
		t.Fatalf("want 1 suggestion, got %d", len(got))
	}
	if got[0].SuggestedRate.Output <= 0 {
		t.Errorf("suggestion should carry a non-zero estimate, got %+v", got[0].SuggestedRate)
	}
	if !strings.Contains(got[0].Source, "estimated from") {
		t.Errorf("source should say the estimate's origin, got %q", got[0].Source)
	}
}

func TestInputOutputCost(t *testing.T) {
	// 1M input @3 + 100k output @15 = 3.00 + 1.50
	b := newTestBook(t)
	almostEqual(t, b.Cost("acme/sonnet", 1_000_000, 100_000, 0, 0), 4.50)
}

func TestCacheIsPriced(t *testing.T) {
	// Cache is ~83% of real traffic; ignoring it understates cost ~8x.
	b := newTestBook(t)
	almostEqual(t, b.Cost("acme/sonnet", 0, 0, 1_000_000, 1_000_000), crR+cwR)
}

func TestUnknownModelIsFreeButFlagged(t *testing.T) {
	b := newTestBook(t)
	almostEqual(t, b.Cost("nope/missing", 1_000_000, 1_000_000, 0, 0), 0)
	if !b.Entry("nope/missing").Unknown {
		t.Error("expected unknown model flagged")
	}
	if _, ok := b.Unknown["nope/missing"]; !ok {
		t.Error("expected unknown set to record key")
	}
}

func TestFreeDetectionBySuffix(t *testing.T) {
	b := newTestBook(t)
	if !b.IsFree("x/llama-free") {
		t.Error("-free suffix not detected")
	}
	if !b.IsFree("x/some-sovereign-model") {
		t.Error("sovereign not detected")
	}
	if b.IsFree("acme/sonnet") {
		t.Error("paid model detected as free")
	}
}

func TestFreeMarkerWinsBeforeCrossProviderFallback(t *testing.T) {
	primary := writePrices(t, map[string]Rate{
		"opencode/deepseek-v4-flash": {
			Name: "DeepSeek V4 Flash", Family: "deepseek", Input: 0.14, Output: 0.28,
		},
	}, "opencode/deepseek-v4-flash")
	b, err := New(primary, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r := b.Entry("companyhub/deepseek-v4-flash-sovereign")
	if !r.Free || !r.Sovereign {
		t.Fatalf("sovereign model matched a paid fallback: %+v", r)
	}
	if got := b.Cost("companyhub/deepseek-v4-flash-sovereign", 1_000_000, 1_000_000, 0, 0); got != 0 {
		t.Errorf("sovereign model cost = %v, want 0", got)
	}
}

func TestReferenceModelDrivesSavings(t *testing.T) {
	b := newTestBook(t)
	almostEqual(t, b.ShadowCost("x/llama-free", 1_000_000, 100_000, 0, 0), 4.50)
}

func TestMissingReferenceFallsBackNotZero(t *testing.T) {
	// A bad reference must not silently zero out savings.
	p := writePrices(t, map[string]Rate{
		"acme/sonnet": {Name: "S", Input: inR, Output: outR, CacheRead: crR, CacheWrite: cwR},
	}, "does/not-exist")
	b, err := New(p, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := b.Models[b.ReferenceModel]; !ok {
		t.Errorf("reference %q not in models", b.ReferenceModel)
	}
	// savings > 0 for a free model under the resolved reference
	if got := b.ShadowCost("x/f-free", 1_000_000, 0, 0, 0); got <= 0 {
		t.Errorf("expected positive savings, got %v", got)
	}
}

func TestOverlayMergesAndOverrides(t *testing.T) {
	primary := writePrices(t, sonnetRates(), "acme/sonnet")
	overlay := writePrices(t, map[string]Rate{
		"acme/sonnet": {Name: "Resold", Input: 3.3, Output: 16.5, CacheRead: 0.33, CacheWrite: 4.125},
		"hub/private": {Name: "Priv", Input: inR, Output: outR, CacheRead: crR, CacheWrite: cwR},
	}, "hub/private")

	b, err := New(primary, overlay)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := b.Models["hub/private"]; !ok {
		t.Error("expected overlay model added")
	}
	if got := b.Models["acme/sonnet"].Input; got != 3.3 {
		t.Errorf("expected override input 3.3, got %v", got)
	}
	if b.ReferenceModel != "hub/private" {
		t.Errorf("expected reference override hub/private, got %q", b.ReferenceModel)
	}
}

func TestNewMissingPrimaryReturnsError(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "nope.json"), ""); err == nil {
		t.Error("expected error for missing primary price file")
	}
}

func TestLooksFree(t *testing.T) {
	cases := map[string]bool{
		"x/llama-free":   true,
		"x/-free-":       true,
		"x/sovereign-7b": true,
		"provider/model": false,
		"anthropic/opus": false,
		"a/free":         false, // suffix must be "-free", not bare "free"
	}
	for key, want := range cases {
		if got := LooksFree(key); got != want {
			t.Errorf("LooksFree(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestCrossProviderFallback(t *testing.T) {
	primary := writePrices(t, map[string]Rate{
		"anthropic/claude-sonnet-5": {Name: "Claude Sonnet 5", Family: "claude", Input: 2.0, Output: 10.0},
		"openai/gpt-5.1":            {Name: "GPT-5.1", Family: "openai", Input: 1.25, Output: 10.0},
	}, "anthropic/claude-sonnet-5")

	b, err := New(primary, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Companyhub proxy alias for claude-sonnet-5
	r1 := b.Entry("companyhub/claude-sonnet-5")
	if r1.Unknown {
		t.Errorf("expected fallback match for companyhub/claude-sonnet-5, got unknown")
	}
	if r1.Input != 2.0 || r1.Output != 10.0 {
		t.Errorf("fallback rate mismatch: in=%v out=%v", r1.Input, r1.Output)
	}
	if r1.Source != "estimate from anthropic/claude-sonnet-5" {
		t.Errorf("expected source label 'estimate from anthropic/claude-sonnet-5', got %q", r1.Source)
	}

	// Opencode proxy alias for gpt-5.1
	r2 := b.Entry("opencode/gpt-5.1")
	if r2.Unknown || r2.Input != 1.25 {
		t.Errorf("expected fallback match for opencode/gpt-5.1, got in=%v unknown=%v", r2.Input, r2.Unknown)
	}
}

func TestCrossProviderFallbackIsDeterministic(t *testing.T) {
	primary := writePrices(t, map[string]Rate{
		"azure/gpt-test":      {Family: "openai", Input: 9, Output: 90},
		"openai/gpt-test":     {Family: "openai", Input: 1, Output: 10},
		"openrouter/gpt-test": {Family: "openai", Input: 5, Output: 50},
	}, "openai/gpt-test")
	b, err := New(primary, "")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		r := b.Entry("companyhub/gpt-test")
		if r.Source != "estimate from openai/gpt-test" || r.Input != 1 || r.Output != 10 {
			t.Fatalf("iteration %d selected unstable fallback: %+v", i, r)
		}
	}
}

func TestEstimatedFallbackIsOfferedForConfirmation(t *testing.T) {
	primary := writePrices(t, map[string]Rate{
		"anthropic/claude-test": {Family: "claude", Input: 2, Output: 10},
	}, "anthropic/claude-test")
	b, err := New(primary, "")
	if err != nil {
		t.Fatal(err)
	}

	got := b.PendingSuggestions([]string{"companyhub/claude-test"})
	if len(got) != 1 || got[0].IsUnknown || got[0].SuggestedRate.Output != 10 {
		t.Fatalf("estimated private-provider rate should be confirmable: %+v", got)
	}
}

func TestSaveOverrideAtomically(t *testing.T) {
	primary := writePrices(t, sonnetRates(), "acme/sonnet")
	overlayPath := filepath.Join(t.TempDir(), "prices.local.json")

	b, err := New(primary, overlayPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	override := Rate{
		Name:   "Custom GPT",
		Family: "openai",
		Input:  4.0,
		Output: 16.0,
	}
	if err := b.SaveOverride(overlayPath, "custom/gpt", override); err != nil {
		t.Fatalf("SaveOverride: %v", err)
	}

	got := b.Entry("custom/gpt")
	if got.Input != 4.0 || got.Output != 16.0 || got.Unknown {
		t.Errorf("override not loaded: %+v", got)
	}

	// Verify file on disk
	raw, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal overlay: %v", err)
	}
	if doc.Models["custom/gpt"].Input != 4.0 {
		t.Errorf("persisted override mismatch: %+v", doc.Models["custom/gpt"])
	}
}
