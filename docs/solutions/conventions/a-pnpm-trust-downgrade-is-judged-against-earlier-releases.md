---
title: A pnpm trust downgrade is judged against earlier releases, so read every release's attestations before excluding one
date: 2026-10-14
last_verified: 2026-10-14
category: conventions
module: frontend/web
problem_type: convention
component: web-console
severity: medium
applies_when:
  - "pnpm refuses a locked or new version with ERR_PNPM_TRUST_DOWNGRADE under trustPolicy: no-downgrade"
  - "Proposing, approving, or reviewing a trustPolicyExclude entry and the reason written beside it"
related_components: [dependency-admission]
tags: [pnpm, supply-chain, trust-policy, verification]
---

# A pnpm trust downgrade is judged against earlier releases

`trustPolicy: no-downgrade` refuses a version whose publish trust is weaker
than what earlier releases of the same package carried. A version without
provenance is refused only because some earlier release had it. So the
reason for an exclusion has to come from the package's full release
history, not from the refused version alone. A refused version that "just
predates provenance" would not be refused at all.

## What happened

With pnpm 11.25.0, `pnpm install --frozen-lockfile` in `frontend/web`
failed once `trustPolicy: no-downgrade` was set:

```text
[ERR_PNPM_TRUST_DOWNGRADE] 1 lockfile entries failed verification:
  undici-types@6.21.0 High-risk trust downgrade for "undici-types@6.21.0" (possible package takeover)
```

The first explanation offered for approval said 6.21.0 was published
before the package adopted provenance. Registry metadata read on
2026-10-14 (`https://registry.npmjs.org/undici-types`,
`versions[<v>].dist.attestations`) showed otherwise. 6.13.0 to 6.19.2,
published 2024-04-12 to 2024-06-18, carry attestations. 6.19.8 onward in the
6.x line, 6.21.0 included, carry none, and 7.8.0 carries them again. The
exclusion still stood, but the person had approved it on a softer
description than the facts. The comment and the plan Decision were
corrected in `bd1ca3ce`.

pnpm's documentation states the comparison: the setting "will prevent
installation of a package if its trust level has decreased compared to
previous releases" (pnpm.io, `docs/settings/dependency-resolution.md`,
trustPolicy). Whether "previous" means earlier by publish time or by semver
is unverified.

## How to apply it

Before proposing an exclusion, list each release's trust evidence and find
where it dropped:

```bash
curl -s https://registry.npmjs.org/<name> | python3 -c '
import json, sys
d = json.load(sys.stdin)
for v, x in sorted(d["versions"].items(), key=lambda kv: d["time"][kv[0]]):
    prov = bool(x.get("dist", {}).get("attestations"))
    pub = bool((x.get("_npmUser") or {}).get("trustedPublisher"))
    print(d["time"][v][:10], v, "provenance" if prov else "-", "trusted" if pub else "-")'
```

State the drop in the question to the person and in the comment beside the
entry, as `frontend/web/pnpm-workspace.yaml:13-17` does:

```yaml
# The undici-types 6.x line stopped publishing provenance after 6.19.2, and
# 6.21.0 arrives through @types/node. Approved by a person on 2026-10-14.
# Remove it when @types/node moves to a release whose undici-types carries provenance.
trustPolicyExclude:
  - 'undici-types@6.21.0'
```

A drop inside one release line, with later lines regaining the evidence,
reads as a publishing gap. A drop with no later recovery, a new publisher,
or a release outside the usual cadence is the takeover case the setting
exists for. Leave that one refused and raise it before any exclusion.

## What it does not cover

- Whether a given drop is benign. The history only shows when the drop
  happened, and a person still rules on each exclusion (`AGENTS.md`, Hard
  boundaries).
- `trustPolicyIgnoreAfter`, which skips the check for old versions. It is
  not set in this repository.
- Detecting that a setting was removed. Nothing in the repository fails
  when `trustPolicy` leaves `frontend/web/pnpm-workspace.yaml`. The phase 1
  plan records that as a review gap.
