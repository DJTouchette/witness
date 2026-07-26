package selector_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djtouchette/recon/pkg/recon"
	"github.com/djtouchette/witness/internal/selector"
)

// newGoFixture writes a minimal Go module (calc.go plus its calc_test.go) to a
// temp dir, indexes it with recon, and returns the repo root and the indexer.
func newGoFixture(t *testing.T) (string, *recon.Recon) {
	t.Helper()

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
	t.Cleanup(func() { r.Close() })

	return dir, r
}

// newPytestFixture writes a package whose tests share a conftest.py fixture,
// indexes it with recon, and returns the repo root and the indexer. conftest.py
// is the shape with the widest blast radius: it declares no test itself, and
// every test in the tree breaks when it does.
func newPytestFixture(t *testing.T) (string, *recon.Recon) {
	t.Helper()

	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/api.py", "def add(a, b):\n    return a + b\n")
	write("tests/conftest.py", "import pytest\nfrom src.api import add\n\n\n@pytest.fixture\ndef adder():\n    return add\n")
	write("tests/test_api.py", "from tests.conftest import adder\nfrom src.api import add\n\n\ndef test_add():\n    assert add(1, 2) == 3\n")

	r, err := recon.New(dir, recon.WithCacheDir(filepath.Join(t.TempDir(), "cache")))
	if err != nil {
		t.Fatalf("recon.New: %v", err)
	}
	t.Cleanup(func() { r.Close() })

	return dir, r
}

// TestSelect_RealRecon_ChangedFixture is the headline fail-open case, driven
// through real recon rather than a fake. Editing conftest.py used to return
// conftest.py and nothing else: pytest collects zero tests from it, so CI went
// red for the wrong reason while every test the fixture change actually broke
// went unrun. recon reports the reverse-import edge, so the dependent test is
// reachable — the selector was discarding it.
func TestSelect_RealRecon_ChangedFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes a repo with recon; skipped in -short")
	}

	_, r := newPytestFixture(t)

	res, err := selector.Select(r, []string{"tests/conftest.py"}, selector.DefaultOptions())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	var paths []string
	for _, st := range res.Tests {
		paths = append(paths, st.Path)
		if st.Path == "tests/conftest.py" {
			t.Errorf("a pytest fixture is not a runnable test target: %+v", res.Tests)
		}
	}
	if len(paths) != 1 || paths[0] != "tests/test_api.py" {
		t.Fatalf("selected %v, want [tests/test_api.py]", paths)
	}
	if len(res.Summary.Unmapped) != 0 {
		t.Errorf("conftest.py is covered by the selection, Unmapped = %v", res.Summary.Unmapped)
	}
}

// TestSelect_RealRecon_UnindexedFileIsReported pins the fail-closed hook
// against real recon: a path recon never scanned must be distinguishable from a
// change that genuinely needs no tests, or the CLI exits 0 on both.
func TestSelect_RealRecon_UnindexedFileIsReported(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes a repo with recon; skipped in -short")
	}

	_, r := newGoFixture(t)

	res, err := selector.Select(r, []string{"does/not/exist.go"}, selector.DefaultOptions())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(res.Tests) != 0 {
		t.Fatalf("expected no tests, got %+v", res.Tests)
	}
	if len(res.Summary.NotIndexed) != 1 || res.Summary.NotIndexed[0] != "does/not/exist.go" {
		t.Errorf("Summary.NotIndexed = %v, want [does/not/exist.go]", res.Summary.NotIndexed)
	}
	if len(res.Summary.Unmapped) != 1 || res.Summary.Unmapped[0] != "does/not/exist.go" {
		t.Errorf("Summary.Unmapped = %v, want [does/not/exist.go]", res.Summary.Unmapped)
	}
}

// TestSelect_RealRecon drives the full chain — recon indexes a real fixture on
// disk, and Select maps a changed source file to its test through recon's own
// test mapping. This validates the integration the fake-based unit tests can't:
// that *recon.Recon satisfies RepoIntel and the wiring holds end to end.
func TestSelect_RealRecon(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes a repo with recon; skipped in -short")
	}

	_, r := newGoFixture(t)

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

