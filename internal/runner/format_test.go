package runner

import (
	"reflect"
	"testing"
)

// mustFormat fails the test if FormatCommand reports it cannot run something.
// root is the repository the selection came from; "" is the no-filesystem case,
// which the path-only arms (go, node, rust in a single crate) do not need.
func mustFormat(t *testing.T, root, framework string, paths []string) []Command {
	t.Helper()
	cmds, err := FormatCommand(root, framework, paths)
	if err != nil {
		t.Fatalf("FormatCommand(%q, %q, %v): %v", root, framework, paths, err)
	}
	return cmds
}

func TestFormatCommand_Go(t *testing.T) {
	// Go paths collapse to deduped package globs (./dir/...), sorted so the
	// emitted command is byte-identical whatever order the paths arrive in.
	want := [][]string{{"go", "test", "./internal/a/...", "./internal/b/...", "./internal/c/..."}}

	for _, paths := range [][]string{
		{"internal/a/x_test.go", "internal/a/y_test.go", "internal/b/z_test.go", "internal/c/w_test.go"},
		{"internal/c/w_test.go", "internal/b/z_test.go", "internal/a/y_test.go", "internal/a/x_test.go"},
	} {
		got := argvsOf(mustFormat(t, "", "go", paths))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("FormatCommand(go, %v) = %v, want %v", paths, got, want)
		}
	}
}

func TestFormatCommand_Rust(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  [][]string
	}{
		{
			// Verified against cargo 1.94.1: `cargo test tests/mathx.rs`
			// reports "0 passed; 1 filtered out" and exits 0 even with a
			// failing test, while `cargo test --test mathx` exits 101.
			name:  "integration test names the target",
			paths: []string{"tests/mathx.rs"},
			want:  [][]string{{"cargo", "test", "--test", "mathx"}},
		},
		{
			name:  "one command per integration target, sorted",
			paths: []string{"tests/orders.rs", "tests/mathx.rs"},
			want: [][]string{
				{"cargo", "test", "--test", "mathx"},
				{"cargo", "test", "--test", "orders"},
			},
		},
		{
			name:  "multi-file target directory maps to its target",
			paths: []string{"tests/api/main.rs", "tests/api/helpers.rs"},
			want:  [][]string{{"cargo", "test", "--test", "api"}},
		},
		{
			// `cargo test --lib` is not valid for every crate shape: verified
			// against cargo 1.94.1, a crate with only src/main.rs answers
			// "error: no library targets found in package" and exits 101, so a
			// unit test under src/ widens to the whole suite instead. Adding
			// --bins does not help; the --lib term itself is what fails.
			name:  "unit tests in src widen to the whole suite",
			paths: []string{"src/mathx.rs"},
			want:  [][]string{{"cargo", "test"}},
		},
		{
			name:  "a src unit test widens even alongside an integration target",
			paths: []string{"tests/mathx.rs", "src/orders.rs"},
			want:  [][]string{{"cargo", "test"}},
		},
		{
			// Unmappable path: widen to the whole suite rather than emit a
			// filter that silently matches nothing.
			name:  "unmappable path falls back to the whole suite",
			paths: []string{"benches/thing.rs"},
			want:  [][]string{{"cargo", "test"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := argvsOf(mustFormat(t, "", "rust", tt.paths))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FormatCommand(rust, %v) = %v, want %v", tt.paths, got, tt.want)
			}
		})
	}
}

func TestFormatCommand_Rust_NeverPassesAPathAsFilter(t *testing.T) {
	// Guard against the original bug in any shape: a bare .rs path as a
	// positional arg is a name filter that matches nothing.
	cmds := mustFormat(t, "", "rust", []string{"tests/a.rs", "src/lib.rs", "tests/b/main.rs"})
	for _, c := range cmds {
		for i, arg := range c.Argv {
			if i > 0 && c.Argv[i-1] == "--test" {
				continue
			}
			if len(arg) > 3 && arg[len(arg)-3:] == ".rs" {
				t.Errorf("command %q passes the path %q as a name filter", c, arg)
			}
		}
	}
}

// TestFormatCommand_NodeRunsPathsNotPatterns pins the jest false green. A bare
// positional argument is a REGEX jest matches against every collected path, so
// a Next.js App Router route group — src/app/(marketing)/page.test.tsx — has
// its parens read as a capture group and matches zero files. jest then prints
// "No tests found" and, with the mainstream passWithNoTests setting, exits 0:
// witness reports success having run none of the tests it selected.
// --runTestsByPath takes the arguments literally.
func TestFormatCommand_NodeRunsPathsNotPatterns(t *testing.T) {
	paths := []string{
		"src/app/(marketing)/page.test.tsx",
		"src/app/[slug]/route.test.ts",
		"src/a+b/c.spec.js",
	}
	got := argvsOf(mustFormat(t, "", "node", paths))
	want := [][]string{append([]string{"npx", "jest", "--runTestsByPath"}, paths...)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FormatCommand(node, %v) = %v, want %v", paths, got, want)
	}

	// Said as a property, so no future rewrite can reintroduce it: a path
	// carrying regex metacharacters must never be a bare positional pattern.
	for _, c := range mustFormat(t, "", "node", paths) {
		if !contains(c.Argv, "--runTestsByPath") {
			t.Errorf("jest command %q passes paths as regex patterns; a route-group path then matches nothing", c)
		}
	}
}

