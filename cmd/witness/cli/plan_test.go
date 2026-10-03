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
