package e2e

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// allFormats is what every scenario is replayed through: the three answers
// witness can give, from the machine-readable one down to the command a CI job
// actually executes.
var allFormats = []string{"json", "paths", "exec"}

// goldenScenario is one working-tree change, replayed through every output
// format and pinned to a golden file.
type goldenScenario struct {
	// name is the golden file prefix and the subtest name.
	name string
	// why says which behaviour the golden exists to protect, so a failure
	// explains itself before anyone opens the .golden file.
	why string
	// edit makes the change under test. The repository is reset to HEAD first,
	// so scenarios cannot leak into each other.
	edit func(t *testing.T, r *repo)
	// args are appended after `select --format <format>`.
	args []string
	// formats defaults to allFormats.
	formats []string
}

type goldenFixture struct {
	// fixture is the directory under testdata/fixtures.
	fixture string
	// setup runs once, after the initial commit, for fixtures that need more
	// git history than a single commit (co-change evidence, say).
	setup     func(t *testing.T, r *repo)
	scenarios []goldenScenario
}

var goldenFixtures = []goldenFixture{
	{
		fixture: "go",
		scenarios: []goldenScenario{
			{
				name: "source-edit",
				why:  "editing a package selects its own _test.go and runs it as a package pattern, never as a file path",
				edit: func(t *testing.T, r *repo) {
					r.append(t, "calc/calc.go", "\n// Sub returns a - b.\nfunc Sub(a, b int) int { return a - b }\n")
				},
			},
			{
				name: "helper-edit",
				why:  "editing a helper the test imports must reach the test through the import graph",
				edit: func(t *testing.T, r *repo) {
					r.append(t, "internal/mathx/mathx.go", "\n// Triple returns n*3.\nfunc Triple(n int) int { return n * 3 }\n")
				},
			},
			{
				name:    "test-cmd-override",
				why:     "--test-cmd appends the selection to the user's runner, and `go test calc/calc_test.go` compiles one file as command-line-arguments and fails to build — the override has to speak package globs like the detected command does",
				args:    []string{"--test-cmd", "gotestsum --"},
				formats: []string{"exec"},
				edit: func(t *testing.T, r *repo) {
					r.append(t, "calc/calc.go", "\n// Sub returns a - b.\nfunc Sub(a, b int) int { return a - b }\n")
				},
			},
		},
	},
	{
		fixture: "python",
		// Two commits that touch conftest.py and test_api.py together: without
		// history there is no co-change evidence, and a shared fixture has no
		// import edge for recon to follow.
		setup: func(t *testing.T, r *repo) {
			for _, msg := range []string{"tighten the customer fixture", "cover the empty order"} {
				r.append(t, "tests/conftest.py", "\n\n# "+msg+"\n")
				r.append(t, "tests/test_api.py", "\n\n# "+msg+"\n")
				r.commit(t, msg)
			}
			r.warm(t)
		},
		scenarios: []goldenScenario{
			{
				name: "source-edit",
				why:  "editing src/api.py selects tests/test_api.py",
				edit: func(t *testing.T, r *repo) {
					r.append(t, "src/api.py", "\n\ndef cancel_order(order):\n    return None\n")
				},
			},
			{
				name: "conftest-edit",
				why:  "editing tests/conftest.py selects the tests that depend on it — conftest.py itself collects no tests",
				edit: func(t *testing.T, r *repo) {
					r.append(t, "tests/conftest.py", "\n\n@pytest.fixture\ndef total():\n    return 10\n")
				},
			},
		},
	},
	{
		fixture: "java",
		scenarios: []goldenScenario{
			{
				name: "source-edit",
				why:  "Calculator.java maps to CalculatorTest.java, and the fixture's pom turns that into `mvn test -Dtest=CalculatorTest` — a class filter, never the path, which mvn would read as a lifecycle phase",
				edit: func(t *testing.T, r *repo) {
					r.write(t, "src/main/java/com/example/Calculator.java", `package com.example;

public class Calculator {
    public int add(int a, int b) {
        return a + b;
    }

    public int sub(int a, int b) {
        return a - b;
    }
}
`)
				},
			},
			{
				name: "audit-edit",
				why:  "Audit.java ends in \"it\" but is production code: it must never be selected as a test",
				edit: func(t *testing.T, r *repo) {
					r.write(t, "src/main/java/com/example/Audit.java", `package com.example;

public class Audit {
    public String record(String event) {
        return event.trim();
    }
}
`)
				},
			},
		},
	},
	{
		fixture: "rust",
		scenarios: []goldenScenario{
			{
				name: "source-edit",
				why:  "cargo takes a target name, not a path: `cargo test tests/orders_test.rs` filters everything out and exits 0",
				edit: func(t *testing.T, r *repo) {
					r.append(t, "src/orders.rs", "\npub fn count(orders: &[Order]) -> usize {\n    orders.len()\n}\n")
				},
			},
		},
	},
	{
		fixture: "csharp",
		scenarios: []goldenScenario{
			{
				name: "source-edit",
				why:  "both test projects cover Calculator.cs and each gets its own `dotnet test <project>`",
				edit: func(t *testing.T, r *repo) {
					r.write(t, "src/Orders/Calculator.cs", `namespace Orders;

public class Calculator
{
    public int Add(int a, int b) => a + b;

    public int Sub(int a, int b) => a - b;
}
`)
				},
			},
			{
				name:    "unit-only",
				why:     "--kind keeps the integration project out without dropping the unit project",
				args:    []string{"--kind", "unit"},
				formats: []string{"paths", "exec"},
				edit: func(t *testing.T, r *repo) {
					r.append(t, "src/Orders/Calculator.cs", "\n// touched\n")
				},
			},
		},
	},
	{
		fixture: "node",
		scenarios: []goldenScenario{
			{
				name: "route-group-edit",
				why:  "jest reads a positional argument as a REGEX, so a Next.js route group — src/app/(marketing)/page.test.js — matches zero tests; with passWithNoTests jest then exits 0 and witness passes having run nothing",
				edit: func(t *testing.T, r *repo) {
					r.append(t, "src/app/(marketing)/page.js", "\nexport function subhead(name) {\n  return `Hi, ${name}`;\n}\n")
				},
			},
		},
	},
	{
		fixture: "polyglot",
		scenarios: []goldenScenario{
			{
				name: "elixir-and-js-edit",
				why:  "a change spanning two ecosystems must produce one command per ecosystem, not one command no runner understands",
				edit: func(t *testing.T, r *repo) {
					r.append(t, "lib/shop/cart.ex", "\n# touched\n")
					r.append(t, "assets/js/cart.js", "\n// touched\n")
				},
			},
		},
	},
}

