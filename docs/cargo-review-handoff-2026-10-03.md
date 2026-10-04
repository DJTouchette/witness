# Cargo identity repair and deterministic Rivet CI handoff

This supersedes the outstanding Cargo/doctor release blockers in the independent
review at `/tmp/witness-final-review-YkBKPb/review.md`. Parent still owns fresh
independent review and the separate release decision.

## Source and evidence

Witness source: `0c248bccb7130bcd99de0b40b80f5221336f02f3`, branch
`wks/astra-reliable-witness`, worktree
`/home/djtouchette/.workspacer/worktrees/witness/astra-reliable-witness`.
Repair base: `8f825418efb2e883eed48c184442422e4c181ebe`.
Implementation commits: `9b5e4d9` and `0c248bc`; subsequent commits record evidence.
A fresh fetch found origin/master unchanged at ancestor `c1abc74`; no integration
was needed. Shared worktrees and pre-existing untracked review notes/skills remain
untouched.

Rivet source: `62e4a6ee609b8a1b2463d0e3dc6c92582fd63e9d`, branch
`wks/astra-reliable-witness-across-projects`, worktree
`/home/djtouchette/.workspacer/worktrees/rivet/astra-reliable-witness-across-projects`.
Its repair base is `3b607ea7cc6c665cf34871785e76348b133e9dce`; origin/master was
unchanged at ancestor `424540d`. No production doctor change was necessary.

Detailed logs are in `/tmp/witness-cargo-ci-repair/`. Portable audit outcomes are
in `audits/verified-2026-10-03-cargo.json`.

## Physical identity proof

`internal/planner/rust.go` uses the shared repopath boundary checks and native
filesystem identity for crate directories, owners, dependency targets and workspace
inheritance. Directory aliases collapse to one graph node and one command. A
symlink repository root is scanned through its physical directory. Commands remain
repository-relative and retain a usable Cargo.toml path.

Two offline Cargo probes establish distinctions that string normalization alone
cannot prove:

- A Cargo.toml symlink in a different crate directory still uses that directory's
  source and dependency paths. The planner keeps both suites; changes to the shared
  physical manifest propagate to all aliases.
- Cargo normalizes dependency dot-dot components lexically before resolving links.
  The planner matches that target, but also checks the original physical traversal
  so an outside-root hop or unresolved component cannot disappear behind dot-dot.

`TestCargoAliasPathSemanticsOffline` runs both contracts against actual Cargo.
Planner tests additionally cover chains, changed aliases/physical paths, duplicate
changes, root aliases, workspace inheritance/root packages/explicit workspaces,
reverse graph cycles, missing/dangling/looping links, unscanned dependencies,
absolute/drive paths and case mismatches. Unknown edges yield incomplete/unknown
coverage and diagnostics, retaining known commands. Production planning contains no
os/exec calls; the executor and shared boundary policy are unchanged.

## Regression evidence and gates

The new tests were run against an archive of exact old Witness HEAD before the
repair. `cargo-public-before.log` records the API losing app and actual Witness run
returning zero while only core runs. `cargo-audit-before.json` fails both reusable
alias cases. The corrected tests/audit pass. `cargo-public-after.log`,
`cargo-semantics-after.log` and the full suite logs record the fixed behavior.
The tiny fixture's core suite passes; app's dependency test fails with Cargo 101;
Witness now returns 101. Changing only the fixture's value makes Witness pass.
Public API, actual CLI plan/exec and audit retain both targets without executing
the fixture's build-hook sentinel. Direct dependency paths are a passing control.

All Go checks use `env -u GOROOT GOTOOLCHAIN=go1.25.7` (inherited GOROOT names a
different installation):

- Witness full Go tests, full race suite, vet, build, gofmt and diff checks pass.
  Existing goldens and physical executor escape/swap regressions are included.
- Rivet full Go tests, full race suite, vet and build pass with `GOWORK=off` and
  with the scratch `go.work` composing this Witness. Neither run uses `-skip` or
  an environment override to conceal the doctor cases.
- Both Rivet binaries pass strict context lint (11 docs). The local HTTP doctor
  fixtures test the real Ollama request/response shape, matching semantic.Store
  data, mismatched host/model/dimension, corrupt indexes, unavailable endpoints
  and missing models. t.Setenv/t.Chdir restore process state; tests are sequential,
  request counts are atomic, servers close in cleanup. Generic Run fixtures
  explicitly select lexical mode rather than inheriting a developer's backend.
- Isolated Rivet serve MCP sentinels pass: pinned smoke 2 calls, composed plan
  smoke 4 calls; both report runner_executed=false. No live MCP was contacted.
- Seven representative audits retain the reviewed outcomes/counts: Workspacer
  native ready/1; Leroy ready/10; Cassadol web ready/1; Cassadol backend
  incomplete/19; mixed Cassadol incomplete/20; renderer incomplete/1;
  mixed Workspacer incomplete/2. Four prior edge audits and both Cargo alias
  cases pass. Independent XML checks retain all real .NET target identities.
- Installed .NET scratch manifest-only evaluation and local fake npm runner pass
  offline. No real application suite, database, package install or service ran.

Principal artifacts: `witness-full.log`, `witness-race.log`, `witness-vet.log`,
`doctor-before.log`, `doctor-after.log`, `rivet-{pinned,composed}-{full,race,vet}.log`,
`rivet-{pinned,composed}-lint.log`, `mcp-{pinned,composed}.json`,
`representative.json`, `edge-audit.json`, `cargo-audit-{before,after}.json`,
`dotnet-targets.json`, `toolchain.log`, `rivet-{pinned,composed}-metadata.txt`.

## Limits and release handoff

Linux runtime verification only; macOS/Windows runtime and release cross-builds
remain separate CI/release work. Native path casing is respected, not lowercased.
The existing bounded discovery exclusions remain; unscanned local targets cannot
claim complete coverage. Whole-repository discovery may still diagnose a generic
directory symlink as an unowned change; this repair proves changed-file Cargo
ownership and reverse closure. Path validation is not a hostile concurrent
filesystem sandbox. Known incomplete project audit outcomes remain incomplete.

No push, merge, tag, publication, installation, global service/tool change,
shared application configuration/index mutation or credential inspection occurred.
Rivet go.mod/go.sum and internal/witness.PinnedVersion still pin v0.5.0. Build
metadata proves the pinned binary uses v0.5.0 and the composed binary uses (devel).
Composed success does not update an installed binary or a live MCP process.

After parent review, the separately authorized release task must check remote
versions, release Witness through its existing tag workflow after green CI, then
update Rivet go.mod/go.sum and PinnedVersion together to that actual released
version. Commit no absolute replace/go.work. Re-run unqualified full pinned gates,
version/help/metadata checks, representative audits and MCP sentinel checks; publish
Rivet through its existing workflow, then verify packaged checksums and embedded
module identity. No version has been selected or release approved in this repair.
