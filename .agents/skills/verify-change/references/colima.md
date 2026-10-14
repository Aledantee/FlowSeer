# Run the service OpenTelemetry tier under Colima

Load when the active Docker context is Colima and the service OpenTelemetry
tier is selected. A successful Docker CLI probe does not establish that the
Go test process uses the same socket.

Keep an explicit `DOCKER_HOST`. When it is unset, use the Docker endpoint
reported by the active context's inspection, including its `unix://` prefix.
Set `TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE` to `/var/run/docker.sock` for
the socket inside Colima's VM. Set `TMPDIR` to an existing directory under
the home directory that the VM mounts. Keep these settings local to the
gate invocation, then run the verifier as usual.

The Collector fixture bind-mounts its temporary output directory in
[`testenv/otel.go`](../../../../src/common/service/test/integration/testenv/otel.go).
The Testcontainers version is pinned in [`go.mod`](../../../../go.mod).
Check that version's `internal/core/docker_host.go` when changing socket
discovery or the override. A different VM mount configuration needs a
matching temporary directory.
