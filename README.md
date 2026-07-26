# Witness

Smart test selection based on what actually changed.

Running the whole test suite on every change is slow. Guessing which tests matter is error-prone. Witness looks at your changed files, walks the dependency graph to find what they affect, and hands back a ranked, scored list of the tests worth running — and the command to run them.

Witness is the test-selection layer of the [Rivet](https://github.com/djtouchette/rivet) ecosystem. It runs standalone as a CLI, and Rivet embeds it to expose `witness.select`, `witness.run`, `witness.staged`, and `witness.since` as MCP tools. It builds on [recon](https://github.com/djtouchette/recon) for the underlying dependency and co-change analysis.

## What It Does

- Maps changed files to relevant tests via the dependency graph
- Scores by distance: **direct test (1.0) > 1-hop import (0.8) > 2-hop (0.5) > 3+-hop (0.3)**, plus co-change history, which is scored by frequency rather than by distance: **0.6** for 10+ shared commits, **0.5** for 5–9, **0.3** below that. A frequently co-changed test therefore outranks a 3-hop import — filter on the signal itself (`--signals`) rather than on `--min-score` if you want import evidence only
- Boosts tests that touch hotspot (high-risk) code
- Stops traversing at high-fan-out boundaries (>100 importers) so a shared utility doesn't drag in your entire suite
- Knows how to run **Go, Elixir, Python, Ruby, Node, Rust, .NET/C#, Java, Kotlin, Scala (Maven / Gradle / sbt), Swift, PHP and Dart** test frameworks
- Auto-detects changes from git (working tree, staged, or since a ref) when you don't pass files
- **Fails closed**: when it cannot prove its selection covers the change, it runs everything or exits non-zero — never "no tests, exit 0"

## Quick Start

```bash
# Install
go install github.com/djtouchette/witness/cmd/witness@latest

# Select tests for your current uncommitted changes
witness select

# Select tests for specific files
witness select internal/orders/handler.go internal/billing/charge.go

# Get a runnable command instead of a list
witness select --format exec

# Or just select and run the tests, exiting with the runner's code
witness run
```

Prebuilt binaries for Linux, macOS and Windows are on the [releases page](https://github.com/DJTouchette/witness/releases).

## Upgrading from v0.4.x

**v0.5.0 changes exit codes.** Two breaking changes will affect an existing CI job; the full list is in [CHANGELOG.md](CHANGELOG.md).

1. **`witness select --format paths` now exits 1 when it cannot prove the selection.** A list of paths has no way to say "run everything", so this recipe — which was in this README through v0.4.2 — no longer silently passes:

   ```bash
   witness select --format paths | xargs go test    # DON'T: false green
   ```

   On a `go.mod`-only diff that pipeline selected zero paths, ran `go test` with no arguments, and reported a pass on a change nothing had tested. Migrate to whichever fits:

   ```bash
   witness run                                      # select and execute, exit with the runner's code
   witness select --format exec                     # can express the whole suite; still exits 0
   witness select --format paths --fallback=none    # opt back into the partial list at exit 0
   ```

   `--format paths` still exits 0 whenever the selection *is* provable, which is the common case.

2. **`--fallback` is new and defaults to `full`.** Runs that used to be fast and empty are now slow and complete. See [Failing closed](#failing-closed) — and note that `--fallback=fail` is the recommended default for a CI job with a long suite.

`--format exec` can also now print more than one line (one command per language), and `--format json` gained `summary.unmapped`, `not_indexed`, `truncated`, `filtered` and `analysis_error`.

## Usage

```
witness select [files...] [-- runner args]   # print the selected tests (json / paths / exec)
witness run    [files...] [-- runner args]   # select and execute them, exiting with their code
```

If no files are given, witness detects changes from git: staged and unstaged edits to tracked files, plus untracked files that are not ignored. `select` output is JSON by default, with per-test scores and the signals that selected each one. `run` detects the test runner (go test, mix test, pytest, dotnet test, ...), streams its output, and exits with the runner's exit code — so it drops straight into a pre-commit hook or CI step.

Anything after `--` is forwarded to the test runner verbatim, so `witness run --since main -- -race` runs the selection with the race detector. `--test-cmd` (alias `--runner`) swaps out the detected program; the selection is still appended to it, except for the runners that cannot take paths (see below).

`--format exec` prints **one command per line** — a polyglot change, a multi-project .NET solution or several cargo targets each get their own. Run every line and take the *worst* exit code; joining them into a single `sh` invocation reports only the last one's status, which is a false green outside witness.

### Failing closed

A test selector that cannot answer must not answer "nothing to run". When witness cannot prove its selection covers the change — recon errored, a changed file is missing from its index, a changed file was deleted, or nothing at all was selected while changed files went uncovered — `--fallback` decides what happens:

| `--fallback` | Behaviour | Use it for |
|--------------|-----------|------------|
| `full` (default) | Run the project's whole suite (`go test ./...`, `mix test`, `pytest`, ...). Exits non-zero instead when witness cannot derive that command — see the *Whole suite* column below | The safe default: a short suite, or a job you would rather have slow than wrong |
| `fail` | Exit non-zero and explain why. Nothing is run | **Recommended for CI on a long suite** — loud and fast, needs a human |
| `none` | Report the gap on stderr and use the selection anyway | Pre-commit hooks. A hook is not the gate |

**`full` is the default on purpose.** Witness is advertised as a CI gate, and a gate that cannot say which tests cover a change has to run all of them — the alternative is a green check on a PR where zero tests executed. The classic case is a `go.mod`-only diff: no test maps to it, so witness used to select nothing and exit 0.

**`--fallback=fail` is the better CI default once your suite is long enough that running it by accident hurts.** It has the same safety property — witness never reports a pass it did not earn — but it spends a minute instead of forty, and it puts a human in the loop with a message naming exactly which files it could not account for. The trade is that an unindexed file turns the build red rather than slow, so it wants a project where a red build gets looked at.

What is and is not a gap:

- **An uncovered file while other tests were selected is not a gap by default.** A `go.mod` bump *alongside* a source edit still runs only the tests for the edit; witness names the uncovered file on stderr and moves on, because the gate still has teeth. Pass `--require-coverage` to make every changed file no selected test covers a gap — then `--fallback=fail` refuses the whole diff, and `--fallback=full` runs everything.
- **Documentation and binary assets are never a gap.** `.md`, `.txt`, images, editor backups and the like cannot change what a test does, so a docs-only commit runs nothing and exits 0 rather than dragging in the suite. Configuration, lockfiles and build manifests are *not* in that group.
- **A selection you emptied yourself is not a gap.** If `--kind`, `--exclude` or `--signals` filtered every candidate away, witness says so on stderr and stops — widening to the whole suite there would defeat the flag you just typed.

Witness always explains the fallback on stderr, and `--format json` carries the same information in `summary.unmapped`, `summary.not_indexed`, `summary.filtered` and `summary.analysis_error`.

`--format paths` is the one output that cannot express the fallback: a list of paths has no way to say "run everything". When witness cannot prove the selection and the policy is `full`, it prints the paths it did find, explains itself on stderr and **exits non-zero**. Use `--format exec` (which prints the whole-suite command) or `--fallback=none` to accept the partial list with exit 0.

A language witness has no runner for is an error, never a skipped suite: `run` exits non-zero, names the language and the tests that would have gone unrun, and tells you to use `witness select --format paths` and run them with your own tool. The same holds on the `full` path — if witness cannot derive a whole-suite command it says *that*, rather than running nothing and exiting 0.

### Exit codes

| Command | Code | Meaning |
|---------|------|---------|
| `select` | `0` | The printed output is the whole answer |
| `select` | `1` | No trustworthy answer: an invalid flag, a recon failure, `--fallback=fail` on a gap, or `--format paths` unable to express a `full` fallback. The reason is on stderr |
| `run` | `0` | The selected tests ran and passed — or the change provably needed none |
| `run` | *runner's code* | Tests ran and failed. The worst code across every suite witness ran, or 128+signal if one was killed |
| `run` | `1` | A witness error: the tests never ran. Distinguishable from a test failure only by the message on stderr, since most runners also exit 1 |

`run` never exits 0 without either executing the tests it selected or proving there were none to select.

### Which command each language gets

`--format exec` and `run` build these. Paths are grouped by the language of the file, not by one project-wide framework, so a polyglot selection produces one command per ecosystem.

| Language | Selected tests | Whole suite (`--fallback=full`) |
|----------|----------------|----------------------------------|
| Go | `go test ./pkg/orders/...` (package globs, never `_test.go` paths) | `go test ./...` |
| Elixir | `mix test test/shop/cart_test.exs` | `mix test` |
| Python | `pytest tests/test_api.py` | `pytest` |
| Ruby | `bundle exec rspec spec/order_spec.rb` | `bundle exec rspec` |
| Node | `npx jest --runTestsByPath src/cart.test.js` | `npx jest` |
| Rust | `cargo test --test orders_test`, one per target | `cargo test` |
| .NET/C# | `dotnet test ./tests/Orders.UnitTests`, one per project | `dotnet test` |
| Java / Kotlin (Maven) | `mvn test -pl services/orders -Dtest=OrderTest` | `mvn test` |
| Java / Kotlin / Scala (Gradle) | `gradle :libs:core:test --tests com.example.CacheTest` | `gradle test` |
| Scala (sbt) | `sbt 'testOnly com.example.OrderSpec'` | `sbt test` |
| Swift (SwiftPM) | `swift test --filter CalculatorTests`, one per suite | `swift test` |
| PHP | `vendor/bin/phpunit tests/OrderTest.php` | `vendor/bin/phpunit` |
| Dart / Flutter | `dart test test/order_test.dart` | `dart test` |

A checked-in `./mvnw` or `./gradlew` is preferred over the bare binary. A Pest project (`pestphp/pest` in `composer.json`) gets `vendor/bin/pest`, and a Flutter package gets `flutter test`.

Some of these are deliberately conservative rather than precise, because the precise form does not exist:

- **Rust.** `cargo test <path>` reads its positional as a test-*name* filter, not a path: it matches nothing, prints "0 passed; N filtered out" and exits 0. Witness names cargo targets instead, and a selection that maps to no nameable target (a `#[cfg(test)]` module under `src/`, whose `--lib` selector is invalid for a binary-only crate) widens to the whole `cargo test` rather than emitting a filter that runs nothing.
- **.NET/C#.** A test file sitting directly in `test/`/`tests/` cannot be pinned to a project, and `dotnet test ./tests` dies with MSB1003, so the run widens to the whole solution.

Over-running is safe. Emitting a plausible-looking command that runs zero tests is not.

#### Where the JVM, Swift, PHP and Dart arms refuse

None of these runners takes a test path, so the command is derived from the build file at the repository **root** and from the class the test file declares. Where that cannot be read, witness exits non-zero with the reason instead of guessing — including under `--fallback=full`, where there is no whole-suite command either. The cases you are most likely to meet:

- **No build file at the root.** Maven and Gradle are detected from `pom.xml` / `build.gradle[.kts]` / `settings.gradle[.kts]` **at the root**, Scala also from `build.sbt`; a repository whose build lives in a subdirectory gets no command. (`pom.xml` wins over Gradle, and `build.sbt` wins over both for Scala.)
- **Maven filters by simple class name**, so witness must find a class declaration in the file. Gradle and sbt need the fully qualified name, so they need the `package` clause too — a Scala brace-delimited `package foo { ... }` block is refused, since which block encloses the suite is not knowable from a line scan.
- **An sbt build with an explicit root project beside another project is refused outright.** `sbt 'testOnly com.example.CoreSpec'` typed at the root only reaches projects the root *aggregates*. When a build declares nothing at `file(".")` sbt generates a root that aggregates everything, and the command is correct — but declare `lazy val root = (project in file("."))` (the idiom for setting a name, or `publish / skip := true`) and that aggregation is gone unless the build restores it. sbt then reports "No tests to run" as a **success**. The project id needed to scope it (`sbt 'core/testOnly ...'`) is a val name, not a directory, so witness cannot derive it, and `sbt test` has the identical hole — widening is not an escape.
- **A suite declared inside another type is refused.** Its runtime name is `Fixtures$NestedSpec`, not `Fixtures.NestedSpec`, and the dotted form is exactly the filter sbt calls a success. (A JUnit 5 `@Nested` class is fine: it runs as part of the outer class, which is the one in the filter.)
- **Every top-level type in a selected file goes into the filter**, not just the one the file is named after. A second test class in the same file is a test the selection promised to run.
- **PHP needs `phpunit/phpunit` or `pestphp/pest` declared in `composer.json`.** A Pest project vendors `phpunit` too, and `vendor/bin/phpunit` runs *none* of its Pest tests, so the binary comes from what the manifest declares rather than from what is on disk.
- **Swift needs a `Package.swift`.** An Xcode project needs `xcodebuild -scheme … -destination …`, and neither is knowable from a test path.
- **Dart needs a `pubspec.yaml`** to tell `dart test` from `flutter test`.

Witness executes an argv directly and never through a shell, so a test path containing spaces, parens or metacharacters is passed through intact. The line `--format exec` prints is shell-quoted for pasting: `npx jest --runTestsByPath 'src/app/(marketing)/page.test.js'`.

### Flags

Both `select` and `run` share these; only `select` has `--format`, only `run` has `--timeout`.

| Flag | Default | Description |
|------|---------|-------------|
| `--format` | `json` | (`select` only) `json` (scored detail), `paths` (one path per line), or `exec` (runnable test command). An unrecognised value is an error, not a silent fallback to JSON |
| `--fallback` | `full` | What to do when the selection cannot be proven complete: `full`, `fail`, or `none` (see above) |
| `--require-coverage` | off | Treat *every* changed file no selected test covers as a gap, not only a wholly empty selection |
| `--depth` | `2` | How many import hops to traverse backward (`0` = direct tests only) |
| `--min-score` | `0.1` | Drop tests scoring below this (`0` = no minimum) |
| `--max` | `50` | Cap on number of tests returned (`0` = no cap) |
| `--kind <k>` | | Only return tests of these kinds: `unit`, `integration`, `e2e`, ... (repeatable) |
| `--signals <s>` | | Only return tests found by these signals: `direct-test`, `changed-test`, `import` (any hop), `co-change`, `hotspot-risk` (repeatable) |
| `--exclude <glob>` | | Drop test paths matching a glob, e.g. `vendor/**` (repeatable) |
| `--co-change-min` | `2` | Minimum co-change count before a co-changed test counts (`0` = no minimum) |
| `--fan-out-cap` | `100` | Don't expand files with more importers than this (`0` = no cap) |
| `--staged` | | Use `git diff --staged` (great for pre-commit hooks). Mutually exclusive with `--since` |
| `--since <ref>` | | Use `git diff <ref>...HEAD` (great for PR review) |
| `--test-cmd <cmd>` | | Override the detected runner, e.g. `--test-cmd "gotestsum --"`; the selection is appended for path-taking runners, and refused for Rust/JVM/Swift. `--runner` is an alias |
| `--timeout <dur>` | | (`run` only) Abort the test run after this long, e.g. `10m` |
| `--cache-dir <path>` | `<repo>/.recon/` | recon cache directory (recon's own default; witness only overrides it when you pass this flag) |

`witness --version` prints the build's version.

`--test-cmd` is an **argv, not a shell line**: witness tokenizes it (honouring quotes) and executes it directly, so `--test-cmd "GOFLAGS=-count=1 go test"` or `--test-cmd "go test ./... | head"` is rejected with the offending token named. Ask for a shell explicitly if you need one: `--test-cmd 'sh -c "GOFLAGS=-count=1 go test \"$@\"" witness'`. For the languages whose runners take paths, the selection is appended the way the detected command builds it — Go gets package globs, not raw `_test.go` paths. Three shapes are refused rather than mis-run:

- a selection spanning two languages, because one command cannot test them all;
- a Rust selection, because cargo reads the appended paths as test-name filters and runs nothing while exiting 0; and
- a Java, Kotlin, Scala or Swift selection, because those runners select by class, fully qualified name or `--filter`. `--test-cmd "mvn test"` would emit `mvn test src/test/java/com/example/CalculatorTest.java`, which fails with `LifecyclePhaseNotFoundException`.

To name JVM or Swift tests yourself, put the selector in `--test-cmd` and leave the selection empty: `witness run --test-cmd "mvn test -Dtest=CalculatorTest"`.

An explicitly-typed `0` always *widens* the selection (no minimum, no cap, no traversal) rather than emptying it — a mistyped flag must never be able to quietly select nothing.

### Common patterns

**CI gate on a PR.** The default: run the affected tests, or everything if witness cannot tell which those are.

```yaml
# .github/workflows/test.yml
- uses: actions/checkout@v4
  with:
    fetch-depth: 0                    # witness diffs against the base ref, so no shallow clone
- run: witness run --since origin/${{ github.base_ref }}
```

**CI gate with a long suite.** Recommended once the whole suite is slow enough that running it by accident costs real time. Witness will not guess and will not quietly widen — it stops and tells you which files it could not account for.

```yaml
- uses: actions/checkout@v4
  with:
    fetch-depth: 0
- run: witness run --since origin/${{ github.base_ref }} --fallback=fail
```

**Pre-commit hook.** Use `--fallback=none` here. A hook is not the gate: a docs-only or `go.mod`-only commit should not drag in the whole suite when CI is about to prove the change anyway.

```bash
#!/bin/sh
# .git/hooks/pre-commit  —  chmod +x this file
exec witness run --staged --fallback=none
```

Other shapes:

```bash
# PR review: the affected test paths, for your own tooling. Exits non-zero if it
# cannot prove the list is complete — see "Failing closed"
witness select --since main --format paths

# Tighter selection: direct + 1-hop imports only, with no co-change guesses
# (co-change alone can score 0.6, so --min-score does not express this)
witness select --depth 1 --signals direct-test,changed-test,import

# Unit tests only, skipping vendored and generated tests
witness run --kind unit --exclude 'vendor/**' --exclude '**/generated/**'

# Pass flags through to the runner
witness run --since main -- -race -count=1

# Strictest gate: every changed file must be covered by a selected test
witness run --since origin/main --require-coverage --fallback=fail
```

## How Scoring Works

For each changed file, witness:

1. **Direct match** — if the file *is* a test, or has a known test, include it (score 1.0).
2. **Reverse dependency walk** — BFS backward through the import graph ("who imports this?") up to `--depth`, scoring by hop distance. Files with more importers than `--fan-out-cap` (default 100) are treated as fan-out boundaries and not expanded, so framework/utility files don't explode the result set.
3. **Co-change history** — tests that have historically changed alongside this file, scored by frequency.
4. **Hotspot boost** — if the changed file is high-risk (high fan-in × churn), nudge its candidate tests up by 0.1, capped at 1.0.

Tests are then filtered by `--min-score`, sorted by score, capped at `--max`, and returned with the signals and source files that selected each one.

## Library Use

The selector is available as a Go package (`github.com/djtouchette/witness/pkg/witness`):

```go
// gate returns the exit code a CI step should exit with.
func gate(ctx context.Context) (int, error) {
	w, err := witness.New(".") // must be inside a git repo; analysis is
	if err != nil {            // rooted at the repository root
		return 1, err
	}
	defer w.Close()

	res, err := w.SelectStaged(witness.DefaultOptions())
	// or w.Select(files, opts), w.SelectSince(ref, opts)
	if err != nil {
		return 1, err
	}

	// Fail closed: an empty selection is only trustworthy when Complete says so.
	if !witness.Complete(res) {
		return runFullSuite(ctx, w)
	}
	if len(res.Tests) == 0 {
		return 0, nil // proven: this change needs no tests
	}

	// A non-zero code with a nil error means tests failed. A non-nil error
	// means they never ran, and the code must not be reported as a result.
	return w.Run(ctx, res, os.Stdout, os.Stderr) // worst code across suites
}

// runFullSuite is the fallback. w.Run only ever runs a selection, so the
// whole-suite commands are the caller's to execute.
func runFullSuite(ctx context.Context, w *witness.Witness) (int, error) {
	cmds, err := w.FullSuiteCommand()
	if err != nil {
		return 1, err // no runner for this language — never skip silently
	}
	worst := 0
	for _, argv := range cmds {
		c := exec.CommandContext(ctx, argv[0], argv[1:]...)
		c.Dir, c.Stdout, c.Stderr = w.Root(), os.Stdout, os.Stderr
		var exit *exec.ExitError
		switch err := c.Run(); {
		case err == nil:
		case errors.As(err, &exit):
			worst = max(worst, exit.ExitCode())
		default:
			return 1, err // the suite never started
		}
	}
	return worst, nil
}
```

`Complete` is the embedded form of the CLI's gate: it is false when recon errored, when a changed file is missing from the index, or when nothing was selected while changed files went uncovered (the `go.mod`-bump case). Treat `Complete(res) == false` with an empty `res.Tests` as "witness could not tell", never as "nothing needs testing".

Three things to know about the embedded API:

- **`Run` only ever runs the selection.** It derives the commands from the result you hand it and returns an error for an empty one; it does not apply a fallback. `Commands` and `FullSuiteCommand` return `[][]string` argvs for you to execute — as above — so the fallback policy stays yours.
- **`Complete` does not know about deletions.** The CLI treats a deleted changed file as a gap because it reads git's status letters; the library takes paths. If that matters, diff with statuses yourself and gate on them too.
- **`Complete` uses the lenient rule**, the CLI's default: an uncovered file alongside a non-empty selection is not a gap. There is no library equivalent of `--require-coverage`; check `res.Summary.Unmapped` directly for the strict reading.

`SelectStaged` and `SelectSince` never fall back to a working-tree diff: an empty index or an empty ref range yields an empty result, not the answer to a different question. Cancelling `ctx` stops the runner and everything it spawned.

The full CLI command is also importable (`github.com/djtouchette/witness/pkg/embedded`) for embedding into another binary — which is how Rivet wires it in. It writes through cobra's writers, so `cmd.SetOut`/`SetErr` captures everything, and it never calls `os.Exit`: `witness run` returns a `*cli.ExitCodeError` carrying the runner's code.

## Building

```bash
make build       # build to ./witness
make test        # run tests
make e2e         # the end-to-end golden tests, verbosely
make golden      # regenerate the golden files after an intentional change
make vet         # static analysis
make install     # go install
make clean       # remove the binary and recon's cache
```

Requires Go 1.25+. The compiled binary is gitignored, as is recon's analysis cache — which lives in `<repo>/.recon/` unless you point `--cache-dir` somewhere else. `make clean` removes both.

The golden files under `internal/e2e/testdata/golden/` record exactly what the real binary prints, and its exit code, for each fixture repository. Read the diff `make golden` produces before committing it: every line of it is a change to what witness tells CI.

## Changelog

See [CHANGELOG.md](CHANGELOG.md). v0.5.0 contains breaking changes to exit codes — see [Upgrading from v0.4.x](#upgrading-from-v04x).

## License

MIT — see [LICENSE](LICENSE).
