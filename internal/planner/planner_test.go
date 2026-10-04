package planner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/djtouchette/witness/internal/selector"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, s := range files {
		f := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(f), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func selection(paths ...string) *selector.SelectResult {
	return &selector.SelectResult{ChangedFiles: paths, Tests: []selector.ScoredTest{}, Summary: selector.Summary{Unmapped: paths}}
}
func TestMonorepoPlans(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		changes []string
		cwd     string
		argv    []string
	}{
		{"Workspacer native inline or new source", map[string]string{"apps/native/Cargo.toml": "[package]\nname='native'\nversion='0.1.0'\n", "services/hub-rs/Cargo.toml": "[package]\nname='hub'\nversion='0.1.0'\n"}, []string{"apps/native/src/ui.rs", "apps/native/src/new.rs"}, ".", []string{"cargo", "test", "--manifest-path", "./apps/native/Cargo.toml"}},
		{"Cassadol Vitest environment and Yarn", map[string]string{"yarn.lock": "", "clients/web/package.json": `{"devDependencies":{"vitest":"3"},"scripts":{"test":"vitest --environment=jsdom"}}`}, []string{"clients/web/src/util/history.ts"}, "clients/web", []string{"yarn", "run", "vitest", "run", "--environment=jsdom"}},
		{"Workspacer nested renderer config", map[string]string{"apps/desktop/package.json": `{"devDependencies":{"vitest":"4","@playwright/test":"1"}}`, "apps/desktop/src/renderer/vitest.config.ts": "throw new Error('must never evaluate config')"}, []string{"apps/desktop/src/renderer/widget.tsx"}, "apps/desktop/src/renderer", []string{"npm", "exec", "--no", "--", "vitest", "run"}},
		{"Cassadol infra Jest", map[string]string{"yarn.lock": "", "infrastructure/package.json": `{"devDependencies":{"jest":"26"},"scripts":{"test":"jest --watch"}}`}, []string{"infrastructure/lib/stack.ts"}, "infrastructure", []string{"yarn", "run", "jest", "--watch=false"}},
		{"nested Go", map[string]string{"old service/go.mod": "module example\n"}, []string{"old service/x.go"}, "old service", []string{"go", "test", "./..."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Build(tree(t, tt.files), selection(tt.changes...))
			if err != nil {
				t.Fatal(err)
			}
			if p.Status != "ready" || len(p.Commands) != 1 {
				t.Fatalf("%+v", p)
			}
			c := p.Commands[0]
			if c.Cwd != tt.cwd || !reflect.DeepEqual(c.Argv, tt.argv) {
				t.Fatalf("%+v, want %s %v", c, tt.cwd, tt.argv)
			}
			if len(p.Diagnostics) == 0 {
				t.Fatal("selection uncertainty lost")
			}
		})
	}
}
func TestDotnetTransitiveProjects(t *testing.T) {
	root := tree(t, map[string]string{
		"backend/src/Billing/Billing.csproj":          `<Project/>`,
		"backend/src/Api/Api.csproj":                  `<Project><ItemGroup><ProjectReference Include="..\Billing\Billing.csproj" /></ItemGroup></Project>`,
		"backend/checks/Unconventional/Actual.csproj": `<Project><ItemGroup><PackageReference Include="Microsoft.NET.Test.Sdk"/><ProjectReference Include="..\..\src\Api\Api.csproj"/></ItemGroup></Project>`,
		"backend/checks/Unrelated/Other.csproj":       `<Project><PropertyGroup><IsTestProject>true</IsTestProject></PropertyGroup></Project>`,
	})
	p, err := Build(root, selection("backend/src/Billing/Service.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Commands) != 1 || p.Commands[0].Argv[2] != "./backend/checks/Unconventional/Actual.csproj" {
		t.Fatalf("%+v", p)
	}
	// Nested test directories must resolve to a real manifest, not a name guess.
	p, err = Build(root, selection("backend/checks/Unconventional/deep/WhenPaid.cs"))
	if err != nil || len(p.Commands) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
}
func TestRustReversePathDependency(t *testing.T) {
	root := tree(t, map[string]string{
		"services/protocol/Cargo.toml":  "[package]\nname='protocol'\nversion='0.1.0'\n",
		"apps/native/Cargo.toml":        "[package]\nname='native'\nversion='0.1.0'\n[dependencies.protocol]\npath='../../services/protocol'\n",
		"services/unrelated/Cargo.toml": "[package]\nname='other'\nversion='0.1.0'\n",
	})
	p, err := Build(root, selection("services/protocol/src/lib.rs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Commands) != 2 {
		t.Fatalf("%+v", p)
	}
	for _, c := range p.Commands {
		if strings.Contains(strings.Join(c.Argv, " "), "unrelated") {
			t.Fatal(c)
		}
	}
}
func TestIncompleteNeverHidesPartialPlan(t *testing.T) {
	root := tree(t, map[string]string{"go.mod": "module example\n"})
	p, err := Build(root, selection("x.go", "unknown.xyz"))
	if err == nil || p.Status != "incomplete" || len(p.Commands) != 1 || len(p.Diagnostics) == 0 {
		t.Fatalf("%+v %v", p, err)
	}
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), `"schema_version":1`) {
		t.Fatal(string(b))
	}
	for _, f := range []string{"../external.rs", "/tmp/x.go", "C:/elsewhere/a.go"} {
		if _, err := Build(root, selection(f)); err == nil {
			t.Fatal(f)
		}
	}
}
func TestExplicitMappingAndNoEvaluation(t *testing.T) {
	root := tree(t, map[string]string{
		".witness.json":     `{"schema_version":1,"suites":[{"name":"custom","paths":["custom/**"],"cwd":"test suite","argv":["./runner","argument with spaces"]}]}`,
		"test suite/runner": "#!/bin/sh\ntouch SENTINEL\n",
	})
	p, err := Build(root, selection("custom/change.xyz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Commands) != 1 || p.Commands[0].Cwd != "test suite" {
		t.Fatalf("%+v", p)
	}
	if _, err := os.Stat(filepath.Join(root, "test suite/SENTINEL")); !os.IsNotExist(err) {
		t.Fatal("planner executed runner")
	}
}
func TestRefusesAmbiguousOrMalformedManifest(t *testing.T) {
	for _, files := range []map[string]string{
		{"package.json": `{"devDependencies":{"vitest":"4","jest":"29"}}`},
		{"package.json": "{broken"},
		{"package.json": `{"scripts":{"test":"echo passed"}}`},
	} {
		p, err := Build(tree(t, files), selection("src/a.ts"))
		if err == nil || p.Status != "incomplete" {
			t.Fatalf("%+v %v", p, err)
		}
	}
}

func TestSeveralDeclaredJSConfigsPlanEachSuite(t *testing.T) {
	root := tree(t, map[string]string{"desktop/package.json": `{"devDependencies":{"vitest":"4","@playwright/test":"1"}}`, "desktop/vitest.config.ts": "export default {}", "desktop/playwright.config.ts": "export default {}"})
	p, err := Build(root, selection("desktop/src/main.ts"))
	if err != nil || len(p.Commands) != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	runners := map[string]bool{}
	for _, c := range p.Commands {
		if c.Cwd != "desktop" {
			t.Fatal(c)
		}
		runners[c.Argv[4]] = true
	}
	if !runners["vitest"] || !runners["playwright"] {
		t.Fatal(runners)
	}
}
