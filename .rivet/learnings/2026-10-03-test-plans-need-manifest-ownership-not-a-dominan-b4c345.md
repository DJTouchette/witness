---
title: Test plans need manifest ownership, not a dominant language
date: 2026-10-03
author: Astra
confidence: high
related_paths:
  - internal/planner/**
  - internal/selector/**
  - cmd/witness/cli/**
promoted: false
---

# Test plans need manifest ownership, not a dominant language

## Observation
Real Workspacer native UI change selected zero tests and v0.5.0 fallback emitted npx jest. Nested .NET project references and Cassadol Vitest need actual manifest ownership. Co-change and capped graph traversal are not proof of coverage.

## Recommendation
Use the shared non-executing planner and versioned argv/cwd contract. Run witness audit audits/representative.json with temporary indexes; never execute representative app suites to validate planning.
