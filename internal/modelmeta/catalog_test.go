package modelmeta

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogExactMatchingCacheAndKeyPrivacy(t *testing.T) {
	failed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failed {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/models" {
			if r.Header.Get("x-api-key") != "" {
				t.Error("key leaked to models.dev")
			}
			w.Write([]byte(`{"vendor/code-5":{"name":"Code 5","tool_call":true,"reasoning":true,"limit":{"context":200000},"modalities":{"input":["text","image"]}}}`))
			return
		}
		if r.Header.Get("x-api-key") != "secret-test-only" {
			t.Error("missing benchmark authorization")
		}
		w.Write([]byte(`{"data":[{"id":"benchmark-1","name":"Code 5","slug":"code-5","evaluations":{"artificial_analysis_coding_index":52.4,"artificial_analysis_intelligence_index":49.2},"median_output_tokens_per_second":91.3}]}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "cache.json")
	c := New(path)
	c.ModelsURL = server.URL + "/models"
	c.BenchmarksURL = server.URL + "/benchmarks"
	if err := c.Refresh("secret-test-only"); err != nil {
		t.Fatal(err)
	}
	if c.NeedsRefresh(true) {
		t.Fatal("fresh metadata unnecessarily refreshes")
	}
	c.data.BenchmarkUpdated = 0
	if !c.NeedsRefresh(true) || c.NeedsRefresh(false) {
		t.Fatal("missing benchmarks do not refresh independently of capabilities")
	}
	m := c.Lookup("companyhub/code-5", "Code 5", "")
	if m.Coding == nil || *m.Coding != 52.4 || m.Context != 200000 || m.ToolCall == nil || !*m.ToolCall {
		t.Fatalf("exact cross-provider match: %+v", m)
	}
	if x := c.Lookup("companyhub/code-5-thinking", "Code 5 Thinking", ""); x.Coding != nil || x.ToolCall != nil {
		t.Fatal("version mismatch was rated")
	}
	if x := c.Lookup("companyhub/code-5-thinking", "Code 5", ""); x.Coding != nil {
		t.Fatal("friendly label hid a different configuration")
	}
	m = c.Lookup("companyhub/routed-model", "Alias", "vendor/code-5")
	if m.Coding == nil {
		t.Fatal("canonical mapping not used")
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "secret-test-only") {
		t.Fatal("key in public cache")
	}
	failed = true
	if err := c.Refresh("secret-test-only"); err == nil {
		t.Fatal("outage unreported")
	}
	if m = New(path).Lookup("companyhub/code-5", "Code 5", ""); m.Coding == nil {
		t.Fatal("outage destroyed cached metadata")
	}
	c.data.Benchmarks = append(c.data.Benchmarks, c.data.Benchmarks[0])
	if c.Lookup("companyhub/code-5", "Code 5", "").Coding != nil {
		t.Fatal("ambiguous rating accepted")
	}
}
