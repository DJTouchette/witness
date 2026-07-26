package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/djtouchette/recon/pkg/recon"
	"github.com/djtouchette/witness/internal/gitdiff"
	"github.com/djtouchette/witness/internal/runner"
	"github.com/djtouchette/witness/internal/selector"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ExitCodeError carries a test-runner exit code out of `run` so the standalone
// main() can exit with it. The command itself must NOT call os.Exit: witness is
// embedded in-process by other tools (e.g. Rivet), where os.Exit would kill the
// host. main() translates this into os.Exit; embedded callers see a normal
// non-nil error.
type ExitCodeError struct{ Code int }

func (e *ExitCodeError) Error() string { return fmt.Sprintf("tests failed (exit code %d)", e.Code) }

// Output formats accepted by `select --format`.
const (
	formatJSON  = "json"
	formatPaths = "paths"
	formatExec  = "exec"
)

var outputFormats = []string{formatJSON, formatPaths, formatExec}

// Fallback policies for a selection witness cannot prove is complete — see
// unprovenReasons for what "cannot prove" means.
const (
	// fallbackFull runs the project's entire test suite instead of a selection
	// witness cannot vouch for. It is the default: witness is advertised as a
	// CI gate, and a gate that cannot prove which tests cover a change must run
	// all of them rather than report a pass it did not earn.
	fallbackFull = "full"
	// fallbackFail exits non-zero without running anything.
	fallbackFail = "fail"
	// fallbackNone continues with the partial selection (possibly empty),
	// warning on stderr. Appropriate for a pre-commit hook, where a docs-only
	// commit must not drag in the whole suite.
	fallbackNone = "none"
)

var fallbackModes = []string{fallbackFull, fallbackFail, fallbackNone}

// NewRootCmd creates the witness CLI command tree.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "witness",
		Short: "Test selector — find which tests to run for changed files",
		Long:  "Witness maps code changes to relevant tests using dependency analysis, co-change history, and hotspot scoring.",
		// run returns ExitCodeError to pass the runner's code up; don't let
		// cobra print it or dump usage for a plain test failure.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	root.Version = version
	root.AddCommand(newSelectCmd(), newRunCmd())

	return root
}

// selectFlags are the knobs shared by `select` and `run`.
type selectFlags struct {
	depth     int
	minScore  float64
	maxTests  int
	staged    bool
	since     string
	cacheDir  string
	coChange  int
	fanOutCap int
	exclude   []string
	kinds     []string
	signals   []string
	fallback  string
	testCmd   string
	runnerCmd string
	timeout   time.Duration

	// requireCoverage turns every changed file no selected test covers into a
	// gap, not just the case where nothing at all was selected.
	requireCoverage bool

	// explicit records which flags the user actually typed. Cobra only knows
	// this after parsing, so capture fills it in PreRunE. It exists so an
	// explicit zero can be told apart from an unset flag — see options.
	explicit map[string]bool
}

