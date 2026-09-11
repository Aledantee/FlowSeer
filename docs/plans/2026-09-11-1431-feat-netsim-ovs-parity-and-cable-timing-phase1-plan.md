---
title: Network Simulation, Phase 1: Cable Timing and Media - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-plan.md
amends: docs/architecture/2026-09-10-virtual-device-direction.md
---

# Network Simulation, Phase 1: Cable Timing and Media - Plan

## Goal

A frame crossing a cable takes its serialization time at the negotiated
rate plus the cable's propagation time, a port that is still sending holds
the next frame until it is free, a host's injection is a transmission on
the host's cable end like any other, and a cable's medium and length decide
both the propagation time and the highest speed the link can negotiate.
The means is a `Medium` and a `Delay` on `fabric.Cable`, a per-endpoint
busy clock in the run, a reach check the fabric runs before negotiation,
and a rewritten cable and run bullet in the direction record. The phase is
wrong if a consumer needs the busy clock to model a store-and-forward
switch's own forwarding latency, which stays zero here.

## Decisions

The parent's Decisions on the two terms, the busy clock, the media and
their velocity factors, and the reach table hold. This phase adds the
shapes:

- `fabric.Medium` is a string type with `TwistedPair`, `MultimodeFiber`,
  `SinglemodeFiber`, and `Twinax`; the empty value means twisted pair, so
  a configuration that names no medium keeps its meaning apart from the
  factor. `Cable` gains `Medium Medium` and `Delay *time.Duration`. `Clone`
  copies the pointer's value; `Diff` reports `medium` and `delay` as cable
  fields beside `length`; `Validate` refuses a negative delay and a medium
  that is neither empty nor one of the four. Why: a pointer is what says
  "set" for a duration where zero is a valid override (a zero-length
  patch), and the four values are what the lab holds.
- The velocity and reach tables live in `src/common/netsim/fabric/medium.go`
  as `func (m Medium) VelocityFactor() float64` and
  `func (m Medium) Reach(speedBPS uint64) float64` in metres, 0 for a
  speed the medium has no row for. A speed reaches a length when
  `length <= reach`. `Propagation(length, medium)` is
  `length / (factor * 299_792_458)` rounded to the nearest nanosecond,
  the formula `cableLatency` uses today with the factor in place of two
  thirds. Why: the medium is a fabric concept, since `phy` models a port
  and knows no cable, and a method on the enum keeps the table beside the
  values it indexes.
- Reach is checked before negotiation, over the speeds the link could
  take: each end's `SupportedSpeedsBPS`, each end's forced
  `Setting.SpeedBPS`, a non-zero `TopSpeedBPS`, and 1 Gbit/s when no end
  declares a speed, which is what `Negotiate` gives two all-speeds auto
  ends. When no candidate reaches the length, `resolveLink` sets both ends
  down with `fabric.ReasonReachExceeded` ("reach-exceeded") and never
  calls `Negotiate`; a forced end whose speed does not reach the length
  is `reach-exceeded` the same way, before `Negotiate` could say
  `speed-mismatch`. Otherwise the cap is the highest candidate that
  reaches, and `Negotiate` gets `top` as the lower of `TopSpeedBPS` and the
  cap, or `TopSpeedBPS` unchanged when it is 0 and the cap is at least
  1 Gbit/s, so two undeclared ends keep today's 1 Gbit/s. A length of 0
  reaches every speed. Why: `Negotiate`'s `top` of 0 means unlimited, so
  the cap cannot be passed through a `min`, and a forced end past reach
  is a cable problem, not a speed mismatch.
- Serialization is `wireOctets(frame) * 8 / rate` rounded up to the
  nanosecond, where `wireOctets` is the encoded length padded to
  `60 + 4 * len(frame.Tags)` (IEEE 802.3 pads the client data, so a tag
  raises the minimum) plus 24 for the FCS, preamble, start delimiter, and
  interpacket gap; the rate is the link's negotiated speed. One function
  in `run.go` computes it for the crossing entry and the busy clock. Why:
  the parent's Decision, made a formula, with the tag correction the
  trunk crossing needs.
- The run keeps `busyUntil map[Endpoint]time.Time`. A transmission on an
  endpoint starts at the later of now and that clock, ends one
  serialization later, and arrives at the far end at the end plus
  propagation, or plus `*Delay` when set; the clock takes the end. A
  delivery to a host is recorded at the arrival time. Within one step the
  copies are charged in `Result.Egress` order, which today holds at most
  one copy per port; a phase that adds a second copy to one port (mirrors)
  states its order. Two arrivals at the same instant are two steps in the
  queue's total order, and the second waits for the first's clock. Why:
  the wait is what serialization exists to show, and the queue order
  already decides which of two equal-time frames goes first.
