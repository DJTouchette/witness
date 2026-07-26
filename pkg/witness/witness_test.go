package witness

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewOutsideAGitRepoIsAnError(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	if err == nil {
		w.Close()
		t.Fatal("New outside a git repo must fail; analysing a non-repo silently answers a different question")
	}
}

// git reports paths relative to the repository root, so a Witness created from
// a subdirectory has to analyse the repository root or every lookup misses.
func TestNewRootsAtRepositoryRoot(t *testing.T) {
	repo := newTestRepo(t)
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := New(sub)
	if err != nil {
		t.Fatalf("New(%s): %v", sub, err)
	}
	defer w.Close()

	if w.Root() != repo {
		t.Errorf("Root() = %q, want the repository root %q", w.Root(), repo)
	}

	res, err := w.Select([]string{"calc.go"}, DefaultOptions())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(res.Tests) == 0 {
		t.Error("Select from a subdirectory found no tests for calc.go")
	}
}

func TestWithCacheDir(t *testing.T) {
	repo := newTestRepo(t)
	cache := t.TempDir()

	w, err := New(repo, WithCacheDir(cache))
	if err != nil {
		t.Fatalf("New with cache dir: %v", err)
	}
	defer w.Close()

	if _, err := w.Select([]string{"calc.go"}, DefaultOptions()); err != nil {
		t.Fatalf("Select: %v", err)
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Errorf("WithCacheDir(%s) left the directory empty; the option did not reach recon", cache)
	}
}

// recon runs a cache directory through filepath.Abs, which anchors a relative
// one at the HOST's working directory: WithCacheDir(".witness") named a
// different cache for every directory the embedding program was started from,
// and pointed two repositories at the same one. It resolves against the
// repository root now, like every other path in the API.
func TestRelativeCacheDirResolvesAgainstTheRepoRoot(t *testing.T) {
	repo := newTestRepo(t)
	// Anywhere but the repository: the old behaviour would put the cache here.
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)

	w, err := New(repo, WithCacheDir(".witness"))
	if err != nil {
		t.Fatalf("New with a relative cache dir: %v", err)
	}
	defer w.Close()

	if _, err := w.Select([]string{"calc.go"}, DefaultOptions()); err != nil {
		t.Fatalf("Select: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(repo, ".witness"))
	if err != nil || len(entries) == 0 {
		t.Errorf("%s = (%v, %v), want the cache under the repository root", filepath.Join(repo, ".witness"), entries, err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, ".witness")); err == nil {
		t.Errorf("the cache landed in the host's working directory %s, not in the repository", elsewhere)
	}
}

func TestRepoCacheDir(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	abs := filepath.Join(string(filepath.Separator), "var", "cache")
	cases := []struct{ in, want string }{
		{"", ""},
		{".witness", filepath.Join(root, ".witness")},
		// An absolute path is the caller naming a location outright.
		{abs, abs},
	}
	for _, tc := range cases {
		if got := repoCacheDir(root, tc.in); got != tc.want {
			t.Errorf("repoCacheDir(%q, %q) = %q, want %q", root, tc.in, got, tc.want)
		}
	}
}

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	if opts.MaxDepth == 0 || opts.MinScore == 0 || opts.MaxTests == 0 {
		t.Errorf("DefaultOptions() = %+v, want the documented non-zero defaults", opts)
	}
}

func TestSelectExplicitFiles(t *testing.T) {
	w := newTestWitness(t)
	res, err := w.Select([]string{"calc.go"}, DefaultOptions())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !containsPath(res.Tests, "calc_test.go") {
		t.Errorf("Select(calc.go) = %+v, want calc_test.go", res.Tests)
	}
	if res.Summary.Changed != 1 {
		t.Errorf("Summary.Changed = %d, want 1", res.Summary.Changed)
	}
}

func TestSelectFallsBackToWorkingTreeDiff(t *testing.T) {
	repo := newTestRepo(t)
	writeFile(t, repo, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return b + a }\n")

	w, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	res, err := w.Select(nil, DefaultOptions())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !containsPath(res.Tests, "calc_test.go") {
		t.Errorf("Select(nil) = %+v, want the working-tree change to select calc_test.go", res.Tests)
	}
}

