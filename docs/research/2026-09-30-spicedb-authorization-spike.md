# SpiceDB authorization spike

Date: 2026-09-30. Status: supporting research for the [operator
authorization record](../architecture/2026-09-30-operator-authorization-direction.md),
which decides.

This note compares SpiceDB v1.56.2 with OpenFGA v1.21.0 on the same workload.
The measurements ran on a laptop and show relative behavior, not service
capacity.

## Setup

- SpiceDB A used port 15051 with a PostgreSQL 17 datastore. SpiceDB B used
  port 15052 against the same datastore for revocation writes. A separate
  SpiceDB rebuild server used port 15053 and its own PostgreSQL 17 datastore.
- OpenFGA v1.21.0 used port 18080 and its own PostgreSQL 17 datastore.
- All containers ran under Colima.
- OpenFGA ran with its check and iterator caches disabled, a 50,000 result
  limit for `ListObjects`, and a 1,000 tuple write batch.
- The dataset contained 20 tenants, 50 sites per tenant, 40 edges per site,
  200 users per tenant with one or two of 10 roles, two capture sessions per
  edge, 363 Tags, and 22,080 Tag grant edges. The union dataset contained
  302,021 tuples.
- Checks used SpiceDB `minimize_latency` and `fully_consistent`. Revocation
  trials also used `at_least_as_fresh` with the token from the revoking write.
  These modes provide different freshness and latency tradeoffs as described
  in [SpiceDB consistency](https://authzed.com/docs/spicedb/concepts/consistency).

## Schema translation

The union model keeps stored relationships separate from computed permissions.
The SpiceDB form uses `relation` for stored edges, `permission` for computed
access, and arrows for traversal. This follows the relationship and permission
mapping described in [the SpiceDB OpenFGA migration guide](https://authzed.com/docs/spicedb/migrate-to-spicedb/migrate-from/openfga).

```text
definition user {}
definition platform {
    relation admin: user
}
definition role {
    relation assignee: user
}
definition tenant {
    relation platform: platform
    relation direct_admin: user | role#assignee
    relation direct_capturer: user | role#assignee
    relation direct_viewer: user | role#assignee
    relation direct_full_payload: user | role#assignee
    permission admin = direct_admin + platform->admin
    permission capturer = direct_capturer + admin
    permission viewer = direct_viewer + capturer
    permission full_payload = direct_full_payload
}
definition site {
    relation tenant: tenant
    relation direct_capturer: user | role#assignee
    relation direct_viewer: user | role#assignee
    permission capturer = direct_capturer + tenant->capturer
    permission viewer = direct_viewer + capturer + tenant->viewer
}
definition tag {
    relation tenant: tenant
    relation parent: tag
    relation direct_capturer: user | role#assignee
    relation direct_viewer: user | role#assignee
    permission capturer = direct_capturer + parent->capturer
    permission viewer = direct_viewer + capturer + parent->viewer
}
definition edge {
    relation site: site
    relation tag: tag
    permission capture = site->capturer + tag->capturer
    permission view = capture + site->viewer + tag->viewer
}
definition capture_session {
    relation edge: edge
    relation requester: user
    permission download = requester + edge->capture
}
definition partner {
    relation admin: user
}
```

## Check latency

Each row used 1,000 calls. The result column confirms that both engines made
the same allow or deny decision.

| Path | SpiceDB fully consistent p50 / p99 ms | SpiceDB minimum latency p50 / p99 ms | OpenFGA cache off p50 / p99 ms | Result |
| --- | ---: | ---: | ---: | --- |
| Global admin, edge capture | 0.614 / 1.486 | 0.513 / 0.879 | 1.662 / 4.199 | allow |
| Tenant capturer, edge capture | 0.636 / 0.985 | 0.454 / 0.756 | 1.249 / 2.418 | allow |
| Site capturer, edge capture | 0.575 / 0.884 | 0.437 / 0.670 | 0.775 / 2.629 | allow |
| Tag grant three levels up | 0.622 / 1.590 | 0.467 / 1.493 | 1.462 / 3.162 | allow |
| No grant | 0.731 / 1.297 | 0.528 / 0.874 | 1.688 / 3.583 | deny |
| Global admin, tenant full payload | 0.591 / 0.835 | 0.447 / 0.768 | 0.668 / 1.202 | deny |

`BatchCheck` for 50 sessions measured 0.682 / 17.192 ms on SpiceDB and
28.885 / 41.192 ms on OpenFGA at p50 / p99. Both results were allowed.

## Throughput

With 16 concurrent callers and 1,600 checks, SpiceDB reached 2,158.3 checks
per second. OpenFGA reached 1,218.2 checks per second with its caches off.

## Resource lookup

Both engines returned complete results for `edge#capture`.

| Subject | SpiceDB count | SpiceDB pages | SpiceDB last page ms | OpenFGA count | OpenFGA elapsed ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| Tag-derived grant | 1,104 | 3 | 16.054 | 1,104 | 5.518 |
| Global admin | 40,000 | 41 | 12.827 | 40,000 | 67.507 |

SpiceDB reported complete pagination on every lookup. OpenFGA returned an
empty continuation token for both results.

## Revocation

The harness wrote each grant through SpiceDB B and checked through SpiceDB A.
The values below are the five allowed durations in milliseconds followed by
the five check latencies in milliseconds.

| Consistency mode | Allowed duration trials | Check latency trials |
| --- | --- | --- |
| `minimize_latency` | 0.997, 0.914, 0.849, 0.872, 0.839 | 0.996, 0.914, 0.849, 0.872, 0.839 |
| `at_least_as_fresh` | 2.355, 2.479, 2.329, 2.273, 2.388 | 2.355, 2.478, 2.328, 2.273, 2.388 |
| `fully_consistent` | 2.599, 2.599, 2.488, 2.482, 2.630 | 2.590, 2.599, 2.488, 2.482, 2.630 |

The `at_least_as_fresh` checks used the revoking write token. The consistency
modes and ZedToken behavior are described in [SpiceDB consistency](https://authzed.com/docs/spicedb/concepts/consistency).
SpiceDB `minimize_latency` uses the documented default 5 s revision-quantization
interval. These five trials did not wait for a quantization-boundary delay.

## Tag preview

The earlier zero result was a selection bug. The corrected dataset intentionally
places 406 grant edges under the 13-Tag preview subtree. The complete Tag
derived set contains 1,104 edges, so 698 remain outside the subtree.

| Operation | Preview result | Real delete result | Equal |
| --- | ---: | ---: | --- |
| Remove one Tag from one edge | 1,103 allowed resources | 1,103 allowed resources | true |
| Remove the 13-Tag subtree | 698 allowed resources | 698 allowed resources | true |

The SpiceDB subtree preview lookup completed in 6.642 ms on the last page. The
real delete lookup completed in 4.532 ms on the last page, and the real delete
write took 402.938 ms. The affected edge count was 406.

The OpenFGA throwaway-store rebuild took 67,802.654 ms. Its baseline lookup
returned 1,104 resources and its post-delete lookup returned 698. The delete
took 25.577 ms and the measured difference was 406 resources.

## Membership gated by token claims

The membership model uses the intended permission shape:

```text
permission member = enrolled + partner->admin + platform->admin
```

SpiceDB stores `enrolled` with the `tenant_claim` caveat. Its request context
contains the claimed organization list, the current time, and the expiration
time. SpiceDB caveats are request-time expressions with caller-supplied
context, as described in [SpiceDB caveats](https://authzed.com/docs/spicedb/concepts/caveats).

OpenFGA stores `member-t0` and `decayed-t0` as conditioned `enrolled` tuples.
Each tuple stores its organization and expiration context. The authorization
model metadata declares the enrolled user type as:

```json
{
  "type": "user",
  "condition": "tenant_claim"
}
```

The `tenant_claim` condition compares the stored organization with the request
`claimed_org` and requires `current_time` to precede `expires_at`. This uses
the condition metadata and tuple context pattern in [OpenFGA conditions](https://openfga.dev/docs/modeling/conditions) and [OpenFGA ABAC guidance](https://openfga.dev/docs/best-practices/modeling-abac).

### Tenant checks

| Case | SpiceDB p50 / p99 ms | SpiceDB | OpenFGA p50 / p99 ms | OpenFGA |
| --- | ---: | --- | ---: | --- |
| Member, token lists the tenant | 0.568 / 1.234 | allow | 0.675 / 2.532 | allow |
| Member, token lists nothing | 0.590 / 2.449 | deny | 2.713 / 33.003 | deny |
| Enrollment decayed | 1.571 / 14.278 | deny | 1.541 / 7.674 | deny |
| Partner admin, token lists the partner | 0.693 / 2.141 | allow | 1.228 / 3.716 | allow |
| Global admin, token lists the platform | 0.639 / 2.380 | allow | 0.954 / 3.378 | allow |
| Stranger | 0.554 / 1.135 | deny | 0.806 / 2.265 | deny |

### Resource checks

The resource permission intersects a site or Tag grant with tenant membership.
The corrected OpenFGA dataset writes the membership edge's `tenant` relation
to its `mtenant` object for every edge.

| Case | SpiceDB p50 / p99 ms | SpiceDB | OpenFGA p50 / p99 ms | OpenFGA |
| --- | ---: | --- | ---: | --- |
| Member with claim | 0.511 / 1.394 | allow | 2.755 / 6.026 | allow |
| Member without claim | 0.535 / 0.864 | deny | 1.262 / 4.143 | deny |
| Decayed enrollment | 0.483 / 0.862 | deny | 1.021 / 2.695 | deny |
| Partner admin without tenant claim | 0.501 / 0.958 | allow | 1.232 / 3.149 | allow |
| Global admin without tenant claim | 0.493 / 0.818 | allow | 1.042 / 2.543 | allow |
| Stranger | 0.459 / 0.813 | deny | 0.965 / 2.431 | deny |

### Membership resource lookup

| Subject | SpiceDB count | SpiceDB pages | OpenFGA count | OpenFGA elapsed ms |
| --- | ---: | ---: | ---: | ---: |
| Member Tag grant | 406 | 1 | 406 | 21.090 |
| Global admin | 2,000 | 3 | 2,000 | 53.667 |

Both engines reported complete results for both membership lookups.

## Summary

| Measure | SpiceDB | OpenFGA |
| --- | ---: | ---: |
| Core check throughput | 2,158.3 checks/s | 1,218.2 checks/s |
| Tag-derived resource lookup | 1,104 complete | 1,104 complete |
| Global resource lookup | 40,000 complete | 40,000 complete |
| Tag preview after subtree removal | 698 resources | 698 resources |
| Membership Tag lookup | 406 complete | 406 complete |
| Membership global lookup | 2,000 complete | 2,000 complete |

These measurements cover one laptop configuration and one dataset shape. They
do not establish capacity limits or production cost.

## Sources

- [SpiceDB consistency](https://authzed.com/docs/spicedb/concepts/consistency)
- [SpiceDB caveats](https://authzed.com/docs/spicedb/concepts/caveats)
- [SpiceDB migration from OpenFGA](https://authzed.com/docs/spicedb/migrate-to-spicedb/migrate-from/openfga)
- [OpenFGA conditions](https://openfga.dev/docs/modeling/conditions)
- [OpenFGA ABAC guidance](https://openfga.dev/docs/best-practices/modeling-abac)
