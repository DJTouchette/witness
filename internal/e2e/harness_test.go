package e2e

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/djtouchette/recon/pkg/recon"
	"github.com/djtouchette/witness/cmd/witness/cli"
)

// update rewrites the golden files instead of comparing against them. `go test
// ./... -update` would fail in every package that does not define the flag, so
// WITNESS_UPDATE_GOLDEN is honoured as well for a repo-wide regeneration.
var update = flag.Bool("update", false, "rewrite the golden files under testdata/golden")

func updateGolden() bool { return *update || os.Getenv("WITNESS_UPDATE_GOLDEN") == "1" }

// pkgDir is this package's directory, captured before any test chdirs into a
// fixture repository. Every testdata path is resolved against it, so a test
// that runs witness from a temp dir still finds its golden file.
var pkgDir string

func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: getwd:", err)
		os.Exit(1)
	}
	pkgDir = wd

	// Hermetic git for the whole test binary, not just for the harness's own
	// invocations: witness shells out to git itself, and it inherits this
	// process's environment. A developer's ~/.gitconfig (core.autocrlf, a
	// rename threshold, a diff driver) or a stray GIT_DIR from a git hook would
	// otherwise change what the tests see.
	for _, kv := range [][2]string{
		{"GIT_CONFIG_GLOBAL", os.DevNull},
		{"GIT_CONFIG_SYSTEM", os.DevNull},
		{"GIT_TERMINAL_PROMPT", "0"},
	} {
		if err := os.Setenv(kv[0], kv[1]); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: setenv %s: %v\n", kv[0], err)
			os.Exit(1)
		}
	}
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR"} {
		if err := os.Unsetenv(name); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: unsetenv %s: %v\n", name, err)
			os.Exit(1)
		}
	}

	code := m.Run()
	// The binary is built lazily and shared by every test, so nothing else can
	// own its lifetime.
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}

func fixturesDir() string { return filepath.Join(pkgDir, "testdata", "fixtures") }
func goldenDir() string   { return filepath.Join(pkgDir, "testdata", "golden") }

// moduleRoot is the witness module root, two levels up from internal/e2e.
func moduleRoot() string { return filepath.Join(pkgDir, "..", "..") }

// repo is a fixture repository: the files from testdata/fixtures/<name> copied
// into a temp directory, committed to a real git repo, with a warm recon index.
//
// The index is warmed on purpose. Whether recon has ever seen a file changes
// the answer for deleted files — a cold index cannot resolve their dependents —
// so a harness that left it to chance would produce a golden that passes or
// fails depending on what ran before it.
type repo struct {
	name string
	root string
}

// newRepo materializes a fixture. Fixtures are checked in as plain files rather
// than built by code so that what witness analyzes is readable in the diff.
func newRepo(t *testing.T, fixture string) *repo {
	t.Helper()
	requireGit(t)

	src := filepath.Join(fixturesDir(), fixture)
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("fixture %q: %v", fixture, err)
	}

	// EvalSymlinks because macOS hands out /var/folders/... temp dirs that git
	// reports back as /private/var/folders/...; without it the repo root and
	// the diff paths disagree and nothing matches.
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatalf("copying fixture %q: %v", fixture, err)
	}

	r := &repo{name: fixture, root: dir}
	r.git(t, "init", "-q")
	r.git(t, "add", "-A")
	r.git(t, "commit", "-q", "-m", "initial commit")
	r.warm(t)
	return r
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed; skipping the end-to-end harness")
	}
}

