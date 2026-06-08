package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/djtouchette/witness/cmd/witness/cli"
)

// version is overridden at build time via -ldflags. When unset, we fall back
// to the module version baked in by `go install ...@vX.Y.Z`.
var version = "dev"

func main() {
	cmd := cli.NewRootCmd(resolveVersion())
	if err := cmd.Execute(); err != nil {
		// `witness run` reports the test runner's exit code this way so we can
		// exit with it (the command can't call os.Exit — see ExitCodeError).
		var ce *cli.ExitCodeError
		if errors.As(err, &ce) {
			os.Exit(ce.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
