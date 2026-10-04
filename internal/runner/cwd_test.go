package runner

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCwdExecutionAndPOSIXRendering(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX rendering; JSON argv/cwd is the Windows interface")
	}
	root := t.TempDir()
	dir := "suite 'quoted' $(literal)"
	full := filepath.Join(root, dir)
	if err := os.Mkdir(full, 0755); err != nil {
		t.Fatal(err)
	}
	// macOS temp paths can begin with /var, an alias of /private/var.
	// Direct execution uses the physical directory; shell pwd may keep aliases.
	full, err := filepath.EvalSymlinks(full)
	if err != nil {
		t.Fatal(err)
	}
	c := Command{Argv: []string{"sh", "-c", "pwd"}, Cwd: dir}
	var out bytes.Buffer
	code, err := Execute(c, root, &out, &out)
	if err != nil || code != 0 || strings.TrimSpace(out.String()) != full {
		t.Fatalf("%d %v %q", code, err, out.String())
	}
	shell := exec.Command("sh", "-c", c.String())
	shell.Dir = root
	b, err := shell.CombinedOutput()
	// The rendered shell command may preserve a logical /var path in pwd.
	renderedPath, pathErr := filepath.EvalSymlinks(strings.TrimSpace(string(b)))
	if err != nil || pathErr != nil || renderedPath != full {
		t.Fatalf("rendered cwd/quoting mismatch: %v (path: %v) %s", err, pathErr, b)
	}
	c.Cwd = "../outside"
	if _, err := Execute(c, root, &out, &out); err == nil {
		t.Fatal("cwd escape accepted")
	}
}
