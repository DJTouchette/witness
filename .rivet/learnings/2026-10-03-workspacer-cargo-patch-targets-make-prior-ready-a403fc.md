---
title: Workspacer Cargo patch targets make prior ready audit expectations incomplete
date: 2026-10-03
confidence: high
related_paths:
  - audits/representative.json
  - internal/planner/rust_overrides.go
promoted: false
---

# Workspacer Cargo patch targets make prior ready audit expectations incomplete

## Observation
After adding Cargo patch detection, actual Workspacer manifests name gpui-component and portable-pty local patches under vendor paths excluded by bounded discovery. The old native ready/1 expectation hid unresolved ownership. The corrected audit requires incomplete/unknown and named patch diagnostics with seven retained Cargo suites; mixed Rust/TS requires eight commands. No shared Workspacer files or indexes were changed.
