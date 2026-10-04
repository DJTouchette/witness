package runner

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// javaSource is a minimal but realistic JUnit test class.
func javaSource(pkg, class string) string {
	return "package " + pkg + ";\n\n" +
		"import org.junit.jupiter.api.Test;\n\n" +
		"class " + class + " {\n" +
		"    @Test\n" +
		"    void works() {}\n" +
		"}\n"
}

// kotlinSource is a minimal but realistic Kotlin test class.
func kotlinSource(pkg, class string) string {
	header := ""
	if pkg != "" {
		header = "package " + pkg + "\n\n"
	}
	return header +
		"import kotlin.test.Test\n\n" +
		"class " + class + " {\n" +
		"    @Test\n" +
		"    fun works() {}\n" +
		"}\n"
}

// scalaSource is a minimal but realistic ScalaTest suite.
func scalaSource(pkgClauses []string, class string) string {
	var b strings.Builder
	for _, clause := range pkgClauses {
		b.WriteString("package " + clause + "\n")
	}
	b.WriteString("\nimport org.scalatest.funsuite.AnyFunSuite\n\n")
	b.WriteString("class " + class + " extends AnyFunSuite {\n  test(\"works\") { assert(true) }\n}\n")
	return b.String()
}

func TestFormatCommand_Maven(t *testing.T) {
	tests := []struct {
		name  string
		lang  string
		files map[string]string
		paths []string
		want  [][]string
	}{
		{
			// Surefire's -Dtest takes a class name. A path there is not a
			// filter maven understands at all.
			name: "single module names the class",
			lang: "java",
			files: map[string]string{
				"pom.xml": "<project/>",
				"src/test/java/com/example/CalculatorTest.java": javaSource("com.example", "CalculatorTest"),
			},
			paths: []string{"src/test/java/com/example/CalculatorTest.java"},
			want:  [][]string{{"mvn", "test", "-Dtest=CalculatorTest"}},
		},
		{
			name: "several classes in one module are one comma-separated filter",
			lang: "java",
			files: map[string]string{
				"pom.xml": "<project/>",
				"src/test/java/com/example/OrderTest.java":      javaSource("com.example", "OrderTest"),
				"src/test/java/com/example/CalculatorTest.java": javaSource("com.example", "CalculatorTest"),
			},
			paths: []string{
				"src/test/java/com/example/OrderTest.java",
				"src/test/java/com/example/CalculatorTest.java",
			},
			want: [][]string{{"mvn", "test", "-Dtest=CalculatorTest,OrderTest"}},
		},
		{
			// -pl scopes the filter to the module that holds the class. Without
			// it, every other module in the reactor fails with "No tests were
			// executed!" — and the usual cure, -DfailIfNoTests=false, is what
			// turns a wrong class name into a silent green.
			name: "multi-module reactor scopes to the owning module",
			lang: "java",
			files: map[string]string{
				"pom.xml":                 "<project><modules/></project>",
				"services/orders/pom.xml": "<project/>",
				"services/orders/src/test/java/com/example/orders/OrderTest.java":     javaSource("com.example.orders", "OrderTest"),
				"services/billing/pom.xml":                                            "<project/>",
				"services/billing/src/test/java/com/example/billing/InvoiceTest.java": javaSource("com.example.billing", "InvoiceTest"),
			},
			paths: []string{
				"services/orders/src/test/java/com/example/orders/OrderTest.java",
				"services/billing/src/test/java/com/example/billing/InvoiceTest.java",
			},
			want: [][]string{
				{"mvn", "test", "-pl", "services/billing", "-Dtest=InvoiceTest"},
				{"mvn", "test", "-pl", "services/orders", "-Dtest=OrderTest"},
			},
		},
		{
			name: "kotlin under maven",
			lang: "kotlin",
			files: map[string]string{
				"pom.xml": "<project/>",
				"src/test/kotlin/com/example/CacheTest.kt": kotlinSource("com.example", "CacheTest"),
			},
			paths: []string{"src/test/kotlin/com/example/CacheTest.kt"},
			want:  [][]string{{"mvn", "test", "-Dtest=CacheTest"}},
		},
		{
			// Kotlin and Scala do not require the file to be named after its
			// class. A file declaring exactly one type is taken at its word.
			name: "class not named after its file",
			lang: "kotlin",
			files: map[string]string{
				"pom.xml":                           "<project/>",
				"src/test/kotlin/CacheBehaviour.kt": kotlinSource("com.example", "CacheSpec"),
			},
			paths: []string{"src/test/kotlin/CacheBehaviour.kt"},
			want:  [][]string{{"mvn", "test", "-Dtest=CacheSpec"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			got := argvsOf(mustFormat(t, root, tt.lang, tt.paths))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FormatCommand(%s) = %v, want %v", tt.lang, got, tt.want)
			}
		})
	}
}

