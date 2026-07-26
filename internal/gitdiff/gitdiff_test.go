package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo is a scratch git repository backing the real-git tests.
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init")
	r.git("config", "user.email", "t@example.com")
	r.git("config", "user.name", "t")
	r.git("config", "commit.gpgsign", "false")
	return r
}

// git runs git in the repository root and fails the test on error.
func (r *repo) git(args ...string) string {
	r.t.Helper()
	return r.gitIn(r.dir, args...)
}

func (r *repo) gitIn(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (r *repo) write(name, body string) {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) commit(msg string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-m", msg)
}

// changed is a convenience wrapper that fails the test on error.
func (r *repo) changed(dir string, mode Mode, ref string) []string {
	r.t.Helper()
	files, err := ChangedFiles(dir, mode, ref)
	if err != nil {
		r.t.Fatalf("ChangedFiles(%q, %v, %q): %v", dir, mode, ref, err)
	}
	return files
}

func wantFiles(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %q, want %q", label, got, want)
		}
	}
}

func TestSplitNUL(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a.go\x00b.go\x00", []string{"a.go", "b.go"}},
		{"", nil},
		{"\x00\x00", nil},
		{"single.go", []string{"single.go"}},
		// Spaces are legal in filenames and must survive verbatim.
		{" lead.go\x00trail.go \x00", []string{" lead.go", "trail.go "}},
		// -z output is not C-quoted, so non-ASCII arrives as raw bytes.
		{"café.go\x00", []string{"café.go"}},
	}
	for _, c := range cases {
		got := splitNUL(c.in)
		wantFiles(t, "splitNUL("+strings.ReplaceAll(c.in, "\x00", "|")+")", got, c.want)
	}
}

