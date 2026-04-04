package selector

import (
	"testing"
)

func TestAddCandidate(t *testing.T) {
	m := make(map[string]*ScoredTest)

	addCandidate(m, "test/a_test.exs", 1.0, "direct-test", "lib/a.ex", "unit")
	if len(m) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(m))
	}
	if m["test/a_test.exs"].Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", m["test/a_test.exs"].Score)
	}

	// Adding again with lower score should keep max.
	addCandidate(m, "test/a_test.exs", 0.5, "import-1hop", "lib/b.ex", "")
	if m["test/a_test.exs"].Score != 1.0 {
		t.Errorf("expected score to stay 1.0, got %f", m["test/a_test.exs"].Score)
	}
	if len(m["test/a_test.exs"].Signals) != 2 {
		t.Errorf("expected 2 signals, got %d", len(m["test/a_test.exs"].Signals))
	}
	if len(m["test/a_test.exs"].ForFiles) != 2 {
		t.Errorf("expected 2 source files, got %d", len(m["test/a_test.exs"].ForFiles))
	}

	// Adding again with higher score should update.
	addCandidate(m, "test/b_test.exs", 0.3, "co-change", "lib/a.ex", "unit")
	addCandidate(m, "test/b_test.exs", 0.8, "import-1hop", "lib/a.ex", "")
	if m["test/b_test.exs"].Score != 0.8 {
		t.Errorf("expected score 0.8, got %f", m["test/b_test.exs"].Score)
	}
}

func TestAddSignalDedup(t *testing.T) {
	c := &ScoredTest{Signals: []string{"direct-test"}}
	addSignal(c, "direct-test")
	if len(c.Signals) != 1 {
		t.Errorf("expected 1 signal after dedup, got %d", len(c.Signals))
	}
	addSignal(c, "import-1hop")
	if len(c.Signals) != 2 {
		t.Errorf("expected 2 signals, got %d", len(c.Signals))
	}
}

func TestDepthScore(t *testing.T) {
	tests := []struct {
		depth int
		want  float64
	}{
		{1, 0.8},
		{2, 0.5},
		{3, 0.3},
		{10, 0.3},
	}
	for _, tt := range tests {
		got := depthScore(tt.depth)
		if got != tt.want {
			t.Errorf("depthScore(%d) = %f, want %f", tt.depth, got, tt.want)
		}
	}
}

func TestCochangeScore(t *testing.T) {
	tests := []struct {
		count int
		want  float64
	}{
		{2, 0.3},
		{4, 0.3},
		{5, 0.5},
		{9, 0.5},
		{10, 0.6},
		{100, 0.6},
	}
	for _, tt := range tests {
		got := cochangeScore(tt.count)
		if got != tt.want {
			t.Errorf("cochangeScore(%d) = %f, want %f", tt.count, got, tt.want)
		}
	}
}

func TestContainsFile(t *testing.T) {
	files := []string{"a.ex", "b.ex"}
	if !containsFile(files, "a.ex") {
		t.Error("expected true for a.ex")
	}
	if containsFile(files, "c.ex") {
		t.Error("expected false for c.ex")
	}
}

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	if opts.MaxDepth != 2 {
		t.Errorf("expected MaxDepth=2, got %d", opts.MaxDepth)
	}
	if opts.MinScore != 0.1 {
		t.Errorf("expected MinScore=0.1, got %f", opts.MinScore)
	}
	if opts.MaxTests != 50 {
		t.Errorf("expected MaxTests=50, got %d", opts.MaxTests)
	}
}
