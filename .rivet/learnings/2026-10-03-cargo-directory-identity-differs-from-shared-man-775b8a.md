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
Offline Cargo confirms that directory symlinks identify the same crate, but a Cargo.toml symlink in a different directory uses that directory for src and dependency paths. Canonical directory identities preserve reverse dependency edges; shared physical manifests propagate changes across distinct crate roots. Cargo lexically normalizes dot-dot before resolving dependency aliases (confirmed offline); separately validate physical traversal so dot-dot cannot erase an escaping or unresolved component. Unresolved local edges diagnose incomplete coverage.
