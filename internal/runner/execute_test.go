package runner

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestExecute_Success(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	var out bytes.Buffer
	code, err := Execute("echo hello", "", &out, &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("output = %q, want it to contain hello", out.String())
	}
}

func TestExecute_PropagatesExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	var out bytes.Buffer
	// A command that ran but failed is not a witness error: (code, nil).
	code, err := Execute("exit 3", "", &out, &out)
	if err != nil {
		t.Fatalf("a failing test command should not error: %v", err)
	}
	if code != 3 {
		t.Errorf("code = %d, want 3", code)
	}
}

func TestExecute_EmptyCommand(t *testing.T) {
	if _, err := Execute("   ", "", nil, nil); err == nil {
		t.Error("empty command should error")
	}
}

func TestExecute_LaunchFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	var out bytes.Buffer
	// sh runs but the inner command isn't found -> sh exits 127 (ran, so no err).
	code, err := Execute("this_binary_does_not_exist_zzz", "", &out, &out)
	if err != nil {
		t.Fatalf("sh ran, so no launch error expected: %v", err)
	}
	if code == 0 {
		t.Error("expected non-zero code for a missing inner command")
	}
}
