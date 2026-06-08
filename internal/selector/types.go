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

// Summary provides counts for the selection.
type Summary struct {
	Changed       int            `json:"changed"`
	TestsSelected int            `json:"tests_selected"`
	BySignal      map[string]int `json:"by_signal"`
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
