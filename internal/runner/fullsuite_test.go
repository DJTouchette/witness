package runner

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestFullSuiteCommand(t *testing.T) {
	tests := []struct {
		framework string
		want      []string
	}{
		{"go", []string{"go", "test", "./..."}},
		{"gin", []string{"go", "test", "./..."}},
		{"elixir", []string{"mix", "test"}},
		{"phoenix", []string{"mix", "test"}},
		{"python", []string{"pytest"}},
		{"ruby", []string{"bundle", "exec", "rspec"}},
		{"node", []string{"npx", "jest"}},
		{"rust", []string{"cargo", "test"}},
		{"csharp", []string{"dotnet", "test"}},
	}
	for _, tc := range tests {
		t.Run(tc.framework, func(t *testing.T) {
			cmds, err := FullSuiteCommand("", tc.framework)
			if err != nil {
				t.Fatalf("FullSuiteCommand(%q): %v", tc.framework, err)
			}
			if len(cmds) != 1 {
				t.Fatalf("got %d commands, want 1: %v", len(cmds), cmds)
			}
			if !reflect.DeepEqual(cmds[0].Argv, tc.want) {
				t.Errorf("argv = %q, want %q", cmds[0].Argv, tc.want)
			}
		})
	}
}

// TestFullSuiteCommand_BuildFileLanguages covers the six ecosystems whose
// whole-suite command is not a constant.
//
// --fallback=full is the documented default and the README calls it the safe
// one, so a Java, Kotlin, Scala, Swift, PHP or Dart repository must get an
// actual whole-suite command out of it. Before these arms existed the default
// fallback was silently identical to --fallback=fail on every one of them: an
// ordinary uncovered change exited 1 with "witness cannot run tests for java".
func TestFullSuiteCommand_BuildFileLanguages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the wrapper scripts asserted here are the POSIX mvnw/gradlew")
	}
	tests := []struct {
		name      string
		framework string
		files     map[string]string
		want      []string
	}{
		{
			name:      "java under maven",
			framework: "java",
			files:     map[string]string{"pom.xml": "<project/>"},
			want:      []string{"mvn", "test"},
		},
		{
			name:      "maven wrapper is preferred",
			framework: "java",
			files:     map[string]string{"pom.xml": "<project/>", "mvnw": "#!/bin/sh\n"},
			want:      []string{"./mvnw", "test"},
		},
		{
			name:      "kotlin under gradle",
			framework: "kotlin",
			files:     map[string]string{"build.gradle.kts": ""},
			want:      []string{"gradle", "test"},
		},
		{
			name:      "gradle wrapper is preferred",
			framework: "kotlin",
			files:     map[string]string{"settings.gradle": "", "gradlew": "#!/bin/sh\n"},
			want:      []string{"./gradlew", "test"},
		},
		{
			name:      "scala under a single-project sbt build",
			framework: "scala",
			files:     map[string]string{"build.sbt": `name := "app"`},
			want:      []string{"sbt", "test"},
		},
		{
			name:      "scala under maven",
			framework: "scala",
			files:     map[string]string{"pom.xml": "<project/>"},
			want:      []string{"mvn", "test"},
		},
		{
			name:      "swift package",
			framework: "swift",
			files:     map[string]string{"Package.swift": "// swift-tools-version:5.9\n"},
			want:      []string{"swift", "test"},
		},
		{
			name:      "php with phpunit",
			framework: "php",
			files:     map[string]string{"composer.json": `{"require-dev":{"phpunit/phpunit":"^11.0"}}`},
			want:      []string{"vendor/bin/phpunit"},
		},
		{
			// A Pest project vendors phpunit too, and `vendor/bin/phpunit`
			// silently runs none of its Pest tests — the whole suite has the
			// same trap as the selected one.
			name:      "php with pest",
			framework: "php",
			files:     map[string]string{"composer.json": `{"require-dev":{"pestphp/pest":"^3.0","phpunit/phpunit":"^11.0"}}`},
			want:      []string{"vendor/bin/pest"},
		},
		{
			name:      "plain dart package",
			framework: "dart",
			files:     map[string]string{"pubspec.yaml": "name: shop\ndev_dependencies:\n  test: ^1.25.0\n"},
			want:      []string{"dart", "test"},
		},
		{
			name:      "flutter package",
			framework: "dart",
			files:     map[string]string{"pubspec.yaml": "name: shop\nflutter:\n  uses-material-design: true\n"},
			want:      []string{"flutter", "test"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeTree(t, tc.files)
			cmds, err := FullSuiteCommand(root, tc.framework)
			if err != nil {
				t.Fatalf("FullSuiteCommand(%q): %v", tc.framework, err)
			}
			if len(cmds) != 1 || !reflect.DeepEqual(cmds[0].Argv, tc.want) {
				t.Errorf("argv = %v, want [%q]", argvsOf(cmds), tc.want)
			}
		})
	}
}