func TestFormatCommand_MavenWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the POSIX wrapper is mvnw; Windows uses mvnw.cmd")
	}
	root := writeTree(t, map[string]string{
		"pom.xml": "<project/>",
		"mvnw":    "#!/bin/sh\n",
		"src/test/java/com/example/CalculatorTest.java": javaSource("com.example", "CalculatorTest"),
	})
	got := argvsOf(mustFormat(t, root, "java", []string{"src/test/java/com/example/CalculatorTest.java"}))
	want := [][]string{{"./mvnw", "test", "-Dtest=CalculatorTest"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("with a checked-in wrapper = %v, want %v", got, want)
	}
}

func TestFormatCommand_Gradle(t *testing.T) {
	tests := []struct {
		name  string
		lang  string
		files map[string]string
		paths []string
		want  [][]string
	}{
		{
			// --tests takes a fully qualified name, so the package has to come
			// out of the file: the path says src/test/kotlin, which is not it.
			name: "root project uses the declared package",
			lang: "kotlin",
			files: map[string]string{
				"build.gradle":    "",
				"settings.gradle": "rootProject.name = 'app'",
				"src/test/kotlin/com/example/OrderTest.kt": kotlinSource("com.example", "OrderTest"),
			},
			paths: []string{"src/test/kotlin/com/example/OrderTest.kt"},
			want:  [][]string{{"gradle", "test", "--tests", "com.example.OrderTest"}},
		},
		{
			name: "default package is the bare class name",
			lang: "kotlin",
			files: map[string]string{
				"build.gradle.kts":             "",
				"src/test/kotlin/OrderTest.kt": kotlinSource("", "OrderTest"),
			},
			paths: []string{"src/test/kotlin/OrderTest.kt"},
			want:  [][]string{{"gradle", "test", "--tests", "OrderTest"}},
		},
		{
			name: "java under gradle",
			lang: "java",
			files: map[string]string{
				"build.gradle": "",
				"src/test/java/com/example/CalculatorTest.java": javaSource("com.example", "CalculatorTest"),
			},
			paths: []string{"src/test/java/com/example/CalculatorTest.java"},
			want:  [][]string{{"gradle", "test", "--tests", "com.example.CalculatorTest"}},
		},
		{
			// The root `test` task would run in every subproject, and Gradle
			// fails a subproject no --tests pattern matches.
			name: "subproject gets its own task path",
			lang: "kotlin",
			files: map[string]string{
				"settings.gradle":        "include 'libs:core'",
				"build.gradle":           "",
				"libs/core/build.gradle": "",
				"libs/core/src/test/kotlin/com/example/core/CacheTest.kt": kotlinSource("com.example.core", "CacheTest"),
			},
			paths: []string{"libs/core/src/test/kotlin/com/example/core/CacheTest.kt"},
			want:  [][]string{{"gradle", ":libs:core:test", "--tests", "com.example.core.CacheTest"}},
		},
		{
			name: "one command per project, patterns sorted",
			lang: "kotlin",
			files: map[string]string{
				"settings.gradle":      "include 'app', 'libs:core'",
				"app/build.gradle.kts": "",
				"app/src/test/kotlin/com/example/app/OrderTest.kt":        kotlinSource("com.example.app", "OrderTest"),
				"app/src/test/kotlin/com/example/app/CartTest.kt":         kotlinSource("com.example.app", "CartTest"),
				"libs/core/build.gradle.kts":                              "",
				"libs/core/src/test/kotlin/com/example/core/CacheTest.kt": kotlinSource("com.example.core", "CacheTest"),
			},
			paths: []string{
				"app/src/test/kotlin/com/example/app/OrderTest.kt",
				"libs/core/src/test/kotlin/com/example/core/CacheTest.kt",
				"app/src/test/kotlin/com/example/app/CartTest.kt",
			},
			want: [][]string{
				{"gradle", ":app:test", "--tests", "com.example.app.CartTest", "--tests", "com.example.app.OrderTest"},
				{"gradle", ":libs:core:test", "--tests", "com.example.core.CacheTest"},
			},
		},
		{
			// A subproject configured entirely from the root build script has no
			// build.gradle of its own, so there is no project path to derive:
			// the root task, which reaches it, is the honest answer.
			name: "subproject without a build script falls back to the root task",
			lang: "kotlin",
			files: map[string]string{
				"settings.gradle": "include 'app'",
				"build.gradle":    "subprojects { apply plugin: 'kotlin' }",
				"app/src/test/kotlin/com/example/OrderTest.kt": kotlinSource("com.example", "OrderTest"),
			},
			paths: []string{"app/src/test/kotlin/com/example/OrderTest.kt"},
			want:  [][]string{{"gradle", "test", "--tests", "com.example.OrderTest"}},
		},
		{
			name: "scala under gradle",
			lang: "scala",
			files: map[string]string{
				"build.gradle": "",
				"src/test/scala/com/example/OrderSpec.scala": scalaSource([]string{"com.example"}, "OrderSpec"),
			},
			paths: []string{"src/test/scala/com/example/OrderSpec.scala"},
			want:  [][]string{{"gradle", "test", "--tests", "com.example.OrderSpec"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			got := argvsOf(mustFormat(t, root, tt.lang, tt.paths))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FormatCommand(%s) = %v, want %v", tt.lang, got, tt.want)
			}
		})
	}
}

