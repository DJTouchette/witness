package runner

import "encoding/json"

// phpCommands builds `vendor/bin/phpunit <path>...`.
//
// PHPUnit and Pest both take test file paths, so unlike the JVM arms nothing has
// to be read out of the test files themselves. What does have to be derived is
// WHICH runner: a repo using Pest has phpunit in its vendor directory too, and
// `vendor/bin/phpunit` there runs the PHPUnit tests while silently ignoring
// every Pest test in the same file. So the binary comes from what composer.json
// declares, and a project declaring neither is refused rather than handed to a
// phpunit that may not exist.
func phpCommands(root string, paths []string) ([]Command, error) {
	binary, reason := phpTestBinary(root)
	if reason != "" {
		return nil, &NoRunnerError{Lang: "php", Paths: paths, Reason: reason}
	}
	return []Command{{Lang: "php", Argv: append([]string{binary}, paths...)}}, nil
}

// phpTestBinary reads composer.json for the test runner the project depends on,
// returning the vendored binary to invoke or the reason it could not be
// determined.
func phpTestBinary(root string) (binary, reason string) {
	data, err := readRepoFile(root, "composer.json")
	if err != nil {
		return "", "no composer.json at the repository root, so witness cannot tell which PHP test runner the project uses"
	}
	var manifest struct {
		Require    map[string]json.RawMessage `json:"require"`
		RequireDev map[string]json.RawMessage `json:"require-dev"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", "composer.json could not be parsed, so witness cannot tell which PHP test runner the project uses"
	}
	declares := func(pkg string) bool {
		_, dev := manifest.RequireDev[pkg]
		_, req := manifest.Require[pkg]
		return dev || req
	}
	switch {
	// Pest first: a Pest project always pulls phpunit in as well, and only the
	// pest binary runs Pest's tests.
	case declares("pestphp/pest"):
		return "vendor/bin/pest", ""
	case declares("phpunit/phpunit"):
		return "vendor/bin/phpunit", ""
	default:
		return "", "composer.json declares neither phpunit/phpunit nor pestphp/pest, so witness cannot tell how these tests are run"
	}
}
