package selector

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/djtouchette/recon/pkg/recon"
)

// RepoIntel is the slice of recon's repo intelligence that the selector needs.
// Taking an interface (rather than the concrete *recon.Recon) keeps the scoring
// logic unit-testable with a fake. *recon.Recon satisfies it structurally, so
// callers pass the real thing unchanged.
type RepoIntel interface {
	IsTestFile(path string) bool
	Tests(path string, maxResults int) ([]recon.TestFile, error)
	ImportedBy(path string) []string
	CoChangedWith(path string, minCount int) []recon.CoChangePair
	Context(path string) (*recon.FileContext, error)
}

// Select finds tests relevant to the given changed files using recon's repo intelligence.
// It combines direct test mapping, transitive import walks, co-change history,
// and hotspot risk scoring to produce a prioritized list.
//
// Select never fails the whole selection because one file could not be
// analyzed; instead it records what it could not account for on Summary
// (Unmapped, NotIndexed, AnalysisError) so a caller gating CI can tell "this
// change needs no tests" apart from "recon could not tell me", and fail closed
// on the second.
func Select(r RepoIntel, changedFiles []string, opts SelectOptions) (*SelectResult, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = defaultMaxDepth
	}
	if opts.MinScore <= 0 {
		opts.MinScore = defaultMinScore
	}
	if opts.MaxTests <= 0 {
		opts.MaxTests = defaultMaxTests
	}
	if opts.CoChangeMinCount <= 0 {
		opts.CoChangeMinCount = defaultCoChangeMinCount
	}
	if opts.FanOutCap <= 0 {
		opts.FanOutCap = defaultFanOutCap
	}

	candidates := make(map[string]*ScoredTest)

	// The first recon error is kept rather than discarded: "recon has no index"
	// and "nothing to run" are the same empty list otherwise, and only one of
	// them is safe to exit 0 on.
	var firstErr error
	noteErr := func(err error, op, file string) {
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("recon %s(%s): %w", op, file, err)
		}
	}

	normalized := make([]string, 0, len(changedFiles))
	var notIndexed []string
	seenChanged := make(map[string]bool)

	for _, raw := range changedFiles {
		// Context doubles as the path resolver: recon maps absolute, "./"- and
		// backslash-prefixed forms onto the repo-relative key its index is
		// actually built from, and reports whether it has ever seen the file.
		ctx, err := r.Context(raw)
		noteErr(err, "Context", raw)

		changed := normalizePath(raw)
		if ctx != nil && ctx.Path != "" {
			changed = normalizePath(ctx.Path)
		}
		if changed == "" || seenChanged[changed] {
			continue
		}
		seenChanged[changed] = true
		normalized = append(normalized, changed)

		if ctx != nil && ctx.Status == recon.StatusNotIndexed {
			notIndexed = append(notIndexed, changed)
		}

		// Step 1: If the changed file IS a runnable test, include it directly.
		// It does not short-circuit the rest: a changed test still has
		// importers, co-changed tests and (for shared fixtures) dependents that
		// this change can break.
		if isSelectableTest(r, changed) {
			addCandidate(candidates, changed, 1.0, "changed-test", changed, "")
		}

		// Step 2: Direct test matches via TestMap.
		tests, err := r.Tests(changed, -1)
		noteErr(err, "Tests", changed)
		for _, t := range tests {
			addTestCandidate(r, candidates, t.Path, 1.0, "direct-test", changed, t.Kind)
		}

		// Step 3: Reverse dependency BFS.
		visited := map[string]bool{changed: true}
		frontier := []string{changed}

		for depth := 1; depth <= opts.MaxDepth; depth++ {
			score := depthScore(depth)
			signal := fmt.Sprintf("import-%dhop", depth)
			var next []string

			for _, file := range frontier {
				importers := r.ImportedBy(file)
				if len(importers) > opts.FanOutCap {
					// Skip high-fan-out files (utilities) to avoid explosion.
					continue
				}
				for _, imp := range importers {
					imp = normalizePath(imp)
					if imp == "" || visited[imp] {
						continue
					}
					visited[imp] = true
					next = append(next, imp)

					if isSelectableTest(r, imp) {
						addCandidate(candidates, imp, score, signal, changed, "")
					} else {
						impTests, err := r.Tests(imp, -1)
						noteErr(err, "Tests", imp)
						for _, t := range impTests {
							addTestCandidate(r, candidates, t.Path, score, signal, changed, t.Kind)
						}
					}
				}
			}
			frontier = next
		}

		// Step 4: Co-change tests.
		cochanged := r.CoChangedWith(changed, opts.CoChangeMinCount)
		for _, pair := range cochanged {
			score := cochangeScore(pair.Count)
			file := normalizePath(pair.File)
			if file == "" {
				continue
			}
			if isSelectableTest(r, file) {
				addCandidate(candidates, file, score, "co-change", changed, "")
			} else {
				pairTests, err := r.Tests(file, -1)
				noteErr(err, "Tests", file)
				for _, t := range pairTests {
					addTestCandidate(r, candidates, t.Path, score, "co-change", changed, t.Kind)
				}
			}
		}

		// Step 5: Hotspot boost.
		if ctx != nil && ctx.HotspotScore > 0.3 {
			for _, c := range candidates {
				if containsFile(c.ForFiles, changed) && c.Score < 1.0 {
					c.Score = min(1.0, c.Score+0.1)
					addSignal(c, "hotspot-risk")
				}
			}
		}
	}

	// Step 6: Build result, filter, sort.
	kindFilter := newKindFilter(opts.Kinds)
	signalFilter := newSignalFilter(opts.Signals)
	tests := []ScoredTest{}
	// filtered counts what the CALLER's own filters removed, so a selection
	// emptied by --kind/--exclude/--signals can be told apart from one witness
	// could not make. The first is the flag doing its job; the second is a hole
	// in the gate.
	filtered := 0
	for _, c := range candidates {
		if c.Score < opts.MinScore {
			continue
		}
		// One source of truth for the kind, so --kind cannot depend on which
		// signal happened to discover the test first.
		c.Kind = resolveKind(c.Path, c.Kind)
		if excluded(c.Path, opts.Exclude) {
			filtered++
			continue
		}
		if kindFilter != nil && !kindFilter[c.Kind] {
			filtered++
			continue
		}
		if signalFilter != nil && !matchesSignals(c.Signals, signalFilter) {
			filtered++
			continue
		}
		tests = append(tests, *c)
	}

	sort.Slice(tests, func(i, j int) bool {
		if tests[i].Score != tests[j].Score {
			return tests[i].Score > tests[j].Score
		}
		return tests[i].Path < tests[j].Path
	})

	truncated := 0
	if len(tests) > opts.MaxTests {
		truncated = len(tests) - opts.MaxTests
		tests = tests[:opts.MaxTests]
	}

	// Counted over the returned list, not the candidate set: a summary that
	// credits a signal for tests the cap dropped describes a run that never
	// happened.
	signalCounts := make(map[string]int)
	covered := make(map[string]bool)
	for _, t := range tests {
		for _, s := range t.Signals {
			signalCounts[s]++
		}
		for _, f := range t.ForFiles {
			covered[f] = true
		}
	}

	// Unmapped is the fail-closed hook: a changed file that no returned test
	// covers is a hole in the gate, whether recon had no index, the language is
	// unsupported, or an exclude/cap removed its only test.
	//
	// Prose and pictures are left out. They cannot change what a test does, and
	// counting them made every stray README edit, editor backup and untracked
	// debug.log widen the run to the entire suite — which is not fail-closed,
	// just useless. A build manifest is NOT in that group: a go.mod bump is
	// precisely the uncovered change the fallback exists for.
	var unmapped []string
	for _, f := range normalized {
		if !covered[f] && canAffectTests(f) {
			unmapped = append(unmapped, f)
		}
	}

	summary := Summary{
		Changed:       len(normalized),
		TestsSelected: len(tests),
		BySignal:      signalCounts,
		Unmapped:      unmapped,
		NotIndexed:    notIndexed,
		Truncated:     truncated,
		Filtered:      filtered,
	}
	if firstErr != nil {
		summary.AnalysisError = firstErr.Error()
	}

	return &SelectResult{
		ChangedFiles: normalized,
		Tests:        tests,
		Summary:      summary,
	}, nil
}

