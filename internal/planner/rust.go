package planner

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/djtouchette/witness/internal/repopath"
	"github.com/djtouchette/witness/internal/runner"
	"github.com/pelletier/go-toml/v2"
)

type cargoDoc struct {
	Package struct {
		Name      string `toml:"name"`
		Workspace string `toml:"workspace"`
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

// cargoPath resolves one component at a time, before joining the next one.
// Cleaning alias/.. lexically first can name a different directory. Resolve
// enforces the shared physical boundary at each step, including missing leaves.
func (r *resolver) cargoPath(base, rel string) (string, error) {
	return r.cargoResolve(base, rel, false)
}

func (r *resolver) cargoResolve(base, rel string, directory bool) (string, error) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	if rel == "" || strings.HasPrefix(rel, "/") || strings.ContainsAny(rel, ":\x00\r\n") {
		return "", fmt.Errorf("unsupported Cargo path %q", rel)
	}
	root, err := repopath.Directory(r.root, ".")
	if err != nil {
		return "", err
	}
	current := base
	for _, part := range strings.Split(rel, "/") {
		full, err := repopath.Resolve(root, path.Join(current, part))
		if err != nil {
			return "", err
		}
		if directory {
			st, err := os.Stat(full)
			if err != nil {
				return "", err
			}
			if !st.IsDir() {
				return "", fmt.Errorf("Cargo path component is not a directory: %s", full)
			}
		}
		within, err := filepath.Rel(root, full)
		if err != nil {
			return "", err
		}
		current = filepath.ToSlash(within)
	}
	return current, nil
}

func (r *resolver) cargoDir(dir string) (string, error) {
	id, err := r.cargoResolve(".", dir, true)
	if err != nil {
		return "", err
	}
	if _, err := repopath.Directory(r.root, id); err != nil {
		return "", err
	}
	// SameFile also reconciles native case-insensitive filesystem identities.
	// Do not lowercase paths: that merges distinct crates on Linux.
	if _, ok := r.crates[id]; !ok {
		st, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(id)))
		if err != nil {
			return "", err
		}
		for known := range r.crates {
			other, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(known)))
			if err == nil && os.SameFile(st, other) {
				return known, nil
			}
		}
	}
	return id, nil
}

func (r *resolver) rust(f string) ([]runner.Command, error) {
	if !r.cargoScanned {
		r.cargoScanned = true
		r.crates = map[string]cargoDoc{}
		// WalkDir does not follow a symlink root. Walk the validated physical root
		// while keeping all emitted paths relative to the caller's repository.
		physicalRoot, err := repopath.Directory(r.root, ".")
		if err != nil {
			return nil, err
		}
		scan := resolver{root: physicalRoot}
		r.cargoErr = scan.walkManifests(func(f string) error {
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
			dir, err := r.cargoDir(path.Dir(f))
			if err != nil {
				return err
			}
			r.crates[dir] = doc
			return nil
		})
	}
	if r.cargoErr != nil {
		return nil, r.cargoErr
	}
	affected := map[string]bool{}
	physical, err := r.cargoPath(".", f)
	if err != nil {
		return nil, err
	}
	// A source-file alias can belong to both the logical and physical crate.
	// Directory aliases collapse to one identity; missing changed leaves still
	// resolve through their existing ancestors.
	for _, changed := range []string{f, physical} {
		d, ok := r.nearest(changed, "Cargo.toml")
		if !ok {
			continue
		}
		id, err := r.cargoDir(d)
		if err != nil {
			return nil, err
		}
		if _, ok := r.crates[id]; !ok {
			return nil, errors.New("Cargo owner is outside manifest scan scope")
		}
		affected[id] = true
	}
	// Symlinked manifest files share changes, but not crate roots: Cargo resolves
	// src/ and dependency paths relative to each containing directory.
	for dir := range r.crates {
		manifest, err := r.cargoPath(dir, "Cargo.toml")
		if err != nil {
			return nil, err
		}
		if manifest == physical {
			affected[dir] = true
		}
	}
	if len(affected) == 0 {
		return nil, errors.New("no owning Cargo.toml")
	}
	var issues []error
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
					anc := dir
					if doc.Package.Workspace != "" {
						var err error
						anc, err = r.cargoResolve(dir, doc.Package.Workspace, true)
						if err != nil {
							issues = append(issues, err)
							continue
						}
						anc, err = r.cargoDir(anc)
						if err != nil {
							issues = append(issues, err)
							continue
						}
					}
					for ; ; anc = path.Dir(anc) {
						if ws, ok := r.crates[anc]; ok && ws.Workspace != nil {
							raw, exists := ws.Workspace.Dependencies[name]
							dep, _ = raw.(map[string]any)
							base = anc
							found = exists
							break
						}
						if anc == "." || doc.Package.Workspace != "" {
							break
						}
					}
					if !found {
						issues = append(issues, fmt.Errorf("cannot resolve workspace dependency %s in %s", name, dir))
						continue
					}
				}
				if local, ok := dep["path"].(string); ok {
					target, err := r.cargoResolve(base, local, true)
					if err == nil {
						target, err = r.cargoDir(target)
					}
					if err == nil {
						if _, ok := r.crates[target]; !ok {
							err = errors.New("dependency is outside manifest scan scope or has no Cargo.toml")
						}
					}
					if err != nil {
						issues = append(issues, fmt.Errorf("Cargo dependency %s in %s (%q): %w", name, dir, local, err))
						continue
					}
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
	return cmds, errors.Join(issues...)
}
