---
title: Remote Packet Capture Phase 3c, Edge Capture Wiring - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
---

# Remote Packet Capture Phase 3c, Edge Capture Wiring - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The agent host assembles the capture engine and runs a capture end to end: it
holds a capture-assignment stream open on the U3a transport, drives a session
from a received `CaptureSessionConfig` through `src/modules/capture`, and
uploads chunks on `UploadCapture` with a fresh assertion inside every window,
until a budget stops it. The means is a capture handler and an upload client in
the agent, assembled by `host` as a `service.Module` beside the access lane and
its dispatch loop. The proof is the host end-to-end test standing a live device
service next to a live agent and reading back the artifact — the same harness
the agent's enrollment and dispatch tests already use. This phase is wrong if
the capture module's engine cannot be driven from a long-lived goroutine under
the host's supervision tree without blocking the dispatch loop, which would
mean the engine needs a different concurrency contract than U2 gave it.

## Decisions

The parent plan's Decisions apply; this phase carries out its edge-wiring
decision and depends on U3a's transport and U3b's schema and central leg. What
this phase decides when re-planned:

- How the capture handler maps an assignment to an engine run and a
  lifecycle report: which goroutine owns the engine, how a stop assignment
  cancels a running capture, and how the session's terminal state is reported
  back so central releases the assignment.
- How the upload client interleaves chunks and re-assertions on the
  `UploadCapture` stream, and where the re-assertion interval is configured
  relative to the sixty-second window.
- Where capture's own `Contact` metrics and events are named, following the
  dispatch loop's pattern from a capture namespace rather than reusing the
  dispatch names.

## Requirements

Carried from the parent: R2 (a filter compiles to a cBPF program the kernel
accepts — exercised here end to end rather than in the engine's unit test), R3
(the receiver's decapsulation, likewise end to end), R4 (a budget stops the
capture and the lifecycle reaches `COMPLETED` with the stop reason), R7 (the
edge sends a fresh assertion inside the window, and stops uploading when the
stream closes), and R8 (no captured bytes in the agent's telemetry).

## Out of scope

- The schema, central leg, and store: U3b landed them.
- The subscribe-loop transport: U3a landed it; this phase only constructs a
  second `subscribeloop.Run` over the assignment message type.
- Lab validation against real switches: that is U3d. This phase proves the
  round trip against the containerised senders and the live-central test
  harness only.

## Open questions

- Does the host end-to-end test have, or can it cheaply gain, a local
  packet source the capture engine can read so a session runs to a budget
  without a real interface — a loopback or a veth pair the test sets up?
- How does the agent bound a capture that never reaches its budget because no
  packets arrive, distinct from the budget stop, so a stuck capture is
  reported rather than held open forever?
- Is the capture module assembled unconditionally, or only when the edge's
  config enables it, given not every edge deployment runs captures?
