# Cargo override repair: ready for independent source review

Repair base: Witness `cc84ebbee31d3ff2cf17ad0f04239d4182650eed`.
Implementation: `d24c201` followed by source/audit hardening
`23f4c5668639061f85f16eb8dab5c789c0ddd7db`.
Branch: `wks/astra-reliable-witness`; worktree:
`/home/djtouchette/.workspacer/worktrees/witness/astra-reliable-witness`.
Subsequent commits record evidence only. Fresh `git fetch origin` before edits
found origin/master `c1abc74e40facd7956444e79109330cc60a67139` still an ancestor.
All pre-existing untracked notes and skills remain untouched.

Rivet is unchanged at `70a31da0d7873abdfb84f831297241a73eab3ac9` in
`/home/djtouchette/.workspacer/worktrees/rivet/astra-reliable-witness-across-projects`.
Its released Witness pin remains **v0.5.0**. Composition uses only
`/tmp/witness-override-repair/go.work`; no replace/go.work/pin change was committed.

## Repair and public contract

The P1 omission is fixed by validated local patch targets plus a conservative
fallback to every discovered Cargo manifest, including workspace roots. This
covers renamed packages, version-only dependencies, non-crates registry/URL
patches, standalone manifests, dev/build/target combinations and inheritance.
Crucially, matching direct names/versions would miss transitive consumers hidden
inside external registry packages. The fallback deliberately does no such pruning.
Each command's reason explains the wider scope. Existing physical directory vs
manifest aliases, lexical dot-dot and strict boundary checks remain in use.

The exact workspace fixture emits **three** commands: workspace `--workspace`,
app and core. The review's two-command expectation must be updated for this
intentional superset, not treated as a missing-dependent regression. A standalone
package patch emits the two package commands. No package resolver, Cargo metadata,
download, restore, build hook or configuration code runs during planning.