func TestParseNameStatus(t *testing.T) {
	// A rename, a deletion and a modification, exactly as git -z emits them.
	in := "R100\x00old.go\x00new.go\x00D\x00gone.go\x00M\x00kept.go\x00"
	want := []Change{
		{Path: "old.go", Status: StatusDeleted},
		{Path: "new.go", Status: StatusRenamed},
		{Path: "gone.go", Status: StatusDeleted},
		{Path: "kept.go", Status: StatusModified},
	}
	got := parseNameStatus(in)
	if len(got) != len(want) {
		t.Fatalf("parseNameStatus = %+v, want %+v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("parseNameStatus[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	// Truncated records must not panic.
	if got := parseNameStatus("M\x00"); got != nil {
		t.Errorf("parseNameStatus(truncated) = %+v, want nil", got)
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
	r := newRepo(t)
	r.write("a.go", "package a\n")
	r.commit("init")

	// Modify a.go and stage it.
	r.write("a.go", "package a\n// changed\n")
	r.git("add", "a.go")

	wantFiles(t, "staged", r.changed(r.dir, Staged, ""), []string{"a.go"})
	wantFiles(t, "working tree", r.changed(r.dir, WorkingTree, ""), []string{"a.go"})

	// Commit, then add a new file for a since-ref diff.
	r.git("commit", "-m", "change a")
	r.write("b.go", "package b\n")
	r.commit("add b")

	wantFiles(t, "since HEAD~1", r.changed(r.dir, SinceRef, "HEAD~1"), []string{"b.go"})
}

// TestChangedFiles_Deletions covers the fail-open case: a deleted file used to
// be filtered out by --diff-filter=ACMR, so a deletion-only commit reported no
// changes at all and witness exited 0 while every dependent was broken.
func TestChangedFiles_Deletions(t *testing.T) {
	r := newRepo(t)
	r.write("a.go", "package a\n")
	r.write("doomed.go", "package a\n\nfunc Doomed() {}\n")
	r.commit("init")

	// Unstaged deletion -> working tree mode.
	if err := os.Remove(filepath.Join(r.dir, "doomed.go")); err != nil {
		t.Fatal(err)
	}
	wantFiles(t, "working tree deletion", r.changed(r.dir, WorkingTree, ""), []string{"doomed.go"})

	// Staged deletion -> staged mode.
	r.git("add", "-A")
	wantFiles(t, "staged deletion", r.changed(r.dir, Staged, ""), []string{"doomed.go"})

	changes, err := ChangedFilesDetailed(r.dir, Staged, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !changes[0].Deleted() {
		t.Fatalf("detailed staged = %+v, want one deleted change", changes)
	}

	// Committed deletion -> since-ref mode.
	r.git("commit", "-m", "delete doomed")
	wantFiles(t, "since deletion", r.changed(r.dir, SinceRef, "HEAD~1"), []string{"doomed.go"})
}

// TestChangedFiles_Rename asserts a rename yields both sides: the old path is
// gone (its dependents break) and the new path is new.
func TestChangedFiles_Rename(t *testing.T) {
	r := newRepo(t)
	r.write("old.go", "package a\n\nfunc Thing() int { return 42 }\n")
	r.commit("init")

	r.git("mv", "old.go", "new.go")

	wantFiles(t, "staged rename", r.changed(r.dir, Staged, ""), []string{"new.go", "old.go"})

	changes, err := ChangedFilesDetailed(r.dir, Staged, "")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, c := range changes {
		byPath[c.Path] = c.Status
	}
	if byPath["old.go"] != StatusDeleted {
		t.Errorf("old.go status = %q, want %q", byPath["old.go"], StatusDeleted)
	}
	if byPath["new.go"] != StatusRenamed {
		t.Errorf("new.go status = %q, want %q", byPath["new.go"], StatusRenamed)
	}
}

// TestChangedFiles_Untracked covers new files that have not been staged yet:
// git diff never reports them, so witness saw an empty change set.
func TestChangedFiles_Untracked(t *testing.T) {
	r := newRepo(t)
	r.write("a.go", "package a\n")
	r.commit("init")

	r.write("fresh.go", "package a\n")
	r.write("sub/fresh_test.go", "package sub\n")
	r.write("ignored.go", "package a\n")
	r.write(".gitignore", "ignored.go\n")

	wantFiles(t, "untracked", r.changed(r.dir, WorkingTree, ""),
		[]string{".gitignore", "fresh.go", "sub/fresh_test.go"})

	// The index is empty, so staged mode must stay empty.
	if got := r.changed(r.dir, Staged, ""); len(got) != 0 {
		t.Errorf("staged = %q, want none", got)
	}
}

// TestChangedFiles_UntrackedNestedRepoIsNotAFile pins the one shape
// `git ls-files --others` does not recurse into: an untracked directory that is
// itself a git repository comes back as the DIRECTORY, "vendorlib/". Feeding
// that to the selector as a changed file maps it to no test, which trips the
// fail-closed fallback — so a repo containing one nested clone ran its entire
// suite on every invocation, forever.
func TestChangedFiles_UntrackedNestedRepoIsNotAFile(t *testing.T) {
	r := newRepo(t)
	r.write("a.go", "package a\n")
	r.commit("init")

	nested := filepath.Join(r.dir, "vendorlib")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	r.gitIn(nested, "init")
	r.write("vendorlib/lib.go", "package lib\n")
	r.write("fresh.go", "package a\n")

	got := r.changed(r.dir, WorkingTree, "")
	wantFiles(t, "nested repo", got, []string{"fresh.go"})
	for _, path := range got {
		if strings.HasSuffix(path, "/") {
			t.Errorf("changed files contain the directory %q; a directory is not a file the selector can map", path)
		}
	}
}

// TestChangedFiles_Subdirectory pins the path space: git reports paths relative
// to the repository root, so ChangedFiles must too even when handed a
// subdirectory, and RepoRoot must tell the caller where that root is.
func TestChangedFiles_Subdirectory(t *testing.T) {
	r := newRepo(t)
	r.write("sub/math.go", "package sub\n")
	r.write("sub/math_test.go", "package sub\n")
	r.commit("init")

	r.write("sub/math.go", "package sub\n// changed\n")
	r.write("sub/untracked.go", "package sub\n")

	sub := filepath.Join(r.dir, "sub")
	wantFiles(t, "from subdir", r.changed(sub, WorkingTree, ""),
		[]string{"sub/math.go", "sub/untracked.go"})

	root, err := RepoRoot(sub)
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	// t.TempDir may hand back a symlinked path; compare resolved forms.
	wantRoot, err := filepath.EvalSymlinks(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	gotRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != wantRoot {
		t.Errorf("RepoRoot(%q) = %q, want %q", sub, gotRoot, wantRoot)
	}
}

// TestChangedFiles_SpecialCharacterPaths covers git's C-quoting: without -z a
// modified café.go comes back as the literal string "caf\303\251.go", which
// matches no file on disk and silently selects no tests.
func TestChangedFiles_SpecialCharacterPaths(t *testing.T) {
	r := newRepo(t)
	names := []string{"café.go", "with space.go", "naïve_test.go"}
	for _, n := range names {
		r.write(n, "package a\n")
	}
	r.commit("init")
	for _, n := range names {
		r.write(n, "package a\n// changed\n")
	}

	got := r.changed(r.dir, WorkingTree, "")
	wantFiles(t, "special characters", got, []string{"café.go", "naïve_test.go", "with space.go"})
	for _, f := range got {
		if strings.Contains(f, `\`) || strings.HasPrefix(f, `"`) {
			t.Errorf("path %q is C-quoted", f)
		}
		if _, err := os.Stat(filepath.Join(r.dir, f)); err != nil {
			t.Errorf("reported path %q does not exist: %v", f, err)
		}
	}
}

// TestChangedFiles_NoCommits covers an unborn HEAD: the old fallback diffed the
// index against the working tree, which is empty for freshly staged files, so a
// brand-new repository reported no changes at all.
func TestChangedFiles_NoCommits(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module scratch\n")
	r.write("calc/calc.go", "package calc\n")
	r.write("calc/calc_test.go", "package calc\n")
	r.git("add", "-A")

	want := []string{"calc/calc.go", "calc/calc_test.go", "go.mod"}
	wantFiles(t, "unborn HEAD working tree", r.changed(r.dir, WorkingTree, ""), want)
	wantFiles(t, "unborn HEAD staged", r.changed(r.dir, Staged, ""), want)
}

// TestChangedFiles_NoCommitsUntracked is the same unborn-HEAD repo with nothing
// staged at all.
func TestChangedFiles_NoCommitsUntracked(t *testing.T) {
	r := newRepo(t)
	r.write("main.go", "package main\n")

	wantFiles(t, "unborn HEAD untracked", r.changed(r.dir, WorkingTree, ""), []string{"main.go"})
}

// TestChangedFiles_ErrorsIncludeGitStderr keeps failures loud: a bad ref used to
// surface as a bare "exit status 128".
func TestChangedFiles_ErrorsIncludeGitStderr(t *testing.T) {
	r := newRepo(t)
	r.write("a.go", "package a\n")
	r.commit("init")

	_, err := ChangedFiles(r.dir, SinceRef, "no-such-ref")
	if err == nil {
		t.Fatal("bad ref should error")
	}
	if !strings.Contains(err.Error(), "no-such-ref") {
		t.Errorf("error %q does not name the bad ref", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "fatal") {
		t.Errorf("error %q does not carry git's stderr", err)
	}
}

// TestChangedFiles_NotAGitRepo must fail closed rather than report no changes.
func TestChangedFiles_NotAGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	// Guard against the temp dir living inside somebody's repository.
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		t.Skipf("temp dir is inside a git repo: %s", strings.TrimSpace(string(out)))
	}
	if _, err := ChangedFiles(dir, WorkingTree, ""); err == nil {
		t.Fatal("ChangedFiles outside a git repo should error, not report zero changes")
	}
}
