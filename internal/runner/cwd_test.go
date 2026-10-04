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
	c := Command{Argv: []string{"sh", "-c", "pwd"}, Cwd: dir}
	var out bytes.Buffer
	code, err := Execute(c, root, &out, &out)
	if err != nil || code != 0 || strings.TrimSpace(out.String()) != full {
		t.Fatalf("%d %v %q", code, err, out.String())
	}
	shell := exec.Command("sh", "-c", c.String())
	shell.Dir = root
	b, err := shell.CombinedOutput()
	if err != nil || strings.TrimSpace(string(b)) != full {
		t.Fatalf("rendered cwd/quoting mismatch: %v %s", err, b)
	}
	c.Cwd = "../outside"
	if _, err := Execute(c, root, &out, &out); err == nil {
		t.Fatal("cwd escape accepted")
	}
}