func TestFormatCommand_GradleWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the POSIX wrapper is gradlew; Windows uses gradlew.bat")
	}
	root := writeTree(t, map[string]string{
		"build.gradle": "",
		"gradlew":      "#!/bin/sh\n",
		"src/test/kotlin/com/example/OrderTest.kt": kotlinSource("com.example", "OrderTest"),
	})
	got := argvsOf(mustFormat(t, root, "kotlin", []string{"src/test/kotlin/com/example/OrderTest.kt"}))
	want := [][]string{{"./gradlew", "test", "--tests", "com.example.OrderTest"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("with a checked-in wrapper = %v, want %v", got, want)
	}
}

func TestFormatCommand_SBT(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		paths []string
		want  [][]string
	}{
		{
			name: "testOnly names the suite",
			files: map[string]string{
				"build.sbt": `name := "app"`,
				"src/test/scala/com/example/OrderSpec.scala": scalaSource([]string{"com.example"}, "OrderSpec"),
			},
			paths: []string{"src/test/scala/com/example/OrderSpec.scala"},
			want:  [][]string{{"sbt", "testOnly com.example.OrderSpec"}},
		},
		{
			// Scala stacks package clauses: this file's suite is
			// com.example.orders.OrderSpec, not com.example.OrderSpec.
			name: "stacked package clauses join",
			files: map[string]string{
				"build.sbt": `name := "app"`,
				"src/test/scala/com/example/orders/OrderSpec.scala": scalaSource([]string{"com.example", "orders"}, "OrderSpec"),
			},
			paths: []string{"src/test/scala/com/example/orders/OrderSpec.scala"},
			want:  [][]string{{"sbt", "testOnly com.example.orders.OrderSpec"}},
		},
		{
			name: "one invocation, names sorted",
			files: map[string]string{
				"build.sbt": `name := "app"`,
				"src/test/scala/com/example/OrderSpec.scala": scalaSource([]string{"com.example"}, "OrderSpec"),
				"src/test/scala/com/example/CartSpec.scala":  scalaSource([]string{"com.example"}, "CartSpec"),
			},
			paths: []string{
				"src/test/scala/com/example/OrderSpec.scala",
				"src/test/scala/com/example/CartSpec.scala",
			},
			want: [][]string{{"sbt", "testOnly com.example.CartSpec com.example.OrderSpec"}},
		},
		{
			name: "default package",
			files: map[string]string{
				"build.sbt":                      `name := "app"`,
				"src/test/scala/OrderSpec.scala": scalaSource(nil, "OrderSpec"),
			},
			paths: []string{"src/test/scala/OrderSpec.scala"},
			want:  [][]string{{"sbt", "testOnly OrderSpec"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			got := argvsOf(mustFormat(t, root, "scala", tt.paths))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FormatCommand(scala) = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFormatCommand_SBTMultiProjectIsRefused is the regression for the sbt
// false green.
//
// `sbt 'testOnly com.example.CoreSpec'` typed at the root of a build whose root
// project does not aggregate `core` prints
//
//	[info] Passed: Total 0, Failed 0, Errors 0, Passed 0
//	[info] No tests to run for Test / testOnly
//	[success] Total time: 0 s
//
// and exits 0 — with a genuinely failing suite sitting in core. Scoping it
// (`sbt 'core/testOnly com.example.CoreSpec'`) exits 1 as it should, but the
// project id is a val name in build.sbt, not a directory, so witness cannot
// derive it. Widening to `sbt test` has the identical hole. Refusing is the
// only answer that is not a guess.
func TestFormatCommand_SBTMultiProjectIsRefused(t *testing.T) {
	// The idiom that breaks it: an explicit root, declared to carry a name or
	// `publish / skip := true`, which aggregates nothing unless it says so.
	explicitRoot := "lazy val root = (project in file(\".\")).settings(name := \"demo\")\n" +
		"lazy val core = (project in file(\"core\")).settings(name := \"core\")\n"

	tests := []struct {
		name  string
		files map[string]string
		path  string
	}{
		{
			name: "explicit root beside a subproject",
			files: map[string]string{
				"build.sbt": explicitRoot,
				"core/src/test/scala/com/example/CoreSpec.scala": scalaSource([]string{"com.example"}, "CoreSpec"),
			},
			path: "core/src/test/scala/com/example/CoreSpec.scala",
		},
		{
			// The old Project(id, base) spelling names the same shape.
			name: "Project(id, file(...)) spelling",
			files: map[string]string{
				"build.sbt": "lazy val root = Project(\"root\", file(\".\"))\n" +
					"lazy val core = Project(\"core\", file(\"core\"))\n",
				"core/src/test/scala/com/example/CoreSpec.scala": scalaSource([]string{"com.example"}, "CoreSpec"),
			},
			path: "core/src/test/scala/com/example/CoreSpec.scala",
		},
		{
			// An explicit root that DOES aggregate is refused too: witness
			// cannot check that the list covers the project the test is in, and
			// guessing that it does is how the false green happened.
			name: "explicit root that aggregates",
			files: map[string]string{
				"build.sbt": "lazy val root = (project in file(\".\")).aggregate(core)\n" +
					"lazy val core = project.in(file(\"core\"))\n",
				"core/src/test/scala/com/example/CoreSpec.scala": scalaSource([]string{"com.example"}, "CoreSpec"),
			},
			path: "core/src/test/scala/com/example/CoreSpec.scala",
		},
		{
			// A subproject keeping its own settings file is evidence enough on
			// its own: the root build.sbt may define nothing witness can read.
			name: "subproject with its own build.sbt",
			files: map[string]string{
				"build.sbt":      "ThisBuild / scalaVersion := \"2.13.14\"\n",
				"core/build.sbt": "name := \"core\"\n",
				"core/src/test/scala/com/example/CoreSpec.scala": scalaSource([]string{"com.example"}, "CoreSpec"),
			},
			path: "core/src/test/scala/com/example/CoreSpec.scala",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			err := mustNoRunner(t, root, "scala", []string{tt.path})
			if !strings.Contains(err.Reason, "more than one sbt project") {
				t.Errorf("Reason = %q, want it to name sbt project scoping", err.Reason)
			}
		})
	}
}

// A build.sbt that only carries settings, or declares nothing but the root
// project, is the single-project shape a root-level testOnly does reach.
// Refusing it would cost every ordinary sbt repo its command for nothing.
func TestFormatCommand_SBTSingleProjectStillRuns(t *testing.T) {
	tests := []struct {
		name     string
		buildSBT string
	}{
		{"settings only", "name := \"app\"\nscalaVersion := \"2.13.14\"\n"},
		{"an explicit root and nothing else", "lazy val root = (project in file(\".\")).settings(name := \"app\")\n"},
		{
			// A commented-out subproject is not a subproject.
			name:     "a subproject in a comment",
			buildSBT: "name := \"app\"\n// lazy val core = (project in file(\"core\"))\n",
		},
		{
			// No project is based at ".", so sbt generates a root — and a
			// GENERATED root aggregates every project in the build, which is
			// exactly what makes an unscoped testOnly reach them. Refusing this
			// would cost every ordinary multi-module sbt repo its command.
			name: "subprojects under a root sbt generates",
			buildSBT: "lazy val core = (project in file(\"core\"))\n" +
				"lazy val api = (project in file(\"api\"))\n",
		},
		{
			// sbt's shorthand: the base directory is the val's own name, so
			// this is a subproject and the root is still generated.
			name:     "the bare project shorthand",
			buildSBT: "lazy val core = project\nlazy val api = project\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, map[string]string{
				"build.sbt": tt.buildSBT,
				"src/test/scala/com/example/OrderSpec.scala": scalaSource([]string{"com.example"}, "OrderSpec"),
			})
			got := argvsOf(mustFormat(t, root, "scala", []string{"src/test/scala/com/example/OrderSpec.scala"}))
			want := [][]string{{"sbt", "testOnly com.example.OrderSpec"}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("FormatCommand(scala) = %v, want %v", got, want)
			}
		})
	}
}

