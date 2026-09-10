---
title: Remote Packet Capture Phase 3, Host Wiring and Lab Validation - Plan
type: feat
date: 2026-09-09
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
---

# Remote Packet Capture Phase 3, Host Wiring and Lab Validation - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The edge agent assembles the capture engine as a `service.Module`, accepts a
capture command from central, runs it against real switches, and streams chunks
back under the re-assertion rule. A capture started from central produces an
artifact `capinfos` reads and a live tail an operator can follow. This phase is
wrong if the edge host's command channel cannot carry an operator-originated
task, because then the capture command needs its own transport and that is a
larger decision than this phase.

## Decisions

The parent plan's Decisions apply. Nothing further can be decided until the edge
host has landed and its supervision tree, config shape, and command channel are
readable in the tree.

## Requirements

Carried from the parent: R1 (a budgetless session is refused end to end), R7
(a stream that stops re-asserting is closed), and the lab check that R3 and R6
hold against real hardware rather than synthetic frames.

## Out of scope

- Central-side storage, retention enforcement, and fan-out of a tail to more than
  one consumer.
- Any device-side configuration of the mirror sessions the lab check depends on;
  those are set up by hand for the check.

## Open questions

- How does a capture command reach the edge? This is the parent's first open
  question and it belongs to the host work. Nothing in this phase can be
  sequenced until it has an answer.
- Which lab devices are in the check? The lab's ICX7150-24-POE is the one FastIron
  model without ERSPAN, so it exercises the local-interface source through local
  SPAN; MikroTik TZSP streaming exercises the receiver. The lab Huawei is not a
  candidate either: the eKitEngine S220 is a Layer 2 SMB switch whose datasheet
  lists plain port mirroring and no remote tier, and its SNMP agent has been
  unreachable. So no lab device produces ERSPAN, and none is borrowed for it:
  phase 2 proves ERSPAN against golden pcaps and containerised Linux and Open
  vSwitch senders, and this phase's lab run covers the paths those cannot reach.
  What stays unproven is interoperability with a shipping ASIC beyond the golden
  captures, which is worth stating in the handoff rather than papering over.
- Does the lab check need a switch powered on with advance notice, and is a write
  to any lab device involved? The capture path itself is read-only from the
  device's point of view, but setting up the mirror session is a device write and
  needs the usual approval.
