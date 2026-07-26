//go:build windows

package runner

import (
	"os"
	"os/exec"
)

// setProcessGroup is a no-op: Windows has no process groups in the POSIX
// sense, so cancellation falls back to killing the process itself.
func setProcessGroup(cmd *exec.Cmd) {}

// terminateGroup kills the test command. Windows has no SIGTERM, so there is
// no graceful phase to wait out.
func terminateGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}

// signalExitCode always returns -1: Windows processes are not signalled, so
// ExitCode() is authoritative.
func signalExitCode(state *os.ProcessState) int { return -1 }
