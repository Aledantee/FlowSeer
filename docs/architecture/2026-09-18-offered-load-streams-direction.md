---
title: Offered-Load Streams - Direction
type: direction
date: 2026-09-18
topic: offered-load-streams
status: proposed-direction
amends: docs/architecture/2026-09-10-virtual-device-direction.md
---

# Offered-Load Streams - Direction

## Context

A fabric run takes one frame per `Fabric.Inject` call
(`src/common/netsim/fabric/run.go`). The run already models what load acts
on: serialization at the negotiated rate, a busy clock per endpoint, strict
priority egress queues with a per-queue maximum rate, ingress policers, and
mirrors. It has no way to state load. A caller who wants 800 Mbit/s into a
gigabit trunk writes a loop of injections, and three properties of the engine
make that loop fail at the sizes a load question needs:

- Egress queues never fill. `enqueueEgress` appends without a bound, so an
  oversubscribed port delivers every frame late and drops none. "Delivered
  100%" at 2:1 oversubscription is a false answer.
- Every frame keeps a full `Journey` for the life of the run, with each
  entry's forwarding result, and `record` rebuilds `Fabric.Metadata()` for
  every entry. One second of 64-octet frames at 1 Gbit/s is 1,488,095 frames
  (84 octets on the wire each, the figure the cable-timing plan derived).
- The arrival queue is a sorted slice. Insertion is linear in the queue
  length, and eager injection of a stream puts the whole stream in it.

Traffic generators such as Ostinato and TRex describe load as streams: a frame
template, a rate, a burst shape, a count, and fields that vary per frame. The
lab has real switches, and netpen already transmits crafted frames on a NIC,
so a stream description that runs in the simulator and on a wire lets the two
be compared.

## Decision

- Load is stated as a stream. A stream is a plain Go value in
  `src/common/netsim/stream`: a frame template, a rate in frames or bits per
  second, a burst size and gap, a frame count or a duration, a start time,
  and a list of field variations. The package imports the value and codec
  packages under `src/common/net` and nothing from `fabric` or `vswitch`.
  Why: the same value has to be executable by an edge transmitter that has no
  simulator in its process.
- A stream is a source the run pulls from. The fabric holds attached sources
  and, before each step, injects every source frame whose time is not after
  the earliest queued arrival. Nothing expands a stream ahead of the clock.
  Why: memory stays flat in the stream length, and a source can end when its
  origin link goes down.
- Variation is deterministic. A field steps by increment or decrement, or
  draws from a generator the package owns, seeded by the stream spec. The
  generator is SplitMix64, pinned by known-answer vectors. The virtual device
  record's "nothing in a run is random" becomes "a run is a function of its
  configuration, its injections and streams, and their seeds". Why: `go doc
  math/rand/v2.PCG` promises no stable output sequence across Go releases, and
  a run that changes with the toolchain is not reproducible.
- An egress queue has a buffer when the configuration states one.
  `traffic.PortQueues` gains a per-PCP buffer size in octets. A frame that
  would exceed it is tail-dropped with reason `queue-full`. A queue with no
  stated buffer stays unbounded, reports its peak depth, and raises an
  `Incomplete` issue on its port scope once the depth passes one maximum-size
  frame, the same shape as `propagation-unknown`. Why: buffer sizes differ per
  vendor and are rarely reported, and an invented default would put a loss
  figure on the record that nothing measured.
- A frame's journey has a retention. `Injection.Retention` is `RetainJourney`
  (the zero value, today's behavior) or `RetainAggregate`. An aggregated frame
  names a flow; when its last copy settles, the fabric folds its outcome into
  that flow's statistics and frees the journey. Statistics per flow: frames
  offered, deliveries per host, drops per reason, cable losses, unresolved
  fates, latency from injection to each delivery as minimum, maximum, sum and
  count, and the merged trust metadata of every folded journey. Why: the
  planning answer is the aggregate, and the trust metadata has to survive the
  fold or an `Unknown` uplink would yield a confident throughput.
- Stream identity is metadata, not payload. The fabric knows each frame's flow
  from its injection. The on-wire transmitter needs a payload signature
  because a NIC does not; that signature belongs to the transmitter.
- A capture file is a stream source. `src/common/net/pcap` reads classic pcap
  and pcapng with `LINKTYPE_ETHERNET` into timestamped byte records, with no
  generated types, so `netsim` and the edge can both import it. The capture
  module's pcapng writer stays where it is; it renders `PacketRecord`
  protobufs and cannot move under `src/common`.
- The on-wire transmitter is an edge application, not part of `netsim`. It
  executes the same `stream` values with wall-clock pacing and counts what a
  second interface receives. `netsim` keeps its rule: no goroutines, no wall
  clock, no I/O.
- The engine scales by structure, not by approximation. The arrival queue
  becomes a heap with the same total order, and `record` reads a cached fabric
  metadata that `SetFault` and link changes invalidate. A fluid or aggregate
  flow model is not part of this direction.

## Alternatives

- Depend on Ostinato or TRex. Both are separate processes in C++ driven over
  RPC, neither runs inside a deterministic in-memory run, and Ostinato's
  binaries are paid. They remain useful as references for the stream shape.
- Expand a stream eagerly into injections. It needs no engine change, and it
  holds every frame and journey in memory and sorts them into a slice, which
  caps a run near 100,000 frames.
- A default buffer size as an opt-in assumption, in the manner of
  `PhyAssumption`. It gives every run a loss figure. It lost because no
  standard supplies the default; `PhyAssumption` fills values IEEE 802.3
  defines, and a buffer depth has no such source.
- A fluid model that moves rates instead of frames. It reaches 10 Gbit/s for
  minutes, and it cannot answer which frame a policer dropped or how a LAG
  bucket table spread a flow set. It is a separate simulator, not a mode.

## Consequences

- The virtual device record's run bullet, its cable bullet's "nothing in a
  run is random", and its "scenario overlays and search" gap change in the
  phase that lands each part.
- `fabric` imports `stream`. `stream` imports only `src/common/net`.
- A run of a million aggregated frames holds no per-frame state after it
  drains. A run that retains journeys behaves as it does today.
- The conformance corpus gains load cases: an oversubscribed trunk with a
  stated buffer, the same trunk with none, and a policed stream.
- Line rate at 10 Gbit/s for a minute is 10⁹ steps and stays out of reach.
  The README says so.
- Running one stream against the simulator and a lab switch, then comparing
  per-flow statistics, becomes the validation of the queue and policer model.
  Transmitting on the lab network needs the owner's approval per run.
