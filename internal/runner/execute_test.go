package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// sh builds a Command that runs a shell snippet. Only tests do this: witness
// itself never shells out.
func sh(snippet string) Command {
	return Command{Lang: "test", Argv: []string{"sh", "-c", snippet}}
}

func skipWithoutSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
}

func TestExecute_Success(t *testing.T) {
	skipWithoutSh(t)
	var out bytes.Buffer
	code, err := Execute(sh("echo hello"), "", &out, &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if out.String() != "hello\n" {
		t.Errorf("output = %q, want %q", out.String(), "hello\n")
	}
}

func TestExecute_PropagatesExitCode(t *testing.T) {
	skipWithoutSh(t)
	var out bytes.Buffer
	// A command that ran but failed is not a witness error: (code, nil).
	code, err := Execute(sh("exit 3"), "", &out, &out)
	if err != nil {
		t.Fatalf("a failing test command should not error: %v", err)
	}
	if code != 3 {
		t.Errorf("code = %d, want 3", code)
	}
}

func TestExecute_EmptyCommand(t *testing.T) {
	code, err := Execute(Command{}, "", nil, nil)
	if err == nil {
		t.Fatal("empty command should error")
	}
	if code != -1 {
		t.Errorf("code = %d, want -1 (never ran)", code)
	}
}

func TestExecute_LaunchFailure(t *testing.T) {
	var out bytes.Buffer
	// No shell to swallow it any more: a missing runner is a launch failure,
	// reported as (-1, err) rather than as a test exit code.
	code, err := Execute(Command{Argv: []string{"this_binary_does_not_exist_zzz"}}, "", &out, &out)
	if err == nil {
		t.Fatal("a missing binary should be a launch error")
	}
	if code != -1 {
		t.Errorf("code = %d, want -1", code)
	}
}

func TestExecute_NoShellInterpretation(t *testing.T) {
	skipWithoutSh(t)
	// A path with spaces, parens, a semicolon and a command substitution: the
	// old `sh -c` string interpolation word-split it, failed with a syntax
	// error, or ran the substitution. argv passes it through untouched.
	dir := t.TempDir()
	name := "weird $(touch pwned) name (marketing);.txt"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("contents"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var out bytes.Buffer
	code, err := Execute(Command{Argv: []string{"cat", name}}, dir, &out, &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if code != 0 {
		t.Fatalf("code = %d (output %q), want 0", code, out.String())
	}
	if out.String() != "contents" {
		t.Errorf("output = %q, want %q", out.String(), "contents")
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Error("the command substitution in the path was executed")
	}
}

func TestExecute_SignalledProcessReportsShellCode(t *testing.T) {
	skipWithoutSh(t)
	if runtime.GOOS == "windows" {
		t.Skip("no signals")
	}
	tests := []struct {
		name    string
		snippet string
		want    int
	}{
		{"SIGTERM", "kill -TERM $$", 143},
		{"SIGKILL", "kill -KILL $$", 137},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			// -1 used to be returned here, colliding with the "never ran"
			// sentinel and exiting the process with status 255.
			code, err := Execute(sh(tt.snippet), "", &out, &out)
			if err != nil {
				t.Fatalf("a signalled test command should not be a launch error: %v", err)
			}
			if code != tt.want {
				t.Errorf("code = %d, want %d", code, tt.want)
			}
		})
	}
}

func TestExecuteContext_CancelStopsAHungSuite(t *testing.T) {
	skipWithoutSh(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	var out bytes.Buffer
	start := time.Now()
	code, err := ExecuteContext(ctx, sh("sleep 60"), "", &out, &out)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a cancelled run must report an error, not a clean pass")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if code == 0 {
		t.Error("code = 0; a cancelled run must not look like a pass")
	}
	if elapsed > 10*time.Second {
		t.Errorf("took %s; the command was not killed promptly", elapsed)
	}
}

func TestExecuteAll_RunsEveryCommandAndReportsTheWorstCode(t *testing.T) {
	skipWithoutSh(t)
	var out bytes.Buffer
	// The old `&&` chain short-circuited: the second project never ran.
	code, err := ExecuteAll(context.Background(), []Command{
		sh("echo first; exit 3"),
		sh("echo second"),
		sh("echo third; exit 2"),
	}, "", &out, &out)
	if err != nil {
		t.Fatalf("ExecuteAll: %v", err)
	}
	if code != 3 {
		t.Errorf("code = %d, want the worst code 3", code)
	}
	if out.String() != "first\nsecond\nthird\n" {
		t.Errorf("output = %q, want every command to have run", out.String())
	}
}

func TestExecuteAll_LaunchFailureIsReportedButOthersStillRun(t *testing.T) {
	skipWithoutSh(t)
	var out bytes.Buffer
	code, err := ExecuteAll(context.Background(), []Command{
		{Argv: []string{"this_binary_does_not_exist_zzz"}},
		sh("echo second"),
	}, "", &out, &out)
	if err == nil {
		t.Fatal("a launch failure must be reported")
	}
	if out.String() != "second\n" {
		t.Errorf("output = %q, want the second command to have run", out.String())
	}
	if code != 0 {
		t.Errorf("code = %d, want 0 (the error carries the failure)", code)
	}
}

func TestExecuteAll_NoCommands(t *testing.T) {
	code, err := ExecuteAll(context.Background(), nil, "", nil, nil)
	if err == nil {
		t.Fatal("no commands should error rather than report a pass")
	}
	if code != -1 {
		t.Errorf("code = %d, want -1", code)
	}
}

func TestExecuteAll_StopsOnceCancelled(t *testing.T) {
	skipWithoutSh(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out bytes.Buffer
	code, err := ExecuteAll(ctx, []Command{sh("echo first"), sh("echo second")}, "", &out, &out)
	if err == nil {
		t.Fatal("a cancelled run must report an error")
	}
	if code == 0 {
		t.Error("code = 0; a cancelled run must not look like a pass")
	}
	if strings.Contains(out.String(), "second") {
		t.Errorf("output = %q; the second command should not have been launched", out.String())
	}
}
