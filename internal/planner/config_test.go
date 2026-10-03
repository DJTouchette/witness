package planner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigurationValidation(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"schema_version":2,"suites":[]}`, `{"schema_version":1,"typo":true}`,
		`{"schema_version":1,"suites":[{"name":"bad","paths":["["],"cwd":".","argv":["custom"]}]}`,
		`{"schema_version":1,"suites":[{"name":"bad","paths":["**"],"cwd":"../outside","argv":["custom"]}]}`,
		`{"schema_version":1,"suites":[{"name":"bad","paths":["**"],"cwd":".","argv":[]}]}`,
		`{"schema_version":1,"suites":[{"name":"bad","paths":["**"],"exclude":["**"],"cwd":".","argv":["custom"]}]}`,
		`{"schema_version":1,"suites":[{"name":"bad","paths":["**"],"cwd":".","argv":["custom"],"fallback":"skip"}]}`,
	} {
		p, err := Build(tree(t, map[string]string{".witness.json": body}), selection("a.rs"))
		if err == nil || p.Status != "incomplete" || len(p.Commands) > 0 || !strings.Contains(err.Error(), ".witness.json") {
			t.Fatalf("config %s: %+v %v", body, p, err)
		}
	}
}
func TestConfiguredUnionExclusionsAndFailure(t *testing.T) {
	config := `{"schema_version":1,"suites":[
 {"name":"native","paths":["src/**"],"tests":["checks/**"],"exclude":["src/generated/**"],"cwd":".","argv":["cargo","test"]},
 {"name":"web","paths":["web/**"],"cwd":".","argv":["custom-test-wrapper","--offline"]},
 {"name":"manual","paths":["manual/**"],"cwd":".","argv":["custom"],"fallback":"fail"}
 ]}`
	root := tree(t, map[string]string{".witness.json": config})
	p, err := Build(root, selection("checks/foo.rs", "web/component.ts"))
	if err != nil || len(p.Commands) != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	for _, c := range p.Commands {
		if c.Lang != "custom" || !strings.Contains(c.Reason, "configured suite") {
			t.Fatal(c)
		}
	}
	for _, file := range []string{"src/generated/a.rs", "manual/foo.rs"} {
		p, err = Build(root, selection(file))
		if err == nil || p.Status != "incomplete" {
			t.Fatalf("excluded/manual change silently dropped: %+v %v", p, err)
		}
	}
}
func TestConfigCwdSymlinkCannotEscape(t *testing.T) {
	root := tree(t, map[string]string{".witness.json": `{"schema_version":1,"suites":[{"name":"outside","paths":["**"],"cwd":"link","argv":["custom"]}]}`})
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	if _, err := Build(root, selection("source.rs")); err == nil {
		t.Fatal("outside cwd accepted")
	}
}
