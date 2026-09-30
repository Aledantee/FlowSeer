# OpenFGA Authorization Spike

Date: 2026-09-30. Status: supporting research for the [operator
authorization record](../architecture/2026-09-30-operator-authorization-direction.md),
which decides. Numbers come from a laptop, so read them for their ratios and
orders of magnitude, not as capacity figures.

The harness that produced them is not in the tree. The benchmark the
authorization plan adds under `src/services/device/test/integration/`
reproduces the Check and BatchCheck latencies and the `ListObjects` cap
against the same model shape. The staleness, preview, and membership
figures below are one-off measurements on the stated setup, and nothing in
the tree reproduces them.

## Setup

- OpenFGA v1.21.0, both embedded through `server.NewServerWithOpts` and as
  the `openfga/openfga:v1.21.0` container over gRPC.
- Postgres 17 in a container under colima on an Apple silicon laptop. The
  embedded server reached it through a port forward, and the OpenFGA
  container shared its Docker network.
- Check cache off unless a row says otherwise.
- Workload: 20 tenants, 50 sites each, 40 edges per site (40,000 edges), 2
  capture sessions per edge, 200 users per tenant holding one or two of 10
  roles per tenant, a Tag tree of three roots with branching 3 and depth 4
  per tenant, two Tags per edge. 292,238 relationships for the union
  model.

## The union model

Grants are unions over stored relationships. Global admins reach every
tenant through `admin from platform`.

```
type platform
  relations
    define admin: [user]
type role
  relations
    define assignee: [user]
type tenant
  relations
    define platform: [platform]
    define admin: [user, role#assignee] or admin from platform
    define capturer: [user, role#assignee] or admin
    define viewer: [user, role#assignee] or capturer
    define full_payload: [user, role#assignee]
type site
  relations
    define tenant: [tenant]
    define capturer: [user, role#assignee] or capturer from tenant
    define viewer: [user, role#assignee] or capturer or viewer from tenant
type tag
  relations
    define tenant: [tenant]
    define parent: [tag]
    define capturer: [user, role#assignee] or capturer from parent
    define viewer: [user, role#assignee] or capturer or viewer from parent
type edge
  relations
    define site: [site]
    define tag: [tag]
    define capture: capturer from site or capturer from tag
    define view: capture or viewer from site or viewer from tag
type capture_session
  relations
    define edge: [edge]
    define requester: [user]
    define download: requester or capture from edge
```

Check latency, 1000 sequential calls per row:

| Path | Embedded p50 / p99 | Container p50 / p99 |
| --- | --- | --- |
| Global admin, edge#capture | 2.12 / 3.21 ms | 1.14 / 2.67 ms |
| Tenant capturer, edge#capture | 1.74 / 3.43 ms | 0.93 / 2.15 ms |
| Site capturer, edge#capture | 0.91 / 1.72 ms | 0.66 / 1.09 ms |
| Tag grant three levels up, edge#capture | 2.17 / 3.20 ms | 1.14 / 1.97 ms |
| No grant (deny) | 2.55 / 3.50 ms | 1.41 / 2.78 ms |
| Global admin, tenant#full_payload (deny, not inherited) | 0.37 / 0.54 ms | 0.55 / 1.55 ms |
| BatchCheck of 50 sessions | 56.71 / 124.10 ms | 31.97 / 53.93 ms |

Throughput with 16 concurrent callers checking random edges: about 1,030
checks/s embedded and 1,430 checks/s against the container. The container
is faster because it sits next to Postgres: a check makes several datastore
round trips and one client round trip.

`ListObjects` for `edge#capture`: 9 ms for a Tag-derived grant (1,104
edges), 148 ms for a global admin (40,000 edges) after raising
`listObjectsMaxResults`. With the default cap of 1000 a preview query that
should return 1,144 edges returned exactly 1000, with nothing in the
response marking it partial.

## Cache staleness across replicas

Check cache and cache controller on, defaults (`checkQueryCache.ttl` and
`cacheController.ttl` both 10 s). A grant was revoked through a second
OpenFGA instance on the same database while the first kept serving checks.

