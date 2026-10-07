---
title: A scoped lockfile repair can move unrelated pins
date: 2026-10-06
last_verified: 2026-10-06
category: conventions
module: frontend/web
problem_type: convention
component: web-console
severity: medium
applies_when:
  - "Repairing selected pnpm lockfile entries or reviewing a dependency change intended to preserve unrelated resolutions"
related_components: [dependency-admission]
tags: [verification, comparison, drift-detection]
---

# A scoped lockfile repair can move unrelated pins

Compare the resolved graph against the starting commit after a selective
lockfile repair. Removing only the entries that need replacement did not
preserve every other resolution in the dependency change at `8b7c3ecd`.
The repair also moved the Vue compiler's parser edges from `7.29.8` to
`7.29.9`. Commit `571ad59c` restored those edges and the removed parser
package and snapshot.

The [dependency convention](../../conventions/dependencies.md#change-a-pin)
already requires a reason for a pin to move. The extra lesson is that a
scoped repair can produce a move outside its intended set. Review removed
package versions and changed snapshot edges even when the install passes.

## Evidence

The explanation in `571ad59c` records the observed re-resolution:

```text
Re-resolving the lockfile entries under the 14-day wait also
deduplicated @babel/parser from 7.29.8 to 7.29.9 for @vue/compiler-core
and @vue/compiler-sfc.
```

The restored package entry is at `frontend/web/pnpm-lock.yaml:166`, and
its snapshot is at `frontend/web/pnpm-lock.yaml:5431`. The restored edges
are at `frontend/web/pnpm-lock.yaml:6330` and
`frontend/web/pnpm-lock.yaml:6343`:

```yaml
'@vue/compiler-core@3.5.43':
  dependencies:
    '@babel/parser': 7.29.8

'@vue/compiler-sfc@3.5.43':
  dependencies:
    '@babel/parser': 7.29.8
```

## Apply it

For this case, compare the unintended move and its correction separately:

```bash
git diff 8b7c3ecd^ 8b7c3ecd -- frontend/web/pnpm-lock.yaml
git show 571ad59c -- frontend/web/pnpm-lock.yaml
```

For a new repair, use its starting commit and final revision. Account for
every removed version and changed dependency edge against the intended
replacement set. A package count alone cannot show which edges moved.

This case does not establish why `trustLockfile` failed to bypass the age
check, or which other repairs will deduplicate entries. It establishes an
observed unintended move and the graph comparison needed to catch it.
