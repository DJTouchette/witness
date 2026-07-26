package selector

import (
	"testing"

	"github.com/djtouchette/recon/pkg/recon"
)

func TestResolveKind(t *testing.T) {
	cases := []struct {
		path      string
		reconKind string
		want      string
	}{
		{"internal/foo/bar_test.go", "", "unit"},
		{"test/e2e/login_test.go", "", "e2e"},
		{"tests/end-to-end/checkout.spec.ts", "", "e2e"},
		{"src/integration/db_test.go", "", "integration"},
		{"test/it/api_test.exs", "", "integration"}, // "it" as a path segment
		{"spec/acceptance/signup_spec.rb", "", "acceptance"},
		{"features/login.feature", "", "acceptance"},
		{"test/smoke/health_test.go", "", "smoke"},
		{"pkg/unittest/x_test.go", "", "unit"}, // "unit..." must not match "it"

		// The path wins over recon's kind wherever it says anything, so the
		// same file gets the same label no matter which signal found it.
		{"test/smoke/health_test.go", "unit", "smoke"},
		{"test/e2e/login_test.go", "integration", "e2e"},
		// recon is the fallback for paths that indicate nothing, normalized.
		{"pkg/foo/bar_test.go", "Integration", "integration"},
		{"pkg/foo/bar_test.go", " e2e ", "e2e"},

		// A src/features/ directory is the mainstream React/Vue/Redux layout,
		// not a Cucumber tree. Reading "acceptance" out of it made every test
		// in such a repo vanish from a `--kind unit` lane — a silent skip in
		// exactly the lane CI runs. Only a .feature file is acceptance.
		{"src/features/login.spec.js", "", "unit"},
		{"src/features/login.spec.js", "unit", "unit"},
		{"src/features/checkout/cart.test.ts", "integration", "integration"},
		// ... and the substring matches behind it: a segment has to say the
		// kind, not merely contain its letters.
		{"src/monolith/e2eish/x_test.go", "", "unit"},
		{"src/integrations/stripe/client_test.go", "", "unit"},
		{"src/smokestack/burner_test.go", "", "unit"},
		// Dotted .NET test-project spellings still classify.
		{"tests/MyApp.IntegrationTests/OrderTests.cs", "", "integration"},
		{"tests/MyApp.E2E/CheckoutTests.cs", "", "e2e"},
		// A kind spelled in the file name, not the directory.
		{"src/app/login.e2e.spec.ts", "", "e2e"},
	}
	for _, tc := range cases {
		if got := resolveKind(tc.path, tc.reconKind); got != tc.want {
			t.Errorf("resolveKind(%q, %q) = %q, want %q", tc.path, tc.reconKind, got, tc.want)
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

		// Fragments must land on path-segment boundaries, and an anchored
		// pattern must not be retried against the bare file name. Every entry
		// below used to be excluded, silently dropping a hand-written test.
		"internal/billing/vendor_client_test.go": false,
		"app/models/vendor_test.rb":              false,
		"vendored/lib/x_test.go":                 false,
		"vendors/x_test.go":                      false,
		"src/report/generated_pdf_test.go":       false,
		"src/foo/generatedstuff/x_test.go":       false,
	}
	for path, want := range cases {
		if got := excluded(path, patterns); got != want {
			t.Errorf("excluded(%q) = %v, want %v", path, got, want)
		}
	}
	if excluded("anything", nil) {
		t.Error("no patterns should never exclude")
	}
	// An empty entry (a stray comma in --exclude) must not swallow everything.
	if excluded("src/app/user_test.go", []string{"", "   "}) {
		t.Error("blank patterns should never exclude")
	}
}

// TestExcludedDoublestarWithWildcard covers the shapes that mix "**" with "*".
// The old literal-fragment matcher compared "*_test.go" as a literal string, so
// the most idiomatic gitignore spelling matched nothing at all and the exclude
// silently no-opped.
func TestExcludedDoublestarWithWildcard(t *testing.T) {
	cases := []struct {
		path    string
		pattern string
		want    bool
	}{
		{"src/app/user_test.go", "**/*_test.go", true},
		{"user_test.go", "**/*_test.go", true}, // "**" also matches no segments
		{"src/app/user.go", "**/*_test.go", false},
		{"src/app/user.spec.ts", "**/*.spec.ts", true},
		{"vendor/lib/x_test.go", "vendor/**/*_test.go", true},
		{"vendor/x_test.go", "vendor/**/*_test.go", true},
		{"vendor/lib/x.go", "vendor/**/*_test.go", false},
		{"other/lib/x_test.go", "vendor/**/*_test.go", false},
		{"vendor/lib/x_test.go", "vendor/**", true},
		{"vendorlib/x_test.go", "vendor/**", false},
		{"a/generated/b.go", "**/generated/**", true},
		{"a/b/c_test.go", "a/*/c_test.go", true},
		{"a/b/c/c_test.go", "a/*/c_test.go", false}, // "*" does not cross "/"
	}
	for _, tc := range cases {
		if got := excluded(tc.path, []string{tc.pattern}); got != tc.want {
			t.Errorf("excluded(%q, %q) = %v, want %v", tc.path, tc.pattern, got, tc.want)
		}
	}
}

// TestExcludedAnchoredPatternIsNeverRetriedAgainstTheBaseName pins the two
// independent pieces that keep `--exclude 'vendor/**'` from dropping a
// hand-written internal/billing/vendor_client_test.go: matchGlob anchors both
// ends, and excluded() only retries a pattern against the bare file name when
// the pattern has no "/" in it. The combination was covered; neither half was.
func TestExcludedAnchoredPatternIsNeverRetriedAgainstTheBaseName(t *testing.T) {
	if matchGlob("vendor/**", "vendor_client_test.go") {
		t.Error("matchGlob must anchor segments: vendor/** is a directory, not a name prefix")
	}
	if !matchGlob("*_test.go", "vendor_client_test.go") {
		t.Error("a base-name glob must still match the base name")
	}
	// The guard itself: a pattern that names a directory must not be consulted
	// against a file name, whatever matchGlob is loosened to later.
	if excluded("internal/billing/vendor_client_test.go", []string{"vendor/**"}) {
		t.Error("an anchored pattern was retried against the base name and dropped a real test")
	}
	if !excluded("src/db.gen_test.go", []string{"*.gen_test.go"}) {
		t.Error("a pattern without a / is a base-name glob at any depth")
	}
}

// TestIsConventionalTestPath covers every extension arm. The negatives are the
// point: this heuristic runs before recon is ever consulted, so a name that
// merely ends in "it" or "test" once lowercased used to make an ordinary source
// file its own test — editing Audit.java selected Audit.java and nothing else.
func TestIsConventionalTestPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Go / Elixir / Rust / Dart: suffix conventions only.
		{"internal/foo/bar_test.go", true},
		{"internal/foo/bar.go", false},
		{"test/a_test.exs", true},
		{"lib/a.ex", false},
		{"src/x_test.rs", true},
		{"src/latest.rs", false},
		{"lib/x_test.dart", true},
		{"lib/latest.dart", false},

		// JS/TS family.
		{"src/a.test.ts", true},
		{"src/a.spec.js", true},
		{"src/a.tsx", false},
		{"src/latest.ts", false},
		{"__tests__/setup.ts", true}, // test tree; runnability is a separate question

		// Python / Ruby.
		{"tests/test_api.py", true},
		{"src/api_test.py", true},
		{"src/api.py", false},
		{"spec/models/user_spec.rb", true},
		{"app/models/user.rb", false},

		// C#: the name is never enough — "Test" is an ordinary domain noun.
		{"src/Domains/Leroy.Certificates/Models/LabTest.cs", false},
		{"src/App/Models/LabTests.cs", false},
		{"src/App/Models/Latest.cs", false},
		{"src/FooTests.cs", false},
		{"backend/tests/Leroy.Platform.Tests/OrderTests.cs", true},
		{"src/MyApp.Specs/CalculatorSpec.cs", true},
		{"UnitTests/CalculatorTest.cs", true},
		{"backend/IntegrationTests/OrderTest.cs", true},
		// A dotted project name with more than one suffix — MyApp.Tests.Unit is
		// how a .NET solution splits a test project — has to be read component
		// by component: reading only the last one missed it entirely.
		{"src/MyApp.Tests.Unit/OrderTest.cs", true},
		{"src/MyApp.Tests.Integration/Orders/OrderTest.cs", true},
		{"src/MyApp.Certificates.Api/LabTest.cs", false},

		// Java: uppercase Test/Tests/IT, or a test tree.
		{"src/main/java/com/acme/Audit.java", false},
		{"src/main/java/com/acme/Deposit.java", false},
		{"src/main/java/com/acme/Manifest.java", false},
		{"src/main/java/com/acme/Unit.java", false},
		{"src/main/java/com/acme/Latest.java", false},
		{"src/main/java/com/acme/FooTest.java", true},
		{"src/main/java/com/acme/FooTests.java", true},
		{"src/main/java/com/acme/FooIT.java", true},
		{"src/test/java/com/acme/Helper.java", true},

		// Kotlin / Swift / PHP / Scala.
		{"src/Circuit.kt", false},
		{"src/CalculatorTest.kt", true},
		{"src/Circuit.swift", false},
		{"src/CalculatorTests.swift", true},
		{"src/Latest.php", false},
		{"src/UserTest.php", true},
		{"src/Latest.scala", false},
		{"src/Circuit.scala", false},
		{"src/FooSpec.scala", true},
		{"src/FooSuite.scala", true},

		// Unknown extensions stay out.
		{"README.md", false},
		{"Makefile", false},
	}
	for _, tc := range cases {
		if got := isConventionalTestPath(tc.path); got != tc.want {
			t.Errorf("isConventionalTestPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestIsTestSupportFile pins the fixture/helper split. These files are tests as
// far as scoring is concerned but cannot be handed to a runner: pytest and jest
// collect zero tests from them and fail the build for the wrong reason.
func TestIsTestSupportFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"tests/conftest.py", true},
		{"tests/factories.py", true},
		{"tests/test_api.py", false},
		{"spec/spec_helper.rb", true},
		{"spec/rails_helper.rb", true},
		{"spec/models/user_spec.rb", false},
		{"__tests__/setup.ts", true},
		{"__tests__/api.test.ts", false},
		{"test/fixtures.go", true},
		{"test/api_test.go", false},
		{"backend/tests/Leroy.Platform.Tests/Fixtures/DbFixture.cs", true},
		{"backend/tests/Leroy.Platform.Tests/OrderTests.cs", false},

		// Outside a test tree nothing is demoted, and an extension with no
		// naming convention is left alone rather than guessed at.
		{"src/app/user.go", false},
		{"src/testing/util.go", false},
		{"test/README.md", false},
		{"tests/run.sh", false},

		// jest's default testMatch collects EVERY file under __tests__, named
		// like a test or not. Demoting them silently dropped real suites.
		{"__tests__/login.js", false},
		{"__tests__/orders/checkout.ts", false},
		{"__tests__/helpers.ts", true},
		{"__tests__/fixtures/user.ts", true},
		{"src/__tests__/api.jsx", false},
		// Outside __tests__ jest needs the .test/.spec name, so a bare file in
		// a test/ tree really is a helper.
		{"test/utils.js", true},
		{"spec/support/render.tsx", true},

		// C# test classes are found by attribute, not by name: xUnit *Facts.cs,
		// MSpec When_*.cs and *Scenarios.cs are real suites.
		{"tests/MyApp.Tests/OrderScenarios.cs", false},
		{"tests/MyApp.Tests/OrderFacts.cs", false},
		{"tests/MyApp.Tests/When_an_order_ships.cs", false},
		{"tests/MyApp.Tests/Helpers/OrderBuilder.cs", true},
		{"tests/MyApp.Tests/TestBase.cs", true},

		// Ruby and Python keep the strict reading: their runners collect by
		// file name, so anything else in the tree is a helper.
		{"spec/support/shared_examples.rb", true},
		{"tests/helpers.py", true},
	}
	for _, tc := range cases {
		if got := isTestSupportFile(tc.path); got != tc.want {
			t.Errorf("isTestSupportFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestCoChangeScoreTiers pins the tiers the README documents. Co-change is the
// weakest signal witness has — the two files merely changed together, with no
// import relationship at all — and its top tier reaching 0.6 means the
// documented "direct + 1-hop only" recipe (--depth 1 --min-score 0.5) still
// admits it. Whatever the numbers are, the README has to say the same ones.
func TestCoChangeScoreTiers(t *testing.T) {
	cases := []struct {
		count int
		want  float64
	}{
		{1, 0.3},
		{4, 0.3},
		{5, 0.5},
		{9, 0.5},
		{10, 0.6},
		{100, 0.6},
	}
	for _, tc := range cases {
		if got := cochangeScore(tc.count); got != tc.want {
			t.Errorf("cochangeScore(%d) = %v, want %v (and README.md must document it)", tc.count, got, tc.want)
		}
	}
}

// TestSelect_SignalsFilter covers --signals: the flag that makes the README's
// "direct + 1-hop only" recipe actually expressible, since a co-changed test
// with no import relationship otherwise clears --min-score 0.5 on frequency
// alone.
func TestSelect_SignalsFilter(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{{Path: "src/a_test.go"}}
	f.cochange["src/a.go"] = []recon.CoChangePair{{File: "other/other_test.go", Count: 10}}
	f.testFiles["other/other_test.go"] = true

	all, _ := Select(f, []string{"src/a.go"}, DefaultOptions())
	if _, ok := byPath(all)["other/other_test.go"]; !ok {
		t.Fatalf("co-changed test should be selected by default; got %+v", all.Tests)
	}

	opts := DefaultOptions()
	opts.Signals = []string{"direct-test", "import"}
	res, _ := Select(f, []string{"src/a.go"}, opts)
	got := byPath(res)
	if _, ok := got["other/other_test.go"]; ok {
		t.Errorf("--signals direct-test,import must drop a co-change-only test; got %+v", res.Tests)
	}
	if _, ok := got["src/a_test.go"]; !ok {
		t.Errorf("--signals direct-test,import must keep the directly mapped test; got %+v", res.Tests)
	}
	if res.Summary.Filtered == 0 {
		t.Error("a test dropped by --signals must be counted in Summary.Filtered")
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

// TestSelect_KindDoesNotDependOnDiscoveringSignal pins the single source of
// truth for a test's kind. recon's path heuristic knows three kinds and
// witness's knows five, and the label used to come from whichever one the
// discovering signal happened to carry — so `witness run --kind smoke` skipped
// the same smoke test on the diffs that reached it through a direct mapping.
func TestSelect_KindDoesNotDependOnDiscoveringSignal(t *testing.T) {
	const smoke = "test/smoke/health_test.go"

	// Discovered as the changed file: no recon kind at all.
	viaChange := newFake()
	viaChange.testFiles[smoke] = true
	changed, _ := Select(viaChange, []string{smoke}, DefaultOptions())

	// Discovered through recon's test map, which labels it "unit".
	viaMap := newFake()
	viaMap.tests["src/health.go"] = []recon.TestFile{{Path: smoke, Kind: "unit"}}
	mapped, _ := Select(viaMap, []string{"src/health.go"}, DefaultOptions())

	if got := byPath(changed)[smoke].Kind; got != "smoke" {
		t.Errorf("kind via changed-test = %q, want smoke", got)
	}
	if got := byPath(mapped)[smoke].Kind; got != "smoke" {
		t.Errorf("kind via direct-test = %q, want smoke", got)
	}

	// And the filter agrees with both.
	opts := DefaultOptions()
	opts.Kinds = []string{"smoke"}
	res, _ := Select(viaMap, []string{"src/health.go"}, opts)
	if len(res.Tests) != 1 {
		t.Errorf("--kind smoke should keep the smoke test; got %+v", res.Tests)
	}
}

// TestSelect_KindFallsBackToRecon keeps recon's classification useful for paths
// witness's own heuristic reads nothing from.
func TestSelect_KindFallsBackToRecon(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{{Path: "pkg/a_test.go", Kind: "Integration"}}

	res, _ := Select(f, []string{"src/a.go"}, DefaultOptions())
	if got := byPath(res)["pkg/a_test.go"].Kind; got != "integration" {
		t.Errorf("kind = %q, want integration (recon's kind, normalized)", got)
	}
}

func TestSelect_KindFilterIsCaseInsensitive(t *testing.T) {
	f := newFake()
	f.tests["src/a.go"] = []recon.TestFile{{Path: "test/e2e/flow_test.go"}}
	opts := DefaultOptions()
	opts.Kinds = []string{"E2E"}

	res, _ := Select(f, []string{"src/a.go"}, opts)
	if len(res.Tests) != 1 {
		t.Errorf("--kind E2E should match kind e2e; got %+v", res.Tests)
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
