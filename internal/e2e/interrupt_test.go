//go:build !windows

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestInterruptStopsTheRunnerNotJustWitness drives the real binary the way a
// terminal does: Ctrl-C delivers SIGINT to witness, and the test runner it
// started must die with it.
//
// The runner is deliberately in its own process group so an embedding host can
// cancel it without signalling itself — which also means the terminal's SIGINT
// never reaches it. Without main() turning the signal into a cancelled context,
// interrupting a long `go test ./...` left it running with no parent, burning
// CPU where a second Ctrl-C could not reach it.
func TestInterruptStopsTheRunnerNotJustWitness(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
	r := newRepo(t, "go")
	r.append(t, "calc/calc.go", "\n// Sub returns a - b.\nfunc Sub(a, b int) int { return a - b }\n")

	pidFile := filepath.Join(t.TempDir(), "runner.pid")
	cmd := exec.Command(witnessBinary(t), "run", "--fallback", "none",
		"--test-cmd", "sh -c 'echo $$ > "+pidFile+"; sleep 120'")
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting witness: %v", err)
	}

	runnerPID := waitForRunnerPID(t, pidFile)
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("interrupting witness: %v", err)
	}

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case <-waited:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("witness did not exit after SIGINT")
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(runnerPID, 0); err != nil {
			return // the runner went down with witness
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(runnerPID, syscall.SIGKILL)
	t.Errorf("REGRESSION (orphaned runner): the test command (pid %d) outlived the interrupted witness", runnerPID)
}

func waitForRunnerPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the test runner never started (no pid in %s)", path)
	return 0
}
