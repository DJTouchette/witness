// Package planner resolves test suites from repository manifests without
// invoking a runner, package manager, build tool, or project configuration code.
package planner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/djtouchette/witness/internal/runner"
	"github.com/djtouchette/witness/internal/selector"
)

// Plan is the versioned, non-executing contract. Cwd is repository-relative;
// consumers execute argv directly with Dir=Root/Cwd. Display is POSIX-only.
type Plan struct {
	SchemaVersion int                    `json:"schema_version"`
	Root          string                 `json:"root"`
	Status        string                 `json:"status"`   // ready, incomplete, no_changes, no_tests
	Coverage      string                 `json:"coverage"` // selection, suites, unknown
	Selection     *selector.SelectResult `json:"selection"`
	Commands      []runner.Command       `json:"commands"`
	Diagnostics   []string               `json:"diagnostics"`
}

// Build plans both the selected tests and every changed source's owning suite.
// A ranked match is not proof of coverage: co-change is correlation and language
// graphs are partial. Whole owning suites preserve inline tests and new files.
// Unknown ownership is an explicit incomplete plan, never a guessed runner.
func Build(root string, result *selector.SelectResult) (*Plan, error) {
	p := &Plan{SchemaVersion: 1, Root: root, Status: "ready", Coverage: "suites", Selection: result, Commands: []runner.Command{}, Diagnostics: []string{}}
	if result == nil {
		p.Status = "incomplete"
		p.Coverage = "unknown"
		return p, errors.New("nil test selection")
	}
	if len(result.ChangedFiles) == 0 && len(result.Tests) == 0 {
		p.Status = "no_changes"
		return p, nil
	}
	r := resolver{root: root}
	if err := r.loadOverrides(); err != nil {
		p.Status = "incomplete"
		p.Coverage = "unknown"
		p.Diagnostics = append(p.Diagnostics, err.Error())
		return p, err
	}
	if len(r.overrides) == 0 {
		if cmds, ok := r.selected(result); ok {
			p.Commands = cmds
			p.Coverage = "selection"
			return p, nil
		}
	}
	paths := append([]string{}, result.ChangedFiles...)
	for _, t := range result.Tests {
		configured := len(t.ForFiles) > 0
		for _, f := range t.ForFiles {
			if !r.configured(f) {
				configured = false
			}
		}
		if !configured {
			paths = append(paths, t.Path)
		}
	}
	seen := map[string]bool{}
	var errs []error
	for _, f := range paths {
		if seen[f] {
			continue
		}
		seen[f] = true
		if !selector.CanAffectTests(f) {
			continue
		}
		cmds, err := r.resolve(f)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w: %w", f, runner.ErrNoRunner, err))
			continue
		}
		p.Commands = append(p.Commands, cmds...)
	}
	p.Commands = dedup(p.Commands)
	p.Diagnostics = append(p.Diagnostics, selector.CoverageReasons(result)...)
	if result.Summary.Filtered > 0 {
		p.Diagnostics = append(p.Diagnostics, "selection filters do not narrow whole-suite plans; review every command before execution")
	}
	if len(errs) > 0 {
		p.Status = "incomplete"
		p.Coverage = "unknown"
		for _, err := range errs {
			p.Diagnostics = append(p.Diagnostics, err.Error())
		}
	}
	if len(p.Commands) == 0 && len(errs) == 0 {
		p.Status = "no_tests"
	}
	return p, errors.Join(errs...)
}

type resolver struct {
	crates       map[string]cargoDoc
	cargoScanned bool
	cargoErr     error
	root         string
	overrides    []Override
	projects     map[string]dotProject
	projectErr   error
	scanned      bool
}

