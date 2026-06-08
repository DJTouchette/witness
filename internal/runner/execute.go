package runner

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
)

// Execute runs a formatted test command in dir, streaming stdout/stderr to the
// given writers, and returns the process exit code.
//
// A non-zero exit because tests failed is NOT a witness error: it returns
// (code, nil) so callers propagate the code without treating it as a launch
// failure. Only an inability to start the command (or an empty command) returns
// a non-nil error.
func Execute(command, dir string, stdout, stderr io.Writer) (int, error) {
	if strings.TrimSpace(command) == "" {
		return 0, fmt.Errorf("no test command to run")
	}
	name, args := shellInvocation(command)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// The command ran and exited non-zero — tests failed. Surface the code.
		return ee.ExitCode(), nil
	}
	return -1, fmt.Errorf("starting test command: %w", err)
}

// shellInvocation wraps a command string for the host shell.
func shellInvocation(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", command}
	}
	return "sh", []string{"-c", command}
}
