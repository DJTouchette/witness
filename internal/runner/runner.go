package runner

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNoRunner reports that witness knows of no test runner for a language, so
// a selection cannot be turned into a command. It is always an error: running
// nothing and exiting 0 would be a false green in CI.
var ErrNoRunner = errors.New("no test runner known")

// NoRunnerError names the language witness could not build a command for and
// the selected tests that would go unrun. It wraps ErrNoRunner.
type NoRunnerError struct {
	Lang  string
	Paths []string
	// Reason, when set, says what stopped witness from deriving the command —
	// no build file at the root, an unreadable package declaration — for the
	// languages whose invocation is not a path. It is advice for a human, and
	// empty for a language witness simply has no runner for.
	Reason string
	// WholeSuite marks the full-suite fallback path, where what is missing is a
	// command that runs EVERYTHING rather than a way to name selected tests.
	// The two read nothing alike to the CI operator who has to act on them, and
	// there are no Paths to list: the fallback used to print "0 selected test(s)
	// cannot be run:" followed by nothing at all.
	WholeSuite bool
}

func (e *NoRunnerError) Error() string {
	var b strings.Builder
	if e.WholeSuite {
		b.WriteString("no whole-suite test command known for " + e.LangLabel())
	} else {
		b.WriteString(ErrNoRunner.Error() + " for " + e.LangLabel())
	}
	if e.Reason != "" {
		b.WriteString(" (" + e.Reason + ")")
	}
	if len(e.Paths) > 0 {
		fmt.Fprintf(&b, "; %d selected test(s) cannot be run: %s", len(e.Paths), strings.Join(e.Paths, " "))
	}
	return b.String()
}

// LangLabel names the language for a message.
//
// "unknown" is the placeholder groupByLang and the fallback assign when nothing
// said what the code is written in; printing it bare reads like a language
// called unknown, and sends the reader looking for a runner that was never the
// problem.
func (e *NoRunnerError) LangLabel() string {
	if e.Lang == "" || e.Lang == "unknown" {
		return "a language recon could not identify"
	}
	return fmt.Sprintf("language %q", e.Lang)
}

func (e *NoRunnerError) Unwrap() error { return ErrNoRunner }

// Command is a single test runner invocation. Argv is handed straight to
// exec.Command — witness never builds a shell string — so paths containing
// spaces, parens or shell metacharacters need no quoting and cannot inject.
type Command struct {
	// Argv is the program followed by its arguments. Never empty.
	Argv []string
	// Lang is the language group the command covers ("go", "rust", ...).
	Lang string
}

