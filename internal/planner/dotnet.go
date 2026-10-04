package planner

import (
	"encoding/xml"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/djtouchette/witness/internal/runner"
)

type dotProject struct {
	Test    bool
	Refs    []string
	Dynamic bool
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
		b, err := r.read(f)
		if err != nil {
			return err
		}
		var doc struct {
			Properties []struct {
				IsTestProject string `xml:"IsTestProject"`
			} `xml:"PropertyGroup"`
			Groups []struct {
				Packages []struct {
					Include string `xml:"Include,attr"`
				} `xml:"PackageReference"`
				Refs []struct {
					Include string `xml:"Include,attr"`
				} `xml:"ProjectReference"`
			} `xml:"ItemGroup"`
		}
		if err = xml.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		p := dotProject{}
		for _, g := range doc.Properties {
			if strings.EqualFold(strings.TrimSpace(g.IsTestProject), "true") {
				p.Test = true
			}
		}
		for _, g := range doc.Groups {
			for _, pkg := range g.Packages {
				switch strings.ToLower(pkg.Include) {
				case "microsoft.net.test.sdk", "xunit", "nunit", "mstest.testframework":
					p.Test = true
				}
			}
			for _, ref := range g.Refs {
				s := strings.ReplaceAll(ref.Include, "\\", "/")
				if strings.ContainsAny(s, "$*?;") {
					p.Dynamic = true
					continue
				}
				s = path.Clean(path.Join(path.Dir(f), s))
				if !safeRel(s) {
					p.Dynamic = true
					continue
				}
				p.Refs = append(p.Refs, s)
			}
		}
		r.projects[f] = p
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
		// A dynamic reference might refer to this change. Refusing is safer than
		// quietly omitting a test project from a supposedly whole-suite plan.
		if meta.Dynamic {
			return nil, fmt.Errorf("%s has dynamic/outside ProjectReference; add a suite mapping", p)
		}
		if affected[p] && meta.Test {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no test project references %s (including transitively)", owner)
	}
	sort.Strings(targets)
	var cmds []runner.Command
	for _, p := range targets {
		cmds = append(cmds, runner.Command{Cwd: ".", Lang: "dotnet", Argv: []string{"dotnet", "test", "./" + p}, Reason: "whole test project from manifest/ProjectReference closure"})
	}
	return cmds, nil
}