func (sf *selectFlags) bind(cmd *cobra.Command) {
	f := cmd.Flags()
	f.IntVar(&sf.depth, "depth", 2, "import graph traversal depth (0 = direct tests only)")
	f.Float64Var(&sf.minScore, "min-score", 0.1, "minimum relevance score (0 = no minimum)")
	f.IntVar(&sf.maxTests, "max", 50, "max tests to return (0 = no cap)")
	f.BoolVar(&sf.staged, "staged", false, "use git diff --staged")
	f.StringVar(&sf.since, "since", "", "use git diff <ref>...HEAD")
	f.StringVar(&sf.cacheDir, "cache-dir", "", "recon cache directory; a relative path is resolved against the repository root, not the current directory")
	f.IntVar(&sf.coChange, "co-change-min", 2, "minimum co-change count to consider (0 = no minimum)")
	f.IntVar(&sf.fanOutCap, "fan-out-cap", 100, "skip files with more importers than this (0 = no cap)")
	f.StringSliceVar(&sf.exclude, "exclude", nil, "glob patterns of test paths to drop (repeatable)")
	f.StringSliceVar(&sf.kinds, "kind", nil, "only return these test kinds: unit, integration, e2e, ... (repeatable)")
	f.StringSliceVar(&sf.signals, "signals", nil, "only return tests found by these signals: direct-test, changed-test, import, co-change, hotspot-risk (repeatable)")
	f.StringVar(&sf.fallback, "fallback", fallbackFull, "when witness cannot prove the selection is complete: full (whole suite), fail (exit non-zero), none (use it anyway)")
	f.BoolVar(&sf.requireCoverage, "require-coverage", false, "treat every changed file no selected test covers as a gap, not only a wholly empty selection")
	f.StringVar(&sf.testCmd, "test-cmd", "", `override the detected test command, e.g. "go test -race"; it is an argv, NOT a shell line, and the selected paths are appended`)
	f.StringVar(&sf.runnerCmd, "runner", "", "alias for --test-cmd")

	// --staged and --since answer different questions; taking one silently is
	// how a CI job ends up diffing an empty index and gating on nothing.
	cmd.MarkFlagsMutuallyExclusive("staged", "since")
	cmd.MarkFlagsMutuallyExclusive("test-cmd", "runner")
}

// capture records which flags were explicitly set and folds --runner into
// --test-cmd. Must run after parsing and before the flags are read.
func (sf *selectFlags) capture(cmd *cobra.Command) {
	sf.explicit = make(map[string]bool)
	cmd.Flags().Visit(func(f *pflag.Flag) { sf.explicit[f.Name] = true })
	if sf.runnerCmd != "" {
		sf.testCmd = sf.runnerCmd
	}
}

// validate rejects flag values that would otherwise be silently ignored.
func (sf *selectFlags) validate() error {
	if !slices.Contains(fallbackModes, sf.fallback) {
		return fmt.Errorf("unknown --fallback %q (want %s)", sf.fallback, strings.Join(fallbackModes, ", "))
	}
	if sf.timeout < 0 {
		return fmt.Errorf("--timeout must not be negative")
	}
	return nil
}

// options maps the flags onto selector options.
//
// selector.Select backfills any zero-valued option with its default, so a flag
// the user explicitly set to 0 would otherwise be silently replaced. An
// explicit 0 is honoured here as "no limit" — no minimum score, no cap, no
// traversal depth — never as "select nothing", because a flag typo that
// silently empties the selection is exactly the false green witness exists to
// prevent.
func (sf *selectFlags) options() selector.SelectOptions {
	opts := selector.SelectOptions{
		MaxDepth:         sf.depth,
		MinScore:         sf.minScore,
		MaxTests:         sf.maxTests,
		CoChangeMinCount: sf.coChange,
		FanOutCap:        sf.fanOutCap,
		Exclude:          sf.exclude,
		Kinds:            sf.kinds,
		Signals:          sf.signals,
	}
	if sf.explicit["depth"] && sf.depth == 0 {
		// Negative disables the BFS loop without tripping the zero backfill.
		opts.MaxDepth = -1
	}
	if sf.explicit["min-score"] && sf.minScore == 0 {
		opts.MinScore = math.SmallestNonzeroFloat64
	}
	if sf.explicit["max"] && sf.maxTests == 0 {
		opts.MaxTests = math.MaxInt
	}
	if sf.explicit["co-change-min"] && sf.coChange == 0 {
		opts.CoChangeMinCount = 1
	}
	if sf.explicit["fan-out-cap"] && sf.fanOutCap == 0 {
		opts.FanOutCap = math.MaxInt
	}
	return opts
}

