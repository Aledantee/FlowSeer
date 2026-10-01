# Dependency admission

This convention turns the [dependency admission direction](../architecture/2026-10-01-dependency-admission-direction.md) into the working rule for Go modules and npm packages. It covers direct dependencies, pinned versions, and the checks a contributor runs before changing them.

## Add a direct dependency

Write a statement before adding a direct dependency. A person approves the statement before the manifest changes. The statement explains why the dependency is required, why the pinned version is safe, and why FlowSeer-owned code does not do the job. The statement gate checks both directions: every direct dependency has a statement, and every statement names a direct dependency.

The existing statements are under [`docs/dependencies/statements/`](../dependencies/statements/). Their proposed verdicts are `keep` or `cut`. `approved` is empty until a person rules on the statement. A `cut` verdict is a removal proposal, not an approved removal.

The statement format is:

```markdown
---
name: github.com/google/uuid
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The package at src/services/device/internal/edgeapi imports github.com/google/uuid at src/services/device/internal/edgeapi/admin.go:10. The tenant package also imports it at src/common/tenant/tenant.go:7. The pinned direct requirement is `github.com/google/uuid` at `v1.6.0`.

## Why it is safe

The publisher is Google. The pinned version `v1.6.0` was published at 2024-01-23T18:54:04Z and was 981 days old on 2026-10-01. Its 14-day wait ended at 2024-02-06T18:54:04Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/google/uuid` at `v1.6.0`. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement UUID generation, parsing, and validation for identifiers. That would own a general-purpose identifier format and its edge cases instead of keeping that boundary with a maintained library. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency.
```

The first section names the importing package or package graph. The second names the pinned version and the evidence still to review. The third states why the dependency's tree is preferable to an owned replacement. The tree size pulled in by a direct dependency counts against it.

## Change a pin

A version waits 14 days after publication before adoption. The Go proxy's publication time is a commit time, so the age check is a convention for Go and not a promise about when a release was tagged. pnpm enforces the same wait during resolution.

A pin moves for one of three reasons:

- it fixes an advisory;
- the code needs a fix or feature;
- the pinned version is stale because the newest version past the wait is at least 90 days newer.

The target is the newest version past the wait with no open advisory. It is never the newest release by default. The [direction record](../architecture/2026-10-01-dependency-admission-direction.md) records why these limits exist.

The inventory classifies a version as `deploy` when shipping code reaches it. As a stricter fallback, a root or netpen `go.sum` entry absent from both closures is also `deploy`, because build tags can hide imports and an unexplained entry receives the stricter review. Sampled test-only closure entries are `run`, and non-shipping modules are `run`. A `run` classification still requires a statement and a source review focused on workstation behavior.

## Inspect the tree

Run these commands from the repository root:

```text
go run ./tools/deps inventory
go run ./tools/deps tree
go run ./tools/deps advisories
go run ./tools/deps age
```

`inventory` prints ecosystem, name, version, lockfile hash, criteria, manifests, and direct-dependency paths. `tree` prints each direct dependency's version count and the count of versions only it reaches. `advisories` prints dependency, version, advisory id, summary, and affected import paths. `age` prints publication time, the 14-day status and until time, and the Go origin commit when the proxy provides one.

The per-version records and the version gate do not exist yet.
