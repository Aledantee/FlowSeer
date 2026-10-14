---
title: Bounded backpressure on the central ingest streams
date: 2026-10-14
status: research; source reading of nats-server v2.15.0 and nats.go v1.54.0, two probe files run on an in-process server, and an audit of the buffers ahead of the central streams
---

# Bounded backpressure on the central ingest streams

A typed ingest stream today removes its oldest records at an age or byte
bound, and `src/services/device/internal/historysink/sink.go` samples the
gap below its consumer's floor as a discard count. This note covers the
contract that replaces both: a central ingest stream refuses a new record
when it is full, never removes a record on its own, and loses a record only
after every consumer resolved it. Line numbers refer to
`github.com/nats-io/nats-server/v2@v2.15.0/server/` and
`github.com/nats-io/nats.go@v1.54.0/jetstream/` in the Go module cache, the
versions `go.mod` pins. In-tree line numbers refer to the tree this note
landed in.

## What the server does

| Fact | Source |
| --- | --- |
| A stream with the discard-new policy refuses a publication that would pass its byte bound, and stores nothing. | `filestore.go:5254-5281` |
| The client reads that refusal as API code 503, error code 10077, description `maximum bytes exceeded`. The code is the general store failure, so the description is what tells a full stream from another store error. | `jetstream_errors_generated.go:901`, `store.go:52`, probe 1 |
| Age expiry runs only when the stream has a maximum age or per-message lifetimes. The discard policy does not affect it. | `filestore.go:7202-7203`, `filestore.go:7223-7226`, `filestore.go:7288-7300` |
| A limits stream keeps an acknowledged record. Nothing removes it but a bound or a request. | `stream.go:9310-9322` |
| An update may change the discard policy, the maximum age, and the duplicate window. It may not change limits retention into a work queue. | `stream.go:2553-2558`, probe 1 |
| The duplicate window is capped by the maximum age only when an age is set. | `stream.go:2007-2008`, probe 1 |
| A purge request with a sequence removes every record below it. Sequence 1 removes nothing. A request with no sequence and no keep count removes every record. | `jetstream_api.go:543-552`, `filestore.go:10317-10328`, probe 2 |
| The client's `WithPurgeSequence(0)` sets nothing, so it sends the request that removes every record. | `jetstream_options.go:104-111`, probe 2 |
| The acknowledgement floor moves only when the acknowledged record is the next one above it, then stops below the next pending record. With nothing pending it equals the delivery position. | `consumer.go:3875-3891` |
| A termination resolves a record through the same path as an acknowledgement, so the floor passes it. | `consumer.go:3329-3360`, probe 3 |
| A consumer created on a stream whose first sequence is above 1 starts with its floor one below that sequence. | `consumer.go:6477-6479`, probe 3 |
| A consumer reset request moves a deliver-all consumer to any sequence, forwards or backwards. | `jetstream_api.go:916-919`, `consumer.go:2922-2932`, `consumer.go:3004-3005` |
| A stream that sources another stores each record through the same check, and on a full stream it recreates its source consumer at the refused sequence. | `stream.go:4926-4948` |
| A source consumer acknowledges nothing, so the stream it reads keeps no record of what its reader holds. | `stream.go:4329-4340` |
| A retry of a record the stream already holds is answered as a duplicate before the byte check, so a successful publish does not show that a full stream has room. | `stream.go:6953-6970` |
| `CreateConsumer` returns the existing consumer when the config matches and `ErrConsumerExists` when it differs. It never updates one. | `stream.go:336-346` in nats.go |

## Probes

Both files run against an in-process server with every write synced, which
is central's policy (`src/modules/edgebus/fsync.go:24`). To rerun one, copy
it into a temporary directory inside the module under a name ending in
`_test.go`, run `go test -race -v ./<dir>/` from the repository root, and
delete the directory. Both files pass against the `go.mod` pins on macOS.

[`bounded-backpressure/prefix_probe_test.go.txt`](bounded-backpressure/prefix_probe_test.go.txt):

