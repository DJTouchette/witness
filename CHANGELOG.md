# Changelog

All notable changes to witness are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased] — 0.5.0

The theme of this release is **failing closed**. An audit found several paths on
which witness selected no tests, emitted a command that ran no tests, or emitted
a command that could not work at all — and exited 0 in every case. A CI gate
that passes without running anything is worse than a slow one, so every one of
those paths now either runs the whole suite or exits non-zero.

### BREAKING CHANGES

Read this section before upgrading. Two of these will change what your CI does.

- **`witness select --format paths` now exits 1 when it cannot honour a `full`
  fallback.** A list of paths has no way to say "run everything", so when
  witness cannot prove the selection covers the diff and `--fallback` is `full`
  (the default), it prints the paths it did find, explains itself on stderr and
  exits non-zero.

  This breaks `witness select --format paths | xargs go test`, a recipe that was
  in witness's own README through v0.4.2 — and it is the exact false green the
  audit found. On a `go.mod`-only diff that pipeline selected zero paths, ran
  `go test` with no arguments, and reported a pass on a change nothing had
  tested. Migrate to one of:

  ```bash
  witness select --format exec        # can express the whole suite; still exit 0
  witness run                         # select and execute, exit with the runner's code
  witness select --format paths --fallback=none   # opt back into the partial list at exit 0
  ```

  `--format paths` still exits 0 whenever the selection *is* provable, which is
  the common case. `--format json` is unaffected: the gaps are already in
  `summary`, so there is nothing for `full` to widen, and only `--fallback=fail`
  changes its exit code.

- **`--fallback` is new and defaults to `full`.** When witness cannot prove the
  selection covers the change — recon errored, a changed file is not in the
  index, a changed file was deleted, or nothing was selected while changed files
  went uncovered — it now runs the project's entire suite instead of exiting 0.
  Runs that used to be fast and empty will now be slow and complete. See
  "Failing closed" in the README for `fail` and `none`, and for which CI shape
  wants which.

- **`--format exec` can now print more than one line**, one command per
  language or project. Consumers that assumed a single line must run every line
  and take the *worst* exit code; joining them with `&&` or `;` in one `sh`
  invocation reports only the last one's status.

  ```
  mix test test/shop/cart_test.exs
  npx jest --runTestsByPath assets/js/__tests__/cart.test.js
  ```

- **An unrecognised `--format` is now an error.** Through v0.4.2 anything that
  was not `paths` or `exec` fell through to JSON, so `--format path` (singular)
  silently printed JSON. It now exits 1 naming the accepted values. `--fallback`
  is validated the same way.

- **`--format json` output shape changed.** Empty selections marshal as `[]`
  rather than `null` (`changed_files` and `tests` both), and `summary` gained
  five `omitempty` fields: `unmapped`, `not_indexed`, `truncated`, `filtered`
  and `analysis_error`. Existing fields are unchanged, but a consumer that
  type-asserted `null` for an empty list needs updating, and one that gates CI
  should now read the new fields rather than `len(tests)`.

- **`summary.by_signal` counts the returned tests**, not the candidate set. It
  previously credited signals for tests the `--max` cap had already dropped, so
  the summary described a run that never happened. The tests removed by the cap
  are now counted in `summary.truncated`.

- **Library: `SelectStaged` and `SelectSince` no longer fall back to a
  working-tree diff.** An empty index or an empty ref range yields an empty
  result rather than the answer to a different question. A caller relying on the
  old behaviour should call `Select(nil, opts)` explicitly.

- **Library: `witness.New` now roots analysis at the git repository root** and
  returns an error outside a git working tree. It previously rooted recon at the
  directory it was handed, which put it in a different path space from the
  repo-root-relative paths git reports — every lookup missed and witness
  answered "no tests" for changes it simply could not see.

