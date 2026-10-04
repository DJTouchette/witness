package planner

import (
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/djtouchette/witness/internal/repopath"
)

// This is a bounded static superset collector, NOT MSBuild evaluation. Literal
// conditional references are unioned. Unknown property values/imports leave the
// graph incomplete. No SDK, target, task, shell, restore or hook is executed.
type msNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Text     string     `xml:",chardata"`
	Children []msNode   `xml:",any"`
}

func (n msNode) attr(key string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == key {
			return a.Value
		}
	}
	return ""
}

type msScan struct {
	r          *resolver
	project    string
	properties map[string]string
	active     map[string]bool
	count      int
	result     dotProject
	references []msReference
}

type msReference struct {
	node                msNode
	file                string
	conditional, target bool
}

func (s *msScan) issue(f, reason string) { s.result.Issues = append(s.result.Issues, f+": "+reason) }
func (s *msScan) expand(value, f string) (string, bool) {
	// Directory properties are absolute inside the root and use MSBuild's trailing
	// slash for ThisFileDirectory. Other properties are literal, unconditional
	// assignments seen so far; unresolved values are never silently empty.
	props := map[string]string{"msbuildthisfiledirectory": filepath.ToSlash(filepath.Join(s.r.root, filepath.FromSlash(path.Dir(f)))) + "/", "msbuildprojectdirectory": filepath.ToSlash(filepath.Join(s.r.root, filepath.FromSlash(path.Dir(s.project))))}
	for i := 0; i < 32; i++ {
		a := strings.Index(value, "$(")
		if a < 0 {
			return value, !strings.ContainsAny(value, "$@%;")
		}
		end := strings.Index(value[a:], ")")
		if end < 0 {
			return "", false
		}
		b := a + end
		key := strings.ToLower(value[a+2 : b])
		v, ok := props[key]
		if !ok {
			v, ok = s.properties[key]
		}
		if !ok {
			return "", false
		}
		value = value[:a] + v + value[b+1:]
	}
	return "", false
}
func (s *msScan) file(f string, conditional bool) {
	if s.active[f] {
		s.issue(f, "MSBuild import cycle")
		return
	}
	s.count++
	if s.count > 256 || len(s.active) >= 32 {
		s.issue(f, "MSBuild import limit (256 files/32 depth)")
		return
	}
	b, err := s.r.read(f)
	if err != nil {
		s.issue(f, "cannot read MSBuild import/project: "+err.Error())
		return
	}
	var doc msNode
	if err = xml.Unmarshal(b, &doc); err != nil {
		s.issue(f, "invalid XML: "+err.Error())
		return
	}
	s.active[f] = true
	defer delete(s.active, f)
	if sdk := doc.attr("Sdk"); sdk != "" && sdk != "Microsoft.NET.Sdk" && sdk != "Microsoft.NET.Sdk.Web" && sdk != "Microsoft.NET.Sdk.Worker" && sdk != "Microsoft.NET.Sdk.Razor" {
		s.issue(f, "custom SDK requires explicit suite mappings: "+sdk)
	}
	s.nodes(doc.Children, f, conditional, false)
}
func (s *msScan) nodes(nodes []msNode, f string, conditional, inTarget bool) {
	for _, n := range nodes {
		cond := conditional || n.attr("Condition") != "" || n.XMLName.Local == "Choose" || n.XMLName.Local == "When" || n.XMLName.Local == "Otherwise"
		target := inTarget || n.XMLName.Local == "Target"
		switch n.XMLName.Local {
		case "PropertyGroup":
			for _, p := range n.Children {
				key := strings.ToLower(p.XMLName.Local)
				value, ok := s.expand(strings.TrimSpace(p.Text), f)
				if cond || p.attr("Condition") != "" || target || !ok {
					delete(s.properties, key)
				} else {
					s.properties[key] = value
				}
				if key == "istestproject" && (!ok || !strings.EqualFold(value, "false")) {
					s.result.Test = true
				}
				switch key {
				case "importdirectorybuildprops", "importdirectorybuildtargets", "directorybuildpropspath", "directorybuildtargetspath", "custombeforemicrosoftcommontargets", "customaftermicrosoftcommontargets":
					s.issue(f, "custom import property "+p.XMLName.Local+" requires explicit suite mappings")
				}
			}
		case "Import":
			raw := n.attr("Project")
			if raw == "" {
				continue
			} // VB namespace Import item, not an MSBuild import.
			if target {
				s.issue(f, "target-time Import is not statically evaluated")
				continue
			}
			value, ok := s.expand(strings.ReplaceAll(raw, "\\", "/"), f)
			if !ok {
				s.issue(f, "unresolved Import "+raw)
				continue
			}
			pattern, err := s.localPath(path.Dir(f), strings.ReplaceAll(value, "\\", "/"))
			if err != nil || strings.Contains(pattern, "**") {
				s.issue(f, "unsupported/outside Import "+raw)
				continue
			}
			matches := []string{pattern}
			if strings.ContainsAny(pattern, "*?[") {
				matches, err = s.importGlob(pattern)
				if err != nil {
					s.issue(f, "import glob: "+err.Error())
					continue
				}
			}
			for _, p := range matches {
				s.file(p, cond)
			}
		case "ProjectReference":
			s.references = append(s.references, msReference{n, f, cond, target})
		case "Sdk":
			s.issue(f, "explicit SDK imports require suite mappings")
		case "Output":
			if strings.EqualFold(n.attr("ItemName"), "ProjectReference") {
				s.issue(f, "task-generated ProjectReference requires suite mappings")
			}
		case "PackageReference":
			if strings.ContainsAny(n.attr("Include"), "$@%*") {
				s.issue(f, "dynamic PackageReference may define test-project identity")
			}
			switch strings.ToLower(n.attr("Include")) {
			case "microsoft.net.test.sdk", "xunit", "nunit", "mstest.testframework":
				s.result.Test = true
			}
		default:
			s.nodes(n.Children, f, cond, target)
		}
	}
}
func (r *resolver) inspectProject(f string) dotProject {
	s := msScan{r: r, project: f, properties: map[string]string{}, active: map[string]bool{}}
	// SDK defaults import the nearest Directory.Build files. Explicit custom
	// import paths are diagnosed above; conditional contents are overapproximated.
	for _, name := range []string{"Directory.Build.props", "", "Directory.Build.targets"} {
		if name == "" {
			s.file(f, false)
			continue
		}
		if d, ok := r.nearest(f, name); ok {
			s.file(path.Join(d, name), false)
		}
	}
	// MSBuild evaluates properties/imports before items, including items that
	// appear textually before the final property assignment.
	for _, ref := range s.references {
		s.reference(ref)
	}
	return s.result
}

