---
title: A Tag-Gated Container Integration Tier Lacking the Binary Under Test Is Silently Inert
date: 2026-09-27
last_verified: 2026-09-27
category: conventions
module: src/edge/netpen/test/integration
problem_type: bug
component: test_fixtures
severity: high
symptoms:
  - "An integration test tier compiles cleanly, but validation matrix rows remain unrecorded or pending indefinitely"
  - "Tests invoke binaries inside containers via docker exec, while the container image lacks the invoked executable"
  - "A test tier gated by build tags passes standard CI because the entire package is skipped when tags are omitted"
root_cause: "A containerized integration tier isolated behind build tags and skip guards references a stock upstream image that lacks the binary under test. Because untagged test suites skip the package and compilers do not inspect container filesystems, the missing executable produces no build failure and no test failure."
resolution_type: code_fix
applies_when:
  - "Designing, wiring, or debugging containerized integration test tiers gated by Go build tags or environment skip guards"
  - "Investigating an integration test tier or validation matrix where all tests compile and pass but results remain unrecorded or pending"
  - "A test invokes a binary inside a compose or docker container via docker exec, and you need to ensure the container runtime environment actually includes the built artifact"
related_components: [conformance-gates, netpen]
tags: [integration-testing, build-tags, docker-compose, test-containers, silent-pass, verification]
---

# A tag-gated container integration tier lacking the binary under test is silently inert

## The situation

When an integration test tier is placed behind a Go build tag and an environment skip guard, missing dependencies inside its target containers fail silently. The tests compile, untagged test suites pass, and regular verification never exercises the container.

In `src/edge/netpen/test/integration/`, the reproducibility test tier is guarded by a build tag and runtime checks (`src/edge/netpen/test/integration/t1_ae6_test.go:1,30-35`):

```go
//go:build netpen_t1
...
if testing.Short() {
	t.Skip("live t1 lab disabled in short mode")
}
if testenv.Target() == "" {
	t.Skip("no t1 target; lab did not start")
}
```

The test runner invokes the tool inside the container (`src/edge/netpen/test/integration/t1_ae6_test.go:55-56`):

```go
cmd := exec.CommandContext(ctx, "docker", "exec", "netpen-t1-frr-r1",
	"netpen", attack, "--json=true", "-i", "eth0", "--duration", "2s", "--timeout", "20s")
```

The compose file originally configured `frr-r1` with a stock upstream image (`src/edge/netpen/test/integration/t1/testdata/frr/docker-compose.yml:6-7`):

```yaml
services:
  frr-r1:
    image: quay.io/frrouting/frr:10.1.0
```

The stock image never contained the `netpen` binary. A comment in the compose file acknowledged this omission: "the image does not include the netpen binary that the command-execution tests require". Because default CI and developer test runs omitted `-tags netpen_t1`, the code compiled cleanly and tests passed or skipped. The tier was structurally un-runnable, leaving corresponding cells in `src/edge/netpen/test/integration/VALIDATION_MATRIX.md` unrecorded.

## Why it bites

Gating integration tests behind build tags and environment guards isolates slow or container-dependent tests from fast unit runs. That isolation turns runtime configuration defects into silent omissions rather than failures:

- Compilers only check host-side Go code. They cannot verify whether a container image contains the executables invoked through `docker exec`.
- Skip conditions (`testing.Short()`, missing daemon) evaluate before container execution. When prerequisites are missing, tests skip cleanly.
- Without execution evidence, an un-runnable test looks identical to an untriggered test.

The result is a tier that provides false reassurance: test files exist, fixtures are present, the package builds, and the code under test is never exercised.

## The working pattern: layer the binary into the container fixture

To make a containerized tier runnable without manual operator intervention, the compose fixture must build and layer the local binary directly onto the target container image.

Define a multi-stage Dockerfile that builds the static binary from the workspace and copies it into the target base image (`src/edge/netpen/test/integration/t1/testdata/frr/Dockerfile:1-11`):

```dockerfile
FROM golang:1.27-bookworm AS builder

WORKDIR /src
COPY . .
WORKDIR /src/src/edge/netpen
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /netpen ./cmd/netpen

FROM quay.io/frrouting/frr:10.1.0

COPY --from=builder /netpen /usr/local/bin/netpen
```

Wire the build definition into the service block instead of referencing a stock image (`src/edge/netpen/test/integration/t1/testdata/frr/docker-compose.yml:6-9`):

```yaml
services:
  frr-r1:
    build:
      context: ../../../../../../../..
      dockerfile: src/edge/netpen/test/integration/t1/testdata/frr/Dockerfile
```

When the test harness brings up the fixture, the container is self-sufficient. Commands invoked via `docker exec` find the binary at `/usr/local/bin/netpen`.

## Evidence

In commit `744c0e67`, replacing the stock upstream image with the combined image enabled the tier to run to completion. The validation matrix recorded the live execution results across all eight superset behaviors (`src/edge/netpen/test/integration/VALIDATION_MATRIX.md:124-133`):

> `| ospf | (a) | FRR r1 lab segment | 2026-09-27: source-(c), finding:ospf | IOS-XE live lab | ...`

Before this fix, the prerequisites section admitted the manual burden (`src/edge/netpen/test/integration/VALIDATION_MATRIX.md:144-146`):

> "The supplied FRR image does not include netpen. The operator must install a compatible binary in `netpen-t1-frr-r1` before running AE5 or AE6. The harness does not install it."

After layering the binary into the fixture image, the harness executes without out-of-band installation steps.

## What this does not cover

This rule applies to local container environments under test execution. It does not replace live device or vendor validation: a synthetic container running the local binary exercises internal logic and repeatability, but does not prove interoperability with real vendor network operating systems.
