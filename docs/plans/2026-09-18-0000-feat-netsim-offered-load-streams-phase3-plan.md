---
title: Offered-Load Streams Phase 3 - Stream Package and the Pull Loop - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-plan.md
---

# Offered-Load Streams Phase 3 - Stream Package and the Pull Loop - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A caller attaches streams to a fabric and reads per-stream results. The means:
`src/common/netsim/stream` with the spec, the variations, SplitMix64, and a
`Source` iterator; `Fabric.Attach`; and a pull in `Step` that injects source
frames due before the earliest queued arrival. The plan is wrong if lazy pull
cannot reproduce eager injection, requirement 11, because the two orders then
give different answers and only one can be documented as the model.

## Decisions

- The parent's decisions and the direction record apply.
- `stream` imports `src/common/net/ethernet`, `netaddr`, `vlan`, `ip`, and
  `udp`, and nothing under `netsim`. `fabric` imports `stream`.
- `Source` is `Next() (at time.Duration, frame ethernet.Frame, ok bool)` with
  `at` relative to the stream's start. Why: a wall-clock transmitter and a
  simulated clock both add their own epoch.
- Rate is stated in frames per second or bits per second of wire octets, the
  84-to-1542 figure `wireOctets` uses (`fabric/run.go:86-96`). Why: line rate
  is a wire figure, and a stream at 100% of 1 Gbit/s must fit exactly.
  Inter-frame times are computed in integer nanoseconds with the remainder
  carried, as `rateInterval` does, so a million frames accumulate no drift.
- Each attached stream gets a flow, and its frames use `RetainAggregate`
  unless the attachment asks for journeys.
- SplitMix64 is implemented in the package. The re-plan fetches the reference
  implementation, records its URL, and takes the known-answer vectors from it.
- A stream at a host whose link is `Down` ends; at an `Unknown` link its
  frames are unresolved, as `Inject` already decides per frame.
- Jitter is the mean absolute difference of consecutive latencies within a
  flow, RFC 3550 section 6.4.1's interarrival jitter without the smoothing
  gain. The re-plan reads that section before keeping the definition.

## Requirements

9. A stream of 1,000 frames at 10,000 frames per second starting at `t0`
   yields injections at `t0 + n * 100 µs`; with a burst of 10 and a gap of
   1 ms, ten frames back to back at line spacing and then the gap.
10. A destination MAC variation of increment, step 1, count 256 yields 256
    distinct addresses and then wraps; two sources built from one spec yield
    identical frames; SplitMix64 from seed 0 matches the reference vectors.
11. Four streams across a LAG trunk, attached, give the same `Flows()` and the
    same per-host delivery times as the same frames injected eagerly in time
    order before the first step.
12. A size variation over `64, 128, 256, 512, 1024, 1280, 1518`, the sizes of
    RFC 2544 section 9.1 (https://www.rfc-editor.org/rfc/rfc2544.txt), yields
    frames whose encoded length plus FCS equals each size in turn.
13. The corpus admits three cases: an oversubscribed trunk with a stated
    buffer, the same with none, and a policed stream, each with the false
    answer it prevents.

## Open questions

- `stream` cannot import `fabric`, yet both need the wire-octet figure, and
  requirement 11 fails if they differ. The likely answer is to move
  `wireOctets` to `src/common/net/ethernet` in this phase's first unit.
- The local network analysis record says a third endpoint kind that reacts is
  a new decision against it. A source attached to a host originates on the
  fabric's pull and ends on link-down. The re-plan decides whether that is an
  injection generator outside the rule or a kind under it, and amends that
  record if it is the second.

- Whether a source frame due at the same instant as a queued arrival is
  injected before it. Eager injection gives injected frames lower `Seq`
  values; the re-plan works requirement 11 through `compareArrival` and
  `enqueueEgress` on paper before choosing.
- IP and UDP field variation needs checksum recomputation. `net/udp` encodes
  with pseudo-header checksums; `net/ip` has to be checked for an encoder.
