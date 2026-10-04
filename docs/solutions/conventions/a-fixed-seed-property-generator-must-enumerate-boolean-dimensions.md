---
title: A Fixed-Seed Property Generator Must Enumerate Boolean Dimensions
date: 2026-10-04
last_verified: 2026-10-04
category: conventions
module: src/services/device/internal/projector
problem_type: convention
component: testing_framework
severity: high
applies_when:
  - "Generating a finite property-test matrix from a fixed-seed pseudo-random stream"
  - "Using a pseudo-random generator to choose Boolean record or state dimensions"
  - "Claiming finite-layout coverage from the number of distinct generated worlds"
related_components: [testing, property-tests, coverage-gate]
tags: [testing, property-tests, fixed-seed, lcg, coverage]
---

# A fixed-seed property generator must enumerate Boolean dimensions

A fixed seed makes a property test repeatable. It does not make its generated
worlds representative. A linear congruential generator with an odd multiplier
and increment flips its low bit on every step. Sampling Boolean dimensions from
that bit alternates the result and can visit only a small part of a finite
layout matrix while still producing distinct worlds through other dimensions.

Map finite Boolean dimensions from the case index. Use the pseudo-random stream
for dimensions that do not need exhaustive coverage. The projector generator
uses index bits for edge, device, session, and grant presence, then draws tuple
states and session edges from the fixed-seed stream
(`src/services/device/internal/projector/projector_test.go:913-943`).

Guard the claims separately. The test checks all 512 generated worlds and all
64 record layouts, then checks literal fault and predicate-signature counts
(`src/services/device/internal/projector/projector_test.go:1462-1528`). A count
of distinct worlds alone would not prove that every record layout was reached.

## Evidence

- The generator's linear congruential recurrence is at
  `src/services/device/internal/projector/projector_test.go:850-860`.
- The fixed generator maps the finite Boolean dimensions from `index` at
  `src/services/device/internal/projector/projector_test.go:918-935`.
- The layout and world guards are at
  `src/services/device/internal/projector/projector_test.go:1511-1518`.
- The earlier low-bit Boolean sampler was removed by the coverage fix in
  commit `5e462c89`.

This rule supplements the count-guard rule in
`a-combinatorial-table-count-guard-must-assert-a-literal.md`. That rule checks
whether the loop ran the intended rows. This one checks whether a bounded
generator reached the intended finite layouts. Unbounded fuzzers need invariant
checks for each generated sample instead.