| Schedule | Result |
| --- | --- |
| Limits retention, discard-new, no maximum age, room for three records. Two connections share one durable consumer. A holds record 1, a peer acknowledges 2. | The floor stays 0 and a fourth publication is refused. |
| A acknowledges 1, then one purge at the floor plus one. | The floor is 2. Records 1 and 2 are gone, 3 remains, and the fourth publication is accepted. |
| Rolling limits stream, records 1 to 3, A holds 1, a peer acknowledges 2, purge up to 4. | The reply counts 3 removed, of which one was already stored by the peer. A purge count is not a loss count. |
| Control: a work-queue stream with discard-new and a 200 ms maximum age. | The unacknowledged record expires. Discard-new alone does not keep accepted work. |

[`bounded-backpressure/cleanup_probe_test.go.txt`](bounded-backpressure/cleanup_probe_test.go.txt):

| Case | Result |
| --- | --- |
| 1. A rolling stream with a 72 hour maximum age and two records is updated to discard-new with no maximum age and a ten minute duplicate window, then filled. | The update succeeds and keeps both records. The refusal is code 503, error code 10077, `maximum bytes exceeded`, and the first sequence is still 1. |
| 2. Purge at sequence 1, then at sequence 0, on three unacknowledged records. | Sequence 1 removes none. Sequence 0 removes all three. |
| 3. Five records. One consumer acknowledges 1, terminates 2, holds 3, and acknowledges 4. Two purges at the floor plus one run at once. The server restarts. The consumer is deleted and created again. | The floor is 2. Both purges succeed and leave 3, 4, and 5. After the restart the first sequence is 3 and the floor is 2. The new consumer starts at floor 2 with three records to deliver. |

## The cleanup rule

A stream's members are the durable consumers named for it in code. A cleaner
reads the floor of every member and purges at the lowest floor plus one. Why
each part holds:

- Every record at or below a member's floor was acknowledged or terminated
  by that member, provided nothing but a resolution moves the floor. That
  needs explicit acknowledgement, unlimited deliveries, and a stream that
  removes nothing itself. A consumer with a delivery limit drops a record it
  gave up on, and the floor then passes a record nobody stored.
- A consumer with a subject filter reports a floor over the records it
  matches. Members have no filter, so one floor speaks for every record
  below it.
- Validating the consumers a listing returns does not close the set. A
  consumer created between the listing and the purge could want the prefix
  the purge removes. Membership closes it: the hub creates every member
  with the stream, before a record is accepted, and the cleaner purges only
  when the consumers on the stream are exactly the members. A member that
  is missing stops cleanup, so a member created late finds every record
  stored since it went missing. A consumer that is not a member stops
  cleanup until a person removes it, and has no claim on a record purged
  before it existed.
- A member's current config says nothing about its past. A member updated
  to a filter, or to a delivery limit, moves its floor past records it never
  resolved, and an update back to the member config keeps that floor
  (`consumer.go:2694-2752`). The server refuses an update of the deliver
  policy or the acknowledgement policy (`consumer.go:2541-2568`). So code
  only creates a member, with the client's create-only call, and never
  updates one. A member found with another config stops cleanup until a
  person deletes it, and the member created after that is offered every
  stored record. A config changed by hand and changed back cannot be
  detected.
- A member is a name, not an incarnation. A member deleted and created
  again starts one below the stream's first sequence (probe 3), and records
  its earlier incarnation resolved stay resolved.
- Two cleaners may run at once. Each purges below a floor it read, floors
  only rise while members resolve records, and a purge below an older floor
  removes nothing new (probe 3).
- The reply to a purge is not used. A lost reply costs one repeated request.
- A floor of zero must end the pass before any request is built, since the
  client turns sequence 0 into a purge of everything (probe 2).
- An acknowledged record above a pending one stays stored. One record held
  by a sink keeps every later record on disk until it resolves, and the
  consumer's acknowledgement wait bounds how long a sink that died holds it.

What can still remove a record nobody stored, each an operator's request and
none of them counted: a purge or a message deletion sent by hand, a consumer
reset to a higher sequence, a stream deletion, a member's config changed by
hand, and a stream update that restores a maximum age or the discard-old
policy.

Not covered by a probe: a clustered hub, a power loss, the cost of one
purge over millions of records, and Linux timings.

## Buffers ahead of the central stream