func contains(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

func TestFormatCommand_DotNet(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  [][]string
	}{
		{
			name:  "suffixed test project",
			paths: []string{"backend/tests/Leroy.Platform.Tests/CertificateTests.cs"},
			want:  [][]string{{"dotnet", "test", "./backend/tests/Leroy.Platform.Tests"}},
		},
		{
			// A project whose name is not *.Tests still has its own directory.
			name:  "unsuffixed test project uses its own directory",
			paths: []string{"tests/Acme.Api.Functional/OrderTests.cs"},
			want:  [][]string{{"dotnet", "test", "./tests/Acme.Api.Functional"}},
		},
		{
			name:  "test class in a non-test project",
			paths: []string{"src/Acme.Core/CalculatorTests.cs"},
			want:  [][]string{{"dotnet", "test", "./src/Acme.Core"}},
		},
		{
			// `dotnet test ./tests` dies with MSB1003 (no project or solution
			// there), so widen to the solution instead.
			name:  "file directly in tests/ widens to the solution",
			paths: []string{"tests/CalculatorTests.cs"},
			want:  [][]string{{"dotnet", "test"}},
		},
		{
			name:  "file at the repo root widens to the solution",
			paths: []string{"CalculatorTests.cs"},
			want:  [][]string{{"dotnet", "test"}},
		},
		{
			// The unpinnable path must not be silently dropped in favour of
			// only running the project that could be pinned.
			name:  "unpinnable path mixed with a project widens to the solution",
			paths: []string{"CalculatorTests.cs", "src/Acme.Tests/ATests.cs"},
			want:  [][]string{{"dotnet", "test"}},
		},
		{
			name: "several projects are separate commands",
			paths: []string{
				"backend/tests/Leroy.Platform.Tests/A.cs",
				"backend/tests/Leroy.Api.IntegrationTests/B.cs",
				"backend/tests/Leroy.Web.E2E/C.cs",
			},
			want: [][]string{
				{"dotnet", "test", "./backend/tests/Leroy.Api.IntegrationTests"},
				{"dotnet", "test", "./backend/tests/Leroy.Platform.Tests"},
				{"dotnet", "test", "./backend/tests/Leroy.Web.E2E"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := argvsOf(mustFormat(t, "", "csharp", tt.paths))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FormatCommand(csharp, %v) = %v, want %v", tt.paths, got, tt.want)
			}
		})
	}
}

func TestFormatCommand_EmptyPaths(t *testing.T) {
	cmds, err := FormatCommand("", "go", nil)
	if err != nil {
		t.Fatalf("FormatCommand(go, nil): %v", err)
	}
	if len(cmds) != 0 {
		t.Errorf("empty paths = %v, want no commands", argvsOf(cmds))
	}
}

func TestCommandString(t *testing.T) {
	tests := []struct {
		argv []string
		want string
	}{
		{[]string{"go", "test", "./internal/a/..."}, "go test ./internal/a/..."},
		{[]string{"npx", "jest", "src/app/(marketing)/page.test.tsx"}, "npx jest 'src/app/(marketing)/page.test.tsx'"},
		{[]string{"pytest", "tests/my app/test_thing.py"}, "pytest 'tests/my app/test_thing.py'"},
		{[]string{"pytest", "tests/$(touch pwned)_test.py"}, `pytest 'tests/$(touch pwned)_test.py'`},
		{[]string{"pytest", "tests/it's_test.py"}, `pytest 'tests/it'\''s_test.py'`},
	}

	for _, tt := range tests {
		got := Command{Argv: tt.argv}.String()
		if got != tt.want {
			t.Errorf("Command%v.String() = %q, want %q", tt.argv, got, tt.want)
		}
	}
}

func TestDetectFramework_GoWebFrameworks(t *testing.T) {
	for _, fw := range []string{"Gin", "Echo", "Fiber"} {
		if got := DetectFramework([]string{fw}); got != "go" {
			t.Errorf("DetectFramework(%q) = %q, want go", fw, got)
		}
	}
	// FastAPI -> python (covers that branch).
	if got := DetectFramework([]string{"FastAPI"}); got != "python" {
		t.Errorf("FastAPI = %q, want python", got)
	}
	// First match wins across a mixed list.
	if got := DetectFramework([]string{"unknown", "Vue"}); got != "node" {
		t.Errorf("mixed list = %q, want node", got)
	}
}
