package planner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCargoPatchFullScope(t *testing.T) {
	for _, source := range []string{"crates-io", "alternative", "https://example.invalid/registry"} {
		for _, group := range []string{"dependencies", "dev-dependencies", "build-dependencies", "target.'cfg(unix)'.dependencies", "target.'cfg(windows)'.build-dependencies", "target.'cfg(unix)'.dev-dependencies"} {
			for _, dep := range []string{`"0.1"`, `{version="=0.1.0", package="core", optional=true}`, `{version=">=2", package="core"}`, `{workspace=true}`} {
				t.Run(source+"/"+group+"/"+dep, func(t *testing.T) {
					root := tree(t, map[string]string{
						"Cargo.toml":      "[workspace]\nmembers=['core','app','indirect']\n[workspace.dependencies]\nrenamed={version='0.1', package='core'}\n[patch.'" + source + "']\npatch_alias={path='alias', package='core', version='0.1'}\n",
						"core/Cargo.toml": "[package]\nname='core'\nversion='0.1.0'\n",
						"core/src/lib.rs": "",
						"app/Cargo.toml":  "[package]\nname='app'\nversion='0.1.0'\n[" + group + "]\nrenamed=" + dep + "\n",
						// A registry dependency may itself depend on core. Name/version
						// matching at the local manifests cannot prove this package unaffected.
						"indirect/Cargo.toml": "[package]\nname='indirect'\nversion='0.1.0'\n[dependencies]\nexternal='1'\n",
					})
					cargoLink(t, root, "alias", "core")
					p, err := Build(root, selection("core/src/lib.rs"))
					want := []string{"./Cargo.toml", "./app/Cargo.toml", "./core/Cargo.toml", "./indirect/Cargo.toml"}
					if err != nil || p.Status != "ready" || !reflect.DeepEqual(cargoTargets(p), want) {
						t.Fatalf("%+v %v", p, err)
					}
					for _, c := range p.Commands {
						if !strings.Contains(c.Reason, "conservative full") {
							t.Fatal(c)
						}
					}
				})
			}
		}
	}
}

