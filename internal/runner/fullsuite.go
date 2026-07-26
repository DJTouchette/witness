package runner

import "strings"

// FullSuiteFramework picks the ecosystem whose whole suite the fail-closed
// fallback should run, given recon's languages (most dominant first) and the
// frameworks it detected.
//
// Unlike FormatCommand, the fallback has no selected paths to correct a bad
// guess with, so the repo's PRIMARY LANGUAGE decides — not a framework name.
// Framework detection matches any name containing ".net"/"xunit", so a Go
// repository holding one C# fixture directory answered `dotnet test`, which
// dies with MSB1003 having run no tests: the fail-closed path failing to run
// anything is the failure it exists to prevent.
//
// A language witness has no runner for is skipped in favour of one it can run,
// and only if none of them is runnable do the frameworks get a say. The primary
// language is still returned as a last resort so the resulting error can name
// it.
func FullSuiteFramework(languages, frameworks []string) string {
	for _, lang := range languages {
		if name := frameworkLang(lang); knownRunner(name) {
			return name
		}
	}
	if framework := DetectFramework(frameworks); framework != "" {
		return framework
	}
	if len(languages) > 0 {
		return strings.ToLower(strings.TrimSpace(languages[0]))
	}
	return ""
}

// knownRunner reports whether witness can run a language's whole suite.
//
// The six build-file languages are in here even though their command depends on
// what is at the repository root: FullSuiteCommand reads the root and refuses
// loudly when it finds nothing it recognises, which is a better answer than
// letting FullSuiteFramework pass over a Java repository in favour of whatever
// language recon ranked second.
func knownRunner(lang string) bool {
	switch lang {
	case "elixir", "go", "python", "ruby", "node", "rust", "dotnet",
		"java", "kotlin", "scala", "swift", "php", "dart":
		return true
	default:
		return false
	}
}

// FullSuiteCommand returns the invocation that runs a project's entire test
// suite, given the repository root and the framework or language name recon
// reports for it.
//
// It backs witness's fail-closed fallback: when a selection cannot be proven to
// cover a change, running everything is the only answer that cannot report a
// pass witness did not earn. A language with no known runner yields a
// *NoRunnerError rather than an empty command, so "witness does not know how to
// run your tests" can never be mistaken for "your tests passed".
//
// root is read for the same reason FormatCommand reads it: `mvn test` and
// `gradle test` are not interchangeable, `dart test` fails in a Flutter package,
// and `vendor/bin/phpunit` runs none of a Pest project's tests. Where the root
// does not say, witness refuses instead of picking one.
func FullSuiteCommand(root, framework string) ([]Command, error) {
	lang := frameworkLang(framework)
	switch lang {
	case "elixir":
		return wholeSuite(lang, "mix", "test")
	case "go":
		return wholeSuite(lang, "go", "test", "./...")
	case "python":
		return wholeSuite(lang, "pytest")
	case "ruby":
		return wholeSuite(lang, "bundle", "exec", "rspec")
	case "node":
		return wholeSuite(lang, "npx", "jest")
	case "rust":
		return wholeSuite(lang, "cargo", "test")
	case "dotnet":
		return wholeSuite(lang, "dotnet", "test")
	case "java", "kotlin", "scala":
		return jvmFullSuite(root, lang)
	case "swift":
		if !hasFile(root, "Package.swift") {
			return nil, &NoRunnerError{Lang: lang, WholeSuite: true, Reason: swiftNoPackageReason}
		}
		return wholeSuite(lang, "swift", "test")
	case "php":
		binary, reason := phpTestBinary(root)
		if reason != "" {
			return nil, &NoRunnerError{Lang: lang, WholeSuite: true, Reason: reason}
		}
		return wholeSuite(lang, binary)
	case "dart":
		pubspec, err := readRepoFile(root, "pubspec.yaml")
		if err != nil {
			return nil, &NoRunnerError{Lang: lang, WholeSuite: true, Reason: dartNoPubspecReason}
		}
		return wholeSuite(lang, dartTestTool(pubspec), "test")
	default:
		return nil, &NoRunnerError{Lang: lang, WholeSuite: true}
	}
}

// jvmFullSuite runs every test in a JVM project, using the build tool at the
// repository root.
//
// sbt is the one build system with no safe whole-suite command: `sbt test` at a
// root that aggregates nothing runs no tests and exits 0, exactly as
// `sbt testOnly` does, so a multi-project build is refused here for the same
// reason sbtCommands refuses it. Maven and Gradle both fail loudly when their
// root task finds nothing to do.
func jvmFullSuite(root, lang string) ([]Command, error) {
	switch jvmBuildSystem(root, lang) {
	case buildMaven:
		return wholeSuite(lang, buildTool(root, "mvnw", "mvn"), "test")
	case buildGradle:
		return wholeSuite(lang, buildTool(root, "gradlew", "gradle"), "test")
	case buildSBT:
		if sbtMultiProject(root, nil) {
			return nil, &NoRunnerError{Lang: lang, WholeSuite: true, Reason: sbtMultiProjectWholeReason}
		}
		return wholeSuite(lang, "sbt", "test")
	default:
		return nil, &NoRunnerError{Lang: lang, WholeSuite: true, Reason: noBuildFileReason(lang)}
	}
}

// sbtMultiProjectWholeReason is sbtMultiProjectReason for the fallback, where
// there is no per-project testOnly to suggest either.
const sbtMultiProjectWholeReason = "the build defines more than one sbt project, and `sbt test` at the root runs only " +
	"what the root aggregates — a build with an explicit root project aggregates nothing unless it says so, and sbt " +
	"reports having run no tests as success; run the per-project `sbt '<project>/test'` tasks yourself, or pass --test-cmd"

// wholeSuite is the one-command shape every runnable arm above returns.
func wholeSuite(lang string, argv ...string) ([]Command, error) {
	return []Command{{Lang: lang, Argv: argv}}, nil
}
