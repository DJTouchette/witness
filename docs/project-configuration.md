# Suite configuration examples

These are examples for project-owner review, not files installed into the three
shared repositories. Add other suite mappings appropriate to your project before
treating a broad pattern as full coverage. Planning never executes these argv.

## Rust and TypeScript monorepo (Workspacer shape)

```json
{
  "schema_version": 1,
  "suites": [
    {"name":"native","paths":["apps/native/**"],"cwd":".","argv":["cargo","test","--manifest-path","./apps/native/Cargo.toml"]},
    {"name":"hub","paths":["services/hub-rs/**"],"cwd":".","argv":["cargo","test","--manifest-path","./services/hub-rs/Cargo.toml"]},
    {"name":"renderer","paths":["apps/desktop/src/renderer/**"],"cwd":"apps/desktop/src/renderer","argv":["npm","exec","--no","--","vitest","run"]},
    {"name":"desktop-main","paths":["apps/desktop/src/main/**"],"tests":["apps/desktop/tests/**"],"cwd":"apps/desktop","argv":["npm","run","test:main"]}
  ]
}
```

Nearest manifests/configs autodetect native, hub and renderer already. Explicit
mappings are useful for narrowing source ownership among several test configs and for
shared protocol changes that must trigger additional suites. Add the shared
source glob to *each* affected suite. Configured rules take responsibility for
cross-suite dependencies; they do not silently add inferred runner commands.

## C# backend plus Vitest (Cassadol shape)

```json
{
  "schema_version": 1,
  "suites": [
    {"name":"data-layer","paths":["backend/Handlers/DataLayer/**"],"cwd":".","argv":["dotnet","test","./backend/Handlers/DataLayer/test/DataLayer.Tests/DataLayer.Tests.csproj"]},
    {"name":"web","paths":["clients/web/**"],"cwd":"clients/web","argv":["yarn","run","vitest","run","--environment=jsdom"]},
    {"name":"infra","paths":["infrastructure/**"],"cwd":"infrastructure","argv":["yarn","run","jest","--watch=false"]}
  ]
}
```

Autodetection uses actual ProjectReference closure and resolves multiple dependent
test projects. A hand-written narrow mapping replaces that inference for matching
changes: include every affected project, or leave those changes to autodetection.
C# integration suites may require a database/cloud environment at execution time.
No such environment is needed to obtain or review these plans.

Leroy's ordinary .NET source/test projects also autodetect. For solution-level
changes or imported MSBuild property conventions, list the agreed test projects
as separate suites sharing patterns such as `backend/Directory.Build.props` and
`backend/Leroy.sln`. Use repository tools' documented argv where they supply
required configuration; do not infer credentials from appsettings.

## Unusual layout and manual fallback

```json
{
  "schema_version": 1,
  "suites": [
    {"name":"contract-checks","paths":["schema/**"],"tests":["checks/contracts/**"],"exclude":["schema/docs/**"],"cwd":"checks","argv":["./project-check","contracts"],"fallback":"suite"},
    {"name":"manual-migration-check","paths":["migrations/**"],"cwd":".","argv":["./project-check","migrations"],"fallback":"fail"}
  ]
}
```

Exclusions narrow a rule rather than globally ignoring files. An unmatched
`schema/docs/change.sql` still needs another owner or produces an incomplete
plan. `fallback: fail` blocks automatic resolution for its matching changes;
it never converts them to a green empty selection. Review configuration like
source code: an explicit execution command can do anything its argv requests.
