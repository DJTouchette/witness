package planner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDotnetImportedReferences(t *testing.T) {
	files := map[string]string{
		"src/Core/Core.csproj":           `<Project Sdk="Microsoft.NET.Sdk"/>`,
		"tests/Direct/Direct.csproj":     `<Project><PropertyGroup><IsTestProject>true</IsTestProject></PropertyGroup><ItemGroup><ProjectReference Include="../../src/Core/Core.csproj"/></ItemGroup></Project>`,
		"tests/Imported/Imported.csproj": `<Project><PropertyGroup><IsTestProject>true</IsTestProject></PropertyGroup><Import Project="Refs.props"/></Project>`,
		"tests/Imported/Refs.props":      `<Project><ItemGroup><ProjectReference Include="../../src/Core/Core.csproj"/></ItemGroup></Project>`,
	}
	p, err := Build(tree(t, files), selection("src/Core/Core.cs"))
	if err != nil || p.Status != "ready" || len(p.Commands) != 2 {
		t.Fatalf("imported target lost: %+v %v", p, err)
	}
}
func TestDotnetUnresolvableImportsRefuseCompleteness(t *testing.T) {
	for _, imp := range []string{`<Import Project="missing.props"/>`, `<Import Project="$(Unknown)/Refs.props"/>`, `<Import Project="cycle.props"/>`, `<Import Project="missing.props" Condition="'$(Unresolved)' == 'true'"/>`} {
		files := map[string]string{
			"Core/Core.csproj":   `<Project/>`,
			"Tests/Tests.csproj": `<Project><PropertyGroup><IsTestProject>true</IsTestProject></PropertyGroup><ItemGroup><ProjectReference Include="../Core/Core.csproj"/></ItemGroup>` + imp + `</Project>`,
			"Tests/cycle.props":  `<Project><Import Project="cycle.props"/></Project>`,
		}
		p, err := Build(tree(t, files), selection("Core/a.cs"))
		if err == nil || p.Status != "incomplete" || len(p.Diagnostics) == 0 {
			t.Errorf("%s: %+v %v", imp, p, err)
		}
	}
}
func TestDotnetDirectoryBuildAndGlobImports(t *testing.T) {
	files := map[string]string{
		"Core/Core.csproj":              `<Project/>`,
		"tests/Directory.Build.props":   `<Project><PropertyGroup><IsTestProject>true</IsTestProject><CorePath>../../Core/Core.csproj</CorePath></PropertyGroup></Project>`,
		"tests/Directory.Build.targets": `<Project><Import Project="refs/*.props"/></Project>`,
		"tests/refs/core.props":         `<Project><ItemGroup Condition="'$(Configuration)' == 'Release'"><ProjectReference Include="$(CorePath)"/></ItemGroup></Project>`,
		"tests/check/Checks.csproj":     `<Project Sdk="Microsoft.NET.Sdk"/>`,
	}
	p, err := Build(tree(t, files), selection("Core/a.cs"))
	if err != nil || len(p.Commands) != 1 || !strings.Contains(p.Commands[0].Argv[2], "Checks.csproj") {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestDotnetConservativeConditionAndPropertyCases(t *testing.T) {
	cases := []struct {
		name, body string
		ready      bool
	}{
		{"condition union", `<ItemGroup Condition="'$(Configuration)' == 'Release'"><ProjectReference Include="../Core/Core.csproj"/></ItemGroup>`, true},
		{"properties before items", `<ItemGroup><ProjectReference Include="$(CorePath)"/></ItemGroup><PropertyGroup><CorePath>../Core/Core.csproj</CorePath></PropertyGroup>`, true},
		{"conditional property", `<PropertyGroup><CorePath>../Core/Core.csproj</CorePath></PropertyGroup><PropertyGroup Condition="'$(Mode)' == 'Other'"><CorePath>../Other/Other.csproj</CorePath></PropertyGroup><ItemGroup><ProjectReference Include="$(CorePath)"/></ItemGroup>`, false},
		{"choose property", `<Choose><When Condition="'$(Mode)' == 'Other'"><PropertyGroup><CorePath>../Core/Core.csproj</CorePath></PropertyGroup></When></Choose><ItemGroup><ProjectReference Include="$(CorePath)"/></ItemGroup>`, false},
		{"case mismatch", `<ItemGroup><ProjectReference Include="../core/core.csproj"/></ItemGroup>`, false},
		{"target mutation", `<Target Name="AddReferences"><ItemGroup><ProjectReference Include="../Core/Core.csproj"/></ItemGroup></Target>`, false},
		{"directory property", `<Import Project="$(MSBuildThisFileDirectory)../shared/refs.props"/>`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"Core/Core.csproj": `<Project/>`, "Tests/Tests.csproj": `<Project><PropertyGroup><IsTestProject>true</IsTestProject></PropertyGroup>` + c.body + `</Project>`, "shared/refs.props": `<Project><ItemGroup><ProjectReference Include="../Core/Core.csproj"/></ItemGroup></Project>`}
			p, err := Build(tree(t, files), selection("Core/a.cs"))
			if c.ready {
				if err != nil || len(p.Commands) != 1 {
					t.Fatalf("%+v %v", p, err)
				}
			} else if err == nil || p.Status != "incomplete" {
				t.Fatalf("%+v %v", p, err)
			}
		})
	}
}

func TestDotnetInRepositoryAliasReference(t *testing.T) {
	root := tree(t, map[string]string{"Core/Core.csproj": `<Project/>`, "Tests/Tests.csproj": `<Project><PropertyGroup><IsTestProject>true</IsTestProject></PropertyGroup><ItemGroup><ProjectReference Include="../Alias/Core.csproj"/></ItemGroup></Project>`})
	if err := os.Symlink(filepath.Join(root, "Core"), filepath.Join(root, "Alias")); err != nil {
		t.Skip(err)
	}
	p, err := Build(root, selection("Core/a.cs"))
	if err != nil || len(p.Commands) != 1 {
		t.Fatalf("alias lost reverse edge: %+v %v", p, err)
	}
}
