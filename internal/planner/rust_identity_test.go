package planner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func cargoLink(t *testing.T, root, name, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}
func cargoTargets(p *Plan) []string {
	var targets []string
	for _, c := range p.Commands {
		targets = append(targets, c.Argv[3])
	}
	return targets
}
func TestCargoPhysicalIdentity(t *testing.T) {
	for _, dependency := range []string{"../core", "../core-alias", "../chain", "../links/core", "../links/deep/../core"} {
		t.Run(dependency, func(t *testing.T) {
			root := tree(t, map[string]string{
				"core/Cargo.toml":  "[package]\nname='core'\nversion='0.1.0'\n",
				"core/src/lib.rs":  "pub fn value() {}",
				"deep/placeholder": "",
				"app/Cargo.toml":   "[package]\nname='app'\nversion='0.1.0'\n[dependencies.core]\npath='" + dependency + "'\n",
			})
			cargoLink(t, root, "core-alias", "core")
			cargoLink(t, root, "chain", "core-alias")
			cargoLink(t, root, "links", ".")
			for _, changed := range [][]string{{"core/src/lib.rs"}, {"core-alias/src/lib.rs"}, {"chain/src/deleted.rs"}, {"core/src/lib.rs", "core-alias/src/lib.rs", "core-alias/Cargo.toml"}} {
				p, err := Build(root, selection(changed...))
				if err != nil || p.Status != "ready" || !reflect.DeepEqual(cargoTargets(p), []string{"./app/Cargo.toml", "./core/Cargo.toml"}) {
					t.Fatalf("%v: %+v %v", changed, p, err)
				}
			}
			alias := filepath.Join(t.TempDir(), "root")
			if err := os.Symlink(root, alias); err != nil {
				t.Fatal(err)
			}
			p, err := Build(alias, selection("core/src/lib.rs"))
			if err != nil || len(p.Commands) != 2 {
				t.Fatalf("root alias: %+v %v", p, err)
			}
		})
	}
}

func TestCargoManifestAliasKeepsDistinctCrateRoots(t *testing.T) {
	// Cargo resolves src/ and local dependencies relative to the crate directory,
	// even when Cargo.toml itself is a symlink. These are two suites, not duplicates.
	root := tree(t, map[string]string{
		"core/Cargo.toml": "[package]\nname='core'\nversion='0.1.0'\n",
		"core/src/lib.rs": "", "copy/src/lib.rs": "",
		"app/Cargo.toml": "[package]\nname='app'\nversion='0.1.0'\n[dependencies.core]\npath='../copy'\n",
	})
	cargoLink(t, root, "copy/Cargo.toml", "../core/Cargo.toml")
	for _, changed := range []string{"copy/src/lib.rs", "copy/Cargo.toml", "core/Cargo.toml"} {
		p, err := Build(root, selection(changed))
		want := []string{"./app/Cargo.toml", "./copy/Cargo.toml"}
		if strings.HasSuffix(changed, "Cargo.toml") {
			want = append(want, "./core/Cargo.toml")
		}
		if err != nil || !reflect.DeepEqual(cargoTargets(p), want) {
			t.Fatalf("%s: %+v %v", changed, p, err)
		}
	}
}

func TestCargoWorkspaceAliasAndCycle(t *testing.T) {
	root := tree(t, map[string]string{
		"Cargo.toml":      "[workspace]\nmembers=['core','app']\n[workspace.dependencies]\ncore={path='alias'}\n",
		"core/Cargo.toml": "[package]\nname='core'\nversion='0.1.0'\n[dev-dependencies]\napp={path='../app'}\n",
		"app/Cargo.toml":  "[package]\nname='app'\nversion='0.1.0'\n[dependencies]\ncore={workspace=true}\n",
	})
	cargoLink(t, root, "alias", "core")
	p, err := Build(root, selection("alias/src/lib.rs"))
	if err != nil || !reflect.DeepEqual(cargoTargets(p), []string{"./app/Cargo.toml", "./core/Cargo.toml"}) {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestCargoUnresolvedPhysicalDependency(t *testing.T) {
	for _, dep := range []string{"../missing", "../missing/../core", "../dangling", "../loop", "../outside", "../../escape", "C:/core", "/absolute", "../CORE", "../target/hidden"} {
		t.Run(dep, func(t *testing.T) {
			root := tree(t, map[string]string{
				"core/Cargo.toml":          "[package]\nname='core'\nversion='0.1.0'\n",
				"app/Cargo.toml":           "[package]\nname='app'\nversion='0.1.0'\n[dependencies.core]\npath='" + dep + "'\n",
				"target/hidden/Cargo.toml": "[package]\nname='hidden'\n",
			})
			cargoLink(t, root, "dangling", "missing")
			cargoLink(t, root, "loop", "loop")
			cargoLink(t, root, "outside", t.TempDir())
			if dep == "../CORE" {
				if _, err := os.Stat(filepath.Join(root, "CORE")); err == nil {
					t.Skip("case-insensitive filesystem resolves this path")
				}
			}
			p, err := Build(root, selection("core/src/lib.rs"))
			if err == nil || p.Status != "incomplete" || p.Coverage != "unknown" || len(p.Diagnostics) == 0 {
				t.Fatalf("%+v %v", p, err)
			}
		})
	}
}

func TestCargoPhysicalParentAndExplicitWorkspace(t *testing.T) {
	root := tree(t, map[string]string{
		"ws/Cargo.toml":       "[workspace]\nmembers=['../app','core']\n[workspace.dependencies]\ncore={path='core-alias'}\n",
		"ws/core/Cargo.toml":  "[package]\nname='core'\nversion='0.1.0'\n",
		"core/Cargo.toml":     "[package]\nname='core'\nversion='0.1.0'\n",
		"ws/deep/placeholder": "",
		"app/Cargo.toml":      "[package]\nname='app'\nversion='0.1.0'\nworkspace='../ws-alias'\n[dependencies]\ncore={workspace=true}\n",
		"other/Cargo.toml":    "[package]\nname='other'\nversion='0.1.0'\n[build-dependencies]\ncore={path='../deep-alias/../core'}\n",
	})
	cargoLink(t, root, "ws-alias", "ws")
	cargoLink(t, root, "ws/core-alias", "core")
	cargoLink(t, root, "deep-alias", "ws/deep")
	for _, tc := range []struct {
		changed string
		want    []string
	}{
		{"ws/core/src/new.rs", []string{"./app/Cargo.toml", "./ws/core/Cargo.toml"}},
		{"core/src/new.rs", []string{"./core/Cargo.toml", "./other/Cargo.toml"}},
	} {
		p, err := Build(root, selection(tc.changed))
		if err != nil || !reflect.DeepEqual(cargoTargets(p), tc.want) {
			t.Fatalf("%s: %+v %v", tc.changed, p, err)
		}
	}
}

func TestCargoRootPackageWorkspaceDependency(t *testing.T) {
	root := tree(t, map[string]string{
		"Cargo.toml":      "[package]\nname='root'\nversion='0.1.0'\n[workspace]\nmembers=['core']\n[workspace.dependencies]\ncore={path='core-alias'}\n[dependencies]\ncore={workspace=true}\n",
		"core/Cargo.toml": "[package]\nname='core'\nversion='0.1.0'\n",
	})
	cargoLink(t, root, "core-alias", "core")
	p, err := Build(root, selection("core/src/lib.rs"))
	if err != nil || !reflect.DeepEqual(cargoTargets(p), []string{"./Cargo.toml", "./core/Cargo.toml"}) {
		t.Fatalf("%+v %v", p, err)
	}
}