// TestFormatCommand_JVMEverySuiteInTheFileIsNamed is the regression for the
// second-class-in-the-file false green.
//
// witness selects FILES. A file holding `class CalculatorTest` beside
// `class RefundsTest` — legal Java, idiomatic Scala — was turned into
// `-Dtest=CalculatorTest`, so surefire ran one of the two, reported BUILD
// SUCCESS, and the failing assertion in RefundsTest never executed. Every
// top-level type in the file has to be in the filter.
func TestFormatCommand_JVMEverySuiteInTheFileIsNamed(t *testing.T) {
	twoJavaClasses := "package com.example;\n\n" +
		"import org.junit.jupiter.api.Test;\n\n" +
		"class CalculatorTest {\n    @Test\n    void passes() {}\n}\n\n" +
		"class RefundsTest {\n    @Test\n    void fails() {}\n}\n"
	twoScalaSuites := "package com.example\n\n" +
		"import org.scalatest.funsuite.AnyFunSuite\n\n" +
		"class OrdersSpec extends AnyFunSuite {\n  test(\"passes\") { assert(true) }\n}\n\n" +
		"class RefundsSpec extends AnyFunSuite {\n  test(\"fails\") { assert(false) }\n}\n"

	tests := []struct {
		name  string
		lang  string
		files map[string]string
		path  string
		want  [][]string
	}{
		{
			name: "maven names both classes",
			lang: "java",
			files: map[string]string{
				"pom.xml": "<project/>",
				"src/test/java/com/example/CalculatorTest.java": twoJavaClasses,
			},
			path: "src/test/java/com/example/CalculatorTest.java",
			want: [][]string{{"mvn", "test", "-Dtest=CalculatorTest,RefundsTest"}},
		},
		{
			name: "gradle repeats --tests for both",
			lang: "java",
			files: map[string]string{
				"build.gradle": "",
				"src/test/java/com/example/CalculatorTest.java": twoJavaClasses,
			},
			path: "src/test/java/com/example/CalculatorTest.java",
			want: [][]string{{"gradle", "test", "--tests", "com.example.CalculatorTest", "--tests", "com.example.RefundsTest"}},
		},
		{
			name: "sbt names both suites",
			lang: "scala",
			files: map[string]string{
				"build.sbt": `name := "app"`,
				"src/test/scala/com/example/OrdersSpec.scala": twoScalaSuites,
			},
			path: "src/test/scala/com/example/OrdersSpec.scala",
			want: [][]string{{"sbt", "testOnly com.example.OrdersSpec com.example.RefundsSpec"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			got := argvsOf(mustFormat(t, root, tt.lang, []string{tt.path}))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FormatCommand(%s) = %v, want %v\nthe sibling suite in the same file must be in the filter", tt.lang, got, tt.want)
			}
		})
	}
}

