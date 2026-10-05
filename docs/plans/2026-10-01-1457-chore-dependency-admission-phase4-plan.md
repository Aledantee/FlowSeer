---
title: Dependency Admission Phase 4, The Dependency Skill - Plan
type: chore
date: 2026-10-01
artifact_contract: flowseer-plan/v2
execution: docs
---

# Dependency Admission Phase 4, The Dependency Skill - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A project skill under `.claude/skills/` carries the procedures a person or
agent follows: add a dependency, upgrade one, review one version, and run
the monthly pass. Each ends in a record the gate accepts.

## Decisions

- The parent plan's Decisions and the direction record apply. The skill
  states procedure and links the convention document for the rules, so the
  rules are written once.
- Add: write the statement, ask the person through the question tool with
  the tree size and the owned-code alternative, and change the manifest only
  on approval.
- Upgrade: name the reason, pick the newest version past the wait with no
  open advisory, read the whole diff between the reviewed version's bytes
  and the new version's bytes, and write a `delta` record.
- Review: read the module cache directory or the registry tarball, never
  the forge. The checklist for malicious code follows the OpenSSF guide
  (https://best.openssf.org/Concise-Guide-for-Evaluating-Open-Source-Software):
  install scripts, reads of credentials and environment, network endpoints,
  process execution, and encoded or obfuscated values. For Go it adds
  `init` functions, `unsafe`, cgo, assembly, `//go:linkname`, and generated
  files that do not match their generator.
- A review is delegated as `delegate` routes a `review-unit`, and a
  `deploy` review of a module gets a second reviewer from a different
  vendor. The re-plan confirms this against the registry's roles.
- The monthly pass re-runs the advisory lookup for every record, lists
  stale pins, and proposes upgrades. It changes no manifest by itself.

## Requirements

1. Following the add procedure for a module produces a statement, an
   approval, and a `reviewed` record, and the gate passes.
2. Following the upgrade procedure produces a record with `from` and a
   reason, and the old version's record remains as the base of the diff.
3. The monthly pass prints the advisories without a ruling and the pins
   older than the staleness bound.
4. Every command in the skill runs verbatim from a fresh shell, as
   `docs/agent-steering.md` requires of a skill.
