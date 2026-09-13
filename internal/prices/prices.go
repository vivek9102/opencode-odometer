// Package prices loads and queries the local model price table.
//
// prices.json is authoritative. Every figure is USD per 1,000,000 tokens.
// An optional private overlay (prices.local.json) is merged on top at
// startup so regenerating the public table never clobbers internal/reseller
// rates, and those rates never have to reach git.
package prices

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Rate is the per-1M-token price table for one model.
type Rate struct {
	Name       string  `json:"name"`
	Family     string  `json:"family,omitempty"`
	Source     string  `json:"source,omitempty"`
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	Free       bool    `json:"free,omitempty"`
	Sovereign  bool    `json:"sovereign,omitempty"`
	Unknown    bool    `json:"unknown,omitempty"`
}

// PriceSuggestion bundles an unconfigured model key with its best suggested rate.
type PriceSuggestion struct {
	Key           string `json:"key"`
	SuggestedRate Rate   `json:"suggested_rate"`
	Source        string `json:"source"`
	IsUnknown     bool   `json:"is_unknown"`
}

// document is the on-disk shape of prices.json / prices.local.json.
type document struct {
	Models         map[string]Rate `json:"models"`
	ReferenceModel string          `json:"reference_model,omitempty"`
}

// Book is the authoritative price table plus an optional overlay.
type Book struct {
	mu sync.RWMutex
	// catalogModels holds entries from prices.json.
	catalogModels map[string]Rate
	// localModels holds entries from prices.local.json (user overrides).
	localModels map[string]Rate
	// Models maps a "provider/model" key to its merged rate.
	Models map[string]Rate
	// ReferenceModel is the paid model used to value free-model savings.
	ReferenceModel string
	// Unknown models encountered so far (used for reporting).
	Unknown map[string]bool

	primary string
	overlay string
}

// FallbackReference is used when neither the price file nor the overlay
// declares a reference_model.
const FallbackReference = "anthropic/claude-sonnet-4-5"

// New builds a Book from a primary price file and an optional overlay file.
// If primary cannot be loaded, an error is returned.
func New(primaryPath, overlayPath string) (*Book, error) {
	b := &Book{
		catalogModels:  map[string]Rate{},
		localModels:    map[string]Rate{},
		Models:         map[string]Rate{},
		ReferenceModel: FallbackReference,
		Unknown:        map[string]bool{},
		primary:        primaryPath,
		overlay:        overlayPath,
	}

	if err := b.loadCatalog(primaryPath); err != nil {
		return nil, err
	}
	if overlayPath != "" && fileExists(overlayPath) {
		if err := b.loadOverlay(overlayPath); err != nil {
			fmt.Fprintf(os.Stderr, "[prices] overlay load failed %s: %v\n", overlayPath, err)
		}
	}

	b.fixupReference()
	return b, nil
}

// Reload re-reads both price files from disk.
func (b *Book) Reload() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.catalogModels = map[string]Rate{}
	b.localModels = map[string]Rate{}
	b.Models = map[string]Rate{}

	if err := b.loadCatalog(b.primary); err != nil {
		return err
	}
	if b.overlay != "" && fileExists(b.overlay) {
		if err := b.loadOverlay(b.overlay); err != nil {
			fmt.Fprintf(os.Stderr, "[prices] overlay reload failed %s: %v\n", b.overlay, err)
		}
	}
	b.fixupReference()
	return nil
}

func (b *Book) loadCatalog(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	for k, r := range doc.Models {
		if r.Family == "" {
			r.Family = InferFamily(k)
		}
		b.catalogModels[k] = r
		b.Models[k] = r
	}
	if doc.ReferenceModel != "" {
		b.ReferenceModel = doc.ReferenceModel
	}
	return nil
}

func (b *Book) loadOverlay(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	for k, r := range doc.Models {
		if r.Family == "" {
			r.Family = InferFamily(k)
		}
		b.localModels[k] = r
		b.Models[k] = r
	}
	if doc.ReferenceModel != "" {
		b.ReferenceModel = doc.ReferenceModel
	}
	if len(doc.Models) > 0 {
		fmt.Fprintf(os.Stderr, "[prices] overlay: +%d models\n", len(doc.Models))
	}
	return nil
}

