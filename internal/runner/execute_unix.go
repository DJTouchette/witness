//go:build !windows

package runner

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup puts the test command in its own process group, so it and
// everything it spawns can be signalled as a unit.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateGroup SIGTERMs the command's process group and schedules a SIGKILL
// for whatever is still alive after killGrace. Signalling the group rather
// than the pid means a compound runner's children die too.
func terminateGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	// Setpgid made the child a group leader, so its pid is the group id.
	pgid := cmd.Process.Pid
	err := syscall.Kill(-pgid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	time.AfterFunc(killGrace, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	return err
}

// signalExitCode returns 128+signal for a signal-killed process, or -1 if the
// process exited normally. os.ProcessState.ExitCode() reports -1 for a
// signalled process, which collides with witness's "never ran" sentinel.
func signalExitCode(state *os.ProcessState) int {
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return -1
}