// TestFormatCommand_SBTNestedSuiteIsRefused is the regression for the nested
// FQCN false green, and it needs no unusual build layout at all: a plain
// single-project sbt build.
//
// A suite declared inside an object is com.example.Fixtures$NestedSpec at
// runtime. The dotted com.example.NestedSpec witness used to emit compiles,
// looks right, matches nothing, and sbt calls that a success.
func TestFormatCommand_SBTNestedSuiteIsRefused(t *testing.T) {
	root := writeTree(t, map[string]string{
		"build.sbt": `name := "app"`,
		"src/test/scala/com/example/NestedSpec.scala": "package com.example\n\n" +
			"import org.scalatest.funsuite.AnyFunSuite\n\n" +
			"object Fixtures {\n" +
			"  class NestedSpec extends AnyFunSuite {\n" +
			"    test(\"this fails\") { assert(1 + 1 == 3) }\n" +
			"  }\n" +
			"}\n",
	})
	err := mustNoRunner(t, root, "scala", []string{"src/test/scala/com/example/NestedSpec.scala"})
	if !strings.Contains(err.Reason, "$ separator") {
		t.Errorf("Reason = %q, want it to say the nested type's runtime name cannot be derived", err.Reason)
	}
}

// A JUnit 5 @Nested class is not the nested case above: it runs as part of the
// outer class the file is named after, which is the one in the filter. Refusing
// it would take Maven and Gradle away from a mainstream JUnit idiom.
func TestFormatCommand_MavenNestedJUnitClassRunsWithItsOuter(t *testing.T) {
	root := writeTree(t, map[string]string{
		"pom.xml": "<project/>",
		"src/test/java/com/example/CalculatorTest.java": "package com.example;\n\n" +
			"import org.junit.jupiter.api.Nested;\nimport org.junit.jupiter.api.Test;\n\n" +
			"class CalculatorTest {\n" +
			"    @Nested\n" +
			"    class Addition {\n        @Test\n        void works() {}\n    }\n" +
			"}\n",
	})
	got := argvsOf(mustFormat(t, root, "java", []string{"src/test/java/com/example/CalculatorTest.java"}))
	want := [][]string{{"mvn", "test", "-Dtest=CalculatorTest"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FormatCommand(java) = %v, want %v", got, want)
	}
}

// TestFormatCommand_SBTArgvIsOneShellWord pins the shape of the sbt argv: sbt
// reads ONE argument as a command, so "testOnly" and the class must not be
// separate argv entries, and the printed form has to be pasteable.
func TestFormatCommand_SBTArgvIsOneShellWord(t *testing.T) {
	root := writeTree(t, map[string]string{
		"build.sbt": `name := "app"`,
		"src/test/scala/com/example/OrderSpec.scala": scalaSource([]string{"com.example"}, "OrderSpec"),
	})
	cmds := mustFormat(t, root, "scala", []string{"src/test/scala/com/example/OrderSpec.scala"})
	if len(cmds) != 1 || len(cmds[0].Argv) != 2 {
		t.Fatalf("argv = %v, want [sbt <one command word>]", argvsOf(cmds))
	}
	if got, want := cmds[0].String(), "sbt 'testOnly com.example.OrderSpec'"; got != want {
		t.Errorf("printed command = %q, want %q", got, want)
	}
}

func TestFormatCommand_JVMRefusals(t *testing.T) {
	tests := []struct {
		name       string
		lang       string
		files      map[string]string
		path       string
		wantReason string
	}{
		{
			name:       "no build file at the root",
			lang:       "java",
			files:      map[string]string{"src/test/java/com/example/OrderTest.java": javaSource("com.example", "OrderTest")},
			path:       "src/test/java/com/example/OrderTest.java",
			wantReason: "pom.xml or build.gradle",
		},
		{
			name:       "no build file names build.sbt for scala",
			lang:       "scala",
			files:      map[string]string{"src/test/scala/OrderSpec.scala": scalaSource(nil, "OrderSpec")},
			path:       "src/test/scala/OrderSpec.scala",
			wantReason: "build.sbt",
		},
		{
			// recon can select a path that is no longer in the working tree.
			// There is nothing to read a class out of, so there is no command.
			name:       "test file missing from the tree",
			lang:       "java",
			files:      map[string]string{"pom.xml": "<project/>"},
			path:       "src/test/java/com/example/GoneTest.java",
			wantReason: "could not be read",
		},
		{
			name:       "path outside the repository",
			lang:       "java",
			files:      map[string]string{"pom.xml": "<project/>"},
			path:       "../elsewhere/OrderTest.java",
			wantReason: "could not be read",
		},
		{
			// Sibling brace blocks in one file: which package holds the suite is
			// not knowable without parsing Scala for real, and a wrong FQCN in
			// `sbt testOnly` is reported as success.
			name: "brace-delimited package block",
			lang: "scala",
			files: map[string]string{
				"build.sbt": `name := "app"`,
				"src/test/scala/OrderSpec.scala": "package com.example {\n" +
					"  class OrderSpec extends AnyFunSuite {}\n" +
					"}\n",
			},
			path:       "src/test/scala/OrderSpec.scala",
			wantReason: "brace-delimited",
		},
		{
			name: "file declares no type to filter on",
			lang: "kotlin",
			files: map[string]string{
				"build.gradle": "",
				"src/test/kotlin/com/example/Fixtures.kt": "package com.example\n\nval sample = listOf(1, 2, 3)\n",
			},
			path:       "src/test/kotlin/com/example/Fixtures.kt",
			wantReason: "declares no class",
		},
		{
			name: "several types and none named after the file",
			lang: "kotlin",
			files: map[string]string{
				"build.gradle": "",
				"src/test/kotlin/com/example/Suites.kt": "package com.example\n\n" +
					"class OrderTest {\n}\n\nclass CartTest {\n}\n",
			},
			path:       "src/test/kotlin/com/example/Suites.kt",
			wantReason: "several types",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			err := mustNoRunner(t, root, tt.lang, []string{tt.path})
			if err.Lang != tt.lang {
				t.Errorf("Lang = %q, want %q", err.Lang, tt.lang)
			}
			if !strings.Contains(err.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, want it to mention %q", err.Reason, tt.wantReason)
			}
		})
	}
}

