package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every test in this file is a named regression. The audit that produced them
// found each one alive in a shipped binary, and every one of them survived
// because the unit tests ran against a fake index instead of a real repository.
// The failure messages say which regression came back on purpose: a golden diff
// tells you the bytes changed, it does not tell you the gate went green on a
// change nobody tested.

// Deleting a file removes it from recon's index, but its dependents' tests are
// exactly the ones that can break. Reporting "no relevant tests" here is the
// worst false green witness can produce: the code that used the deleted symbol
// is now broken and CI says nothing.
func TestDeletedFileStillSelectsItsDependentsTests(t *testing.T) {
	r := newRepo(t, "go")
	r.remove(t, "internal/mathx/mathx.go")

	res := r.run(t, "select", "--format", "paths")
	if !strings.Contains(res.stdout, "calc/calc_test.go") {
		t.Errorf("REGRESSION (deleted files): deleting internal/mathx/mathx.go selected %q, want calc/calc_test.go — the test that imports it",
			strings.TrimSpace(res.stdout))
	}
	if !strings.Contains(res.stderr, "deleted") {
		t.Errorf("REGRESSION (deleted files): stderr = %q, want the deletion reported as something witness cannot fully resolve", res.stderr)
	}
}

// The same deletion against a cold index — recon has never seen the file, so
// there is no import edge left to follow. Witness cannot find the dependents;
// what it must not do is call that an empty change set and exit 0.
func TestDeletedFileWithColdIndexFailsClosed(t *testing.T) {
	r := newRepo(t, "go")
	if err := os.RemoveAll(filepath.Join(r.root, ".recon")); err != nil {
		t.Fatalf("dropping the recon cache: %v", err)
	}
	r.remove(t, "internal/mathx/mathx.go")

	full := r.run(t, "select", "--format", "exec")
	if got := strings.TrimSpace(full.stdout); got != "go test ./..." {
		t.Errorf("REGRESSION (fail open): a deletion witness cannot resolve produced %q, want the full suite `go test ./...`", got)
	}

	fail := r.run(t, "select", "--format", "exec", "--fallback", "fail")
	if fail.code == 0 {
		t.Errorf("REGRESSION (fail open): --fallback=fail exited 0 on an unresolvable deletion; stdout=%q stderr=%q", fail.stdout, fail.stderr)
	}
}

// tests/conftest.py holds shared fixtures. Handing it to pytest collects zero
// tests and exits 0 — a green build that ran nothing — so witness has to select
// the tests that depend on it and leave conftest.py itself out of the command.
func TestConftestEditSelectsDependentTestsNotConftestItself(t *testing.T) {
	r := newRepo(t, "python")
	// Co-change history: the evidence that ties a shared fixture to the tests
	// using it, since pytest's conftest injection leaves no import to follow.
	for _, msg := range []string{"tighten the customer fixture", "cover the empty order"} {
		r.append(t, "tests/conftest.py", "\n\n# "+msg+"\n")
		r.append(t, "tests/test_api.py", "\n\n# "+msg+"\n")
		r.commit(t, msg)
	}
	r.warm(t)
	r.append(t, "tests/conftest.py", "\n\n# widen the fixture\n")

	paths := r.run(t, "select", "--format", "paths")
	got := strings.TrimSpace(paths.stdout)
	if got != "tests/test_api.py" {
		t.Errorf("REGRESSION (conftest short-circuit): editing tests/conftest.py selected %q, want tests/test_api.py", got)
	}
	if strings.Contains(got, "conftest.py") {
		t.Errorf("REGRESSION (conftest short-circuit): conftest.py was handed to the runner as a test path (%q); pytest collects nothing from it and exits 0", got)
	}

	exec := r.run(t, "select", "--format", "exec")
	if want := "pytest tests/test_api.py"; strings.TrimSpace(exec.stdout) != want {
		t.Errorf("REGRESSION (conftest short-circuit): command = %q, want %q", strings.TrimSpace(exec.stdout), want)
	}
}