// fixupReference guarantees ReferenceModel is a real, paid entry in Models.
func (b *Book) fixupReference() {
	if _, ok := b.Models[b.ReferenceModel]; ok {
		return
	}
	if _, ok := b.Models[FallbackReference]; ok {
		b.ReferenceModel = FallbackReference
		return
	}
	var best string
	bestOutput := 0.0
	for k, r := range b.Models {
		if r.Free || r.Output <= 0 {
			continue
		}
		if r.Output > bestOutput {
			bestOutput = r.Output
			best = k
		}
	}
	if best != "" {
		b.ReferenceModel = best
	}
}

// NormalizeKey standardizes provider and model identifiers into a canonical "provider/model" key.
func NormalizeKey(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)

	if provider == "" {
		return model
	}

	if strings.HasPrefix(model, provider+"/") {
		return model
	}

	if strings.Contains(model, "/") {
		return model
	}

	return provider + "/" + model
}

// InferFamily returns the model family group (e.g. "claude", "openai", "gemini", "qwen", etc.).
func InferFamily(key string) string {
	low := strings.ToLower(key)
	switch {
	case strings.Contains(low, "claude") || strings.Contains(low, "anthropic"):
		return "claude"
	case strings.Contains(low, "gpt") || strings.Contains(low, "o1") || strings.Contains(low, "o3") || strings.Contains(low, "o4") || strings.Contains(low, "openai"):
		return "openai"
	case strings.Contains(low, "gemini") || strings.Contains(low, "gemma"):
		return "gemini"
	case strings.Contains(low, "qwen") || strings.Contains(low, "qvq") || strings.Contains(low, "qwq"):
		return "qwen"
	case strings.Contains(low, "deepseek"):
		return "deepseek"
	case strings.Contains(low, "llama"):
		return "llama"
	case strings.Contains(low, "mistral") || strings.Contains(low, "devstral") || strings.Contains(low, "codestral"):
		return "mistral"
	case strings.Contains(low, "nemotron"):
		return "nemotron"
	case strings.Contains(low, "nova"):
		return "nova"
	default:
		return ""
	}
}

// LooksFree reports whether a model key names a free model by convention:
// names ending in "-free" or containing "sovereign".
func LooksFree(key string) bool {
	low := strings.ToLower(key)
	return strings.HasSuffix(low, "-free") || strings.Contains(low, "sovereign") || strings.Contains(low, "-free-")
}

// CleanModelName strips provider prefixes and version date suffixes to find the base model name.
func CleanModelName(key string) string {
	s := strings.ToLower(key)
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	// Strip common region prefixes in bedrock/vertex (e.g. "au.anthropic.", "eu.", "global.")
	if idx := strings.LastIndex(s, "."); idx >= 0 {
		s = s[idx+1:]
	}
	// Strip trailing versions/timestamps like "-20251001-v1:0" or ":0"
	if idx := strings.Index(s, ":"); idx >= 0 {
		s = s[:idx]
	}
	return s
}

// Entry returns the rate for a model key following the strict 5-step lookup hierarchy:
//  1. Exact entry in prices.local.json (user override)
//  2. Exact entry in prices.json (models.dev catalog)
//  3. LooksFree() convention match -> $0, Free: true
//  4. Cross-provider fallback: match catalog entry from ANY provider with same name/family,
//     flagged as "estimate from <source-provider>"
//  5. Unknown -> $0, Unknown: true, and added to seen-models tracking
func (b *Book) Entry(key string) Rate {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.entryLocked(key)
}

