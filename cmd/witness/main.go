package main

import (
	"os"

	"github.com/djtouchette/witness/cmd/witness/cli"
)

var version = "dev"

func main() {
	cmd := cli.NewRootCmd(version)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