// git reports paths relative to the repository root. Rooting recon at the
// directory witness happens to be invoked from puts the two in different path
// spaces, where every lookup misses and witness reports "no tests" for a change
// that is right there.
func TestSubdirectoryInvocationMatchesRepoRoot(t *testing.T) {
	r := newRepo(t, "python")
	r.append(t, "src/api.py", "\n\ndef refund(order):\n    return order\n")

	fromRoot := r.run(t, "select", "--format", "json")
	for _, sub := range []string{"src", "tests"} {
		dir := filepath.Join(r.root, sub)
		fromSub := r.runFrom(t, dir, "select", "--format", "json")
		if fromSub.stdout != fromRoot.stdout {
			t.Errorf("REGRESSION (subdirectory invocation): running from %s gave\n%s\nbut the repository root gave\n%s", sub, fromSub.stdout, fromRoot.stdout)
		}
		if fromSub.code != fromRoot.code {
			t.Errorf("REGRESSION (subdirectory invocation): exit code from %s = %d, from the root = %d", sub, fromSub.code, fromRoot.code)
		}
	}
}

// Filenames with spaces and non-ASCII characters have to survive git's -z
// output, recon's index and the runner's argv untouched. A path that is split
// on the space becomes two files that do not exist, both of which map to no
// test at all.
func TestSpaceAndUnicodeFilenamesRoundTrip(t *testing.T) {
	const (
		src  = "src/naïve api.py"
		test = "tests/test_naïve api.py"
	)

	r := newRepo(t, "python")
	r.write(t, src, "def naive_total(items):\n    return sum(items)\n")
	r.write(t, test, "def test_naive_total():\n    assert True\n")
	r.commit(t, "add a file with a space and a diaeresis")
	r.warm(t)
	r.append(t, src, "\n\n# touched\n")

	var result struct {
		ChangedFiles []string `json:"changed_files"`
		Tests        []struct {
			Path string `json:"path"`
		} `json:"tests"`
	}
	res := r.run(t, "select", "--format", "json")
	if err := json.Unmarshal([]byte(res.stdout), &result); err != nil {
		t.Fatalf("select --format json: %v\n%s", err, res.stdout)
	}
	if len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != src {
		t.Errorf("REGRESSION (path round-trip): changed_files = %q, want [%q]", result.ChangedFiles, src)
	}
	if len(result.Tests) != 1 || result.Tests[0].Path != test {
		t.Errorf("REGRESSION (path round-trip): tests = %+v, want [%q]", result.Tests, test)
	}

	// The printed command has to be pasteable, which means the space is quoted
	// — and the quoting must not leak into the argv the runner executes.
	cmd := strings.TrimSpace(r.run(t, "select", "--format", "exec").stdout)
	if want := `pytest 'tests/test_naïve api.py'`; cmd != want {
		t.Errorf("REGRESSION (path round-trip): exec command = %q, want %q", cmd, want)
	}
}

// A brand-new file is untracked, and `git diff` never mentions it. Missing it
// means the file nobody has committed yet — the one most likely to be wrong —
// selects nothing.
func TestUntrackedFileIsDetected(t *testing.T) {
	r := newRepo(t, "go")
	r.write(t, "calc/mul.go", "package calc\n\n// Mul returns a*b.\nfunc Mul(a, b int) int {\n\treturn a * b\n}\n")
	r.write(t, "calc/mul_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestMul(t *testing.T) {\n\tif Mul(2, 3) != 6 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")

	res := r.run(t, "select", "--format", "json")
	if !strings.Contains(res.stdout, "calc/mul.go") {
		t.Errorf("REGRESSION (untracked files): a new uncommitted file was not detected as a change:\n%s", res.stdout)
	}
	if !strings.Contains(res.stdout, "calc/mul_test.go") {
		t.Errorf("REGRESSION (untracked files): the new test was not selected:\n%s", res.stdout)
	}
}