// TestGolden runs every fixture through `select` in every output format and
// compares the whole transcript — stdout, stderr and exit code — against a
// checked-in golden file.
//
// The stderr half matters as much as stdout: witness's contract is that it says
// out loud when it cannot prove a selection, and a silent stderr with an empty
// selection is the false green the whole tool exists to prevent.
func TestGolden(t *testing.T) {
	for _, f := range goldenFixtures {
		t.Run(f.fixture, func(t *testing.T) {
			r := newRepo(t, f.fixture)
			if f.setup != nil {
				f.setup(t, r)
			}

			for _, sc := range f.scenarios {
				formats := sc.formats
				if len(formats) == 0 {
					formats = allFormats
				}
				for _, format := range formats {
					t.Run(sc.name+"/"+format, func(t *testing.T) {
						// Say what the golden is for, but only when it broke.
						t.Cleanup(func() {
							if t.Failed() {
								t.Logf("this golden protects: %s", sc.why)
							}
						})

						r.reset(t)
						sc.edit(t, r)

						args := append([]string{"select", "--format", format}, sc.args...)
						res := r.run(t, args...)
						if res.err != nil && res.code == 0 {
							t.Fatalf("%s: error %v reported with exit code 0", sc.why, res.err)
						}
						r.checkGolden(t, path.Join(f.fixture, sc.name+"_"+format), res)
					})
				}
			}
		})
	}
}

// TestNoOrphanedGoldenFiles fails when testdata/golden holds a file no scenario
// produces any more. A golden nothing checks is worse than no golden: it looks
// like coverage in the diff and asserts nothing at all.
func TestNoOrphanedGoldenFiles(t *testing.T) {
	claimed := make(map[string]bool)
	for _, f := range goldenFixtures {
		for _, sc := range f.scenarios {
			formats := sc.formats
			if len(formats) == 0 {
				formats = allFormats
			}
			for _, format := range formats {
				claimed[path.Join(f.fixture, sc.name+"_"+format)+".golden"] = true
			}
		}
	}

	err := filepath.WalkDir(goldenDir(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".golden") {
			return nil
		}
		rel, err := filepath.Rel(goldenDir(), p)
		if err != nil {
			return err
		}
		if !claimed[filepath.ToSlash(rel)] {
			t.Errorf("golden file %s belongs to no scenario; delete it or restore the scenario that produced it", rel)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walking %s: %v", goldenDir(), err)
	}
}
