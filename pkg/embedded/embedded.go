// Package embedded exports witness's CLI command tree for embedding in other tools.
package embedded

import (
	"errors"

	"github.com/djtouchette/witness/cmd/witness/cli"
	"github.com/spf13/cobra"
)

// NewCommand returns witness's root cobra command.
// Callers can execute it directly or attach it as a subcommand.
func NewCommand(version string) *cobra.Command {
	return cli.NewRootCmd(version)
}

// ExitCodeError is returned by `run` when the test runner itself exited
// non-zero. It is re-exported here because an embedded host has no other way to
// tell "the tests failed, with this code" from "witness failed": the command
// cannot call os.Exit without killing the host, so the code travels as an error.
//
// Hosts should type-check it and propagate Code rather than flattening every
// failure to 1:
//
//	var ce *embedded.ExitCodeError
//	if errors.As(err, &ce) {
//		return ce.Code, nil // the tests ran and failed
//	}
type ExitCodeError = cli.ExitCodeError

// TestsFailed reports whether err carries a test-runner exit code, and returns
// that code. It returns (0, false) for a witness-side error, so a host can tell
// a real test failure from a usage or analysis error without importing errors.As
// plumbing of its own.
func TestsFailed(err error) (int, bool) {
	var ce *ExitCodeError
	if errors.As(err, &ce) {
		return ce.Code, true
	}
	return 0, false
}
