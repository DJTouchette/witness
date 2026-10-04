# Witness

Witness selects relevant tests from Recon's dependency graph and history, then
plans commands from the repository's actual manifests. It works standalone and
is embedded by Rivet. Planning never invokes a test runner, package manager,
configuration script, model, database, or cloud service. Recon may update its
local index; use `--cache-dir` to put that index outside the project.

```sh
witness select path/to/source              # ranked tests + coverage diagnostics
witness select --format plan path/to/source # versioned JSON argv/cwd plan
witness select --format exec path/to/source # POSIX command lines, no execution
witness run path/to/source                 # EXPLICITLY executes the plan
```

**Rivet's MCP `witness.run` means planning** (`select --format exec`). The
standalone `witness run` and shell `rivet witness run` execute tests. These are
different interfaces. A project configuration describes commands; discovering
that configuration is not authority for an agent to execute them.

## Selection and coverage

With no positional paths, Witness reads the working-tree diff, including staged
and untracked changes. `--staged` and `--since REF` choose other Git diffs.
Paths typed from a subdirectory are resolved relative to that directory; output
paths are repository-relative. Recon refreshes its index when opened. Missing,
new, renamed, deleted, ignored and unsupported files must not be confused with
changes requiring no tests.

Selection JSON retains `changed_files`, `tests`, and `summary`. Tests contain
`path`, `score`, `signals`, `kind`, and `for_files`. Summary reports `unmapped`,
`not_indexed`, `analysis_error`, `truncated`, `filtered`, and `diagnostics` when
applicable. Diagnostics include unresolved imports, unsupported/failed parses
and skipped high-fan-out traversal. History-only matches remain recommendations,
not proof of coverage. Rust files with inline test attributes are candidates even
when their filenames do not end in `_test.rs`.

`--depth`, `--min-score`, `--max`, `--co-change-min`, and `--fan-out-cap` tune the
ranking. `--kind`, `--exclude`, and `--signals` filter the ranked list. A zero
`--max` removes the result cap; zero `--depth` disables the import walk.

An empty or sparse list is **unproven**, not a passing test result. `Complete`
in the Go API now rejects *any* uncovered changed file, truncated selection or
reported analysis diagnostic. Selection itself is heuristic: absence of a
reported gap does not mathematically prove the dependency graph complete.

## Plans

The machine contract is [test-plan-v1.json](schemas/test-plan-v1.json).

`select --format plan` always writes a machine result when planning resolves or
refuses a suite (initialization/usage errors can still occur before a result).
It exits nonzero for an incomplete plan. It ignores no unsupported part of a
mixed change: valid partial commands remain in the JSON for diagnosis, but
callers must check status and the exit code before execution.

```json
{
  "schema_version": 1,
  "root": "/checkout",
  "status": "ready",
  "coverage": "suites",
  "selection": {"changed_files": ["native/src/ui.rs"], "tests": [], "summary": {}},
  "commands": [{
    "argv": ["cargo", "test", "--manifest-path", "./native/Cargo.toml"],
    "cwd": ".",
    "language": "rust",
    "reason": "whole Cargo manifest and reverse path dependencies (includes inline tests)"
  }],
  "diagnostics": ["unmapped: native/src/ui.rs"]
}
```

`ready` means the commands were resolved, **not that tests passed or that
selection coverage was proven**. `incomplete` means some ownership/configuration
could not be resolved; `no_changes` means an empty input diff; `no_tests` means
only recognized documentation/assets remained. Coverage is `selection`,
`suites`, or `unknown`. Retain diagnostics even on a ready suite plan.

Run each argv directly, with working directory `root/cwd`, and aggregate failures.
Do not join argv with spaces. The `exec` rendering is POSIX shell syntax, including
quoted `(cd ... && ...)` for nested packages. JSON argv/cwd is the cross-platform
contract; Windows clients should use their native process/package-manager launcher.

The default planner uses:

| Stack | Scope and command |
|---|---|
| Rust | Nearest `Cargo.toml` and reverse in-repository Cargo path dependencies; `cargo test --manifest-path ...`, `--workspace` for workspace manifests. No `.rs` path/name filters. |
| .NET | Actual `.csproj`/`.fsproj`/`.vbproj` ownership, test SDK/framework evidence and transitive `ProjectReference` closure; one `dotnet test ./actual.csproj` per test project. |
| JavaScript/TypeScript | Nearest package and test config; declared Vitest, Jest or Playwright dependency; package manager from `packageManager`/lockfiles. Vitest runs once, Jest watch is disabled. No unconditional Jest or implicit npx installation. |
| Go/Python/Ruby/Elixir/JVM | Existing precise selectors when the selection has no reported gaps and an owning manifest is known; otherwise whole owning manifest suites. |
| PHP/Dart/Swift | Whole manifest-backed suites through the existing language adapters. |

