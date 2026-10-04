# Planner coverage corrections

The plan and configuration schemas remain version 1. Existing argv/cwd fields
and public method signatures are unchanged. Some plans previously marked ready
now return `incomplete`, `coverage: unknown`, diagnostics, and an error/nonzero
CLI exit. Partial commands are evidence for review, never authority to execute
an incomplete plan as though all owners were covered. `Witness.Run` refuses it.

## Discovery and ownership

`FullSuitePlan` examines every potentially test-affecting file in the bounded
repository scan, not only Go/Cargo/npm/.NET manifests. Python (`pyproject.toml`,
`pytest.ini`, `setup.cfg`, `setup.py`), Ruby, Elixir, JVM, Swift, PHP and Dart
manifests resolve through their established adapters. Unsupported/config-only
owners remain incomplete unless explicitly mapped. Configured suites are included
even when their source/test globs match no current file. `fallback: fail` remains
a refusal, including whole-repository requests. Empty discovery is unknown.

The scan skips hidden directories and node_modules/vendor/target/bin/obj/dist/build,
but does not equate an unrecognized visible source file with documentation.
Hidden source/generated trees need explicit suite mappings. Source fixtures and
unowned build files can therefore make a whole-repository plan incomplete;
changed-file planning is often more useful. No-argument CLI selection still means
the working-tree Git diff, not a whole-repository request. `--fallback=full` resolves
owning suites for that diff. Legacy `FullSuiteCommand` also refuses cwd loss.

JS package/lock changes enumerate independent standard Vitest/Jest/Playwright
configs owned by that package, including nested config directories. A nested
package.json is a separate owner. Root workspace declarations require explicit
mappings for workspace-wide manifest/lock changes; Witness does not guess which
packages share runtime state. Nonstandard config filenames require explicit
`--config` argv mappings. JS config code is never evaluated.

Only a direct script with a small set of non-narrowing options is inferred:
`run`/`test`, watch normalization, `--globals`, `--environment=...`, and
`--reporter=...`. Quoting, shell variables/operators, custom wrappers, positional
filters, project/config/workspace flags, pretest/posttest and runner-name script
collisions require an explicit mapping. This avoids discarding setup, environment
or script selection semantics. The planner does not parse arbitrary shell syntax.
For example, preserve a package's wrapper and lifecycle with a reviewed mapping:

```json
{"name":"package-tests","paths":["web/**"],"cwd":"web","argv":["npm","run","test","--"]}
```

The owner must verify that this script covers every mapped path and terminates
without watch mode. Configuration declares that contract; it does not prove it.
`select --format exec ... -- 'argument with spaces'` appends an intact argv
argument. JSON plan mode disallows passthrough to keep its declared contract
reviewable. No planning command installs packages or launches the script.

## Static .NET graph limits

The XML collector reads project files, nearest Directory.Build.props/targets,
and explicit imports. It supports literal properties, MSBuildThisFileDirectory,
MSBuildProjectDirectory, and filename-only import globs. Properties/imports are
collected before ProjectReference items. Includes in imported files remain
relative to the owning project; import paths are relative to the importing file.
Backslashes are normalized; missing or case-mismatched references fail closed
on case-sensitive filesystems. Conventional Microsoft.NET.Sdk/Web/Worker/Razor
projects are recognized; custom SDKs require mappings.

Conditions are not an MSBuild interpreter: literal references from all branches
are included as a superset. Conditional/unknown property values used by a reference
or import, missing imports, import cycles, unsupported wildcard directories,
property functions, item expressions, custom import properties and target-time
reference mutation produce incomplete coverage. Removing a reference only narrows
this static superset. Build tasks and external SDK/package-generated behavior
are not evaluated. Use explicit mappings for conventions outside this model.
No runtime SDK, restore, target invocation, arbitrary config evaluation, cloud
or service is needed. The opt-in SDK test evaluates only a scratch fixture offline.

Graph uncertainty is repository-wide because an unresolved import can hide a
reverse edge from any test project. Known affected targets remain in the partial
plan. In particular, Cassadol retains all 19 audited backend targets but its
legacy external MSBuild imports prevent a completeness claim. Leroy's 10 audited
billing targets remain ready. Mapping only one convenient test project would
replace inference and lose the other dependents: map every affected suite.