// TestFullSuiteCommand_Refusals pins the cases where the root does not say what
// the whole suite is. Every one of them has to be an error: the fallback is the
// path that exists to run everything, and an empty answer there is the false
// green the whole tool guards against.
func TestFullSuiteCommand_Refusals(t *testing.T) {
	tests := []struct {
		name       string
		framework  string
		files      map[string]string
		wantReason string
	}{
		{
			name:       "jvm tree with no build file",
			framework:  "java",
			files:      map[string]string{"src/main/java/com/example/App.java": "class App {}\n"},
			wantReason: "pom.xml or build.gradle",
		},
		{
			// `sbt test` has exactly the hole `sbt testOnly` has: a root that
			// aggregates nothing runs nothing and calls it success. Widening is
			// not an escape, so the fallback refuses too.
			name:      "multi-project sbt build",
			framework: "scala",
			files: map[string]string{
				"build.sbt": "lazy val root = (project in file(\".\")).settings(name := \"demo\")\n" +
					"lazy val core = (project in file(\"core\")).settings(name := \"core\")\n",
			},
			wantReason: "more than one sbt project",
		},
		{
			name:       "swift without a package manifest",
			framework:  "swift",
			files:      map[string]string{"App.xcodeproj/project.pbxproj": ""},
			wantReason: "Package.swift",
		},
		{
			name:       "php without composer.json",
			framework:  "php",
			files:      map[string]string{"src/Cart.php": "<?php\n"},
			wantReason: "composer.json",
		},
		{
			name:       "php declaring no test runner",
			framework:  "php",
			files:      map[string]string{"composer.json": `{"require":{"php":"^8.3"}}`},
			wantReason: "neither phpunit/phpunit nor pestphp/pest",
		},
		{
			name:       "dart without a pubspec",
			framework:  "dart",
			files:      map[string]string{"lib/shop.dart": ""},
			wantReason: "pubspec.yaml",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeTree(t, tc.files)
			cmds, err := FullSuiteCommand(root, tc.framework)
			if err == nil {
				t.Fatalf("FullSuiteCommand(%q) = %v, want an error", tc.framework, argvsOf(cmds))
			}
			if cmds != nil {
				t.Errorf("FullSuiteCommand(%q) returned %v alongside the error", tc.framework, argvsOf(cmds))
			}
			var nre *NoRunnerError
			if !errors.As(err, &nre) {
				t.Fatalf("error = %v, want a *NoRunnerError", err)
			}
			if !nre.WholeSuite {
				t.Errorf("WholeSuite = false; the fallback's refusal has to read as one")
			}
			if !strings.Contains(nre.Reason, tc.wantReason) {
				t.Errorf("Reason = %q, want it to mention %q", nre.Reason, tc.wantReason)
			}
		})
	}
}

