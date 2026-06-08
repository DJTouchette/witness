package main

import (
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