// entryLocked is Entry without the lock, so callers that already hold b.mu can
// resolve a rate. sync.RWMutex is not reentrant: calling Entry from a method
// holding the lock deadlocks outright.
//
// Callers must hold b.mu (write lock - this records unknown keys).
func (b *Book) entryLocked(key string) Rate {
	// 1. Exact match in prices.local.json (user overrides)
	if r, ok := b.localModels[key]; ok {
		return r
	}
	lowKey := strings.ToLower(key)
	for k, r := range b.localModels {
		if strings.ToLower(k) == lowKey {
			return r
		}
	}

	// 2. Exact match in prices.json (catalog)
	if r, ok := b.catalogModels[key]; ok {
		return r
	}
	for k, r := range b.catalogModels {
		if strings.ToLower(k) == lowKey {
			return r
		}
	}

	// 3. A free marker on the actual model key must win before fuzzy matching.
	// Otherwise a key such as "companyhub/deepseek-v4-flash-sovereign" is
	// incorrectly assigned the paid rate for "opencode/deepseek-v4-flash".
	if LooksFree(key) {
		return Rate{
			Name:      key,
			Free:      true,
			Sovereign: strings.Contains(strings.ToLower(key), "sovereign"),
		}
	}

	// 4. Cross-provider fallback. Collect and sort matches before choosing one:
	// Go deliberately randomises map iteration, and taking the first match made
	// the same private-provider model alternate between different public rates.
	cleanTarget := CleanModelName(key)
	targetFamily := InferFamily(key)

	// 4a. Exact base model name match across all providers.
	var exact []string
	for k := range b.catalogModels {
		if CleanModelName(k) == cleanTarget {
			exact = append(exact, k)
		}
	}
	if len(exact) > 0 {
		sortCatalogCandidates(exact, targetFamily, cleanTarget)
		bestKey := exact[0]
		res := b.catalogModels[bestKey]
		res.Name = key
		res.Source = "estimate from " + bestKey
		res.Unknown = false
		return res
	}

	// 4b. Partial slug match, ranked deterministically by family, canonical
	// provider, closeness of the model name, then lexical key.
	var partial []string
	for k := range b.catalogModels {
		cleanK := CleanModelName(k)
		if strings.Contains(cleanK, cleanTarget) || strings.Contains(cleanTarget, cleanK) {
			partial = append(partial, k)
		}
	}
	if len(partial) > 0 {
		sortCatalogCandidates(partial, targetFamily, cleanTarget)
		bestKey := partial[0]
		res := b.catalogModels[bestKey]
		res.Name = key
		res.Source = "estimate from " + bestKey
		res.Unknown = false
		return res
	}

	// 5. Unknown -> $0, Unknown: true
	b.Unknown[key] = true
	return Rate{Name: key, Unknown: true}
}

func sortCatalogCandidates(keys []string, family, target string) {
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		ra, rb := candidateRank(a, family, target), candidateRank(b, family, target)
		for n := range ra {
			if ra[n] != rb[n] {
				return ra[n] < rb[n]
			}
		}
		return strings.ToLower(a) < strings.ToLower(b)
	})
}

func candidateRank(key, family, target string) [3]int {
	keyFamily := InferFamily(key)
	familyPenalty := 1
	if family != "" && keyFamily == family {
		familyPenalty = 0
	}
	canonicalPenalty := 1
	provider := strings.ToLower(strings.SplitN(key, "/", 2)[0])
	canonical := map[string]string{
		"claude": "anthropic", "openai": "openai", "gemini": "google",
		"qwen": "alibaba", "deepseek": "deepseek", "mistral": "mistral",
		"nova": "amazon-bedrock",
	}[family]
	if canonical != "" && provider == canonical {
		canonicalPenalty = 0
	}
	delta := len(CleanModelName(key)) - len(target)
	if delta < 0 {
		delta = -delta
	}
	return [3]int{familyPenalty, canonicalPenalty, delta}
}