func addTestCandidate(r RepoIntel, m map[string]*ScoredTest, path string, score float64, signal, forFile, kind string) {
	path = normalizePath(path)
	if path == "" || !isSelectableTest(r, path) {
		return
	}
	addCandidate(m, path, score, signal, forFile, kind)
}

func addCandidate(m map[string]*ScoredTest, path string, score float64, signal, forFile, kind string) {
	path = normalizePath(path)
	if path == "" {
		return
	}
	if c, ok := m[path]; ok {
		// Use max score.
		if score > c.Score {
			c.Score = score
		}
		addSignal(c, signal)
		if forFile != "" && !containsFile(c.ForFiles, forFile) {
			c.ForFiles = append(c.ForFiles, forFile)
		}
		if kind != "" && c.Kind == "" {
			c.Kind = kind
		}
	} else {
		var forFiles []string
		if forFile != "" {
			forFiles = []string{forFile}
		}
		m[path] = &ScoredTest{
			Path:     path,
			Score:    score,
			Signals:  []string{signal},
			Kind:     kind,
			ForFiles: forFiles,
		}
	}
}

// normalizePath puts a path in the repo-relative, slash-separated form the
// candidate map is keyed by. Without it "./src/a_test.go" and "src/a_test.go"
// are two candidates for one file and the runner is asked to run it twice.
// Absolute paths are left alone here — only recon knows the repo root, so
// Select resolves those through recon.Context before this is applied.
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(p))
}

