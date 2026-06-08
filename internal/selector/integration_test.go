package selector_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/djtouchette/recon/pkg/recon"
	"github.com/djtouchette/witness/internal/selector"
)

// TestSelect_RealRecon drives the full chain — recon indexes a real fixture on
// disk, and Select maps a changed source file to its test through recon's own
// test mapping. This validates the integration the fake-based unit tests can't:
// that *recon.Recon satisfies RepoIntel and the wiring holds end to end.
func TestSelect_RealRecon(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes a repo with recon; skipped in -short")
	}

	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module witnessfixture\n\ngo 1.21\n")
	write("calc.go", "package fixture\n\nfunc Add(a, b int) int { return a + b }\n")
	write("calc_test.go", "package fixture\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fail()\n\t}\n}\n")

	// Cache outside the fixture so it isn't picked up as a source file.
	r, err := recon.New(dir, recon.WithCacheDir(filepath.Join(t.TempDir(), "cache")))
	if err != nil {
		t.Fatalf("recon.New: %v", err)
	}
	defer r.Close()

	res, err := selector.Select(r, []string{"calc.go"}, selector.DefaultOptions())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	var found *selector.ScoredTest
	for i := range res.Tests {
		if filepath.Base(res.Tests[i].Path) == "calc_test.go" {
			found = &res.Tests[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("calc_test.go not selected for a change to calc.go; got %+v", res.Tests)
	}
	if found.Score != 1.0 {
		t.Errorf("direct test score = %v, want 1.0", found.Score)
	}
	hasDirect := false
	for _, s := range found.Signals {
		if s == "direct-test" {
			hasDirect = true
		}
	}
	if !hasDirect {
		t.Errorf("expected direct-test signal, got %v", found.Signals)
	}
}
