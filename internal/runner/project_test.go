package runner

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeTree materialises a scratch repository under t.TempDir() from a map of
// repo-relative paths to contents, and returns its root. Every test that
// exercises a build-file-driven arm builds a real tree: the arms stat and read
// the filesystem, so a fake would test nothing.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// mustNoRunner asserts that FormatCommand refused the whole selection, and
// returns the error so the caller can check what it says.
func mustNoRunner(t *testing.T, root, framework string, paths []string) *NoRunnerError {
	t.Helper()
	cmds, err := FormatCommand(root, framework, paths)
	if err == nil {
		t.Fatalf("FormatCommand(%q, %v) = %v, want ErrNoRunner", framework, paths, argvsOf(cmds))
	}
	if !errors.Is(err, ErrNoRunner) {
		t.Fatalf("error = %v, want it to wrap ErrNoRunner", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("commands = %v, want none", argvsOf(cmds))
	}
	var nre *NoRunnerError
	if !errors.As(err, &nre) {
		t.Fatalf("error = %v, want a *NoRunnerError", err)
	}
	// A refusal a human cannot act on is barely better than silence.
	if nre.Reason == "" {
		t.Errorf("*NoRunnerError for %s has no Reason: %v", framework, err)
	}
	for _, p := range paths {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("error %q does not name the unrunnable test %q", err, p)
		}
	}
	return nre
}

func TestJVMBuildSystem_Precedence(t *testing.T) {
	tests := []struct {
		name  string
		lang  string
		files map[string]string
		want  buildSystem
	}{
		{
			name:  "maven",
			lang:  "java",
			files: map[string]string{"pom.xml": "<project/>"},
			want:  buildMaven,
		},
		{
			name:  "gradle groovy",
			lang:  "kotlin",
			files: map[string]string{"build.gradle": ""},
			want:  buildGradle,
		},
		{
			name:  "gradle kotlin dsl",
			lang:  "kotlin",
			files: map[string]string{"build.gradle.kts": ""},
			want:  buildGradle,
		},
		{
			name:  "settings.gradle alone still marks a gradle build",
			lang:  "java",
			files: map[string]string{"settings.gradle": "include 'app'"},
			want:  buildGradle,
		},
		{
			// Documented precedence: Maven's -Dtest=<Class> needs only the class
			// name, Gradle's --tests needs the package too, so the tool witness
			// can derive with fewest assumptions wins a tie.
			name:  "maven beats gradle when both are present",
			lang:  "java",
			files: map[string]string{"pom.xml": "<project/>", "build.gradle": ""},
			want:  buildMaven,
		},
		{
			name:  "sbt wins for scala",
			lang:  "scala",
			files: map[string]string{"build.sbt": "", "pom.xml": "<project/>"},
			want:  buildSBT,
		},
		{
			// build.sbt is not a Java or Kotlin build witness knows how to
			// select in; Maven still decides.
			name:  "sbt does not claim java",
			lang:  "java",
			files: map[string]string{"build.sbt": "", "pom.xml": "<project/>"},
			want:  buildMaven,
		},
		{
			name:  "scala falls through sbt to maven",
			lang:  "scala",
			files: map[string]string{"pom.xml": "<project/>"},
			want:  buildMaven,
		},
		{
			name:  "no build file",
			lang:  "java",
			files: map[string]string{"src/test/java/A.java": ""},
			want:  buildNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jvmBuildSystem(writeTree(t, tt.files), tt.lang); got != tt.want {
				t.Errorf("jvmBuildSystem(%s) = %q, want %q", tt.lang, got, tt.want)
			}
		})
	}
}

func TestJVMBuildSystem_NoRoot(t *testing.T) {
	// An embedder that gave witness no root has no build files to detect: the
	// arm must refuse, not assume the process's working directory is the repo.
	if got := jvmBuildSystem("", "java"); got != buildNone {
		t.Errorf("jvmBuildSystem with no root = %q, want none", got)
	}
}

func TestNearestManifest(t *testing.T) {
	root := writeTree(t, map[string]string{
		"pom.xml":                 "<project/>",
		"services/orders/pom.xml": "<project/>",
		"services/orders/src/test/java/OrderTest.java": "",
		"services/legacy/src/test/java/OldTest.java":   "",
		"RootTest.java": "",
	})

	tests := []struct {
		path string
		want string
		ok   bool
	}{
		{"services/orders/src/test/java/OrderTest.java", "services/orders", true},
		// No pom under services/legacy: the walk carries on up to the root.
		{"services/legacy/src/test/java/OldTest.java", "", true},
		{"RootTest.java", "", true},
		// Outside the repository: never resolved, never read.
		{"../elsewhere/OrderTest.java", "", false},
		{"/etc/passwd", "", false},
	}

	for _, tt := range tests {
		got, ok := nearestManifest(root, tt.path, "pom.xml")
		if got != tt.want || ok != tt.ok {
			t.Errorf("nearestManifest(%q) = (%q, %v), want (%q, %v)", tt.path, got, ok, tt.want, tt.ok)
		}
	}

	if _, ok := nearestManifest("", "a/b/OrderTest.java", "pom.xml"); ok {
		t.Error("nearestManifest with no root resolved a manifest")
	}
}

func TestRepoFileRefusesEscapes(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"../secrets.java", "../../etc/passwd", "/etc/passwd", ""} {
		if _, ok := repoFile(root, rel); ok {
			t.Errorf("repoFile(%q) resolved; a selection path must not escape the repository", rel)
		}
	}
	if _, ok := repoFile("", "a.java"); ok {
		t.Error("repoFile with no root resolved")
	}
}

func TestBuildToolPrefersWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the wrapper on Windows is gradlew.bat; this asserts the POSIX script")
	}
	withWrapper := writeTree(t, map[string]string{"gradlew": "#!/bin/sh\n", "build.gradle": ""})
	if got := buildTool(withWrapper, "gradlew", "gradle"); got != "./gradlew" {
		t.Errorf("buildTool with a wrapper = %q, want ./gradlew", got)
	}
	without := writeTree(t, map[string]string{"build.gradle": ""})
	if got := buildTool(without, "gradlew", "gradle"); got != "gradle" {
		t.Errorf("buildTool without a wrapper = %q, want gradle", got)
	}
}
