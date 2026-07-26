//go:build !windows

package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecuteContext_CancelKillsGrandchildren(t *testing.T) {
	skipWithoutSh(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var out bytes.Buffer
		// The shell stays alive waiting, so the sleep is a grandchild of
		// witness: only a process-group signal reaches it.
		_, _ = ExecuteContext(ctx, sh("sleep 60 & echo $! > "+pidFile+"; wait"), dir, &out, &out)
	}()

	pid := waitForPID(t, pidFile)
	cancel()
	<-done

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // gone
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Errorf("grandchild pid %d survived cancellation", pid)
}

// waitForPID reads a pid written by a child shell, waiting for the file.
func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
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
