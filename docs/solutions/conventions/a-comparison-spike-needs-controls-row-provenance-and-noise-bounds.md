---
title: A Comparison Spike Needs Controls, Row Provenance, and Noise Bounds
date: 2026-09-30
category: conventions
module: docs/research
problem_type: convention
component: comparison
severity: high
applies_when:
  - "Writing or reviewing a comparison spike that reports cache staleness, latency, throughput, or lookup results for two engines."
  - "A table repeats another note's scenarios, or a row has no fixture, client, deadline, or run provenance."
  - "A shared-host benchmark produces overlapping spreads, unexplained outliers, or a tempting speed ranking."
related_components: [testing_framework, documentation]
tags: [benchmark, comparison, evidence, provenance, staleness, throughput]
---

# A Comparison Spike Needs Controls, Row Provenance, and Noise Bounds

A comparison spike supports a decision only when its harness proves that it can
observe the property, each row comes from the run it claims to describe, and
the host noise does not support a stronger conclusion than the data allows.

## Make negative results observable

A staleness result of "none observed" can mean fresh reads or a harness that
never detected a stale read. Start each revocation trial with a grant control.
Keep polling until the control shows the expected stale deny and only then use
the same observation path to measure a stale revoke allow. If the control does
not pass, report the timing of the first deny and omit a staleness estimate.

The SpiceDB note uses this rule. Its row reports revocation staleness only when
both a stale control deny and a stale revoke allow were observed
(`docs/research/2026-09-30-spicedb-authorization-spike.md:328-330`). The
positive-control table shows stale controls for `minimize_latency` and
OpenFGA's `UNSPECIFIED` mode, while the stronger modes show none
(`docs/research/2026-09-30-spicedb-authorization-spike.md:346-354`). The note
then reports no estimate for the modes without a stale control
(`docs/research/2026-09-30-spicedb-authorization-spike.md:376-392`).

## Keep every row tied to its own run

Matching dimensions do not prove matching fixtures. A copied count or timing
can look like a remeasurement even when a dedicated user, relationship list,
client, or deadline changed. Store the fixture and run conditions with each
row. If the new harness did not measure a scenario comparably, say so and keep
the prior result as context outside the new result row.

The note states that equal workload dimensions do not make individual grants
identical (`docs/research/2026-09-30-spicedb-authorization-spike.md:72-75`).
Its exclusion section says that variant was not measured comparably
(`docs/research/2026-09-30-spicedb-authorization-spike.md:456-460`). For the
disposable rebuild, it reports both engines from the current execution and
separately explains why the earlier OpenFGA numbers differ
(`docs/research/2026-09-30-spicedb-authorization-spike.md:472-490`).

## Bound throughput claims by the host

Record the host, container resources, cache settings, caller count, input set,
and load around each timed execution. Report the median and spread. A large
unexplained outlier or overlapping spreads means the measurement is supporting
evidence for the workload, not a speed ranking.

The authorization spike labels its shared-Colima measurements as laptop
measurements rather than capacity figures (`docs/research/2026-09-30-spicedb-authorization-spike.md:37-42`).
Its throughput table contains an unexplained 5,366.107 checks/s execution
inside the SpiceDB spread (`docs/research/2026-09-30-spicedb-authorization-spike.md:236-245`),
and the note leaves the engine ordering unresolved because the spreads overlap
(`docs/research/2026-09-30-spicedb-authorization-spike.md:247-253`).

This does not prohibit a controlled capacity benchmark. It limits what a
shared-laptop comparison can claim until a dedicated benchmark controls the
host and repeats the workload.
