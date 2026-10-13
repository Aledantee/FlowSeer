---
title: A Count the Schema Caps Needs a Cap Where the Producer Builds It When Two Clocks Decide the Count
date: 2026-10-13
category: conventions
module: src/edge/agent/internal/rawwindow
problem_type: bug
component: data_model
severity: high
symptoms:
  - "Source.Run returns 'validate ingest record' and the syslog source dies and restarts, then fails again on every matching record"
  - "protovalidate reports 'windows: must contain no more than 16 item(s)'"
root_cause: "Central caps an edge at 16 unexpired windows by its own clock, but the edge counts a window active by its own clock. An edge whose clock runs behind holds 17 or more active windows, and the controller charged and named every one of them."
resolution_type: code_fix
applies_when:
  - "A repeated field with max_items is filled from state that the producer and another party each count by a different clock or rule"
  - "A producer validates its own envelope before publishing, so a violation ends the producer instead of dropping one record"
  - "Adding a limit on central that the edge is assumed to mirror"
related_components: [edge, service_layer]
tags: [protovalidate, max-items, clock-skew, raw-window, ingest-record]
---

# A Count the Schema Caps Needs a Cap Where the Producer Builds It

## The situation

`RawEvidence.windows` has `max_items = 16`
(`spec/proto/flowseer/integration/ingest/v1/ingest_record.proto:65`). Central
refuses a seventeenth open window by counting unexpired ones against its clock
(`rawwindowapi/store.go:201`). The edge decides a window is still active by its
own wall clock and an elapsed clock. With a wall clock behind central's, a
window stays active on the edge after central stopped counting it, central
admits another, and one record can match 17.

## What is true

Central's limit does not bound the edge's count, because the two sides disagree
about which windows are live. The edge validates the envelope before it
publishes, so the 17th ref ends the whole syslog source, and the debits already
made stay made. Every later record that matches fails the same way, for every
device on the edge.

The producer enforces the schema bound itself. `Select` stops at
`MaxWindowsPerRecord` in id order, and a window past the cap is neither charged
nor ended for that record (`rawwindow/controller.go:326`). The constant is
documented as the schema's `max_items` (`controller.go:41`).

## How to apply it

When a `max_items` field is filled from a set the producer does not size
itself, cap at the loop that fills it and pick the dropped members by a stable
order. Do not rely on a limit enforced by another party.

## Evidence

- `rawwindow/controller_test.go:479` `TestARecordPaysAtMostSixteenWindows`
  fails with the cap at 17.
- `syslogsource/source_test.go:1258` opens 17 windows and asserts that the
  record is published naming 16, the end-to-end form of the same defect.

## Not covered

Which windows deserve the 16 places is not decided. Id order is stable, not
fair.