- **A relative `--cache-dir` now resolves against the repository root, not the
  process's working directory.** `witness select --cache-dir .cache` typed in
  `sub/deep/` used to put the cache in `sub/deep/.cache`, so the same flag in
  the same repository named a different — and always cold — cache depending on
  where it was typed, and two checkouts started from the same parent shared one.
  Every other path witness handles is repo-root-relative; the cache is now too.
  An absolute path is passed through untouched.

  The cache is derived analysis, so the cost of the move is one rebuild. The CLI
  prints a note when it detects a populated cache left behind at the old
  location. **`witness.WithCacheDir` changed the same way and prints nothing** —
  an embedder passing a relative path gets the new location silently, which is
  correct but worth knowing before the first slow run.

- **Anything after `--` is now forwarded to the test runner** instead of being
  treated as a positional file argument. `witness run -- -race` used to select
  tests for a changed file named `-race`, find none, and exit 0.

- Minimum recon is now v0.13.1 (was v0.8.0).

### Added

- **Test-runner support for six more ecosystems: Java, Kotlin, Scala, Swift,
  PHP and Dart.** None of these runners accepts a test *path*, so every command
  is derived from the build file at the repository root and from the package and
  class the test file itself declares — and a path that does not yield both is a
  loud refusal, never a guess.

  | Ecosystem | Selected tests | Whole suite |
  |-----------|----------------|-------------|
  | Maven (Java, Kotlin, Scala) | `mvn test -pl services/orders -Dtest=OrderTest` | `mvn test` |
  | Gradle (Java, Kotlin, Scala) | `gradle :libs:core:test --tests com.example.CacheTest` | `gradle test` |
  | sbt (Scala) | `sbt 'testOnly com.example.OrderSpec'` | `sbt test` |
  | SwiftPM | `swift test --filter CalculatorTests`, one per suite | `swift test` |
  | PHPUnit / Pest | `vendor/bin/phpunit tests/OrderTest.php` | `vendor/bin/phpunit` |
  | Dart / Flutter | `dart test test/order_test.dart` | `dart test` |

  A checked-in `./mvnw` or `./gradlew` wins over the bare binary; a Pest project
  gets `vendor/bin/pest`; a Flutter package gets `flutter test`. Maven is scoped
  with `-pl` to the module owning the class and never passes
  `-DfailIfNoTests=false`, which would turn a class name witness got wrong into
  a zero-test success.

  The derivation caveats are the failure modes you will meet first, and all of
  them exit non-zero rather than run nothing: Maven and Gradle need their build
  file **at the root**; PHP needs `phpunit/phpunit` or `pestphp/pest` declared
  in `composer.json`, because a Pest project vendors phpunit too and
  `vendor/bin/phpunit` runs none of its tests; Swift needs a `Package.swift`
  (an Xcode project needs a scheme and destination no path can supply); Dart
  needs a `pubspec.yaml` to tell `dart test` from `flutter test`; and **sbt
  reports a filter that matched nothing as a success**, which is why an sbt
  build with an explicit root project beside another project (a root-level
  `testOnly` reaches only what the root aggregates) and a suite declared inside
  another type (`Fixtures$NestedSpec`, not `Fixtures.NestedSpec`) are both
  refused outright. See "Where the JVM, Swift, PHP and Dart arms refuse" in the
  README.
- `--fallback=full|fail|none` on both `select` and `run`: what to do when the
  selection cannot be proven complete. Defaults to `full`. `fail` is the
  recommended CI default for a long suite; `none` is the right choice in a
  pre-commit hook.
- `--require-coverage`: treat *every* changed file no selected test covers as a
  gap, not only a wholly empty selection.
- `--signals`: keep only tests found by given signals (`direct-test`,
  `changed-test`, `import` — which matches every hop — `co-change`,
  `hotspot-risk`). `--min-score` could not express "import evidence only",
  because a frequently co-changed test scores 0.6 and a 3-hop import 0.3.
- `--test-cmd` (alias `--runner`): replace the detected runner, with the
  selection still appended and translated the same way. It is an argv, not a
  shell line: it is tokenized honouring quotes and executed directly, and a
  leading environment assignment or a shell operator is rejected by name rather
  than passed to the runner as a literal argument.
