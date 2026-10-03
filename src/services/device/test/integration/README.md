# Device Service Integration Tests

Integration tests in this package verify service behavior against container
dependencies.

## Running the tier

Run the integration suite with the `authz_integration` build tag and container
environment variables:

```bash
DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" \
TMPDIR="$HOME/tmp" \
TESTCONTAINERS_RYUK_DISABLED=true \
go test -v -race -tags authz_integration ./src/services/device/test/integration
```

To run the OpenFGA benchmark against Postgres:

```bash
DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" \
TMPDIR="$HOME/tmp" \
TESTCONTAINERS_RYUK_DISABLED=true \
go test -tags authz_integration -bench=BenchmarkOpenFGA -benchmem -run=^$ ./src/services/device/test/integration
```

## Benchmark fixture

`BenchmarkOpenFGA` provisions Postgres and OpenFGA in a container network, applies
database migrations, and loads:

- 20 tenants
- 2,000 edges per tenant (40,000 edges total)
- 4,000 capture sessions per tenant (80,000 sessions total)
- 200 users per tenant across 10 roles

The benchmark measures p50 and p99 latency for platform administration, tenant
capturer evaluation, denied queries, batch checks, and token-gated tenant
membership paths.
