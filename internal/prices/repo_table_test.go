package prices

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestRepoPricesJSONIsValid loads the actual prices.json shipped at the repo
// root (not a fixture) and checks the invariants the old Python CI step
// enforced: it has models, its reference_model exists among them, and every
// rate is numeric.
//
// This is the CI safety net for the price table itself: prices.json is the
// public catalogue that gets embedded as internal/prices/seed_prices.json (the
// offline fallback), so a broken table here means a broken fallback in every
// shipped binary.
func TestRepoPricesJSONIsValid(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve test file path")
	}
	// internal/prices/repo_table_test.go -> repo root is two levels up.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	path := filepath.Join(root, "prices.json")

	b, err := New(path, "")
	if err != nil {
		t.Fatalf("repo prices.json failed to load: %v", err)
	}

	b.RLockModels()
	n := len(b.Models)
	b.RUnlockModels()
	if n == 0 {
		t.Fatal("repo prices.json has no models")
	}

	if b.ReferenceModel == "" {
		t.Fatal("repo prices.json has no reference_model")
	}
	ref := b.Entry(b.ReferenceModel)
	if ref.Unknown {
		t.Errorf("reference_model %q is missing from the table", b.ReferenceModel)
	}

	b.RLockModels()
	defer b.RUnlockModels()
	var bad []string
	for k, r := range b.Models {
		if r.Input < 0 || r.Output < 0 || r.CacheRead < 0 || r.CacheWrite < 0 {
			bad = append(bad, k)
			if len(bad) >= 5 {
				break
			}
		}
	}
	if len(bad) > 0 {
		t.Errorf("negative rates in repo prices.json: %v", bad)
	}
}