func projectIssues(projects map[string]dotProject) []error {
	var errs []error
	keys := make([]string, 0, len(projects))
	for p := range projects {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		meta := projects[p]
		for _, issue := range meta.Issues {
			errs = append(errs, fmt.Errorf("%s: incomplete static MSBuild graph: %s; add explicit suite mappings", p, issue))
		}
	}
	return errs
}

func (s *msScan) reference(ref msReference) {
	raw := ref.node.attr("Include")
	if raw == "" {
		return
	} // Remove/Update only narrow an already included superset.
	value, ok := s.expand(strings.ReplaceAll(raw, "\\", "/"), ref.file)
	if ref.target || !ok || strings.ContainsAny(value, "*?[") {
		s.issue(ref.file, "unresolved/target-time ProjectReference "+raw)
		return
	}
	valuePath, err := s.localPath(path.Dir(s.project), strings.ReplaceAll(value, "\\", "/"))
	if err != nil {
		s.issue(ref.file, "outside ProjectReference "+raw)
		return
	}
	// Exact spelling matters on case-sensitive hosts. Missing/miscased refs
	// cannot silently disconnect the reverse graph.
	if !s.r.exists(valuePath) {
		s.issue(ref.file, "missing or case-mismatched ProjectReference "+valuePath)
		return
	}
	physical, err := repopath.Resolve(s.r.root, valuePath)
	if err != nil {
		s.issue(ref.file, err.Error())
		return
	}
	physicalRoot, err := repopath.Resolve(s.r.root, ".")
	if err != nil {
		s.issue(ref.file, err.Error())
		return
	}
	canonical, err := filepath.Rel(physicalRoot, physical)
	if err != nil {
		s.issue(ref.file, err.Error())
		return
	}
	s.result.Refs = append(s.result.Refs, filepath.ToSlash(canonical))
	if ref.conditional {
		s.result.Conditional = true
	}

}

func (s *msScan) localPath(base, value string) (string, error) {
	var rel string
	if filepath.IsAbs(value) {
		var err error
		rel, err = filepath.Rel(s.r.root, filepath.FromSlash(value))
		if err != nil {
			return "", err
		}
		rel = filepath.ToSlash(rel)
	} else {
		rel = path.Clean(path.Join(base, value))
	}
	if !safeRel(rel) {
		return "", fmt.Errorf("outside path %s", value)
	}
	// Glob metacharacters need not exist, but their ancestors must stay inside.
	if _, err := repopath.Resolve(s.r.root, rel); err != nil {
		return "", err
	}
	return rel, nil
}

// Only filename globs are supported. Read at most 4097 directory entries and
// refuse broader patterns, rather than silently skipping obj/hidden imports.
func (s *msScan) importGlob(pattern string) ([]string, error) {
	dir := path.Dir(pattern)
	if strings.ContainsAny(dir, "*?[") {
		return nil, fmt.Errorf("wildcard import directories require explicit mapping: %s", pattern)
	}
	full, err := repopath.Directory(s.r.root, dir)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(4097)
	if err != nil && len(entries) == 0 {
		return nil, err
	}
	if len(entries) > 4096 {
		return nil, fmt.Errorf("import glob exceeds 4096 entries")
	}
	var matches []string
	for _, entry := range entries {
		yes, err := path.Match(path.Base(pattern), entry.Name())
		if err != nil {
			return nil, err
		}
		if yes {
			matches = append(matches, path.Join(dir, entry.Name()))
		}
	}
	sort.Strings(matches)
	return matches, nil
}