// git runs git in the fixture repository with an identity of its own and with
// the user's global and system configuration switched off, so a developer's
// ~/.gitconfig (commit.gpgsign, init.defaultBranch, core.autocrlf, a diff
// driver) cannot change what the test sees.
func (r *repo) git(t *testing.T, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=witness e2e",
		"-c", "user.email=e2e@witness.test",
		"-c", "commit.gpgsign=false",
		"-c", "core.autocrlf=false",
		"-c", "core.quotepath=false",
	}, args...)

	cmd := exec.Command("git", full...)
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=true",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// warm builds recon's index for the repository as it stands, the way a
// developer's working copy would already have one.
func (r *repo) warm(t *testing.T) {
	t.Helper()
	rec, err := recon.New(r.root)
	if err != nil {
		t.Fatalf("warming the recon index for %s: %v", r.name, err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("closing recon for %s: %v", r.name, err)
	}
}

func (r *repo) path(name string) string { return filepath.Join(r.root, filepath.FromSlash(name)) }

func (r *repo) write(t *testing.T, name, body string) {
	t.Helper()
	path := r.path(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func (r *repo) append(t *testing.T, name, body string) {
	t.Helper()
	f, err := os.OpenFile(r.path(name), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("append %s: %v", name, err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatalf("append %s: %v", name, err)
	}
}

func (r *repo) remove(t *testing.T, name string) {
	t.Helper()
	if err := os.Remove(r.path(name)); err != nil {
		t.Fatalf("remove %s: %v", name, err)
	}
}

func (r *repo) commit(t *testing.T, msg string) {
	t.Helper()
	r.git(t, "add", "-A")
	r.git(t, "commit", "-q", "-m", msg)
}

// reset returns the working tree to HEAD so one fixture can serve several
// cases. `git clean -fd` (without -x) leaves the ignored .recon cache alone,
// which is what keeps the index warm between cases.
func (r *repo) reset(t *testing.T) {
	t.Helper()
	r.git(t, "checkout", "--", ".")
	r.git(t, "clean", "-fdq")
}

// result is one witness invocation: everything a caller could observe, exit
// code included. The code is part of it because witness's whole product claim
// is about which exit code an unprovable selection gets.
type result struct {
	args   []string
	stdout string
	stderr string
	err    error
	code   int
}

// run drives the real cobra command tree from the repository root, capturing
// stdout and stderr through SetOut/SetErr the way an embedding host does.
func (r *repo) run(t *testing.T, args ...string) result {
	t.Helper()
	return r.runFrom(t, r.root, args...)
}

// runFrom is run with an explicit working directory, for the tests that check
// witness answers the same from a subdirectory.
func (r *repo) runFrom(t *testing.T, dir string, args ...string) result {
	t.Helper()
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	root := cli.NewRootCmd("v-e2e")
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()

	return result{
		args:   args,
		stdout: stdout.String(),
		stderr: stderr.String(),
		err:    err,
		code:   exitCodeFor(err),
	}
}

// exitCodeFor mirrors cmd/witness/main.go: an ExitCodeError carries the test
// runner's own code, every other error is 1, and nil is 0. The mirror is
// checked against the real binary in TestBinaryAgreesWithTheInProcessCommand.
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var ce *cli.ExitCodeError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return 1
}

var (
	binOnce sync.Once
	binDir  string
	binPath string
	binErr  error
)

// witnessBinary builds cmd/witness once per test run and returns its path. The
// in-process command tree cannot prove what a shell sees, so the exit-code
// tests go through the real executable.
func witnessBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		if _, err := exec.LookPath("go"); err != nil {
			binErr = fmt.Errorf("go toolchain not installed: %w", err)
			return
		}
		binDir, binErr = os.MkdirTemp("", "witness-e2e-bin")
		if binErr != nil {
			return
		}
		binPath = filepath.Join(binDir, "witness")
		if _, err := os.Stat(filepath.Join(moduleRoot(), "go.mod")); err != nil {
			binErr = fmt.Errorf("locating the module root: %w", err)
			return
		}
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/witness")
		cmd.Dir = moduleRoot()
		if out, err := cmd.CombinedOutput(); err != nil {
			binErr = fmt.Errorf("go build ./cmd/witness: %w\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatalf("building the witness binary: %v", binErr)
	}
	return binPath
}

// runBinary executes the built binary in dir and reports the process's real
// exit status.
func (r *repo) runBinary(t *testing.T, dir string, args ...string) result {
	t.Helper()
	bin := witnessBinary(t)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		// The harness is offline by contract, and the fixtures have no
		// dependencies — so a test runner that reaches for the network is a bug
		// in the fixture, not something to wait 30s for.
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"CARGO_NET_OFFLINE=true",
	)

	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s %s: %v", bin, strings.Join(args, " "), err)
	}
	return result{args: args, stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// --- golden files -------------------------------------------------------------

// transcript renders an invocation as the golden document: the command, its
// exit code, and both streams, all normalized.
func (r *repo) transcript(res result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "$ witness %s\n", strings.Join(res.args, " "))
	fmt.Fprintf(&b, "exit %d\n", res.code)
	fmt.Fprintf(&b, "--- stdout ---\n%s", section(r.normalize(res.stdout)))
	fmt.Fprintf(&b, "--- stderr ---\n%s", section(r.normalize(res.stderr)))
	if res.err != nil {
		// What main() would print before exiting; a golden that loses this line
		// is a gate that stopped explaining itself.
		fmt.Fprintf(&b, "--- error ---\n%s", section(r.normalize(res.err.Error())))
	}
	return r.normalize(b.String())
}

// section renders a stream, using an explicit marker for an empty one so that
// "printed nothing" cannot be mistaken for "section missing".
func section(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(empty)\n"
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

// normalize strips everything that differs between two machines running the
// same test: the temp directory the fixture landed in, Windows line endings,
// and trailing whitespace. A golden file that only passes on the machine that
// generated it is worth nothing.
func (r *repo) normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")

	// Longest first: the repo root is usually inside the temp dir, and
	// replacing the temp dir first would leave a half-rewritten path.
	replacements := []string{r.root, filepath.ToSlash(r.root)}
	for _, p := range replacements {
		if p != "" {
			s = strings.ReplaceAll(s, p, "<repo>")
		}
	}
	for _, tmp := range tempDirs() {
		s = strings.ReplaceAll(s, tmp, "<tmp>")
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

// tempDirs lists the spellings of the system temp directory that can show up in
// output: macOS hands out /var/folders/... but reports /private/var/folders/...
func tempDirs() []string {
	tmp := strings.TrimRight(os.TempDir(), string(filepath.Separator))
	dirs := []string{}
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil && resolved != tmp {
		dirs = append(dirs, resolved, filepath.ToSlash(resolved))
	}
	return append(dirs, tmp, filepath.ToSlash(tmp))
}

// checkGolden compares an invocation against testdata/golden/<name>.golden,
// or rewrites it under -update.
func (r *repo) checkGolden(t *testing.T, name string, res result) {
	t.Helper()
	got := r.transcript(res)
	path := filepath.Join(goldenDir(), name+".golden")

	if updateGolden() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v\nrun `go test ./internal/e2e -update` to create it\ngot:\n%s", path, err, got)
	}
	if got != string(want) {
		t.Errorf("witness output changed for %s\n%s\nif this change is intended, run `go test ./internal/e2e -update` and review the diff",
			name, diffLines(string(want), got))
	}
}

// diffLines renders a line diff of two transcripts, marking removed lines with
// "-" and added ones with "+". Pairing the two texts line by line instead would
// report every line after an insertion as changed, which buries the one line
// that actually moved — and finding that line is the whole point of a golden.
func diffLines(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")

	// Longest common subsequence table; the transcripts are tens of lines, so
	// the quadratic table costs nothing.
	lcs := make([][]int, len(wantLines)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(gotLines)+1)
	}
	for i := len(wantLines) - 1; i >= 0; i-- {
		for j := len(gotLines) - 1; j >= 0; j-- {
			if wantLines[i] == gotLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var b strings.Builder
	b.WriteString("--- want / +++ got ---\n")
	i, j := 0, 0
	for i < len(wantLines) && j < len(gotLines) {
		switch {
		case wantLines[i] == gotLines[j]:
			b.WriteString("  " + wantLines[i] + "\n")
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			b.WriteString("- " + wantLines[i] + "\n")
			i++
		default:
			b.WriteString("+ " + gotLines[j] + "\n")
			j++
		}
	}
	for ; i < len(wantLines); i++ {
		b.WriteString("- " + wantLines[i] + "\n")
	}
	for ; j < len(gotLines); j++ {
		b.WriteString("+ " + gotLines[j] + "\n")
	}
	return b.String()
}
