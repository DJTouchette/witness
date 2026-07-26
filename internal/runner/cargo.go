package runner

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// rustCommands maps test paths to cargo target selectors.
//
// `cargo test <path>` treats the positional as a test NAME filter, not a path:
// it matches nothing, reports "0 passed; N filtered out" and exits 0, so a
// failing suite looks green. Targets must be named instead — `--test <name>`
// for an integration test under tests/, `--lib` for a #[cfg(test)] module in
// src/ — and anything that maps to neither falls back to the whole suite
// rather than to a filter that runs nothing.
//
// In a workspace the target name alone is not enough. Run from the workspace
// root, `cargo test --test orders` looks for that target in the root package
// (or, under a virtual manifest, in nothing at all) and answers "no test target
// named `orders`" — a loud failure, but a failure. The package has to be named
// too, and its name comes from the [package] name of the nearest Cargo.toml
// rather than from the directory, which is frequently not the same string
// (crates/http-api holding package "acme-http").
func rustCommands(root string, paths []string) []Command {
	if !cargoWorkspace(root) {
		return rustCrateCommands(paths)
	}

	var (
		wholeWorkspace bool
		wholePackages  = make(map[string]bool)
		targets        = make(map[string]map[string]bool)
	)
	for _, p := range paths {
		pkg := cargoPackageOf(root, p)
		switch target := rustTestTarget(p); {
		case pkg == "":
			// No manifest to name the package with: widening to the whole
			// workspace over-runs, where a guessed -p would run another crate's
			// tests and report their green as this change's.
			wholeWorkspace = true
		case target == "":
			// A unit test in src/ has no nameable target, but the package it
			// belongs to is known, so the run widens to that package instead of
			// to everything. `cargo test -p <pkg>` is valid whatever shape the
			// crate has, which `--lib` is not.
			wholePackages[pkg] = true
		default:
			if targets[pkg] == nil {
				targets[pkg] = make(map[string]bool)
			}
			targets[pkg][target] = true
		}
	}

	if wholeWorkspace {
		// Not a bare `cargo test`: with a root package present that runs only
		// the root package's tests, silently skipping the member the selection
		// pointed at.
		return []Command{{Lang: "rust", Argv: []string{"cargo", "test", "--workspace"}}}
	}

	packages := make(map[string]bool, len(targets)+len(wholePackages))
	for pkg := range targets {
		packages[pkg] = true
	}
	for pkg := range wholePackages {
		packages[pkg] = true
	}

	var commands []Command
	for _, pkg := range sortedKeys(packages) {
		if wholePackages[pkg] {
			commands = append(commands, Command{Lang: "rust", Argv: []string{"cargo", "test", "-p", pkg}})
			continue
		}
		for _, target := range sortedKeys(targets[pkg]) {
			commands = append(commands, Command{Lang: "rust", Argv: []string{"cargo", "test", "-p", pkg, "--test", target}})
		}
	}
	if len(commands) == 0 {
		return []Command{{Lang: "rust", Argv: []string{"cargo", "test", "--workspace"}}}
	}
	return commands
}

// rustCrateCommands is the single-crate form: no -p, because there is only one
// package and naming it adds a way to be wrong.
func rustCrateCommands(paths []string) []Command {
	var (
		targets = make(map[string]bool)
		whole   bool
	)
	for _, p := range paths {
		switch target := rustTestTarget(p); target {
		case "":
			whole = true
		default:
			targets[target] = true
		}
	}
	if whole {
		return []Command{{Lang: "rust", Argv: []string{"cargo", "test"}}}
	}

	var commands []Command
	for _, name := range sortedKeys(targets) {
		commands = append(commands, Command{Lang: "rust", Argv: []string{"cargo", "test", "--test", name}})
	}
	return commands
}

// rustTestTarget returns the cargo integration-test target name for a path, or
// "" when the path maps to no nameable target and the run has to widen.
//
// A #[cfg(test)] module under src/ is deliberately in the second group. The
// selector for it would be `--lib`, but that is not valid for every crate
// shape: verified against cargo 1.94.1, a crate with only src/main.rs answers
// "error: no library targets found in package" and exits 101, and adding
// --bins does not help — the --lib term itself is what fails. cargo has no
// crate-shape-independent way to say "this crate's unit tests", so witness
// over-runs rather than emitting a command that dies on a binary crate.
func rustTestTarget(path string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i, part := range parts[:len(parts)-1] {
		if part != "tests" {
			continue
		}
		// cargo builds one target per tests/<name>.rs and per tests/<dir>/,
		// so the segment right after tests/ names the target either way.
		return strings.TrimSuffix(parts[i+1], ".rs")
	}
	return ""
}