Unsupported graph declarations remain visible: `[replace]`, nonlocal patches,
unknown top-level/workspace/target/dependency keys, malformed dependency fields,
unresolved targets, and repository `.cargo/config{,.toml}` overrides (`paths`,
`patch`, `source`, `include`, `unstable`, unknown keys) return incomplete/unknown
with a named diagnostic and retain known commands as evidence. `Plan` returns an
error; `Commands` refuses; `Run` returns -1 plus error without launching commands.
CLI plan emits partial JSON and nonzero; CLI exec emits no commands and nonzero.
Schema/public signatures are unchanged. See [coverage and migration](planner-corrections.md#cargo-override-coverage-and-migration).

## Independent failure-preservation evidence

All logs and scratch binaries: `/tmp/witness-override-repair/`.
The `before/` archive was made from exact `cc84ebb`, with only new regression tests,
fixture and audit copied in. `prior-head.log` fails the API target assertion and
proves old Witness run succeeds while omitting the failing app. `witness-before`
was built from that archive with `GOWORK=off`.

`proof.py`, `proof-results.json`, `before-{plan,exec,audit,run}.log`,
`after-{api,plan,exec,audit,run,corrected}.log`, `cargo-{core,app}.log` and
`composed-run.log` record actual outcomes:

- Old API/plan/exec: ready, core only; new: ready, workspace + app + core.
- Old reusable patch audit fails; fixed audit passes both cases.
- Offline Cargo core: 0; app: 101 with `observes_core_value ... FAILED`.
- Old Witness run: 0; fixed Witness run: 101; composed Rivet: 1 with test failure.
- Changing only core's value to 1 makes fixed Witness run return 0.
- Both planner Cargo-process and build-script sentinels stay absent during
  planning. Build script runs only during separately authorized scratch execution.
- `standalone-{api,core,app,witness}.log` independently confirms renamed dependency
  and patch package identities in a standalone manifest, including failure 101.
- Non-crates patch coverage is tested statically, without contacting a registry.

Portable cases: `audits/cargo-patch.json`, `audits/cargo-unsupported.json`.
The reusable e2e tests exercise public API, actual CLI, audits and real offline
Cargo; planner tests cover source kinds, inheritance/conditional dependency forms,
version mismatches, indirect consumers, malformed/new forms and boundaries.
`toolchain-and-boundaries.log` has no skipped Cargo/.NET/npm target checks.

## Final gates and real-project audits

All Go commands used `env -u GOROOT GOTOOLCHAIN=go1.25.7`, because the inherited
GOROOT does not match the launcher. `witness-gates.json` and
`composed-gates.json` record full tests, full race, vet, all-package and scratch
binary builds, gofmt (empty output), diff check and GoReleaser check: PASS.
Witness full/race included installed Cargo and opt-in scratch .NET manifest-only
and local fake npm argv/cwd checks. Rivet tests used no doctor exclusion or
backend suppression; `doctor.log` separately proves all deterministic cases ran.
`context-lint-final.log`: 11 docs, no issues. `mcp-composed-final.json`: 4 isolated
MCP calls pass, runner_executed=false. Metadata names Witness `(devel)` as expected
for composition; this does not validate a future released pin.

The installed GoReleaser binary was used directly because its mise shim had no
configured version. No tool installation or global configuration change occurred.

All **13 previous audit cases** pass after correcting two stale Workspacer
expectations, and the **3 new override cases** pass. The seven real targets are:

| Case | Status / coverage | Commands |
|---|---|---:|
| Workspacer native | incomplete / unknown | 7 |
| Leroy billing | ready / suites | 10 |
| Cassadol web | ready / suites | 1 |
| Cassadol backend | incomplete / unknown | 19 |
| Cassadol mixed | incomplete / unknown | 20 |
| Workspacer renderer | incomplete / unknown | 1 |
| Workspacer mixed | incomplete / unknown | 8 |

Workspacer's manifests patch gpui-component and portable-pty under excluded vendor
paths. Old planning silently ignored these declarations. The updated native and
mixed cases require those specific uncertainty diagnostics and every retained
Cargo command. The previous native ready/1 is **not** preserved as a false claim.
`representative.json` retains the initial old-expectation failure;
`representative-updated.json` records corrected passing expectations.
`composed-representative.json` repeats them through Rivet. `edge-audit.json` and
`alias-audit.json` retain all six original edge/alias cases. Independent XML
verification in `dotnet-targets-updated.json` retains actual .NET target evidence.
Audits use temporary indexes and do not run real application suites.

## Bounds and release handoff

This intentionally trades precision for safe coverage: a local patch anywhere in
bounded discovery widens all Cargo changes to all discovered suites, potentially
running packages twice via workspace and direct manifest commands. Unknown or
excluded patch targets remain incomplete; no weak semver/source/name heuristic
claims completeness. Ambient Cargo home/parent/environment/CLI configuration,
external graphs and excluded independent projects are outside static coverage.
Explicit mappings must cover every affected suite. This is not a full Cargo
resolver or a hostile concurrent-filesystem sandbox.

Linux runtime only. macOS/Windows runtime and release cross-build assets still
need CI/delivery verification. No merge, push, tag, release, install, live MCP,
credential inspection, global service, shared application source/config/index
mutation, real app suite, database, cloud or provider start occurred.

Parent must review this source freshly before authorizing delivery. Likely versions
remain Witness **v0.6.0** and composed Rivet **v0.21.0**, but the release agent must
re-query remote tags before choosing them. Then require green supported-branch CI,
publish Witness via its existing workflow, update Rivet go.mod/go.sum/PinnedVersion
together to the actual published version in an authorized isolated checkout, and
rerun full released-pin gates plus all audits and the isolated MCP sentinel.
Update release notes (current changelog's Unreleased 0.5.0 heading is stale) and
embedding context examples. Confirm module metadata has the released version and
no local replacement. Publish Rivet only after pin review, then verify all six
assets, packaged Linux checksum/version/metadata/MCP behavior. Installation and
live MCP restart are separate delivery actions. This repair publishes nothing.
