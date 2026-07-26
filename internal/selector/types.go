package selector

// ScoredTest is a test file with a relevance score and the signals that contributed.
type ScoredTest struct {
	Path     string   `json:"path"`
	Score    float64  `json:"score"`
	Signals  []string `json:"signals"`
	Kind     string   `json:"kind"`
	ForFiles []string `json:"for_files"`
}

// SelectResult is the output of the Select function.
type SelectResult struct {
	ChangedFiles []string     `json:"changed_files"`
	Tests        []ScoredTest `json:"tests"`
	Summary      Summary      `json:"summary"`
}

// Summary provides counts for the selection, plus everything the selection
// could not account for.
//
// The coverage fields exist because an empty test list is two different
// answers: "this change needs no tests" and "recon could not tell me which
// tests it needs". Only the first is safe for a CI gate to exit 0 on, so
// callers gate on Unmapped/NotIndexed/AnalysisError rather than on len(Tests).
type Summary struct {
	Changed       int            `json:"changed"`
	TestsSelected int            `json:"tests_selected"`
	BySignal      map[string]int `json:"by_signal"` // counted over the returned tests, after the MaxTests cap

	// Unmapped lists the (normalized) changed files that no returned test
	// covers — because recon had no mapping, because the language is
	// unsupported, or because an exclude glob or the MaxTests cap removed the
	// only test that did. Each entry is a hole in the gate.
	Unmapped []string `json:"unmapped,omitempty"`

	// NotIndexed lists the changed files recon has never scanned: a stale
	// index, a path outside the repo, or a file the ignore rules skipped.
	// Every signal for these files is missing rather than zero.
	NotIndexed []string `json:"not_indexed,omitempty"`

	// Truncated counts tests that passed every filter but were dropped by
	// MaxTests. Non-zero means the returned list is not the whole answer.
	Truncated int `json:"truncated,omitempty"`

	// Filtered counts tests witness selected and then dropped because the
	// caller's own Kinds, Exclude or Signals filter said so. It is what tells
	// "witness found nothing" apart from "witness found tests and you asked it
	// not to run them" — only the first is a gap in the gate.
	Filtered int `json:"filtered,omitempty"`

	// AnalysisError is the first error recon returned during the walk,
	// formatted. Select still returns a result — a partial answer beats no
	// answer — but a caller that wants to fail closed has the reason here.
	AnalysisError string `json:"analysis_error,omitempty"`
}

// SelectOptions configures the selection algorithm.
type SelectOptions struct {
	MaxDepth         int      // import graph traversal depth (default: 2)
	MinScore         float64  // minimum score to include (default: 0.1)
	MaxTests         int      // max tests to return (default: 50)
	CoChangeMinCount int      // minimum co-change count to consider (default: 2)
	FanOutCap        int      // skip files with more importers than this (default: 100)
	Exclude          []string // glob patterns; matching test paths are dropped
	Kinds            []string // if non-empty, only tests of these kinds are kept
	Signals          []string // if non-empty, only tests carrying one of these signals are kept
}

// Defaults for the tunable thresholds, also used to backfill zero-value options.
const (
	defaultMaxDepth         = 2
	defaultMinScore         = 0.1
	defaultMaxTests         = 50
	defaultCoChangeMinCount = 2
	defaultFanOutCap        = 100
)

// DefaultOptions returns sensible defaults.
func DefaultOptions() SelectOptions {
	return SelectOptions{
		MaxDepth:         defaultMaxDepth,
		MinScore:         defaultMinScore,
		MaxTests:         defaultMaxTests,
		CoChangeMinCount: defaultCoChangeMinCount,
		FanOutCap:        defaultFanOutCap,
	}
}
