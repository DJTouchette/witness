package selector

import (
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
}

func newFake() *fakeIntel {
	return &fakeIntel{
		testFiles:  map[string]bool{},
		tests:      map[string][]recon.TestFile{},
		importedBy: map[string][]string{},
		cochange:   map[string][]recon.CoChangePair{},
		hotspot:    map[string]float64{},
	}
}

func (f *fakeIntel) IsTestFile(p string) bool { return f.testFiles[p] }
func (f *fakeIntel) Tests(p string, _ int) ([]recon.TestFile, error) {
	return f.tests[p], nil
}
func (f *fakeIntel) ImportedBy(p string) []string { return f.importedBy[p] }
func (f *fakeIntel) CoChangedWith(p string, _ int) []recon.CoChangePair {
	return f.cochange[p]
}
func (f *fakeIntel) Context(p string) (*recon.FileContext, error) {
	if s, ok := f.hotspot[p]; ok {
		return &recon.FileContext{Path: p, HotspotScore: s}, nil
	}
	return &recon.FileContext{Path: p}, nil
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
	many := make([]string, fanOutCap+5)
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
	// Exactly fanOutCap importers is allowed (cap is "> fanOutCap").
	many := make([]string, fanOutCap)
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
	if len(res.Tests) != 3 {
		t.Errorf("MaxTests not enforced: got %d, want 3", len(res.Tests))
	}
	if res.Summary.TestsSelected != 3 {
		t.Errorf("summary count = %d, want 3", res.Summary.TestsSelected)
	}
}

func TestSelect_SortByScoreThenPath(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{{Path: "zzz_test.go"}} // 1.0
	f.importedBy["src/a.go"] = []string{"src/b.go"}
	f.tests["src/b.go"] = []recon.TestFile{{Path: "aaa_test.go"}} // 0.8

	res, _ := Select(f, []string{"src/a.go"}, DefaultOptions())
	if len(res.Tests) < 2 {
		t.Fatalf("expected 2 tests, got %d", len(res.Tests))
	}
	// Higher score first regardless of path ordering.
	if res.Tests[0].Path != "zzz_test.go" {
		t.Errorf("highest score should sort first, got %q", res.Tests[0].Path)
	}
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
