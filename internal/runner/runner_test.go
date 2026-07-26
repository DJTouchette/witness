package runner

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// argvsOf flattens commands to their argv slices for exact comparison.
func argvsOf(cmds []Command) [][]string {
	got := make([][]string, 0, len(cmds))
	for _, c := range cmds {
		got = append(got, c.Argv)
	}
	return got
}

func TestFormatCommand(t *testing.T) {
	tests := []struct {
		name      string
		framework string
		paths     []string
		want      [][]string
	}{
		{
			name:      "elixir",
			framework: "elixir",
			paths:     []string{"test/a_test.exs", "test/b_test.exs"},
			want:      [][]string{{"mix", "test", "test/a_test.exs", "test/b_test.exs"}},
		},
		{
			name:      "phoenix",
			framework: "phoenix",
			paths:     []string{"test/a_test.exs"},
			want:      [][]string{{"mix", "test", "test/a_test.exs"}},
		},
		{
			name:      "python",
			framework: "python",
			paths:     []string{"tests/test_a.py"},
			want:      [][]string{{"pytest", "tests/test_a.py"}},
		},
		{
			name:      "ruby",
			framework: "ruby",
			paths:     []string{"spec/a_spec.rb"},
			want:      [][]string{{"bundle", "exec", "rspec", "spec/a_spec.rb"}},
		},
		{
			name:      "node",
			framework: "node",
			paths:     []string{"src/a.test.ts"},
			want:      [][]string{{"npx", "jest", "--runTestsByPath", "src/a.test.ts"}},
		},
		{
			name:      "go",
			framework: "go",
			paths:     []string{"internal/a/x_test.go"},
			want:      [][]string{{"go", "test", "./internal/a/..."}},
		},
		{
			// `cargo test <path>` is a test NAME filter: it would run nothing
			// and exit 0. The target must be named instead.
			name:      "rust integration test",
			framework: "rust",
			paths:     []string{"tests/a.rs"},
			want:      [][]string{{"cargo", "test", "--test", "a"}},
		},
		{
			name:      "csharp",
			framework: "csharp",
			paths: []string{
				"backend/tests/Leroy.Platform.Tests/CertificateTests.cs",
				"backend/tests/Leroy.Platform.Tests/OtherTests.cs",
				"backend/tests/Leroy.Api.IntegrationTests/CertificatesApiTests.cs",
			},
			// Separate commands, not an `&&` chain: a red first project must
			// not skip the second.
			want: [][]string{
				{"dotnet", "test", "./backend/tests/Leroy.Api.IntegrationTests"},
				{"dotnet", "test", "./backend/tests/Leroy.Platform.Tests"},
			},
		},
		{
			name:      "no paths",
			framework: "",
			paths:     nil,
			want:      [][]string{},
		},
		{
			// The framework is only a hint; the file extension decides, so a
			// polyglot selection gets one command per ecosystem instead of
			// `mix test cart.test.js orders_test.exs`.
			name:      "polyglot elixir and node",
			framework: "elixir",
			paths:     []string{"test/orders_test.exs", "assets/cart.test.js"},
			want: [][]string{
				{"mix", "test", "test/orders_test.exs"},
				{"npx", "jest", "--runTestsByPath", "assets/cart.test.js"},
			},
		},
		{
			name:      "extension beats a mismatched framework",
			framework: "python",
			paths:     []string{"spec/a_spec.rb"},
			want:      [][]string{{"bundle", "exec", "rspec", "spec/a_spec.rb"}},
		},
		{
			name:      "framework decides when the extension does not",
			framework: "python",
			paths:     []string{"tests/no_extension"},
			want:      [][]string{{"pytest", "tests/no_extension"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormatCommand("", tt.framework, tt.paths)
			if err != nil {
				t.Fatalf("FormatCommand(%q, %v): %v", tt.framework, tt.paths, err)
			}
			if !reflect.DeepEqual(argvsOf(got), tt.want) {
				t.Errorf("FormatCommand(%q, %v) argv = %v, want %v", tt.framework, tt.paths, argvsOf(got), tt.want)
			}
		})
	}
}

func TestFormatCommand_UnknownLanguage(t *testing.T) {
	// The old fallback returned the bare joined paths, which `sh -c` then tried
	// to EXECUTE (exit 126, reported as "tests failed"). Every language the
	// selector recognises but witness cannot run must error instead.
	for _, tt := range []struct {
		framework string
		path      string
		wantLang  string
		// wantLabel is how the language reads in the printed message. It is not
		// always Lang: "unknown" is the placeholder for a file whose extension
		// said nothing, and printing it bare reads like a language.
		wantLabel string
	}{
		{"java", "src/test/java/com/ex/OrderTest.java", "java", `language "java"`},
		{"kotlin", "src/test/kotlin/OrderTest.kt", "kotlin", `language "kotlin"`},
		{"php", "tests/OrderTest.php", "php", `language "php"`},
		{"scala", "src/test/scala/OrderSpec.scala", "scala", `language "scala"`},
		{"swift", "Tests/OrderTests.swift", "swift", `language "swift"`},
		{"dart", "test/order_test.dart", "dart", `language "dart"`},
		{"cobol", "tests/order", "cobol", `language "cobol"`},
		{"", "tests/order", "unknown", "a language recon could not identify"},
	} {
		t.Run(tt.framework, func(t *testing.T) {
			cmds, err := FormatCommand("", tt.framework, []string{tt.path})
			if err == nil {
				t.Fatalf("FormatCommand(%q, [%q]) = %v, want ErrNoRunner", tt.framework, tt.path, argvsOf(cmds))
			}
			if !errors.Is(err, ErrNoRunner) {
				t.Fatalf("error = %v, want it to wrap ErrNoRunner", err)
			}
			var nre *NoRunnerError
			if !errors.As(err, &nre) {
				t.Fatalf("error = %v, want a *NoRunnerError", err)
			}
			if nre.Lang != tt.wantLang {
				t.Errorf("Lang = %q, want %q", nre.Lang, tt.wantLang)
			}
			if !reflect.DeepEqual(nre.Paths, []string{tt.path}) {
				t.Errorf("Paths = %v, want %v", nre.Paths, []string{tt.path})
			}
			if len(cmds) != 0 {
				t.Errorf("commands = %v, want none", argvsOf(cmds))
			}
			// The CLI prints this, so it has to name the language and the
			// tests that would otherwise be skipped in silence.
			if msg := err.Error(); !strings.Contains(msg, tt.wantLabel) || !strings.Contains(msg, tt.path) {
				t.Errorf("error message %q should name both the language and the test", msg)
			}
		})
	}
}

func TestFormatCommand_PartialPolyglotStillErrors(t *testing.T) {
	// A Go+Java monorepo: the Go half is runnable, but the Java half is not.
	// The runnable commands come back so a caller can report them, and the
	// error comes back so the caller cannot silently pass.
	cmds, err := FormatCommand("", "go", []string{"internal/a/x_test.go", "src/test/java/OrderTest.java"})
	if !errors.Is(err, ErrNoRunner) {
		t.Fatalf("err = %v, want ErrNoRunner", err)
	}
	want := [][]string{{"go", "test", "./internal/a/..."}}
	if !reflect.DeepEqual(argvsOf(cmds), want) {
		t.Errorf("commands = %v, want %v", argvsOf(cmds), want)
	}
}

func TestDetectFramework(t *testing.T) {
	tests := []struct {
		frameworks []string
		want       string
	}{
		{[]string{"Phoenix"}, "elixir"},
		{[]string{"Rails"}, "ruby"},
		{[]string{"React", "Express"}, "node"},
		{[]string{"Django"}, "python"},
		{[]string{"Microsoft.NET.Test.Sdk"}, "dotnet"},
		{[]string{"ASP.NET Core"}, "dotnet"},
		{[]string{"xunit"}, "dotnet"},
		{[]string{"unknown"}, ""},
		{nil, ""},
	}

	for _, tt := range tests {
		got := DetectFramework(tt.frameworks)
		if got != tt.want {
			t.Errorf("DetectFramework(%v) = %q, want %q", tt.frameworks, got, tt.want)
		}
	}
}