// resolve opens recon and figures out the changed files (explicit args, or a
// git diff in the chosen mode). Caller must Close the returned recon.
//
// errOut carries the notes resolution itself has to report — currently only the
// relocated cache directory; it may be nil for a caller with nowhere to put
// them.
func (sf *selectFlags) resolve(errOut io.Writer, args []string) (*recon.Recon, string, []gitdiff.Change, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, "", nil, fmt.Errorf("getting working directory: %w", err)
	}

	// gitdiff reports paths relative to the repository root. Rooting recon at
	// whatever subdirectory witness was invoked from would put the two in
	// different path spaces, where every lookup misses and witness reports "no
	// tests" for a change it simply could not see.
	root, err := gitdiff.RepoRoot(wd)
	if err != nil {
		return nil, "", nil, fmt.Errorf("locating git repository root: %w", err)
	}

	var reconOpts []recon.Option
	if sf.cacheDir != "" {
		cacheDir := repoCacheDir(root, sf.cacheDir)
		noteRelocatedCache(errOut, root, wd, sf.cacheDir, cacheDir)
		reconOpts = append(reconOpts, recon.WithCacheDir(cacheDir))
	}
	r, err := recon.New(root, reconOpts...)
	if err != nil {
		return nil, "", nil, fmt.Errorf("initializing recon: %w", err)
	}

	if len(args) > 0 {
		changes := make([]gitdiff.Change, 0, len(args))
		for _, a := range args {
			changes = append(changes, gitdiff.Change{Path: repoRelative(root, wd, a), Status: gitdiff.StatusModified})
		}
		return r, root, changes, nil
	}

	mode := gitdiff.WorkingTree
	ref := ""
	switch {
	case sf.staged:
		mode = gitdiff.Staged
	case sf.since != "":
		mode = gitdiff.SinceRef
		ref = sf.since
	}
	changes, err := gitdiff.ChangedFilesDetailed(root, mode, ref)
	if err != nil {
		r.Close()
		return nil, "", nil, fmt.Errorf("detecting changes: %w", err)
	}
	return r, root, changes, nil
}

// repoCacheDir anchors a relative --cache-dir at the repository root instead of
// at the shell's working directory.
//
// recon.WithCacheDir runs the path through filepath.Abs, which resolves it
// against the process's working directory: `witness run --cache-dir .cache`
// typed in a subdirectory put the cache in that subdirectory, so the same flag
// in the same repository named a different — and always cold — cache depending
// on where it was typed. Every other path witness handles is repo-root-relative
// (see repoRelative), and the cache is now one too. An absolute path is the user
// naming a location outright and is passed through untouched.
func repoCacheDir(root, dir string) string {
	if dir == "" || filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(root, dir)
}

// noteRelocatedCache tells the one user repoCacheDir can surprise: someone who
// has been running witness from a subdirectory with a relative --cache-dir and
// already has a populated cache sitting there.
//
// recon's cache is derived analysis, not user data, so moving it costs a
// one-time reanalysis and nothing else — but a rebuild nobody announced reads as
// witness having suddenly become slow, and the old directory is left behind for
// the user to delete. Hence a note, printed only when the resolution actually
// changed where the cache lands AND something is already there; running from the
// repository root, the usual case, says nothing.
func noteRelocatedCache(w io.Writer, root, wd, flag, resolved string) {
	if w == nil || filepath.IsAbs(flag) || root == wd {
		return
	}
	old := filepath.Join(wd, flag)
	if old == resolved {
		return
	}
	if entries, err := os.ReadDir(old); err != nil || len(entries) == 0 {
		return
	}
	fmt.Fprintf(w, "witness: --cache-dir %s is relative to the repository root (%s), not to the current directory; "+
		"the cache in %s is no longer used and will be rebuilt once (it is a derived analysis cache; delete it when you are ready)\n",
		flag, resolved, old)
}

