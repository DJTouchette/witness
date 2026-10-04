---
title: Cargo patches need a full static scope to include unseen transitive consumers
date: 2026-10-03
confidence: high
related_paths:
  - internal/planner/rust_overrides.go
  - internal/e2e/cargo_patch_test.go
promoted: false
---

# Cargo patches need a full static scope to include unseen transitive consumers

## Observation
Cargo patches apply transitively through registry dependencies. Matching local dependency package names, aliases or versions cannot prove that a local crate is unaffected. The repair validates local override targets through the physical Cargo boundary and falls back to every discovered Cargo suite; unsupported graph declarations and repository Cargo config overrides retain commands but make coverage incomplete. Exact cc84ebb archive loses the app failure (Cargo 101) in the checked-in rust-patch fixture.
