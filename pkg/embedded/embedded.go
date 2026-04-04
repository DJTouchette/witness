// Package embedded exports witness's CLI command tree for embedding in other tools.
package embedded

import (
	"github.com/djtouchette/witness/cmd/witness/cli"
	"github.com/spf13/cobra"
)

// NewCommand returns witness's root cobra command.
// Callers can execute it directly or attach it as a subcommand.
func NewCommand(version string) *cobra.Command {
	return cli.NewRootCmd(version)
}
