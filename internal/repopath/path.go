// Package repopath validates repository-relative filesystem paths. It does not
// grant trust to test commands or protect a tree against concurrent hostile writes.
package repopath

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func Safe(rel string) bool {
	return rel != "" && !strings.ContainsAny(rel, "\x00\r\n\\:") && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(path.Clean(rel), "../")
}

// Resolve canonicalizes existing ancestors even when the leaf was deleted or
// has not been created. Dangling symlinks and symlink loops fail closed.
func Resolve(root, rel string) (string, error) {
	if !Safe(rel) {
		return "", fmt.Errorf("path escapes repository: %q", rel)
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	full := filepath.Join(base, filepath.FromSlash(rel))
	tail := []string{}
	for {
		_, err = os.Lstat(full)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		tail = append(tail, filepath.Base(full))
		parent := filepath.Dir(full)
		if parent == full {
			return "", err
		}
		full = parent
	}
	full, err = filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	for i := len(tail) - 1; i >= 0; i-- {
		full = filepath.Join(full, tail[i])
	}
	within, err := filepath.Rel(base, full)
	if err != nil || !Safe(filepath.ToSlash(within)) {
		return "", fmt.Errorf("path symlink escapes repository: %q", rel)
	}
	return full, nil
}

func Directory(root, rel string) (string, error) {
	full, err := Resolve(root, rel)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("cwd is not a directory: %s", rel)
	}
	return full, nil
}
