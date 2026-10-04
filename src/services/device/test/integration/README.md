# Device Service Integration Tests

Integration tests in this package verify service behavior against container
dependencies. They need a reachable Docker daemon and the environment below.

## Running the tier

Run the whole package with the `authz_integration` build tag, which exercises
`authz_enforcement_test.go` and the OpenFGA container fixtures:

```bash
DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" \
TMPDIR="$HOME/tmp" \
TESTCONTAINERS_RYUK_DISABLED=true \
go test -v -race -tags=authz_integration ./src/services/device/test/integration/
```

`TESTCONTAINERS_RYUK_DISABLED=true` turns off the testcontainers reaper, so
each test must terminate its own containers. The benchmark does this in a
cleanup that reports a failure. `TestOpenFGAListObjectsStopsAtTheCap` starts
the environment with `OPENFGA_LIST_OBJECTS_MAX_RESULTS=10`, grants 15 edges,
and checks that `ListObjects` returns 10 objects and that the response message
descriptor holds only the `objects` field. Nothing in the response marks the
result partial.

## Running the OpenFGA benchmark

```bash
DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" \
TMPDIR="$HOME/tmp" \
TESTCONTAINERS_RYUK_DISABLED=true \
go test -tags=authz_integration -run '^$' -bench . -benchtime=1000x ./src/services/device/test/integration/
```

`-benchtime=1000x` gives every path 1000 iterations, matching the 1000
sequential calls per row the authorization spike used.

## Benchmark fixture

`BenchmarkOpenFGA` provisions Postgres and OpenFGA in one container network,
runs `openfga migrate`, writes the embedded authorization model, and loads:

- 20 tenants
- 2,000 edges per tenant (40,000 edges)
- 4,000 capture sessions per tenant (80,000 sessions)
- 200 users per tenant on one of 10 roles

The checks run through `openfga.New`, the same checker the device service
uses. OpenFGA's check query cache and cache controller stay at their defaults,
both off (`github.com/openfga/openfga@v1.21.0/pkg/server/config/config.go`), so
every Check and BatchCheck reaches the datastore uncached. The benchmark raises
`OPENFGA_REQUEST_TIMEOUT` to 30s so the 100-tuple fixture writes finish on a
shared host. A measured sub-benchmark fails when its slowest sample reaches
OpenFGA's default 3s `requestTimeout`, so a run that prints metrics held every
call under that bound.

## What the output means

Each measured path is a sub-benchmark: `platform_admin_edge_capture`,
`tenant_capturer_edge_capture`, `no_grant_edge_capture`,
`platform_admin_tenant_full_payload`, `batch_check_50_sessions`,
`member_token_lists_tenant`, `member_token_lists_nothing`,
`enrollment_decayed`, `partner_admin_token_lists_partner`,
`global_admin_token_lists_platform`, and `stranger_member`. Each reports two
custom metrics:

| Metric | Unit | Meaning |
| --- | --- | --- |
| `p50_ms` | milliseconds | median latency over the samples |
| `p99_ms` | milliseconds | 99th percentile latency, nearest rank |

Each sub-benchmark takes one latency sample per iteration, so a path records
`b.N` samples (1000 under `-benchtime=1000x`). The `ns/op` column the testing
framework prints is the timed section divided by `b.N`, which includes the
timing overhead the sampler adds. Read `p50_ms` and `p99_ms`.

Every path states its expected answer, and the benchmark fails on an error or
an answer that differs. From the embedded model and the fixture:

| Path | Expected answer |
| --- | --- |
| `platform_admin_edge_capture` | allowed, the platform admin holds the tenant |
| `tenant_capturer_edge_capture` | allowed, the user reaches capturer through a role assignee |
| `no_grant_edge_capture` | denied |
| `platform_admin_tenant_full_payload` | denied, `full_payload` is not inherited |
| `batch_check_50_sessions` | 50 results, all allowed |
| `member_token_lists_tenant` | allowed, claimed and enrolled |
| `member_token_lists_nothing` | denied, enrolled without the claim |
| `enrollment_decayed` | denied, claimed without enrollment |
| `partner_admin_token_lists_partner` | allowed through the partner active admin |
| `global_admin_token_lists_platform` | allowed through the platform admin |
| `stranger_member` | denied |

A `batch_check_50_sessions` iteration is one gRPC call, because `BatchCheck`
sends at most 50 checks per call.

## Reading the numbers

The benchmark's output depends on the host and the load on it, so treat the
reported latencies as supporting evidence for the workload rather than
capacity figures. The check cache is off and one caller runs at a time, so the
spread across paths matters more than any single value. See
[`docs/solutions/conventions/a-comparison-spike-needs-controls-row-provenance-and-noise-bounds.md`](../../../../../docs/solutions/conventions/a-comparison-spike-needs-controls-row-provenance-and-noise-bounds.md).