Bounds: 2 MiB per XML/manifest, 256 imported files per project, depth 32, property
expansion 32 substitutions, import glob 4096 directory entries. Ordinary discovery
is limited to 200,000 entries. Explicit import globs inspect their named directory
including hidden/build directories; they do not silently use discovery exclusions.
Limits and unsupported evaluation are diagnostics, not proof of absent references.

## Physical paths and execution

Changed paths, selected test paths, manifests, configured cwd and inferred cwd
must remain inside the canonical repository root. Existing symlink ancestors are
resolved even when a changed leaf is new/deleted. Cwd must exist. In-repository
symlinks are allowed; escaping links, dangling links, drive-qualified paths and
UNC/absolute relative-path inputs are refused. Source-reading runner helpers and
inline Rust inspection use the same boundary policy.

Execution revalidates cwd and uses the physical path, so swapping an alias after
planning cannot redirect a ready command outside the tree. This is not a sandbox
against concurrent hostile renaming of physical ancestors, nor confinement of a
trusted test command's own filesystem access. Embedders executing argv themselves
must apply the same checks. Runtime validation here is Linux; Windows/macOS remain
release CI/platform verification work. POSIX display strings are not PowerShell.

## Repeatable verification

```sh
witness audit audits/representative.json --root /path/to/Work > /tmp/audit.json
python3 scripts/verify-dotnet-audit.py /tmp/audit.json
```

Audit cases can require `expected_coverage`, `expected_diagnostics` (substrings),
and `expected_command_count`, as well as expected tests/argv/cwd/status. The cases
assert all 10 Leroy and 19 Cassadol targets, not only one representative target.
The independent XML verifier checks each target's test evidence and literal
reference chain back to the changed source owner without executing MSBuild.
Workspacer native and mixed cases remain incomplete/unknown because excluded
vendor patch targets cannot be resolved; they retain seven and eight commands,
respectively. Desktop cases also require a wrapper diagnostic. Audits create
temporary indexes and never execute application tests.

## Cargo physical identity

Cargo reverse path dependencies use validated physical crate directories, including
in-repository directory aliases, alias chains, existing path components and native
filesystem identity. Changed alias and physical paths produce the same deduplicated
suites. The manifest scan canonicalizes a symlink repository root. Local dependencies
that escape the root, are missing/dangling/cyclic, or point outside the bounded scan
produce incomplete coverage while retaining known commands. No Cargo metadata,
build script or runner is launched during planning.

A symlinked Cargo.toml in a *different directory* is not the same crate: Cargo uses
that containing directory for sources and relative dependencies. Such suites keep
their usable manifest paths. A change to the shared physical manifest affects all
those suites. Workspace inheritance (including package.workspace and a root package)
uses the same directory identities. Cargo lexically normalizes dependency paths
before resolving aliases; Witness also checks the uncleaned physical traversal so
`..` cannot erase an outside-root hop or missing component. Absolute/drive-qualified dependency paths are
not supported; use repository-relative paths or explicit mappings. Filesystem case
is native, not lowercased; Linux is the runtime-verified platform.

The reusable alias audit has a tiny offline fixture. To materialize it in scratch:

```sh
PROOF_DIR=$(mktemp -d)
cp -R internal/e2e/testdata/fixtures/rust-alias "$PROOF_DIR/repo"
(cd "$PROOF_DIR/repo" && git init -q && ln -s core core-alias)
witness audit audits/cargo-alias.json --root "$PROOF_DIR/repo"
```

`TestCargoAliasPublicPlanningAndAudit` checks the public API, actual CLI plan/exec,
both audit cases, direct-path control and a build-hook sentinel. The separate
`TestCargoAliasOfflineExecution` runs only this scratch Cargo fixture offline: core
alone passes, app fails with exit 101, Witness retains that failure, and correcting
the assertion makes the emitted suites pass. No real application suite is run.

## Cargo override coverage and migration

