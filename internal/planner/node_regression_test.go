package planner

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManifestEnumeratesIndependentNodeConfigs(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json":              `{"devDependencies":{"vitest":"4","jest":"30"}}`,
		"vitest.config.ts":          `export default {test:{include:['main/*.test.ts']}}`,
		"renderer/vitest.config.ts": `export default {test:{include:['*.test.ts']}}`,
		"legacy/jest.config.js":     `module.exports={testMatch:['**/*.test.js']}`,
		"unrelated/package.json":    `{"devDependencies":{"jest":"30"}}`,
		"unrelated/jest.config.js":  `module.exports={}`,
	})
	for _, f := range []string{"package.json", "package-lock.json"} {
		p, err := Build(root, selection(f))
		if err != nil || len(p.Commands) != 3 {
			t.Fatalf("%s: %+v %v", f, p, err)
		}
		for _, c := range p.Commands {
			if strings.HasPrefix(c.Cwd, "unrelated") {
				t.Fatal("crossed package boundary")
			}
		}
	}
}

func TestNodeScriptsCannotBeSilentlyBypassed(t *testing.T) {
	for _, script := range []string{"node run-tests.js", "npm run custom", "cross-env FOO=bar vitest run", "FOO=bar jest", "vitest --config 'with spaces.ts'", "vitest run src/narrow.test.ts", "vitest run --project=unit"} {
		t.Run(script, func(t *testing.T) {
			root := tree(t, map[string]string{"package.json": `{"devDependencies":{"vitest":"4"},"scripts":{"test":` + quoteJSON(script) + `}}`})
			p, err := Build(root, selection("src/a.ts"))
			if err == nil || p.Status != "incomplete" || len(p.Diagnostics) == 0 {
				t.Fatalf("wrapper silently bypassed: %+v %v", p, err)
			}
		})
	}
}

func quoteJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestNodeLifecycleRequiresMapping(t *testing.T) {
	for _, runner := range []string{"vitest", "jest"} {
		for _, hook := range []string{"pretest", "posttest", runner} {
			root := tree(t, map[string]string{"package.json": `{"devDependencies":{` + quoteJSON(runner) + `:"4"},"scripts":{"test":` + quoteJSON(runner) + `,` + quoteJSON(hook) + `:"node setup.js"}}`})
			p, err := Build(root, selection("a.ts"))
			if err == nil || p.Status != "incomplete" {
				t.Fatalf("%s %s: %+v %v", runner, hook, p, err)
			}
		}
	}
}

func TestDirectNodeScripts(t *testing.T) {
	for _, runner := range []string{"vitest", "jest"} {
		root := tree(t, map[string]string{"package.json": `{"devDependencies":{` + quoteJSON(runner) + `:"4"},"scripts":{"test":` + quoteJSON(runner) + `}}`})
		p, err := Build(root, selection("a.ts"))
		if err != nil || len(p.Commands) != 1 || !strings.Contains(strings.Join(p.Commands[0].Argv, " "), runner) {
			t.Fatalf("%+v %v", p, err)
		}
	}
}
