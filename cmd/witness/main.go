package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/djtouchette/witness/cmd/witness/cli"
)

// version is overridden at build time via -ldflags. When unset, we fall back
// to the module version baked in by `go install ...@vX.Y.Z`.
var version = "dev"

func main() {
	// Ctrl-C has to reach the test runner. It runs in its own process group
	// (so an embedding host can cancel it without signalling itself), which
	// means the terminal's SIGINT lands on witness alone — without this the
	// suite was orphaned and kept running. Installed here rather than in the
	// command tree because signal handlers are process-wide and an embedder
	// must keep its own.
	ctx, stop := cli.SignalContext(context.Background())
	defer stop()

	cmd := cli.NewRootCmd(resolveVersion())
	if err := cmd.ExecuteContext(ctx); err != nil {
		os.Exit(reportError(os.Stderr, err))
	}
}

// reportError turns a command error into the process's exit status, printing
// whatever the user needs to see first. Split out of main so the mapping can be
// tested; main itself is the only caller.
func reportError(w io.Writer, err error) int {
	// `witness run` reports the test runner's exit code this way so we can exit
	// with it (the command can't call os.Exit — see ExitCodeError).
	var ce *cli.ExitCodeError
	if errors.As(err, &ce) {
		if code := ce.Code; code >= 1 && code <= 255 {
			return code
		}
		// Anything else cannot be handed to os.Exit as it stands. Unix keeps
		// only the low 8 bits, so a code of 256 would exit 0 — witness
		// reporting a failed suite as a pass, the exact false green the gate
		// exists to prevent. A negative code is the "never ran" sentinel
		// (internal/runner's Windows signalExitCode still returns -1), and a
		// zero contradicts the error that carries it. All of them become a
		// plain 1: information is lost, but never the failure itself.
		fmt.Fprintf(w, "%v\nwitness: exit code %d cannot be reported as a process status; exiting 1\n", err, ce.Code)
		return 1
	}
	fmt.Fprintln(w, err)
	return 1
}

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return version
	}
	return info.Main.Version
}
