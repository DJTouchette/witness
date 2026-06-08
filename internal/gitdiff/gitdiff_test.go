package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseFiles(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a.go\nb.go\n", []string{"a.go", "b.go"}},
		{"  a.go  \n\n b.go \n", []string{"a.go", "b.go"}},
		{"", nil},
		{"\n\n", nil},
		{"single.go", []string{"single.go"}},
	}
	for _, c := range cases {
		got := parseFiles(c.in)
		if len(got) != len(c.want) {
			t.Errorf("parseFiles(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parseFiles(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestChangedFiles_SinceRefRequiresRef(t *testing.T) {
	if _, err := ChangedFiles(".", SinceRef, ""); err == nil {
		t.Error("SinceRef with empty ref should error")
	}
}

// TestChangedFiles_RealRepo drives ChangedFiles against a real git repo so the
// staged / working-tree / since-ref modes are exercised end to end.
func TestChangedFiles_RealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	write("a.go", "package a\n")
	run("add", "a.go")
	run("commit", "-m", "init")

	// Modify a.go and stage it.
	write("a.go", "package a\n// changed\n")
	run("add", "a.go")

	staged, err := ChangedFiles(dir, Staged, "")
	if err != nil {
		t.Fatalf("staged: %v", err)
	}
	if len(staged) != 1 || staged[0] != "a.go" {
		t.Errorf("staged = %v, want [a.go]", staged)
	}

	// Commit, then add an untracked-then-staged file for a since-ref diff.
	run("commit", "-m", "change a")
	write("b.go", "package b\n")
	run("add", "b.go")
	run("commit", "-m", "add b")

	since, err := ChangedFiles(dir, SinceRef, "HEAD~1")
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(since) != 1 || since[0] != "b.go" {
		t.Errorf("since HEAD~1 = %v, want [b.go]", since)
	}
}
