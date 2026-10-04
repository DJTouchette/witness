package planner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Opt-in toolchain checks run only scratch manifests and a local fake JS runner.
// No app suite, restore, install, database, or remote service is involved.
func TestDotnetManifestUnderMSBuild(t *testing.T) {
	tool := os.Getenv("WITNESS_DOTNET")
	if tool == "" {
		t.Skip("set WITNESS_DOTNET to an installed SDK binary for offline manifest evaluation")
	}
	root := tree(t, map[string]string{
		"backend/src/Core/Core.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup></Project>`,
		"backend/checks/Tests.csproj":  `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><IsTestProject>true</IsTestProject></PropertyGroup><Import Project="Refs.props"/></Project>`,
		"backend/checks/Refs.props":    `<Project><ItemGroup><ProjectReference Include="../src/Core/Core.csproj"/></ItemGroup></Project>`,
	})
	p, err := Build(root, selection("backend/src/Core/Value.cs"))
	if err != nil || len(p.Commands) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	target := p.Commands[0].Argv[2]
	cmd := exec.Command(tool, "msbuild", target, "-getProperty:IsTestProject", "-getItem:ProjectReference", "-nologo")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DOTNET_CLI_HOME="+t.TempDir(), "DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1", "DOTNET_NOLOGO=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1")
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("MSBuild: %v %s", err, b)
	}
	if !strings.Contains(string(b), `"IsTestProject": "true"`) || !strings.Contains(string(b), "Core.csproj") {
		t.Fatalf("MSBuild disagrees with plan: %s", b)
	}
}

func TestNpmCommandResolvesLocalRunnerOffline(t *testing.T) {
	tool := os.Getenv("WITNESS_NPM")
	if tool == "" {
		t.Skip("set WITNESS_NPM to an installed npm binary for offline argv/cwd validation")
	}
	root := tree(t, map[string]string{
		"web/package.json":                     `{"name":"fixture","version":"1.0.0","devDependencies":{"vitest":"4.0.0"}}`,
		"web/node_modules/vitest/package.json": `{"name":"vitest","version":"4.0.0","bin":{"vitest":"cli.js"}}`,
		"web/node_modules/vitest/cli.js":       "#!/usr/bin/env node\nconsole.log(JSON.stringify({cwd:process.cwd(),argv:process.argv.slice(2)}));\n",
		"web/node_modules/.bin/placeholder":    "",
		"empty-npmrc":                          "",
	})
	binary := filepath.Join(root, "web/node_modules/vitest/cli.js")
	if err := os.Chmod(binary, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../vitest/cli.js", filepath.Join(root, "web/node_modules/.bin/vitest")); err != nil {
		t.Skip(err)
	}
	p, err := Build(root, selection("web/src/value.ts"))
	if err != nil {
		t.Fatal(err)
	}
	c := p.Commands[0]
	cmd := exec.Command(tool, c.Argv[1:]...)
	cmd.Dir = filepath.Join(root, c.Cwd)
	cmd.Env = append(os.Environ(), "npm_config_offline=true", "npm_config_cache="+t.TempDir(), "npm_config_userconfig="+filepath.Join(root, "empty-npmrc"))
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("npm offline invocation: %v %s", err, b)
	}
	var got struct {
		Cwd  string   `json:"cwd"`
		Argv []string `json:"argv"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	if got.Cwd != filepath.Join(root, "web") || len(got.Argv) != 1 || got.Argv[0] != "run" {
		t.Fatalf("wrong cwd/argv: %+v", got)
	}
}