// SelectStaged used to hand an empty index to Select, which then diffed the
// working tree — returning tests for edits the caller never staged.
func TestSelectStagedWithCleanIndexReturnsEmpty(t *testing.T) {
	repo := newTestRepo(t)
	// Modified but deliberately NOT staged.
	writeFile(t, repo, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return b + a }\n")

	w, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	res, err := w.SelectStaged(DefaultOptions())
	if err != nil {
		t.Fatalf("SelectStaged: %v", err)
	}
	if len(res.ChangedFiles) != 0 || len(res.Tests) != 0 {
		t.Errorf("SelectStaged with a clean index = changed %v, tests %+v; want empty, not a working-tree diff",
			res.ChangedFiles, res.Tests)
	}
}

func TestSelectStagedSeesTheIndex(t *testing.T) {
	repo := newTestRepo(t)
	writeFile(t, repo, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return b + a }\n")
	gitIn(t, repo, "add", "calc.go")

	w, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	res, err := w.SelectStaged(DefaultOptions())
	if err != nil {
		t.Fatalf("SelectStaged: %v", err)
	}
	if !containsPath(res.Tests, "calc_test.go") {
		t.Errorf("SelectStaged = %+v, want calc_test.go", res.Tests)
	}
}

func TestSelectSinceWithEmptyRangeReturnsEmpty(t *testing.T) {
	repo := newTestRepo(t)
	// Dirty working tree, but nothing committed since HEAD.
	writeFile(t, repo, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return b + a }\n")

	w, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	res, err := w.SelectSince("HEAD", DefaultOptions())
	if err != nil {
		t.Fatalf("SelectSince: %v", err)
	}
	if len(res.ChangedFiles) != 0 || len(res.Tests) != 0 {
		t.Errorf("SelectSince(HEAD) on an empty range = changed %v, tests %+v; want empty",
			res.ChangedFiles, res.Tests)
	}
}

func TestSelectSinceSeesCommittedChanges(t *testing.T) {
	repo := newTestRepo(t)
	writeFile(t, repo, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return b + a }\n")
	gitIn(t, repo, "add", "calc.go")
	gitIn(t, repo, "commit", "-q", "-m", "tweak")

	w, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	res, err := w.SelectSince("HEAD~1", DefaultOptions())
	if err != nil {
		t.Fatalf("SelectSince: %v", err)
	}
	if !containsPath(res.Tests, "calc_test.go") {
		t.Errorf("SelectSince(HEAD~1) = %+v, want calc_test.go", res.Tests)
	}
}

// A JS or Python consumer doing result.tests.length breaks on null.
func TestEmptyResultMarshalsAsArraysNotNull(t *testing.T) {
	w := newTestWitness(t)
	res, err := w.SelectStaged(DefaultOptions())
	if err != nil {
		t.Fatalf("SelectStaged: %v", err)
	}

	blob, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"changed_files", "tests"} {
		if string(raw[field]) == "null" {
			t.Errorf("%q marshalled as null, want []: %s", field, blob)
		}
	}
}

func TestComplete(t *testing.T) {
	if !Complete(&SelectResult{}) {
		t.Error("a clean empty result is complete")
	}
	if Complete(&SelectResult{Summary: Summary{NotIndexed: []string{"a.go"}}}) {
		t.Error("a result with unindexed files is not complete")
	}
	if Complete(&SelectResult{Summary: Summary{AnalysisError: "boom"}}) {
		t.Error("a result with an analysis error is not complete")
	}
	if Complete(nil) {
		t.Error("nil is not complete")
	}
	// The go.mod-bump shape: recon indexed the file and answered cleanly, and
	// still no test covers the change. An embedder that trusts this exits 0
	// having run nothing — the false green witness exists to prevent.
	if Complete(&SelectResult{Summary: Summary{Unmapped: []string{"go.mod"}}}) {
		t.Error("an empty selection with uncovered changed files is not complete")
	}
	// ... but an uncovered file alongside a real selection is: tests ran.
	if !Complete(&SelectResult{
		Tests:   []ScoredTest{{Path: "calc_test.go"}},
		Summary: Summary{Unmapped: []string{"go.mod"}},
	}) {
		t.Error("an uncovered file alongside a selection is not a gap; the gate still has teeth")
	}
}

