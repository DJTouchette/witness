// Package witness is the embeddable test-selection API: point it at a git
// repository, hand it a set of changed files (or let it diff them itself), and
// get back the tests worth running plus the commands that run them.
package witness

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/djtouchette/recon/pkg/recon"
	"github.com/djtouchette/witness/internal/gitdiff"
	"github.com/djtouchette/witness/internal/planner"
	"github.com/djtouchette/witness/internal/runner"
	"github.com/djtouchette/witness/internal/selector"
)

// Witness wraps recon and provides test selection.
type Witness struct {
	recon *recon.Recon
	root  string
}

// Option configures Witness behaviour.
type Option func(*options)

type options struct {
	cacheDir string
}

// WithCacheDir stores the recon cache in the given directory.
//
// A relative path is resolved against the REPOSITORY ROOT, not the host
// process's working directory, so an embedder that analyses several repositories
// gets one cache per repository from a single option value — and the same option
// keeps naming the same cache however the host was launched. Pass an absolute
// path to place the cache somewhere of your own choosing.
func WithCacheDir(dir string) Option {
	return func(o *options) { o.cacheDir = dir }
}

// New creates a Witness instance for the git repository containing root.
//
// Analysis is always rooted at the repository root, not at root itself: git
// reports changed paths relative to the repository, so a Witness rooted at a
// subdirectory would look every one of them up under the wrong prefix and
// report "no tests" for changes it simply could not see. root outside a git
// working tree is an error rather than a silently empty selection.
func New(root string, opts ...Option) (*Witness, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	repoRoot, err := gitdiff.RepoRoot(root)
	if err != nil {
		return nil, fmt.Errorf("locating git repository root for %s: %w", root, err)
	}

	var reconOpts []recon.Option
	if o.cacheDir != "" {
		reconOpts = append(reconOpts, recon.WithCacheDir(repoCacheDir(repoRoot, o.cacheDir)))
	}

	r, err := recon.New(repoRoot, reconOpts...)
	if err != nil {
		return nil, err
	}

	return &Witness{recon: r, root: repoRoot}, nil
}

// repoCacheDir anchors a relative cache directory at the repository root.
//
// recon.WithCacheDir runs the path through filepath.Abs, which resolves it
// against the HOST PROCESS's working directory — so WithCacheDir(".cache") named
// a different cache for every directory the embedding program happened to be
// started from, and pointed several repositories at the same one. Selected paths
// and changed files are already repo-root-relative (see Root); the cache is now
// too. An absolute path is left alone.
func repoCacheDir(root, dir string) string {
	if dir == "" || filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(root, dir)
}

// Close releases resources.
func (w *Witness) Close() error {
	return w.recon.Close()
}

// Root returns the git repository root this Witness analyses. Selected test
// paths and changed files are relative to it.
func (w *Witness) Root() string { return w.root }

// SelectResult re-exports the selector result type.
type SelectResult = selector.SelectResult

// SelectOptions re-exports the selector options type.
type SelectOptions = selector.SelectOptions

// ScoredTest re-exports one scored test in a selection.
type ScoredTest = selector.ScoredTest

// Summary re-exports the per-selection counts and coverage gaps.
type Summary = selector.Summary

// DefaultOptions returns the default tuning (depth 2, min score 0.1, max 50
// tests). Exported so a library caller never has to reach into an internal
// package to build a SelectOptions.
func DefaultOptions() SelectOptions { return selector.DefaultOptions() }

// selectFiles runs the selection over exactly the given files, with no git
// fallback. An empty list yields an empty result — never a working-tree diff,
// which would silently answer a different question than the caller asked.
func (w *Witness) selectFiles(changedFiles []string, opts SelectOptions) (*SelectResult, error) {
	opts.Root = w.root
	return selector.Select(w.recon, changedFiles, opts)
}

