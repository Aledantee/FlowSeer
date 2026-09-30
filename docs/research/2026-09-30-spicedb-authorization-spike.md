# SpiceDB authorization spike

Date: 2026-09-30

This note records a controlled comparison of SpiceDB v1.56.2 and OpenFGA v1.21.0 for the operator authorization workload. The measurements cover consistency, revocation, resource lookup, tag exclusion, tenant membership, and rebuild cost.

The harness, generated data, and service containers lived outside the repository. Each service used PostgreSQL. All counterpart values used the same generated workload and service configuration. SpiceDB used its default dispatch cache setting, enabled, its default revision quantization interval of five seconds, and a maximum staleness percentage of 0.1. Point-check latency and membership latency used `fully_consistent` for SpiceDB alongside the separate `minimize_latency` column. Throughput and resource lookup used `fully_consistent`. OpenFGA used its REST API. Ordinary OpenFGA check and lookup measurements disabled its query, iterator, and ListObjects iterator caches. The revocation comparison enabled the query, iterator, ListObjects iterator, and cache controller on both OpenFGA instances with the default ten-second cache TTL. The OpenFGA embedded deployment does not apply to this service-backed comparison. SpiceDB has no embedded deployment counterpart.

## Workload

The union dataset contains:

- 20 tenants
- 50 sites per tenant
- 40 edges per site, for 40,000 edges
- two capture sessions per edge
- 200 users per tenant
- one or two of ten roles per user
- three tag roots per tenant, branching factor three, depth four including the root, for 120 tags per tenant and 2,400 tags overall
- two tag relationships per edge

The union schema stored 292,238 relationships. The membership variant added 4,023 enrollment, partner, and administrative relationships. The resource checks used the same edge and tag graph in both systems.

The common resource path was:

```text
user -> capture_session -> edge -> site -> tenant
user -> edge -> tag_assignment -> tag -> tenant
```

The tag-assignment shape kept a tag grant scoped to an edge. Its effective permissions were:

```text
tag_assignment.capture = tag.capturer - blocked
tag_assignment.view    = tag.viewer - blocked
edge.capture           = site.capturer + tag_assignment.capture
edge.view              = edge.capture + site.viewer + tag_assignment.view
```

## Check latency

Each row is 1,000 sequential checks. Values are milliseconds at p50 and p99. OpenFGA caches were disabled for these measurements.

| Scenario | SpiceDB fully consistent | SpiceDB minimize latency | OpenFGA cache off |
| --- | ---: | ---: | ---: |
| Global admin edge capture | 3.656 / 19.063 | 1.202 / 6.583 | 2.406 / 7.161 |
| Tenant capturer edge capture | 1.819 / 10.840 | 1.385 / 75.740 | 6.379 / 129.840 |
| Site capturer edge capture | 1.690 / 26.295 | 5.820 / 56.007 | 11.371 / 183.143 |
| Tag grant three levels up | 11.479 / 260.507 | 6.069 / 97.733 | 4.201 / 27.489 |
| No grant | 1.607 / 12.985 | 2.789 / 14.717 | 7.132 / 46.522 |
| Global admin tenant payload deny | 4.678 / 36.556 | 2.824 / 18.677 | 3.454 / 18.271 |

For a 50-item batch containing 27 allowed and 23 denied pairs, SpiceDB fully consistent completed in 33.142 ms. The OpenFGA cache-off batch completed in 38.321 ms.

With 16 concurrent callers and 1,600 random edge checks, SpiceDB fully consistent completed in 2.885 seconds at 554.6 checks per second. OpenFGA with caches off completed in 2.125 seconds at 752.8 checks per second.

## Resource lookup

SpiceDB used `LookupResources` with a page size of 1,000 and followed every cursor. OpenFGA used `ListObjects` with a maximum result count of 50,000.

| Subject | SpiceDB raw rows | SpiceDB distinct resources | SpiceDB pages | SpiceDB total | OpenFGA objects | OpenFGA total |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Tag user | 1,104 | 1,104 | 2 | 36.841 ms | 1,104 | 8.291 ms |
| Global admin | 80,000 | 40,000 | 80 | 2,697.875 ms | 40,000 | 113.167 ms |

The global-admin SpiceDB result contains two raw rows for each edge because the site and tag branches both return it. The client must union the raw stream into 40,000 distinct resources.

A separate OpenFGA instance with `listObjectsMaxResults` set to 1,000 returned exactly 1,000 objects in 40.576 ms. The response contained only the `objects` field and no continuation token or partial-result marker. A caller must therefore know the configured cap and query the remaining set by another design if more results are possible.

## Revocation and cache staleness

The revocation test warmed checks on SpiceDB A. A grant was written through SpiceDB B more than five seconds before the revoke. The revoke occurred 50, 150, 250, 350, or 450 ms after a five-second revision boundary. The measured value is wall time from the delete response to the first deny on A. Each mode has five trials in that offset order.

