---
title: Offered-Load Streams Phase 5 - On-Wire Transmitter and the Lab Comparison - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
parent: docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-plan.md
---

# Offered-Load Streams Phase 5 - On-Wire Transmitter and the Lab Comparison - Plan

## Goal

An edge application sends finite `stream.Source` values through one explicitly
named NIC, receives their signed frames through a second NIC, and reports each
flow beside the same source's `fabric.FlowStats`. The application uses wall-clock
pacing and a Linux packet socket; an opt-in comparison runs through the owner's
ICX7150 on the isolated lab network. This plan is wrong if the lab host, its two
NICs, or the kernel capture path cannot send and observe 10,000 frames at 1,000
frames per second within 1% of the source's scheduled first-to-last interval
without an interface-reported capture drop.

## Decisions

- The parent plan and
  `docs/architecture/2026-09-18-offered-load-streams-direction.md` apply. Phase 5
  depends on the landed stream phase, P3, and not on the still-independent pcap
  source phase, P4. Why: the parent graph names `After: P3` for P5, and the
  transmitter consumes `stream.Source` directly.
- The application is `src/edge/netsimload` in the root Go module, with binary
  `src/edge/netsimload/cmd/netsimload`. It is not a netpen subcommand. Why:
  netpen's nested module can import the root module through its existing
  `replace`, but `src/edge/README.md` defines netpen as an operator security
  audit and attack tool and isolates its gopacket and terminal dependencies.
  Offered-load execution is a separate edge role and already belongs to the
  module that owns `stream` and `fabric`.
- The transmitter gets a send-only packet-socket package under
  `src/edge/netsimload/packetio`; it does not import or move
  `src/edge/netpen/link`. The receiver reuses
  `src/modules/capture/rawsocket.OpenLocalInterface` with promiscuous mode off.
  Why: `netpen/link.Leg` couples transmit to a gopacket receive ring and exposes
  neither receive timestamps nor interface-drop counters. The capture raw
  socket already provides both, while a small `golang.org/x/sys/unix` sender
  keeps gopacket inside the boundary enforced by
  `src/common/internal/netpenguard`.
- A run configuration names distinct transmit and receive interfaces; neither
  has a default. The application resolves both interfaces, checks that they are
  up, and validates every source before opening either socket. Received
  signatures name one of the configured nonzero flow IDs or are counted as
  malformed without entering a flow. Why: netpen's
  `eth0` default is unsafe for a load tool, and the owner must see the complete
  interface pair in the blast radius before any I/O begins.
- All flows share one monotonic epoch. The runner holds one head frame per
  source, sends the earliest `Source.Next` offset, and breaks equal-offset ties
  by ascending flow ID. It waits until `epoch + offset`, stamps the actual
  software submission time immediately before `Send`, and stops on the first
  send, receive, or context error. Why: the source contract returns offsets
  relative to its consumer's epoch, and a deterministic tie-break makes a
  two-flow run repeatable. A transmit timestamp proves userspace submission to
  the kernel, not physical NIC departure; receive timestamps and the observed
  first-to-last interval are the pacing evidence.
- The signature occupies the first 32 payload octets without changing frame
  length: bytes `[0:4]` are ASCII `FSLD`, `[4:8]` are big-endian version `1`,
  `[8:12]` are the nonzero `fabric.FlowID`, `[12:16]` are zero, `[16:24]` are a
  zero-based sequence, and `[24:32]` are signed Unix nanoseconds at submission.
  The runner preflights a clone of each source and refuses any yielded frame
  whose payload is shorter than 32 octets. Why: simulator flow identity remains
  metadata, while the receiver needs an on-wire join key. Overwriting reserved
  bytes keeps bit-rate spacing based on `Frame.WireOctets` true; prepending a
  header after the source has calculated its offsets would not. The literal
  layout test follows
  `docs/solutions/conventions/a-codec-round-trip-cannot-locate-a-field-on-the-wire.md`.
