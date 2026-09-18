---
title: Remote Packet Capture Phase 3b, Command Channel and Central Capture Leg - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
---

# Remote Packet Capture Phase 3b, Command Channel and Central Capture Leg - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

An operator's `CreateCaptureSession` on the device service reaches the edge as
an assignment, and the packets the edge uploads come back to that operator. The
means is one new RPC and a capture leg on central: `CaptureEdgeService` gains an
edge-called `SubscribeCaptureAssignments` server-stream beside `UploadCapture`;
the device service serves the operator `CaptureService`, turns a created session
into an assignment on that stream, receives `UploadCapture` under the
re-assertion rule, stores the pcapng, and serves `TailCaptureSession` and
`DownloadCaptureSession`. This phase is wrong if the device service's
supervision and store shapes cannot hold a multi-megabyte artifact under the
capture direction's bounded-retention rule without a persistence decision of
its own, in which case the store is its own plan before this one.

## Decisions

The parent plan's Decisions apply; this phase carries out its command-channel
and central-storage decisions. What this phase decides when re-planned:

- The assignment message shape: an assignment stream carrying a start
  (`CaptureSessionConfig`, which already holds the ref, source, filter, budget,
  and authorization) and a stop (`CaptureSessionGlobalRef`), and how a
  duplicate or superseded assignment is answered, reusing the dedup reasoning
  the dispatch registry already documents rather than inventing a second one.
- Where central's capture leg sits in the device service: a new
  `internal/captureapi` beside `internal/dispatchapi`/`internal/edgeapi`, how
  the operator `CaptureService` and the edge `CaptureEdgeService` are both
  served from one host, and how an assignment is originated and re-originated
  under the same re-dispatch-until-reported discipline central already runs.
- The artifact store: where the pcapng bytes live, how expiry deletes the
  payload while the session record and counters survive (the capture
  direction's departure from retire-is-not-purge), and how `TailCaptureSession`
  and `DownloadCaptureSession` read it. This is the decision that may need its
  own direction record; the re-plan rules on that first.

## Requirements

Carried from the parent: R1 (a budgetless session is refused), R4 (a capture
stops at its first satisfied budget and reports why, now observed through
central's session state), R5 (loss is attributable through the counters a tail
reads), R6 (the stored artifact is a pcapng `capinfos` reads), R7 (an upload
stream that stops re-asserting is closed — the server side of this rule lives
here), and R8 (captured bytes never reach telemetry, now on the central leg
too).

## Out of scope

- The edge side of the assignment loop and the upload client: that is U3c.
- Lab validation: that is U3d.
- Indexing or search across many sessions, and fan-out of one tail to more
  than one consumer (parent Out of scope).

## Open questions

- Does the artifact store need its own direction record, written against the
  device service record's storage decisions, or does the capture direction's
  amendment cover it? The re-plan decides this before writing units.
- How is an assignment de-duplicated when central re-originates one for a
  session it has no terminal report for, given a capture's lifecycle differs
  from a device mutation's submit-and-terminate shape?
- What binds a `CaptureSessionConfig` to the edge that must run it on the
  assignment stream — the `EdgeGlobalRef` the config already carries, matched
  against the assertion the stream opened with, and refused when they differ?
