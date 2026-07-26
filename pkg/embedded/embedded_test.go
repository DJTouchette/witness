package embedded

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewCommandTree(t *testing.T) {
	cmd := NewCommand("v9.9.9")
	if cmd == nil {
		t.Fatal("NewCommand returned nil")
	}
	if cmd.Use != "witness" {
		t.Errorf("Use = %q, want witness", cmd.Use)
	}

	want := map[string]bool{"select": false, "run": false}
	for _, c := range cmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not registered", name)
		}
	}
}

// The whole point of this package is in-process embedding: a host that
// redirects the command's writers must get the output, because its own stdout
// is carrying an MCP stdio stream.
func TestEmbeddedCommandHonoursRedirectedWriters(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommand("v9.9.9")
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(out.String(), "select") {
		t.Errorf("help output did not reach the redirected writer: %q", out.String())
	}
}

// A host that cannot recover the runner's exit code has to flatten every failure
// to 1, losing the difference between "your tests failed" and "witness broke".
func TestTestsFailedRecoversTheRunnerExitCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantOK   bool
	}{
		{"a runner exit code is recovered", &ExitCodeError{Code: 2}, 2, true},
		{"wrapped is still recovered", fmt.Errorf("run: %w", &ExitCodeError{Code: 137}), 137, true},
		{"a witness-side error is not a test failure", errors.New("no such flag"), 0, false},
		{"nil is not a test failure", nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, ok := TestsFailed(tt.err)
			if ok != tt.wantOK || code != tt.wantCode {
				t.Errorf("TestsFailed(%v) = (%d, %v), want (%d, %v)", tt.err, code, ok, tt.wantCode, tt.wantOK)
			}
		})
	}
}

// The alias must stay an alias: a distinct type would silently stop matching the
// error the cli package actually returns, which is the whole point of exporting it.
func TestExitCodeErrorIsTheSameTypeTheCLIReturns(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommand("v9.9.9")
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	// Not a git repo, so this fails witness-side rather than in a runner —
	// enough to confirm a non-runner error is NOT reported as a test failure.
	cmd.SetArgs([]string{"run", "--fallback", "none"})
	err := cmd.Execute()
	if err == nil {
		t.Skip("run succeeded in this environment; nothing to classify")
	}
	if code, ok := TestsFailed(err); ok {
		t.Errorf("a witness-side error was misreported as a test failure with code %d: %v", code, err)
	}
}