- Lab statistics keep their observation domain explicit. Per flow they report
  successful sends, unique sequences received, missing sequences including a
  missing tail, duplicates, reordering, and software submission-to-capture
  latency. They do not put missing sequences into `fabric.FlowStats.Lost`, which
  means cable loss specifically, and they do not invent a switch drop reason.
  The comparison shows the simulator's full `FlowStats`, `Metadata.Status()`,
  and every `Metadata.Issues()` entry beside the lab counters, then adds a
  normalized offered/delivered/unreceived row for the named destination host.
  Why: the two sides can compare outcomes while preserving provenance and the
  simulator's trust limits.
- Tests use injected clocks, senders, and receivers for pacing and failure
  paths. Linux packet-socket coverage uses an explicit integration tag, and the
  ICX7150 comparison has its own live tag. Why: default tests must not need root,
  `CAP_NET_RAW`, real interfaces, or a powered switch.
- **LIVE-DEVICE:** the ICX7150 unit performs a live-device operation. Before
  every run, the implementer states the blast radius and waits for the owner's
  explicit go-ahead. The statement names the two host NICs, ICX7150 ports and
  VLAN, source and destination MACs, EtherType, the 32-octet signature, frame
  size, maximum count, aggregate rate, duration, stop conditions, and whether
  any switch configuration changes. The planned comparison makes no switch
  configuration change: it sends two bounded runs of 128-wire-octet unicast
  frames, at most 20,000 frames and 2.56 MB on the wire over about 20 seconds,
  then closes both sockets. Its lasting switch effects are ordinary MAC-table
  and counter updates. The owner needs advance notice to power on the normally
  off switch. An unreachable switch is reported once and not retried. This note
  is enforced when the live unit runs; it does not block planning or the
  non-live units.

## Requirements

16. The edge runner executes each `stream.Source` at its stated offsets. For
    example, a 10,000-frame source at 1,000 frames per second produces
    sequences 0 through 9,999; on the ICX7150 path the receive timestamps span
    the ideal 9.999 seconds within 1% and the receiver reports zero interface
    drops. A canceled wait or a send error stops the run without sending a later
    frame.
17. The receiver separates signed flows and accounts for sequence history. For
    example, flow 7 receiving `0, 1, 1, 3, 2` after four successful sends
    reports four unique deliveries, no missing frame, one duplicate, and one
    reordered frame; if sequence 3 never arrives it reports one missing tail.
    A bad magic, version, reserved word, timestamp, or short payload is ignored
    and counted as malformed without being assigned to a flow.
18. A comparison report preserves both observation domains. For example, a
    simulated flow with 10,000 offered, 9,990 delivered to `rx`, ten
    `queue-full` drops, and an `Incomplete` issue is printed beside a live flow
    with 10,000 successful sends, 9,992 unique receives, eight missing
    sequences, capture-drop count zero, and software latency. The normalized
    row says 10 versus 8 unreceived; it retains the simulator issue and does not
    label the eight live misses `queue-full` or cable loss.

## Out of scope

- Packet generation outside finite `stream.Source` values, sustained 10 Gbit/s,
  hardware transmit timestamps, and a fluid traffic model.
- Reusing netpen's attack catalog, runner safety classes, output UI, gopacket
  link, or release artifact.
- Stamping arbitrary protocols. A transmitted frame reserves the first 32
  payload octets; a size variation must preserve them. UDP application-payload
  placement and signatures outside that region need a later contract.
- Inferring queue depth, policer configuration, or switch drop reason from
  sequence gaps. A comparison against an unstated ICX7150 buffer remains
  `Incomplete` and says so.
- Configuring the ICX7150, changing its VLANs or QoS, or retaining captured
  payload. The receiver decodes the signature and discards frame bytes.

## Units

