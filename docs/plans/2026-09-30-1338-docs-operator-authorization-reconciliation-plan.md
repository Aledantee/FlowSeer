---
title: Operator Authorization Reconciliation - Plan
type: docs
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
execution: docs
amends: docs/architecture/2026-09-30-operator-authorization-direction.md
---

# Operator Authorization Reconciliation - Plan

> Implemented. U1, U2, and U3 passed focused verification on 2026-09-30.

## Goal

One operator authorization record remains: the proposed
`docs/architecture/2026-09-30-operator-authorization-direction.md`, which
takes in what the accepted
`docs/architecture/2026-09-28-operator-authorization-direction.md` decided
and what landed under it. The 09-28 record is superseded, and so is its
parent plan with the phases that never started. A SpiceDB spike measures the
same workload the OpenFGA spike measured, on the same machine, so the engine
is chosen from two sets of numbers. The means is a research note and edits
to records and plans. No code changes. Stop condition: this plan is wrong if
the SpiceDB spike shows the tenant-per-request membership model cannot be
expressed in SpiceDB without stored per-request state, since the tenant
decision below would then constrain the engine rather than sit beside it.

## Decisions

- A request's tenant is named per request and admitted by membership, as
  the 09-30 record decides: the `X-FlowSeer-Tenant` header names it, and
  the caller is admitted when FlowSeer has enrolled them and their token
  claims the tenant's organization. Partner admins through `partner` and
  global admins through `platform` are also admitted. Why: one token can act
  in several tenants, and a service provider's admins reach customer tenants through
  the `partner` relation, which a token-bound tenant cannot model. (decided
  by the user, 2026-09-30)
- The 09-30 record absorbs the 09-28 record, and the 09-30 parent plan
  continues. Why: the 09-30 record carries the spike evidence, the per-RPC
  rule, the list checks, and the membership model. The 09-28 parent's
  remaining phases (3 to 6) are stubs that never planned. (decided by the
  user, 2026-09-30)
- The engine is chosen after a SpiceDB spike that repeats the OpenFGA
  spike's measurements. (decided by the user, 2026-09-30) Why this spike
  and not a paper comparison: the 09-28 record chose SpiceDB for its
  consistency token from documentation, the 09-30 record chose OpenFGA from
  measurements, and the two never ran on the same workload.
- The landed tenant binding is the membership model's `claimed` mapping.
  A `TenantConfig` binds a tenant to an issuer, an organization claim name,
  and a value, and the `tenants` bucket's `org_` index resolves an (issuer,
  organization) pair to one tenant in one atomic batch
  (`src/services/device/internal/tenantstore/store.go`). The 09-30 plan
  left "how a provider's organization identifiers map to FlowSeer tenant
  ids" as an open question for its phase 2. This answers the one-issuer case.
  The several-issuer case remains open in the 09-30 parent plan's open questions
  (`docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md`). A tenant id
  is never assumed equal to an organization id. Why: the index
  exists, is tested against every applicable read and publish fault, and is
  exactly the lookup the interceptor needs to turn a token's organization
  claims into tenants. No landed code changes: nothing reads the index on a request
  path until caller authentication lands.
- The 09-28 record's engine-neutral rules carry over: the service reaches
  the engine through a Go interface with an in-memory fake, and nothing
  under `spec/proto/` names an engine. Why: both hold whichever engine the
  spike favors, and the 09-30 record does not state them.
- The 09-28 rule "a person who works for two tenants holds two tokens" is
  withdrawn. The request interceptor admits only a `member` of the named
  tenant. Membership is FlowSeer enrollment with a token claiming the
  tenant's organization, or reach through `partner` or `platform`. A caller
  who is not a member of the named tenant is `PermissionDenied`, not
  `Unauthenticated`. Why: follows from the first decision.
- The SpiceDB spike's harness stays out of the tree, as the OpenFGA
  spike's did, and the note says so. Why: it is a one-off comparison, and
  the benchmark the 09-30 plan adds under
  `src/services/device/test/integration/` becomes the reproducible check
  for whichever engine wins.

- The SpiceDB note drops its exclusion-model lookup rows, which mixed in
  the OpenFGA spike's results, and says that variant was not measured
  comparably. It keeps the throwaway rebuild both engines completed. Why: no
  design under consideration previews an access change through the
  exclusion model. (decided by the user, 2026-09-30)
- Enrollment plus a claimed organization is one way into a tenant, not the
  only one: partner admins and platform admins are admitted too, as the
  records say. Why: the partner relation is the reason for the membership
  model. (decided by the user, 2026-09-30)

## Requirements

1. The SpiceDB spike answers every question the OpenFGA spike answered.
   Acceptance: each table in
   `docs/research/2026-09-30-openfga-authorization-spike.md` (Check
   latency, throughput, listing, cache staleness, Tag preview, membership)
   has a counterpart table in the SpiceDB note with SpiceDB numbers and
   OpenFGA numbers re-measured in the same session, or a sentence saying
   why the row does not apply.