// The heart of the product: what happens when witness cannot vouch for the
// selection. Whatever --fallback says, "print nothing and exit 0" is never it.
func TestEmptySelectionHonoursFallback(t *testing.T) {
	r := newRepo(t, "rust")
	// A new source file no test covers: the shape of the false green.
	r.write(t, "src/shipping.rs", "pub fn ship(order_id: u32) -> u32 {\n    order_id\n}\n")

	t.Run("full", func(t *testing.T) {
		res := r.run(t, "select", "--format", "exec")
		if got := strings.TrimSpace(res.stdout); got != "cargo test" {
			t.Errorf("REGRESSION (fail open): --fallback=full produced %q, want the whole suite `cargo test`", got)
		}
		if !strings.Contains(res.stderr, "full suite") {
			t.Errorf("--fallback=full must explain itself on stderr, got %q", res.stderr)
		}
	})

	t.Run("fail", func(t *testing.T) {
		res := r.run(t, "select", "--format", "exec", "--fallback", "fail")
		if res.code == 0 {
			t.Errorf("REGRESSION (fail open): --fallback=fail exited 0 on a selection witness cannot prove")
		}
		if res.err == nil || !strings.Contains(res.err.Error(), "cannot prove") {
			t.Errorf("--fallback=fail error = %v, want it to say witness cannot prove the selection", res.err)
		}
		if strings.TrimSpace(res.stdout) != "" {
			t.Errorf("--fallback=fail must not print a command to run: %q", res.stdout)
		}
	})

	t.Run("none", func(t *testing.T) {
		res := r.run(t, "select", "--format", "exec", "--fallback", "none")
		if res.code != 0 {
			t.Errorf("--fallback=none is the explicit opt-out and must succeed, got exit %d (%v)", res.code, res.err)
		}
		if !strings.Contains(res.stderr, "cannot prove") {
			t.Errorf("REGRESSION (silent pass): --fallback=none exited 0 without warning; stderr = %q", res.stderr)
		}
	})
}