| Stage | Bound and overflow | What a full central stream does to it |
| --- | --- | --- |
| UDP receive (`src/protocol/syslog/receiver_udp.go:20-49`) | A frame queue and a byte admission limit. A datagram that finds either full is dropped and counted. | Nothing reaches this far unless the edge buffer refuses, which it does not for capacity. |
| Edge source (`src/edge/agent/internal/syslogsource/source.go:278-300`) | Retries one refused publish forever with the same envelope and record id, and reads no further record meanwhile. | Unchanged. |
| Edge buffer (`src/modules/edgebus/leaf.go:91-92`, `leaf.go:188-206`) | 256 MiB and 7 days, oldest record removed first. It carries telemetry and ingest records together. | Unchanged. The hub keeps copying from it, since a source consumer acknowledges nothing. |
| Per-edge hub stream (`src/modules/edgebus/hub.go:150-151`, `hub.go:725-738`) | 64 MiB and 24 hours, oldest record removed first. | Intake's consumer holds its records pending. They wait here until these bounds remove them, uncounted. |
| Intake (`src/services/device/internal/intake/intake.go:197-215`) | Negatively acknowledges a delivery whose central publish failed and acknowledges after every publication. | It retries a refused record every retry delay for as long as the per-edge stream holds it. |
| Raw evidence stream (`src/modules/edgebus/hub.go:503-522`) | 64 MiB and 24 hours, oldest record removed first. It is a window of raw bytes kept for inspection, not accepted work. | Intake publishes evidence before the typed record, so each retry past the ten minute duplicate window stores the evidence again and pushes older evidence out. |
| Typed ingest stream | Bounded bytes, refuses when full. | Accepted work starts here. |

So a record is accepted work once the typed ingest stream stored it. Before
that it is in rolling buffers that hold it for a bounded time.

How the upstream bound meets the central horizon, with `c` the stored bytes
of a record as intake publishes it and `r` an edge's records per second.
`TestStoredBytesPerSyslogMessage` in `src/modules/edgebus/hub_test.go`
measures `c` at about 570 bytes for a syslog fixture published with the
message id header intake sets. The per-edge durations below use the same
figure and are upper bounds under two assumptions: the edge sends no
telemetry and no record carries raw bytes. The per-edge record's subject is
longer than the central one, and the server counts subject and header bytes
(`filestore.go:10055-10065`), so a per-edge record is larger still.

| Outage length | What happens |
| --- | --- |
| Up to the central horizon, `max_bytes_per_type / (c * fleet rate)` | Every record is stored centrally. Nothing is refused. |
| Past the horizon, for up to `min(24 h, 64 MiB / (c * r))` more per edge | Intake is refused and retries. The edge's records wait in its per-edge hub stream, which telemetry shares and can fill on its own. At one record per device per minute, that is at most 24 hours for an edge of up to 81 devices and at most 3.9 hours for an edge of 500. |
| Longer | Each per-edge hub stream removes its oldest records at the rate new ones arrive. Nothing counts them. |

The loss does not move further out, because the hub keeps draining the edge
buffer whatever intake does.

## The smallest restructure that would move the loss to the edge

Split each edge's hub stream by branch: one stream sourcing the telemetry
subjects and one sourcing the ingest subjects, the second bounded with
discard-new, read by intake as its only member, and cleaned by the rule
above. A full central stream would then fill the per-edge ingest stream,
the server would stop sourcing into it (`stream.go:4926-4948`), and the
backlog would wait in the edge buffer for up to 7 days or 256 MiB.

What it costs, read from the tree:

- It changes the per-edge stream the telemetry forwarder reads
  (`src/modules/edgebus/forwarder.go:148-160`).
- Each edge account holds two streams inside a 128 MiB budget that today
  covers one 64 MiB stream with margin (`src/modules/edgebus/hub.go:162-168`).
- A new ingest stream sources the edge buffer from its start, so every
  record still buffered at the edge is stored centrally again.
- One cleanup pass per attached edge.
- The server recreates the source consumer on every refused record, and its
  own comment marks that path for improvement (`stream.go:4926-4928`). No
  probe ran it across a leaf link.

Keeping one per-edge stream does not work: its two consumers are filtered,
and a forwarder that sees no telemetry would hold its floor and stop the
cleanup. Interest retention there would need both consumers to exist before
the first record is sourced, since a record nobody is interested in is not
stored (`stream.go:7188-7210`).

The split is a plan of its own, with the leaf-link probe as its first unit.
Until then the remedy for a central stream that fills is an operator's:
restore ClickHouse, or raise `max_bytes_per_type` and the central budget and
restart the hub, which widens the stream in place (probe 1 shows an update
keeps the records). Nothing here measures what interest or work-queue
retention would add to each acknowledgement.