// TestSelect_RealRecon_AbsolutePath pins the behaviour witness depends on for
// callers that hand it absolute paths (embedders, agents, editor plugins):
// recon must resolve them against the repo root instead of missing the index.
// Older recon releases looked the changed file up verbatim, so an absolute path
// selected zero tests and witness reported success — a green gate that ran
// nothing.
func TestSelect_RealRecon_AbsolutePath(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes a repo with recon; skipped in -short")
	}

	dir, r := newGoFixture(t)

	res, err := selector.Select(r, []string{filepath.Join(dir, "calc.go")}, selector.DefaultOptions())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	for i := range res.Tests {
		if filepath.Base(res.Tests[i].Path) == "calc_test.go" {
			return
		}
	}
	t.Fatalf("calc_test.go not selected for an absolute-path change to calc.go; got %+v", res.Tests)
}

// newCSharpFixture writes a .NET solution shaped like the one the audit came
// from: a domain model whose name ends in "Test" (LabTest.cs is a laboratory
// test, not a unit test) next to a real test project.
func newCSharpFixture(t *testing.T) (string, *recon.Recon) {
	t.Helper()

	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/Certificates/Certificates.csproj", "<Project Sdk=\"Microsoft.NET.Sdk\">\n</Project>\n")
	write("src/Certificates/Models/LabTest.cs", "namespace Certificates.Models;\n\npublic class LabTest\n{\n    public string Name { get; set; } = \"\";\n}\n")
	write("src/Certificates/CertificateService.cs", "namespace Certificates;\n\nusing Certificates.Models;\n\npublic class CertificateService\n{\n    public string Describe(LabTest test) => test.Name;\n}\n")
	write("tests/Certificates.Tests/Certificates.Tests.csproj", "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <ItemGroup>\n    <PackageReference Include=\"xunit\" Version=\"2.9.2\" />\n  </ItemGroup>\n</Project>\n")
	write("tests/Certificates.Tests/CertificateServiceTests.cs", "namespace Certificates.Tests;\n\nusing Certificates;\nusing Xunit;\n\npublic class CertificateServiceTests\n{\n    [Fact]\n    public void Describes() => Assert.True(true);\n}\n")
	// A test class xUnit finds by attribute, not by name: no Test/Tests suffix
	// anywhere. Demoting it as a "support file" dropped a real suite.
	write("tests/Certificates.Tests/OrderScenarios.cs", "namespace Certificates.Tests;\n\nusing Certificates;\nusing Xunit;\n\npublic class OrderScenarios\n{\n    [Fact]\n    public void Ships() => Assert.True(true);\n}\n")

	r, err := recon.New(dir, recon.WithCacheDir(filepath.Join(t.TempDir(), "cache")))
	if err != nil {
		t.Fatalf("recon.New: %v", err)
	}
	t.Cleanup(func() { r.Close() })

	return dir, r
}

// TestSelect_RealRecon_CSharpDomainModelNamedTestIsNotSelected is the audit's
// C# case run against real recon rather than a fake. Witness defers to recon
// for a .cs file outside any test project — recon v0.13.1 requires test-project
// evidence before calling one a test — so this is the test that would catch
// recon regressing on that. If it does, editing CertificateService.cs "covers"
// itself with a domain model and the change looks tested when nothing ran.
func TestSelect_RealRecon_CSharpDomainModelNamedTestIsNotSelected(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes a repo with recon; skipped in -short")
	}

	_, r := newCSharpFixture(t)

	res, err := selector.Select(r, []string{"src/Certificates/CertificateService.cs"}, selector.DefaultOptions())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	for _, st := range res.Tests {
		if strings.Contains(st.Path, "Models/LabTest.cs") {
			t.Errorf("the domain model LabTest.cs was selected as a test (%+v); an edit to production code now looks covered by itself", res.Tests)
		}
		if strings.HasSuffix(st.Path, ".csproj") {
			t.Errorf("project metadata %q is not a runnable test target", st.Path)
		}
	}
}

// TestSelect_RealRecon_CSharpTestClassWithoutATestSuffix pins the other
// direction against real recon: xUnit finds test classes by attribute, so
// OrderScenarios.cs is a real suite even though nothing in its name says so.
func TestSelect_RealRecon_CSharpTestClassWithoutATestSuffix(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes a repo with recon; skipped in -short")
	}

	_, r := newCSharpFixture(t)

	res, err := selector.Select(r, []string{"tests/Certificates.Tests/OrderScenarios.cs"}, selector.DefaultOptions())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	for _, st := range res.Tests {
		if strings.HasSuffix(st.Path, "OrderScenarios.cs") {
			return
		}
	}
	t.Errorf("editing an xUnit test class with no Test/Tests suffix selected %+v, want the class itself; demoting it drops a real suite", res.Tests)
}