func isSelectableTest(r RepoIntel, path string) bool {
	if isProjectMetadataPath(path) {
		return false
	}
	// Checked before recon, because recon classifies everything under a test
	// directory as a test — fixtures and helpers included — and those are not
	// paths a runner can be pointed at.
	if isTestSupportFile(path) {
		return false
	}
	if isConventionalTestPath(path) {
		return true
	}
	return r.IsTestFile(path)
}

// canAffectTests reports whether a changed file could plausibly change what a
// test does. Only documentation and binary assets are ruled out: everything
// else — source, configuration, build manifests, lockfiles, scripts, files with
// no extension at all — is assumed to matter, because the cost of being wrong
// here is a test that should have run and did not.
func canAffectTests(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".rst", ".adoc", ".org", ".txt", ".text",
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico", ".bmp",
		".pdf", ".mp3", ".mp4", ".mov", ".woff", ".woff2", ".ttf", ".eot",
		".log", ".bak", ".orig", ".rej", ".swp", ".tmp":
		return false
	}
	// Editor backups: "handler.go~".
	return !strings.HasSuffix(path, "~")
}

func isProjectMetadataPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csproj", ".fsproj", ".vbproj", ".sln", ".props", ".targets":
		return true
	default:
		return false
	}
}

// isConventionalTestPath reports whether a path looks like a test without
// asking recon. It is witness's fallback for a file recon has not indexed, so
// it must not guess: a name suffix only counts when it is written the way the
// language's convention writes it. "Audit.java", "Deposit.java",
// "Manifest.java", "Circuit.kt" and "Latest.php" all end in "it" or "test" once
// lowercased, and none of them is a test.
func isConventionalTestPath(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	ext := strings.ToLower(filepath.Ext(p))
	lname := strings.TrimSuffix(filepath.Base(p), ext)
	// Preserved-case base name: for the JVM/PHP/Scala family the capital in
	// "FooTest"/"FooIT" is the convention, not decoration.
	name := strings.TrimSuffix(filepath.Base(filepath.ToSlash(path)), filepath.Ext(path))

	switch ext {
	case ".go", ".exs", ".rs", ".dart":
		return strings.HasSuffix(lname, "_test")
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".mts":
		return strings.HasSuffix(lname, ".test") || strings.HasSuffix(lname, ".spec") || hasTestPathContext(p)
	case ".py":
		return strings.HasPrefix(lname, "test_") || strings.HasSuffix(lname, "_test") || hasTestPathContext(p)
	case ".rb":
		return strings.HasSuffix(lname, "_spec") || strings.HasSuffix(lname, "_test") || hasTestPathContext(p)
	case ".cs":
		// C# has no naming convention the compiler or runner enforces, and
		// "Test" is an ordinary domain noun (LabTest.cs, StressTest.cs). recon
		// v0.13.1 requires test-project evidence before calling a .cs file a
		// test; witness's fallback requires the same, and then defers to recon.
		return hasTestPathContext(p)
	case ".java":
		return hasAnySuffix(name, "Test", "Tests", "IT") || hasTestPathContext(p)
	case ".kt", ".kts", ".swift":
		return hasAnySuffix(name, "Test", "Tests") || hasTestPathContext(p)
	case ".php":
		return hasAnySuffix(name, "Test", "Tests") || hasTestPathContext(p)
	case ".scala":
		return hasAnySuffix(name, "Spec", "Test", "Tests", "Suite") || hasTestPathContext(p)
	default:
		return false
	}
}

