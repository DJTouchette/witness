package witness

import (
	"github.com/djtouchette/recon/pkg/recon"
	"github.com/djtouchette/witness/internal/gitdiff"
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
func WithCacheDir(dir string) Option {
	return func(o *options) { o.cacheDir = dir }
}

// New creates a Witness instance rooted at the given directory.
func New(root string, opts ...Option) (*Witness, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	var reconOpts []recon.Option
	if o.cacheDir != "" {
		reconOpts = append(reconOpts, recon.WithCacheDir(o.cacheDir))
	}

	r, err := recon.New(root, reconOpts...)
	if err != nil {
		return nil, err
	}

	return &Witness{recon: r, root: root}, nil
}

// Close releases resources.
func (w *Witness) Close() error {
	return w.recon.Close()
}

// SelectResult re-exports the selector result type.
type SelectResult = selector.SelectResult

// SelectOptions re-exports the selector options type.
type SelectOptions = selector.SelectOptions

// Select finds tests relevant to the given changed files.
// If changedFiles is empty, uses git diff working tree.
func (w *Witness) Select(changedFiles []string, opts SelectOptions) (*SelectResult, error) {
	if len(changedFiles) == 0 {
		var err error
		changedFiles, err = gitdiff.ChangedFiles(w.root, gitdiff.WorkingTree, "")
		if err != nil {
			return nil, err
		}
	}

	if len(changedFiles) == 0 {
		return &SelectResult{
			ChangedFiles: changedFiles,
			Summary:      selector.Summary{BySignal: map[string]int{}},
		}, nil
	}

	return selector.Select(w.recon, changedFiles, opts)
}

// SelectStaged finds tests relevant to staged git changes.
func (w *Witness) SelectStaged(opts SelectOptions) (*SelectResult, error) {
	files, err := gitdiff.ChangedFiles(w.root, gitdiff.Staged, "")
	if err != nil {
		return nil, err
	}
	return w.Select(files, opts)
}

// SelectSince finds tests relevant to changes since a git ref.
func (w *Witness) SelectSince(ref string, opts SelectOptions) (*SelectResult, error) {
	files, err := gitdiff.ChangedFiles(w.root, gitdiff.SinceRef, ref)
	if err != nil {
		return nil, err
	}
	return w.Select(files, opts)
}
