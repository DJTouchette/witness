package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/djtouchette/witness/internal/planner"
)

func TestPlanAndExecNeverExecuteConfiguredCommands(t *testing.T) {
	root := newTestRepo(t)
	t.Chdir(root)
	writeFile(t, root, ".witness.json", `{"schema_version":1,"suites":[{"name":"sentinel","paths":["calc.go"],"cwd":".","argv":["sh","-c","touch SENTINEL"]}]}`)
	for _, format := range []string{"plan", "exec"} {
		var out, stderr bytes.Buffer
		cmd := NewRootCmd("test")
		cmd.SetOut(&out)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"select", "--format", format, "--cache-dir", t.TempDir(), "calc.go"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s: %v %s", format, err, stderr.String())
		}
		if out.Len() == 0 {
			t.Fatal("empty output")
		}
		if format == "plan" {
			var p planner.Plan
			if err := json.Unmarshal(out.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			if p.SchemaVersion != 1 || p.Status != "ready" || len(p.Commands) != 1 {
				t.Fatalf("%+v", p)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "SENTINEL")); !os.IsNotExist(err) {
			t.Fatal("planning executed a configured command")
		}
	}
}
func TestIncompletePlanStillWritesMachineResult(t *testing.T) {
	root := newTestRepo(t)
	t.Chdir(root)
	var out, stderr bytes.Buffer
	cmd := NewRootCmd("test")
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"select", "--format", "plan", "--cache-dir", t.TempDir(), "unknown.xyz"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("incomplete plan succeeded")
	}
	var p planner.Plan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Status != "incomplete" || len(p.Diagnostics) == 0 {
		t.Fatalf("%+v", p)
	}
}

func TestRustInlineNewDeletedAndRenamedFilesUseFreshIndex(t *testing.T) {
	root := newTestRepo(t)
	t.Chdir(root)
	cache := t.TempDir()
	writeFile(t, root, "native/Cargo.toml", "[package]\nname='native'\nversion='0.1.0'\n")
	writeFile(t, root, "native/src/old.rs", "#[cfg(test)]\nmod tests {}\n")
	plan := func(files ...string) planner.Plan {
		t.Helper()
		var out, stderr bytes.Buffer
		cmd := NewRootCmd("test")
		cmd.SetOut(&out)
		cmd.SetErr(&stderr)
		cmd.SetArgs(append([]string{"select", "--format", "plan", "--cache-dir", cache}, files...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v %s", err, stderr.String())
		}
		var p planner.Plan
		if err := json.Unmarshal(out.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if len(p.Commands) != 1 || p.Commands[0].Argv[0] != "cargo" || p.Commands[0].Argv[3] != "./native/Cargo.toml" {
			t.Fatalf("%+v", p)
		}
		return p
	}
	p := plan("native/src/old.rs")
	if len(p.Selection.Tests) != 1 || p.Selection.Tests[0].Path != "native/src/old.rs" {
		t.Fatalf("inline test missing: %+v", p.Selection)
	}
	if err := os.Rename(filepath.Join(root, "native/src/old.rs"), filepath.Join(root, "native/src/new.rs")); err != nil {
		t.Fatal(err)
	}
	p = plan("native/src/old.rs", "native/src/new.rs")
	if len(p.Selection.Summary.NotIndexed) != 1 || p.Selection.Summary.NotIndexed[0] != "native/src/old.rs" {
		t.Fatalf("deleted file disappeared: %+v", p.Selection)
	}
	found := false
	for _, test := range p.Selection.Tests {
		if test.Path == "native/src/new.rs" {
			found = true
		}
	}
	if !found {
		t.Fatal("new inline test missed after cache refresh")
	}
}

func TestMixedNoArgsAndFallbackPlan(t *testing.T) {
	root := newTestRepo(t)
	t.Chdir(root)
	writeFile(t, root, "python/pyproject.toml", "[project]\nname='fixture'\n")
	writeFile(t, root, "python/new.py", "def new(): pass\n")
	writeFile(t, root, "new.go", "package calc\n")
	for _, args := range [][]string{nil, {"new.go", "python/new.py"}, {"new.go", "python/new.py", "unknown.xyz"}} {
		var out bytes.Buffer
		cmd := NewRootCmd("test")
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(append([]string{"select", "--format", "plan", "--fallback", "full", "--cache-dir", t.TempDir()}, args...))
		err := cmd.Execute()
		var p planner.Plan
		if e := json.Unmarshal(out.Bytes(), &p); e != nil {
			t.Fatalf("%v %s", e, out.String())
		}
		if len(p.Commands) != 2 {
			t.Fatalf("lost mixed suites: %+v %v", p, err)
		}
		if len(args) == 3 {
			if err == nil || p.Status != "incomplete" {
				t.Fatalf("%+v %v", p, err)
			}
		} else if err != nil || p.Status != "ready" {
			t.Fatalf("%+v %v", p, err)
		}
	}
}

func TestMappedScriptRetainsArgvAndPassThrough(t *testing.T) {
	root := newTestRepo(t)
	t.Chdir(root)
	writeFile(t, root, "package.json", `{"scripts":{"test":"node 'wrapper with spaces.js'","pretest":"node setup.js"},"devDependencies":{"vitest":"4"}}`)
	writeFile(t, root, ".witness.json", `{"schema_version":1,"suites":[{"name":"script","paths":["src/**"],"cwd":".","argv":["npm","run","test","--"]}]}`)
	var out bytes.Buffer
	cmd := NewRootCmd("test")
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"select", "--format", "exec", "--cache-dir", t.TempDir(), "src/a.ts", "--", "argument with spaces"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "npm run test -- 'argument with spaces'\n" {
		t.Fatalf("argv mangled: %q", out.String())
	}
}

func TestAuditRequiresCoverageAndDiagnosticEvidence(t *testing.T) {
	root := newTestRepo(t)
	for _, want := range []string{"no validated test-suite owner", "invented diagnostic"} {
		cases := []auditCase{{Name: "unknown", Root: root, Files: []string{"unknown.xyz"}, ExpectedStatus: "incomplete", ExpectedCoverage: "unknown", ExpectedDiagnostics: []string{want}}}
		b, _ := json.Marshal(cases)
		file := filepath.Join(t.TempDir(), "cases.json")
		if err := os.WriteFile(file, b, 0644); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		cmd := NewRootCmd("test")
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"audit", file})
		err := cmd.Execute()
		if (err == nil) != (want == "no validated test-suite owner") {
			t.Fatalf("%s: %v %s", want, err, out.String())
		}
	}
}
