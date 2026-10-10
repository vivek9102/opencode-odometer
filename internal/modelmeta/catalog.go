// Package modelmeta stores public model facts separately from route pricing.
// Missing measurements remain unknown; names are never used to invent ratings.
package modelmeta

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

const ModelsURL = "https://models.dev/models.json"
const BenchmarksURL = "https://artificialanalysis.ai/api/v2/data/llms/models"

type Metadata struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Family          string   `json:"family,omitempty"`
	ToolCall        *bool    `json:"tool_call,omitempty"`
	Reasoning       *bool    `json:"reasoning,omitempty"`
	Context         int64    `json:"context,omitempty"`
	InputModalities []string `json:"input_modalities,omitempty"`
	Intelligence    *float64 `json:"intelligence,omitempty"`
	Coding          *float64 `json:"coding,omitempty"`
	Speed           *float64 `json:"speed,omitempty"`
	Latency         *float64 `json:"latency,omitempty"`
	BenchmarkID     string   `json:"benchmark_id,omitempty"`
	Source          string   `json:"source,omitempty"`
}
type Snapshot struct {
	Updated          int64               `json:"updated"`
	BenchmarkUpdated int64               `json:"benchmark_updated"`
	Models           map[string]Metadata `json:"models"`
	Benchmarks       []Metadata          `json:"benchmarks"`
}
type Catalog struct {
	mu            sync.RWMutex
	refreshMu     sync.Mutex
	path          string
	data          Snapshot
	Client        *http.Client
	ModelsURL     string
	BenchmarksURL string
}

func New(path string) *Catalog {
	c := &Catalog{path: path, data: Snapshot{Models: map[string]Metadata{}}, Client: &http.Client{Timeout: 20 * time.Second}, ModelsURL: ModelsURL, BenchmarksURL: BenchmarksURL}
	b, _ := os.ReadFile(path)
	_ = json.Unmarshal(b, &c.data)
	if c.data.Models == nil {
		c.data.Models = map[string]Metadata{}
	}
	return c
}
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func (c *Catalog) Lookup(key, _ string, canonical string) Metadata {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, bare, _ := strings.Cut(key, "/")
	m := Metadata{}
	if canonical != "" {
		m = c.data.Models[canonical]
	}
	if m.ID == "" {
		m = c.data.Models[key]
	}
	if m.ID == "" {
		// Match a complete model/version only, not a family or a dated alias.
		matches := []Metadata{}
		for id, x := range c.data.Models {
			_, tail, _ := strings.Cut(id, "/")
			if normalize(tail) == normalize(bare) {
				matches = append(matches, x)
			}
		}
		if len(matches) == 1 {
			m = matches[0]
		}
	}
	candidates := []Metadata{}
	for _, b := range c.data.Benchmarks {
		_, tail, _ := strings.Cut(m.ID, "/")
		// A friendly display name can hide a dated/thinking configuration.
		// Join only exact version IDs or an explicit canonical mapping.
		if normalize(b.ID) == normalize(bare) || m.ID != "" && normalize(b.ID) == normalize(tail) {
			candidates = append(candidates, b)
		}
	}
	if len(candidates) == 1 {
		b := candidates[0]
		m.Intelligence, m.Coding, m.Speed, m.Latency, m.BenchmarkID, m.Source = b.Intelligence, b.Coding, b.Speed, b.Latency, b.BenchmarkID, b.Source
	}
	return m
}
func (c *Catalog) Age() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.data.Updated == 0 {
		return 100 * 365 * 24 * time.Hour
	}
	return time.Since(time.Unix(c.data.Updated, 0))
}
func (c *Catalog) NeedsRefresh(withBenchmarks bool) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	stale := func(updated int64) bool { return updated == 0 || time.Since(time.Unix(updated, 0)) >= 24*time.Hour }
	return stale(c.data.Updated) || withBenchmarks && stale(c.data.BenchmarkUpdated)
}
func (c *Catalog) fetch(url, key string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "OpenCode-Odometer")
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("x-api-key", key)
	}
	response, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("metadata service unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("metadata service returned HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 20<<20)).Decode(out)
}
func (c *Catalog) Refresh(key string) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	var raw map[string]json.RawMessage
	err := c.fetch(c.ModelsURL, "", &raw)
	if err == nil {
		if nested := raw["models"]; nested != nil {
			_ = json.Unmarshal(nested, &raw)
		}
		models := map[string]Metadata{}
		for id, b := range raw {
			var x struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Family    string `json:"family"`
				ToolCall  *bool  `json:"tool_call"`
				Reasoning *bool  `json:"reasoning"`
				Limit     struct {
					Context int64 `json:"context"`
				} `json:"limit"`
				Modalities struct {
					Input []string `json:"input"`
				} `json:"modalities"`
			}
			if json.Unmarshal(b, &x) != nil || x.Name == "" {
				continue
			}
			models[id] = Metadata{ID: id, Name: x.Name, Family: x.Family, ToolCall: x.ToolCall, Reasoning: x.Reasoning, Context: x.Limit.Context, InputModalities: x.Modalities.Input}
		}
		if len(models) == 0 {
			err = fmt.Errorf("model metadata response contained no models")
		} else {
			c.mu.Lock()
			c.data.Models = models
			c.data.Updated = time.Now().Unix()
			c.mu.Unlock()
		}
	}
	benchmarkErr := error(nil)
	if key != "" {
		var response struct {
			Data []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				Slug        string `json:"slug"`
				Evaluations struct {
					Intelligence *float64 `json:"artificial_analysis_intelligence_index"`
					Coding       *float64 `json:"artificial_analysis_coding_index"`
				} `json:"evaluations"`
				Speed   *float64 `json:"median_output_tokens_per_second"`
				Latency *float64 `json:"median_time_to_first_token_seconds"`
			} `json:"data"`
		}
		benchmarkErr = c.fetch(c.BenchmarksURL, key, &response)
		if benchmarkErr == nil {
			if len(response.Data) == 0 {
				benchmarkErr = fmt.Errorf("benchmark response contained no models")
			} else {
				models := []Metadata{}
				for _, m := range response.Data {
					models = append(models, Metadata{ID: m.Slug, Name: m.Name, BenchmarkID: m.ID, Intelligence: m.Evaluations.Intelligence, Coding: m.Evaluations.Coding, Speed: m.Speed, Latency: m.Latency, Source: "Artificial Analysis"})
				}
				c.mu.Lock()
				c.data.Benchmarks = models
				c.data.BenchmarkUpdated = time.Now().Unix()
				c.mu.Unlock()
			}
		}
	}
	c.mu.RLock()
	b, marshalErr := json.MarshalIndent(c.data, "", "  ")
	c.mu.RUnlock()
	if marshalErr != nil {
		return marshalErr
	}
	if err == nil || benchmarkErr == nil && key != "" {
		if writeErr := atomicWrite(c.path, b); writeErr != nil {
			return writeErr
		}
	}
	if benchmarkErr != nil {
		return benchmarkErr
	}
	return err
}
func atomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "metadata-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
