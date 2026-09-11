package prices

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// catalogFixture mimics models.dev: wanted providers alongside ones that must
// be skipped, including nested objects and arrays that the skipper has to walk
// past correctly.
const catalogFixture = `{
  "noise-before": {
    "models": {"junk": {"name": "Junk", "cost": {"input": 999, "output": 999}}},
    "meta": {"nested": {"deep": [1, 2, {"x": "}"}]}, "arr": [[],[{}]]}
  },
  "anthropic": {
    "models": {
      "claude-test": {"name": "Claude Test", "cost": {"input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75}},
      "no-cost-model": {"name": "Priceless"},
      "claude-free-tier-free": {"name": "Free One", "cost": {"input": 0, "output": 0}}
    }
  },
  "noise-after": {"models": {"junk2": {"name": "J2", "cost": {"input": 1, "output": 1}}}},
  "openai": {
    "models": {"gpt-test": {"name": "GPT Test", "cost": {"input": 1.25, "output": 10}}}
  }
}`

func fixtureServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

// withCatalogURL points the fetcher at a local server for the duration of a test.
func withCatalogURL(t *testing.T, url string) {
	t.Helper()
	prev := catalogURL
	catalogURL = url
	t.Cleanup(func() { catalogURL = prev })
}

func TestFetchCatalogFiltersProviders(t *testing.T) {
	srv := fixtureServer(t, catalogFixture)
	defer srv.Close()
	withCatalogURL(t, srv.URL)

	models, err := FetchCatalog([]string{"anthropic", "openai"})
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}

	// Providers outside the list must never appear, and the skipper must not
	// lose its place while walking their nested objects/arrays.
	for k := range models {
		if strings.HasPrefix(k, "noise-") {
			t.Errorf("unwanted provider leaked: %s", k)
		}
	}
	if _, ok := models["openai/gpt-test"]; !ok {
		t.Error("a wanted provider after skipped ones was lost; the skipper misaligned the stream")
	}

	r, ok := models["anthropic/claude-test"]
	if !ok {
		t.Fatal("anthropic/claude-test missing")
	}
	if r.Input != 3 || r.Output != 15 || r.CacheRead != 0.3 || r.CacheWrite != 3.75 {
		t.Errorf("rates mis-parsed: %+v", r)
	}
	if r.Family == "" {
		t.Error("family should be inferred so estimates can group by it")
	}

	// A model with no cost block cannot be accounted for and must be dropped
	// rather than recorded as free.
	if _, ok := models["anthropic/no-cost-model"]; ok {
		t.Error("model without a cost block should be skipped, not priced at zero")
	}
	if f, ok := models["anthropic/claude-free-tier-free"]; !ok || !f.Free {
		t.Errorf("free model not flagged: %+v", f)
	}
}

func TestFetchCatalogRejectsNonObject(t *testing.T) {
	srv := fixtureServer(t, `["not", "an", "object"]`)
	defer srv.Close()
	withCatalogURL(t, srv.URL)

	if _, err := FetchCatalog([]string{"anthropic"}); err == nil {
		t.Error("expected an error for a non-object payload")
	}
}

func TestFetchCatalogErrorsWhenNoProvidersMatch(t *testing.T) {
	srv := fixtureServer(t, catalogFixture)
	defer srv.Close()
	withCatalogURL(t, srv.URL)

	// Silently returning an empty table would overwrite a good price file
	// with nothing, so this must surface as an error.
	if _, err := FetchCatalog([]string{"nonexistent-provider"}); err == nil {
		t.Error("expected an error when no configured provider is present")
	}
}

func TestWriteCatalogRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/prices.json"
	models := map[string]Rate{
		"acme/one": {Name: "One", Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 1.25},
	}
	if err := WriteCatalog(path, models, "acme/one"); err != nil {
		t.Fatalf("WriteCatalog: %v", err)
	}
	b, err := New(path, "")
	if err != nil {
		t.Fatalf("written table is not loadable: %v", err)
	}
	if got := b.Cost("acme/one", 1_000_000, 0, 0, 0); got != 1 {
		t.Errorf("round-tripped input rate = %v, want 1", got)
	}
	if b.ReferenceModel != "acme/one" {
		t.Errorf("reference model lost in round trip: %q", b.ReferenceModel)
	}

	// The document must stay valid JSON with the metadata a human needs to
	// judge staleness.
	var doc map[string]any
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written catalog: %v", err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("written catalog is not valid JSON: %v", err)
	}
	if doc["_fetched"] == nil {
		t.Error("catalog should record when it was fetched")
	}
}
