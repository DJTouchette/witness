package planner

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/djtouchette/witness/internal/runner"
	"github.com/djtouchette/witness/internal/selector"
)

// Keep existing precise selection for established path/class runners when no
// coverage gap is known. Monorepo Rust/.NET/JS use manifest suites below: their
// language graphs alone cannot establish runner configuration or inline tests.
func (r *resolver) selected(result *selector.SelectResult) ([]runner.Command, bool) {
	if len(result.Tests) == 0 || len(selector.CoverageReasons(result)) > 0 {
		return nil, false
	}
	for _, f := range result.ChangedFiles {
		switch path.Ext(f) {
		case ".go", ".py", ".rb", ".ex", ".exs", ".java", ".kt", ".scala", ".swift", ".php", ".dart":
		default:
			if selector.CanAffectTests(f) {
				return nil, false
			}
		}
	}
	var out []runner.Command
	for _, t := range result.Tests {
		var names []string
		var lang string
		switch path.Ext(t.Path) {
		case ".go":
			names = []string{"go.mod"}
			lang = "go"
		case ".py":
			names = []string{"pyproject.toml", "pytest.ini", "setup.cfg"}
			lang = "python"
		case ".rb":
			names = []string{"Gemfile"}
			lang = "ruby"
		case ".exs":
			names = []string{"mix.exs"}
			lang = "elixir"
		case ".java":
			names = []string{"pom.xml", "build.gradle", "build.gradle.kts"}
			lang = "java"
		case ".kt":
			names = []string{"pom.xml", "build.gradle", "build.gradle.kts"}
			lang = "kotlin"
		case ".scala":
			names = []string{"build.sbt", "pom.xml", "build.gradle"}
			lang = "scala"
		default:
			return nil, false
		}
		d, ok := r.nearest(t.Path, names...)
		if !ok {
			return nil, false
		}
		rel := strings.TrimPrefix(t.Path, d+"/")
		if d == "." {
			rel = t.Path
		}
		cmds, err := runner.FormatCommand(filepath.Join(r.root, filepath.FromSlash(d)), lang, []string{rel})
		if err != nil {
			return nil, false
		}
		for i := range cmds {
			cmds[i].Cwd = d
			cmds[i].Reason = "selected tests under validated owning manifest"
		}
		out = append(out, cmds...)
	}
	return dedup(out), true
}