- `--timeout` on `run`: abort the test run after a duration.
- `--` passthrough: everything after it is appended to the runner's arguments,
  so `witness run --since main -- -race` runs the selection with the race
  detector.
- `summary.unmapped`, `summary.not_indexed`, `summary.truncated`,
  `summary.filtered` and `summary.analysis_error` in `--format json`, all
  `omitempty` — everything the selection could not account for, so an embedder
  can apply its own fallback policy.
- Stderr diagnostics for every gap: uncovered files, files missing from the
  index, a selection the `--max` cap truncated, and a selection the caller's own
  `--kind`/`--exclude`/`--signals` emptied. "Nothing to run" and "you filtered
  everything out" were previously indistinguishable from the exit code.
- Library API on `pkg/witness`, so an embedder no longer has to reach into an
  internal package: `Complete(result)`, `(*Witness).Commands`,
  `(*Witness).FullSuiteCommand`, `(*Witness).Run`, `(*Witness).Root`,
  `DefaultOptions`, and the `ScoredTest` / `Summary` type aliases.
- Ctrl-C handling in the standalone binary (`cli.SignalContext`), which
  cancels the run and signals the test runner's whole process group. Not
  installed when witness is embedded, because signal handlers are process-wide.
- An end-to-end golden suite (`internal/e2e`) that drives the real binary
  against Go, Python, Node, Rust, Java, C# and polyglot fixture repositories,
  plus `make e2e` and `make golden`.
- MIT `LICENSE` file.
- `embedded.ExitCodeError` and `embedded.TestsFailed(err) (int, bool)`, so an
  embedding host can tell "the tests ran and failed, with this code" from
  "witness itself failed". The command cannot call `os.Exit` without killing its
  host, so the runner's exit code travels out as an error; until now that type
  was only reachable from `cmd/witness/cli`, which forced hosts to flatten every
  failure to 1.

### Changed

- **Test commands are executed as an argv, never through `sh -c`.** Test paths
  containing spaces, parens or shell metacharacters are passed through intact
  and can no longer be reinterpreted as shell syntax. `--format exec` still
  prints a copy-pasteable, shell-quoted line for display.
- **Go tests are pointed at package globs**, `./internal/orders/...`, not raw
  `_test.go` paths. `go test internal/orders/orders_test.go` compiles that one
  file as package `command-line-arguments` and dies with `undefined: ...`.
- **Node tests use `npx jest --runTestsByPath`**, never bare positionals.
- **Rust integration tests use `cargo test --test <target>`**, and a selection
  that maps to no nameable cargo target widens to `cargo test` rather than
  emitting a filter that runs nothing.
- **C# emits one `dotnet test <project>` per project**, as separate commands
  rather than an `&&` chain, so a failing project cannot short-circuit the ones
  after it. A test that cannot be pinned to a project widens to `dotnet test`.
- **A polyglot selection emits one command per ecosystem.** Every command runs
  even if an earlier one fails, and the worst exit code wins, so a red Elixir
  suite cannot cause the JavaScript suite to be skipped.
- **A language witness has no runner for is an error, not a skipped suite.**
  The error names the language and the tests that would have gone unrun, and
  points at `witness select --format paths`. The `full` fallback has its own
  wording — there are no selected tests to list there, and a repository whose
  language recon could not name is described as exactly that rather than as a
  language called "unknown".
- **Documentation and binary assets are never treated as an uncovered change.**
  `.md`, `.txt`, images, editor backups and the like cannot change what a test
  does, so a docs-only commit runs nothing and exits 0. Configuration, lockfiles
  and build manifests are deliberately *not* in that group — a `go.mod` bump is
  precisely the uncovered change the fallback exists for.
- **A selection the caller emptied with `--kind`/`--exclude`/`--signals` is not
  a gap.** Widening to the whole suite there would defeat the flag just typed.
  It is reported on stderr and counted in `summary.filtered`.
