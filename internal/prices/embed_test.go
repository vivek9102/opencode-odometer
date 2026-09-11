package prices

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSeedIsUsable guards the embedded fallback: if the snapshot fails to
// parse or loses its reference model, a bare executable silently prices
// everything at $0 and the odometer reports "you spent nothing".
func TestSeedIsUsable(t *testing.T) {
	models := SeedModels()
	if len(models) < 100 {
		t.Fatalf("embedded seed has %d models, expected a full catalogue", len(models))
	}
	ref := SeedReference()
	if ref == "" {
		t.Fatal("embedded seed has no reference model; savings maths would break")
	}
	if _, ok := models[ref]; !ok {
		t.Errorf("reference model %q missing from the seed", ref)
	}
	priced := 0
	for _, r := range models {
		if r.Output > 0 {
			priced++
		}
	}
	if priced == 0 {
		t.Error("no seed entry carries a price")
	}
}

// TestEnsureCatalogWritesOnlyWhenAbsent covers first-run seeding without
// clobbering a table the user already has.
func TestEnsureCatalogWritesOnlyWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.json")

	wrote, err := EnsureCatalog(path)
	if err != nil {
		t.Fatalf("EnsureCatalog: %v", err)
	}
	if !wrote {
		t.Fatal("expected the seed to be written when no table exists")
	}

	b, err := New(path, "")
	if err != nil {
		t.Fatalf("seeded table is not loadable: %v", err)
	}
	if got := b.Cost(SeedReference(), 1_000_000, 0, 0, 0); got <= 0 {
		t.Errorf("seeded reference model priced at %v, want > 0", got)
	}

	// A second call must leave an existing table alone.
	if err := os.WriteFile(path, []byte(`{"models":{"x/y":{"name":"y","output":1}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	wrote, err = EnsureCatalog(path)
	if err != nil {
		t.Fatalf("EnsureCatalog second call: %v", err)
	}
	if wrote {
		t.Error("existing price table must not be overwritten by the seed")
	}
}