2. One record decides operator authorization. Acceptance: the 09-28 record
   reads `status: superseded` with `superseded_by` naming the 09-30 record,
   and `git grep -l 2026-09-28-operator-authorization-direction -- ':!docs/plans'`
   prints only the 09-28 record, the 09-30 record, and
   `docs/architecture/README.md`.
3. Nothing landed is lost. Acceptance: every bullet of the 09-28 record's
   `2026-09-30 — tenancy as built` amendment, and its partition rule with
   the lookup-index exception, appears in the 09-30 record.
4. The plans point one way. Acceptance: the 09-28 parent and its phase 3
   to 6 plans read `status: superseded` with `superseded_by` naming the
   09-30 parent, and `plan-state.py` on the 09-30 parent names its phase 1
   with the `plan` stage.

## Out of scope

- Choosing the engine. The user decides after reading the spike. That
  decision amends the 09-30 record and sets it `accepted-direction` in a
  change of its own.
- Any code change, including the authentication interceptor and the header.
- Re-planning the 09-30 phases. `drive` re-plans each when its turn comes.

## Units

### U1. SpiceDB spike

Files: `docs/research/2026-09-30-spicedb-authorization-spike.md`,
`docs/research/README.md`
After: none
Change:
- A harness outside the tree (under the worker's `$TMPDIR`) runs SpiceDB
  v1.56.2 (the latest release, published 2026-09-11) as the
  `authzed/spicedb` container on Postgres 17, and OpenFGA v1.21.0 as the
  `openfga/openfga` container on its own Postgres 17, both under colima, on
  the OpenFGA spike's workload: 20 tenants, 50 sites each, 40 edges per
  site, 2 sessions per edge, 200 users per tenant holding one or two of 10
  roles, a Tag tree of three roots with branching 3 and depth 4 per tenant,
  two Tags per edge.
- The SpiceDB schema translates the OpenFGA spike's union model
  definition for definition (`relation` for stored relations, `permission`
  with `->` for `from`), and the note shows it.
- Each section repeats its OpenFGA counterpart:
  - Check latency, 1000 sequential calls per row, for the same seven rows,
    SpiceDB under `fully_consistent` and under `minimize_latency`, OpenFGA
    with its cache off.
  - Throughput with 16 concurrent callers checking random edges.
  - `LookupResources` for `edge#capture`: a Tag-derived grant (about 1,104
    edges) and a global admin (40,000 edges), paged by cursor, stating
    whether the result is complete and how long the last page took.
  - Revocation across two SpiceDB instances on one datastore: how long a
    revoked grant stays allowed under `minimize_latency` (the 5 s default
    quantization interval), under `at_least_as_fresh` with the revoking
    write's ZedToken, and under `fully_consistent`, five trials each, with
    each mode's check latency.
  - Tag preview: the exclusion model with SpiceDB's `-` operator, checked
    against a real delete, plus `LookupResources` under it. The
    throwaway rebuild goes into a second SpiceDB datastore, since SpiceDB
    has no stores, and is timed as the OpenFGA rebuild was.
  - Membership: `claimed` re-modeled as a caveat on `enrolled` that tests
    whether the tenant's organization is in a `claimed_orgs` list passed as
    caveat context (SpiceDB has no contextual tuples;
    [migrating from OpenFGA](https://authzed.com/docs/spicedb/migrate-to-spicedb/migrate-from/openfga)),
    enrollment decay as a relationship expiration or a time caveat, the
    same six cases, correctness and latency. It also runs the variant with
    membership inside every resource permission and reports
    `LookupResources` for it, since that is where OpenFGA's `ListObjects`
    broke.
- A closing table sets each measured property side by side, with no
  recommendation: the note is evidence, and the record decides.
- `docs/research/README.md` lists the note.
Tests: none. This is a research note. Requirement 1's acceptance is checked
by reading the note against the OpenFGA spike table by table.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/research/2026-09-30-spicedb-authorization-spike.md docs/research/README.md`

### U2. One record decides operator authorization

Files: `docs/architecture/2026-09-30-operator-authorization-direction.md`,
`docs/architecture/2026-09-28-operator-authorization-direction.md`,
`docs/architecture/README.md`,
`GOALS.md`,
`docs/architecture/2026-08-20-device-service-and-inventory-direction.md`,
`docs/architecture/2026-08-20-network-model-structure-direction.md`,
`docs/architecture/2026-09-09-remote-packet-capture-direction.md`,
`src/services/device/README.md`,
`docs/solutions/architecture-patterns/a-multi-key-uniqueness-claim-needs-one-conditional-batch.md`
After: U1
Change:
- The 09-30 record gains a "Tenants, as landed" section taking in the
  09-28 record's "Tenants are entities, and the token names one" and its
  two 2026-09-30 amendments: the tenant entity in `model/identity`, its
  binding to an issuer and organization claim, the atomic-batch
  organization claim, tenant-partitioned stores with the lookup-index
  exception, the edgebus tenant token and the audit wildcard,
  `TenantService` unserved until authentication, canonical tenant ids, and
  the state no longer read. Each cites the code, not a plan.
- Its "Any OIDC provider, and a tenant the request names" and
  "Membership" sections say the `claimed` tuples (or caveat context) come
  from the token's organization claims resolved through the tenant
  binding, and restate the `PermissionDenied` rule from this plan's
  Decisions.
- Its engine section keeps the OpenFGA argument and the SpiceDB
  measurements side by side, links both spike notes, and says the engine is
  chosen by a person after reading them. The record stays
  `proposed-direction` until then. The sections that name OpenFGA-specific
  mechanics (contextual tuples, stores, `ListObjects`) say which engine
  they describe and link the SpiceDB note's equivalent.
- It gains the 09-28 engine-neutral rules: the engine sits behind a Go
  interface with an in-memory fake, and `spec/proto/` names no engine.
- The 09-28 record's frontmatter reads `status: superseded` and
  `superseded_by: docs/architecture/2026-09-30-operator-authorization-direction.md`,
  and a line under its title says which record now decides. Its body stays
  as history.
- `docs/architecture/README.md` shows the 09-28 record as superseded.
- Every other link to the 09-28 record (`GOALS.md`, the three records, the
  device README, the solution) points at the 09-30 record's matching
  section.
Tests: none. Prose. The verifier's link and prose checks cover the edits,
and Requirement 2's grep and Requirement 3's reading check the rest.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture GOALS.md src/services/device/README.md docs/solutions/architecture-patterns/a-multi-key-uniqueness-claim-needs-one-conditional-batch.md`

### U3. The plans point one way

Files: `docs/plans/2026-09-28-2029-feat-operator-authorization-plan.md`,
`docs/plans/2026-09-28-2029-feat-operator-authorization-phase3-plan.md`,
`docs/plans/2026-09-28-2029-feat-operator-authorization-phase4-plan.md`,
`docs/plans/2026-09-28-2029-feat-operator-authorization-phase5-plan.md`,
`docs/plans/2026-09-28-2029-feat-operator-authorization-phase6-plan.md`,
`docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md`,
`docs/plans/2026-09-30-1139-feat-operator-authorization-phase1-plan.md`
After: none
Change:
- The 09-28 parent and its phase 3 to 6 plans read `status: superseded`
  and `superseded_by: docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md`.
  The parent keeps its `Landed:` lines for phases 1 and 2 and gains an
  outcome note under its title: phases 1 and 2 landed, the rest continues
  in the 09-30 plan.
- The 09-30 parent's Decisions gain the landed foundation (the tenancy the
  09-28 phases built, with their `Landed:` ranges) and this plan's three
  user decisions. Its organization-mapping question is answered for one
  issuer by the tenant binding. The several-issuer case stays open in the
  parent plan's Open questions. The reconciliation hold under its Open
  questions is replaced by one question: the engine, decided by the user
  after the SpiceDB spike, before its phase 2 re-plans. Its
  `artifact_readiness` returns to `implementation-ready`.
- The 09-30 phase 1 plan's hold is replaced by one line: it re-plans
  against a tree holding `model/identity`, `src/common/tenant`, and the
  tenant-partitioned stores. It stays `needs-decisions` so `drive` re-plans
  it first.
Tests: none. Plan files. The frontmatter and `plan-state.py` on the 09-30
parent check Requirement 4.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans`

Waves: U1 U3 | U2

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- docs/research docs/architecture docs/plans GOALS.md src/services/device/README.md docs/solutions/architecture-patterns/a-multi-key-uniqueness-claim-needs-one-conditional-batch.md
python3 .claude/skills/drive/scripts/plan-state.py docs/plans/2026-09-28-2029-feat-operator-authorization-plan.md
python3 .claude/skills/drive/scripts/plan-state.py docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md
git grep -l 2026-09-28-operator-authorization-direction -- ':!docs/plans'
```

U1 needs Docker (colima) and runs both engines in one session.

## Definition of done

- [x] Verifier green for every changed path.
- [x] Requirements 1 to 4 hold by their acceptance checks.
- [x] This plan's `status` set with an outcome note under its title.
- [x] No plan labels in the records, the research note, or the READMEs.

## Open questions

- The engine. The user chooses after reading the SpiceDB note beside the
  OpenFGA spike. The choice amends the 09-30 record's engine section and
  sets the record `accepted-direction`, in a change of its own.
