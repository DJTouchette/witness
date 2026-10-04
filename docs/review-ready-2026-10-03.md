# Witness planner corrections: review handoff

Source checked: `3318c90008d1a9e68a94a84e2ddfb5d56867ba91`, on
`wks/astra-reliable-witness`. The final handoff commit only records evidence.
Start of this repair: `814b2c33151ff38a92fff9600b3c6de98f46e70a`;
upstream baseline: `c1abc74e40facd7956444e79109330cc60a67139`.
A fresh fetch confirmed origin/master remains that ancestor; no integration was
needed. Worktree:
`/home/djtouchette/.workspacer/worktrees/witness/astra-reliable-witness`.

## Findings and changes

1. WholeRepository/FullSuitePlan accounts for legacy manifests, configured suites
   with no files, unsupported owners and mixed source trees. Go+Python emits both
   suites; unsupported/config-only owners make coverage unknown. Public API and
   CLI no-argument/diff/fallback tests cover these contracts.
2. Package/lock changes include independently owned nested standard JS configs
   without history rescue. Package boundaries, nonstandard configs and workspace
   declarations are explicit. Jest/Vitest suites remain distinct.
3. Bounded offline XML inspection follows imports and Directory.Build files,
   unions literal conditional references, and retains all known test targets.
   Missing/cyclic/dynamic/custom imports and references make completeness unknown.
   Reference identity is normalized for in-repository symlinks; unscanned targets
   are diagnosed. The reviewed imported-reference repro now retains both targets.
4. Physical containment is shared by planning, config validation, runner source
   reads and execution. Tests prove outside/new-leaf symlink refusal, manifest
   boundary refusal, a post-plan alias swap refusal, and permitted inside aliases.
   A real scratch shell sentinel proved the old executor ran outside; the fixed
   executor refuses before launch.
5. Custom/non-direct scripts, lifecycle hooks, quoted/narrowing flags, multiline
   shell scripts and expansion require explicit mappings. Direct simple scripts
   preserve supported options. A mapped npm wrapper preserves argv and appended
   arguments with spaces. No arbitrary shell parsing is attempted.

The initial failing regressions are recorded in `/tmp/witness-fixes-evidence/`:
`boundary-before.log`, `node-before.log`, `repository-before.log` and
`dotnet-before.log`. These are failures against the old behavior, not merely
new tests added after a successful implementation. Original review fixtures and
findings remain at `/tmp/witness-independent-review-CvQiTd/`.

## Verification and evidence

All Go commands used `env -u GOROOT GOTOOLCHAIN=go1.25.7` because inherited GOROOT
names another installation. Final source checks:

- Full Witness `make test vet build`, golden regressions included: pass.
- Full `go test -race ./... -count=1`: pass.
- `gofmt -l .` empty; `git diff --check`: pass.
- Installed SDK scratch imported-reference evaluation and offline local fake npm
  runner: pass. No real project MSBuild evaluation/restore/build/test was run.
- Original four-case edge audit: all pass, including both imported test projects,
  nested renderer config, wrapper refusal, and symlink refusal.
- All seven strengthened representative audits: pass. Assertions include coverage,
  expected diagnostic text, exact command count and every independently reviewed
  .NET target. All targets also pass `scripts/verify-dotnet-audit.py`, which reads
  their actual XML test evidence and traces each literal source-reference chain.
- Rivet at unchanged `3b607ea7cc6c665cf34871785e76348b133e9dce`, composed through
  `/tmp/witness-fixes-evidence/go.work`: all tests except the documented baseline
  doctor group pass; vet/build pass. Strict context lint passes for all 11 docs.
- Actual isolated composed Rivet MCP JSON-RPC: four calls, ready mixed plan and
  incomplete unknown owner both preserved, `runner_executed=false`. Tagged-pin
  smoke: two calls, also no runner execution. Rivet's module/version pin remains
  v0.5.0; no local replace or absolute workspace path is committed.

Final logs/artifacts under `/tmp/witness-fixes-evidence/`:
`witness-final.log`, `witness-final-race.log`, `gofmt-final.log`,
`toolchain-final.log`, `edge-audit.json`, `representative-final.json`,
`dotnet-real-targets-final.json`, `rivet-composed-final.log`,
`rivet-vet-final.log`, `rivet-context-lint.log`, `mcp-final.log`,
`mcp-pinned.log`. Compact portable audit results are committed in
`audits/verified-2026-10-03-corrections.json`; regenerate the full results using
the commands documented in `docs/planner-corrections.md`.

| Case | Coverage result | Commands |
| --- | --- | ---: |
| Workspacer native | ready | 1 |
| Leroy billing | ready, all actual targets | 10 |
| Cassadol web | ready | 1 |
| Cassadol backend | incomplete: unresolved legacy external imports | 19 |
| Cassadol backend + web | incomplete: same graph uncertainty | 20 |
| Workspacer renderer | incomplete: compound desktop script | 1 |
| Workspacer Rust + renderer | incomplete: same script uncertainty | 2 |

Incomplete cases are acceptance successes because they preserve known targets
and name missing proof; they are not executable complete gates. Shared project
configuration was not altered to hide these gaps. Observed timings are in the
compact audit artifact, not an SLA. Discovery/import bounds and migration examples
are in `docs/planner-corrections.md` and the version 1 schema descriptions.

Read-only source identities: Workspacer `bbe684281c42ad8d41b054d341b0b3305d136b7e`,
Leroy `696363b07dbad425c0e8d752ea515bfb842dda7d`,
Cassadol `0ea0cb29fc7899625aaad8d871dbc7c0ca77e493`.
Audits use current working-tree content and temporary indexes.

## Remaining release disposition

Parent owns fresh review and publication. This task did not merge, push, tag,
release, install, change Rivet source/pins, restart live MCP, edit shared project
settings/indexes, read credentials, execute real application suites, start Ollama,
or update global tools. Pre-existing untracked reviewer learnings, .agents and
.workspacer material remain intact. One owning-repository learning was added.

The unchanged Witness JVM formatting CI failure is fixed separately in
`676ca08` (gofmt only). No known Witness Linux gate remains failing. Windows and
macOS runtime checks were not run here; parent must follow cross-platform CI.
Path checks are not a hostile concurrent-filesystem sandbox.

Rivet full-suite baseline constraint is unchanged: at exact baseline
`424540db1d29df0d8ab3bf94773866a44cfece15`, the independent review reproduced
`TestSemanticIndexStates/index_and_backend` and
`TestSemanticIndexStates/backend_but_no_index` failures because configured Ollama
`localhost:11434` was not answering (connection refused). See original
`rivet-baseline-doctor.log` and `rivet-pinned-tests.log`. This repair's composed
suite explicitly skips `^TestSemanticIndexStates$`; that skip is not a claim of
full Rivet CI success. No doctor repair or service startup was attempted.

Read the original review's release handoff before publishing. Parent must approve
these fixes, disposition the existing Rivet CI prerequisite, choose fresh remote
versions, publish Witness through its existing v-tag workflow, then update Rivet's
module pin and PinnedVersion together. Recheck the released pin with GOWORK=off,
packaged build metadata, checksums and the MCP sentinel smoke before publishing
Rivet. No release command was executed during this repair.
