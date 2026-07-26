//go:build !windows

package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSignalContextCancelsOnInterrupt pins the wiring main() depends on: the
// context handed to the command tree has to end when the terminal interrupts
// witness.
func TestSignalContextCancelsOnInterrupt(t *testing.T) {
	ctx, stop := SignalContext(context.Background())
	defer stop()

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("raising SIGINT: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SignalContext did not cancel on SIGINT; Ctrl-C would kill witness and orphan the running test suite")
	}
}

// TestInterruptStopsTheRunnerInsteadOfOrphaningIt is the product-level version:
// `witness run` is interrupted the way a terminal interrupts it, and the test
// command it started must die with it. The runner lives in its own process
// group, so nothing but this wiring can reach it.
func TestInterruptStopsTheRunnerInsteadOfOrphaningIt(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	repo := newTestRepo(t)
	t.Chdir(repo)

	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		root := NewRootCmd("v")
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs([]string{"run", "--fallback", "none",
			"--test-cmd", "sh -c 'echo $$ > " + pidFile + "; sleep 60'", "calc.go"})
		done <- root.ExecuteContext(ctx)
	}()

	pid := waitForPID(t, pidFile)
	cancel() // what SignalContext does when the terminal sends SIGINT

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("witness did not return after the interrupt")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // the runner died with witness
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Errorf("the test runner (pid %d) outlived the interrupted witness; it would keep burning CPU with no parent", pid)
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no pid written to %s", path)
	return 0
}
