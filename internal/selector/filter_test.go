package selector

import (
	"testing"

	"github.com/djtouchette/recon/pkg/recon"
)

func TestClassifyKind(t *testing.T) {
	cases := map[string]string{
		"internal/foo/bar_test.go":          "unit",
		"test/e2e/login_test.go":            "e2e",
		"tests/end-to-end/checkout.spec.ts": "e2e",
		"src/integration/db_test.go":        "integration",
		"test/it/api_test.exs":              "integration", // "it" as a path segment
		"spec/acceptance/signup_spec.rb":    "acceptance",
		"features/login.feature":            "acceptance",
		"test/smoke/health_test.go":         "smoke",
		"pkg/unittest/x_test.go":            "unit", // "unit..." must not match "it"
	}
	for path, want := range cases {
		if got := classifyKind(path); got != want {
			t.Errorf("classifyKind(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestExcluded(t *testing.T) {
	patterns := []string{"vendor/**", "**/generated/**", "*.gen_test.go"}
	cases := map[string]bool{
		"vendor/foo/bar_test.go":    true,
		"src/generated/api_test.go": true,
		"src/app/user_test.go":      false,
		"db.gen_test.go":            true, // base-name glob
		"src/db.gen_test.go":        true, // matched on base name
		"src/app/handler.go":        false,
	}
	for path, want := range cases {
		if got := excluded(path, patterns); got != want {
			t.Errorf("excluded(%q) = %v, want %v", path, got, want)
		}
	}
	if excluded("anything", nil) {
		t.Error("no patterns should never exclude")
	}
}

func TestSelect_KindFilter(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{
		{Path: "unit/a_test.go", Kind: "unit"},
		{Path: "e2e/a_test.go", Kind: "e2e"},
	}
	opts := DefaultOptions()
	opts.Kinds = []string{"e2e"}

	res, _ := Select(f, []string{"src/a.go"}, opts)
	got := byPath(res)
	if _, ok := got["e2e/a_test.go"]; !ok {
		t.Error("e2e test should pass the kind filter")
	}
	if _, ok := got["unit/a_test.go"]; ok {
		t.Error("unit test should be filtered out by --kind e2e")
	}
}

func TestSelect_KindFilterUsesClassifiedKind(t *testing.T) {
	f := newFake()
	// recon supplies no kind; classification from path should drive the filter.
	f.tests["src/a.go"] = []recon.TestFile{{Path: "test/e2e/flow_test.go"}}
	opts := DefaultOptions()
	opts.Kinds = []string{"unit"}

	res, _ := Select(f, []string{"src/a.go"}, opts)
	if len(res.Tests) != 0 {
		t.Errorf("path-classified e2e test should be excluded by --kind unit; got %+v", res.Tests)
	}
}

func TestSelect_ExcludeDropsTests(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{
		{Path: "vendor/lib/x_test.go"},
		{Path: "src/a_test.go"},
	}
	opts := DefaultOptions()
	opts.Exclude = []string{"vendor/**"}

	res, _ := Select(f, []string{"src/a.go"}, opts)
	got := byPath(res)
	if _, ok := got["vendor/lib/x_test.go"]; ok {
		t.Error("vendored test should be excluded")
	}
	if _, ok := got["src/a_test.go"]; !ok {
		t.Error("non-vendored test should remain")
	}
}

func TestSelect_CoChangeMinCountConfigurable(t *testing.T) {
	// A custom CoChangeMinCount is passed through to recon's CoChangedWith.
	var gotMin int
	f := newFake()
	f.cochangeHook = func(min int) { gotMin = min }
	f.cochange["src/a.go"] = []recon.CoChangePair{{File: "co_test.go", Count: 5}}
	f.testFiles["co_test.go"] = true

	opts := DefaultOptions()
	opts.CoChangeMinCount = 7
	Select(f, []string{"src/a.go"}, opts)
	if gotMin != 7 {
		t.Errorf("CoChangedWith min = %d, want 7", gotMin)
	}
}

func TestSelect_FanOutCapConfigurable(t *testing.T) {
	f := newFake()
	// 3 importers; with FanOutCap=2 the file is skipped, so no test surfaces.
	f.importedBy["src/util.go"] = []string{"a.go", "b.go", "c.go"}
	f.tests["a.go"] = []recon.TestFile{{Path: "a_test.go"}}
	opts := DefaultOptions()
	opts.FanOutCap = 2

	res, _ := Select(f, []string{"src/util.go"}, opts)
	if len(res.Tests) != 0 {
		t.Errorf("FanOutCap=2 should skip a 3-importer file; got %+v", res.Tests)
	}
}
