package selector

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/djtouchette/recon/pkg/recon"
)

// fakeIntel is a scripted RepoIntel for driving Select through its branches
// without a real repo or tree-sitter index.
type fakeIntel struct {
	testFiles  map[string]bool                 // paths recognized as tests
	tests      map[string][]recon.TestFile     // file -> mapped tests
	importedBy map[string][]string             // file -> importers
	cochange   map[string][]recon.CoChangePair // file -> co-changed pairs
	hotspot    map[string]float64              // file -> hotspot score
	testsErr   map[string]error                // file -> error from Tests
	notIndexed map[string]bool                 // paths recon has never scanned
	resolve    map[string]string               // caller-supplied path -> repo-relative path

	cochangeHook func(min int) // observes the minCount passed to CoChangedWith
}

func newFake() *fakeIntel {
	return &fakeIntel{
		testFiles:  map[string]bool{},
		tests:      map[string][]recon.TestFile{},
		importedBy: map[string][]string{},
		cochange:   map[string][]recon.CoChangePair{},
		hotspot:    map[string]float64{},
		testsErr:   map[string]error{},
		notIndexed: map[string]bool{},
		resolve:    map[string]string{},
	}
}

func (f *fakeIntel) IsTestFile(p string) bool { return f.testFiles[p] }
func (f *fakeIntel) Tests(p string, _ int) ([]recon.TestFile, error) {
	return f.tests[p], f.testsErr[p]
}
func (f *fakeIntel) ImportedBy(p string) []string { return f.importedBy[p] }
func (f *fakeIntel) CoChangedWith(p string, min int) []recon.CoChangePair {
	if f.cochangeHook != nil {
		f.cochangeHook(min)
	}
	return f.cochange[p]
}

// Context mirrors recon's: it resolves the caller-supplied path onto the
// repo-relative key the index uses and reports whether that key is indexed.
func (f *fakeIntel) Context(p string) (*recon.FileContext, error) {
	if r, ok := f.resolve[p]; ok {
		p = r
	}
	ctx := &recon.FileContext{Path: p, Status: recon.StatusIndexed}
	if f.notIndexed[p] {
		ctx.Status = recon.StatusNotIndexed
	}
	if s, ok := f.hotspot[p]; ok {
		ctx.HotspotScore = s
	}
	return ctx, nil
}

// assertPaths asserts the exact selected paths, in order. Ordering is part of
// the contract: --max truncates the tail, so a set assertion would let the
// sort tiebreaker rot without any test noticing.
func assertPaths(t *testing.T, res *SelectResult, want []string) {
	t.Helper()
	got := make([]string, len(res.Tests))
	for i, st := range res.Tests {
		got[i] = st.Path
	}
	if len(got) != len(want) {
		t.Fatalf("selected paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selected paths = %v, want %v", got, want)
		}
	}
}

// byPath indexes a result's tests for easy assertions.
func byPath(res *SelectResult) map[string]ScoredTest {
	m := map[string]ScoredTest{}
	for _, t := range res.Tests {
		m[t.Path] = t
	}
	return m
}

