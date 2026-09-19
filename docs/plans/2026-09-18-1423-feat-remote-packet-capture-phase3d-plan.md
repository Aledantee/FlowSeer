---
title: Remote Packet Capture Phase 3d, Lab Validation - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
---

# Remote Packet Capture Phase 3d, Lab Validation - Plan

> Re-planned by plan when its turn comes and the lab has reopened; the tree
> will have moved. Blocked until then: the lab has been closed since
> 2026-09-10 ([the runbook](../runbooks/lab-icx7150-first-write.md)), and this
> phase's whole content is evidence from real hardware that no synthetic frame
> can stand in for. It is planned so the evidence the change still owes is
> named rather than forgotten, not because code is missing.

## Goal

The capture path is validated against the two lab facts the containerised
senders in U2 and the live-central harness in U3c cannot reach: an ICX7150
local SPAN into the edge's capture interface, and a MikroTik TZSP stream to the
edge's receiver, each producing an artifact `capinfos` reads. The means is a
recorded lab run under the usual device-write approval, capturing what a
shipping mirroring ASIC puts on the wire. This phase is complete when both runs
have produced an artifact and the interoperability gap the parent's Verification
names — no shipping-ASIC bytes exercising a mirror decapsulator — is closed for
the local-SPAN and TZSP paths, and honestly restated for the ERSPAN path it
still does not cover.

## Decisions

The parent plan's Decisions apply. What this phase decides when re-planned:

- Which lab devices are in the run and what each one proves. The lab's
  ICX7150-24-POE is the FastIron model without ERSPAN, so it exercises the
  local-interface source through local SPAN; the MikroTik exercises the TZSP
  receiver. Neither produces ERSPAN, and none is borrowed for it, so ERSPAN
  interoperability stays proven only against golden pcaps and containerised
  senders — stated in the handoff rather than papered over.
- The mirror-session setup on each device is by hand and is a device write,
  so it needs the advance notice and approval the lab runbook requires; the
  capture path itself is read-only from the device's point of view.

## Requirements

Carried from the parent: R5 (loss attributable) and R6 (the stored artifact is
a pcapng `capinfos` reads), each asserted against real hardware rather than a
synthetic frame, and the parent's Verification lab check that R3's
decapsulation holds for the TZSP path on real wire.

## Out of scope

- ERSPAN against a shipping ASIC: no lab device emits it, so it stays out of
  reach and the handoff says so.
- Any device configuration beyond the by-hand mirror sessions the run depends
  on (parent Out of scope: configuring SPAN/RSPAN/ERSPAN on a managed device).

## Open questions

- Is the lab open, and is the ICX7150 powered on with the advance notice the
  runbook requires? This phase cannot start until both are true.
- Which edge host runs the capture in the lab, and how does its capture
  interface connect to the ICX7150 SPAN destination port and receive the
  MikroTik TZSP stream?
