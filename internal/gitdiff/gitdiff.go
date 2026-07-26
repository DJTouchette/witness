package gitdiff

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Mode determines how changed files are detected.
type Mode int

const (
	// WorkingTree reports staged + unstaged edits to tracked files, plus
	// untracked files that are not ignored. Deletions are included.
	WorkingTree Mode = iota
	// Staged reports the index only (git diff --staged). Untracked files are
	// not part of the index and so are not reported.
	Staged
	// SinceRef reports git diff <ref>...HEAD.
	SinceRef
)

// Status is the git status letter attached to a change. Deletions and renames
// matter to the selector: a deleted path can no longer be looked up in the
// index, but its dependents still need testing.
const (
	StatusAdded     = "A"
	StatusModified  = "M"
	StatusDeleted   = "D"
	StatusRenamed   = "R"
	StatusUntracked = "?"
)

// Change is one changed path together with the git status letter that produced
// it. Renames are reported as two changes: the source path as StatusDeleted
// (it no longer exists) and the destination as StatusRenamed.
type Change struct {
	Path   string
	Status string
}

// Deleted reports whether the path no longer exists in the working tree.
func (c Change) Deleted() bool { return c.Status == StatusDeleted }

// RepoRoot returns the absolute path of the git working tree containing dir.
//
// Paths reported by ChangedFiles are always relative to this directory, never
// to dir, so a caller that roots other tooling (recon) at dir must root it here
// instead or the two path spaces silently disagree and nothing matches.
func RepoRoot(dir string) (string, error) {
	out, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimRight(out, "\r\n")
	if root == "" {
		return "", fmt.Errorf("git rev-parse --show-toplevel: no working tree for %s", dir)
	}
	return root, nil
}

// ChangedFiles returns the paths changed according to the given mode, relative
// to the repository root. ref is only used with SinceRef mode.
func ChangedFiles(root string, mode Mode, ref string) ([]string, error) {
	changes, err := ChangedFilesDetailed(root, mode, ref)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, c := range changes {
		files = append(files, c.Path)
	}
	return files, nil
}

// ChangedFilesDetailed is ChangedFiles with the git status letter kept, so
// callers can tell a deletion (which cannot be resolved against the current
// index) from an edit. Paths are relative to the repository root and sorted.
func ChangedFilesDetailed(root string, mode Mode, ref string) ([]Change, error) {
	if mode == SinceRef && ref == "" {
		return nil, fmt.Errorf("ref is required for SinceRef mode")
	}

	// git reports paths relative to the repository root, and `git ls-files`
	// only sees the directory it runs in, so pin every invocation to the root.
	repo, err := RepoRoot(root)
	if err != nil {
		return nil, err
	}

	var changes []Change
	switch mode {
	case Staged:
		out, err := runGit(repo, "diff", "--cached", "--name-status", "-z")
		if err != nil {
			return nil, err
		}
		changes = parseNameStatus(out)
	case SinceRef:
		out, err := runGit(repo, "diff", "--name-status", "-z", ref+"...HEAD")
		if err != nil {
			return nil, err
		}
		changes = parseNameStatus(out)
	default:
		args := []string{"diff", "HEAD", "--name-status", "-z"}
		if !headExists(repo) {
			// Unborn HEAD (no commits yet): there is nothing to diff against,
			// so the index is the whole change.
			args = []string{"diff", "--cached", "--name-status", "-z"}
		}
		out, err := runGit(repo, args...)
		if err != nil {
			return nil, err
		}
		changes = parseNameStatus(out)

		// git diff never reports untracked files, so a brand-new file would
		// otherwise select zero tests.
		out, err = runGit(repo, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		for _, path := range splitNUL(out) {
			// An untracked directory that is itself a git repository (a nested
			// clone, a submodule that was never registered) is reported as the
			// DIRECTORY, "vendorlib/", rather than recursed into. It is not a
			// file the selector can map to a test, and passing it on made every
			// invocation in such a repo fall back to the whole suite.
			if strings.HasSuffix(path, "/") {
				continue
			}
			changes = append(changes, Change{Path: path, Status: StatusUntracked})
		}
	}

	return normalize(changes), nil
}

// headExists reports whether HEAD points at a commit. A fresh repository with
// no commits has an unborn HEAD and cannot be diffed against.
func headExists(repo string) bool {
	_, err := runGit(repo, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// runGit runs git in dir and returns stdout. git's stderr carries the only
// useful part of a failure ("fatal: bad revision ..."), so it is folded into
// the error rather than dropped.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// parseNameStatus parses `git diff --name-status -z` output. Records are
// "<status>\0<path>\0", or "<status>\0<src>\0<dst>\0" for renames and copies.
func parseNameStatus(output string) []Change {
	fields := splitNUL(output)
	var changes []Change
	for i := 0; i < len(fields); {
		// git appends a similarity score to renames and copies ("R100").
		status := fields[i][:1]
		i++
		switch status {
		case StatusRenamed, "C":
			if i+1 >= len(fields) {
				return changes
			}
			src, dst := fields[i], fields[i+1]
			i += 2
			if status == StatusRenamed {
				// The old path is gone; its dependents still need testing.
				changes = append(changes, Change{Path: src, Status: StatusDeleted})
			}
			changes = append(changes, Change{Path: dst, Status: status})
		default:
			if i >= len(fields) {
				return changes
			}
			changes = append(changes, Change{Path: fields[i], Status: status})
			i++
		}
	}
	return changes
}

// splitNUL splits NUL-separated git output. Entries are never trimmed: leading
// and trailing spaces are legal in filenames, and -z output is already exact
// (it disables git's C-quoting of non-ASCII and special characters).
func splitNUL(output string) []string {
	var fields []string
	for _, field := range strings.Split(output, "\x00") {
		if field != "" {
			fields = append(fields, field)
		}
	}
	return fields
}

// normalize drops duplicate paths (a rename source can also appear untracked)
// and sorts, so the selection is deterministic across runs.
func normalize(changes []Change) []Change {
	seen := make(map[string]bool, len(changes))
	var out []Change
	for _, c := range changes {
		if c.Path == "" || seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
