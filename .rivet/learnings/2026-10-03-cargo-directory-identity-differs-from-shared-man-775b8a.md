---
title: Cargo directory identity differs from shared manifest identity
date: 2026-10-03
confidence: high
related_paths:
  - internal/planner/rust.go
promoted: false
---

# Cargo directory identity differs from shared manifest identity

## Observation
Offline Cargo confirms that directory symlinks identify the same crate, but a Cargo.toml symlink in a different directory uses that directory for src and dependency paths. Canonical directory identities preserve reverse dependency edges; shared physical manifests propagate changes across distinct crate roots. Alias parents must resolve before dot-dot components; unresolved local edges must diagnose incomplete coverage.
