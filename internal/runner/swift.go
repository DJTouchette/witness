package runner

// swiftNoPackageReason is why a Swift repository with no SwiftPM manifest gets
// no command, from either the selected-test arm or the full-suite fallback.
const swiftNoPackageReason = "no Package.swift at the repository root; witness cannot derive the xcodebuild scheme and destination an Xcode project needs"

// swiftCommands builds `swift test --filter <TestName>`, one command per
// selected test type.
//
// SwiftPM has no way to run a test FILE: --filter takes a regular expression
// matched against `<Suite>/<testMethod>` names, so the type has to be read out
// of the file. It is read rather than taken from the base name because Swift
// does not require them to match, and a filter matching nothing is not reliably
// an error — XCTest has reported "no matching tests" as a successful run.
//
// One command per name instead of a repeated --filter: the commands then need no
// assumption about the option being repeatable, and a red suite cannot hide the
// ones after it (ExecuteAll runs them all regardless).
//
// Every top-level type in a selected file is named, not only the one the file is
// named after — see filterTypes. A file's second XCTestCase is a test the
// selection promised to run, and leaving it out is silent. The cost is that a
// plain helper type sitting beside a suite gets a command of its own that
// matches no test; newer SwiftPM calls that an error, which is a loud, wrong-way
// failure rather than a quiet pass.
//
// Without a Package.swift at the root there is nothing to derive: an Xcode
// project needs `xcodebuild -scheme <scheme> -destination <platform>`, none of
// which is knowable from a test path, so those repos get a *NoRunnerError.
func swiftCommands(root string, paths []string) ([]Command, error) {
	if !hasFile(root, "Package.swift") {
		return nil, &NoRunnerError{Lang: "swift", Paths: paths, Reason: swiftNoPackageReason}
	}

	var reasons noRunnerReasons
	names := make(map[string]bool, len(paths))
	for _, p := range paths {
		src, err := readRepoFile(root, p)
		if err != nil {
			reasons.add("the test file could not be read from the repository root", p)
			continue
		}
		// Every suite in the file, not just the one it is named after: Swift
		// puts several XCTestCase subclasses in one file as readily as one, and
		// a suite the filter never names does not run.
		suites, err := filterTypes(baseName(p), declaredTypes(stripComments(src, true)))
		if err != nil {
			reasons.add(err.Error(), p)
			continue
		}
		for _, name := range suites {
			names[name] = true
		}
	}

	commands := make([]Command, 0, len(names))
	for _, name := range sortedKeys(names) {
		commands = append(commands, Command{Lang: "swift", Argv: []string{"swift", "test", "--filter", name}})
	}
	return commands, reasons.err("swift")
}