- **An explicitly typed `0` widens rather than empties.** `--max 0`,
  `--min-score 0`, `--depth 0`, `--co-change-min 0` and `--fan-out-cap 0` now
  mean "no cap / no minimum / direct only" instead of being silently replaced by
  the default. A flag typo must not be able to quietly select nothing.
- **`--staged` and `--since` are mutually exclusive**, as are `--test-cmd` and
  `--runner`. Taking one silently is how a job ends up gating on nothing.
- **A signal-killed test runner reports 128+signal** (137, 143) instead of -1,
  which collided with witness's "never ran" sentinel.
- **The whole-suite fallback picks the repo's primary language**, consulting
  detected frameworks only if no language is runnable. Framework detection
  matches any name containing `.net`/`xunit`, so a Go repository holding one C#
  fixture directory answered `dotnet test` — a fail-closed path that ran nothing
  at all.
- `witness select` writes through the cobra command's writers instead of
  `os.Stdout`/`os.Stderr`, so an embedding host captures the output rather than
  having it leak into its stdio stream. (`witness run` was fixed this way in
  v0.4.1; `select` was missed.)
- Cancelling the context stops the test runner and everything it spawned:
  SIGTERM to the child's process group, then SIGKILL after a five-second grace.
- recon upgraded from v0.8.0 to v0.13.1.
- `--test-cmd` / `--runner` now refuses a Java, Kotlin, Scala or Swift selection
  instead of appending file paths to it. Those runners select by class, fully
  qualified name or `--filter`, so `--test-cmd 'mvn test'` used to emit
  `mvn test src/test/java/com/example/CalculatorTest.java` — which dies with
  `LifecyclePhaseNotFoundException` (sbt: `Not a valid key: src`). Loud rather
  than a false green, but wrong. To name JVM or Swift tests yourself, put the
  selector in `--test-cmd` and leave the selection empty. Rust was already
  refused for the same reason; the docs claimed paths were translated for every
  language, which was never true.

### Fixed

- **`witness select --format paths | xargs go test` reported a pass on a
  `go.mod`-only diff.** No test mapped to the change, witness printed nothing,
  `xargs` ran nothing, and CI went green. This is what the `--format paths` exit
  code and the whole `--fallback` machinery exist for.
- **`cargo test <path>` ran zero tests and exited 0.** cargo reads a positional
  argument as a test-*name* filter, so a path matched nothing, reported
  "0 passed; N filtered out", and turned a failing Rust suite green. `--test-cmd`
  now refuses a Rust selection outright for the same reason, rather than
  appending paths cargo would ignore.
- **jest positional arguments are regexes, not paths.** A Next.js route group
  (`src/app/(marketing)/page.test.tsx`) read as a capture group, matched no
  test, and — with the mainstream `passWithNoTests` setting — exited 0 having
  run none of the selected tests. The same held for any path containing
  `+ [ ] ? | $`.
- **`dotnet test ./tests` died with MSB1003** for a test file sitting directly
  in `test/`/`tests/` with no project to point at.
- **Running witness from a subdirectory found no tests.** recon was rooted at
  the working directory while git reported repo-root-relative paths. Both are
  now rooted at the repository root, and positional path arguments are rewritten
  relative to it — resolving symlinks on both sides first, so a symlinked
  checkout (a symlinked home, macOS `/tmp`) no longer puts the two in different
  spellings of the same directory.
- **New, untracked files selected zero tests.** `git diff` never reports them;
  witness now also reads `git ls-files --others --exclude-standard`. An
  untracked nested git repository, which git reports as a directory, is skipped
  rather than passed on as a changed file that widened every run to the whole
  suite.
- **Renamed files lost their dependents.** The old path is now reported as a
  deletion so the tests that depended on it still run.
- **A repository with no commits could not be diffed** (unborn HEAD); the index
  is now treated as the whole change.