// TestFullSuiteFramework is the fallback's own detection, and it is the one
// place where a framework name must NOT outrank the repo's primary language.
// FormatCommand can correct a bad guess from the extensions of the paths it was
// given; the fallback has no paths, so a Go repo that merely contains a C#
// fixture directory used to answer `dotnet test` — which dies with MSB1003 and
// runs no tests at all, in the one code path that exists to run everything.
func TestFullSuiteFramework(t *testing.T) {
	tests := []struct {
		name       string
		languages  []string
		frameworks []string
		want       string
	}{
		{
			name:       "the primary language wins over a framework from a fixture directory",
			languages:  []string{"Go", "C#", "Python"},
			frameworks: []string{"Cobra", "xUnit"},
			want:       "go",
		},
		{
			name:       "a real .NET repo still answers dotnet",
			languages:  []string{"C#", "TypeScript"},
			frameworks: []string{"xUnit"},
			want:       "dotnet",
		},
		{
			name:       "a language with no runner is skipped for one that has it",
			languages:  []string{"Markdown", "Haskell", "Python"},
			frameworks: nil,
			want:       "python",
		},
		{
			// Java is a runnable language now: the build file at the root says
			// which command, and FullSuiteCommand refuses loudly if it cannot
			// tell. Ranking Spring's "React" above it would run the wrong suite.
			name:       "a java repo is not handed to a javascript framework",
			languages:  []string{"Java", "TypeScript"},
			frameworks: []string{"Spring", "React"},
			want:       "java",
		},
		{
			name:       "frameworks are the fallback when no language has a runner",
			languages:  []string{"Haskell", "Nix"},
			frameworks: []string{"Spring", "React"},
			want:       "node",
		},
		{
			name:      "the primary language is named even with no runner, so the error can say so",
			languages: []string{"Haskell"},
			want:      "haskell",
		},
		{
			name: "nothing known stays empty",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FullSuiteFramework(tc.languages, tc.frameworks); got != tc.want {
				t.Errorf("FullSuiteFramework(%q, %q) = %q, want %q", tc.languages, tc.frameworks, got, tc.want)
			}
		})
	}
}

// "witness does not know how to run your tests" must never be expressible as an
// empty command that a caller could mistake for a clean run.
func TestFullSuiteCommandUnknownLanguage(t *testing.T) {
	for _, framework := range []string{"", "haskell", "ocaml"} {
		cmds, err := FullSuiteCommand(t.TempDir(), framework)
		if err == nil {
			t.Fatalf("FullSuiteCommand(%q) = %v, want an error", framework, cmds)
		}
		if !errors.Is(err, ErrNoRunner) {
			t.Errorf("FullSuiteCommand(%q) error = %v, want ErrNoRunner", framework, err)
		}
		if cmds != nil {
			t.Errorf("FullSuiteCommand(%q) returned %v alongside the error", framework, cmds)
		}
	}
}

// TestFullSuiteRefusalReadsAsOne is the wording contract for the message a CI
// operator meets when a fallback has nothing to run.
//
// It used to be "no test runner known for language "unknown"; 0 selected
// test(s) cannot be run: " — a sentence that names a language nobody writes,
// counts zero tests, and then lists nothing after the colon.
func TestFullSuiteRefusalReadsAsOne(t *testing.T) {
	_, err := FullSuiteCommand(t.TempDir(), "")
	if err == nil {
		t.Fatal("a repository whose language recon could not name must not yield a command")
	}
	got := err.Error()
	for _, unwanted := range []string{"unknown", "0 selected test(s)", ": "} {
		if strings.Contains(got, unwanted) {
			t.Errorf("error %q must not contain %q", got, unwanted)
		}
	}
	if !strings.Contains(got, "whole-suite") {
		t.Errorf("error %q must say the missing thing is a whole-suite command", got)
	}
	if !strings.Contains(got, "recon could not identify") {
		t.Errorf("error %q must say recon could not name the language", got)
	}
}
