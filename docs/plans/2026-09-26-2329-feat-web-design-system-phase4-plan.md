---
title: Web Design System Phase 4, Data Display - Plan
type: feat
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-26-2329-feat-web-design-system-plan.md
---

# Web Design System Phase 4, Data Display - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`src/ui/` gains the data display components, each with stories:
`UiTable` (dense, sortable header, sticky header, row selection),
`UiEmptyState`, `UiSkeleton`, `UiMeter` (replacing `ResourceMeter` and
`HealthBar`), `UiProgress`, and `UiPagination`. `TrafficChart`,
`TrafficSparkline`, and the topology link colors read `chart-1` to
`chart-6` and `graph-edge`. The device and client tables move to
`UiTable`.

This plan is wrong if `UiTable` cannot keep the row-order stability the
Devices page promises while values update
(`frontend/web/README.md`, "rows retain their order as values change").

## Decisions

- `UiTable` styles native `<table>` markup and adds no grid library. Why:
  the README defers choosing a grid until real update rates and fleet
  sizes can be tested.
- Series colors are assigned by a stable index in the order cyan, coral,
  violet, green, amber, blue, and every series also has a direct label.
  Why: the categorical guidance in `frontend/web/design/README.md`.

## Requirements

1. `UiTable` sorts on a header click and announces the sort with
   `aria-sort`. Example: clicking "Name" twice sets `aria-sort` to
   `descending`.
2. Chart series never rely on color alone. Example: every
   `TrafficChart` series has a legend label or a direct label in its
   story.
