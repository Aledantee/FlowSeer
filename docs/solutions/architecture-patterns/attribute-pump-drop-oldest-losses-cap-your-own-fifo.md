---
title: Attributing pump.TrySendDropOldest Losses to a Specific Batch Requires Capping Your Own Tracking FIFO to the Pump's Buffer Size
date: 2026-09-09
last_verified: 2026-09-09
category: architecture-patterns
module: src/common/pump
problem_type: architecture_pattern
component: concurrency
severity: critical
applies_when:
  - "a producer wants to attribute a pump.Pump[T].TrySendDropOldest eviction to the specific value that was evicted (a record count, a sequence range, an identifier) rather than just counting drops"
  - "adding or reviewing a drop-oldest streaming producer built on src/common/pump.Pump[T].TrySendDropOldest that needs more than a plain drop count (src/protocol/snmp's TrapStream is this pattern's simpler sibling: it uses TrySendDropOldest but only counts drops, with no attribution)"
  - "a drop-accounting counter looks right under a light load test but drifts or misattributes under sustained throughput"
related_components: [pump, capture_engine]
tags: [pump, trysenddropoldest, drop-accounting, fifo, backpressure, attributable-loss]
---

# Attributing pump.TrySendDropOldest Losses to a Specific Batch Requires Capping Your Own Tracking FIFO to the Pump's Buffer Size

## The situation

`pump.Pump[T].TrySendDropOldest` (`src/common/pump/pump.go:154-218`) reports
only `(delivered bool, dropped int)` per call — it deliberately keeps no
drop counter or identity of what it evicted, "callers that want one should
add to their own `sync/atomic.Uint64` based on the returned dropped count"
(`pump.go:172-174`). A caller that wants to say *which* value was lost, not
just how many, has to track buffered-item identity itself, alongside the
pump. `src/modules/capture`'s engine does this to satisfy R5 ("loss is
always attributable": a consumer that sees a sequence gap must find the
exact gap size in the next batch's `dropped_by_transport` counter). The
first version of that tracking grew without bound over a long, healthy run
and, once an eviction did happen, blamed the wrong batch — one long since
delivered and consumed, not the one actually still sitting in the channel.

## What is true, and why

`TrySendDropOldest`'s five documented outcomes (`pump.go:162-170`) tell a
caller whether *this* send evicted something and whether *this* send itself
landed, but never tell it that a *previous* send's value was later read out
normally by the consumer — an ordinary, successful delivery carries no
signal back to the producer at all. A caller that appends one entry per
successful send to build a FIFO mirroring buffer occupancy, and only ever
removes an entry on the `dropped == 1` (eviction) case, is implicitly
assuming every successful send stays in the channel until an eviction
removes it. That assumption is false the moment a consumer is actually
consuming: every consumer read shrinks the real channel without the
producer ever finding out. The tracking FIFO then grows past the channel's
real occupancy indefinitely, and when an eviction eventually does happen,
popping "the oldest tracked entry" pops whichever entry was appended
*first*, not whichever value the eviction actually displaced.

The channel `pump.New` constructs has a fixed capacity (the `buf` argument,
`pump.go:65-76`), so real occupancy can never exceed it regardless of how
fast or slow the consumer is. A caller's own tracking structure can safely
assume the same bound: after every send that did not itself require an
eviction, if the tracking FIFO's length exceeds that capacity, the excess
at the front must already have been consumed normally (nothing else could
have removed it), so it is discarded — not counted as a loss, just dropped
from the tracking structure to restore the invariant.

## How to apply

```go
// src/modules/capture/engine.go:428-452 (trim added at 432-434)
func applyDropAccounting(fifo *[]int, pendingDrops *uint64, delivered bool, dropped int, sentCount int) {
	switch {
	case delivered && dropped == 0:
		*fifo = append(*fifo, sentCount)
		if len(*fifo) > pumpBuffer {
			*fifo = (*fifo)[len(*fifo)-pumpBuffer:]
		}
	case delivered && dropped == 1:
		if len(*fifo) > 0 {
			*pendingDrops += uint64((*fifo)[0])
			*fifo = (*fifo)[1:]
		}
		*fifo = append(*fifo, sentCount)
	// ...
	}
}
```

`pumpBuffer` here is the exact `buf` value passed to `pump.New` for this
pump — the trim bound must match the pump's own capacity, not an estimate
of it. The other four `TrySendDropOldest` outcomes need no equivalent
change: they either pop-then-push (net length unchanged) or never touch the
FIFO at all (the current send's own value never entered the channel), so
only the always-succeeds branch can grow the structure past its bound.

## Evidence

- The invariant the trim restores: `src/common/pump/pump.go:154-218`
  (`TrySendDropOldest`'s doc comment and body) — nothing in its contract
  notifies a caller of an ordinary successful read.
- The fix: `src/modules/capture/engine.go:428-452`.
- `src/modules/capture/engine_test.go`'s
  `TestApplyDropAccounting_TrimsFIFOToPumpCapacity` proves it directly: 20
  successive non-evicting sends leave exactly `pumpBuffer` (8) entries
  behind, and a subsequent eviction blames the 13th send (the oldest one
  the pump could still actually be holding), not the 1st (the oldest one
  ever recorded).
- `TestEngine_AttributableLoss` (same file) exercises the same accounting
  end to end but does not by itself prove this fix: its consumer never
  reads concurrently with the producer, so the real channel and the
  tracking FIFO never diverge in that test regardless of whether the trim
  exists — the direct `applyDropAccounting` test above is what catches the
  bug this document describes.

## What this does not cover

This pattern applies to `TrySendDropOldest` specifically; `pump.Send`
(`pump.go:115-152`) is a blocking, non-evicting send and needs no such
tracking. It also assumes one producer per pump, as `capture`'s engine and
every other current `TrySendDropOldest` caller in this codebase are — a
second concurrent producer sending to the same pump would need its own
synchronization over the shared FIFO, which this pattern does not address.
