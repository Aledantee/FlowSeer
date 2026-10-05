---
title: Raw Window - Plan
type: feat
date: 2026-10-02
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Raw Window - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

An operator opens a raw window for an integration, a source, or a device with
an expiry, and the edge attaches raw bytes to records in scope until the
expiry by its own clock.

## Decisions

The parent plan's Decisions apply.

- The window reaches the edge as a Connect call. Why: the bus carries nothing
  an edge must act on (device service record, 2026-09-06 amendment).
- The edge enforces the expiry itself. Why: a lost close must not leave raw
  bytes flowing.
- Opening a window is an authorized operator action and is audited. Why: raw
  device payload can hold credentials and personal data.

## Requirements

1. Records in scope carry raw evidence of reason `WINDOW` while the window is
   open. Example: the parent plan's requirement 5.
2. An edge that restarts inside a window does not resume it unless central
   re-sends it.
3. A window has a maximum length an operator cannot exceed.

## Open questions

- The carrier: a new arm on `DispatchService.Subscribe`, which is keyed per
  device today (`spec/proto/flowseer/edge/dispatch/v1/dispatch.proto`), or a
  call of its own.
- The authorization rule and the audit record for opening a window.
- The maximum window length and a size cap per window.