| SpiceDB consistency mode | Allowed after revoke, ms | Check latency, ms |
| --- | --- | --- |
| `minimize_latency` | 10.150, 12.851, 4.963, 3.840, 4.805 | 10.140, 12.823, 4.955, 3.834, 4.798 |
| `at_least_as_fresh` with the revoking write token | 6.857, 5.174, 4.505, 12.772, 4.984 | 6.849, 5.165, 4.497, 12.759, 4.975 |
| `fully_consistent` | 4.723, 5.209, 3.924, 4.954, 3.932 | 4.714, 5.201, 3.915, 4.946, 3.924 |

OpenFGA used cache-on instances for this comparison. Its allowed-after-revoke times were 28.059, 28.740, 25.925, 27.952, and 29.747 ms. The corresponding check latencies were 4.105, 4.532, 3.576, 4.014, and 3.325 ms.

The SpiceDB measurements show the cost of a consistency choice without a long stale-read window in this topology. `fully_consistent` and `at_least_as_fresh` denied in about one check. OpenFGA's cache-on path also denied quickly here, with a higher end-to-end revoke-to-deny delay than the observed SpiceDB modes.

## Tag exclusion and preview

Both systems modeled an explicit `tag_assignment` object for each edge and tag pair. SpiceDB preview used `LookupResources` under the exclusion permission. OpenFGA preview used `ListObjects` under the equivalent permission. The SpiceDB form was:

```text
definition tag_assignment {
    relation edge: edge
    relation tag: tag
    relation blocked: user | user:*
    permission capture = tag->capturer - blocked
    permission view = tag->viewer - blocked
}

definition edge {
    relation site: site
    relation tag_assignment: tag_assignment
    permission capture = site->capturer + tag_assignment->capture
    permission view = capture + site->viewer + tag_assignment->view
}
```

The equivalent OpenFGA form used `capturer from tag but not blocked` and `viewer from tag but not blocked` on `tag_assignment`, then unioned those permissions with the site path on `edge`.

The excluded subtree contained 13 tags and affected 406 edges. Preview removed the same effective resources as the subsequent relationship deletion.

| Engine and operation | Baseline resources | Preview resources | Preview time | Delete resources | Delete time |
| --- | ---: | ---: | ---: | ---: | ---: |
| SpiceDB, one tag | 1,104 | 1,103 | 104.392 ms | 1,103 | 134.794 ms |
| SpiceDB, 13-tag subtree | 1,104 | 698 | 94.778 ms | 698 | 38.247 ms |
| OpenFGA, one tag | 1,104 | 1,103 | 5.932 ms | 1,103 | 6.523 ms |
| OpenFGA, 13-tag subtree | 1,104 | 698 | 4.965 ms | 698 | 5.955 ms |

The single-tag preview removed one user-resource pair. The subtree preview removed 406 pairs. Each preview returned one user and the expected resource pairs.

A one-tenant rebuild included exactly 14,615 relationships or tuples, including capture sessions. A fresh SpiceDB datastore accepted its schema and data in 497.454 ms. A fresh OpenFGA store accepted its model and tuples in 1,710.839 ms.

## Tenant membership

The SpiceDB membership model used a caveat for enrollment freshness. Its relevant definitions were:

```text
caveat tenant_claim(claimed_orgs list<string>, current_time timestamp,
                    organization string, expires_at timestamp) {
    organization in claimed_orgs && current_time < expires_at
}

definition platform { relation admin: user }
definition role { relation assignee: user }

definition tenant {
    relation platform: platform
    relation partner: tenant
    relation enrolled: user with tenant_claim
    relation direct_admin: user | role#assignee
    relation direct_capturer: user | role#assignee
    relation direct_viewer: user | role#assignee
    permission active_admin = direct_admin & member
    permission member = enrolled + partner->active_admin + platform->admin
    permission admin = direct_admin + platform->admin
    permission capturer = direct_capturer + admin
    permission viewer = direct_viewer + capturer + admin
}

definition site {
    relation tenant: tenant
    relation direct_capturer: user | role#assignee
    relation direct_viewer: user | role#assignee
    permission capturer = (direct_capturer + tenant->capturer) & tenant->member
    permission viewer = (direct_viewer + capturer + tenant->viewer) & tenant->member
    permission member = tenant->member
}

definition tag {
    relation tenant: tenant
    relation parent: tag
    relation direct_capturer: user | role#assignee
    relation direct_viewer: user | role#assignee
    permission capturer = (direct_capturer + parent->capturer) & tenant->member
    permission viewer = (direct_viewer + capturer + parent->viewer) & tenant->member
    permission member = tenant->member
}

definition edge {
    relation site: site
    relation tag: tag
    permission capture = (site->capturer + tag->capturer) & site->member
    permission view = (capture + site->viewer + tag->viewer) & site->member
}

definition capture_session {
    relation edge: edge
    relation requester: user
    permission download = requester + edge->capture
}
```