// isTestSupportFile reports whether a path is a shared fixture or helper that
// lives in a test tree but is not itself executable: tests/conftest.py,
// spec/spec_helper.rb, __tests__/setup.ts, Fixtures/DbFixture.cs. Changing one
// must pull in the tests that depend on it, but handing it to the runner
// collects zero tests and fails the build for the wrong reason.
//
// Demoting is the dangerous direction — a test witness drops is a test nobody
// runs — so it only happens on positive evidence:
//
//   - a support name or a support directory (conftest, spec_helper, setup,
//     Fixtures/, __mocks__/), for any language; or
//   - a name that breaks a convention the RUNNER itself collects by. pytest,
//     go test and rspec only pick up test_*.py, *_test.go and *_spec.rb, so
//     anything else in their tree really is a helper.
//
// Where the runner collects by directory or by attribute instead — every file
// under jest's __tests__/, every [Fact]-carrying class in a .NET test project —
// an unconventional name proves nothing, and the file is left as a test.
func isTestSupportFile(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	if !hasTestPathContext(p) {
		return false
	}
	named, known := hasTestFileName(path)
	if known && named {
		return false
	}
	if hasSupportName(p) || hasSupportDir(p) {
		return true
	}
	return known && collectsByFileName(p)
}

// collectsByFileName reports whether the language's runner only collects files
// whose NAME follows its convention, which is what makes an unconventional name
// in a test tree evidence of a helper. jest is the exception witness has to
// carry: its default testMatch takes every file under __tests__/, so a name
// says nothing there.
func collectsByFileName(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".go", ".exs", ".rs", ".dart", ".zig", ".lua", ".jl", ".py", ".rb":
		return true
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".mts", ".cjs":
		return !hasSegment(p, "__tests__")
	default:
		// .cs, .java, .kt, .swift, .php, .scala: the test runner finds classes
		// by attribute or annotation, so *Facts.cs and When_it_ships.cs are
		// tests whatever their name says.
		return false
	}
}

// hasSupportName reports whether the base name is one a test tree uses for
// shared setup rather than for tests.
func hasSupportName(p string) bool {
	name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	switch name {
	case "conftest", "setup", "setuptests", "setup-tests", "setup_tests", "jest.setup",
		"test-setup", "test_setup", "testsetup", "spec_helper", "rails_helper", "test_helper",
		"testhelper", "support", "base", "testbase", "constants", "globalusings", "assemblyinfo",
		"util", "utils", "test-utils", "test_utils", "testutils", "index":
		return true
	}
	return hasAnySuffix(name,
		"fixture", "fixtures", "helper", "helpers", "factory", "factories",
		"builder", "builders", "mock", "mocks", "utilities")
}

// hasSupportDir reports whether the path sits in a directory a test tree
// reserves for shared setup.
func hasSupportDir(p string) bool {
	dirs := strings.Split(filepath.ToSlash(filepath.Dir(p)), "/")
	for _, seg := range dirs {
		switch seg {
		case "fixture", "fixtures", "helper", "helpers", "support", "mock", "mocks",
			"__mocks__", "factory", "factories", "builders", "testdata", "testutils":
			return true
		}
	}
	return false
}

