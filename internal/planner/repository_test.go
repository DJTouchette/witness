package planner

import "testing"

func TestWholeRepositoryMixedAndUnknown(t *testing.T) {
	for _, extra := range []string{"python", "unsupported", "config-only"} {
		files := map[string]string{"go.mod": "module mixed\n", "a.go": "package mixed"}
		switch extra {
		case "python":
			files["python/pyproject.toml"] = "[project]\nname='py'\n"
			files["python/tests/test_a.py"] = "def test_a(): pass"
		case "unsupported":
			files["native/a.zig"] = "test {}"
		case "config-only":
			files["jest.config.js"] = "module.exports={}"
		}
		p, err := WholeRepository(tree(t, files))
		if extra == "python" {
			if err != nil || len(p.Commands) != 2 || p.Status != "ready" {
				t.Errorf("%s: %+v %v", extra, p, err)
			}
		} else if err == nil || p.Status != "incomplete" || len(p.Diagnostics) == 0 {
			t.Errorf("%s falsely complete: %+v %v", extra, p, err)
		}
	}
}
func TestWholeRepositoryConfiguredSuitesWithoutFiles(t *testing.T) {
	p, err := WholeRepository(tree(t, map[string]string{".witness.json": `{"schema_version":1,"suites":[{"name":"custom","paths":["future/**"],"cwd":".","argv":["custom-test"]}]}`}))
	if err != nil || p.Status != "ready" || len(p.Commands) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestWholeRepositoryLegacyManifestOnly(t *testing.T) {
	for _, manifest := range []string{"mix.exs", "pyproject.toml", "pytest.ini", "setup.cfg", "Gemfile", "composer.json", "pubspec.yaml", "Package.swift", "pom.xml", "build.gradle", "build.gradle.kts", "build.sbt"} {
		t.Run(manifest, func(t *testing.T) {
			files := map[string]string{"nested/" + manifest: ""}
			if manifest == "composer.json" {
				files["nested/composer.json"] = `{"require-dev":{"phpunit/phpunit":"11"}}`
			}
			p, err := WholeRepository(tree(t, files))
			if err != nil || p.Status != "ready" || len(p.Commands) != 1 || p.Commands[0].Cwd != "nested" {
				t.Fatalf("%+v %v", p, err)
			}
		})
	}
}
