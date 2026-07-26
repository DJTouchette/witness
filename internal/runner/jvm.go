package runner

import (
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// jvmCommands builds the invocations for the Java, Kotlin and Scala share of a
// selection, using the build tool detected at the repository root.
//
// None of these tools takes a test path. Maven filters by simple class name,
// Gradle and sbt by fully qualified name, so every command here is derived from
// two things read off disk — the build file that owns the test, and the package
// and class the test file itself declares. A path either yields all of that or
// yields a *NoRunnerError; a selection where only some paths resolve returns
// commands for those AND the error for the rest, so the caller cannot report a
// pass for tests that were never named.
func jvmCommands(root, lang string, paths []string) ([]Command, error) {
	switch jvmBuildSystem(root, lang) {
	case buildMaven:
		return mavenCommands(root, lang, paths)
	case buildGradle:
		return gradleCommands(root, lang, paths)
	case buildSBT:
		return sbtCommands(root, paths)
	default:
		return nil, &NoRunnerError{Lang: lang, Paths: paths, Reason: noBuildFileReason(lang)}
	}
}

// noBuildFileReason names the build files witness looked for and did not find.
func noBuildFileReason(lang string) string {
	wanted := "pom.xml or build.gradle[.kts]"
	if lang == "scala" {
		wanted = "build.sbt, pom.xml or build.gradle[.kts]"
	}
	return "no " + wanted + " at the repository root, so witness cannot tell how the tests are invoked"
}

// jvmTest is one selected test file resolved into the pieces a build tool asks
// for: the module that owns it, and the types to filter on.
type jvmTest struct {
	module string // repo-relative directory of the build file owning the test
	pkg    string // declared package, "" for the default package
	// classes are every top-level type the file declares. All of them go into
	// the filter: witness selects files, and a second test class in the same
	// file is a test the selection promised to run.
	classes []string
}

// fqcns are the fully qualified class names Gradle and sbt select by.
func (t jvmTest) fqcns() []string {
	names := make([]string, 0, len(t.classes))
	for _, class := range t.classes {
		if t.pkg == "" {
			names = append(names, class)
			continue
		}
		names = append(names, t.pkg+"."+class)
	}
	return names
}

// resolveJVMTests reads what each selected path needs, and buckets the paths it
// cannot resolve by why.
//
// manifests names the build files that mark a module, searched upwards from the
// test file; an empty list means the tool has no per-module invocation witness
// can derive. needPkg is false for Maven, which filters on the simple class name
// — refusing a test because its package was unreadable would be strict about
// something the command never uses.
func resolveJVMTests(root, lang string, paths []string, manifests []string, needPkg bool) ([]jvmTest, *noRunnerReasons) {
	var (
		tests   []jvmTest
		reasons noRunnerReasons
	)
	for _, p := range paths {
		src, err := readRepoFile(root, p)
		if err != nil {
			reasons.add("the test file could not be read from the repository root", p)
			continue
		}
		// Block comments nest everywhere here except Java, where /* /* */ ends
		// at the first close.
		stripped := stripComments(src, lang != "java")

		var test jvmTest
		if needPkg {
			// Only Scala stacks package clauses (`package com.example` then
			// `package orders` names com.example.orders).
			pkg, err := declaredPackage(stripped, lang == "scala")
			if err != nil {
				reasons.add(err.Error(), p)
				continue
			}
			test.pkg = pkg
		}
		classes, err := filterTypes(baseName(p), declaredTypes(stripped))
		if err != nil {
			reasons.add(err.Error(), p)
			continue
		}
		test.classes = classes
		if len(manifests) > 0 {
			if module, ok := nearestManifest(root, p, manifests...); ok {
				test.module = module
			}
		}
		tests = append(tests, test)
	}
	return tests, &reasons
}

// baseName is a file's name without its directory or extension — the class a
// JVM test file is named after.
func baseName(p string) string {
	base := path.Base(filepath.ToSlash(p))
	return strings.TrimSuffix(base, path.Ext(base))
}

// mavenCommands builds `mvn test -Dtest=<Class>[,<Class>]`, one command per
// Maven module.
//
// Two things it deliberately does not do:
//
//   - It never passes -DfailIfNoTests=false. That flag is the usual answer to a
//     multi-module reactor, where modules without the named class fail the build
//     with "No tests were executed!" — but it also turns a class name witness got
//     wrong into a silent zero-test success. Surefire's default is to fail, and
//     failing is the answer witness wants.
//   - It scopes to the owning module with -pl instead, derived from the nearest
//     pom.xml, so the filter runs where the class actually is and cannot match
//     nothing. -am is left off on purpose: it would drag the dependency modules
//     into the same filtered run and fail them for having no matching test.
func mavenCommands(root, lang string, paths []string) ([]Command, error) {
	tests, reasons := resolveJVMTests(root, lang, paths, []string{"pom.xml"}, false)

	byModule := make(map[string]map[string]bool)
	for _, test := range tests {
		if byModule[test.module] == nil {
			byModule[test.module] = make(map[string]bool)
		}
		for _, class := range test.classes {
			byModule[test.module][class] = true
		}
	}

	mvn := buildTool(root, "mvnw", "mvn")
	commands := make([]Command, 0, len(byModule))
	for _, module := range sortedKeys(byModule) {
		argv := []string{mvn, "test"}
		if module != "" {
			argv = append(argv, "-pl", module)
		}
		argv = append(argv, "-Dtest="+strings.Join(sortedKeys(byModule[module]), ","))
		commands = append(commands, Command{Lang: lang, Argv: argv})
	}
	return commands, reasons.err(lang)
}

// gradleCommands builds `gradle :<project>:test --tests <FQCN>`, one command per
// Gradle project.
//
// The project path comes from the directory of the nearest build.gradle[.kts],
// which is the default layout settings.gradle produces. Running the root `test`
// task instead would run it in every subproject, and Gradle fails a subproject
// whose tests none of the --tests patterns match — a red build, not a false
// green, but a confusing one. A settings.gradle that remaps projectDir makes the
// derived path wrong; Gradle then answers "project not found" and exits
// non-zero, which is the loud failure witness prefers to a guess.
func gradleCommands(root, lang string, paths []string) ([]Command, error) {
	tests, reasons := resolveJVMTests(root, lang, paths, []string{"build.gradle", "build.gradle.kts"}, true)

	byProject := make(map[string]map[string]bool)
	for _, test := range tests {
		if byProject[test.module] == nil {
			byProject[test.module] = make(map[string]bool)
		}
		for _, fqcn := range test.fqcns() {
			byProject[test.module][fqcn] = true
		}
	}

	gradle := buildTool(root, "gradlew", "gradle")
	commands := make([]Command, 0, len(byProject))
	for _, project := range sortedKeys(byProject) {
		task := "test"
		if project != "" {
			task = ":" + strings.ReplaceAll(project, "/", ":") + ":test"
		}
		argv := []string{gradle, task}
		for _, fqcn := range sortedKeys(byProject[project]) {
			argv = append(argv, "--tests", fqcn)
		}
		commands = append(commands, Command{Lang: lang, Argv: argv})
	}
	return commands, reasons.err(lang)
}

// sbtCommands builds `sbt 'testOnly <FQCN> <FQCN>'` for a single-project build,
// and refuses a multi-project one.
//
// The whole selection goes into one invocation because sbt's startup cost is
// paid once per run. There is no per-project scoping to derive: an sbt project's
// id is the name of a val in build.sbt (or the first argument to `Project`), and
// nothing ties it to the directory a test file sits in.
//
// The risk this arm carries is that sbt reports a filter matching nothing as
// SUCCESS. `sbt 'testOnly com.example.NoSuchClass'` prints "No tests to run for
// Test / testOnly" and exits 0. That is why the class name comes from a
// declaration inside the file rather than from the path, why a nested type is
// refused, and why a multi-project build is refused here: an unscoped
// root-level testOnly only reaches the projects the ROOT AGGREGATES, and a build
// that defines its own root — `lazy val root = (project in file("."))`, the
// idiom for setting a name or `publish / skip := true` — aggregates nothing
// unless it says so. The module under test is then never visited, zero tests
// run, and the gate goes green.
//
// Widening is not a way out: bare `sbt test` in the same build exits 0 having
// run nothing too. Refusing is the only answer that cannot lie.
func sbtCommands(root string, paths []string) ([]Command, error) {
	if sbtMultiProject(root, paths) {
		return nil, &NoRunnerError{Lang: "scala", Paths: paths, Reason: sbtMultiProjectReason}
	}

	tests, reasons := resolveJVMTests(root, "scala", paths, nil, true)

	names := make(map[string]bool, len(tests))
	for _, test := range tests {
		for _, fqcn := range test.fqcns() {
			names[fqcn] = true
		}
	}
	var commands []Command
	if len(names) > 0 {
		commands = []Command{{
			Lang: "scala",
			Argv: []string{"sbt", "testOnly " + strings.Join(sortedKeys(names), " ")},
		}}
	}
	return commands, reasons.err("scala")
}

// sbtMultiProjectReason says why a multi-project sbt build gets no command.
const sbtMultiProjectReason = "the build defines more than one sbt project, and witness cannot derive the project id " +
	"`sbt '<project>/testOnly <suite>'` needs; an unscoped testOnly reaches only what the root aggregates, and sbt " +
	"reports a filter that matched nothing as success — run it yourself with the project prefix, or pass --test-cmd"

// sbtProjectDeclRe matches the start of an sbt project declaration, in every
// spelling a build.sbt uses:
//
//	lazy val root = (project in file("."))
//	lazy val core = project.in(file("core"))
//	lazy val core = project                     // base is the val's own name
//	lazy val core = Project("core", file("core"))
var sbtProjectDeclRe = regexp.MustCompile(`\bval[ \t]+[A-Za-z_][A-Za-z0-9_$]*[ \t]*=[ \t]*\(?[ \t]*(?:project\b|Project[ \t]*\()`)

// sbtFileArgRe pulls the directory out of the first `file("...")` in a project
// declaration.
var sbtFileArgRe = regexp.MustCompile(`\bfile[ \t]*\([ \t]*"([^"]*)"`)

// sbtMultiProject reports whether the build at root is one where a root-level
// `testOnly` cannot be trusted.
//
// Two pieces of evidence, either of which is enough:
//
//   - a selected test whose nearest build.sbt is not the root's, which is a
//     subproject keeping its own settings file; or
//   - a root build.sbt declaring an EXPLICIT ROOT PROJECT — one based at "." —
//     alongside at least one other project.
//
// The explicit root is the whole point. When a build declares no project at "."
// sbt generates one, and the generated root AGGREGATES every project in the
// build, so `testOnly` typed at the root reaches all of them and a failing suite
// fails the run. Declare `lazy val root = (project in file("."))` — the idiom
// for setting a name, or `publish / skip := true` — and that generated root goes
// away with its aggregation, unless the build says `.aggregate(...)` itself.
// witness cannot check that it says it for the right project, so it refuses.
//
// A build.sbt with no project declaration at all — just `name := "app"` and some
// settings — is the ordinary single-project shape and is not refused. Note which
// way the remaining uncertainty points: a build that DOES aggregate and gets
// refused here is a false alarm, and a false alarm exits non-zero.
func sbtMultiProject(root string, paths []string) bool {
	for _, p := range paths {
		if dir, ok := nearestManifest(root, p, "build.sbt"); ok && dir != "" {
			return true
		}
	}
	src, err := readRepoFile(root, "build.sbt")
	if err != nil {
		return false
	}
	// Scala's block comments nest, and a commented-out subproject is not one.
	bases := sbtProjectBases(string(stripComments(src, true)))
	if len(bases) < 2 {
		return false
	}
	for _, base := range bases {
		if base == "." {
			return true
		}
	}
	return false
}

// sbtProjectBases returns the base directory of every project the build script
// declares, in file order. A declaration with no `file(...)` — sbt's `lazy val
// core = project`, whose base is the val's name — reports "", which is never the
// root.
func sbtProjectBases(src string) []string {
	decls := sbtProjectDeclRe.FindAllStringIndex(src, -1)
	bases := make([]string, 0, len(decls))
	for i, decl := range decls {
		// A declaration runs until the next one starts; its first file("...")
		// is its base.
		end := len(src)
		if i+1 < len(decls) {
			end = decls[i+1][0]
		}
		base := ""
		if m := sbtFileArgRe.FindStringSubmatch(src[decl[1]:end]); m != nil {
			base = path.Clean(m[1])
		}
		bases = append(bases, base)
	}
	return bases
}

// sortedKeys returns a map's keys in sorted order, so emitted commands are
// byte-identical across runs.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
