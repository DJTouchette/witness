package runner

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// cargoWorkspaceTree is a workspace whose member directories are named nothing
// like their packages — crates/http-api holds "acme-http" — which is why -p
// cannot be derived from the path.
//
// The root manifest is a package as well as a workspace, the shape that makes a
// bare `cargo test` run only the root package's tests (verified against cargo
// 1.94.1: it reports "0 passed" and exits 0 while the members go untested).
func cargoWorkspaceTree() map[string]string {
	return map[string]string{
		"Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n" +
			"[workspace]\nmembers = [\"crates/http-api\", \"crates/cli\"]\n",
		"src/lib.rs": "pub fn app() -> i32 { 1 }\n",

		"crates/http-api/Cargo.toml":      "[package]\nname = \"acme-http\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"crates/http-api/src/lib.rs":      "pub fn add(a: i32, b: i32) -> i32 { a + b }\n",
		"crates/http-api/tests/orders.rs": "#[test]\nfn adds_numbers() { assert_eq!(acme_http::add(1, 2), 3); }\n",

		// A binary-only crate: `cargo test --lib` is an error here, so the
		// widened form must be `-p`, not `--lib`.
		"crates/cli/Cargo.toml":  "[package]\nname = \"acme-cli\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"crates/cli/src/main.rs": "fn main() {}\n\n#[cfg(test)]\nmod tests {\n    #[test]\n    fn cli_works() { assert!(true); }\n}\n",
	}
}

// cargoVirtualWorkspaceTree is the other workspace shape: a root manifest that
// is only a [workspace], with no package of its own to attribute a stray path
// to.
func cargoVirtualWorkspaceTree() map[string]string {
	return map[string]string{
		"Cargo.toml":                      "[workspace]\nmembers = [\"crates/http-api\"]\nresolver = \"2\"\n",
		"crates/http-api/Cargo.toml":      "[package]\nname = \"acme-http\"\nversion = \"0.1.0\"\n",
		"crates/http-api/tests/orders.rs": "#[test]\nfn adds() {}\n",
		"tests/smoke.rs":                  "#[test]\nfn smoke() {}\n",
	}
}

