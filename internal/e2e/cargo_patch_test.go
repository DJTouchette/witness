package e2e

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	witness "github.com/djtouchette/witness/pkg/witness"
)

func cargoPatchRepo(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t, "rust-patch")
	if err := os.Symlink("core", r.path("core-alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return r
}
func requirePatchTargets(t *testing.T, p *witness.Plan, err error) {
	t.Helper()
	if err != nil || p == nil {
		t.Fatalf("plan: %+v %v", p, err)
	}
	var got []string
	for _, c := range p.Commands {
		got = append(got, strings.Join(c.Argv, " "))
	}
	want := []string{"cargo test --manifest-path ./Cargo.toml --workspace", "cargo test --manifest-path ./app/Cargo.toml", "cargo test --manifest-path ./core/Cargo.toml"}
	if p.Status != "ready" || p.Coverage != "suites" || !reflect.DeepEqual(got, want) {
		t.Fatalf("lost reverse dependent: %+v", p)
	}
}
func TestCargoPatchPublicPlanningAndAudit(t *testing.T) {
	r := cargoPatchRepo(t)
	sentinel := t.TempDir()
	cargo := filepath.Join(sentinel, "cargo")
	if err := os.WriteFile(cargo, []byte("#!/bin/sh\ntouch '"+filepath.Join(sentinel, "EXECUTED")+"'\nexit 91\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", sentinel+string(os.PathListSeparator)+os.Getenv("PATH"))
	defer func() {
		if _, err := os.Stat(filepath.Join(sentinel, "EXECUTED")); !os.IsNotExist(err) {
			t.Fatal("planning launched Cargo")
		}
	}()
	w, err := witness.New(r.root, witness.WithCacheDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, files := range [][]string{{"core/src/lib.rs"}, {"core-alias/src/lib.rs"}, {"core/src/lib.rs", "core-alias/src/lib.rs"}} {
		p, err := w.Plan(&witness.SelectResult{ChangedFiles: files})
		requirePatchTargets(t, p, err)
	}
	var p *witness.Plan
	res := r.runBinary(t, r.root, "select", "--cache-dir", t.TempDir(), "--format", "plan", "core/src/lib.rs")
	if res.code != 0 {
		t.Fatalf("CLI: %+v", res)
	}
	if err := json.Unmarshal([]byte(res.stdout), &p); err != nil {
		t.Fatal(err)
	}
	requirePatchTargets(t, p, nil)
	res = r.runBinary(t, r.root, "select", "--cache-dir", t.TempDir(), "--format", "exec", "core/src/lib.rs")
	if res.code != 0 || len(nonEmptyLines(res.stdout)) != 3 || !strings.Contains(res.stdout, "./app/Cargo.toml") {
		t.Fatalf("exec: %+v", res)
	}
	audit := filepath.Join(moduleRoot(), "audits", "cargo-patch.json")
	res = r.runBinary(t, r.root, "audit", audit, "--root", r.root)
	if res.code != 0 || strings.Count(res.stdout, `"pass": true`) != 2 {
		t.Fatalf("audit: %+v", res)
	}
	if _, err := os.Stat(r.path("core/BUILD_EXECUTED")); !os.IsNotExist(err) {
		t.Fatal("planning executed Cargo build hook")
	}
	// Direct-path control has exactly the same suites.
	r.write(t, "app/Cargo.toml", strings.ReplaceAll(readFixture(t, r.path("app/Cargo.toml")), `core-fixture = "0.1.0"`, `core-fixture = {path="../core"}`))
	p, err = w.Plan(&witness.SelectResult{ChangedFiles: []string{"core/src/lib.rs"}})
	requirePatchTargets(t, p, err)
}
func TestCargoPatchOfflineExecution(t *testing.T) {
	requireToolchain(t, "cargo", "rust")
	r := cargoPatchRepo(t)
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

func TestCargoUnsupportedOverrideRefusesExecution(t *testing.T) {
	r := cargoPatchRepo(t)
	r.write(t, ".cargo/config.toml", "paths=['core']\n")
	w, err := witness.New(r.root, witness.WithCacheDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	sel := &witness.SelectResult{ChangedFiles: []string{"core/src/lib.rs"}}
	for i := 0; i < 2; i++ {
		p, err := w.Plan(sel)
		if err == nil || p.Status != "incomplete" || p.Coverage != "unknown" || len(p.Commands) != 3 || !strings.Contains(strings.Join(p.Diagnostics, "\n"), "Cargo config override") {
			t.Fatalf("%+v %v", p, err)
		}
		if _, err := w.Commands(sel); err == nil {
			t.Fatal("legacy commands allowed incomplete plan")
		}
		if code, err := w.Run(context.Background(), sel, io.Discard, io.Discard); err == nil || code == 0 {
			t.Fatalf("Run allowed incomplete plan: %d %v", code, err)
		}
	}
	for _, format := range []string{"plan", "exec"} {
		res := r.runBinary(t, r.root, "select", "--cache-dir", t.TempDir(), "--format", format, "core/src/lib.rs")
		if res.code == 0 {
			t.Fatalf("CLI allowed incomplete plan: %+v", res)
		}
		if format == "exec" && strings.TrimSpace(res.stdout) != "" {
			t.Fatalf("exec emitted runnable partial plan: %+v", res)
		}
	}
	res := r.runBinary(t, r.root, "run", "--cache-dir", t.TempDir(), "core/src/lib.rs")
	if res.code == 0 {
		t.Fatalf("CLI executed incomplete plan: %+v", res)
	}
	res = r.runBinary(t, r.root, "audit", filepath.Join(moduleRoot(), "audits", "cargo-unsupported.json"), "--root", r.root)
	if res.code != 0 || !strings.Contains(res.stdout, `"pass": true`) {
		t.Fatalf("audit: %+v", res)
	}
	if _, err := os.Stat(r.path("core/BUILD_EXECUTED")); !os.IsNotExist(err) {
		t.Fatal("incomplete plan executed build hook")
	}
}