func TestCargoStandalonePatch(t *testing.T) {
	root := tree(t, map[string]string{
		"core/Cargo.toml": "[package]\nname='core'\nversion='0.1.0'\n",
		"app/Cargo.toml":  "[package]\nname='app'\nversion='0.1.0'\n[dependencies]\nrenamed={package='core',version='0.1'}\n[patch.crates-io]\nother={package='core',path='../core'}\n",
	})
	p, err := Build(root, selection("core/src/lib.rs"))
	if err != nil || !reflect.DeepEqual(cargoTargets(p), []string{"./app/Cargo.toml", "./core/Cargo.toml"}) {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestCargoUnknownGraphForms(t *testing.T) {
	for _, tc := range []struct{ file, body, diagnostic string }{
		{"Cargo.toml", "[replace]\n'core:0.1.0'={path='core'}", "[replace]"},
		{"Cargo.toml", "[patch.crates-io]\ncore={git='https://example.invalid/core'}", "only local path"},
		{"Cargo.toml", "[patch.crates-io]\ncore={path='core', new-source='unknown'}", "new-source"},
		{"Cargo.toml", "[future-dependencies]\ncore={path='core'}", "future-dependencies"},
		{"Cargo.toml", "[dependencies]\ncore={path=42}", "dependency field path"},
		{"Cargo.toml", "[dependencies]\ncore=42", "unsupported Cargo dependency"},
		{"Cargo.toml", "[dev_dependencies]\ncore={path='core'}", "dev_dependencies"},
		{"Cargo.toml", "[workspace.future-dependencies]\ncore={path='core'}", "future-dependencies"},
		{"Cargo.toml", "[target.'cfg(unix)'.future-dependencies]\ncore={path='core'}", "future-dependencies"},
		{".cargo/config", "paths=['core']", "config override \"paths\""},
		{".cargo/config.toml", "[patch.crates-io]\ncore={path='core'}", "config override \"patch\""},
		{"app/.cargo/config.toml", "[source.crates-io]\nreplace-with='local'", "config override \"source\""},
		{".cargo/config.toml", "include=['other.toml']", "config override \"include\""},
		{".cargo/config.toml", "future-overrides=[]", "future-overrides"},
		{".cargo/config.toml", "invalid=[", "Cargo config"},
	} {
		t.Run(tc.file+tc.body, func(t *testing.T) {
			files := map[string]string{"Cargo.toml": "[workspace]\nmembers=['core','app']\n", "core/Cargo.toml": "[package]\nname='core'\nversion='0.1.0'\n", "app/Cargo.toml": "[package]\nname='app'\nversion='0.1.0'\n"}
			files[tc.file] += tc.body
			root := tree(t, files)
			for i := 0; i < 2; i++ {
				p, err := Build(root, selection("core/src/lib.rs"))
				if err == nil || p.Status != "incomplete" || p.Coverage != "unknown" || len(p.Commands) == 0 || !strings.Contains(strings.Join(p.Diagnostics, "\n"), tc.diagnostic) {
					t.Fatalf("%+v %v", p, err)
				}
			}
		})
	}
}

func TestCargoPatchBoundaryAndIdentity(t *testing.T) {
	for _, local := range []string{"core", "alias", "nested/deep/../../core", "deep-alias/../core", "copy", "missing", "missing/../core", "outside", "loop", "dangling", "target/hidden", "CORE", "/absolute", "C:/core"} {
		t.Run(local, func(t *testing.T) {
			root := tree(t, map[string]string{
				"Cargo.toml":              "[workspace]\nmembers=['core','app']\n[patch.crates-io]\ncore={path='" + local + "'}\n",
				"core/Cargo.toml":         "[package]\nname='core'\nversion='0.1.0'\n",
				"app/Cargo.toml":          "[package]\nname='app'\nversion='0.1.0'\n[dependencies]\ncore='0.1'\n",
				"nested/deep/placeholder": "", "nested/core/placeholder": "", "copy/src/lib.rs": "", "target/hidden/Cargo.toml": "[package]\nname='core'\n",
			})
			cargoLink(t, root, "alias", "core")
			cargoLink(t, root, "deep-alias", "nested/deep")
			cargoLink(t, root, "copy/Cargo.toml", "../core/Cargo.toml")
			cargoLink(t, root, "outside", t.TempDir())
			cargoLink(t, root, "loop", "loop")
			cargoLink(t, root, "dangling", "missing")
			if local == "CORE" {
				if _, err := os.Stat(filepath.Join(root, "CORE")); err == nil {
					t.Skip("case insensitive filesystem")
				}
			}
			p, err := Build(root, selection("core/src/lib.rs"))
			valid := local == "core" || local == "alias" || local == "nested/deep/../../core" || local == "deep-alias/../core" || local == "copy"
			if valid {
				if err != nil || len(p.Commands) != 4 {
					t.Fatalf("%+v %v", p, err)
				}
			} else if err == nil || p.Status != "incomplete" || p.Coverage != "unknown" {
				t.Fatalf("%+v %v", p, err)
			}
		})
	}
}

func TestCargoConfigPhysicalBoundary(t *testing.T) {
	root := tree(t, map[string]string{"core/Cargo.toml": "[package]\nname='core'\nversion='0.1.0'\n"})
	outside := tree(t, map[string]string{"config.toml": "paths=['somewhere']\n"})
	cargoLink(t, root, ".cargo", outside)
	p, err := Build(root, selection("core/src/lib.rs"))
	if err == nil || p.Status != "incomplete" || p.Coverage != "unknown" || len(p.Commands) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestCargoOverrideAllowlistMatchesWholeKeys(t *testing.T) {
	// Quoted TOML keys may contain spaces: a substring is not a declaration.
	issues := cargoUnknownKeys(map[string]any{"path package": "core"}, "fixture", "version path package registry")
	if len(issues) != 1 {
		t.Fatal(issues)
	}
}