func TestFormatCommand_RustWorkspace(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		paths []string
		want  [][]string
	}{
		{
			// The package name is not the directory name, so -p has to come out
			// of the member's Cargo.toml.
			name:  "integration test names package and target",
			files: cargoWorkspaceTree(),
			paths: []string{"crates/http-api/tests/orders.rs"},
			want:  [][]string{{"cargo", "test", "-p", "acme-http", "--test", "orders"}},
		},
		{
			// A src/ unit test has no nameable target, but the package is known:
			// widening to that package still runs the selected test, where the
			// old bare `cargo test` ran none of it.
			name:  "unit test widens to its package, not the workspace",
			files: cargoWorkspaceTree(),
			paths: []string{"crates/cli/src/main.rs"},
			want:  [][]string{{"cargo", "test", "-p", "acme-cli"}},
		},
		{
			name:  "a package-wide run subsumes that package's targets",
			files: cargoWorkspaceTree(),
			paths: []string{"crates/http-api/tests/orders.rs", "crates/http-api/src/lib.rs"},
			want:  [][]string{{"cargo", "test", "-p", "acme-http"}},
		},
		{
			name:  "several packages, sorted",
			files: cargoWorkspaceTree(),
			paths: []string{"crates/cli/src/main.rs", "crates/http-api/tests/orders.rs"},
			want: [][]string{
				{"cargo", "test", "-p", "acme-cli"},
				{"cargo", "test", "-p", "acme-http", "--test", "orders"},
			},
		},
		{
			// The root manifest is a package too, and its own targets need -p
			// just as much: `cargo test --test smoke` there answers "no test
			// target named `smoke` in default-run packages".
			name:  "the root package is named too",
			files: withFiles(cargoWorkspaceTree(), map[string]string{"tests/smoke.rs": "#[test]\nfn smoke() {}\n"}),
			paths: []string{"tests/smoke.rs"},
			want:  [][]string{{"cargo", "test", "-p", "app", "--test", "smoke"}},
		},
		{
			// No [package] name to be had: widen to the whole workspace rather
			// than guess a -p. `cargo test` alone would not do — under a root
			// package it runs only that package.
			name: "a member with an unreadable name widens to the workspace",
			files: withFiles(cargoWorkspaceTree(), map[string]string{
				"crates/http-api/Cargo.toml": "[package]\nname = { workspace = true }\nversion = \"0.1.0\"\n",
			}),
			paths: []string{"crates/http-api/tests/orders.rs"},
			want:  [][]string{{"cargo", "test", "--workspace"}},
		},
		{
			// A virtual manifest has no root package; witness still scopes to the
			// member so a many-crate workspace does not rebuild everything.
			name:  "virtual manifest",
			files: cargoVirtualWorkspaceTree(),
			paths: []string{"crates/http-api/tests/orders.rs"},
			want:  [][]string{{"cargo", "test", "-p", "acme-http", "--test", "orders"}},
		},
		{
			// Nothing above tests/smoke.rs but the virtual manifest, which names
			// no package: there is no -p to derive, so the run widens. Running
			// only the half that resolved and exiting 0 would report a pass for
			// the other half.
			name:  "a path in no package widens the whole selection",
			files: cargoVirtualWorkspaceTree(),
			paths: []string{"crates/http-api/tests/orders.rs", "tests/smoke.rs"},
			want:  [][]string{{"cargo", "test", "--workspace"}},
		},
		{
			// A path that climbs out of the repository is never read, so it can
			// never be scoped either.
			name:  "a path outside the repository widens the whole selection",
			files: cargoVirtualWorkspaceTree(),
			paths: []string{"../elsewhere/tests/orders.rs"},
			want:  [][]string{{"cargo", "test", "--workspace"}},
		},
		{
			// Not a workspace: -p would be one more thing to get wrong, and
			// there is only one package for the target to be in.
			name: "single crate keeps the plain form",
			files: map[string]string{
				"Cargo.toml":      "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
				"tests/orders.rs": "#[test]\nfn adds() {}\n",
			},
			paths: []string{"tests/orders.rs"},
			want:  [][]string{{"cargo", "test", "--test", "orders"}},
		},
		{
			name: "single crate unit test widens to the crate",
			files: map[string]string{
				"Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
				"src/lib.rs": "pub fn f() {}\n",
			},
			paths: []string{"src/lib.rs"},
			want:  [][]string{{"cargo", "test"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			got := argvsOf(mustFormat(t, root, "rust", tt.paths))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FormatCommand(rust) = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFormatCommand_RustWorkspaceNeverWidensToBareCargoTest pins the false green
// this arm was written for. In a workspace with a root package, `cargo test`
// runs the ROOT package's tests only — verified against cargo 1.94.1, it prints
// "running 0 tests" and exits 0 while every member goes untested. Whatever the
// selection, the widened command must say --workspace or -p.
func TestFormatCommand_RustWorkspaceNeverWidensToBareCargoTest(t *testing.T) {
	trees := map[string]map[string]string{
		"scoped": cargoWorkspaceTree(),
		// Nothing to scope with: the member's name cannot be read, so the widest
		// command is the only honest one — and it still must not be bare.
		"unscopable member": withFiles(cargoWorkspaceTree(), map[string]string{
			"crates/http-api/Cargo.toml": "[package]\nname = { workspace = true }\n",
		}),
	}
	selections := [][]string{
		{"crates/cli/src/main.rs"},
		{"crates/http-api/src/lib.rs", "crates/http-api/tests/orders.rs"},
		{"benches/throughput.rs"},
		{"crates/http-api/tests/orders.rs", "benches/throughput.rs"},
		{"crates/http-api/tests/orders.rs"},
	}

	for name, files := range trees {
		root := writeTree(t, files)
		for _, paths := range selections {
			for _, c := range mustFormat(t, root, "rust", paths) {
				if reflect.DeepEqual(c.Argv, []string{"cargo", "test"}) {
					t.Errorf("%s: selection %v widened to a bare `cargo test`, which in a workspace with a root package runs none of the members' tests and exits 0", name, paths)
				}
			}
		}
	}
}

// withFiles overlays files onto a tree, for a variant of a shared fixture.
func withFiles(base, overlay map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range overlay {
		merged[k] = v
	}
	return merged
}

func TestParseCargoManifest(t *testing.T) {
	tests := []struct {
		name          string
		toml          string
		wantName      string
		wantWorkspace bool
	}{
		{
			name:     "plain package",
			toml:     "[package]\nname = \"acme-http\"\nversion = \"0.1.0\"\n",
			wantName: "acme-http",
		},
		{
			name:     "comment after the value",
			toml:     "[package]\nname = \"acme-http\" # the HTTP crate\n",
			wantName: "acme-http",
		},
		{
			name:     "a # inside the string is not a comment",
			toml:     "[package]\ndescription = \"tag # here\"\nname = \"acme-http\"\n",
			wantName: "acme-http",
		},
		{
			name:     "literal string",
			toml:     "[package]\nname = 'acme-http'\n",
			wantName: "acme-http",
		},
		{
			name:     "quoted key",
			toml:     "[package]\n\"name\" = \"acme-http\"\n",
			wantName: "acme-http",
		},
		{
			name:     "dotted key with no table header",
			toml:     "package.name = \"acme-http\"\npackage.version = \"0.1.0\"\n",
			wantName: "acme-http",
		},
		{
			// The reason this is a line scan and not a regex: a README pasted
			// into description is full of text that looks like TOML.
			name: "multi-line string is not TOML",
			toml: "[package]\nname = \"acme-http\"\ndescription = \"\"\"\n" +
				"[workspace]\nname = \"evil\"\n\"\"\"\nversion = \"0.1.0\"\n",
			wantName:      "acme-http",
			wantWorkspace: false,
		},
		{
			name:     "a name in another table is not the package name",
			toml:     "[package]\nname = \"acme-cli\"\n\n[[bin]]\nname = \"acme\"\npath = \"src/main.rs\"\n",
			wantName: "acme-cli",
		},
		{
			name:     "only a bin name means no package name",
			toml:     "[[bin]]\nname = \"acme\"\n",
			wantName: "",
		},
		{
			name:          "virtual manifest",
			toml:          "[workspace]\nmembers = [\"crates/a\"]\n",
			wantName:      "",
			wantWorkspace: true,
		},
		{
			name:          "workspace subtable",
			toml:          "[package]\nname = \"app\"\n\n[workspace.package]\nedition = \"2021\"\n",
			wantName:      "app",
			wantWorkspace: true,
		},
		{
			name:          "dotted workspace key",
			toml:          "workspace.members = [\"crates/a\"]\n[package]\nname = \"app\"\n",
			wantName:      "app",
			wantWorkspace: true,
		},
		{
			// A dependency inheriting from the workspace is not a workspace
			// declaration; reading it as one would widen every member's run.
			name:          "an inherited dependency is not a workspace",
			toml:          "[package]\nname = \"acme-http\"\n\n[dependencies]\nserde = { workspace = true }\n",
			wantName:      "acme-http",
			wantWorkspace: false,
		},
		{
			// Anything that is not a plain quoted, cargo-legal name leaves the
			// name empty, which widens the run. A wrong -p would run another
			// crate's tests and report them as this change's.
			name:     "name that is not a plain string",
			toml:     "[package]\nname = { workspace = true }\n",
			wantName: "",
		},
		{
			name:     "name that cargo would reject",
			toml:     "[package]\nname = \"not a name\"\n",
			wantName: "",
		},
		{
			// A header the scan cannot read leaves the table unknown, so the
			// name below it is not attributed to [package].
			name:     "unterminated table header",
			toml:     "[package\nname = \"acme\"\n",
			wantName: "",
		},
		{
			name:     "unterminated string value",
			toml:     "[package]\nname = \"acme\n",
			wantName: "",
		},
		{
			name:     "empty value",
			toml:     "[package]\nname =\n",
			wantName: "",
		},
		{
			name:     "empty manifest",
			toml:     "",
			wantName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCargoManifest([]byte(tt.toml))
			if got.name != tt.wantName {
				t.Errorf("name = %q, want %q", got.name, tt.wantName)
			}
			if got.workspace != tt.wantWorkspace {
				t.Errorf("workspace = %v, want %v", got.workspace, tt.wantWorkspace)
			}
		})
	}
}

// TestCargoRunsTheDerivedInvocation runs real cargo against the scratch
// workspace and checks that what witness emits actually selects the test — and
// that what witness used to emit does not.
func TestCargoRunsTheDerivedInvocation(t *testing.T) {
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		t.Skip("cargo is not installed: the derived invocations are pinned by argv above but cannot be run here")
	}
	root := writeTree(t, cargoWorkspaceTree())
	target := t.TempDir()

	t.Run("integration target in a member", func(t *testing.T) {
		cmds := mustFormat(t, root, "rust", []string{"crates/http-api/tests/orders.rs"})
		if len(cmds) != 1 {
			t.Fatalf("commands = %v, want one", argvsOf(cmds))
		}

		out, code := runCargo(t, cargo, root, target, cmds[0].Argv[1:]...)
		if code != 0 || !strings.Contains(out, "1 passed") {
			t.Errorf("%s exited %d and did not run the test:\n%s", cmds[0], code, out)
		}

		// The form witness emitted before workspaces were understood.
		out, code = runCargo(t, cargo, root, target, "test", "--test", "orders")
		if code == 0 {
			t.Errorf("`cargo test --test orders` resolved after all; -p may no longer be needed:\n%s", out)
		}
		if !strings.Contains(out, "no test target named") {
			t.Logf("cargo rejected the un-scoped form differently than expected:\n%s", out)
		}
	})

	t.Run("unit test in a binary-only member", func(t *testing.T) {
		cmds := mustFormat(t, root, "rust", []string{"crates/cli/src/main.rs"})
		if len(cmds) != 1 {
			t.Fatalf("commands = %v, want one", argvsOf(cmds))
		}

		out, code := runCargo(t, cargo, root, target, cmds[0].Argv[1:]...)
		if code != 0 || !strings.Contains(out, "cli_works") {
			t.Errorf("%s exited %d and did not run cli_works:\n%s", cmds[0], code, out)
		}

		// The false green this replaced: in a workspace with a root package,
		// `cargo test` runs the root package only — zero tests, exit 0.
		out, code = runCargo(t, cargo, root, target, "test")
		if code == 0 && !strings.Contains(out, "cli_works") {
			t.Logf("confirmed: bare `cargo test` exits 0 having run none of the member's tests:\n%s", out)
		} else {
			t.Logf("bare `cargo test` behaved differently here (exit %d):\n%s", code, out)
		}

		// `--lib` is not a substitute for -p: this crate has no library target.
		if _, code := runCargo(t, cargo, root, target, "test", "-p", "acme-cli", "--lib"); code == 0 {
			t.Error("`cargo test --lib` succeeded on a binary-only crate; the comment explaining why witness cannot use it is stale")
		}
	})
}

// runCargo runs cargo in root with an isolated target directory and no network,
// returning its combined output and exit code.
func runCargo(t *testing.T, cargo, root, target string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(cargo, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CARGO_TARGET_DIR="+target, "CARGO_NET_OFFLINE=true")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	default:
		t.Fatalf("running cargo %v: %v", args, err)
		return "", -1
	}
}
