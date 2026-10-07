---
title: Dependency Admission Phase 5, Review Backlog - Plan
type: chore
date: 2026-10-01
artifact_contract: flowseer-plan/v2
execution: docs
---

# Dependency Admission Phase 5, Review Backlog - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`docs/dependencies/baseline.txt` is empty. Every version the tree pins has
been read at the depth its criteria require, and each statement's safety
section points at the record that says so.

## Decisions

- The parent plan's Decisions apply. Order: `deploy` versions of the root
  module, then `src/edge/netpen`, then the npm `deploy` closure, then `run`
  versions, largest trees last.
- One reviewer per dependency name. Records are one file per name, so the
  work runs in parallel up to what `delegate` allows for a wave.
- A finding that a version is unsafe stops the review of that name and goes
  to the person with the evidence. The record takes a `violation` entry, the
  form cargo-vet uses
  (https://mozilla.github.io/cargo-vet/recording-audits.html), and the
  dependency is pinned back, replaced, or cut.
- A version reviewed here that is also stale is upgraded first, so the full
  read is spent on the version that stays.
- The re-plan sizes the work from the counts at that time and splits it
  into units by ecosystem and module. It measures the lines of source per
  `deploy` module first, which is where the parent's stop condition is
  decided.

## Requirements

1. The baseline has no lines and the gate passes.
2. Every record's review names its reviewer, date, and criteria, and a
   `deploy` record's notes name what was read.
3. No statement ends with "Source not yet reviewed."
