package prices

import (
	_ "embed"
	"encoding/json"
	"os"
	"sync"
)

// seedCatalog is a snapshot of the price table compiled into the binary.
//
// It exists so a bare executable still prices correctly. Without it, a missing
// prices.json makes every model unknown and the odometer reports a confident
// $0.00 - which reads as "you spent nothing" rather than "this could not be
// priced", the worst possible failure for a cost meter.
//
// The snapshot is a FLOOR, not the source of truth: rates move, so the app
// refreshes from models.dev in the background and writes the result to disk,
// which then wins on every subsequent start.
//
//go:embed seed_prices.json
var seedCatalog []byte

var (
	seedOnce   sync.Once
	seedModels map[string]Rate
	seedRef    string
)

func loadSeed() {
	seedOnce.Do(func() {
		var doc document
		if err := json.Unmarshal(seedCatalog, &doc); err != nil {
			return
		}
		seedModels = doc.Models
		seedRef = doc.ReferenceModel
	})
}

// SeedModels returns the embedded fallback table.
func SeedModels() map[string]Rate {
	loadSeed()
	out := make(map[string]Rate, len(seedModels))
	for k, v := range seedModels {
		if v.Family == "" {
			v.Family = InferFamily(k)
		}
		out[k] = v
	}
	return out
}

// SeedReference returns the embedded reference model for savings maths.
func SeedReference() string {
	loadSeed()
	if seedRef == "" {
		return FallbackReference
	}
	return seedRef
}

// EnsureCatalog writes the embedded seed to path when no readable table exists
// there, so first run on a machine with only the executable still prices.
// Reports whether it wrote the file.
func EnsureCatalog(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		return false, nil
	}
	if len(SeedModels()) == 0 {
		return false, nil
	}
	if err := WriteCatalog(path, SeedModels(), SeedReference()); err != nil {
		return false, err
	}
	// Mark the table as coming from the build rather than from a fetch, so the
	// staleness check refreshes it promptly.
	//
	// An earlier version backdated the file by a year to force that refresh,
	// which made a brand-new install announce "prices 365 days old" - alarming
	// and untrue. A flag conveys "not fetched yet" without lying about age.
	return true, markSeeded(path)
}

// markSeeded records that a table came from the embedded snapshot.
func markSeeded(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	doc["_seeded"] = true
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// IsSeeded reports whether the table on disk is the built-in snapshot that has
// not yet been replaced by a live fetch.
func IsSeeded(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc struct {
		Seeded bool `json:"_seeded"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false
	}
	return doc.Seeded
}