- `Inject` at a host becomes a transmission on the host's cable end: the
  journey gains a crossing entry for the host leg, the host end's busy
  clock is charged, and the arrival at the switch is the transmission end
  plus the cable's propagation. `Inject` at a host whose link has no
  negotiated speed returns an error naming the link's reason and enqueues
  nothing. `Inject` at a switch port stays a direct arrival, since it
  stands for a capture replay at that port. Why: a host is a port too, and
  a division by a zero rate is the alternative.
- The journey's crossing entry gains `Serialization` and `Wait` (start
  minus now) and keeps `Latency` as the propagation term. `Snapshot` gains
  `Busy map[Endpoint]time.Time`, the endpoints whose clock is after
  `Clock`, with that time. Both are read by this phase's tests and the
  README; `Compare` ignores timing as it does today. Why: a wait that is
  not visible in the journey is a delay the reader cannot explain, and a
  clock in the past says nothing.
- A BPDU emission drained from a switch is a transmission on its port and
  takes the busy clock; a wake-up itself is not a transmission. Why: one
  rule for every frame.
- The README's example cable between the switches becomes 300 m of
  multimode fiber, since 300 m of twisted pair is past reach; its numbers
  are recomputed below. Why: the example must run.

## Requirements

Every example uses the fabric README's topology: `h1` on `sw1:1/1/1`
untagged in VLAN 10 over a 0 m cable, `sw1:1/1/24` to `sw2:1/1/24` tagged
VLAN 10 over 300 m of multimode fiber, `sw2:1/1/1` to `h2` over a 0 m
cable, every link at 1 Gbit/s, and a frame from `h1` with a 46-octet
payload (60 octets encoded, 84 on the wire untagged, 64 encoded and 88 on
the wire with the trunk's C-tag). Serialization at 1 Gbit/s is 672 ns
untagged and 704 ns tagged; propagation over the fiber is 1494 ns.

1. Serialization plus propagation. Acceptance: the journey has a crossing
   for the host leg with `Serialization` 672 ns, `Wait` 0, `Latency` 0,
   the hop on `sw1` at `t0 + 672 ns`, a crossing for the trunk with
   `Serialization` 704 ns, `Latency` 1494 ns, the hop on `sw2` at
   `t0 + 2870 ns`, and the delivery to `h2` at `t0 + 3542 ns`.
2. Delay override on a link that negotiates. Acceptance: the trunk with
   `Delay` 10 µs puts the hop on `sw2` at `t0 + 11,376 ns`; with `Delay`
   0 at `t0 + 1376 ns`.
3. Singlemode fiber at 10 Gbit/s. Acceptance: the trunk as 10,000 m of
   singlemode fiber between ends whose `SupportedSpeedsBPS` are
   {1 Gbit/s, 10 Gbit/s} negotiates 10 Gbit/s (10,000 m is within the 10
   km row); the trunk crossing has `Serialization` 71 ns (704 bits at 10
   Gbit/s, 70.4 rounded up) and `Latency` 49,786 ns.
4. The busy clock. Acceptance: two frames injected from `h1` and from a
   third host `h3` on `sw1:1/1/2` (VLAN 10, 0 m) at `t0`, both unknown
   unicast, arrive at `sw1` at `t0 + 672 ns` and flood to the trunk; the
   trunk crossings have `Wait` 0 and 704 ns, the hops on `sw2` are 704 ns
   apart, and `Snapshot().Busy[sw1:1/1/24]` after the two steps is
   `t0 + 672 + 1408 ns`.
5. Reach bounds negotiation. Acceptance: 120 m twisted pair between two
   ends supporting {1 Gbit/s}: both link ends `Down` with `reach-exceeded`;
   90 m multimode fiber between ends supporting {1, 10 Gbit/s} negotiates
   10 Gbit/s; 400 m negotiates 1 Gbit/s; 600 m is `reach-exceeded`; 0 m
   twisted pair between the same ends negotiates 10 Gbit/s; 300 m twisted
   pair between two undeclared ends (hosts) is `reach-exceeded`, and
   `Inject` from such a host returns an error naming `reach-exceeded`.
6. Host ends serialize. Acceptance: two injections from `h1` at `t0`
   arrive at `sw1` at `t0 + 672 ns` and `t0 + 1344 ns`, and the second
   journey's host crossing has `Wait` 672 ns.
7. Diff and Validate. Acceptance: `Diff` of a cable changed from twisted
   pair to singlemode fiber reports field `medium` with those values, and
   a `Delay` set from nil to 1 µs reports field `delay` from nil to 1 µs;
   `Validate` refuses `Delay` of -1 ns and medium `"coax"` and accepts an
   empty medium.
8. Records. Acceptance: the fabric README's journey shows the numbers of
   requirement 1 with the formulas, its medium table lists the factors and
   reach rows, and the direction record's cable and run bullets read as
   the parent's last Decision says.

## Out of scope

Everything the parent lists, and the switch's own forwarding latency,
which stays zero.

## Units

### U1. Media, reach, and the cable fields

Files: `src/common/netsim/fabric/medium.go`, `medium_test.go`,
`config.go`, `diff.go`, `config_test.go`, `compare_test.go`
After: none
Change: `Medium`, its four values, `VelocityFactor`, `Reach`, and
`Propagation` as the Decisions say; `Cable.Medium` and `Cable.Delay`;
`Clone`, `Validate`, and `Diff` cover them; `ReasonReachExceeded`
declared beside the fabric's other reasons.
Tests: `medium_test.go`, every velocity factor and reach row spelled out,
`Propagation` for 300 m twisted pair (1564 ns), 100 m (521 ns), 300 m
multimode (1494 ns), 10 km singlemode (49,786 ns), 3 m twinax (13 ns),
and 0 m (0); `config_test.go`, the `Validate` cases of requirement 7;
`compare_test.go`, the two `Diff` changes of requirement 7. Each is
evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U2. Reach in link resolution

Files: `src/common/netsim/fabric/fabric.go`, `link_test.go`,
`run_test.go`, `compare_test.go`, `counters_test.go`, `config_test.go`,
`README.md`
After: U1
Change: `resolveLink` runs the reach check of the Decisions and passes
the cap to `Negotiate`; every existing test topology whose cable is
longer than 100 m (`run_test.go` line 86, `compare_test.go` line 83,
`counters_test.go` lines 205 and 210, `config_test.go` line 295) and the
README's example set `Medium: fabric.MultimodeFiber`, so they keep
linking; the README's link-state section gains the `reach-exceeded` row.
Tests: `link_test.go`, requirement 5's seven cases, and a forced 1 Gbit/s
end on 400 m multimode fiber staying up while a forced 10 Gbit/s end on
the same cable is `reach-exceeded`. Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U3. Serialization, the busy clock, injection, and the journey

Files: `src/common/netsim/fabric/run.go`, `journey.go`, `run_test.go`,
`replay_test.go`, `stp_test.go`, `routing_test.go`
After: U2
Change: `wireOctets`, `serialization`, `busyUntil`, the transmission
timing of the Decisions, `Inject` as a host transmission with its
crossing entry and its no-speed error, `Entry.Serialization` and
`Entry.Wait`, `Snapshot.Busy`, deliveries at arrival time, and emissions
through the same path. Every existing test that asserts a 1501 ns
arrival, an immediate delivery, or the journey kinds of a host injection
moves to the new numbers and kinds.
Tests: `run_test.go`, requirements 1 through 4 and 6 with the crossing
entries, hop times, and `Busy` asserted, the `Inject` error of
requirement 5, and a BPDU emission's crossing carrying a serialization;
`replay_test.go`, a replayed run reproducing the same arrival times. Each
is evidence for this unit; the busy-clock case is watched failing first
by charging nothing.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U4. The records

Files: `src/common/netsim/fabric/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`,
`src/common/netsim/README.md`
After: U3
Change: the README's example, journey, snapshot, queue, and link-state
sections carry the two terms, the medium table with its factors and
reach rows, the override, the busy clock, and the host leg; the direction
record's cable bullet and its run bullet ("enqueues one copy per egress
cable at the arrival time plus the cable's latency") are rewritten as the
parent's last Decision says; the netsim README's one-line description of
`fabric` names timing.
Tests: none; the unit changes prose, and requirement 8 is read.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric docs/architecture
go test -race ./src/common/netsim/...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] The fabric README, the netsim README, and the direction record
      match the landed API and numbers.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for this phase filled.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Whether `Snapshot.Busy` should also carry the frame the port is
  sending. The plan says the time only; the journey has the frame.