// A JVM tree with no build file at its root cannot be turned into a command:
// mvn, gradle and sbt each select tests by class name, and which of them owns
// the tree — and therefore which selector to write — is only knowable from the
// build file. witness has to say so and exit non-zero: the alternative that
// shipped once was handing the .java paths to a shell, which exits 126 ("cannot
// execute") and reads as a test failure nobody can explain.
//
// (With the fixture's pom.xml in place the same selection IS derivable, and
// becomes `mvn test -Dtest=CalculatorTest` — see the java/source-edit golden.)
func TestUnknownLanguageReportsNoRunner(t *testing.T) {
	r := newRepo(t, "java")
	// Committed, not just deleted: a deleted file is itself a gap, which would
	// widen to the full suite and prove something else.
	r.remove(t, "pom.xml")
	r.commit(t, "drop the build file")
	r.append(t, "src/main/java/com/example/Calculator.java", "\n// touched\n")

	res := r.runBinary(t, r.root, "run")
	switch res.code {
	case 0:
		t.Fatalf("REGRESSION (fail open): `witness run` exited 0 on a language it cannot run\nstdout: %s\nstderr: %s", res.stdout, res.stderr)
	case 126, 127:
		t.Fatalf("REGRESSION (paths executed as a program): exit %d — witness tried to execute a test path instead of reporting that it has no java runner\nstderr: %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stderr, "no test runner known for language") {
		t.Errorf("stderr = %q, want it to name the missing runner", res.stderr)
	}
	if !strings.Contains(res.stderr, "CalculatorTest.java") {
		t.Errorf("stderr = %q, want it to name the tests that will not run", res.stderr)
	}
}

// Audit.java ends in "it", the JUnit integration-test suffix. Classifying it as
// a test sends `mvn`/`gradle` after a class with no tests in it, and — worse —
// makes an edit to production code look like an edit to its own test.
func TestProductionFileEndingInITIsNotATest(t *testing.T) {
	r := newRepo(t, "java")
	r.append(t, "src/main/java/com/example/Audit.java", "\n// touched\n")

	res := r.run(t, "select", "--format", "json")
	if strings.Contains(res.stdout, "Audit.java\",") || strings.Contains(res.stdout, `"path": "src/main/java/com/example/Audit.java"`) {
		t.Errorf("REGRESSION (java misclassification): Audit.java was selected as a test:\n%s", res.stdout)
	}
	if !strings.Contains(res.stderr, "no test covers") {
		t.Errorf("REGRESSION (silent pass): a change no test covers must be reported; stderr = %q", res.stderr)
	}
}

// `cargo test <path>` is a name filter, not a path: it matches nothing, prints
// "0 passed; N filtered out" and exits 0. Every rust selection has to name a
// cargo target instead.
func TestRustSelectsACargoTargetNotAPathFilter(t *testing.T) {
	r := newRepo(t, "rust")
	r.append(t, "src/orders.rs", "\npub fn count(orders: &[Order]) -> usize {\n    orders.len()\n}\n")

	got := strings.TrimSpace(r.run(t, "select", "--format", "exec").stdout)
	if want := "cargo test --test orders_test"; got != want {
		t.Errorf("REGRESSION (rust false green): command = %q, want %q", got, want)
	}
	if strings.Contains(got, ".rs") {
		t.Errorf("REGRESSION (rust false green): %q passes a file path to cargo, which silently filters out every test", got)
	}
}

// The proof behind the previous test: cargo has to actually run the test.
// `cargo test tests/orders_test.rs` prints "0 passed; 1 filtered out" and exits
// 0, which is indistinguishable from a green suite unless something reads the
// output — so read it.
func TestRustCommandActuallyRunsTheSelectedTarget(t *testing.T) {
	requireToolchain(t, "cargo", "rust")

	r := newRepo(t, "rust")
	r.append(t, "src/orders.rs", "\npub fn count(orders: &[Order]) -> usize {\n    orders.len()\n}\n")

	pass := r.runBinary(t, r.root, "run", "--fallback", "fail")
	out := pass.stdout + pass.stderr
	if pass.code != 0 {
		t.Fatalf("`witness run` on a passing rust suite exited %d\n%s", pass.code, out)
	}
	if !strings.Contains(out, "1 passed") {
		t.Errorf("REGRESSION (rust false green): cargo ran but did not run the test:\n%s", out)
	}
	if strings.Contains(out, "0 passed") || strings.Contains(out, "1 filtered out") {
		t.Errorf("REGRESSION (rust false green): cargo filtered the test out and still exited 0:\n%s", out)
	}

	// And the gate has teeth: break the assertion, keep the exit code.
	r.write(t, "tests/orders_test.rs", `use orders::orders::{total, Order};

#[test]
fn sums_totals() {
    assert_eq!(total(&[Order { total: 2 }, Order { total: 3 }]), 6);
}
`)
	fail := r.runBinary(t, r.root, "run", "--fallback", "fail")
	if fail.code == 0 {
		t.Errorf("REGRESSION (false green): a failing rust suite exited 0\n%s", fail.stdout+fail.stderr)
	}
}

// requireToolchain skips when the runner a fixture needs is not installed, or
// is installed as a shim with nothing behind it (rustup with no default
// toolchain), which is a skip rather than a failure of witness.
func requireToolchain(t *testing.T, bin, lang string) {
	t.Helper()
	path, err := exec.LookPath(bin)
	if err != nil {
		t.Skipf("%s is not installed; skipping the %s end-to-end run", bin, lang)
	}
	if out, err := exec.Command(path, "--version").CombinedOutput(); err != nil {
		t.Skipf("%s --version failed (%v: %s); skipping the %s end-to-end run", bin, err, out, lang)
	}
}

// A change spanning two ecosystems must produce one command per ecosystem. The
// single merged command that shipped once (`mix test ... cart.test.js`) fails
// to start, or worse runs half the suite and reports success.
func TestPolyglotChangeProducesOneCommandPerEcosystem(t *testing.T) {
	r := newRepo(t, "polyglot")
	r.append(t, "lib/shop/cart.ex", "\n# touched\n")
	r.append(t, "assets/js/cart.js", "\n// touched\n")

	lines := nonEmptyLines(r.run(t, "select", "--format", "exec").stdout)
	if len(lines) != 2 {
		t.Fatalf("REGRESSION (polyglot runner): got %d command(s) %q, want one per ecosystem", len(lines), lines)
	}
	for _, line := range lines {
		if strings.Contains(line, ".exs") && strings.Contains(line, ".js") {
			t.Errorf("REGRESSION (polyglot runner): %q mixes two ecosystems into one command", line)
		}
	}
	if !strings.HasPrefix(lines[0], "mix test ") || !strings.HasPrefix(lines[1], "npx jest ") {
		t.Errorf("commands = %q, want a mix command and a jest command", lines)
	}
}

// jest reads a positional argument as a REGEX matched against the paths it
// collected, so a Next.js App Router route group — src/app/(marketing)/... —
// has its parens read as a capture group and matches zero files. jest reports
// "No tests found" and, with the mainstream passWithNoTests setting, exits 0:
// witness passes having run none of the tests it selected.
func TestJestSelectionIsRunByPathNotByPattern(t *testing.T) {
	r := newRepo(t, "node")
	r.append(t, "src/app/(marketing)/page.js", "\nexport function subhead(name) {\n  return `Hi, ${name}`;\n}\n")

	cmd := strings.TrimSpace(r.run(t, "select", "--format", "exec").stdout)
	if !strings.Contains(cmd, "(marketing)") {
		t.Fatalf("REGRESSION (jest selection): command = %q, want the route-group test selected", cmd)
	}
	if !strings.Contains(cmd, "--runTestsByPath") {
		t.Errorf("REGRESSION (jest false green): command = %q passes the path as a regex; a route group matches nothing and passWithNoTests exits 0", cmd)
	}
}

// The fail-closed fallback has no selected paths to correct a bad framework
// guess with, so it has to follow the repo's primary language. Framework
// detection matches any name containing ".net"/"xunit", and a Go repository
// holding a C# fixture directory answered `dotnet test` — which dies with
// MSB1003 having run nothing, in the one code path whose job is to run
// everything.
func TestFullSuiteFallbackFollowsThePrimaryLanguage(t *testing.T) {
	r := newRepo(t, "go")
	// A C# test project sitting in the repo as a fixture, exactly like the one
	// in witness's own testdata.
	r.write(t, "testdata/fixtures/csharp/Orders.Tests/Orders.Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <PackageReference Include="xunit" Version="2.9.2" />
  </ItemGroup>
</Project>
`)
	r.write(t, "testdata/fixtures/csharp/Orders.Tests/CalculatorTests.cs", "namespace Orders.Tests;\n\npublic class CalculatorTests\n{\n}\n")
	r.commit(t, "add a C# fixture")
	r.warm(t)

	// A change nothing covers, so the fallback decides the command.
	r.append(t, "go.mod", "\n// touched\n")

	res := r.run(t, "select", "--format", "exec")
	if got := strings.TrimSpace(res.stdout); got != "go test ./..." {
		t.Errorf("REGRESSION (fallback runs nothing): the whole-suite command for a Go repo = %q, want `go test ./...`\nstderr: %s", got, res.stderr)
	}
}

// A README-only change cannot break a test. Counting documentation as an
// uncovered file made every stray note, editor backup and untracked debug.log —
// all of which `git ls-files --others` reports — widen the run to the entire
// suite, which is not fail-closed, just useless.
func TestDocsOnlyChangeDoesNotRunTheWholeSuite(t *testing.T) {
	r := newRepo(t, "go")
	r.write(t, "NOTES.txt", "scratch\n")
	r.write(t, "docs/design.md", "# design\n")

	res := r.run(t, "select", "--format", "exec")
	if got := strings.TrimSpace(res.stdout); got != "" {
		t.Errorf("a docs-only change produced %q; documentation cannot change what a test does", got)
	}
	if res.code != 0 {
		t.Errorf("exit = %d (%v), want 0 for a change with nothing to test", res.code, res.err)
	}

	// A source change in the same tree still selects its test, so the filter
	// cannot have swallowed the diff.
	r.append(t, "calc/calc.go", "\n// Sub returns a - b.\nfunc Sub(a, b int) int { return a - b }\n")
	paths := strings.TrimSpace(r.run(t, "select", "--format", "paths").stdout)
	if paths != "calc/calc_test.go" {
		t.Errorf("selection alongside the docs change = %q, want calc/calc_test.go", paths)
	}
}

// An empty selection must marshal as [], not null: a consumer doing
// result.tests.length or iterating the array crashes on null, and a CI script
// that swallows the crash goes green.
func TestEmptySelectionJSONIsAnArrayNotNull(t *testing.T) {
	r := newRepo(t, "java")
	r.append(t, "src/main/java/com/example/Audit.java", "\n// touched\n")

	for _, args := range [][]string{
		{"select", "--format", "json"},
		{"select", "--format", "json", "--staged"},
	} {
		res := r.run(t, args...)
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(res.stdout), &raw); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, res.stdout)
		}
		for _, field := range []string{"changed_files", "tests"} {
			if string(raw[field]) == "null" {
				t.Errorf("REGRESSION (null JSON): %v marshalled %q as null, want []", args, field)
			}
		}
	}
}

// The end of the pipeline, with a real toolchain: witness picks the tests,
// executes them, and exits with the runner's code. A gate that reports success
// while the suite it ran was red is the failure this whole repository is about.
func TestRunExecutesTheSelectedSuiteAndPropagatesItsExitCode(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not installed; cannot run the fixture's suite")
	}

	r := newRepo(t, "go")
	r.append(t, "calc/calc.go", "\n// Sub returns a - b.\nfunc Sub(a, b int) int { return a - b }\n")

	pass := r.runBinary(t, r.root, "run")
	if pass.code != 0 {
		t.Fatalf("a passing suite exited %d\nstdout: %s\nstderr: %s", pass.code, pass.stdout, pass.stderr)
	}
	if !strings.Contains(pass.stdout, "ok") {
		t.Errorf("the runner's output must reach stdout, got %q", pass.stdout)
	}

	// Break the assertion the fixture's test makes.
	r.write(t, "calc/calc.go", "// Package calc adds numbers.\npackage calc\n\n// Add returns a + b.\nfunc Add(a, b int) int {\n\treturn a - b\n}\n")
	fail := r.runBinary(t, r.root, "run")
	if fail.code == 0 {
		t.Errorf("REGRESSION (false green): a failing suite exited 0\nstdout: %s\nstderr: %s", fail.stdout, fail.stderr)
	}
	if fail.code != 1 {
		t.Errorf("exit code = %d, want the runner's own 1", fail.code)
	}
}

// The binary and the embedded command tree are the same code path, and the
// exit-code translation in main() is the only part the in-process tests cannot
// see. Pin them together so the harness's mirror of main() cannot drift.
func TestBinaryAgreesWithTheInProcessCommand(t *testing.T) {
	r := newRepo(t, "python")
	r.append(t, "src/api.py", "\n\ndef refund(order):\n    return order\n")

	cases := []struct {
		args []string
		// wantCode pins the answer as well as the agreement: two invocations
		// that both quietly exit 0 with no output would "agree" perfectly.
		wantCode int
	}{
		{args: []string{"select", "--format", "json"}, wantCode: 0},
		{args: []string{"select", "--format", "exec"}, wantCode: 0},
		// pyproject.toml is not a language recon indexes, so the selection
		// cannot be proven and --fallback=fail must make both forms exit 1.
		{args: []string{"select", "--format", "exec", "--fallback", "fail", "pyproject.toml"}, wantCode: 1},
	}

	for _, tc := range cases {
		inProcess := r.run(t, tc.args...)
		binary := r.runBinary(t, r.root, tc.args...)

		if inProcess.stdout != binary.stdout {
			t.Errorf("%v: stdout differs\nin-process: %q\nbinary:     %q", tc.args, inProcess.stdout, binary.stdout)
		}
		if inProcess.code != binary.code {
			t.Errorf("%v: exit code differs: in-process %d, binary %d (stderr: %q)", tc.args, inProcess.code, binary.code, binary.stderr)
		}
		if binary.code != tc.wantCode {
			t.Errorf("%v: exit code = %d, want %d (stderr: %q)", tc.args, binary.code, tc.wantCode, binary.stderr)
		}
	}
}

// --since is the mode CI actually uses. Nothing else in the suite drives it
// end-to-end, and a mode that silently reports no changes gates on nothing.
func TestSinceRefSelectsTheCommittedChange(t *testing.T) {
	r := newRepo(t, "go")
	r.append(t, "calc/calc.go", "\n// Sub returns a - b.\nfunc Sub(a, b int) int { return a - b }\n")
	r.commit(t, "add Sub")

	res := r.run(t, "select", "--format", "paths", "--since", "HEAD~1")
	if got := strings.TrimSpace(res.stdout); got != "calc/calc_test.go" {
		t.Errorf("REGRESSION (--since gates on nothing): selected %q, want calc/calc_test.go\nstderr: %s", got, res.stderr)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