- **Ctrl-C orphaned the test runner.** The runner is started in its own process
  group so an embedder can cancel it without signalling itself, which also meant
  the terminal's SIGINT reached witness alone: witness died and `go test` kept
  running with no parent, unreachable by a second Ctrl-C.
- **`--kind unit` silently skipped every test under `src/features/`.** Kind
  classification matched substrings, so the mainstream React/Vue/Redux layout
  was relabelled "acceptance" and vanished from the lane CI actually runs. The
  match is now on whole words of a path segment.
- **`Audit.java`, `Deposit.java`, `Manifest.java`, `Circuit.kt` and
  `Latest.php` were classified as tests** because they end in "it" or "test"
  once lowercased. Test-name conventions are now matched the way each language
  writes them.
- **A test's kind depended on which signal discovered it first**, so the same
  file was "unit" for one diff and "smoke" for another. It now has one source of
  truth.
- **`--exclude 'vendor/**'` also dropped
  `internal/billing/vendor_client_test.go`.** The base-name retry is no longer
  applied to patterns containing a `/`.
- **Fixtures and helpers were selected as runnable tests.** recon classifies
  everything under a test directory as a test; `conftest.py`, `spec_helper.rb`,
  `jest.setup.js` and friends are not paths a runner can be pointed at.
- **`--test-cmd "GOFLAGS=-count=1 go test"` was passed to the runner as a
  literal argument** and failed with a message naming neither the cause nor the
  fix. Environment assignments and shell operators are now rejected by name,
  with the `sh -c` wrapping shown.
- Git paths are parsed from `--name-status -z` output, so filenames with
  spaces, newlines or non-ASCII characters are no longer mangled by git's
  C-quoting.
- `--cache-dir` was documented as defaulting to `.witness/`; recon's actual
  default is `<repo>/.recon/`. Both are now gitignored and removed by
  `make clean`.
- The README's library example did not compile (it imported an internal
  package) and its fail-closed pattern computed a whole-suite fallback it then
  threw away.

## [0.4.2] — 2026-07-02

### Added

- .NET/C# support: `dotnet test <project>` commands, `.csproj`/`.sln` project
  metadata handling, and conventional C# test-path detection.

## [0.4.1] — 2026-06-07

### Fixed

- `witness run` is embed-safe. It wrote test output to `os.Stdout`/`os.Stderr`
  and called `os.Exit`, which corrupted an embedding host's stdio and killed its
  process. It now writes through `cmd.OutOrStdout()`/`ErrOrStderr()` and carries
  the runner's exit code out as a typed `ExitCodeError` that `main()` translates
  into `os.Exit`; embedded callers see a normal non-nil error.

## [0.4.0] — 2026-06-07

### Added

- `witness run`: select tests and execute them, exiting with the runner's code.
- Test-kind classification and the `--kind` filter.
- `--exclude` glob patterns.
- `--co-change-min` and `--fan-out-cap` tuning knobs.

### Changed

- gofmt gate added to CI.

## [0.3.0] — 2026-06-07

### Added

- CI and release pipeline (GoReleaser).
- README.

### Changed

- recon upgraded to v0.8.0.
- Raised test coverage.

## [0.2.0] — 2026-04-04

### Changed

- recon upgraded to v0.7.0: C# dotted directories and test import scanning.

## [0.1.0] — 2026-04-04

### Added

- Initial release: test selection from a git diff via recon's dependency graph,
  co-change history and hotspot scoring.
- `witness select` with `json`, `paths` and `exec` output.
- Go library API (`pkg/witness`) and embeddable CLI (`pkg/embedded`).

[Unreleased]: https://github.com/DJTouchette/witness/compare/v0.4.2...HEAD
[0.4.2]: https://github.com/DJTouchette/witness/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/DJTouchette/witness/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/DJTouchette/witness/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/DJTouchette/witness/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/DJTouchette/witness/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/DJTouchette/witness/releases/tag/v0.1.0
