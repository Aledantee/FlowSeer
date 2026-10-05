---
title: Analysis, Trace, Stream, and Search - Plan
type: fix
date: 2026-10-01
artifact_contract: flowseer-plan/v2
execution: code
---

# Analysis, Trace, Stream, and Search - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A stream source yields frames a consumer may change, `search` names the
journey it aligned or minimized against, and the four leaf packages export
what a caller uses. Stop condition: if `fabric.Compare` cannot name which
journey diverged, `search.Minimize` cannot be made exact here, and the
entry moves to phase 10 with the comparison change.

## Decisions

- The parent's Decisions apply.
- `search.L3Domain` is deleted until a routed domain is implemented. Why: it
  is an interface with no implementation (`search/domain.go:152`), which
  `docs/code-style.md` forbids. The virtual-device record names it, and the
  record is amended in this phase.
- Scope ordering entries 2 and 3 are judged against what `analysis` promises
  before they are fixed. `Scope.Compare` is a total order either way. The
  entries are defects only if a doc or a caller relies on the order matching
  the identifiers' own order.

## Requirements

R1. Every Correctness entry has a test that fails before its fix or is
struck with its reason.

R2. Two frames from one source share no memory with each other or with the
spec. Example: writing to the payload of the first frame from a spec source
leaves the second frame and a clone of the source unchanged.

R3. `Align` reports a divergence whenever the comparison is `Different`.
Example: one side has an extra journey from a mirror copy, every paired
journey matches, and `Align` names the unpaired journey.

R4. `Search` accounts for every candidate. Example: after a run,
`Tested == EquivalentCount + TotalDifferences + InconclusiveCount + Errors`.

## Inventory

Paths are as of commit `61775c73`. `N` is `src/common/netsim`, which phase 1
moves to `src/common/sim`. No entry has a test in the tree.

### Correctness

1. High. `specSource.Next` returns the template frame without cloning its
   tags or payload (`N/stream/source.go:35`). With no variation that
   rewrites them, every frame shares the template's memory.
2. Unjudged. `appendScope` ends an encoded identifier with `;`
   (`N/analysis/scope.go:138`), so `NodeScope("a")` sorts after
   `NodeScope("a0")`.
3. Unjudged. `FieldScope` writes an element's length in decimal
   (`N/analysis/scope.go:107`), so a 2-character element sorts after a
   10-character one.
4. High. `Align` iterates the paired journeys only and returns no
   divergence when the sides differ in journey count
   (`N/search/align.go:35`).
5. High. `Spec.Validate` checks a size variation against a UDP port
   variation only when the size comes first (`N/stream/stream.go:87`). The
   other order truncates the UDP header and leaves the IPv4 total length.
6. Medium. `MinimizeWithLimit` accepts a reduction when the observable's
   category matches (`N/search/minimize.go:76`), so a different injection
   diverging in the same category passes for the original.
7. Medium. `deduplicateAndSortTimes` keys by `UnixNano`
   (`N/search/fault.go:164`), which is undefined for the zero time, and
   formats without converting to UTC.
8. Medium. `Align` returns the entry index without the journey index
   (`N/search/align.go:26`).
9. Low. `CompareStep` orders facts shortest-first and evidence
   lexicographically (`N/trace/trace.go:212`).
10. Low. `Search` drops `Comparison.Err` and counts the candidate as tested
    (`N/search/search.go:37`).
11. Low. `defaultSourceMAC` hashes node and port joined by `/` without
    length prefixes (`N/search/l2.go:171`), so two endpoints can collide.

### Completeness

- `analysis.Metadata` has `StatusFor` and `IssuesFor` and no
  `AssumptionsFor`. Tests in `netmodel` filter assumptions by hand
  (`N/vswitch/netmodel/trust_test.go:1459`).
- `search.Limits` and the two domain configurations have no `Validate`. A
  budget of zero yields `Inconclusive` results with no error.
- `Limits.MaxDifferences` of zero means unlimited
  (`N/search/search.go:40`), so a caller cannot ask for counts alone.
- `TestVocabulary` (`N/trace/trace_test.go:82-90`) omits `OpQueue` and
  `Held`.
- `N/analysis/README.md:9` shows `errs.Msg` where the runnable example uses
  `errors.New`.

### Design

- `analysis.Disposition` is a comparison outcome in the trust package, whose
  package comment says outcomes belong to their producer
  (`N/analysis/doc.go:9-10`). Phase 2 moves `Difference` here. Decide
  whether both live in `analysis` or in a comparison leaf.
- `search.FaultSpec` and `search.TimedFault` repeat `fabric`'s fault action
  (`N/search/fault.go:13`, `N/search/domain.go:23`,
  `N/fabric/scenario.go:41`).
- `search.Align` depends on nothing in `search` (`N/search/align.go:14-68`).
  It belongs beside `fabric.Comparison`.
- `Variation.Apply` takes a concrete `*SplitMix64`
  (`N/stream/variation.go:21`), which forces the generator to be exported.
- A stream source has no offset, so the lab test wraps one
  (`src/edge/netsimload/test/integration/lab_test.go:192-205`), and
  `netsimload` walks a cloned source to learn the smallest payload
  (`src/edge/netsimload/run.go:200-221`).
- Endpoints are rendered `node/port` in `N/search/l2.go:157` and
  `node:port` in `N/search/fault.go:142`.

### Tests

- No source test mutates a yielded frame, unlike `N/stream/capture_test.go:44`.
- `N/search/align_test.go:47` has one injection and one journey.
- `N/search/domain_test.go:89` checks that an empty struct satisfies
  `L3Domain`.
- `N/fact_contract_test.go:188,200` return after `t.Fatal`.

## Open questions

- Does `fabric` change the frames a source yields in place
  (`N/fabric/attach.go:18`)? The answer sets the severity of Correctness 1.
- How does a candidate order a fault and an injection at the same instant
  (`N/fabric/scenario.go:512`)?
