package planner

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/djtouchette/witness/internal/runner"
)

type nodeManifest struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Scripts         map[string]string `json:"scripts"`
	Workspaces      json.RawMessage   `json:"workspaces"`
	PackageManager  string            `json:"packageManager"`
}

// Manifest and lock changes affect every independent config owned by this
// package. A nested package.json is a boundary, not an implicit workspace.
func (r *resolver) node(f string) ([]runner.Command, error) {
	if nonstandardNodeConfig(f) {
		return nil, errors.New("nonstandard JS config requires an explicit suite mapping with --config")
	}
	switch path.Base(f) {
	case "package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml":
		pkg, ok := r.nearest(f, "package.json")
		if !ok {
			return nil, errors.New("no owning package.json")
		}
		b, err := r.read(path.Join(pkg, "package.json"))
		if err != nil {
			return nil, err
		}
		var manifest nodeManifest
		if err = json.Unmarshal(b, &manifest); err != nil {
			return nil, err
		}
		if len(manifest.Workspaces) > 0 && string(manifest.Workspaces) != "null" {
			return nil, errors.New("workspace manifest/lock changes require explicit mappings for affected workspace packages")
		}
		var configs []string
		err = r.walkManifests(func(p string) error {
			if nodeConfigKind(p) == "" && !nonstandardNodeConfig(p) {
				return nil
			}
			owner, ok := r.nearest(p, "package.json")
			if ok && owner == pkg && nonstandardNodeConfig(p) {
				return fmt.Errorf("%s: nonstandard JS config requires an explicit suite mapping with --config", p)
			}
			if ok && owner == pkg {
				configs = append(configs, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(configs) > 0 {
			all, err := r.nodeWithKind(f, "")
			if err != nil {
				return nil, err
			}
			for _, config := range configs {
				cmds, err := r.nodeWithKind(config, nodeConfigKind(config))
				if err != nil {
					return nil, err
				}
				all = append(all, cmds...)
			}
			return dedup(all), nil
		}
	}
	return r.nodeWithKind(f, "")
}

func nodeConfigKind(f string) string {
	for _, k := range []string{"vitest", "jest", "playwright"} {
		for _, ext := range []string{"ts", "js", "mts", "mjs", "cts", "cjs"} {
			if path.Base(f) == k+".config."+ext {
				return k
			}
		}
	}
	return ""
}

func (r *resolver) nodeWithKind(f, forced string) ([]runner.Command, error) {
	pkg, ok := r.nearest(f, "package.json")
	if !ok {
		return nil, errors.New("no owning package.json")
	}
	b, err := r.read(path.Join(pkg, "package.json"))
	if err != nil {
		return nil, err
	}
	var m nodeManifest
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("package.json: %w", err)
	}
	has := func(name string) bool { return m.Dependencies[name] != "" || m.DevDependencies[name] != "" }
	// A nearer test config is a distinct suite (e.g. desktop/src/renderer).
	dir := pkg
	kind := ""
	for d := path.Dir(f); ; d = path.Dir(d) {
		kinds := []string{}
		for _, k := range []string{"vitest", "jest", "playwright"} {
			for _, ext := range []string{"ts", "js", "mts", "mjs", "cts", "cjs"} {
				if r.exists(path.Join(d, k+".config."+ext)) {
					kinds = append(kinds, k)
					break
				}
			}
		}
		if len(kinds) > 1 {
			if forced != "" {
				dir, kind = d, forced
				break
			}
			// Several declared configs are several suites, not permission to
			// arbitrarily choose one. Resolve each; custom mappings can narrow
			// their source ownership when the repository knows more.
			var all []runner.Command
			for _, k := range kinds {
				cmds, err := r.nodeWithKind(f, k)
				if err != nil {
					return nil, err
				}
				all = append(all, cmds...)
			}
			return all, nil
		}
		if len(kinds) == 1 {
			dir, kind = d, kinds[0]
			break
		}
		if d == pkg || d == "." {
			break
		}
	}
	if kind == "" {
		for _, k := range []string{"vitest", "jest", "@playwright/test"} {
			if has(k) {
				if kind != "" {
					return nil, errors.New("multiple JS runners and no owning config; add a suite mapping")
				}
				kind = k
			}
		}
	}
	if kind == "@playwright/test" {
		kind = "playwright"
	}
	if kind == "" {
		return nil, errors.New("no supported declared JS runner; add a suite mapping")
	}
	dep := kind
	if kind == "playwright" {
		dep = "@playwright/test"
	}
	if !has(dep) {
		return nil, fmt.Errorf("%s config has no corresponding declared dependency in %s/package.json", kind, pkg)
	}
	// Use the package manager without npx's implicit installation. Planning itself
	// runs none of these. A package install is always a separate user operation.
	manager := "npm"
	for d := pkg; ; d = path.Dir(d) {
		if r.exists(path.Join(d, "yarn.lock")) {
			manager = "yarn"
			break
		}
		if r.exists(path.Join(d, "pnpm-lock.yaml")) {
			manager = "pnpm"
			break
		}
		if r.exists(path.Join(d, "package-lock.json")) {
			break
		}
		if d == "." {
			break
		}
	}
	if s, _, ok := strings.Cut(m.PackageManager, "@"); ok {
		switch s {
		case "npm", "yarn", "pnpm":
			manager = s
		default:
			return nil, fmt.Errorf("unsupported package manager %s", s)
		}
	}
	var argv []string
	switch manager {
	case "yarn":
		argv = []string{"yarn", "run", kind}
	case "pnpm":
		argv = []string{"pnpm", "exec", kind}
	default:
		argv = []string{"npm", "exec", "--no", "--", kind}
	}
	switch kind {
	case "vitest":
		argv = append(argv, "run")
	case "playwright":
		argv = append(argv, "test")
	case "jest":
		argv = append(argv, "--watch=false")
	}
	// Only a direct, non-narrowing script can be represented by this inferred
	// whole-runner command. Never bypass wrappers, shell expansion or lifecycle
	// setup. A declarative mapping can preserve them using package-manager argv.
	if m.Scripts["pretest"] != "" || m.Scripts["posttest"] != "" || m.Scripts[kind] != "" {
		return nil, errors.New("test lifecycle/runner script requires an explicit suite mapping to preserve setup and package-manager semantics")
	}
	if strings.ContainsAny(m.Scripts["test"], "\r\n") {
		return nil, errors.New("multiline test script requires an explicit suite mapping")
	}
	script := strings.Fields(m.Scripts["test"])
	if len(script) > 0 {
		if script[0] != kind {
			return nil, errors.New("non-direct test script requires an explicit suite mapping; inferred runner would bypass wrapper/setup")
		}
		for _, a := range script[1:] {
			if a == "run" && kind == "vitest" || a == "test" && kind == "playwright" || a == "--watch=false" || a == "--watch" || a == "--watchAll" {
				continue
			}
			// This intentionally tiny allowlist cannot narrow test ownership or point
			// at another config/workspace. Quoted values need a mapping, not shell parsing.
			allowed := a == "--globals" || strings.HasPrefix(a, "--environment=") || strings.HasPrefix(a, "--reporter=")
			if !allowed || strings.ContainsAny(a, "; &|$`\"'\\<>*?[]{}()~\n\r\t") {
				return nil, fmt.Errorf("test script option %q requires an explicit suite mapping (cannot prove whole-suite semantics)", a)
			}
			argv = append(argv, a)
		}
	}
	return []runner.Command{{Cwd: dir, Lang: "node", Argv: argv, Reason: "whole declared JS runner suite at nearest test config/package"}}, nil
}

func nonstandardNodeConfig(f string) bool {
	base := path.Base(f)
	for _, k := range []string{"vitest", "jest", "playwright"} {
		if strings.HasPrefix(base, k+".") && strings.Contains(base, "config.") && nodeConfigKind(f) == "" {
			return true
		}
	}
	return false
}