// TestCompleteMatchesTheRealIndexedButUnmappedChange is the same case through
// the real API rather than a hand-built struct: recon has the file, answers
// without error, and no test maps to it.
func TestCompleteMatchesTheRealIndexedButUnmappedChange(t *testing.T) {
	w := newTestWitness(t)

	res, err := w.Select([]string{"go.mod"}, DefaultOptions())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(res.Tests) != 0 {
		t.Skipf("go.mod unexpectedly mapped to %d test(s); the case under test needs an unmapped file", len(res.Tests))
	}
	if len(res.Summary.NotIndexed) != 0 {
		t.Skipf("go.mod is not indexed here (%v); NotIndexed already covers that case", res.Summary.NotIndexed)
	}
	if Complete(res) {
		t.Errorf("Complete = true for a change no test covers (summary %+v); an embedder following the doc comment reports a pass witness never earned", res.Summary)
	}
}

func TestCommandsAndFullSuiteCommand(t *testing.T) {
	w := newTestWitness(t)

	if cmds, err := w.Commands(&SelectResult{}); err != nil || len(cmds) != 0 {
		t.Errorf("Commands(empty) = (%v, %v), want (nil, nil)", cmds, err)
	}

	res, err := w.Select([]string{"calc.go"}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	cmds, err := w.Commands(res)
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0][0] != "go" || cmds[0][1] != "test" {
		t.Errorf("Commands = %v, want a single go test invocation", cmds)
	}

	full, err := w.FullSuiteCommand()
	if err != nil {
		t.Fatalf("FullSuiteCommand: %v", err)
	}
	if len(full) != 1 || strings.Join(full[0], " ") != "go test ./..." {
		t.Errorf("FullSuiteCommand = %v, want [go test ./...]", full)
	}
}

// Maven, Gradle, sbt, SwiftPM, PHPUnit, dart and cargo-in-a-workspace cannot be
// invoked from a test path alone: the runner has to read the build file that
// owns the tree and the class the test file declares. It reads them under the
// root Commands hands it, so a root that never arrives turns a runnable
// selection into ErrNoRunner.
func TestCommandsDerivesInvocationsFromTheRepository(t *testing.T) {
	repo := newTestRepo(t)
	// A pom beside the Go module: Commands groups by the path's own language,
	// so the java test below is derived from the pom no matter what recon calls
	// the project's primary language.
	writeFile(t, repo, "pom.xml", "<project><artifactId>calc</artifactId></project>\n")
	writeFile(t, repo, filepath.Join("src", "test", "java", "com", "example", "CalculatorTest.java"),
		"package com.example;\n\nclass CalculatorTest {\n}\n")

	w, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	cmds, err := w.Commands(&SelectResult{
		Tests: []ScoredTest{{Path: "src/test/java/com/example/CalculatorTest.java"}},
	})
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	if len(cmds) != 1 || strings.Join(cmds[0], " ") != "mvn test -Dtest=CalculatorTest" {
		t.Errorf("Commands = %v, want [mvn test -Dtest=CalculatorTest] derived from the pom at the repository root", cmds)
	}
}

func TestRunRefusesAnEmptySelection(t *testing.T) {
	w := newTestWitness(t)
	code, err := w.Run(context.Background(), &SelectResult{}, nil, nil)
	if err == nil {
		t.Fatal("Run with nothing selected must error, not report a pass")
	}
	if code == 0 {
		t.Errorf("code = 0 for a run that never happened; want a non-zero sentinel")
	}
}

func TestRunExecutesTheSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs a real go test binary")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	w := newTestWitness(t)
	res, err := w.Select([]string{"calc.go"}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code, err := w.Run(context.Background(), res, &out, &errOut)
	if err != nil {
		t.Fatalf("Run: %v\n%s\n%s", err, out.String(), errOut.String())
	}
	if code != 0 {
		t.Errorf("code = %d, want 0\n%s\n%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "ok") && !strings.Contains(errOut.String(), "ok") {
		t.Errorf("no go test output was captured: %q / %q", out.String(), errOut.String())
	}
}

// --- helpers -----------------------------------------------------------------

func containsPath(tests []ScoredTest, path string) bool {
	for _, t := range tests {
		if t.Path == path {
			return true
		}
	}
	return false
}

func newTestWitness(t *testing.T) *Witness {
	t.Helper()
	w, err := New(newTestRepo(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}

	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "config", "user.email", "witness@example.test")
	gitIn(t, dir, "config", "user.name", "witness")

	// recon's cache lives in the repo; without this it shows up as an untracked
	// change and pollutes every working-tree diff under test.
	writeFile(t, dir, ".gitignore", ".recon/\n.witness/\n")
	writeFile(t, dir, "go.mod", "module example.test/calc\n\ngo 1.25\n")
	writeFile(t, dir, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, dir, "calc_test.go",
		"package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")

	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