// TestFormatCommand_JVMPartialSuccess is the fail-closed contract for a
// half-derivable selection: the tests witness CAN name come back as commands,
// the ones it cannot come back as an error, and a caller that runs the commands
// while dropping the error is the only way to get a false pass.
func TestFormatCommand_JVMPartialSuccess(t *testing.T) {
	root := writeTree(t, map[string]string{
		"pom.xml": "<project/>",
		"src/test/java/com/example/CalculatorTest.java": javaSource("com.example", "CalculatorTest"),
	})
	paths := []string{
		"src/test/java/com/example/CalculatorTest.java",
		"src/test/java/com/example/DeletedTest.java",
	}

	cmds, err := FormatCommand(root, "java", paths)
	if !errors.Is(err, ErrNoRunner) {
		t.Fatalf("err = %v, want ErrNoRunner for the path that could not be read", err)
	}
	want := [][]string{{"mvn", "test", "-Dtest=CalculatorTest"}}
	if !reflect.DeepEqual(argvsOf(cmds), want) {
		t.Errorf("commands = %v, want %v", argvsOf(cmds), want)
	}
	if !strings.Contains(err.Error(), "DeletedTest.java") {
		t.Errorf("error %q must name the test that will not run", err)
	}
	if strings.Contains(err.Error(), "CalculatorTest.java") {
		t.Errorf("error %q names a test that IS being run", err)
	}
}

// TestFormatCommand_JVMNeverPassesAPath is the java arm's version of the cargo
// rule: these tools read a positional argument as a goal, a task or a name, so
// a source path in the argv is always a bug.
func TestFormatCommand_JVMNeverPassesAPath(t *testing.T) {
	root := writeTree(t, map[string]string{
		"pom.xml": "<project/>",
		"src/test/java/com/example/CalculatorTest.java": javaSource("com.example", "CalculatorTest"),
		"src/test/kotlin/com/example/CacheTest.kt":      kotlinSource("com.example", "CacheTest"),
	})
	cmds, err := FormatCommand(root, "java", []string{
		"src/test/java/com/example/CalculatorTest.java",
		"src/test/kotlin/com/example/CacheTest.kt",
	})
	if err != nil {
		t.Fatalf("FormatCommand: %v", err)
	}
	for _, c := range cmds {
		for _, arg := range c.Argv {
			for _, ext := range []string{".java", ".kt", ".scala"} {
				if strings.HasSuffix(arg, ext) {
					t.Errorf("command %q passes the source path %q; no JVM build tool reads that as a test selector", c, arg)
				}
			}
		}
	}
}
