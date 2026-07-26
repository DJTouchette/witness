package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// killGrace is how long a cancelled test command has to exit after SIGTERM
// before it is SIGKILLed.
const killGrace = 5 * time.Second

// sigkillExitCode is the shell convention for a SIGKILLed process (128 + 9).
const sigkillExitCode = 137

// Execute runs a test command in dir with no cancellation. It is a thin
// wrapper over ExecuteContext for callers that have no context to pass.
func Execute(command Command, dir string, stdout, stderr io.Writer) (int, error) {
	return ExecuteContext(context.Background(), command, dir, stdout, stderr)
}

// ExecuteContext runs a test command in dir, streaming stdout/stderr to the
// given writers, and returns the process exit code.
//
// The command's argv is executed directly — never through a shell — so test
// paths containing spaces, parens or metacharacters are passed through intact
// and cannot be reinterpreted as shell syntax.
//
// A non-zero exit because tests failed is NOT a witness error: it returns
// (code, nil) so callers propagate the code without treating it as a launch
// failure. A process killed by a signal reports the shell convention 128+signal
// (137 SIGKILL, 143 SIGTERM), never -1. Only an inability to start the command,
// an empty command, or cancellation returns a non-nil error; -1 is reserved for
// "never ran".
//
// When ctx is cancelled the child's whole process group is SIGTERMed and then,
// after killGrace, SIGKILLed — so an embedding host can stop a hung suite
// without leaking the runner's children.
func ExecuteContext(ctx context.Context, command Command, dir string, stdout, stderr io.Writer) (int, error) {
	if len(command.Argv) == 0 || command.Argv[0] == "" {
		return -1, fmt.Errorf("no test command to run")
	}

	cmd := exec.CommandContext(ctx, command.Argv[0], command.Argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Own process group: cancellation can then signal the runner and every
	// process it spawned, without reaching the host that embedded witness.
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return terminateGroup(cmd) }
	cmd.WaitDelay = killGrace

	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return exitCodeOf(cmd.ProcessState), fmt.Errorf("running %s: %w", command, ctxErr)
	}
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// The command ran and exited non-zero — tests failed. Surface the code.
		return exitCodeOf(ee.ProcessState), nil
	}
	return -1, fmt.Errorf("starting test command %s: %w", command, err)
}

// ExecuteAll runs every command in turn and returns the worst (highest) exit
// code seen.
//
// Every command runs even if an earlier one fails: a polyglot or multi-project
// selection must not have half its suites skipped because the first one was
// red. Launch failures are collected and returned joined, so a caller that
// treats a non-nil error as fatal still fails closed.
func ExecuteAll(ctx context.Context, commands []Command, dir string, stdout, stderr io.Writer) (int, error) {
	if len(commands) == 0 {
		return -1, fmt.Errorf("no test command to run")
	}

	worst := 0
	var errs []error
	for _, command := range commands {
		code, err := ExecuteContext(ctx, command, dir, stdout, stderr)
		if err != nil {
			errs = append(errs, err)
		}
		if code > worst {
			worst = code
		}
		// A cancelled context will not un-cancel: stop rather than launching
		// the remaining suites just to have them killed.
		if ctx.Err() != nil {
			break
		}
	}
	return worst, errors.Join(errs...)
}

// exitCodeOf maps a finished process state to a shell-style exit code,
// reporting 128+signal for a signal-killed process rather than -1.
func exitCodeOf(state *os.ProcessState) int {
	if state == nil {
		// Killed before the state was recorded; report it as a SIGKILL rather
		// than as the -1 that means "never ran".
		return sigkillExitCode
	}
	if code := signalExitCode(state); code >= 0 {
		return code
	}
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	return 1
}