// String renders the command as a copy-pasteable shell line, quoting any
// argument that needs it. It is for display only; execution uses Argv.
func (c Command) String() string {
	quoted := make([]string, 0, len(c.Argv))
	for _, arg := range c.Argv {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

// FormatCommand builds the test runner commands for the given framework and
// test paths, in the repository rooted at root.
//
// Paths are grouped by the language of the file, not by a single project-wide
// framework, so a polyglot selection yields one command per ecosystem instead
// of one command that no runner understands. framework is only a hint, used
// for paths whose extension does not identify a language. Test paths are
// relative to root, which is also where the commands are meant to run.
//
// root is read, not just joined onto: Maven, Gradle, sbt, SwiftPM, PHPUnit,
// dart and cargo-in-a-workspace all need something no path can supply — which
// build file owns the tree, what package a test file declares, what a crate is
// called. An empty root means witness has no filesystem context; the arms that
// need one then report ErrNoRunner instead of inventing an invocation.
//
// A language with no known runner, or a path whose invocation cannot be
// derived, produces a *NoRunnerError (wrapping ErrNoRunner) rather than a bare
// path list that a shell would try to execute. Commands for the paths that WERE
// understood are still returned alongside the error; callers must not drop the
// error, since running a subset and exiting 0 would report a false pass.
func FormatCommand(root, framework string, testPaths []string) ([]Command, error) {
	if len(testPaths) == 0 {
		return nil, nil
	}

	groups, langs := groupByLang(testPaths, frameworkLang(framework))

	var (
		commands []Command
		errs     []error
	)
	for _, lang := range langs {
		// Both are meaningful together: an arm that derived commands for half
		// its paths returns those AND the error naming the other half.
		cmds, err := commandsFor(root, lang, groups[lang])
		commands = append(commands, cmds...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return commands, errors.Join(errs...)
}

// OverrideCommand appends a selection to a test command the user supplied
// (--test-cmd / --runner) and returns the single invocation to run.
//
// Paths are translated the way FormatCommand translates them only for the
// languages whose runners actually take paths: `go test pkg/calc/calc_test.go`
// compiles that one file as package command-line-arguments and dies with
// "undefined: Add", so Go gets package globs here too.
//
// Three shapes are refused outright rather than silently mis-run:
//
//   - a selection spanning more than one language, which would hand .js files
//     to `mix test` — the single-command bug FormatCommand exists to avoid;
//   - cargo, whose positional arguments are test-NAME filters: appending .rs
//     paths matches nothing, reports "0 filtered out" and exits 0; and
//   - the JVM languages and Swift, whose runners select by class, fully
//     qualified name or filter rather than by path. `mvn test Foo.java` dies
//     with LifecyclePhaseNotFoundException and sbt with "Not a valid key" —
//     loud, but wrong, and the correct selector is not derivable from a path
//     without the build-system context commandsFor resolves.
//
// An empty selection (the full-suite fallback) just runs the command as given.
func OverrideCommand(argv []string, framework string, paths []string) ([]Command, error) {
	if len(paths) == 0 {
		return []Command{{Argv: argv, Lang: frameworkLang(framework)}}, nil
	}

	groups, langs := groupByLang(paths, frameworkLang(framework))
	if len(langs) > 1 {
		return nil, fmt.Errorf("--test-cmd cannot run a selection spanning %d languages (%s): one command cannot test them all; drop --test-cmd, or narrow the selection with --kind/--exclude",
			len(langs), strings.Join(langs, ", "))
	}

	lang := langs[0]
	switch lang {
	case "go":
		argv = append(argv, goPackages(groups[lang])...)
	case "rust":
		return nil, fmt.Errorf("--test-cmd cannot name rust tests: cargo reads a positional argument as a test-name filter, so appending %d path(s) would match nothing and still exit 0; use the detected command, or pass cargo's own selectors after `--`",
			len(groups[lang]))
	case "java", "kotlin", "scala", "swift":
		return nil, fmt.Errorf("--test-cmd cannot name %s tests by path: that runner selects by class, fully qualified name or filter, so appending %d path(s) would fail or run nothing; use the detected command, or name the tests yourself in --test-cmd (e.g. --test-cmd \"mvn test -Dtest=CalculatorTest\") with the selection left empty",
			lang, len(groups[lang]))
	default:
		argv = append(argv, groups[lang]...)
	}
	return []Command{{Argv: argv, Lang: lang}}, nil
}

// commandsFor builds the invocations for one language's share of a selection.
//
// An arm may return commands and an error together: the JVM, Swift and PHP arms
// derive per path, and a selection where only some paths resolve is reported as
// exactly that.
func commandsFor(root, lang string, paths []string) ([]Command, error) {
	switch lang {
	case "elixir":
		return []Command{{Lang: lang, Argv: append([]string{"mix", "test"}, paths...)}}, nil
	case "go":
		return []Command{{Lang: lang, Argv: append([]string{"go", "test"}, goPackages(paths)...)}}, nil
	case "python":
		return []Command{{Lang: lang, Argv: append([]string{"pytest"}, paths...)}}, nil
	case "ruby":
		return []Command{{Lang: lang, Argv: append([]string{"bundle", "exec", "rspec"}, paths...)}}, nil
	case "node":
		// --runTestsByPath, never a bare positional: jest treats positionals as
		// REGEXES matched against collected paths, so a Next.js route group
		// (src/app/(marketing)/page.test.tsx) reads as a capture group and
		// matches nothing. jest then reports "No tests found" and — with the
		// mainstream passWithNoTests setting — exits 0, so witness passes
		// having run none of the tests it selected. The same holds for any path
		// containing + [ ] ? | or $.
		return []Command{{Lang: lang, Argv: append([]string{"npx", "jest", "--runTestsByPath"}, paths...)}}, nil
	case "rust":
		return rustCommands(root, paths), nil
	case "dotnet":
		return dotNetCommands(paths), nil
	case "java", "kotlin", "scala":
		return jvmCommands(root, lang, paths)
	case "swift":
		return swiftCommands(root, paths)
	case "php":
		return phpCommands(root, paths)
	case "dart":
		return dartCommands(root, paths)
	default:
		return nil, &NoRunnerError{Lang: lang, Paths: paths}
	}
}

// DetectFramework returns a framework name from recon's overview data.
func DetectFramework(frameworks []string) string {
	for _, f := range frameworks {
		lower := strings.ToLower(f)
		switch {
		case strings.Contains(lower, "asp.net") || strings.Contains(lower, ".net") ||
			strings.Contains(lower, "xunit") || strings.Contains(lower, "nunit") ||
			strings.Contains(lower, "mstest"):
			return "dotnet"
		case strings.Contains(lower, "phoenix"):
			return "elixir"
		case strings.Contains(lower, "gin") || strings.Contains(lower, "echo") || strings.Contains(lower, "fiber"):
			return "go"
		case strings.Contains(lower, "django") || strings.Contains(lower, "flask") || strings.Contains(lower, "fastapi"):
			return "python"
		case strings.Contains(lower, "rails"):
			return "ruby"
		case strings.Contains(lower, "react") || strings.Contains(lower, "next") || strings.Contains(lower, "express") || strings.Contains(lower, "vue"):
			return "node"
		}
	}
	return ""
}

// groupByLang buckets test paths by language, falling back to hint for paths
// whose extension says nothing. It returns the buckets plus their keys in
// sorted order, so the emitted commands are deterministic.
func groupByLang(paths []string, hint string) (map[string][]string, []string) {
	groups := make(map[string][]string)
	for _, p := range paths {
		lang := langForPath(p)
		if lang == "" {
			lang = hint
		}
		if lang == "" {
			lang = "unknown"
		}
		groups[lang] = append(groups[lang], p)
	}
	langs := make([]string, 0, len(groups))
	for lang := range groups {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	return groups, langs
}

// langForPath maps a test file to the language group that runs it. Languages
// the selector recognises but witness has no runner for (java, kotlin, ...)
// are named here on purpose, so the resulting error can say which one.
func langForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ex", ".exs":
		return "elixir"
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".rb":
		return "ruby"
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return "node"
	case ".rs":
		return "rust"
	case ".cs":
		return "dotnet"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".scala", ".sc":
		return "scala"
	case ".swift":
		return "swift"
	case ".php":
		return "php"
	case ".dart":
		return "dart"
	}
	return ""
}

// frameworkLang maps a framework (or language) name from recon's overview to a
// language group. An unrecognised name is kept as-is so it can be named in the
// no-runner error.
func frameworkLang(framework string) string {
	switch lower := strings.ToLower(strings.TrimSpace(framework)); lower {
	case "":
		return ""
	case "elixir", "phoenix":
		return "elixir"
	case "go", "gin", "echo", "fiber":
		return "go"
	case "python", "django", "flask", "fastapi":
		return "python"
	case "ruby", "rails":
		return "ruby"
	case "node", "express", "next.js", "react", "vue", "typescript", "javascript":
		return "node"
	case "rust":
		return "rust"
	case "csharp", "c#", ".net", "dotnet", "asp.net core", "xunit", "nunit", "mstest":
		return "dotnet"
	default:
		return lower
	}
}

// goPackages converts test file paths to deduped, sorted Go package globs.
func goPackages(paths []string) []string {
	pkgs := make(map[string]bool)
	for _, p := range paths {
		dir := filepath.ToSlash(filepath.Dir(p))
		pkgs["./"+dir+"/..."] = true
	}
	result := make([]string, 0, len(pkgs))
	for pkg := range pkgs {
		result = append(result, pkg)
	}
	// Sorted so the emitted command is byte-identical across runs.
	sort.Strings(result)
	return result
}

// dotNetCommands builds one `dotnet test <project>` per selected project.
// They are separate commands rather than a single `&&` chain so that a failing
// project cannot short-circuit the ones after it.
func dotNetCommands(paths []string) []Command {
	projectDirs := make(map[string]bool)
	whole := false
	for _, p := range paths {
		dir := dotNetProjectDir(p)
		if dir == "" {
			whole = true
			continue
		}
		projectDirs["./"+dir] = true
	}
	// A selected test we cannot pin to a project widens the run to the whole
	// solution: over-running is safe, dropping the test silently is not.
	if whole || len(projectDirs) == 0 {
		return []Command{{Lang: "dotnet", Argv: []string{"dotnet", "test"}}}
	}

	targets := make([]string, 0, len(projectDirs))
	for dir := range projectDirs {
		targets = append(targets, dir)
	}
	sort.Strings(targets)

	commands := make([]Command, 0, len(targets))
	for _, target := range targets {
		commands = append(commands, Command{Lang: "dotnet", Argv: []string{"dotnet", "test", target}})
	}
	return commands
}

// dotNetProjectDir returns the project directory owning a test file, or "" if
// the file cannot be pinned to one.
func dotNetProjectDir(path string) string {
	p := filepath.ToSlash(filepath.Clean(path))
	parts := strings.Split(p, "/")
	for i, part := range parts[:len(parts)-1] {
		lower := strings.ToLower(part)
		switch {
		case strings.HasSuffix(lower, ".tests"),
			strings.HasSuffix(lower, ".test"),
			strings.HasSuffix(lower, ".integrationtests"),
			strings.HasSuffix(lower, ".unittests"),
			strings.HasSuffix(lower, ".e2etests"),
			strings.HasSuffix(lower, ".e2e"):
			return strings.Join(parts[:i+1], "/")
		}
	}
	// A test project that does not use the *.Tests suffix (Acme.Specs,
	// Acme.Api.Functional) still lives in its own directory. But a file
	// sitting directly in test/ or tests/ — or at the repo root — has no
	// project to point at, and `dotnet test ./tests` dies with MSB1003, so
	// report "" and let the caller widen to the whole solution.
	dir := filepath.ToSlash(filepath.Dir(p))
	if dir == "." || dir == "/" {
		return ""
	}
	switch strings.ToLower(filepath.Base(dir)) {
	case "test", "tests":
		return ""
	}
	return dir
}

// shellSafe reports whether an argument can be printed unquoted.
func shellSafe(arg string) bool {
	if arg == "" {
		return false
	}
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			if !strings.ContainsRune("_@%+=:,./-", r) {
				return false
			}
		}
	}
	return true
}

// shellQuote single-quotes an argument for display so the printed command can
// be pasted into a shell verbatim.
func shellQuote(arg string) string {
	if shellSafe(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}
