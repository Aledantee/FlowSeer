---
title: The OTel Collector gate on a Colima host needs the Colima socket and a TMPDIR under home
date: 2026-10-14
last_verified: 2026-10-14
category: conventions
module: src/common/service/test/integration
problem_type: bug
component: test_fixtures
severity: medium
symptoms:
  - "`run.py verify service-otel-integration` fails every TestManagedTelemetry* test with `get provider: rootless Docker not found, failed to create Docker provider` while `docker info` succeeds"
  - "With DOCKER_HOST set, the same tests fail with `invalid mount config for type \"bind\": bind source path does not exist: /var/folders/...`"
root_cause: "testcontainers-go finds the daemon through DOCKER_HOST or the default socket, not through the active docker CLI context, so Colima's socket is invisible to it. The Collector fixture bind-mounts a t.TempDir() directory, which lives under macOS's /var/folders, and Colima shares only $HOME with its VM."
resolution_type: environment_setup
applies_when:
  - "Running `run.py verify change` or `verify service-otel-integration` on a macOS host whose Docker daemon is Colima"
  - "A testcontainers test fails with `rootless Docker not found` although `docker info` works"
  - "A container test fails with `bind source path does not exist` for a path under /var/folders"
related_components: [verification, service-runtime]
tags: [integration-testing, test-containers, colima, docker, verification, macos]
---

# The OTel Collector gate on a Colima host needs the Colima socket and a TMPDIR under home

The verifier runs the service OpenTelemetry tier
(`tools/scripts/verify/service_otel_integration.py`) whenever a change
reaches `src/common/service`. On a Mac whose Docker daemon is Colima, the
tier fails twice in a row for reasons that have nothing to do with the
change, and `docker info` succeeds throughout, so the gate's own Docker
probe (`service_otel_integration.py:35`) passes.

## Why it fails

First failure: testcontainers-go v0.44.0 resolves the daemon from
`~/.testcontainers.properties`, then `DOCKER_HOST`, then a fixed list of
socket paths, and falls back to the rootless socket
(`~/go/pkg/mod/github.com/testcontainers/testcontainers-go@v0.44.0/internal/core/docker_host.go:80-87`).
The docker CLI's active context (`colima *`, endpoint
`unix:///Users/<user>/.colima/default/docker.sock`) is not on that path,
and `/var/run/docker.sock` does not exist under Colima. The run ends in:

```text
otel_test.go:37: start pinned OpenTelemetry Collector: get provider: rootless Docker not found, failed to create Docker provider
```

Second failure: the Collector fixture bind-mounts a fresh `t.TempDir()`
into the container
(`src/common/service/test/integration/testenv/otel.go:43` and `:59-64`):

```go
outputDir := t.TempDir()
...
hostConfig.Mounts = append(hostConfig.Mounts, mount.Mount{
	Type:   mount.TypeBind,
	Source: outputDir,
	Target: "/output",
})
```

`t.TempDir()` lives under `$TMPDIR`, which macOS sets to `/var/folders/...`.
Colima mounts only `$HOME` into its VM by default (`~/.colima/default/colima.yaml`,
"Colima default behaviour: $HOME is mounted as writable", `mounts: []`), so
the daemon cannot see the directory:

```text
otel_test.go:37: start pinned OpenTelemetry Collector: create container: container create: Error response from daemon: invalid mount config for type "bind": bind source path does not exist: /var/folders/98/.../TestManagedTelemetryHTTP543693162/001
```

## How to run it

Set all three variables for the verifier run, with the sandbox disabled:

```bash
mkdir -p ~/.cache/flowseer-tmp
TMPDIR=$HOME/.cache/flowseer-tmp \
DOCKER_HOST=unix://$HOME/.colima/default/docker.sock \
TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock \
uv run tools/scripts/run.py verify change --base main
```

`TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE` names the socket path inside the
VM, which testcontainers mounts into its reaper container
(`docker_host.go:105-110`). With these set, the tier passed on
2026-10-14 (macOS Darwin 25.6.0, Colima 0.10.3):

```text
ok  	go.aledante.io/FlowSeer/src/common/service/test/integration	87.085s
```

## What it does not cover

Docker Desktop shares `/var/folders` and publishes `/var/run/docker.sock`, so
none of this applies there. A Colima profile with a custom `mounts:` list
that includes the temp directory needs only the two socket variables. The
fixture itself is correct: making it write under the repository instead of
`t.TempDir()` would trade a host setup step for test output in the tree.