// hasTestFileName reports whether the base name follows a test naming
// convention for its language, ignoring the directory it sits in, plus whether
// the extension has a convention at all. It is only consulted for files already
// known to be tests, so it errs toward "this is a test" — the cost of demoting
// a real test is a silently unrun test, and the cost of promoting a helper is a
// loud runner error.
func hasTestFileName(path string) (named, known bool) {
	p := strings.ToLower(filepath.ToSlash(path))
	ext := strings.ToLower(filepath.Ext(p))
	name := strings.TrimSuffix(filepath.Base(p), ext)

	switch ext {
	case ".go", ".exs", ".rs", ".dart", ".zig", ".lua", ".jl":
		return strings.HasPrefix(name, "test_") || hasAnySuffix(name, "_test", "_tests", "_spec"), true
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".mts", ".cjs":
		return hasAnySuffix(name, ".test", ".spec", "_test", "_spec", ".cy", ".e2e", "test", "spec"), true
	case ".py":
		return strings.HasPrefix(name, "test_") || hasAnySuffix(name, "_test", "_tests"), true
	case ".rb":
		return hasAnySuffix(name, "_spec", "_test"), true
	case ".cs", ".java", ".kt", ".kts", ".swift", ".php", ".scala":
		return hasAnySuffix(name, "test", "tests", "spec", "specs", "suite", "it"), true
	default:
		return false, false
	}
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

// hasTestPathContext reports whether one of the DIRECTORIES a file sits in
// declares a test tree. p must be a lowercased, slash-separated file path; the
// last segment is the file name and is never read, so a source file called
// "test.go" cannot make its own directory a test tree.
func hasTestPathContext(p string) bool {
	dirs := strings.Split(filepath.ToSlash(filepath.Dir(p)), "/")
	for _, seg := range dirs {
		switch seg {
		case "__tests__", "test", "tests", "spec", "specs":
			return true
		}
		// .NET project directories are dotted names, and the test marker is
		// not always the LAST component: MyApp.Tests.Unit and
		// MyApp.Tests.Integration are how a solution splits one test project
		// in two. Reading only the suffix missed every one of them.
		for _, part := range strings.Split(seg, ".") {
			switch part {
			case "tests", "test", "specs", "spec", "e2e":
				return true
			}
			// Dotless .NET spellings: UnitTests/, IntegrationTests/, FooTests/.
			// Only the plural compound suffix is accepted — "Latest",
			// "Manifest" and "Contest" all end in "test".
			if hasAnySuffix(part, "unittests", "integrationtests", "acceptancetests", "e2etests", "functionaltests") {
				return true
			}
		}
	}
	return false
}

func addSignal(c *ScoredTest, signal string) {
	for _, s := range c.Signals {
		if s == signal {
			return
		}
	}
	c.Signals = append(c.Signals, signal)
}

func containsFile(files []string, file string) bool {
	for _, f := range files {
		if f == file {
			return true
		}
	}
	return false
}

func depthScore(depth int) float64 {
	switch depth {
	case 1:
		return 0.8
	case 2:
		return 0.5
	default:
		return 0.3
	}
}

func cochangeScore(count int) float64 {
	switch {
	case count >= 10:
		return 0.6
	case count >= 5:
		return 0.5
	default:
		return 0.3
	}
}

// resolveKind settles a test's kind from a single source of truth. The path
// heuristic wins wherever it finds positive evidence, recon's kind is the
// fallback, and "unit" is the floor. Deriving the kind from whichever signal
// discovered the test made one file "unit" for one diff and "smoke" for the
// next, so --kind quietly skipped it on half the changes.
func resolveKind(path, reconKind string) string {
	if k := kindFromPath(path); k != "" {
		return k
	}
	if k := strings.ToLower(strings.TrimSpace(reconKind)); k != "" {
		return k
	}
	return "unit"
}

// kindFromPath returns the kind a path positively indicates, or "" when it
// indicates nothing — the empty case is what lets recon's kind be a fallback
// rather than being overwritten by a default.
//
// The match is on whole WORDS of a path segment (split on ".", "-" and "_"),
// never on a substring of the path. A loose substring match relabelled every
// test in src/features/ — the mainstream React/Vue/Redux layout — as
// "acceptance", which then made them all vanish from `--kind unit`: a silent
// skip in the lane CI actually runs.
func kindFromPath(path string) string {
	p := strings.ToLower(filepath.ToSlash(path))
	ext := strings.ToLower(filepath.Ext(p))
	words := pathWords(p)

	switch {
	case hasWord(words, "e2e", "endtoend") || strings.Contains(p, "end-to-end") || strings.Contains(p, "end_to_end"):
		return "e2e"
	case hasWord(words, "integration", "integrationtest", "integrationtests") || hasSegment(p, "it"):
		return "integration"
	case hasWord(words, "acceptance", "acceptancetest", "acceptancetests"):
		return "acceptance"
	// "features/" is Cucumber's tree only when it holds .feature files;
	// everywhere else it is a source layout, not a test kind.
	case ext == ".feature" && hasWord(words, "feature", "features"):
		return "acceptance"
	case hasWord(words, "smoke", "smoketest", "smoketests"):
		return "smoke"
	default:
		return ""
	}
}

// pathWords splits a path into the words its segments are built from, so
// "tests/MyApp.IntegrationTests/login.e2e.spec.ts" yields "integrationtests"
// and "e2e" without also matching "e2eish" or "integrations".
func pathWords(p string) []string {
	fields := strings.FieldsFunc(p, func(r rune) bool {
		return r == '/' || r == '.' || r == '-' || r == '_'
	})
	return fields
}

func hasWord(words []string, want ...string) bool {
	for _, w := range words {
		for _, candidate := range want {
			if w == candidate {
				return true
			}
		}
	}
	return false
}

// hasSegment reports whether name is a full slash-delimited path segment of p
// (so "it" matches "test/it/foo" but not "unit/foo").
func hasSegment(p, name string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == name {
			return true
		}
	}
	return false
}

