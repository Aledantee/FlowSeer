---
title: Dependency Admission Phase 3, Records, Baseline, and the Gate - Plan
type: chore
date: 2026-10-01
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Dependency Admission Phase 3, Records, Baseline, and the Gate - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Every code-bearing version left after phase 2 has a record bound to its
hash. Versions nobody has reviewed are listed in
`docs/dependencies/baseline.txt`, and the conformance gate fails a lockfile
change that the records do not cover. Container images, buf inputs, the Go
toolchain, pnpm, and the fetches inside fixture Dockerfiles are pinned by
hash and recorded.

## Decisions

- The parent plan's Decisions apply: one JSON file per dependency name under
  `docs/dependencies/records/<ecosystem>/`, parsed with `encoding/json`, and
  an offline gate in `test/conformance/dependencies`.
- A record version holds the hash, the criteria, a status of `unreviewed`
  or `reviewed`, the publication date, the origin commit for a Go module,
  the date of the last advisory lookup, and a ruling per advisory id. A
  reviewed version adds the reviewer, the date, `full` or `delta` with the
  version it was diffed from, and the reason the pin moved. The re-plan
  fixes the field names against what phase 1's `inventory --json` emits.
- `tools/deps` gains a subcommand that writes baseline records from the
  inventory and the two lookups, and the `advisories` subcommand starts to
  exit non-zero for an advisory without a ruling. The two advisories the
  2026-10-01 lookup returned get their rulings here.
- `tools/hooks/pre-tool-policy.sh` lists `docs/dependencies/statements/*`
  and `docs/dependencies/baseline.txt` among the policy surfaces. That file
  is itself a policy surface, so the unit stops at a diff the person
  approves.
- `frontend/web/package.json` moves to exact versions with no lockfile
  version change. Each `FROM` line gains a digest, `packageManager` gains
  its hash, `go.mod` gains a `toolchain` line, and the verifier exports
  `GOTOOLCHAIN=local` so a newer `go` line fails instead of downloading a
  toolchain (https://go.dev/doc/toolchain).
- Two image tags are `latest` today (`clixon/clixon-example` and
  `sysrepo/sysrepo-netopeer2`). The re-plan resolves each to the digest in
  use and records the tag it came from.

## Requirements

Parent requirements 1 to 4, 6, 7, 9, 11, and 12, each with its example. In
addition:

1. The baseline lists exactly the `unreviewed` versions. Example: a
   baseline line for a version whose record says `reviewed` fails.
2. The gate reports how many versions it checked and fails on zero.
3. `go run ./tools/deps advisories` exits non-zero for an advisory without a
   ruling and zero once the record holds one.
