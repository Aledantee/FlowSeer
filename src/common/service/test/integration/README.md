# Service OpenTelemetry integration tests

The `service_otel_integration` tier starts a pinned OpenTelemetry Collector and
runs the service package through its public API. It checks OTLP/HTTP and gRPC,
module signal policies, durable-message trace propagation, exporter ownership,
outage behavior, and shutdown flushes.

Run the tier from the repository root:

```sh
go test -race -tags=service_otel_integration ./src/common/service/test/integration/...
```

Docker must be running. The first run may need registry access to fetch
`otel/opentelemetry-collector-contrib:0.160.0` at the digest pinned in the test
helper. The tests clear inherited `OTEL_*` values, allocate unique service
identities, and do not use external credentials.

Collector output stays in test-owned temporary directories. A wrapper may set
`FLOWSEER_OTEL_TEST_ARTIFACT_DIR` to an empty directory that it owns. On test
failure, the helper scans bounded Collector logs and OTLP files for the synthetic
secret sentinel before copying them there. A failed scan withholds the artifacts.
Successful runs leave the wrapper directory untouched.

The outage case exercises the runtime's full bounded retry window and normally
takes about 40 seconds. Use `-short` to skip the entire Docker-backed tier when
running broad local checks without its prerequisites.