Local `[patch.<source>]` declarations now trigger the full discovered Cargo scope:
all package manifests and workspace roots, retaining their normal argv/cwd. The
command `reason` names this conservative fallback. It applies to workspace and
standalone package manifests, `crates-io`, other registry names, and URL sources.
Patch entries may rename their package with `package`; their local target must
identify that package. Targets use the same physical directory, lexical `..`,
symlink and scan-boundary checks as direct path dependencies.

This is deliberately a scope fallback, not a Cargo dependency resolver. Patches
apply transitively through registry packages that static repository inspection
cannot see. Filtering consumers by a direct dependency name, semver requirement,
registry identity, feature, or target condition could lose those consumers. The
fallback therefore keeps even apparently unrelated packages and inactive/version-
incompatible branches. It may run a package both via its workspace and directly.
Normal, dev, build, target-specific and workspace-inherited dependencies all remain
covered. Patch tables in non-root members also widen scope conservatively, even
when Cargo would ignore them. No lockfile resolution, metadata, download, restore,
build script or Cargo process runs during planning.

Unsupported forms are no longer silently discarded by TOML struct decoding.
`[replace]`, non-local patches, unknown top-level/workspace/target/dependency
fields, malformed dependency fields and unresolved local targets produce named
diagnostics. Known commands remain available as evidence, but `Plan` returns an
error with `status: incomplete` and `coverage: unknown`. Legacy `Commands` refuses;
`Run` returns an error and code -1 without executing. CLI plan prints the partial
JSON and exits nonzero; CLI exec emits no runnable commands and exits nonzero.
Repeated planning must preserve the same refusal.

Repository `.cargo/config` and `.cargo/config.toml` files at each discovered
crate's ancestors are inspected explicitly despite hidden-directory discovery
exclusions. Known `paths`, `patch`, `source`, `include`, `unstable`, and unknown
configuration declarations are unsupported and diagnose incomplete coverage.
Both filenames are inspected conservatively rather than simulating precedence.
Cargo credential files, home/ancestor configuration outside the repository,
environment overrides, CLI `--config`, custom build-generated dependencies and
external registry graphs are not read or evaluated. Completeness is scoped to
repository declarations and the existing bounded manifest scan; ambient overrides
and hidden/excluded independent crates require reviewed explicit suite mappings.
A ready plan is a coverage contract, not proof that Cargo can build successfully.

Migration: remove unsupported overrides, move a supported local override into a
manifest patch table, or map **all** affected suites with `.witness.json`. Mapping
just core would recreate the lost-dependent bug. API/schema versions and Rivet's
released pin are unchanged. Consumers must check the error/status before executing
partial commands. The additional workspace command is intentional: the tiny patch
fixture now has three commands rather than the original expected two.

Repeat the portable audits in a disposable copy (fixtures intentionally contain a
failing dependent test):

```sh
PROOF_DIR=$(mktemp -d)
cp -R internal/e2e/testdata/fixtures/rust-patch "$PROOF_DIR/repo"
git -C "$PROOF_DIR/repo" init -q
witness audit audits/cargo-patch.json --root "$PROOF_DIR/repo"
mkdir -p "$PROOF_DIR/repo/.cargo"
printf "paths=['core']\n" > "$PROOF_DIR/repo/.cargo/config.toml"
witness audit audits/cargo-unsupported.json --root "$PROOF_DIR/repo"
```

`TestCargoPatchPublicPlanningAndAudit` guards API, CLI plan/exec, the reusable audit,
physical aliases, and both Cargo-process/build-hook sentinels.
`TestCargoPatchOfflineExecution` proves core passes, app fails with Cargo 101,
Witness preserves that failure, and a corrected fixture passes.
`TestCargoUnsupportedOverrideRefusesExecution` tests API/legacy/CLI refusal and the
incomplete audit. The planner matrix also retains transitive consumers without a
matching direct package name, version-incompatible and optional dependencies,
renamed packages, standalone patches, registry/URL sources and strict boundaries.
Cargo's [override reference](https://doc.rust-lang.org/cargo/reference/overriding-dependencies.html)
describes the transitive patch behavior underlying this fallback.