// newKindFilter returns a set of allowed kinds, or nil when no filter is set.
func newKindFilter(kinds []string) map[string]bool {
	if len(kinds) == 0 {
		return nil
	}
	set := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" {
			set[k] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// newSignalFilter returns the set of signal prefixes a test must carry one of,
// or nil when no filter is set. Prefixes rather than exact names so that
// "import" covers import-1hop, import-2hop and everything deeper — the depth is
// already expressed by --depth, and nobody should have to enumerate hops.
func newSignalFilter(signals []string) []string {
	var set []string
	for _, s := range signals {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			set = append(set, s)
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// matchesSignals reports whether a test carries at least one of the wanted
// signals. A test keeps every signal that found it, so this is a union: a test
// found by both an import walk and a co-change survives --signals import.
func matchesSignals(signals []string, want []string) bool {
	for _, s := range signals {
		for _, w := range want {
			if strings.HasPrefix(s, w) {
				return true
			}
		}
	}
	return false
}

// excluded reports whether path matches any exclude glob. A pattern containing
// a "/" is anchored against the whole repo-relative path; a pattern without one
// ("*.gen_test.go") is a base-name glob and is matched against the file name at
// any depth. The base-name retry is deliberately not applied to anchored
// patterns: "vendor/**" must not drop internal/billing/vendor_client_test.go.
func excluded(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	p := normalizePath(path)
	base := filepath.Base(p)
	for _, pat := range patterns {
		pat = filepath.ToSlash(strings.TrimSpace(pat))
		if pat == "" {
			continue
		}
		if matchGlob(pat, p) {
			return true
		}
		if !strings.Contains(pat, "/") && matchGlob(pat, base) {
			return true
		}
	}
	return false
}

// matchGlob matches a slash-separated path against a doublestar glob. "*" and
// "?" match within a single path segment (filepath.Match semantics); "**"
// matches any run of segments, including none. Both ends are anchored, so
// "vendor/**" matches "vendor/lib/x_test.go" but not "vendorlib/x_test.go" nor
// "internal/vendor_client_test.go", and mixed forms like "**/*_test.go" and
// "vendor/**/*_test.go" work — the previous literal-fragment matcher matched
// nothing at all for those.
func matchGlob(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(seg); i++ {
				if matchSegments(pat[1:], seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 {
			return false
		}
		if ok, err := filepath.Match(pat[0], seg[0]); err != nil || !ok {
			return false
		}
		pat, seg = pat[1:], seg[1:]
	}
	return len(seg) == 0
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