// Select finds tests relevant to the given changed files.
// If changedFiles is empty, uses git diff working tree.
func (w *Witness) Select(changedFiles []string, opts SelectOptions) (*SelectResult, error) {
	if len(changedFiles) == 0 {
		detected, err := gitdiff.ChangedFiles(w.root, gitdiff.WorkingTree, "")
		if err != nil {
			return nil, err
		}
		changedFiles = detected
	}
	return w.selectFiles(changedFiles, opts)
}

// SelectStaged finds tests relevant to staged git changes.
//
// An empty index yields an empty result: it does not fall back to a working
// tree diff, which would return tests for edits the caller never staged.
func (w *Witness) SelectStaged(opts SelectOptions) (*SelectResult, error) {
	files, err := gitdiff.ChangedFiles(w.root, gitdiff.Staged, "")
	if err != nil {
		return nil, err
	}
	return w.selectFiles(files, opts)
}

// SelectSince finds tests relevant to changes since a git ref.
//
// An empty diff yields an empty result, with no working-tree fallback.
func (w *Witness) SelectSince(ref string, opts SelectOptions) (*SelectResult, error) {
	files, err := gitdiff.ChangedFiles(w.root, gitdiff.SinceRef, ref)
	if err != nil {
		return nil, err
	}
	return w.selectFiles(files, opts)
}

// Complete reports whether selection carries no known coverage gaps. It does
// not claim the heuristic graph is a proof, or that any tests have executed.
func Complete(result *SelectResult) bool {
	return result != nil && len(selector.CoverageReasons(result)) == 0
}

// Plan is the versioned command plan; no test or package manager is executed.
type Plan = planner.Plan

// Command carries argv and repository-relative cwd. Execute argv directly.
type Command = runner.Command

func (w *Witness) Plan(result *SelectResult) (*Plan, error) { return planner.Build(w.root, result) }

// Commands is the legacy argv-only interface. Use Plan for cwd-aware commands;
// this method refuses to discard a non-root working directory.
func (w *Witness) Commands(result *SelectResult) ([][]string, error) {
	p, err := w.Plan(result)
	if err != nil {
		return nil, err
	}
	for _, c := range p.Commands {
		if c.Cwd != "" && c.Cwd != "." {
			return nil, fmt.Errorf("Commands cannot express cwd %q; use Plan and execute argv with its cwd", c.Cwd)
		}
	}
	return argvs(p.Commands), nil
}

// FullSuitePlan plans all discovered manifests without a dominant-language guess.
func (w *Witness) FullSuitePlan() (*Plan, error) { return planner.WholeRepository(w.root) }

// FullSuiteCommand is the legacy argv-only whole-repository interface.
func (w *Witness) FullSuiteCommand() ([][]string, error) {
	p, err := w.FullSuitePlan()
	if err != nil {
		return nil, err
	}
	for _, c := range p.Commands {
		if c.Cwd != "." && c.Cwd != "" {
			return nil, fmt.Errorf("FullSuiteCommand cannot express cwd %q; use FullSuitePlan", c.Cwd)
		}
	}
	return argvs(p.Commands), nil
}

// Run executes the commands for a selection in the repository root, streaming
// output to the given writers, and returns the worst exit code seen.
//
// A non-zero code means tests failed and is returned with a nil error. A
// non-nil error means the tests never ran (or ctx was cancelled); the code is
// then meaningless and must not be reported as a test result. Cancelling ctx
// stops the runner and everything it spawned.
func (w *Witness) Run(ctx context.Context, result *SelectResult, stdout, stderr io.Writer) (int, error) {
	p, err := w.Plan(result)
	if err != nil {
		return -1, err
	}
	if len(p.Commands) == 0 {
		return -1, fmt.Errorf("no test commands planned; nothing to run")
	}
	return runner.ExecuteAll(ctx, p.Commands, w.root, stdout, stderr)
}

// argvs strips the internal Command type off the public surface, since callers
// outside the module cannot name it.
func argvs(cmds []runner.Command) [][]string {
	out := make([][]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.Argv)
	}
	return out
}
