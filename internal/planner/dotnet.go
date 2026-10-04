package planner

import (
	"errors"
	"fmt"
	"path"
	"sort"

	"github.com/djtouchette/witness/internal/runner"
)

type dotProject struct {
	Test        bool
	Refs        []string
	Issues      []string
	Conditional bool
}

func (r *resolver) scanProjects() error {
	if r.scanned {
		return r.projectErr
	}
	r.scanned = true
	r.projects = map[string]dotProject{}
	r.projectErr = r.walkManifests(func(f string) error {
		switch path.Ext(f) {
		case ".csproj", ".fsproj", ".vbproj":
		default:
			return nil
		}
		r.projects[f] = r.inspectProject(f)
		return nil
	})
	return r.projectErr
}
func (r *resolver) projectOwner(f string) (string, error) {
	if _, ok := r.projects[f]; ok {
		return f, nil
	}
	for d := path.Dir(f); ; d = path.Dir(d) {
		var found []string
		for p := range r.projects {
			if path.Dir(p) == d {
				found = append(found, p)
			}
		}
		if len(found) == 1 {
			return found[0], nil
		}
		if len(found) > 1 {
			return "", fmt.Errorf("multiple project manifests in %s; use an explicit suite mapping", d)
		}
		if d == "." {
			break
		}
	}
	return "", errors.New("no owning .NET project manifest")
}
func (r *resolver) dotnet(f string) ([]runner.Command, error) {
	switch path.Ext(f) {
	case ".sln", ".slnx", ".props", ".targets":
		return nil, errors.New("solution/imported MSBuild configuration requires explicit test-suite mappings")
	}
	if err := r.scanProjects(); err != nil {
		return nil, err
	}
	owner, err := r.projectOwner(f)
	if err != nil {
		return nil, err
	} // solution/props membership needs an explicit mapping
	affected := map[string]bool{owner: true}
	for progress := true; progress; {
		progress = false
		for p, meta := range r.projects {
			if affected[p] {
				continue
			}
			for _, ref := range meta.Refs {
				if affected[ref] {
					affected[p] = true
					progress = true
					break
				}
			}
		}
	}
	var targets []string
	for p, meta := range r.projects {
		if affected[p] && meta.Test {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		return nil, errors.Join(append(r.projectGraphIssues(), fmt.Errorf("no test project references %s (including transitively)", owner))...)
	}
	sort.Strings(targets)
	var cmds []runner.Command
	for _, p := range targets {
		cmds = append(cmds, runner.Command{Cwd: ".", Lang: "dotnet", Argv: []string{"dotnet", "test", "./" + p}, Reason: "whole test project from static ProjectReference superset (conditional literal edges included)"})
	}
	return cmds, errors.Join(r.projectGraphIssues()...)
}

// Report repository-wide uncertainty once per Build; every known target is
// still retained. This avoids repeating the same import graph for each ranked
// test and turning a small plan into megabytes of duplicate diagnostics.
func (r *resolver) projectGraphIssues() []error {
	if r.projectIssuesReported {
		return nil
	}
	r.projectIssuesReported = true
	return projectIssues(r.projects)
}
