package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	witness "github.com/djtouchette/witness/pkg/witness"
)

func cargoAliasRepo(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t, "rust-alias")
	if err := os.Symlink("core", r.path("core-alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return r
}
func requireAliasTargets(t *testing.T, p *witness.Plan, err error) {
	t.Helper()
	if err != nil || p == nil {
		t.Fatalf("plan: %+v %v", p, err)
	}
	var got []string
	for _, c := range p.Commands {
		got = append(got, strings.Join(c.Argv, " "))
	}
	want := []string{"cargo test --manifest-path ./app/Cargo.toml", "cargo test --manifest-path ./core/Cargo.toml"}
	if p.Status != "ready" || p.Coverage != "suites" || !reflect.DeepEqual(got, want) {
		t.Fatalf("lost reverse dependent: %+v", p)
	}
}
func TestCargoAliasPublicPlanningAndAudit(t *testing.T) {
	r := cargoAliasRepo(t)
	w, err := witness.New(r.root, witness.WithCacheDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, files := range [][]string{{"core/src/lib.rs"}, {"core-alias/src/lib.rs"}, {"core/src/lib.rs", "core-alias/src/lib.rs"}} {
		p, err := w.Plan(&witness.SelectResult{ChangedFiles: files})
		requireAliasTargets(t, p, err)
	}
	var p *witness.Plan
	res := r.runBinary(t, r.root, "select", "--cache-dir", t.TempDir(), "--format", "plan", "core/src/lib.rs")
	if res.code != 0 {
		t.Fatalf("CLI: %+v", res)
	}
	if err := json.Unmarshal([]byte(res.stdout), &p); err != nil {
		t.Fatal(err)
	}
	requireAliasTargets(t, p, nil)
	res = r.runBinary(t, r.root, "select", "--cache-dir", t.TempDir(), "--format", "exec", "core/src/lib.rs")
	if res.code != 0 || len(nonEmptyLines(res.stdout)) != 2 || !strings.Contains(res.stdout, "./app/Cargo.toml") {
		t.Fatalf("exec: %+v", res)
	}
	audit := filepath.Join(moduleRoot(), "audits", "cargo-alias.json")
	res = r.runBinary(t, r.root, "audit", audit, "--root", r.root)
	if res.code != 0 || strings.Count(res.stdout, `"pass": true`) != 2 {
		t.Fatalf("audit: %+v", res)
	}
	if _, err := os.Stat(r.path("core/BUILD_EXECUTED")); !os.IsNotExist(err) {
		t.Fatal("planning executed Cargo build hook")
	}
	// Direct-path control has exactly the same suites.
	r.write(t, "app/Cargo.toml", strings.ReplaceAll(readFixture(t, r.path("app/Cargo.toml")), "../core-alias", "../core"))
	p, err = w.Plan(&witness.SelectResult{ChangedFiles: []string{"core/src/lib.rs"}})
	requireAliasTargets(t, p, err)
}
func readFixture(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func TestCargoAliasOfflineExecution(t *testing.T) {
	requireToolchain(t, "cargo", "rust")
	r := cargoAliasRepo(t)
	t.Setenv("CARGO_NET_OFFLINE", "true")
	t.Setenv("CARGO_TARGET_DIR", t.TempDir())
	// The old selection passes, while the omitted dependent actually fails.
	for _, tc := range []struct {
		manifest string
		code     int
	}{{"core", 0}, {"app", 101}} {
		cmd := exec.Command("cargo", "test", "--offline", "--manifest-path", r.path(tc.manifest+"/Cargo.toml"))
		cmd.Dir = r.root
		b, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != tc.code {
			t.Fatalf("%s code %d, want %d: %s", tc.manifest, code, tc.code, b)
		}
		if tc.manifest == "app" && !strings.Contains(string(b), "observes_core_value ... FAILED") {
			t.Fatalf("dependent test did not run: %s", b)
		}
	}
	fail := r.runBinary(t, r.root, "run", "core/src/lib.rs")
	if fail.code != 101 || !strings.Contains(fail.stdout+fail.stderr, "observes_core_value ... FAILED") {
		t.Fatalf("failing dependent lost: %+v", fail)
	}
	r.write(t, "core/src/lib.rs", "pub fn value() -> i32 { 1 }\n")
	pass := r.runBinary(t, r.root, "run", "core/src/lib.rs")
	if pass.code != 0 || !strings.Contains(pass.stdout+pass.stderr, "observes_core_value ... ok") {
		t.Fatalf("fixed dependent did not pass: %+v", pass)
	}
}

// Cargo normalizes dot-dot lexically, but a symlinked Cargo.toml keeps the
// containing crate directory. Verify both with actual offline Cargo, not just
// planner expectations that could encode the same wrong assumption.
func TestCargoAliasPathSemanticsOffline(t *testing.T) {
	requireToolchain(t, "cargo", "rust")
	for _, kind := range []string{"parent components", "manifest file"} {
		t.Run(kind, func(t *testing.T) {
			r := cargoAliasRepo(t)
			t.Setenv("CARGO_NET_OFFLINE", "true")
			t.Setenv("CARGO_TARGET_DIR", t.TempDir())
			r.write(t, "nested/core/src/lib.rs", "pub fn value() -> i32 { 1 }\n")
			r.write(t, "nested/core/Cargo.toml", readFixture(t, r.path("core/Cargo.toml")))
			changed := "core/src/lib.rs"
			dependency := "../deep-alias/../core"
			if kind == "parent components" {
				r.write(t, "nested/deep/placeholder", "")
				if err := os.Symlink("nested/deep", r.path("deep-alias")); err != nil {
					t.Fatal(err)
				}
			} else {
				// Shared manifest contents, different sources. Do not deduplicate the
				// core suite (value=2) with the nested/core suite (value=1).
				r.remove(t, "nested/core/Cargo.toml")
				if err := os.Symlink("../../core/Cargo.toml", r.path("nested/core/Cargo.toml")); err != nil {
					t.Fatal(err)
				}
				dependency = "../nested/core"
				changed = "nested/core/src/lib.rs"
			}
			r.write(t, "app/Cargo.toml", strings.ReplaceAll(readFixture(t, r.path("app/Cargo.toml")), "../core-alias", dependency))
			w, err := witness.New(r.root, witness.WithCacheDir(t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			p, err := w.Plan(&witness.SelectResult{ChangedFiles: []string{changed}})
			if err != nil || len(p.Commands) != 2 || p.Commands[0].Argv[3] != "./app/Cargo.toml" {
				t.Fatalf("lost app: %+v %v", p, err)
			}
			got := r.runBinary(t, r.root, "run", changed)
			wantCode, wantTest := 101, "observes_core_value ... FAILED"
			if kind == "manifest file" {
				wantCode, wantTest = 0, "observes_core_value ... ok"
			}
			if got.code != wantCode || !strings.Contains(got.stdout+got.stderr, wantTest) {
				t.Fatalf("Cargo semantics: %+v", got)
			}
		})
	}
}