func (r *resolver) resolve(f string) ([]runner.Command, error) {
	if !safeRel(f) {
		return nil, errors.New("path is outside repository")
	}
	var overrides []runner.Command
	for _, o := range r.overrides {
		for _, pat := range append(append([]string{}, o.Paths...), o.Tests...) {
			if match(pat, f) && !matchesAny(o.Exclude, f) {
				if o.Fallback == "fail" {
					return nil, fmt.Errorf("configured suite %q requires manual selection (fallback=fail)", o.Name)
				}
				overrides = append(overrides, runner.Command{Cwd: o.Cwd, Argv: o.Argv, Lang: "custom", Reason: "configured suite " + o.Name + " (.witness.json; whole suite)"})
				break
			}
		}
	}
	if len(overrides) > 0 {
		return overrides, nil
	}
	switch strings.ToLower(path.Ext(f)) {
	case ".rs":
		return r.rust(f)
	case ".cs", ".csproj", ".fsproj", ".vbproj", ".sln", ".slnx", ".props", ".targets":
		return r.dotnet(f)
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".mts", ".cjs", ".cts":
		return r.node(f)
	case ".go":
		return r.goSuite(f)
	}
	switch path.Base(f) {
	case "Cargo.toml", "Cargo.lock":
		return r.rust(f)
	case "package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml":
		return r.node(f)
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return r.goSuite(f)
	}
	// Common package configuration affects its owning package's suite.
	switch strings.ToLower(path.Ext(f)) {
	case ".json", ".yaml", ".yml", ".toml":
		for d := path.Dir(f); ; d = path.Dir(d) {
			var cmds []runner.Command
			var errs []error
			for _, m := range []string{"Cargo.toml", "package.json", "go.mod"} {
				if !r.exists(path.Join(d, m)) {
					continue
				}
				var c []runner.Command
				var e error
				switch m {
				case "Cargo.toml":
					c, e = r.rust(path.Join(d, m))
				case "package.json":
					c, e = r.node(path.Join(d, m))
				case "go.mod":
					c, e = r.goSuite(path.Join(d, m))
				}
				cmds = append(cmds, c...)
				if e != nil {
					errs = append(errs, e)
				}
			}
			if len(cmds) > 0 || len(errs) > 0 {
				return cmds, errors.Join(errs...)
			}
			if d == "." {
				break
			}
		}
	}
	// Preserve established runners only when a real owning manifest is present.
	var manifest, lang string
	switch strings.ToLower(path.Ext(f)) {
	case ".ex", ".exs":
		manifest, lang = "mix.exs", "elixir"
	case ".py":
		manifest, lang = "pyproject.toml", "python"
	case ".rb":
		manifest, lang = "Gemfile", "ruby"
	case ".php":
		manifest, lang = "composer.json", "php"
	case ".dart":
		manifest, lang = "pubspec.yaml", "dart"
	case ".swift":
		manifest, lang = "Package.swift", "swift"
	case ".java", ".kt", ".scala":
		for _, m := range []string{"pom.xml", "build.gradle", "build.gradle.kts", "build.sbt"} {
			if d, ok := r.nearest(f, m); ok {
				lang = map[string]string{".java": "java", ".kt": "kotlin", ".scala": "scala"}[path.Ext(f)]
				return r.legacySuite(d, lang)
			}
		}
	}
	if manifest != "" {
		if d, ok := r.nearest(f, manifest); ok {
			return r.legacySuite(d, lang)
		}
	}
	return nil, errors.New("no validated test-suite owner; add a .witness.json suite mapping")
}
func (r *resolver) legacySuite(d, lang string) ([]runner.Command, error) {
	cmds, err := runner.FullSuiteCommand(filepath.Join(r.root, filepath.FromSlash(d)), lang)
	for i := range cmds {
		cmds[i].Cwd = d
		cmds[i].Reason = "whole owning manifest suite"
	}
	return cmds, err
}
func (r *resolver) goSuite(f string) ([]runner.Command, error) {
	if path.Base(f) == "go.work" || path.Base(f) == "go.work.sum" {
		return nil, errors.New("Go workspace changes require explicit suite mappings for every member module")
	}
	d, ok := r.nearest(f, "go.mod")
	if !ok {
		return nil, errors.New("no owning go.mod; go.work requires an explicit suite mapping")
	}
	return []runner.Command{{Cwd: d, Lang: "go", Argv: []string{"go", "test", "./..."}, Reason: "whole owning Go module"}}, nil
}
func (r *resolver) read(f string) ([]byte, error) {
	if !safeRel(f) {
		return nil, errors.New("path escapes repository")
	}
	full := filepath.Join(r.root, filepath.FromSlash(f))
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return nil, err
	}
	physicalRoot, err := filepath.EvalSymlinks(r.root)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(physicalRoot, resolved)
	if err != nil || !safeRel(filepath.ToSlash(rel)) {
		return nil, fmt.Errorf("manifest symlink escapes repository: %s", f)
	}
	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > 2<<20 {
		return nil, fmt.Errorf("manifest %s is not a regular file of at most 2 MiB", f)
	}
	return os.ReadFile(full)
}
func (r *resolver) exists(f string) bool {
	st, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(f)))
	return err == nil && st.Mode().IsRegular()
}
func (r *resolver) dirExists(f string) bool {
	st, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(f)))
	return err == nil && st.IsDir()
}
func (r *resolver) nearest(f string, names ...string) (string, bool) {
	if !safeRel(f) {
		return "", false
	}
	for d := path.Dir(f); ; d = path.Dir(d) {
		for _, n := range names {
			if r.exists(path.Join(d, n)) {
				return d, true
			}
		}
		if d == "." {
			break
		}
	}
	return "", false
}
func safeRel(p string) bool {
	p = filepath.ToSlash(p)
	return !strings.ContainsAny(p, "\x00\r\n") && p != "" && !filepath.IsAbs(p) && !strings.Contains(p, "\\") && !strings.Contains(p, ":") && p != ".." && !strings.HasPrefix(path.Clean(p), "../")
}
func dedup(cmds []runner.Command) []runner.Command {
	out := []runner.Command{}
	seen := map[string]bool{}
	for _, c := range cmds {
		if c.Cwd == "" {
			c.Cwd = "."
		}
		b, _ := json.Marshal([]any{c.Cwd, c.Argv})
		if !seen[string(b)] {
			seen[string(b)] = true
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Cwd+"\x00"+strings.Join(out[i].Argv, "\x00") < out[j].Cwd+"\x00"+strings.Join(out[j].Argv, "\x00")
	})
	return out
}
func match(pattern, s string) bool {
	a, b := strings.Split(pattern, "/"), strings.Split(s, "/")
	var walk func([]string, []string) bool
	walk = func(a, b []string) bool {
		if len(a) == 0 {
			return len(b) == 0
		}
		if a[0] == "**" {
			for i := 0; i <= len(b); i++ {
				if walk(a[1:], b[i:]) {
					return true
				}
			}
			return false
		}
		if len(b) == 0 {
			return false
		}
		ok, _ := path.Match(a[0], b[0])
		return ok && walk(a[1:], b[1:])
	}
	return walk(a, b)
}

// walkManifests never visits dependency/build/cache trees or follows symlinked
// directories. The limit makes pathological worktrees fail visibly and bound IO.
func (r *resolver) walkManifests(visit func(string) error) error {
	n := 0
	return filepath.WalkDir(r.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == r.root {
			return nil
		}
		n++
		if n > 200000 {
			return errors.New("manifest scan exceeded 200000 entries")
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "vendor", "target", "bin", "obj", "dist", "build":
				return filepath.SkipDir
			}
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(r.root, p)
		return visit(filepath.ToSlash(rel))
	})
}