Nested JS configurations remain separate suites (for example desktop versus
renderer). Simple script arguments such as `vitest --environment=jsdom` are
preserved. Several recognized configs in one directory produce one suite per runner.
Ambiguous dependency-only runners, shell wrappers, dynamic .NET references,
solution/props ownership and unusual manifest/configuration conventions can
require explicit mappings. Detection does not evaluate JS config or MSBuild.
External runtime services may still be needed to *run* a resolved suite.

`--fallback=fail` stops an unproven selection; `full` (default) allows manifest
suite planning; `none` permits the partial selection but does not authorize an
invented runner. Paths output cannot represent suite fallback and exits nonzero
when fallback is needed. Automatic execution refuses a nonempty filtered
selection when suite planning could widen it; inspect the plan or provide an
explicit `--test-cmd`. A fully caller-filtered selection retains the existing
empty-result behavior. Plans explicitly warn that selection filters do not narrow
whole suites. A plan is always a description, not a test pass.

`--test-cmd`/`--runner` remains an explicit argv override for the legacy exec/run
interface; selected paths are appended using the runner's established conventions.
It is not accepted with `--format plan`. Prefer suite mappings for monorepos.
`run --timeout 2m` bounds explicit execution. Arguments after `--` are runner
arguments for exec/run; use only arguments meaningful to every planned runner.

## Project configuration

Optional **`.witness.json` at the Git root** is the single Witness configuration.
Witness previously had CLI flags only. Rivet's `.rivet/config.yaml` controls its
MCP registry/context, not test-suite ownership; do not duplicate these rules there.
See [the configuration schema](schemas/witness-config-v1.json) and
[monorepo examples](docs/project-configuration.md).

```json
{
  "schema_version": 1,
  "suites": [{
    "name": "web-unit",
    "paths": ["web/src/**", "web/package.json"],
    "tests": ["web/tests/**"],
    "exclude": ["web/src/generated/**"],
    "cwd": "web",
    "argv": ["yarn", "run", "vitest", "run"],
    "fallback": "suite"
  }]
}
```

All matching suites are included; explicit matches take precedence over detection
for that changed file and its associated ranked tests. `paths` and `tests` are
repository-relative globs; `*` matches one segment, `**` any number of segments.
`exclude` narrows that rule only: an excluded changed file still needs detection
or another rule. It cannot disappear from the plan. `fallback: suite` (default)
uses argv as the whole suite, with no appended paths; `fail` reports that the
suite requires manual selection. Unknown/custom executables are allowed because
they are explicit configuration, and are labelled `language: custom` with the
suite name in `reason`. Their existence and behavior are not inferred.

Names must be unique; cwd must exist inside the repository (including after
symlink resolution); patterns, argv, fields, version and fallback policy are
validated. Invalid config fails the plan, even if its rule would not match.
No configuration hooks run. Config affects plans; `select`'s ranked test list
continues to report Recon evidence rather than fabricated test-file mappings.

## Go API

```go
w, err := witness.New(root, witness.WithCacheDir(cacheOutsideProject))
// handle err; defer w.Close()
selection, err := w.Select(files, witness.DefaultOptions())
plan, err := w.Plan(selection) // inspect status, diagnostics, argv and cwd
// w.Run(ctx, selection, stdout, stderr) is explicit execution.
```

`Commands` preserves its `[][]string` return type, but refuses commands requiring
a non-root cwd; migrate those callers to `Plan`. `FullSuitePlan` enumerates
manifests instead of picking a dominant language. `FullSuiteCommand` similarly
refuses cwd loss or unresolved manifests. Whole-repository discovery may include
example projects: prefer changed-file plans or explicit mappings. `Run` uses the
same planner and preserves cwd, returns test failures as exit codes, and uses
errors for planning/launch/cancellation failures. The embedded CLI exports
`TestsFailed(err)` for hosts to preserve runner exit codes.

## Verification and recurring audits

```sh
make test
make vet
make build
witness audit audits/representative.json --root /path/to/Work
```

`audit` checks expected selected tests, required argv/cwd and plan status for
repeatable cases. It measures cold and warm planning, creates temporary indexes,
and never executes test commands. The shipped cases cover Workspacer Rust/TS,
Leroy .NET, and Cassadol web, C# backend and mixed changes. No project settings,
credentials, test services or live app processes are needed.

The manifest walk skips hidden/dependency/build trees, does not follow symlinked
directories, caps scanning at 200,000 entries and manifest reads at 2 MiB.
Failure to inspect a needed owner is visible, not an empty successful plan.

Rivet consumes tagged Witness releases. A sibling commit does not update an
installed Rivet binary. Review combined changes with a temporary Go workspace;
after release, bump Rivet's module pin and rebuild through the normal delivery
process. Do not install or restart a live MCP server merely to test a plan.
