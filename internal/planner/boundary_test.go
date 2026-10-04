package planner

import (
	"github.com/djtouchette/witness/internal/runner"
	"github.com/djtouchette/witness/internal/selector"
	"os"
	"path/filepath"
	"testing"
)

func TestPhysicalOwnershipBoundary(t *testing.T) {
	root := tree(t, map[string]string{"inside/go.mod": "module inside\n", "inside/a_test.go": "package inside"})
	outside := tree(t, map[string]string{"go.mod": "module outside\n", "a_test.go": "package outside"})
	for name, target := range map[string]string{"external": outside, "alias": filepath.Join(root, "inside")} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skip(err)
		}
	}
	for _, f := range []string{"external/a.go", "external/missing/new.go", "external/a_test.go"} {
		for _, selected := range []bool{false, true} {
			s := selection(f)
			if selected {
				s = &selector.SelectResult{ChangedFiles: []string{f}, Tests: []selector.ScoredTest{{Path: "external/a_test.go"}}}
			}
			p, err := Build(root, s)
			if err == nil || p.Status != "incomplete" || len(p.Commands) > 0 {
				t.Errorf("%s selected=%v: %+v %v", f, selected, p, err)
			}
		}
	}
	p, err := Build(root, selection("alias/new.go"))
	if err != nil || p.Status != "ready" {
		t.Fatalf("inside alias: %+v %v", p, err)
	}
}

func TestExecutionRechecksReadyPlan(t *testing.T) {
	root := tree(t, map[string]string{"inside/go.mod": "module inside\n"})
	outside := t.TempDir()
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "inside"), alias); err != nil {
		t.Skip(err)
	}
	p, err := Build(root, selection("alias/new.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	p.Commands[0].Argv = []string{"sh", "-c", "touch SENTINEL"}
	code, err := runner.Execute(p.Commands[0], root, nil, nil)
	if err == nil || code != -1 {
		t.Fatalf("changed alias ran: %d %v", code, err)
	}
	if _, err = os.Stat(filepath.Join(outside, "SENTINEL")); !os.IsNotExist(err) {
		t.Fatal("outside sentinel")
	}
}

func TestUnsafeManifestCannotHideBehindAnotherOwner(t *testing.T) {
	root := tree(t, map[string]string{"go.mod": "module root\n", "nested/a.go": "package nested"})
	outside := tree(t, map[string]string{"go.mod": "module outside\n"})
	if err := os.Symlink(filepath.Join(outside, "go.mod"), filepath.Join(root, "nested/go.mod")); err != nil {
		t.Skip(err)
	}
	p, err := Build(root, selection("nested/a.go"))
	if err == nil || p.Status != "incomplete" {
		t.Fatalf("%+v %v", p, err)
	}
}
