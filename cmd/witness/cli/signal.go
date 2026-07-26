package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// SignalContext returns a context cancelled by SIGINT or SIGTERM, plus the stop
// function that restores the default handlers.
//
// It exists for the standalone binary, and must NOT be installed by an
// embedding host: os/signal handlers are process-wide, so witness taking them
// over would swallow the host's own Ctrl-C handling.
//
// Why it is needed at all: the test runner is started in its OWN process group
// so that an embedder cancelling a context can stop the runner and everything
// it spawned without signalling itself. That also means the terminal's SIGINT
// no longer reaches the runner — witness died on Ctrl-C and left `go test` (or
// `dotnet test`) running with no parent, and a second Ctrl-C could not reach it
// either. Cancelling the context on the signal puts the two back together:
// runner.ExecuteContext SIGTERMs the child's whole group and then SIGKILLs
// whatever is left.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
