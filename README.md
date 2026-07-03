# Witness

Smart test selection based on what actually changed.

Running the whole test suite on every change is slow. Guessing which tests matter is error-prone. Witness looks at your changed files, walks the dependency graph to find what they affect, and hands back a ranked, scored list of the tests worth running — and the command to run them.

Witness is the test-selection layer of the [Rivet](https://github.com/djtouchette/rivet) ecosystem. It runs standalone as a CLI, and Rivet embeds it to expose `witness.select`, `witness.run`, `witness.staged`, and `witness.since` as MCP tools. It builds on [recon](https://github.com/djtouchette/recon) for the underlying dependency and co-change analysis.

## What It Does

- Maps changed files to relevant tests via the dependency graph
- Scores by distance: **direct test (1.0) > 1-hop import (0.8) > 2-hop (0.5) > 3+-hop (0.3) > co-change pattern**
- Boosts tests that touch hotspot (high-risk) code
- Stops traversing at high-fan-out boundaries (>100 importers) so a shared utility doesn't drag in your entire suite
- Knows how to run **Go, Elixir, Python, Ruby, Node, Rust, and .NET/C#** test frameworks
- Auto-detects changes from git (working tree, staged, or since a ref) when you don't pass files

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

## Usage

```
witness select [files...]   # print the selected tests (json / paths / exec)
witness run    [files...]   # select and execute them, exiting with their code
```

If no files are given, witness detects changes from `git diff`. `select` output is JSON by default, with per-test scores and the signals that selected each one. `run` detects the test runner (go test, mix test, pytest, dotnet test, ...), streams its output, and exits with the runner's exit code — so it drops straight into a pre-commit hook or CI step.

### Flags

Both `select` and `run` share these; only `select` has `--format`.

| Flag | Default | Description |
|------|---------|-------------|
| `--format` | `json` | (`select` only) `json` (scored detail), `paths` (one path per line), or `exec` (runnable test command) |
| `--depth` | `2` | How many import hops to traverse backward |
| `--min-score` | `0.1` | Drop tests scoring below this |
| `--max` | `50` | Cap on number of tests returned |
| `--kind <k>` | | Only return tests of these kinds: `unit`, `integration`, `e2e`, ... (repeatable) |
| `--exclude <glob>` | | Drop test paths matching a glob, e.g. `vendor/**` (repeatable) |
| `--co-change-min` | `2` | Minimum co-change count before a co-changed test counts |
| `--fan-out-cap` | `100` | Don't expand files with more importers than this |
| `--staged` | | Use `git diff --staged` (great for pre-commit hooks) |
| `--since <ref>` | | Use `git diff <ref>...HEAD` (great for PR review) |
| `--cache-dir <path>` | `.witness/` | recon cache directory |

### Common patterns

```bash
# Pre-commit: run only the tests for what you're about to commit
witness run --staged

# PR review: everything affected since main
witness select --since main --format paths

# Tighter selection: direct + 1-hop only
witness select --depth 1 --min-score 0.5

# Unit tests only, skipping vendored and generated tests
witness run --kind unit --exclude 'vendor/**' --exclude '**/generated/**'
```

## How Scoring Works

For each changed file, witness:

1. **Direct match** — if the file *is* a test, or has a known test, include it (score 1.0).
2. **Reverse dependency walk** — BFS backward through the import graph ("who imports this?") up to `--depth`, scoring by hop distance. Files with more importers than `--fan-out-cap` (default 100) are treated as fan-out boundaries and not expanded, so framework/utility files don't explode the result set.
3. **Co-change history** — tests that have historically changed alongside this file, scored by frequency.
4. **Hotspot boost** — if the changed file is high-risk (high fan-in × churn), nudge its candidate tests up.

Tests are then filtered by `--min-score`, sorted by score, capped at `--max`, and returned with the signals and source files that selected each one.

## Library Use

The selector is available as a Go package (`github.com/djtouchette/witness/pkg/witness`):

```go
w, _ := witness.New(".")
defer w.Close()
opts := selector.DefaultOptions()
res, _ := w.SelectStaged(opts)   // or Select(files, opts), SelectSince(ref, opts)
```

The full CLI command is also importable (`github.com/djtouchette/witness/pkg/embedded`) for embedding into another binary — which is how Rivet wires it in.

## Building

```bash
make build       # build to ./witness
make test        # run tests
make vet         # static analysis
make install     # go install
```

Requires Go 1.25+. The compiled binary and the `.witness/` cache are gitignored.

## License

MIT