// PendingSuggestions returns suggestions for all seen model keys not explicitly in local overrides.
func (b *Book) PendingSuggestions(seenKeys []string) []PriceSuggestion {
	// Write lock: resolving an entry records unknown keys, and RWMutex is not
	// reentrant, so an RLock here plus Entry's Lock would deadlock.
	b.mu.Lock()
	defer b.mu.Unlock()

	var out []PriceSuggestion
	for _, k := range seenKeys {
		if b.isLocalLocked(k) {
			continue
		}
		r := b.entryLocked(k)
		// Unknown models hide spend; cross-provider rates are useful but still
		// estimates. Both deserve a one-time confirmation from the user.
		isEstimate := strings.HasPrefix(r.Source, "estimate from ")
		if !r.Unknown && !isEstimate {
			continue
		}
		// An unknown entry carries no numbers, so offer the median rate of its
		// family as a starting point. A wrong-but-plausible figure the user can
		// correct beats an empty box: it makes the cost visible to the budget
		// immediately, and the family is usually the right order of magnitude.
		rate, src := r, r.Source
		if r.Unknown {
			src = "unknown model"
			if guess, from, ok := b.guessFromFamilyLocked(k); ok {
				rate = guess
				src = "estimated from " + from
			}
		}
		out = append(out, PriceSuggestion{
			Key:           k,
			SuggestedRate: rate,
			Source:        src,
			IsUnknown:     r.Unknown,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// IsLocal reports whether a key has a user-authored overlay rate. Local
// overrides are authoritative and intentionally beat provider-reported cost.
func (b *Book) IsLocal(key string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.isLocalLocked(key)
}

func (b *Book) isLocalLocked(key string) bool {
	if _, ok := b.localModels[key]; ok {
		return true
	}
	for k := range b.localModels {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

// guessFromFamilyLocked proposes a rate for an unpriced model from the median
// of its family in the catalogue, reporting the family it drew from.
//
// The median rather than the mean: families span a nano tier and a flagship,
// and a single outlier would drag an average somewhere useless.
//
// Callers must hold b.mu.
func (b *Book) guessFromFamilyLocked(key string) (Rate, string, bool) {
	fam := InferFamily(key)
	if fam == "" {
		return Rate{}, "", false
	}
	var in, out []float64
	for k, r := range b.catalogModels {
		if k == key || r.Unknown || r.Free {
			continue
		}
		if r.Family != fam || r.Output <= 0 {
			continue
		}
		in = append(in, r.Input)
		out = append(out, r.Output)
	}
	if len(out) == 0 {
		return Rate{}, "", false
	}
	sort.Float64s(in)
	sort.Float64s(out)
	median := func(v []float64) float64 {
		if len(v) == 0 {
			return 0
		}
		return v[len(v)/2]
	}
	mIn, mOut := median(in), median(out)
	return Rate{
		Name:   key,
		Family: fam,
		Input:  mIn,
		Output: mOut,
		// Cache rates are rarely published per model; the widespread
		// convention is a discount on input for reads and a premium for
		// writes. Better than leaving them at zero, which would under-report
		// cache-heavy sessions badly.
		CacheRead:  round4(mIn * 0.1),
		CacheWrite: round4(mIn * 1.25),
	}, fmt.Sprintf("%d %s models", len(out), fam), true
}

func round4(f float64) float64 { return float64(int64(f*1e4+0.5)) / 1e4 }

// ForgetUnknown stops reporting a key as unpriced without recording a rate.
//
// Takes the lock: the ingest goroutine writes b.Unknown on every priced
// message, so deleting from the UI goroutine unsynchronised is a data race.
func (b *Book) ForgetUnknown(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.Unknown, key)
}

// SaveOverride writes an override rate atomically to prices.local.json.
func (b *Book) SaveOverride(overlayPath, key string, rate Rate) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if overlayPath == "" {
		overlayPath = b.overlay
	}
	if overlayPath == "" {
		return fmt.Errorf("no overlay path configured")
	}

	doc := document{Models: map[string]Rate{}}
	if raw, err := os.ReadFile(overlayPath); err == nil {
		_ = json.Unmarshal(raw, &doc)
		if doc.Models == nil {
			doc.Models = map[string]Rate{}
		}
	}

	doc.Models[key] = rate

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := overlayPath + ".tmp"
	if err := os.MkdirAll(filepath.Dir(overlayPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, overlayPath); err != nil {
		return err
	}

	b.localModels[key] = rate
	b.Models[key] = rate
	delete(b.Unknown, key)
	return nil
}

// RLockModels takes a read lock over the merged model table so callers can
// snapshot it without racing a Reload.
func (b *Book) RLockModels() { b.mu.RLock() }

// RUnlockModels releases the read lock taken by RLockModels.
func (b *Book) RUnlockModels() { b.mu.RUnlock() }

// IsFree reports whether the model key is priced as free.
func (b *Book) IsFree(key string) bool {
	return b.Entry(key).Free
}

// Cost computes the USD cost of a message's token usage.
func (b *Book) Cost(key string, in, out, cacheRead, cacheWrite int64) float64 {
	r := b.Entry(key)
	if r.Free {
		return 0
	}
	return float64(in)/1e6*r.Input +
		float64(out)/1e6*r.Output +
		float64(cacheRead)/1e6*r.CacheRead +
		float64(cacheWrite)/1e6*r.CacheWrite
}

// ShadowCost computes what a free model would have cost at the reference rate.
func (b *Book) ShadowCost(key string, in, out, cacheRead, cacheWrite int64) float64 {
	b.mu.RLock()
	ref, ok := b.Models[b.ReferenceModel]
	b.mu.RUnlock()
	if !ok {
		return 0
	}
	return float64(in)/1e6*ref.Input +
		float64(out)/1e6*ref.Output +
		float64(cacheRead)/1e6*ref.CacheRead +
		float64(cacheWrite)/1e6*ref.CacheWrite
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
