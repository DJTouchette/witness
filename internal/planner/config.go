package planner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Override describes a whole suite. Paths maps source/config changes; Tests
// maps test changes. Exclude only narrows this rule, never erases a changed file.
// Multiple matching suites are a union. Fallback=fail explicitly refuses to
// invent a narrower invocation for a suite that requires manual selection.
type Override struct {
	Name     string   `json:"name"`
	Paths    []string `json:"paths"`
	Tests    []string `json:"tests,omitempty"`
	Exclude  []string `json:"exclude,omitempty"`
	Cwd      string   `json:"cwd"`
	Argv     []string `json:"argv"`
	Fallback string   `json:"fallback,omitempty"` // suite (default), fail
}

func (r *resolver) loadOverrides() error {
	b, err := r.read(".witness.json")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var c struct {
		SchemaVersion int        `json:"schema_version"`
		Suites        []Override `json:"suites"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&c); err != nil {
		return fmt.Errorf(".witness.json: %w", err)
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return errors.New(".witness.json: expected one JSON object")
	}
	if c.SchemaVersion != 1 {
		return fmt.Errorf(".witness.json: schema_version %d unsupported (want 1)", c.SchemaVersion)
	}
	if c.Suites == nil {
		return errors.New(".witness.json: suites array is required")
	}
	names := map[string]bool{}
	for i, o := range c.Suites {
		bad := func(s string) error { return fmt.Errorf(".witness.json suites[%d] %q: %s", i, o.Name, s) }
		if strings.TrimSpace(o.Name) == "" || names[o.Name] {
			return bad("name must be nonempty and unique")
		}
		names[o.Name] = true
		if len(o.Paths)+len(o.Tests) == 0 || len(o.Argv) == 0 || strings.TrimSpace(o.Argv[0]) == "" {
			return bad("requires source paths or tests globs, and nonempty argv")
		}
		if o.Fallback != "" && o.Fallback != "suite" && o.Fallback != "fail" {
			return bad("fallback must be suite or fail")
		}
		for _, arg := range o.Argv {
			if strings.ContainsAny(arg, "\x00\r\n") {
				return bad("argv contains a NUL or newline")
			}
		}
		if !safeRel(o.Cwd) || !r.dirExists(o.Cwd) {
			return bad("cwd must name a directory inside the repository")
		}
		root, _ := filepath.EvalSymlinks(r.root)
		cwd, err := filepath.EvalSymlinks(filepath.Join(r.root, filepath.FromSlash(o.Cwd)))
		if err != nil {
			return bad("cwd cannot be resolved")
		}
		rel, err := filepath.Rel(root, cwd)
		if err != nil || !safeRel(filepath.ToSlash(rel)) {
			return bad("cwd symlink escapes repository")
		}
		patterns := append(append(append([]string{}, o.Paths...), o.Tests...), o.Exclude...)
		for _, pat := range patterns {
			if !safeRel(pat) {
				return bad("invalid repository-relative glob " + pat)
			}
			for _, seg := range strings.Split(pat, "/") {
				if seg == "**" {
					continue
				}
				if _, err := path.Match(seg, ""); err != nil {
					return bad("invalid glob " + pat)
				}
			}
		}
		for _, pat := range append(append([]string{}, o.Paths...), o.Tests...) {
			for _, exclude := range o.Exclude {
				if pat == exclude || exclude == "**" {
					return bad("exclude contradicts mapped paths/tests")
				}
			}
		}
	}
	r.overrides = c.Suites
	return nil
}
func matchesAny(patterns []string, p string) bool {
	for _, pat := range patterns {
		if match(pat, p) {
			return true
		}
	}
	return false
}

func (r *resolver) configured(f string) bool {
	for _, s := range r.overrides {
		if !matchesAny(s.Exclude, f) && (matchesAny(s.Paths, f) || matchesAny(s.Tests, f)) {
			return true
		}
	}
	return false
}