### U1. Wire transport and observation contract
Files: `src/edge/netsimload/packetio/doc.go`,
`src/edge/netsimload/packetio/sender.go`,
`src/edge/netsimload/packetio/sender_linux.go`,
`src/edge/netsimload/packetio/sender_other.go`,
`src/edge/netsimload/packetio/sender_test.go`,
`src/edge/netsimload/packetio/sender_linux_test.go`,
`src/edge/netsimload/signature.go`,
`src/edge/netsimload/signature_test.go`,
`src/edge/netsimload/stats.go`, `src/edge/netsimload/stats_test.go`,
`src/edge/netsimload/report.go`, `src/edge/netsimload/report_test.go`
After: none
Change: `packetio.OpenSender(interfaceName)` returns a concrete sender bound to
that Linux interface. `Send(ctx, frame)` writes one complete Ethernet frame,
checks cancellation before the write, and returns the kernel write error;
`Close` is idempotent. Non-Linux builds return the same unsupported-platform
shape as capture raw sockets. The package uses `x/sys/unix`, not gopacket, and
does not enable promiscuous mode or open a receive ring. Beside it, the
signature codec writes the fixed 32-octet layout into a copied frame and
decodes only that version and zero reserved word. The accumulator tracks
successful sends and unique, missing, duplicate, reordered, malformed, and
late-after-close receives per flow, plus latency and the receiver's
interface-drop counter. The report projects `fabric.FlowStats` through its
public accessors and emits deterministic JSON with raw simulator and lab
statistics, normalized destination results, status, issues, and the timing
limitations above.
Tests: package tests inject the socket operations and prove interface binding,
complete-frame writes, pre-write cancellation, short-write and syscall errors,
idempotent close, and unsupported platforms. A Linux-only link test behind
`netsimload_linktest` sends a literal frame across an operator-provided veth or
dedicated interface pair; unset interface configuration skips before opening a
socket. A hand-written byte vector pins every signature offset and byte order;
round trips cover remaining values only. Table tests cover zero flow, sequence
overflow boundaries, short frames, wrong magic/version/reserved word, negative
or future timestamps, interleaved flows, duplicates, reordering, interior and
tail gaps, malformed unrelated traffic, receive-after-close, latency
min/max/sum/count, capture drops, stable JSON order, and preservation of every
simulator issue. The comparison test proves it never maps a live missing
sequence to a simulator drop reason or `FlowStats.Lost`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netsimload/packetio src/edge/netsimload/signature.go src/edge/netsimload/signature_test.go src/edge/netsimload/stats.go src/edge/netsimload/stats_test.go src/edge/netsimload/report.go src/edge/netsimload/report_test.go`

### U2. Paced edge runner and command
Files: `src/edge/netsimload/run.go`, `src/edge/netsimload/run_test.go`,
`src/edge/netsimload/cmd/netsimload/main.go`,
`src/edge/netsimload/cmd/netsimload/main_test.go`,
`src/edge/netsimload/README.md`, `src/edge/README.md`
After: U1
Change: `netsimload.Run` validates distinct named interfaces and preflights
cloned sources before opening the capture receiver and sender. It starts the
receiver through `src/common/spawn`, merges flow heads by offset and flow ID,
waits on an injected monotonic clock, stamps and encodes each frame, records a
send only after success, and drains the receiver for a bounded configured grace
period after the last send. The public input is `Config{TXInterface,
RXInterface, Drain, Flows []FlowSource}`, where each `FlowSource` holds a
nonzero `fabric.FlowID` and a fresh finite `stream.Source`. The `transmit`
command reads versioned JSON with the two interface names, drain duration, and
flows whose fields are `id`, an encoded Ethernet `frame_hex`, exactly one of
`frames_per_second` or `bits_per_second`, exactly one of `count` or
`duration`, `burst`, `gap`, `start`, and `seed`; it constructs each
`stream.Spec` and writes lab JSON. This command format deliberately carries
no polymorphic `Variation`; library callers can pass any finite source whose
yielded frames retain the signature reservation. `compare` reads simulator
and lab JSON and writes the combined report. Neither command has an interface
default or retries an unavailable interface.
Tests: fake clock, sender, and receiver tests prove exact due times, equal-time
tie order, bursts and gaps inherited from the source, no socket open after
configuration or preflight failure, receive-before-send ordering, successful
send accounting, prompt cancellation, first-error propagation, bounded drain,
goroutine completion, close-error handling, and two interleaved flows. Command
tests prove missing, equal, down, and unknown interfaces fail before the opener;
duplicate or unknown flow IDs and an invalid JSON stream shape are refused;
JSON stdout is clean, diagnostics use stderr, and `compare` preserves issues
and observation labels. The README shows a 10,000-frame, 1,000-fps run, required
capabilities, signature reservation, blast-radius checklist, and the difference
between send, receive, and simulated timestamps.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netsimload/run.go src/edge/netsimload/run_test.go src/edge/netsimload/cmd/netsimload/main.go src/edge/netsimload/cmd/netsimload/main_test.go src/edge/netsimload/README.md src/edge/README.md`

