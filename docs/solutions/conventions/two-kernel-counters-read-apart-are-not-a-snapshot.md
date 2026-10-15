---
title: Two Kernel Counters Read One After the Other Are Not a Snapshot, So Cap the Derived Delta and Carry the Rest
date: 2026-10-15
category: conventions
module: src/modules/capture/rawsocket
problem_type: bug
component: freebsd-packet-io
severity: high
applies_when:
  - "Deriving one count from two cumulative counters the kernel or a device reports, such as received = Δrecv - Δdrop from BIOCGSTATS, or delivered = Δin - Δdiscards from interface statistics."
  - "Reporting counter deltas since the previous call with modular uint32 arithmetic, where an underflow reads as a counter wrap."
  - "A Stats call reports about 4 billion packets for one interval under packet loss."
related_components: [capture, bpfdev]
tags: [freebsd, bpf, counters, stats, delta, race, uint32]
symptoms:
  - "Stats returns received near 2^32 for one call while the interface is dropping packets."
root_cause: "The kernel fetches the two counters separately and without a lock, so a packet counted between the fetches appears in the second counter but not the first. The second delta then exceeds the first, and the modular subtraction wraps."
resolution_type: code_fix
---

## Situation

FreeBSD's local capture reports `received = Δbs_recv - Δbs_drop` and
`dropped = Δbs_drop` since the previous `Stats` call. Every drop is also a
receive, so the obvious formula assumes `Δdrop <= Δrecv`. Each counter wraps
at 2^32, so the deltas use modular `uint32` subtraction. That subtraction
cannot tell a real wrap from a negative result.

## What is true

`BIOCGSTATS` copies the two counters one after the other and holds no lock
(FreeBSD releng/14.3 `sys/net/bpf.c:1708-1710`):

```c
/* XXXCSJP overflow */
bs->bs_recv = (u_int)counter_u64_fetch(d->bd_rcount);
bs->bs_drop = (u_int)counter_u64_fetch(d->bd_dcount);
```

A packet increments `bd_rcount` when the tap sees it (`bpf.c:2326`, `2400`,
`2474`) and `bd_dcount` later, when `catchpacket` finds no buffer space
(`bpf.c:2595`, `2646`). A packet that arrives after the `bd_rcount` fetch and
is dropped before the `bd_dcount` fetch shows up as a drop with no receive.
The sample `{recv: 100, drop: 1}` after `{recv: 100, drop: 0}` then reports
received `4294967295`. This happens under drop pressure, which is when the
numbers matter.

## How to apply it

Compute each counter's delta modularly, so an independent wrap of either one
stays correct. Report at most the first delta as the derived part. Advance
the saved baseline of the second counter only by what was reported, so the
excess carries into the next sample and the totals still add up once the
first counter catches up (`src/modules/capture/rawsocket/stats_delta.go:17-25`):

```go
recvDelta := c.recv - prev.recv
dropDelta := c.drop - prev.drop
reportedDrop := min(dropDelta, recvDelta)
return uint64(recvDelta - reportedDrop), uint64(reportedDrop), counterPair{
	recv: c.recv,
	drop: prev.drop + reportedDrop,
}
```

The caller keeps the returned baseline, not the raw sample
(`src/modules/capture/rawsocket/local_freebsd.go:118-119`). Clamping to zero
without the carry would hide the overflow, but the next sample would then
count that receive a second time.

## Evidence

`TestCounterPairDeltaSinceCarriesPendingDrops`
(`src/modules/capture/rawsocket/stats_delta_test.go:62`) runs
`{100,0} -> {100,1} -> {101,1} -> {102,1}` and expects `(0,0)`, `(0,1)`,
`(1,0)`. It also covers drops pending across several samples and a drop
counter that wraps before the receive counter. Under the old formula every
case failed, for example `--- FAIL:
TestCounterPairDeltaSinceCarriesPendingDrops/drop_appears_before_receive`.
The FreeBSD packet I/O record states the resulting contract
([direction record](../../architecture/2026-10-12-freebsd-packet-io-direction.md),
"Local capture computes each counter's delta").

## What it does not cover

The Linux reader also subtracts, `Packets - Drops`, but the kernel folds the
drops into the packet count before it copies the pair out, so `Packets` can
never be the smaller one (`src/modules/capture/rawsocket/local_linux.go:46-52`).
The mirror receiver counts in userspace with one atomic counter. Neither
engages this mechanism. A counter pair read from a device over SNMP or gNMI has the
same hazard whenever the two values come from separate reads.
