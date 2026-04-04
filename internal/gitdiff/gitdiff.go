package gitdiff

import (
	"fmt"
	"os/exec"
	"strings"
)

// Mode determines how changed files are detected.
type Mode int

const (
	WorkingTree Mode = iota // unstaged + staged changes
	Staged                  // git diff --staged only
	SinceRef                // git diff <ref>...HEAD
)

// ChangedFiles returns files changed according to the given mode.
// ref is only used with SinceRef mode.
func ChangedFiles(root string, mode Mode, ref string) ([]string, error) {
	var args []string

	switch mode {
	case Staged:
		args = []string{"diff", "--staged", "--name-only", "--diff-filter=ACMR"}
	case SinceRef:
		if ref == "" {
			return nil, fmt.Errorf("ref is required for SinceRef mode")
		}
		args = []string{"diff", "--name-only", "--diff-filter=ACMR", ref + "...HEAD"}
	default:
		// WorkingTree: both staged and unstaged.
		args = []string{"diff", "HEAD", "--name-only", "--diff-filter=ACMR"}
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		// If HEAD doesn't exist (initial commit), try without HEAD.
		if mode == WorkingTree {
			args = []string{"diff", "--name-only", "--diff-filter=ACMR"}
			cmd = exec.Command("git", args...)
			cmd.Dir = root
			out, err = cmd.Output()
			if err != nil {
				return nil, fmt.Errorf("git diff: %w", err)
			}
		} else {
			return nil, fmt.Errorf("git diff: %w", err)
		}
	}

	return parseFiles(string(out)), nil
}

func parseFiles(output string) []string {
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}
