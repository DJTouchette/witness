package planner

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/djtouchette/witness/internal/runner"
	"github.com/djtouchette/witness/internal/selector"
)

// WholeRepository accounts for every potentially test-affecting file in the
// bounded discovery tree, including unrecognized languages. Finding one runnable
// manifest never discharges an unaccounted owner. Configured suites are included
// even when their globs currently have no files (e.g. generated tests).
func WholeRepository(root string) (*Plan, error) {
	r := resolver{root: root}
	if err := r.loadOverrides(); err != nil {
		return incompleteRepository(root, err)
	}
	var files []string
	err := r.walkManifests(func(f string) error {
		// Repository administration dotfiles are outside discovery, except explicitly
		// mapped files. Hidden source trees are also outside walkManifests' scope.
		if strings.HasPrefix(path.Base(f), ".") && !r.configured(f) {
			return nil
		}
		if f != ".witness.json" && selector.CanAffectTests(f) {
			files = append(files, f)
		}
		return nil
	})
	if err != nil {
		return incompleteRepository(root, err)
	}
	p, buildErr := Build(root, &selector.SelectResult{ChangedFiles: files})
	var errs []error
	if buildErr != nil {
		errs = append(errs, buildErr)
	}
	for _, o := range r.overrides {
		if o.Fallback == "fail" {
			err := fmt.Errorf("configured suite %q requires manual selection (fallback=fail)", o.Name)
			errs = append(errs, err)
			p.Diagnostics = append(p.Diagnostics, err.Error())
			continue
		}
		p.Commands = append(p.Commands, runner.Command{Cwd: o.Cwd, Argv: o.Argv, Lang: "custom", Reason: "configured whole-repository suite " + o.Name})
	}
	p.Commands = dedup(p.Commands)
	if len(p.Commands) == 0 && len(errs) == 0 {
		err := errors.New("no supported or configured suites discovered; coverage is unknown")
		errs = append(errs, err)
		p.Diagnostics = append(p.Diagnostics, err.Error())
	}
	if len(errs) > 0 {
		p.Status, p.Coverage = "incomplete", "unknown"
	} else {
		p.Status, p.Coverage = "ready", "suites"
	}
	return p, errors.Join(errs...)
}
func incompleteRepository(root string, err error) (*Plan, error) {
	return &Plan{SchemaVersion: 1, Root: root, Status: "incomplete", Coverage: "unknown", Commands: []runner.Command{}, Diagnostics: []string{err.Error()}}, err
}

// legacyManifestLanguage also handles manifest-only repositories. These must be
// recognized before generic JSON/TOML ownership, especially in mixed roots.
func legacyManifestLanguage(f string) string {
	switch path.Base(f) {
	case "mix.exs":
		return "elixir"
	case "pyproject.toml", "pytest.ini", "setup.cfg", "setup.py":
		return "python"
	case "Gemfile":
		return "ruby"
	case "composer.json", "phpunit.xml", "phpunit.xml.dist":
		return "php"
	case "pubspec.yaml":
		return "dart"
	case "Package.swift":
		return "swift"
	case "pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts":
		return "java"
	case "build.sbt":
		return "scala"
	}
	return ""
}
