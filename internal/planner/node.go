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
	PackageManager  string            `json:"packageManager"`
}

func (r *resolver) node(f string) ([]runner.Command, error) {
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
			return nil, fmt.Errorf("ambiguous test configs in %s; add a suite mapping", d)
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
	// Preserve simple test-script options (notably Vitest's jsdom environment).
	// Complex script wrappers need an explicit mapping, never shell evaluation.
	script := strings.Fields(m.Scripts["test"])
	if dir == pkg && len(script) > 0 && script[0] == kind {
		for _, a := range script[1:] {
			if strings.ContainsAny(a, ";&|$`\"'\\") {
				return nil, errors.New("complex test script requires an explicit suite mapping")
			}
			if a == "run" || a == "--watch" || a == "--watchAll" || a == "--watch=false" {
				continue
			}
			argv = append(argv, a)
		}
	}
	return []runner.Command{{Cwd: dir, Lang: "node", Argv: argv, Reason: "whole declared JS runner suite at nearest test config/package"}}, nil
}
