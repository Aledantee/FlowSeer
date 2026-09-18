---
title: Offered-Load Streams Phase 4 - Capture File as a Stream Source - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-plan.md
---

# Offered-Load Streams Phase 4 - Capture File as a Stream Source - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A pcap or pcapng file of Ethernet frames replays into a fabric at a chosen
origin with its recorded spacing. The means: a reader under
`src/common/net/pcap` and an adapter in `stream` from records to a `Source`.
The plan is wrong if the captures worth replaying are Linux cooked captures
(`LINKTYPE_LINUX_SLL2`, 276), which carry no Ethernet header to replay.

## Decisions

- The parent's decisions and the direction record apply.
- The reader is the repository's own code and returns `Record{At time.Time,
  Data []byte, OrigLen uint32}` plus the link type. Why: the main module has no
  pcap dependency, `gopacket` is pinned only in the netpen module
  (`src/edge/netpen/go.mod:17`), and `src/common` takes no generated types.
  The two-consumer test of `src/common/README.md:22-24` is met by `netsim` and
  by the phase 5 transmitter.
- Formats: classic pcap in both byte orders with microsecond and nanosecond
  magic, and pcapng Section Header, Interface Description, and Enhanced Packet
  blocks with `if_tsresol`. Other blocks are skipped. The re-plan fetches
  draft-ietf-opsawg-pcapng and the pcap file format draft and cites sections.
- A record shorter than its original length (a snap length cut) is refused by
  the adapter, not padded. Why: a truncated frame serializes in less time than
  the real one and would understate load.
- Tests decode fixture bytes whose fields were checked by hand against a hex
  dump, per
  `docs/solutions/conventions/a-codec-round-trip-cannot-locate-a-field-on-the-wire.md`.
  The capture module's writer is a second, independent encoder, so one test
  reads a file it wrote; that is a cross-check, not the wire proof.
- Fixtures are new small files under the package's `testdata/`. The netpen
  captures are not reused. Why: they belong to a separate module and their
  content is crafted protocol frames, not a load.

## Requirements

14. A hand-checked classic pcap of three frames yields three records with the
    expected timestamps to the microsecond and the expected first 14 octets; a
    pcapng file with `if_tsresol` 9 yields nanosecond timestamps; a file with
    link type 276 is refused with the link type named.
15. A capture of two frames 250 µs apart, attached at `h1` with start `t0`,
    injects at `t0` and `t0 + 250 µs`; a capture with a truncated record is
    refused at attach.

## Open questions

- Whether replay rewrites source MACs to the origin host's. A capture taken on
  a trunk carries many sources, and `Inject` at a host keeps the frame's
  addresses, so the default is no rewrite.
- Whether `ethernet.Decode` needs to strip a trailing FCS some captures carry;
  it has no FCS handling today (`src/common/net/ethernet/ethernet.go:148-189`).
