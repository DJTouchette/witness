package selector

import (
	"fmt"
	"sort"

	"github.com/djtouchette/recon/pkg/recon"
)

const fanOutCap = 100

// Select finds tests relevant to the given changed files using recon's repo intelligence.
// It combines direct test mapping, transitive import walks, co-change history,
// and hotspot risk scoring to produce a prioritized list.
func Select(r *recon.Recon, changedFiles []string, opts SelectOptions) (*SelectResult, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 2
	}
	if opts.MinScore <= 0 {
		opts.MinScore = 0.1
	}
	if opts.MaxTests <= 0 {
		opts.MaxTests = 50
	}

	candidates := make(map[string]*ScoredTest)

	for _, changed := range changedFiles {
		// Step 1: If the changed file IS a test, include it directly.
		if r.IsTestFile(changed) {
			addCandidate(candidates, changed, 1.0, "changed-test", changed, "")
			continue
		}

		// Step 2: Direct test matches via TestMap.
		tests, _ := r.Tests(changed, -1)
		for _, t := range tests {
			addCandidate(candidates, t.Path, 1.0, "direct-test", changed, t.Kind)
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
				if len(importers) > fanOutCap {
					// Skip high-fan-out files (utilities) to avoid explosion.
					continue
				}
				for _, imp := range importers {
					if visited[imp] {
						continue
					}
					visited[imp] = true
					next = append(next, imp)

					if r.IsTestFile(imp) {
						addCandidate(candidates, imp, score, signal, changed, "")
					} else {
						impTests, _ := r.Tests(imp, -1)
						for _, t := range impTests {
							addCandidate(candidates, t.Path, score, signal, changed, t.Kind)
						}
					}
				}
			}
			frontier = next
		}

		// Step 4: Co-change tests.
		cochanged := r.CoChangedWith(changed, 2)
		for _, pair := range cochanged {
			score := cochangeScore(pair.Count)
			if r.IsTestFile(pair.File) {
				addCandidate(candidates, pair.File, score, "co-change", changed, "")
			} else {
				pairTests, _ := r.Tests(pair.File, -1)
				for _, t := range pairTests {
					addCandidate(candidates, t.Path, score, "co-change", changed, t.Kind)
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
	var tests []ScoredTest
	signalCounts := make(map[string]int)
	for _, c := range candidates {
		if c.Score < opts.MinScore {
			continue
		}
		if c.Kind == "" {
			c.Kind = classifyKind(c.Path)
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

func classifyKind(path string) string {
	// Delegate to recon's classification.
	// Simple fallback when kind wasn't set from TestFile.
	return "unit"
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