func newSelectCmd() *cobra.Command {
	var sf selectFlags
	var format string

	cmd := &cobra.Command{
		Use:   "select [files...] [-- runner args...]",
		Short: "Select tests to run based on changed files",
		Long: `Given changed files, return a prioritized list of tests to run.

If no files are provided, uses git diff to detect changes.

Output formats:
  json   — structured JSON with scores and signals (default)
  paths  — one test path per line
  exec   — test runner command (auto-detected: mix test, go test, dotnet test, etc.)`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			sf.capture(cmd)
			if !slices.Contains(outputFormats, format) {
				return fmt.Errorf("unknown --format %q (want %s)", format, strings.Join(outputFormats, ", "))
			}
			return sf.validate()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Never write to os.Stdout/os.Stderr directly: witness is embedded
			// in-process (Rivet), where the host redirects these to capture the
			// result and where a stray write corrupts its MCP stdio stream.
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()

			files, passthrough := splitPassthrough(cmd, args)
			r, root, changes, err := sf.resolve(errOut, files)
			if err != nil {
				return err
			}
			defer r.Close()

			if len(changes) == 0 {
				fmt.Fprintln(errOut, "No changed files detected.")
			}

			result, err := selector.Select(r, changedPaths(changes), sf.options())
			if err != nil {
				return err
			}

			gaps := unprovenReasons(result, deletedPaths(changes), sf.requireCoverage)
			reportGaps(errOut, result, gaps)

			if format == formatJSON {
				// The JSON already carries the gaps in summary.unmapped,
				// summary.not_indexed and summary.analysis_error, so there is
				// nothing for `full` to widen. Only `fail` changes the outcome.
				if len(gaps) > 0 && sf.fallback == fallbackFail {
					return unprovenError(gaps)
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			useFull, err := applyFallback(errOut, sf.fallback, gaps)
			if err != nil {
				return err
			}

			switch format {
			case formatPaths:
				for _, t := range result.Tests {
					fmt.Fprintln(out, t.Path)
				}
				if useFull {
					// A list of paths cannot say "run everything". Printing
					// what was found and exiting 0 is the false green this
					// command exists to prevent — `witness select --format
					// paths | xargs go test` would then run a selection nobody
					// vouched for, or nothing at all, and pass. The paths are
					// still printed (they are the best partial answer there is)
					// but the exit code says the fallback went unhonoured.
					return fmt.Errorf("witness cannot prove the selection is complete and --format paths cannot express the full-suite fallback; "+
						"use --format exec (it prints the whole-suite command) or --fallback=none to accept the partial list: %s",
						strings.Join(gaps, "; "))
				}
			case formatExec:
				cmds, err := testCommands(root, r, result, &sf, useFull, passthrough)
				if err != nil {
					return noRunnerHint(err)
				}
				for _, c := range cmds {
					fmt.Fprintln(out, c)
				}
			}
			return nil
		},
	}

	sf.bind(cmd)
	cmd.Flags().StringVar(&format, "format", formatJSON, "output format: json, paths, exec")
	return cmd
}

func newRunCmd() *cobra.Command {
	var sf selectFlags

	cmd := &cobra.Command{
		Use:   "run [files...] [-- runner args...]",
		Short: "Select tests and run them",
		Long: `Select the relevant tests for the changed files and execute them.

Detects the test runner from the project (go test, mix test, pytest, dotnet test, ...),
streams its output, and exits with the runner's exit code — so it drops into
CI or a pre-commit hook directly. With no files, uses git diff like 'select'.

Anything after '--' is appended to the test runner's arguments, so
'witness run --since main -- -race' runs the selection with the race detector.

When witness cannot prove the selection covers the change — a recon error, a
file missing from the index, a deleted file whose dependents cannot be resolved,
or nothing selected at all while changed files went uncovered — --fallback
decides what happens. It defaults to running the whole suite rather than exiting
0 on a selection nobody can vouch for; use --fallback=none in a pre-commit hook,
where a docs-only commit should not drag in every test.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			sf.capture(cmd)
			return sf.validate()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()

			files, passthrough := splitPassthrough(cmd, args)
			r, root, changes, err := sf.resolve(errOut, files)
			if err != nil {
				return err
			}
			defer r.Close()

			if len(changes) == 0 {
				// Nothing changed is the one empty answer that proves itself.
				fmt.Fprintln(errOut, "No changed files detected; nothing to run.")
				return nil
			}

			result, err := selector.Select(r, changedPaths(changes), sf.options())
			if err != nil {
				return err
			}

			gaps := unprovenReasons(result, deletedPaths(changes), sf.requireCoverage)
			reportGaps(errOut, result, gaps)

			useFull, err := applyFallback(errOut, sf.fallback, gaps)
			if err != nil {
				return err
			}

			cmds, err := testCommands(root, r, result, &sf, useFull, passthrough)
			if err != nil {
				// The runner could not turn part of the selection into a
				// command. Running the rest and exiting 0 would report a pass
				// for tests that never ran, so the whole invocation fails.
				return noRunnerHint(err)
			}
			if len(cmds) == 0 {
				fmt.Fprintln(errOut, "No relevant tests selected; nothing to run.")
				return nil
			}

			fmt.Fprintf(errOut, "witness: running %d command(s) for %d test target(s)\n", len(cmds), len(result.Tests))
			for _, c := range cmds {
				fmt.Fprintf(errOut, "  $ %s\n", c)
			}
			fmt.Fprintln(errOut)

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if sf.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, sf.timeout)
				defer cancel()
			}

			// Write through the command's writers so embedded callers capture
			// output instead of it leaking to the host's stdout.
			code, err := runner.ExecuteAll(ctx, cmds, root, out, errOut)
			if err != nil {
				// The suite never ran (or was cancelled): that is a witness
				// error, not a test failure, so it must not be reported as an
				// exit code the caller might mistake for a real test result.
				return err
			}
			if code != 0 {
				return &ExitCodeError{Code: code}
			}
			return nil
		},
	}

	sf.bind(cmd)
	cmd.Flags().DurationVar(&sf.timeout, "timeout", 0, "abort the test run after this long (0 = no limit)")
	return cmd
}

// splitPassthrough separates positional file arguments from everything the user
// put after `--`, which is forwarded to the test runner verbatim. Without this
// split a `-- -race` would be treated as a changed file named "-race", which
// maps to no test and turns the run green having executed nothing.
func splitPassthrough(cmd *cobra.Command, args []string) (files, passthrough []string) {
	dash := cmd.ArgsLenAtDash()
	if dash < 0 || dash > len(args) {
		return args, nil
	}
	return args[:dash], args[dash:]
}

// repoRelative rewrites a positional path argument, which the user typed
// relative to their shell's directory, into the repo-root-relative form
// everything downstream speaks. Without it `witness select foo.go` from a
// subdirectory looks up "foo.go" at the repository root, finds nothing, and
// reports no tests for a file that is right there.
//
// A path outside the repository is handed through untouched so recon can be the
// one to say it does not know it.
//
// Both sides are resolved through EvalSymlinks first. os.Getwd honours $PWD and
// hands back the logical path, while git's rev-parse --show-toplevel resolves
// symlinks — so a repository reached through a symlinked path (a symlinked home
// or CI checkout directory, macOS's /tmp) put the two in different spellings of
// the same directory, filepath.Rel returned "../..." and the argument was passed
// through unchanged, straight back into the wrong path space.
func repoRelative(root, wd, arg string) string {
	abs := arg
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(wd, abs)
	}
	root, abs = resolveSymlinks(root), resolveSymlinks(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return arg
	}
	return filepath.ToSlash(rel)
}

// resolveSymlinks returns the physical path, or the input unchanged when it
// cannot be resolved (a file that does not exist yet, a permission error) —
// guessing is better than dropping the argument.
func resolveSymlinks(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	// The file itself may not exist (a path the user is about to create), but
	// its directory usually does.
	dir, base := filepath.Split(path)
	if resolved, err := filepath.EvalSymlinks(filepath.Clean(dir)); err == nil {
		return filepath.Join(resolved, base)
	}
	return path
}

// changedPaths projects the git changes onto the path list the selector wants.
func changedPaths(changes []gitdiff.Change) []string {
	paths := make([]string, 0, len(changes))
	for _, c := range changes {
		paths = append(paths, c.Path)
	}
	return paths
}

// deletedPaths lists the changes whose file no longer exists.
func deletedPaths(changes []gitdiff.Change) []string {
	var deleted []string
	for _, c := range changes {
		if c.Deleted() {
			deleted = append(deleted, c.Path)
		}
	}
	return deleted
}

// unprovenReasons lists why witness cannot prove the selection covers the
// change. An empty list means the answer is trustworthy: recon answered
// cleanly, every changed file is in its index and mapped to at least one
// selected test, and nothing was deleted.
//
// The last clause is the one that matters. A changed file no test covers is
// unremarkable while OTHER tests were still selected — witness has something to
// run and the gate has teeth. But when nothing at all was selected, every
// changed file is a hole, and "no relevant tests; exit 0" is a pass witness did
// not earn. That is the go.mod-bump false green: the dependency changed, no
// test mapped to it, and CI went green having run nothing.
//
// The common PR shape — a go.mod bump ALONGSIDE a source edit — is deliberately
// outside that clause, because widening on any uncovered file would run the
// whole suite for every README in every diff. requireCoverage (--require-coverage)
// is the knob for a gate that wants the stricter reading.
//
// A selection the CALLER emptied is not a gap either: --kind, --exclude and
// --signals are the user saying which tests to skip, and answering that by
// running every test in the repository defeats the flag they just typed.
func unprovenReasons(result *selector.SelectResult, deleted []string, requireCoverage bool) []string {
	var reasons []string
	if result.Summary.AnalysisError != "" {
		reasons = append(reasons, "analysis failed: "+result.Summary.AnalysisError)
	}
	if n := len(result.Summary.NotIndexed); n > 0 {
		reasons = append(reasons, fmt.Sprintf("%d changed file(s) are not in the recon index, so their tests are unknown: %s",
			n, strings.Join(result.Summary.NotIndexed, " ")))
	}
	if n := len(deleted); n > 0 {
		reasons = append(reasons, fmt.Sprintf("%d changed file(s) were deleted, so their dependents cannot be resolved: %s",
			n, strings.Join(deleted, " ")))
	}
	if n := len(result.Summary.Unmapped); n > 0 {
		switch {
		case requireCoverage:
			reasons = append(reasons, fmt.Sprintf("%d changed file(s) are covered by no selected test (--require-coverage): %s",
				n, strings.Join(result.Summary.Unmapped, " ")))
		case len(result.Tests) == 0 && result.Summary.Filtered == 0:
			reasons = append(reasons, fmt.Sprintf("no test covers any of the %d changed file(s): %s",
				n, strings.Join(result.Summary.Unmapped, " ")))
		}
	}
	return reasons
}

// reportGaps prints everything the selection could not account for. Without it
// an empty test list caused by a stale index is indistinguishable from "nothing
// to test", which is the false green witness exists to prevent.
func reportGaps(w io.Writer, result *selector.SelectResult, gaps []string) {
	for _, g := range gaps {
		fmt.Fprintf(w, "witness: %s\n", g)
	}
	if n := result.Summary.Truncated; n > 0 {
		fmt.Fprintf(w, "witness: capped at %d of %d relevant tests; raise --max\n",
			result.Summary.TestsSelected, result.Summary.TestsSelected+n)
	}
	// A selection the caller's own --kind/--exclude/--signals emptied is not a
	// gap, so it must still be said out loud: "nothing to run" and "you filtered
	// everything out" look identical from the exit code.
	if n := result.Summary.Filtered; n > 0 && len(result.Tests) == 0 {
		fmt.Fprintf(w, "witness: %d relevant test(s) were dropped by --kind/--exclude/--signals; nothing is left to run\n", n)
	}
	// Skipped when nothing was selected, and when --require-coverage already
	// turned the same files into a gap: unprovenReasons said it in stronger
	// terms, and saying it twice reads as two different problems.
	if n := len(result.Summary.Unmapped); n > 0 && len(result.Tests) > 0 && !slices.ContainsFunc(gaps, isCoverageGap) {
		fmt.Fprintf(w, "witness: %d changed file(s) covered by no selected test: %s\n",
			n, strings.Join(result.Summary.Unmapped, " "))
	}
}

// isCoverageGap reports whether a gap is already about uncovered changed files.
func isCoverageGap(gap string) bool {
	return strings.Contains(gap, "no selected test") || strings.Contains(gap, "no test covers")
}

// applyFallback turns the --fallback policy into a decision. It reports whether
// the caller should widen to the project's whole suite, and errors out when the
// policy says an unprovable selection must not pass.
func applyFallback(w io.Writer, mode string, gaps []string) (useFull bool, err error) {
	if len(gaps) == 0 {
		return false, nil
	}
	switch mode {
	case fallbackFail:
		return false, unprovenError(gaps)
	case fallbackFull:
		fmt.Fprintln(w, "witness: cannot prove the selection is complete; falling back to the full suite (--fallback=none to disable)")
		return true, nil
	default:
		fmt.Fprintln(w, "witness: cannot prove the selection is complete; continuing anyway (--fallback=none)")
		return false, nil
	}
}

// unprovenError is what --fallback=fail returns: a plain error, so the gate
// exits non-zero through main()'s normal path rather than through an exit code
// that would read as a test result.
func unprovenError(gaps []string) error {
	return fmt.Errorf("witness cannot prove which tests cover this change (--fallback=fail): %s",
		strings.Join(gaps, "; "))
}

// noRunnerHint rewrites a runner.ErrNoRunner into advice, leaving other errors
// alone. It stays a plain error — not an ExitCodeError — because no test ran,
// so there is no test exit code to report.
func noRunnerHint(err error) error {
	var nre *runner.NoRunnerError
	if !errors.As(err, &nre) {
		return err
	}
	// The fallback's failure is a different one: the selection was fine, there
	// is just no command that runs everything. Telling that operator to go and
	// select tests by hand is advice for the other problem.
	if nre.WholeSuite {
		return fmt.Errorf("witness has no whole-suite command for %s; run your own test command, or use --fallback=fail to stop before this point: %w",
			nre.LangLabel(), err)
	}
	return fmt.Errorf("witness cannot run tests for %s; use 'witness select --format paths' and run them with your own tool: %w",
		nre.LangLabel(), err)
}

// testCommands turns a selection into the commands to execute, honouring
// --test-cmd, the full-suite fallback, and anything after `--`.
//
// root is the repository root: the directory the commands will run in, the one
// the selected paths are relative to, and the one runner.FormatCommand reads the
// build files out of to derive an invocation for the ecosystems whose runners do
// not take a path (Maven, Gradle, sbt, SwiftPM, PHPUnit, dart, cargo in a
// workspace). Passing "" there would cost those languages their commands.
//
// It returns no commands (and no error) for a selection that is genuinely
// empty; the caller decides what an empty run means.
func testCommands(root string, r *recon.Recon, result *selector.SelectResult, sf *selectFlags, useFull bool, passthrough []string) ([]runner.Command, error) {
	framework := detectFramework(r)

	var paths []string
	if !useFull {
		for _, t := range result.Tests {
			paths = append(paths, t.Path)
		}
		if len(paths) == 0 {
			return nil, nil
		}
	}

	if sf.testCmd != "" {
		argv, err := splitCommand(sf.testCmd)
		if err != nil {
			return nil, fmt.Errorf("parsing --test-cmd: %w", err)
		}
		if len(argv) == 0 {
			return nil, fmt.Errorf("--test-cmd is empty")
		}
		if err := checkShellSyntax(argv); err != nil {
			return nil, err
		}
		cmds, err := runner.OverrideCommand(argv, framework, paths)
		if err != nil {
			return nil, err
		}
		for i := range cmds {
			cmds[i].Argv = append(cmds[i].Argv, passthrough...)
		}
		return cmds, nil
	}

	var cmds []runner.Command
	var err error
	if useFull {
		cmds, err = runner.FullSuiteCommand(root, framework)
	} else {
		cmds, err = runner.FormatCommand(root, framework, paths)
	}
	if err != nil {
		// FormatCommand can return commands AND an error for a partly-runnable
		// polyglot selection. Dropping the error and running the subset would
		// report a pass for the languages witness skipped.
		return nil, err
	}
	for i := range cmds {
		cmds[i].Argv = append(cmds[i].Argv, passthrough...)
	}
	return cmds, nil
}

// detectFramework names the ecosystem recon's overview points at: the primary
// language when witness can run it, and only then a detected framework.
//
// The order matters for the full-suite fallback, which has no paths to correct
// a bad guess with — see runner.FullSuiteFramework. For FormatCommand it is
// only a hint, since that groups the selected paths by their own extensions.
func detectFramework(r *recon.Recon) string {
	if r == nil {
		return ""
	}
	overview, err := r.Overview()
	if err != nil || overview == nil {
		return ""
	}
	var langNames []string
	for _, lang := range overview.Languages {
		langNames = append(langNames, lang.Name)
	}
	var fwNames []string
	for _, fw := range overview.Frameworks {
		fwNames = append(fwNames, fw.Name)
	}
	return runner.FullSuiteFramework(langNames, fwNames)
}

// checkShellSyntax rejects a --test-cmd that was written as a shell line. The
// string is tokenized into an argv and executed directly — witness never hands
// it to a shell — so an environment assignment or a pipe would otherwise be
// passed to the test runner as a literal argument and fail with a message that
// names neither the cause nor the fix.
func checkShellSyntax(argv []string) error {
	const advice = `--test-cmd is an argv, not a shell line: witness executes it directly. ` +
		`Wrap it yourself if you need shell syntax: --test-cmd 'sh -c "<your line> \"$@\"" witness'`

	if name := argv[0]; strings.Contains(name, "=") && !strings.Contains(name, "/") {
		return fmt.Errorf("--test-cmd starts with the environment assignment %q; %s", name, advice)
	}
	for _, arg := range argv {
		switch arg {
		case "|", "||", "&&", ";", "&", ">", ">>", "<", "2>", "2>&1":
			return fmt.Errorf("--test-cmd contains the shell operator %q; %s", arg, advice)
		}
	}
	return nil
}

// splitCommand tokenizes a --test-cmd string into an argv, honouring single
// quotes, double quotes and backslash escapes. Witness never hands the string
// to a shell, so the quoting has to be undone here.
func splitCommand(s string) ([]string, error) {
	var (
		argv  []string
		cur   strings.Builder
		open  bool // cur holds a token, even if it is empty ("" is an argument)
		quote rune
	)
	flush := func() {
		if open {
			argv = append(argv, cur.String())
			cur.Reset()
			open = false
		}
	}

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
				continue
			}
			cur.WriteRune(c)
		case quote == '"':
			if c == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
				i++
				cur.WriteRune(runes[i])
				continue
			}
			if c == '"' {
				quote = 0
				continue
			}
			cur.WriteRune(c)
		case c == '\'' || c == '"':
			quote = c
			open = true
		case c == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
			open = true
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			cur.WriteRune(c)
			open = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote in %q", quote, s)
	}
	flush()
	return argv, nil
}
