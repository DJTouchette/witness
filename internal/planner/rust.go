package planner

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/djtouchette/witness/internal/runner"
	"github.com/pelletier/go-toml/v2"
)

type cargoDoc struct {
	Package struct {
		Name string `toml:"name"`
	} `toml:"package"`
	Workspace *struct {
		Members      []string       `toml:"members"`
		Dependencies map[string]any `toml:"dependencies"`
	} `toml:"workspace"`
	Dependencies      map[string]any `toml:"dependencies"`
	DevDependencies   map[string]any `toml:"dev-dependencies"`
	BuildDependencies map[string]any `toml:"build-dependencies"`
	Target            map[string]struct {
		Dependencies      map[string]any `toml:"dependencies"`
		DevDependencies   map[string]any `toml:"dev-dependencies"`
		BuildDependencies map[string]any `toml:"build-dependencies"`
	} `toml:"target"`
}

func (r *resolver) rust(f string) ([]runner.Command, error) {
	d, ok := r.nearest(f, "Cargo.toml")
	if !ok {
		return nil, errors.New("no owning Cargo.toml")
	}
	if !r.cargoScanned {
		r.cargoScanned = true
		r.crates = map[string]cargoDoc{}
		r.cargoErr = r.walkManifests(func(f string) error {
			if path.Base(f) != "Cargo.toml" {
				return nil
			}
			b, err := r.read(f)
			if err != nil {
				return err
			}
			var doc cargoDoc
			if err = toml.Unmarshal(b, &doc); err != nil {
				return fmt.Errorf("%s: %w", f, err)
			}
			r.crates[path.Dir(f)] = doc
			return nil
		})
	}
	if r.cargoErr != nil {
		return nil, r.cargoErr
	}
	if _, ok := r.crates[d]; !ok {
		return nil, errors.New("Cargo owner is outside manifest scan scope")
	}
	affected := map[string]bool{d: true}
	// Include reverse path dependencies, not just the crate containing the file.
	// Workspace inheritance resolves against the ancestor workspace manifest.
	refs := map[string][]string{}
	for dir, doc := range r.crates {
		groups := []map[string]any{doc.Dependencies, doc.DevDependencies, doc.BuildDependencies}
		for _, t := range doc.Target {
			groups = append(groups, t.Dependencies, t.DevDependencies, t.BuildDependencies)
		}
		for _, g := range groups {
			for name, raw := range g {
				dep, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				base := dir
				if inherited, _ := dep["workspace"].(bool); inherited {
					found := false
					for anc := path.Dir(dir); ; anc = path.Dir(anc) {
						if ws, ok := r.crates[anc]; ok && ws.Workspace != nil {
							dep, _ = ws.Workspace.Dependencies[name].(map[string]any)
							base = anc
							found = true
							break
						}
						if anc == "." {
							break
						}
					}
					if !found {
						return nil, fmt.Errorf("cannot resolve workspace dependency %s in %s", name, dir)
					}
				}
				if local, ok := dep["path"].(string); ok {
					target := path.Clean(path.Join(base, strings.ReplaceAll(local, "\\", "/")))
					if !safeRel(target) {
						continue
					} // dependency outside this repository cannot be a changed path here
					refs[dir] = append(refs[dir], target)
				}
			}
		}
	}
	for progress := true; progress; {
		progress = false
		for dir, deps := range refs {
			if affected[dir] {
				continue
			}
			for _, dep := range deps {
				if affected[dep] {
					affected[dir] = true
					progress = true
					break
				}
			}
		}
	}
	dirs := []string{}
	for dir := range affected {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	cmds := []runner.Command{}
	for _, dir := range dirs {
		doc := r.crates[dir]
		if doc.Package.Name == "" && doc.Workspace == nil {
			return nil, fmt.Errorf("%s/Cargo.toml has no package or workspace", dir)
		}
		argv := []string{"cargo", "test", "--manifest-path", "./" + path.Join(dir, "Cargo.toml")}
		if doc.Workspace != nil {
			argv = append(argv, "--workspace")
		}
		cmds = append(cmds, runner.Command{Cwd: ".", Lang: "rust", Argv: argv, Reason: "whole Cargo manifest and reverse path dependencies (includes inline tests)"})
	}
	return cmds, nil
}