| Trial | Revoked grant still allowed on the first instance for |
| --- | --- |
| 1 | 9.019 s |
| 2 | 6.506 s |
| 3 | 3.978 s |
| 4 | 1.509 s |
| 5 | 8.971 s |

A revocation on the same instance, and every `HIGHER_CONSISTENCY` check,
saw the revocation at once. The cache controller reads the store's
changelog at most once per `cacheController.ttl`
(`internal/cachecontroller/cache_controller.go`, `DetermineInvalidationTime`,
OpenFGA v1.21.0).

## Previewing a Tag change

The GOALS entry asks who gains or loses access before a Tag change applies.

**Exclusion model.** Each (edge, Tag) pair became a `tag_assignment` object,
and Tags gained `define blocked: [user:*]` with `but not blocked` on their
permissions. A contextual `blocked@user:*` tuple then evaluates the store as
if the Tag were gone. 372,239 relationships.

| Question | Result |
| --- | --- |
| Remove one Tag from one edge | 22 users lose access. Preview 5 ms, identical to deleting the relationship and asking again. |
| Delete a Tag with 13 Tags in its subtree | 406 edges affected, 291 (edge, user) pairs lose access. Preview 1.79 s, identical to the real delete. |
| `ListObjects` for a Tag-derived grant | 0 results after 60 s, with both OpenFGA ListObjects algorithms |
| `ListObjects` for a role gaining a Tag grant | 4 of 1,110 after 60 s |

**Throwaway store.** On the union model, one tenant (14,629 relationships)
was rebuilt into a new store in 0.55 s, the Tag deletion applied there in
0.10 s, and 406 edges diffed with `ListUsers` against the live store in
2.54 s. The result matched the real delete: 291 pairs across 291 edges.

## Membership gated by token claims

`tenant.member = (claimed and enrolled) or active_admin from partner or
admin from platform`, where `claimed` arrives as contextual tuples built
from the token's organization claims and `enrolled` is stored with a
`membership_fresh` condition on `current_time`. 336,249 relationships.

**Intersection inside every resource permission** (`edge.capture = (...)
and member from tenant`): all 14 correctness cases held (missing or
wrong-organization claim, decayed enrollment, grant without enrollment,
partner and platform admin with and without their claim, site-level grant
without a claim). Check p50 stayed between 1.05 ms and 2.94 ms embedded.
`ListObjects` broke: 11 edges after 60 s for a Tag-derived grant, 0 after
60 s for a global admin, and 4,000 edges in 11.18 s for a partner.

**Intersection once, on the tenant:** a single `tenant#member` check with
the token's claims, all six cases correct.

| Case | Embedded p50 | Container p50 / p99 |
| --- | --- | --- |
| Member, token lists the tenant | 0.43 ms | 0.63 / 1.51 ms |
| Member, token lists nothing | 1.49 ms | 0.96 / 1.91 ms |
| Enrollment decayed | 1.49 ms | 0.94 / 1.86 ms |
| Partner admin, token lists the partner | 1.08 ms | 0.82 / 1.60 ms |
| Global admin, token lists the platform | 0.76 ms | 0.70 / 1.27 ms |
| Stranger | 1.46 ms | 0.89 / 1.61 ms |

## Sources

- [OpenFGA query consistency](https://openfga.dev/docs/interacting/consistency)
- [OpenFGA configuration](https://openfga.dev/docs/getting-started/setup-openfga/configuration)
- [Token claims as contextual tuples](https://openfga.dev/docs/modeling/token-claims-contextual-tuples)
- [OpenFGA conditions](https://openfga.dev/docs/modeling/conditions)
- [ListUsers](https://openfga.dev/docs/getting-started/perform-list-users), which names `and` and `but not` as particularly expensive
- [ListObjects pagination request, openfga/openfga#2828](https://github.com/openfga/openfga/issues/2828)
- [SpiceDB consistency](https://authzed.com/docs/spicedb/concepts/consistency) and [SpiceDB Enterprise](https://authzed.com/products/spicedb-enterprise)
- [Zitadel scopes](https://zitadel.com/docs/apis/openidoauth/scopes) and [zitadel#11869](https://github.com/zitadel/zitadel/issues/11869)