The stored enrollment relationship carried `organization` and `expires_at` in its optional caveat context. Requests supplied `claimed_orgs` and `current_time`. Expiration was therefore evaluated from stored relationship context and request-time context.

The OpenFGA model represented membership as `(claimed and enrolled) or active_admin from partner or admin from platform`. Contextual `claimed` tuples represented the token organization claims. The stored `enrolled` tuples used a `membership_fresh` condition with `expires_at`, while each request supplied `current_time`.

Its resource intersections were:

```text
tenant.member = (claimed and enrolled)
              or active_admin from partner
              or admin from platform
tenant.active_admin = direct_admin and member
site.capturer = (direct_capturer or capturer from tenant) and member from tenant
site.viewer = (direct_viewer or capturer or viewer from tenant) and member from tenant
tag.capturer = (direct_capturer or capturer from parent) and member from tenant
tag.viewer = (direct_viewer or capturer or viewer from parent) and member from tenant
edge.capture = (capturer from site or capturer from tag) and member from site
edge.view = (capture or viewer from site or viewer from tag) and member from site
```

Fourteen correctness cases held in both engines. They covered missing and wrong claims, expired enrollment, direct grants without enrollment, partner administration with and without a provider claim, global platform administration, and the resource intersections for site and tag paths.

Tenant check latency used 1,000 sequential checks with OpenFGA caches disabled.

| Case | SpiceDB fully consistent | OpenFGA cache off |
| --- | ---: | ---: |
| Member with claim | 2.874 / 63.657 ms | 1.079 / 3.044 ms |
| Member without claim | 1.141 / 3.442 ms | 1.189 / 2.369 ms |
| Enrollment decayed | 1.150 / 1.819 ms | 1.131 / 1.695 ms |
| Partner admin with provider claim | 1.093 / 2.947 ms | 1.370 / 2.378 ms |
| Global admin with platform claim | 1.129 / 3.176 ms | 1.183 / 1.942 ms |
| Stranger | 1.103 / 3.300 ms | 2.181 / 4.884 ms |

Values are p50 and p99 in milliseconds.

Membership-aware resource lookup exposed a larger difference in recursive intersection listing:

| Subject | SpiceDB distinct resources | SpiceDB pages | SpiceDB total | OpenFGA objects | OpenFGA total |
| --- | ---: | ---: | ---: | ---: | ---: |
| Tag user with claim context | 1,104 | 2 | 71.172 ms | 1,104 | 1,194.308 ms |
| Global admin with platform claim | 40,000 | 82 raw pages | 3,033.860 ms | 0 | 3-second default deadline |

The membership global lookup produced 82,000 raw SpiceDB rows and 40,000 distinct edges. OpenFGA returned no objects before its default `ListObjects` deadline for the equivalent recursive intersection.

## Findings

| Area | Result |
| --- | --- |
| Point checks | Both engines completed the operator cases. SpiceDB consistency mode changed tail latency. OpenFGA cache-off checks had higher p99 on several traversals. |
| Batch checks | The engines agreed on 27 allowed and 23 denied results. |
| Throughput | OpenFGA led this cache-off concurrent check workload at 752.8 checks per second versus 554.6 for SpiceDB fully consistent. |
| Resource lookup | OpenFGA returned the 40,000-object union directly. SpiceDB required cursor draining and client-side de-duplication. |
| Revocation | The observed first-deny delay stayed within one check for the SpiceDB consistency modes and was 25.925 to 29.747 ms for the cache-on OpenFGA trials. |
| Exclusion preview | Both models previewed the same 1,103 and 698 resource sets as their deletes. |
| Rebuild | SpiceDB rebuilt the one-tenant dataset in 497.454 ms. OpenFGA took 1,710.839 ms. |
| Membership listing | Recursive membership intersections remained materially more expensive than direct membership checks. OpenFGA did not finish the global lookup before its default deadline. |

## Sources

- [SpiceDB consistency](https://authzed.com/docs/spicedb/concepts/consistency)
- [SpiceDB caveats](https://authzed.com/docs/spicedb/concepts/caveats)
- [SpiceDB command reference](https://authzed.com/docs/spicedb/reference/commands)
- [SpiceDB migration from OpenFGA](https://authzed.com/docs/spicedb/migrate-to-spicedb/migrate-from/openfga)
- [OpenFGA configuration](https://openfga.dev/docs/getting-started/setup-openfga/configuration)
- [OpenFGA token claims and contextual tuples](https://openfga.dev/docs/modeling/token-claims-contextual-tuples)
- [OpenFGA conditions](https://openfga.dev/docs/modeling/conditions)
