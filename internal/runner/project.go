package runner

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// A test path on its own does not say how to run it. `mvn test -Dtest=<Class>`,
// `gradle test --tests <FQCN>` and `sbt 'testOnly <FQCN>'` all need something
// only the repository can answer: which build tool owns the tree, and what
// package the test file declares. This file reads that off disk.
//
// Everything here reports "cannot tell" rather than filling a gap with a plausible
// guess. An invented invocation is how `cargo test <path>` — a NAME filter that
// matches no test — came to run nothing and exit 0. A path witness cannot resolve
// stays a *NoRunnerError, which is loud; a path it resolves wrongly would be a
// green build that tested nothing.

// maxSourceBytes caps how much of a test file witness reads when it needs the
// package or class declared in it. Both live in the first few lines; the cap
// only stops a generated multi-megabyte file from being slurped whole.
const maxSourceBytes = 1 << 20

// repoFile joins a repo-relative path onto the repository root, refusing one
// that is absolute or climbs out of the tree.
//
// An empty root means witness was given no filesystem context (an embedder that
// never set one). That is not an error — it just means nothing on disk can be
// consulted, so every derivation below reports failure and the caller falls back
// to a *NoRunnerError.
func repoFile(root, rel string) (string, bool) {
	if root == "" || rel == "" {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(root, clean), true
}

// hasFile reports whether a repo-relative path is a regular file in the repo.
func hasFile(root, rel string) bool {
	full, ok := repoFile(root, rel)
	if !ok {
		return false
	}
	info, err := os.Stat(full)
	return err == nil && info.Mode().IsRegular()
}

// readRepoFile reads a repo-relative file, truncated at maxSourceBytes.
func readRepoFile(root, rel string) ([]byte, error) {
	full, ok := repoFile(root, rel)
	if !ok {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxSourceBytes))
}

// nearestManifest walks up from the directory holding a repo-relative test path
// looking for one of the named manifest files, and returns the repo-relative
// directory that holds it ("" for the repository root).
//
// The walk stops AT the root: a build file above it is not witness's to reason
// about, since the command runs in the root and a module path outside it cannot
// be named.
func nearestManifest(root, testPath string, names ...string) (string, bool) {
	if _, ok := repoFile(root, testPath); !ok {
		return "", false
	}
	dir := path.Dir(path.Clean(filepath.ToSlash(testPath)))
	for {
		rel := dir
		if rel == "." {
			rel = ""
		}
		for _, name := range names {
			if hasFile(root, path.Join(rel, name)) {
				return rel, true
			}
		}
		if rel == "" {
			return "", false
		}
		dir = path.Dir(dir)
	}
}

// buildSystem is the tool that runs a JVM project's tests.
type buildSystem string

const (
	buildNone   buildSystem = ""
	buildMaven  buildSystem = "maven"
	buildGradle buildSystem = "gradle"
	buildSBT    buildSystem = "sbt"
)

// gradleBuildFiles are the root markers of a Gradle build. settings.gradle
// counts because a multi-project build can keep every subproject's script in
// the subprojects and carry only settings at the root.
var gradleBuildFiles = []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}

// jvmBuildSystem picks the tool that runs a JVM project's tests, from the build
// files present at the repository ROOT — the directory witness runs the command
// in, not the one the test file happens to sit under.
//
// Repos carry more than one of these often enough (a Gradle migration that kept
// its pom.xml, a Scala service with a Maven parent) that the precedence has to
// be fixed and documented rather than left to whichever stat won:
//
//	scala:        build.sbt, then pom.xml, then build.gradle[.kts]
//	java, kotlin: pom.xml, then build.gradle[.kts]
//
// sbt comes first for Scala because a build.sbt is only ever there to be the
// build. Maven comes before Gradle because its selector is the one witness can
// derive with the fewest assumptions: `-Dtest=<ClassName>` needs only the class,
// while `--tests <FQCN>` needs the package read out of the file as well.
//
// Picking the tool a repo no longer uses is loud either way — mvn without a pom
// and gradle without a build script both exit non-zero having run nothing — so
// the wrong branch here cannot turn into a false green.
func jvmBuildSystem(root, lang string) buildSystem {
	if lang == "scala" && hasFile(root, "build.sbt") {
		return buildSBT
	}
	if hasFile(root, "pom.xml") {
		return buildMaven
	}
	for _, name := range gradleBuildFiles {
		if hasFile(root, name) {
			return buildGradle
		}
	}
	return buildNone
}

// buildTool prefers a wrapper script checked into the repository over the bare
// binary: `./gradlew` and `./mvnw` are how such a project is meant to be built,
// and are frequently the only build tool a CI image has.
//
// The command runs with the repository root as its working directory, so the
// relative "./gradlew" resolves there.
func buildTool(root, wrapper, binary string) string {
	if runtime.GOOS == "windows" {
		for _, ext := range []string{".bat", ".cmd"} {
			if hasFile(root, wrapper+ext) {
				return wrapper + ext
			}
		}
		return binary
	}
	if hasFile(root, wrapper) {
		return "./" + wrapper
	}
	return binary
}

// noRunnerReasons collects the paths witness could not derive a command for,
// bucketed by why, so a partly-derivable selection reports one precise error
// per cause instead of one vague error for the language.
type noRunnerReasons struct {
	byReason map[string][]string
}

// add records that path could not be turned into a command, because of reason.
func (n *noRunnerReasons) add(reason, path string) {
	if n.byReason == nil {
		n.byReason = make(map[string][]string)
	}
	n.byReason[reason] = append(n.byReason[reason], path)
}

// err returns the *NoRunnerError(s) for everything recorded, or nil if the whole
// selection was derivable. Several causes are joined; errors.Is/As still find
// ErrNoRunner and the first *NoRunnerError through the join.
func (n *noRunnerReasons) err(lang string) error {
	if len(n.byReason) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(n.byReason))
	for reason := range n.byReason {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)

	errs := make([]error, 0, len(reasons))
	for _, reason := range reasons {
		errs = append(errs, &NoRunnerError{Lang: lang, Paths: n.byReason[reason], Reason: reason})
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return errors.Join(errs...)
}
