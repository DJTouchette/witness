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

	for _, changed := range changedFiles {
		// Step 1: If the changed file IS a test, include it directly.
		if isSelectableTest(r, changed) {
			addCandidate(candidates, changed, 1.0, "changed-test", changed, "")
			continue
		}

		// Step 2: Direct test matches via TestMap.
		tests, _ := r.Tests(changed, -1)
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
					if visited[imp] {
						continue
					}
					visited[imp] = true
					next = append(next, imp)

					if isSelectableTest(r, imp) {
						addCandidate(candidates, imp, score, signal, changed, "")
					} else {
						impTests, _ := r.Tests(imp, -1)
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
			if isSelectableTest(r, pair.File) {
				addCandidate(candidates, pair.File, score, "co-change", changed, "")
			} else {
				pairTests, _ := r.Tests(pair.File, -1)
				for _, t := range pairTests {
					addTestCandidate(r, candidates, t.Path, score, "co-change", changed, t.Kind)
				}
			}
		}

		// Step 5: Hotspot boost.
		ctx, _ := r.Context(changed)
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
	var tests []ScoredTest
	signalCounts := make(map[string]int)
	for _, c := range candidates {
		if c.Score < opts.MinScore {
			continue
		}
		if c.Kind == "" {
			c.Kind = classifyKind(c.Path)
		}
		if excluded(c.Path, opts.Exclude) {
			continue
		}
		if kindFilter != nil && !kindFilter[c.Kind] {
			continue
		}
		tests = append(tests, *c)
		for _, s := range c.Signals {
			signalCounts[s]++
		}
	}

	sort.Slice(tests, func(i, j int) bool {
		if tests[i].Score != tests[j].Score {
			return tests[i].Score > tests[j].Score
		}
		return tests[i].Path < tests[j].Path
	})

	if len(tests) > opts.MaxTests {
		tests = tests[:opts.MaxTests]
	}

	return &SelectResult{
		ChangedFiles: changedFiles,
		Tests:        tests,
		Summary: Summary{
			Changed:       len(changedFiles),
			TestsSelected: len(tests),
			BySignal:      signalCounts,
		},
	}, nil
}

func addTestCandidate(r RepoIntel, m map[string]*ScoredTest, path string, score float64, signal, forFile, kind string) {
	if !isSelectableTest(r, path) {
		return
	}
	addCandidate(m, path, score, signal, forFile, kind)
}

func addCandidate(m map[string]*ScoredTest, path string, score float64, signal, forFile, kind string) {
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

func isSelectableTest(r RepoIntel, path string) bool {
	if isProjectMetadataPath(path) {
		return false
	}
	if isConventionalTestPath(path) {
		return true
	}
	if isCSharpPath(path) {
		// C# domains commonly use singular nouns like LabTest.cs. Recon v0.8
		// marks any *Test.cs as a test, so require explicit test context for
		// singular names and let *Tests.cs pass through conventionally above.
		return false
	}
	return r.IsTestFile(path)
}

func isProjectMetadataPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csproj", ".fsproj", ".vbproj", ".sln", ".props", ".targets":
		return true
	default:
		return false
	}
}

func isConventionalTestPath(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	ext := strings.ToLower(filepath.Ext(p))
	name := strings.TrimSuffix(filepath.Base(p), ext)
	switch ext {
	case ".go", ".exs", ".rs", ".dart":
		return strings.HasSuffix(name, "_test")
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".mts":
		return strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".spec") || hasTestPathContext(p)
	case ".py":
		return strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test") || hasTestPathContext(p)
	case ".rb":
		return strings.HasSuffix(name, "_spec") || strings.HasSuffix(name, "_test") || hasTestPathContext(p)
	case ".cs":
		return strings.HasSuffix(name, "tests") || hasTestPathContext(p)
	case ".java":
		return strings.HasSuffix(name, "test") || strings.HasSuffix(name, "tests") || strings.HasSuffix(name, "it") || hasTestPathContext(p)
	case ".kt", ".kts", ".swift":
		return strings.HasSuffix(name, "test") || strings.HasSuffix(name, "tests") || hasTestPathContext(p)
	case ".php":
		return strings.HasSuffix(name, "test") || hasTestPathContext(p)
	case ".scala":
		return strings.HasSuffix(name, "spec") || strings.HasSuffix(name, "test") ||
			strings.HasSuffix(name, "tests") || strings.HasSuffix(name, "suite") || hasTestPathContext(p)
	default:
		return false
	}
}

func isCSharpPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".cs")
}

func hasTestPathContext(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "__tests__", "test", "tests", "spec", "specs":
			return true
		}
		if strings.HasSuffix(seg, ".tests") || strings.HasSuffix(seg, ".test") ||
			strings.HasSuffix(seg, ".integrationtests") || strings.HasSuffix(seg, ".unittests") ||
			strings.HasSuffix(seg, ".e2e") || strings.HasSuffix(seg, ".e2etests") {
			return true
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

// classifyKind infers a test's kind from its path when recon didn't supply one.
// It's a heuristic over common conventions; recon-provided kinds always win
// (this is only called when Kind is empty).
func classifyKind(path string) string {
	p := strings.ToLower(filepath.ToSlash(path))
	switch {
	case strings.Contains(p, "e2e") || strings.Contains(p, "end-to-end") || strings.Contains(p, "end_to_end"):
		return "e2e"
	case strings.Contains(p, "integration") || hasSegment(p, "it"):
		return "integration"
	case strings.Contains(p, "acceptance") || strings.Contains(p, "feature"):
		return "acceptance"
	case strings.Contains(p, "smoke"):
		return "smoke"
	default:
		return "unit"
	}
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

// excluded reports whether path matches any exclude glob. Patterns support
// filepath.Match wildcards plus a leading/trailing "**" for directory subtrees
// (e.g. "vendor/**", "**/generated/**"); they're matched against the full path
// and the base name.
func excluded(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	p := filepath.ToSlash(path)
	base := filepath.Base(p)
	for _, pat := range patterns {
		pat = filepath.ToSlash(pat)
		if matchGlob(pat, p) || matchGlob(pat, base) {
			return true
		}
	}
	return false
}

// matchGlob matches a path against a glob. Without "**" it uses filepath.Match
// (single "*", no "/" crossing). With "**" the pattern is split into literal
// segments that must appear in order: the first is anchored to the start unless
// the pattern begins with "**", the last to the end unless it ends with "**".
// This covers the common shapes "vendor/**", "**/generated/**", "**/x_test.go".
func matchGlob(pattern, path string) bool {
	if !strings.Contains(pattern, "**") {
		ok, _ := filepath.Match(pattern, path)
		return ok
	}

	segs := strings.Split(pattern, "**")
	anchorStart := !strings.HasPrefix(pattern, "**")
	anchorEnd := !strings.HasSuffix(pattern, "**")

	pos := 0
	for i, seg := range segs {
		seg = strings.Trim(seg, "/")
		if seg == "" {
			continue
		}
		if i == 0 && anchorStart {
			if !strings.HasPrefix(path, seg) {
				return false
			}
			pos = len(seg)
			continue
		}
		if i == len(segs)-1 && anchorEnd {
			return strings.HasSuffix(path, seg) && strings.Index(path[pos:], seg) >= 0
		}
		idx := strings.Index(path[pos:], seg)
		if idx < 0 {
			return false
		}
		pos += idx + len(seg)
	}
	return true
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