### U3. ICX7150 simulator-to-lab comparison
Files: `src/edge/netsimload/test/integration/doc.go`,
`src/edge/netsimload/test/integration/lab_test.go`,
`src/edge/netsimload/test/integration/README.md`
After: U2
Change: an opt-in `netsimload_lab` test builds a one-switch fabric with the
configured host-facing speeds, attaches the same cloned sources with
`RetainAggregate`, runs them through the named destination host, then runs the
live sources through two host NICs and LABSW06, the owner's ICX7150. One case is
10,000 frames at 1,000 frames per second; a second interleaves two 5,000-frame
flows at the same aggregate rate. The test prints the deterministic comparison
report and its measured pacing tolerance. Unset lab configuration skips before
I/O; malformed configuration fails; switch unreachability stops without retry.
Tests: the live case asserts the 1% first-to-last pacing bound, no capture
interface drops, exact per-flow send accounting, signature separation, and a
report containing every simulator issue. It accepts a nonzero sequence gap as
an observed result and reports it; it does not convert that gap into a switch
reason. Before invocation, the implementer gives the LIVE-DEVICE blast-radius
statement above, gives advance power-on notice, and records the owner's explicit
go-ahead in the implementation handoff.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netsimload/test/integration`

Waves: U1 | U2 | U3

## Verification

- `go test -race ./src/edge/netsimload/...`
- `go test -race ./src/common/netsim/... ./src/modules/capture/...`
- `go test -race ./...`
- On Linux with an isolated local interface pair:
  `go test -tags=netsimload_linktest -run TestRealInterface ./src/edge/netsimload/packetio`
- **LIVE-DEVICE, only after the blast-radius statement, advance power-on notice,
  and the owner's explicit go-ahead:**
  `go test -tags=netsimload_lab -run TestICX7150Comparison -v ./src/edge/netsimload/test/integration`
- Run each unit's diff-aware verifier on every changed path.

## Definition of done

- [ ] The verifier passes for every changed path, both default race suites are
      green, and the Linux packet-socket build and tagged link test pass.
- [ ] The approved ICX7150 run records its exact blast radius, measured pacing,
      interface-drop count, per-flow results, simulator status and issues, and
      the report location. A failed physical pacing gate is an outcome, not a
      reason to weaken the 1% requirement.
- [ ] `src/edge/README.md` and the application README describe the new edge role,
      privileges, supported signature shape, and live-device gate.
- [ ] This plan's `status` becomes `implemented` with an outcome note below its
      title after code lands; the parent P5 `Landed:` line is filled, and no plan
      labels enter code, comments, test names, or commit messages.

## Open questions

None for implementation. The offered-load direction record remains a proposed
parent-plan decision; this phase follows it and does not amend an accepted
record. Physical interface names, switch ports, and VLAN are required live-run
inputs named in the approval statement, not design choices.
