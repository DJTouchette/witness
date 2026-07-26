package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/djtouchette/witness/cmd/witness/cli"
)

// A test runner's own exit code is the product: `witness run` in CI must exit
// with what the suite exited with. But os.Exit cannot carry every int — Unix
// keeps the low 8 bits — so the codes it would mangle are reported as a plain 1
// instead. 256 is the case that matters: exiting 0 for a suite that failed is
// the false green witness exists to prevent.
func TestReportErrorClampsUnreportableExitCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
		// clamped codes and plain errors have to explain themselves; a
		// reportable runner code is carried silently.
		explains bool
	}{
		{"generic error", errors.New("boom"), 1, true},
		{"runner failure", &cli.ExitCodeError{Code: 1}, 1, false},
		{"runner signal code", &cli.ExitCodeError{Code: 137}, 137, false},
		{"highest reportable", &cli.ExitCodeError{Code: 255}, 255, false},
		// os.Exit(256) exits 0 on Unix: a failed suite reported as a pass.
		{"truncates to zero", &cli.ExitCodeError{Code: 256}, 1, true},
		// A Windows NTSTATUS (0xC0000005) arrives as a large positive int.
		{"windows status", &cli.ExitCodeError{Code: 3221225477}, 1, true},
		// internal/runner's Windows signalExitCode still answers -1, which is
		// witness's "never ran" sentinel, and os.Exit(-1) is 255 on Unix.
		{"never-ran sentinel", &cli.ExitCodeError{Code: -1}, 1, true},
		// Zero contradicts the error carrying it; exiting 0 on it is a false green.
		{"zero", &cli.ExitCodeError{Code: 0}, 1, true},
		// Wrapped, the way cobra hands errors back.
		{"wrapped", fmt.Errorf("run: %w", &cli.ExitCodeError{Code: 2}), 2, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if got := reportError(&out, tc.err); got != tc.want {
				t.Errorf("reportError(%v) = %d, want %d", tc.err, got, tc.want)
			}
			if explained := strings.TrimSpace(out.String()) != ""; explained != tc.explains {
				t.Errorf("output = %q, want explains = %v", out.String(), tc.explains)
			}
		})
	}
}

// A clamped code still has to explain itself: the number the runner reported is
// gone from the exit status, so it has to be in the output.
func TestReportErrorExplainsAClampedCode(t *testing.T) {
	var out bytes.Buffer
	reportError(&out, &cli.ExitCodeError{Code: 256})
	if !strings.Contains(out.String(), "256") {
		t.Errorf("output = %q, want it to name the code that could not be reported", out.String())
	}
}

// A test failure exits with the runner's code and prints nothing extra: the
// runner already streamed its own output, and "tests failed (exit code 1)" on
// top of it is noise.
func TestReportErrorIsQuietForAReportableTestFailure(t *testing.T) {
	var out bytes.Buffer
	if got := reportError(&out, &cli.ExitCodeError{Code: 3}); got != 3 {
		t.Errorf("reportError = %d, want 3", got)
	}
	if out.Len() != 0 {
		t.Errorf("output = %q, want nothing: the runner already reported the failure", out.String())
	}
}
