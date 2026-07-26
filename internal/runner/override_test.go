package runner

import (
	"strings"
	"testing"
)

// OverrideCommand had no direct test: it was only reached through the CLI, which
// is how it kept appending raw paths to runners that cannot take them.
func TestOverrideCommandTranslatesOrRefuses(t *testing.T) {
	argv := []string{"mvn", "test"}

	tests := []struct {
		name      string
		argv      []string
		framework string
		paths     []string
		wantArgv  []string
		wantErr   string
	}{
		{
			name:      "an empty selection runs the command as given",
			argv:      []string{"go", "test", "./..."},
			framework: "go",
			paths:     nil,
			wantArgv:  []string{"go", "test", "./..."},
		},
		{
			name:      "go gets package globs, not file paths",
			argv:      []string{"gotestsum", "--"},
			framework: "go",
			paths:     []string{"calc/calc_test.go"},
			wantArgv:  []string{"gotestsum", "--", "./calc/..."},
		},
		{
			name:      "python takes paths directly",
			argv:      []string{"pytest", "-x"},
			framework: "python",
			paths:     []string{"tests/test_api.py"},
			wantArgv:  []string{"pytest", "-x", "tests/test_api.py"},
		},
		{
			name:      "rust is refused: positionals are name filters",
			argv:      []string{"cargo", "test"},
			framework: "rust",
			paths:     []string{"tests/orders.rs"},
			wantErr:   "cannot name rust tests",
		},
		{
			name:      "java is refused: mvn selects by class",
			argv:      argv,
			framework: "java",
			paths:     []string{"src/test/java/com/example/CalculatorTest.java"},
			wantErr:   "cannot name java tests by path",
		},
		{
			name:      "kotlin is refused",
			argv:      []string{"gradle", "test"},
			framework: "kotlin",
			paths:     []string{"src/test/kotlin/com/example/CartTest.kt"},
			wantErr:   "cannot name kotlin tests by path",
		},
		{
			name:      "scala is refused: sbt keys are not paths",
			argv:      []string{"sbt", "test"},
			framework: "scala",
			paths:     []string{"src/test/scala/com/example/CoreSpec.scala"},
			wantErr:   "cannot name scala tests by path",
		},
		{
			name:      "swift is refused: --filter takes a type name",
			argv:      []string{"swift", "test"},
			framework: "swift",
			paths:     []string{"Tests/AppTests/CartTests.swift"},
			wantErr:   "cannot name swift tests by path",
		},
		{
			name:      "a polyglot selection is refused",
			argv:      []string{"make", "test"},
			framework: "elixir",
			paths:     []string{"test/orders_test.exs", "assets/cart.test.js"},
			wantErr:   "spanning 2 languages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmds, err := OverrideCommand(tt.argv, tt.framework, tt.paths)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want an error containing %q, got argv %v", tt.wantErr, cmds)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				if cmds != nil {
					t.Errorf("a refusal must return no commands, got %v", cmds)
				}
				return
			}

			if err != nil {
				t.Fatalf("OverrideCommand: %v", err)
			}
			if len(cmds) != 1 {
				t.Fatalf("want exactly 1 command, got %d: %v", len(cmds), cmds)
			}
			if got := strings.Join(cmds[0].Argv, " "); got != strings.Join(tt.wantArgv, " ") {
				t.Errorf("argv = %q, want %q", got, strings.Join(tt.wantArgv, " "))
			}
		})
	}
}

// The refusals exist because these runners exit non-zero on a path (loud but
// wrong) or, worse, exit 0 having run nothing. Either way witness must not be
// the thing that constructs the invocation.
func TestOverrideCommandNeverAppendsAPathForClassSelectingRunners(t *testing.T) {
	for _, lang := range []string{"java", "kotlin", "scala", "swift", "rust"} {
		path := "src/test/Example." + lang
		cmds, err := OverrideCommand([]string{"someRunner"}, lang, []string{path})
		if err == nil {
			t.Errorf("%s: want a refusal, got %v", lang, cmds)
			continue
		}
		for _, c := range cmds {
			for _, a := range c.Argv {
				if a == path {
					t.Errorf("%s: the path leaked into argv despite the error: %v", lang, c.Argv)
				}
			}
		}
	}
}