func TestSelect_ChangedFileIsTest(t *testing.T) {
	f := newFake()
	f.testFiles["foo_test.go"] = true

	res, err := Select(f, []string{"foo_test.go"}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	got := byPath(res)
	st, ok := got["foo_test.go"]
	if !ok {
		t.Fatalf("changed test not selected: %+v", res.Tests)
	}
	if st.Score != 1.0 {
		t.Errorf("score = %v, want 1.0", st.Score)
	}
	if st.Signals[0] != "changed-test" {
		t.Errorf("signal = %v, want changed-test", st.Signals)
	}
}

// TestSelect_ChangedTestStillWalksTheGraph pins that a changed test file no
// longer short-circuits the rest of the pipeline. It used to `continue` after
// step 1, so a diff touching only a test skipped its importers, its co-changed
// tests and its direct mappings.
func TestSelect_ChangedTestStillWalksTheGraph(t *testing.T) {
	f := newFake()
	f.testFiles["spec/models/user_spec.rb"] = true
	f.testFiles["spec/models/account_spec.rb"] = true
	f.testFiles["spec/requests/signup_spec.rb"] = true
	f.importedBy["spec/models/user_spec.rb"] = []string{"spec/requests/signup_spec.rb"}
	f.cochange["spec/models/user_spec.rb"] = []recon.CoChangePair{
		{File: "spec/models/account_spec.rb", Count: 20},
	}

	res, _ := Select(f, []string{"spec/models/user_spec.rb"}, DefaultOptions())
	want := []string{
		"spec/models/user_spec.rb",     // 1.0 changed-test
		"spec/requests/signup_spec.rb", // 0.8 import-1hop
		"spec/models/account_spec.rb",  // 0.6 co-change
	}
	assertPaths(t, res, want)
}

// TestSelect_ChangedTestSupportFilePullsInDependents is the headline case: a
// shared fixture is not a runnable test, so it must never be handed to the
// runner, and the tests that depend on it must still be selected. Previously
// witness returned the fixture alone (pytest/jest then collect zero tests) and
// nothing that could catch the breakage.
func TestSelect_ChangedTestSupportFilePullsInDependents(t *testing.T) {
	f := newFake()
	// recon classifies everything under tests/ as a test, fixtures included.
	f.testFiles["tests/conftest.py"] = true
	f.testFiles["tests/test_api.py"] = true
	f.testFiles["tests/test_billing.py"] = true
	f.importedBy["tests/conftest.py"] = []string{"tests/test_api.py"}
	f.cochange["tests/conftest.py"] = []recon.CoChangePair{
		{File: "tests/test_billing.py", Count: 20},
	}

	res, _ := Select(f, []string{"tests/conftest.py"}, DefaultOptions())
	assertPaths(t, res, []string{"tests/test_api.py", "tests/test_billing.py"})
}

func TestSelect_TestSupportFileIsNeverEmitted(t *testing.T) {
	f := newFake()
	// Discovered as an importer rather than as the changed file: the helper
	// still must not become a path the runner is pointed at.
	f.importedBy["src/api.ts"] = []string{"__tests__/setup.ts", "__tests__/api.test.ts"}
	f.testFiles["__tests__/setup.ts"] = true
	f.testFiles["__tests__/api.test.ts"] = true

	res, _ := Select(f, []string{"src/api.ts"}, DefaultOptions())
	assertPaths(t, res, []string{"__tests__/api.test.ts"})
}

// TestSelect_JestConventionTestsUnderTestsDirAreSelected is the other side of
// the support-file guard. jest's default testMatch collects EVERY file under
// __tests__/, named like a test or not, so demoting __tests__/login.js dropped
// a real suite: the change reported no covering test at all.
func TestSelect_JestConventionTestsUnderTestsDirAreSelected(t *testing.T) {
	f := newFake()
	f.tests["src/login.js"] = []recon.TestFile{{Path: "__tests__/login.js"}}
	f.testFiles["__tests__/login.js"] = true

	res, _ := Select(f, []string{"src/login.js"}, DefaultOptions())
	assertPaths(t, res, []string{"__tests__/login.js"})
	if len(res.Summary.Unmapped) != 0 {
		t.Errorf("the change is covered, Unmapped = %v", res.Summary.Unmapped)
	}

	// And when the jest test is itself the changed file.
	changed, _ := Select(f, []string{"__tests__/login.js"}, DefaultOptions())
	assertPaths(t, changed, []string{"__tests__/login.js"})
}

// TestSelect_CSharpTestClassesWithoutTestSuffixAreSelected pins the C# half:
// xUnit and MSpec find test classes by attribute, not by name, so *Facts.cs,
// *Scenarios.cs and When_*.cs are real suites. Demoting them made an edit to
// one select nothing, and made a .NET project whose classes are all named that
// way fall back to the whole solution.
func TestSelect_CSharpTestClassesWithoutTestSuffixAreSelected(t *testing.T) {
	const scenarios = "tests/MyApp.Tests/OrderScenarios.cs"

	f := newFake()
	f.tests["src/MyApp/Order.cs"] = []recon.TestFile{{Path: scenarios}}
	f.testFiles[scenarios] = true

	res, _ := Select(f, []string{"src/MyApp/Order.cs"}, DefaultOptions())
	assertPaths(t, res, []string{scenarios})

	changed, _ := Select(f, []string{scenarios}, DefaultOptions())
	assertPaths(t, changed, []string{scenarios})
}

// TestSelect_DocsAndAssetsAreNotCoverageHoles keeps the fail-closed fallback
// aimed at changes that can actually break a test. Witness reports every
// changed file no test covers, and an uncovered file is what widens a run to
// the whole suite — so a stray NOTES.txt or an untracked debug.log (both of
// which `git ls-files --others` reports) made an ordinary working tree run the
// entire suite on every invocation. A build manifest is a different matter: a
// go.mod bump changes what the code compiles against, and that gap is the whole
// reason the fallback exists.
func TestSelect_DocsAndAssetsAreNotCoverageHoles(t *testing.T) {
	f := newFake()
	changed := []string{"NOTES.txt", "docs/guide.md", "debug.log", "assets/logo.png", "go.mod"}

	res, _ := Select(f, changed, DefaultOptions())
	if len(res.ChangedFiles) != len(changed) {
		t.Errorf("changed files = %v, want all %d reported", res.ChangedFiles, len(changed))
	}
	if got := res.Summary.Unmapped; len(got) != 1 || got[0] != "go.mod" {
		t.Errorf("Unmapped = %v, want [go.mod] — docs and assets cannot break a test, a dependency bump can", got)
	}
}

func TestSelect_DirectTestMapping(t *testing.T) {
	f := newFake()
	f.tests["src/foo.go"] = []recon.TestFile{{Path: "src/foo_test.go", Kind: "unit"}}

	res, _ := Select(f, []string{"src/foo.go"}, DefaultOptions())
	got := byPath(res)
	st, ok := got["src/foo_test.go"]
	if !ok {
		t.Fatalf("direct test not selected: %+v", res.Tests)
	}
	if st.Score != 1.0 || st.Kind != "unit" {
		t.Errorf("got score=%v kind=%q, want 1.0/unit", st.Score, st.Kind)
	}
}

func TestSelect_ImportHopScores(t *testing.T) {
	f := newFake()
	// src/a.go <- src/b.go (1 hop) <- src/c.go (2 hops).
	f.importedBy["src/a.go"] = []string{"src/b.go"}
	f.importedBy["src/b.go"] = []string{"src/c.go"}
	f.tests["src/b.go"] = []recon.TestFile{{Path: "b_test.go", Kind: "unit"}}
	f.tests["src/c.go"] = []recon.TestFile{{Path: "c_test.go", Kind: "unit"}}

	res, _ := Select(f, []string{"src/a.go"}, SelectOptions{MaxDepth: 2, MinScore: 0.1, MaxTests: 50})
	got := byPath(res)
	if got["b_test.go"].Score != 0.8 {
		t.Errorf("1-hop test score = %v, want 0.8", got["b_test.go"].Score)
	}
	if got["c_test.go"].Score != 0.5 {
		t.Errorf("2-hop test score = %v, want 0.5", got["c_test.go"].Score)
	}
}

func TestSelect_ImporterThatIsItselfATest(t *testing.T) {
	f := newFake()
	f.importedBy["src/a.go"] = []string{"src/a_test.go"}
	f.testFiles["src/a_test.go"] = true

	res, _ := Select(f, []string{"src/a.go"}, DefaultOptions())
	got := byPath(res)
	st, ok := got["src/a_test.go"]
	if !ok || st.Score != 0.8 {
		t.Errorf("test importer not scored as 1-hop: %+v", res.Tests)
	}
}

func TestSelect_FanOutCapSkipsExplosion(t *testing.T) {
	f := newFake()
	// src/util.go has >100 importers — traversal must skip it.
	many := make([]string, defaultFanOutCap+5)
	for i := range many {
		many[i] = "imp" + itoa(i) + ".go"
	}
	f.importedBy["src/util.go"] = many
	// If it were traversed, this importer would surface a test.
	f.tests["imp0.go"] = []recon.TestFile{{Path: "imp0_test.go"}}

	res, _ := Select(f, []string{"src/util.go"}, DefaultOptions())
	if _, ok := byPath(res)["imp0_test.go"]; ok {
		t.Error("fan-out cap not enforced: high-importer file dragged in a test")
	}
}

func TestSelect_FanOutAtCapStillTraverses(t *testing.T) {
	f := newFake()
	// Exactly defaultFanOutCap importers is allowed (cap is "> defaultFanOutCap").
	many := make([]string, defaultFanOutCap)
	for i := range many {
		many[i] = "imp" + itoa(i) + ".go"
	}
	f.importedBy["src/util.go"] = many
	f.tests["imp0.go"] = []recon.TestFile{{Path: "imp0_test.go"}}

	res, _ := Select(f, []string{"src/util.go"}, DefaultOptions())
	if _, ok := byPath(res)["imp0_test.go"]; !ok {
		t.Error("a file with exactly the cap of importers should still traverse")
	}
}

func TestSelect_CoChange(t *testing.T) {
	f := newFake()
	f.cochange["src/a.go"] = []recon.CoChangePair{
		{File: "src/b.go", Count: 10},  // -> 0.6
		{File: "co_test.go", Count: 3}, // test file directly -> 0.3
	}
	f.tests["src/b.go"] = []recon.TestFile{{Path: "b_test.go"}}
	f.testFiles["co_test.go"] = true

	res, _ := Select(f, []string{"src/a.go"}, DefaultOptions())
	got := byPath(res)
	if got["b_test.go"].Score != 0.6 {
		t.Errorf("co-change(10) mapped test = %v, want 0.6", got["b_test.go"].Score)
	}
	if got["co_test.go"].Score != 0.3 {
		t.Errorf("co-change(3) test = %v, want 0.3", got["co_test.go"].Score)
	}
}

func TestSelect_CSharpSingularTestDomainModelIsNotATest(t *testing.T) {
	f := newFake()
	f.cochange["src/certificates/CertificateOrchestrationService.cs"] = []recon.CoChangePair{
		{File: "src/Domains/Leroy.Certificates/Models/LabTest.cs", Count: 10},
		{File: "backend/tests/Leroy.Platform.Tests/CertificateOrchestrationServiceTests.cs", Count: 10},
		{File: "backend/tests/Leroy.Platform.Tests/Leroy.Platform.Tests.csproj", Count: 10},
	}
	// recon v0.13.1 requires test-project evidence before calling a .cs file a
	// test, so LabTest.cs (a domain model under src/) is reported as source.
	// The blanket "reject every .cs recon calls a test" workaround is gone;
	// what has to hold now is that witness's own conventional check doesn't
	// re-introduce the false positive.
	f.testFiles["backend/tests/Leroy.Platform.Tests/CertificateOrchestrationServiceTests.cs"] = true
	f.testFiles["backend/tests/Leroy.Platform.Tests/Leroy.Platform.Tests.csproj"] = true

	res, _ := Select(f, []string{"src/certificates/CertificateOrchestrationService.cs"}, DefaultOptions())
	got := byPath(res)
	if _, ok := got["src/Domains/Leroy.Certificates/Models/LabTest.cs"]; ok {
		t.Fatalf("singular C# domain model should not be selected as a test: %+v", res.Tests)
	}
	if _, ok := got["backend/tests/Leroy.Platform.Tests/CertificateOrchestrationServiceTests.cs"]; !ok {
		t.Fatalf("C# test project file should still be selected: %+v", res.Tests)
	}
	if _, ok := got["backend/tests/Leroy.Platform.Tests/Leroy.Platform.Tests.csproj"]; ok {
		t.Fatalf("C# project metadata should not be selected as a test: %+v", res.Tests)
	}
}

// TestSelect_CSharpReconMappedTestsInProjectDirs pins the other half of the C#
// trade-off. isSelectableTest used to return false for every .cs path that
// failed the conventional check, before recon was ever consulted, so a test
// recon had mapped directly to the changed file was discarded — a .NET repo
// laid out as src/MyApp + IntegrationTests/ selected nothing at all.
func TestSelect_CSharpReconMappedTestsInProjectDirs(t *testing.T) {
	mapped := []string{
		"UnitTests/CalculatorTest.cs",
		"src/MyApp/UnitTests/CalculatorTest.cs",
		"backend/IntegrationTests/OrderTest.cs",
		"src/MyApp.Specs/CalculatorSpec.cs",
		"backend/Leroy.Platform.Tests/CalculatorTests.cs",
	}
	f := newFake()
	for _, p := range mapped {
		f.tests["src/MyApp/Calculator.cs"] = append(f.tests["src/MyApp/Calculator.cs"], recon.TestFile{Path: p})
		f.testFiles[p] = true
	}

	res, _ := Select(f, []string{"src/MyApp/Calculator.cs"}, DefaultOptions())
	got := byPath(res)
	for _, p := range mapped {
		if _, ok := got[p]; !ok {
			t.Errorf("recon-mapped C# test %q was dropped: %+v", p, res.Tests)
		}
	}
	if n := len(res.Summary.Unmapped); n != 0 {
		t.Errorf("changed file should be covered, Unmapped = %v", res.Summary.Unmapped)
	}
}

func TestSelect_HotspotBoost(t *testing.T) {
	f := newFake()
	f.importedBy["src/a.go"] = []string{"src/b.go"}
	f.tests["src/b.go"] = []recon.TestFile{{Path: "b_test.go"}}
	f.hotspot["src/a.go"] = 0.5 // > 0.3 triggers boost

	res, _ := Select(f, []string{"src/a.go"}, DefaultOptions())
	got := byPath(res)
	// 1-hop 0.8 + 0.1 hotspot = 0.9.
	if got["b_test.go"].Score != 0.9 {
		t.Errorf("hotspot-boosted score = %v, want 0.9", got["b_test.go"].Score)
	}
	found := false
	for _, s := range got["b_test.go"].Signals {
		if s == "hotspot-risk" {
			found = true
		}
	}
	if !found {
		t.Errorf("hotspot-risk signal missing: %v", got["b_test.go"].Signals)
	}
}

func TestSelect_HotspotDoesNotBoostDirectTests(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{{Path: "a_test.go"}} // direct, score 1.0
	f.hotspot["src/a.go"] = 0.9

	res, _ := Select(f, []string{"src/a.go"}, DefaultOptions())
	if s := byPath(res)["a_test.go"].Score; s != 1.0 {
		t.Errorf("direct test score should stay capped at 1.0, got %v", s)
	}
}

func TestSelect_MinScoreFilter(t *testing.T) {
	f := newFake()
	f.importedBy["src/a.go"] = []string{"src/b.go"}
	f.importedBy["src/b.go"] = []string{"src/c.go"}
	f.tests["src/c.go"] = []recon.TestFile{{Path: "c_test.go"}} // 2-hop = 0.5

	// MinScore above 0.5 should filter the 2-hop test out.
	res, _ := Select(f, []string{"src/a.go"}, SelectOptions{MaxDepth: 2, MinScore: 0.6, MaxTests: 50})
	if _, ok := byPath(res)["c_test.go"]; ok {
		t.Error("test below MinScore should be filtered")
	}
}

func TestSelect_MaxTestsCap(t *testing.T) {
	f := newFake()
	var tests []recon.TestFile
	for i := 0; i < 10; i++ {
		tests = append(tests, recon.TestFile{Path: "t" + itoa(i) + "_test.go"})
	}
	f.tests["src/a.go"] = tests

	res, _ := Select(f, []string{"src/a.go"}, SelectOptions{MaxDepth: 2, MinScore: 0.1, MaxTests: 3})
	// The survivors are the lexicographically first three, not three at random.
	assertPaths(t, res, []string{"t0_test.go", "t1_test.go", "t2_test.go"})
	if res.Summary.TestsSelected != 3 {
		t.Errorf("summary count = %d, want 3", res.Summary.TestsSelected)
	}
	// A silent cap is a silent false green: 7 tests that passed every filter
	// are not going to run, and only Truncated says so.
	if res.Summary.Truncated != 7 {
		t.Errorf("Summary.Truncated = %d, want 7", res.Summary.Truncated)
	}
}

// TestSelect_BySignalCountsPostTruncation pins that the summary describes the
// run that will actually happen. by_signal used to be accumulated over the
// whole candidate set, so it credited signals whose tests the cap dropped.
func TestSelect_BySignalCountsPostTruncation(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{{Path: "direct_test.go"}} // 1.0
	f.cochange["src/a.go"] = []recon.CoChangePair{{File: "co_test.go", Count: 2}}
	f.testFiles["co_test.go"] = true // 0.3, dropped by MaxTests: 1

	res, _ := Select(f, []string{"src/a.go"}, SelectOptions{MaxDepth: 2, MinScore: 0.1, MaxTests: 1})
	assertPaths(t, res, []string{"direct_test.go"})
	if res.Summary.BySignal["co-change"] != 0 {
		t.Errorf("by_signal credits a signal that contributed no returned test: %v", res.Summary.BySignal)
	}
	if res.Summary.BySignal["direct-test"] != 1 {
		t.Errorf("by_signal = %v, want direct-test:1", res.Summary.BySignal)
	}
}

func TestSelect_SortByScoreThenPath(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{{Path: "zzz_test.go"}} // 1.0
	f.importedBy["src/a.go"] = []string{"src/b.go", "src/c.go"}
	// Two 1-hop tests share a score, so the path tiebreaker is the only thing
	// separating them — a map is ranged over to build the list, so without it
	// the order is whatever the runtime feels like.
	f.tests["src/b.go"] = []recon.TestFile{{Path: "mmm_test.go"}} // 0.8
	f.tests["src/c.go"] = []recon.TestFile{{Path: "aaa_test.go"}} // 0.8

	res, _ := Select(f, []string{"src/a.go"}, DefaultOptions())
	assertPaths(t, res, []string{"zzz_test.go", "aaa_test.go", "mmm_test.go"})
}

// TestSelect_OrderIsStableAcrossRuns catches a tiebreaker that is present but
// wrong (or a sort that isn't total): identical input must produce byte-equal
// output, or --max reruns a different subset on every CI invocation.
func TestSelect_OrderIsStableAcrossRuns(t *testing.T) {
	build := func() *fakeIntel {
		f := newFake()
		var tests []recon.TestFile
		for i := 0; i < 10; i++ {
			tests = append(tests, recon.TestFile{Path: "t" + itoa(i) + "_test.go"})
		}
		f.tests["src/a.go"] = tests
		return f
	}
	opts := SelectOptions{MaxDepth: 2, MinScore: 0.1, MaxTests: 3}

	first, _ := Select(build(), []string{"src/a.go"}, opts)
	for i := 0; i < 20; i++ {
		res, _ := Select(build(), []string{"src/a.go"}, opts)
		assertPaths(t, res, []string{first.Tests[0].Path, first.Tests[1].Path, first.Tests[2].Path})
	}
}

// TestSelect_EmptySelectionIsAnArray keeps the JSON contract honest: an
// uninitialized slice marshals to null, which every consumer has to special
// case before it can iterate.
func TestSelect_EmptySelectionIsAnArray(t *testing.T) {
	res, _ := Select(newFake(), []string{"README.md"}, DefaultOptions())
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"tests":[]`) {
		t.Errorf("empty selection should marshal to [], got %s", b)
	}
}

// TestSelect_UnmappedChangedFilesAreReported is the fail-closed hook: "this
// change needs no tests" and "recon could not tell me" were the same empty
// list, and the CLI exited 0 on both.
func TestSelect_UnmappedChangedFilesAreReported(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{{Path: "a_test.go"}}
	f.notIndexed["vendor/thirdparty/lib.go"] = true

	res, _ := Select(f, []string{"src/a.go", "src/b.go", "vendor/thirdparty/lib.go"}, DefaultOptions())
	assertPaths(t, res, []string{"a_test.go"})
	wantUnmapped := []string{"src/b.go", "vendor/thirdparty/lib.go"}
	if !equalStrings(res.Summary.Unmapped, wantUnmapped) {
		t.Errorf("Summary.Unmapped = %v, want %v", res.Summary.Unmapped, wantUnmapped)
	}
	if !equalStrings(res.Summary.NotIndexed, []string{"vendor/thirdparty/lib.go"}) {
		t.Errorf("Summary.NotIndexed = %v, want [vendor/thirdparty/lib.go]", res.Summary.NotIndexed)
	}
}

// TestSelect_ExcludedOnlyTestLeavesFileUnmapped pins that a hole punched by the
// user's own flags is reported too — the tests exist, but none of them will run
// for that file.
func TestSelect_ExcludedOnlyTestLeavesFileUnmapped(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{{Path: "generated/a_test.go"}}
	opts := DefaultOptions()
	opts.Exclude = []string{"generated/**"}

	res, _ := Select(f, []string{"src/a.go"}, opts)
	if !equalStrings(res.Summary.Unmapped, []string{"src/a.go"}) {
		t.Errorf("Summary.Unmapped = %v, want [src/a.go]", res.Summary.Unmapped)
	}
}

// TestSelect_ReconErrorIsRecorded keeps a failed analysis distinguishable from
// a clean "nothing to run". Select still returns its partial answer.
func TestSelect_ReconErrorIsRecorded(t *testing.T) {
	f := newFake()
	f.testsErr["src/a.go"] = errors.New("index is stale")

	res, err := Select(f, []string{"src/a.go"}, DefaultOptions())
	if err != nil {
		t.Fatalf("Select should return a partial result, not fail: %v", err)
	}
	if res.Summary.AnalysisError == "" {
		t.Fatal("recon error was swallowed; an unanalyzable change reads as 'no tests needed'")
	}
	if !strings.Contains(res.Summary.AnalysisError, "index is stale") ||
		!strings.Contains(res.Summary.AnalysisError, "src/a.go") {
		t.Errorf("AnalysisError = %q, want the file and the underlying cause", res.Summary.AnalysisError)
	}
}

// TestSelect_NormalizesChangedPaths covers the two shapes callers actually hand
// over: "./x" (git and shell completion) and an absolute path (embedders,
// editors, agents). Both used to miss, and "./x" additionally produced a second
// candidate for a file already selected under its clean name.
func TestSelect_NormalizesChangedPaths(t *testing.T) {
	f := newFake()
	f.testFiles["src/a_test.go"] = true
	f.tests["src/b.go"] = []recon.TestFile{{Path: "./src/b_test.go"}}
	// recon resolves an absolute path against the repo root; the fake mirrors it.
	f.resolve["/repo/src/b.go"] = "src/b.go"

	res, _ := Select(f, []string{"./src/a_test.go", "src/a_test.go", "/repo/src/b.go"}, DefaultOptions())
	assertPaths(t, res, []string{"src/a_test.go", "src/b_test.go"})
	if !equalStrings(res.ChangedFiles, []string{"src/a_test.go", "src/b.go"}) {
		t.Errorf("ChangedFiles = %v, want [src/a_test.go src/b.go]", res.ChangedFiles)
	}
	if res.Summary.Changed != 2 {
		t.Errorf("Summary.Changed = %d, want 2 (duplicates collapse)", res.Summary.Changed)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSelect_DefaultsApplied(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{{Path: "a_test.go", Kind: "unit"}}
	// Zero-value opts should be backfilled with defaults, not zero everything out.
	res, err := Select(f, []string{"src/a.go"}, SelectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tests) != 1 {
		t.Errorf("zero-value opts should apply defaults and select; got %d", len(res.Tests))
	}
}

func TestSelect_SummaryBySignal(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{{Path: "a_test.go"}}
	res, _ := Select(f, []string{"src/a.go"}, DefaultOptions())
	if res.Summary.BySignal["direct-test"] != 1 {
		t.Errorf("BySignal counts = %v, want direct-test:1", res.Summary.BySignal)
	}
	if res.Summary.Changed != 1 {
		t.Errorf("Changed = %d, want 1", res.Summary.Changed)
	}
}

// itoa avoids strconv import noise in fixtures.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