// cargoWorkspace reports whether the repository root's Cargo.toml declares a
// workspace. Only the root is consulted: witness runs cargo IN the root, and
// that is the manifest cargo itself will read.
func cargoWorkspace(root string) bool {
	data, err := readRepoFile(root, "Cargo.toml")
	if err != nil {
		return false
	}
	return parseCargoManifest(data).workspace
}

// cargoPackageOf returns the [package] name of the nearest Cargo.toml at or
// above a test path, or "" when there is none, it cannot be read, or it is a
// virtual manifest with no package of its own.
func cargoPackageOf(root, testPath string) string {
	dir, ok := nearestManifest(root, testPath, "Cargo.toml")
	if !ok {
		return ""
	}
	data, err := readRepoFile(root, path.Join(dir, "Cargo.toml"))
	if err != nil {
		return ""
	}
	return parseCargoManifest(data).name
}

// cargoManifest is the part of a Cargo.toml witness needs.
type cargoManifest struct {
	// name is the [package] name, "" for a virtual manifest or one this scan
	// could not read.
	name string
	// workspace reports a [workspace] table.
	workspace bool
}

// cargoPackageNameRe is what cargo accepts as a package name. A value that does
// not match is discarded rather than passed to -p.
var cargoPackageNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// parseCargoManifest pulls the [package] name and the presence of a [workspace]
// table out of a Cargo.toml.
//
// This is a field extraction, not a TOML parser, and not a regex over the whole
// file either: it walks lines tracking the current table, skips comments and
// multi-line strings (a README embedded in `description = """..."""` is full of
// text that looks like TOML), and accepts `name` only as a plain quoted string
// inside [package]. Anything it is unsure of leaves the name empty, which widens
// the run to `cargo test --workspace`. That asymmetry is the whole design: a
// missing name over-runs, while a wrong name would hand -p another crate whose
// green tests would be reported as this change's.
func parseCargoManifest(data []byte) cargoManifest {
	var (
		manifest cargoManifest
		table    string
		closing  string // delimiter ending the multi-line string being skipped
	)
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		if closing != "" {
			_, after, found := strings.Cut(line, closing)
			if !found {
				continue
			}
			closing, line = "", after
		}
		line = strings.TrimSpace(stripTOMLComment(line))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			table = tomlTableName(line)
			if table == "workspace" || strings.HasPrefix(table, "workspace.") {
				manifest.workspace = true
			}
			continue
		}

		rawKey, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key := strings.Trim(strings.TrimSpace(rawKey), `"'`)
		value = strings.TrimSpace(value)
		if delim := multilineStringDelim(value); delim != "" {
			closing = delim
			continue
		}
		switch {
		// The dotted forms are the same tables written without a header:
		// `package.name = "acme"` at top level is legal TOML.
		case (table == "package" && key == "name") || (table == "" && key == "package.name"):
			if manifest.name == "" {
				manifest.name = tomlString(value, cargoPackageNameRe)
			}
		case table == "" && strings.HasPrefix(key, "workspace."):
			manifest.workspace = true
		}
	}
	return manifest
}

// stripTOMLComment removes a trailing # comment, leaving one inside a string
// alone.
func stripTOMLComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return line[:i]
		}
	}
	return line
}

// tomlTableName returns the table a header line names: "package" for [package],
// "bin" for [[bin]].
func tomlTableName(line string) string {
	end := strings.LastIndex(line, "]")
	if end < 0 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(line[:end+1]), "[]")
}

// multilineStringDelim reports the delimiter of a multi-line string opened on
// this line, or "" if the value is not one (or opens and closes here).
func multilineStringDelim(value string) string {
	for _, delim := range []string{`"""`, `'''`} {
		if strings.HasPrefix(value, delim) && !strings.Contains(value[len(delim):], delim) {
			return delim
		}
	}
	return ""
}

// tomlString unquotes a single-line TOML string and validates it, returning ""
// for anything that is not a plain quoted value matching valid.
func tomlString(value string, valid *regexp.Regexp) string {
	if len(value) < 2 {
		return ""
	}
	quote := value[0]
	if quote != '"' && quote != '\'' {
		return ""
	}
	end := strings.IndexByte(value[1:], quote)
	if end < 0 {
		return ""
	}
	unquoted := value[1 : 1+end]
	if !valid.MatchString(unquoted) {
		return ""
	}
	return unquoted
}
