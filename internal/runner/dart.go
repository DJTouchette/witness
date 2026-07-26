package runner

import "strings"

// dartNoPubspecReason is why a Dart repository with no pubspec gets no command,
// from either the selected-test arm or the full-suite fallback.
const dartNoPubspecReason = "no pubspec.yaml at the repository root, so witness cannot tell whether these tests run under `dart test` or `flutter test`"

// dartCommands builds `dart test <path>...` — or `flutter test <path>...` for a
// Flutter package.
//
// The dart test runner takes file paths, so nothing has to be read out of the
// test files. The one thing that must be derived is which of the two front ends
// owns the package: `dart test` in a Flutter package fails ("Flutter users
// should run flutter test"), and the reverse fails just as loudly, so a wrong
// answer here cannot pass silently.
func dartCommands(root string, paths []string) ([]Command, error) {
	pubspec, err := readRepoFile(root, "pubspec.yaml")
	if err != nil {
		return nil, &NoRunnerError{Lang: "dart", Paths: paths, Reason: dartNoPubspecReason}
	}
	return []Command{{Lang: "dart", Argv: append([]string{dartTestTool(pubspec), "test"}, paths...)}}, nil
}

// dartTestTool reports whether a pubspec describes a Flutter package.
//
// It looks for the two markers a Flutter package always carries — a top-level
// `flutter:` section, and the `sdk: flutter` a dependency on flutter or
// flutter_test expands to — rather than parsing YAML, which would mean a
// dependency for two string comparisons.
func dartTestTool(pubspec []byte) string {
	for _, line := range strings.Split(string(pubspec), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "flutter:" || strings.TrimSpace(line) == "sdk: flutter" {
			return "flutter"
		}
	}
	return "dart"
}
