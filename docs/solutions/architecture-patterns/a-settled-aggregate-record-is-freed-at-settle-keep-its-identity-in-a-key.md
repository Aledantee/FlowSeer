---
title: A Settled Aggregate Record Is Freed at Settle; a Later Event's Identity Belongs in a Key, Not the Record
date: 2026-09-23
category: architecture-patterns
module: src/common/netsim/fabric
problem_type: architecture_pattern
component: netsim
severity: high
applies_when:
  - "Implementing or reviewing a retention policy that frees a record only once some later event claims it, especially a frame a switch holds until a release."
  - "Keeping a settled journey, run, or request alive past settle so a future event can name it."
  - "A run never reports convergence (StopConverged never fires) because a settled frame still counts as a pending journey."
  - "A release or follow-up event misattributes itself to the wrong frame after retention has freed earlier frames."
related_components: [convergence, comparison]
tags: [retention, settle, convergence, attribution, placeholder, netsim]
---

An aggregate record that survives its own settle in order to answer a *future*
event breaks two things at once: convergence, because the record still reads as
pending, and attribution, because the future event can now match the wrong
record. The fix is to let settle free the record unconditionally and keep only
the small identity a later event needs — a key, not the record.

## What happened

Phase 1 of offered-load streams settles a frame when its last in-flight copy
leaves and, for `RetainAggregate`, folds its outcome into the flow and frees the
journey (`src/common/netsim/fabric/flow.go:82-99`). A switch that routes a
frame to an unresolved address holds it for neighbor resolution; the journey's
last entry outcome becomes `trace.Held`, so the frame has settled even though no
release has happened yet. The release marks the released frame as
`OriginRelease` naming the frame it was held from, and downstream comparison
tracks descendants by that link.

The first implementation kept a held aggregate journey alive until its release
could claim it, on the reasoning that the release needed the journey to name its
holder. Review found the two failures that rule produces:

- **Convergence never fires.** `Report` counts a held journey that no release has
  named as `JourneyPending` (`src/common/netsim/fabric/journey.go:248-252`),
  `hasPendingJourneys` returns true for it
  (`src/common/netsim/fabric/run.go:1646-1652`), and the convergence check
  requires `!hasPendingJourneys()`
  (`src/common/netsim/fabric/run.go:1581`, `src/common/netsim/fabric/compare.go:249`).
  A single abandoned hold therefore pinned the run out of `StopConverged`.
- **Releases misattribute.** `findAndPopHeld` picks a candidate by payload
  match, then falls back to the lowest candidate ID
  (`src/common/netsim/fabric/run.go:1289-1327`). With no journey for the freed
  frame, the release either collapsed to a fresh injection or the fallback
  pinned it to a coexisting held journey — a wrong `Origin.Of`.

## The rule

Settle frees every `RetainAggregate` journey. When a later event needs to know
which record it belongs to, keep that record's identity in a side map keyed by
the thing that will look it up, and have the later event *claim* (remove) the
key. The record itself is gone.

The shipped shape: `recordHeldAggregate` stores the freed frame's `FrameID`
under the device of its last entry, ascending, and `settle` deletes the journey
and its re-entry set (`src/common/netsim/fabric/flow.go:92-114`). The release's
path adds those IDs to the candidate list (`src/common/netsim/fabric/run.go:1308-1312`),
payload matching skips a placeholder because it has no journey
(`src/common/netsim/fabric/run.go:1315-1319`), and `claimHeldAggregate` removes
the claimed ID so a later release cannot name it twice
(`src/common/netsim/fabric/flow.go:116-126`, called at
`src/common/netsim/fabric/run.go:1258-1263`). The identity a release needs is a
`FrameID`; the journey was never the identity.

```go
// at settle: fold, free the record, keep only the key
delete(f.journeys, fid)
delete(f.entered, fid)
f.recordHeldAggregate(fid, j) // map[device][]FrameID, ascending

// later: the release looks the key up and removes it
if holdingFID := f.findAndPopHeld(device, em.Frame); holdingFID != 0 {
    f.claimHeldAggregate(device, holdingFID)
}
```

`TestAggregateRetentionPreservesReleaseAttribution`
(`src/common/netsim/fabric/flow_internal_test.go:676`) is the evidence: the same
fixture runs retained and aggregated, and for every release the two runs name
the same holder, the aggregated run keeps no `RetainAggregate` journey and no
pending journey, and only the abandoned holds' placeholders remain. The
direction record that authorized retention, so this reading of it, is
`docs/architecture/2026-09-18-offered-load-streams-direction.md:67-75`; the
virtual device record's run bullet states the same freeing rule
(`docs/architecture/2026-09-10-virtual-device-direction.md:87-95`).

## What it does not cover

A placeholder for an abandoned hold is not garbage-collected: nothing ever
claims it, so it stays for the life of the run (the test asserts exactly this).
That is a `FrameID` per distinct abandoned hold instead of a whole journey, and
the design accepts it for attribution, but a workload that abandons holds
without bound keeps one key each. A placeholder also carries no payload, so a
release whose payload matches a later real held journey still chooses the real
one — correct here, but it means the placeholder is only ever a fallback.
