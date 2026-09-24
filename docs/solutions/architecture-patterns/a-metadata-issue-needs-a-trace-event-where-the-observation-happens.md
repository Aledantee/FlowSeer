---
title: A Metadata Issue Needs a Trace Event Where the Observation Happens, Not a Fabricated Ref
date: 2026-09-24
last_verified: 2026-09-24
category: architecture-patterns
module: src/common/netsim/fabric
problem_type: architecture_pattern
component: netsim
severity: high
applies_when:
  - "Attaching evidence to an analysis issue raised by a runtime condition (such as queue occupancy, resource limits, or protocol warnings) where no trace step currently exists."
  - "Deciding whether a metadata issue can cite a synthesized evidence reference or attach to a later, unrelated trace step."
  - "Adding a diagnostic trace event that must not alter behavioral run comparisons."
related_components: [trace, analysis, traffic, vswitch]
tags: [analysis-metadata, trace, evidence, runtime-evidence, diagnostic-step, netsim]
---

# A metadata issue needs a trace event where the observation happens

An analysis issue that degrades confidence (such as `analysis.Incomplete`) must
cite evidence from the run's evidence catalog. When that issue is raised by a
runtime condition during simulation, it cannot point to a fabricated reference
or borrow a later, unrelated trace event. The evidence must be created at the
exact site of the observation, paired with a trace step on the crossing frame.

## What happened

During offered-load streams phase 3, conformance case 13b
(`planning/oversubscribed-trunk-unstated-buffer`) required an oversubscribed
egress queue without a stated buffer to report `IssueQueueBufferUnstated` with
`analysis.Incomplete` status. Under `netsimtest.ValidateCase`, every
non-Complete issue must cite resolvable evidence.

Initially, `queueBufferUnstatedIssue` constructed the issue with no evidence.
Attempting to satisfy the validator exposed an architectural gap:

- The observation happens at `enqueueEgress` when queue depth exceeds one
  maximum-size frame.
- The frame's subsequent transmission across the cable has no `trace.Step`.
- In some tests, the marker is called on a synthetic or empty journey, so no
  prior trace step exists to cite.

Borrowing a later cable-crossing step would misattribute the time and cause of
the observation. Synthesizing a detached evidence reference in the catalog
without an accompanying trace step leaves the execution trace silent about why
readiness degraded.

## The rule

When runtime execution discovers a condition that warrants an analysis issue:

1. **Emit a trace step at the observation site.** Define a trace operation and
   rule ID (such as `trace.OpQueue` and `traffic.RuleQueueBufferUnstated`). Record
   an entry on the journey at the moment the condition is observed.
2. **Bind the step and issue to one runtime evidence reference.** Create an
   `analysis.Evidence` entry recording the observation's context (rule ID,
   physical endpoint, logical port, instant, and typed facts). Store the
   returned `trace.EvidenceRef` in both the journey's trace step and the
   analysis issue.
3. **Filter diagnostic-only entries from behavioral comparisons.** When the
   observation records internal state without changing frame delivery or
   drops, have `diffJourney` filter the entry out. This lets the diagnostic step
   serve human inspectors and conformance checks without producing false
   behavioral diffs between otherwise identical runs.

## Working example

At enqueue time, `markQueueBufferUnstated` verifies the threshold transition,
builds the runtime evidence and typed fact, and records `EntryQueueThreshold`:

```go
fact := traffic.QueueThresholdFact(depthBefore, frameOctets, threshold)
f.runtimeEvidence, ref = f.runtimeEvidence.Add(analysis.Evidence{
	Kind: "fabric.runtime", Origin: "egress-queue", Context: context,
})
f.unstatedBacked[ep] = ref
f.record(journey, Entry{
	At: now, Kind: EntryQueueThreshold, Device: ep.Node, Port: ep.Port, PCP: pcp,
	Step: &trace.Step{
		Layer: traffic.Layer, Op: trace.OpQueue, RuleID: traffic.RuleQueueBufferUnstated,
		Subject: subject, Inputs: []trace.Fact{fact}, Evidence: []trace.EvidenceRef{ref},
	},
})
```

`Metadata()` merges `runtimeEvidence` and passes `ref` to `queueBufferUnstatedIssue`.
In `compare.go`, `diffJourney` strips `EntryQueueThreshold` before comparing
behavioral entries.

## Evidence

- The queue trace operation: `OpQueue` in `src/common/netsim/trace/trace.go:42`.
- The typed fact and rule: `RuleQueueBufferUnstated` and `QueueThresholdFact` in
  `src/common/netsim/vswitch/traffic/fact.go:16`, `:61-65`.
- Observation capture and step emission: `markQueueBufferUnstated` in
  `src/common/netsim/fabric/fabric.go:1025-1064`.
- Shared evidence reference in metadata issue: `queueBufferUnstatedIssue` in
  `src/common/netsim/fabric/fabric.go:1002-1010`.
- Diagnostic filtering during comparison: `diffJourney` in
  `src/common/netsim/fabric/compare.go:453-454`.
- Verification of shared reference: `TestEgressUnstatedThresholdRecordsEvidenceAtEnqueue`
  in `src/common/netsim/fabric/egress_buffer_internal_test.go:108-156`.
- Corpus validation: `planning/oversubscribed-trunk-unstated-buffer` in
  `src/common/netsim/internal/netsimtest/load_cases.go:211-231`.

## What this does not cover

- Static construction evidence: evidence known prior to execution (such as
  cabling declarations or interface config) is stored in `Fabric.Config.Spec().Evidence`
  and requires no runtime trace step.
- Lifecycle copying across branches: when components holding runtime state or
  attachments fork, lifecycle methods like `Fabric.Fork` must clone those
  sources (see
  `docs/solutions/architecture-patterns/a-third-kind-joins-a-two-kind-system-silently.md`).
