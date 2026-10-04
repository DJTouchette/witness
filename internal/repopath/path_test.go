package repopath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBoundary(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Skip(err)
	}
	for _, rel := range []string{"../out", "out/new/missing", "C:/outside", "C:relative", "//server/share", `a\b`, "/tmp"} {
		if p, err := Resolve(root, rel); err == nil {
			t.Errorf("accepted %q: %s", rel, p)
		}
	}
	if _, err := Resolve(root, "new/missing"); err != nil {
		t.Fatal(err)
	}
	if _, err := Directory(root, "new/missing"); err == nil {
		t.Fatal("accepted missing cwd")
	}
}
