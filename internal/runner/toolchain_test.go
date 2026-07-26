package runner

import (
	"os/exec"
	"testing"
)

// TestToolchainVerification reports which of the real build tools are available
// to confirm the invocations this package derives.
//
// Only cargo is exercised end to end (TestCargoRunsTheDerivedInvocation): it is
// the one toolchain that can build and run a scratch project with no network.
// Every other arm's invocation is pinned by exact argv against a real scratch
// tree, but running it needs a resolvable dependency — mvn fetches surefire and
// JUnit, gradle downloads its own distribution, sbt fetches scala-library, dart
// needs `pub get` for package:test, phpunit needs a composer install — so a
// machine with the tool but no network would fail here for reasons that have
// nothing to do with the argv. Those are skipped with the reason, and the argv
// they would run is the one the tests above assert.
func TestToolchainVerification(t *testing.T) {
	tools := []struct {
		binary string
		needs  string
	}{
		{"mvn", "a populated ~/.m2 (surefire and JUnit) to run a scratch project"},
		{"gradle", "a downloaded Gradle distribution and test framework to run a scratch project"},
		{"sbt", "a resolvable scala-library and ScalaTest to run a scratch project"},
		{"swift", "a scratch SwiftPM package built here (not automated)"},
		{"dart", "`dart pub get` for package:test to run a scratch project"},
		{"phpunit", "a composer install to run a scratch project"},
	}

	for _, tool := range tools {
		t.Run(tool.binary, func(t *testing.T) {
			if _, err := exec.LookPath(tool.binary); err != nil {
				t.Skipf("%s is not installed: its invocation is pinned by exact argv above, but not run here", tool.binary)
			}
			t.Skipf("%s is installed but not exercised: verification needs %s", tool.binary, tool.needs)
		})
	}
}
