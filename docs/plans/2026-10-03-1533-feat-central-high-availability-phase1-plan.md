---
title: Central High Availability, Phase 1, Edge Listener Split - Plan
type: feat
date: 2026-10-03
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md
parent: docs/plans/2026-10-03-1533-feat-central-high-availability-plan.md
---

# Central High Availability, Phase 1, Edge Listener Split - Plan

## Goal

The device service serves the edge-facing services on their own address, so
a deployment can expose them without exposing the operator services. The
means is a third address in `ServiceListeners` and a second HTTP server in
the host.

Stop condition: the plan is wrong if any handler mounted for an edge needs a
route that only the operator mux holds.

## Decisions

The parent's decisions apply
([parent plan](2026-10-03-1533-feat-central-high-availability-plan.md)). This phase adds:

- The new field is `ServiceListeners.edge`, required, with the same pattern
  and port rule as `bus`. Why: an edge dials `central_url`, which names this
  listener, so a kernel-chosen port would match nothing an edge was told.
- `EdgeEnrollmentPolicy.central_url` keeps its name and now points at the
  edge listener. Why: `EdgeProvisioning.central_url` is derived from it and
  already means "where an edge dials".
- One Service Module binds and serves both HTTP listeners. Why: the README
  lists six modules supervised `RestForOne` with the hub first, and a seventh
  would change the restart order for no gain.
- Both listeners serve the certificate `serverTLS` builds. Why: an edge pins
  one digest for the edge listener and the bus
  (`src/services/device/internal/host/serve.go`).
- `Enroll` and `UploadCapture` move with the other edge procedures and keep
  their own body bounds. Why: they are mounted in front of the assertion
  middleware for reasons that do not depend on the listener.

## Requirements

1. A configuration without `listeners.edge` is refused at load. Example:
   the current `deploy/lab/central.textproto` makes `host.LoadConfig` return
   a validation error naming `listeners.edge`.
2. The edge listener serves only edge procedures. Example: a POST to
   `/flowseer.api.device.v1.DeviceService/GetDeviceAccessStatus` on the edge
   address returns HTTP 404.
3. The API listener serves only operator procedures. Example: a POST to the
   `EdgeService` `Enroll` procedure on the API address returns HTTP 404.
4. Both listeners present the same certificate. Example: the leaf
   certificate's public key digest read from each address is equal.
5. An agent enrolls and attaches through the edge listener. Example: the
   existing end-to-end test passes with `central_url` set to the edge address.

## Out of scope

- Authorization for the operator services.
- A plain-HTTP mode for either listener.
- Moving NATS out of the process, which the second phase does.
- Kubernetes manifests, which the deployment plan writes.

## Units

### U1. Schema

Files: spec/proto/flowseer/store/device/v1/service_config.proto, spec/proto/flowseer/store/device/v1/README.md, generated/go/proto/flowseer/store/device/v1/service_config.pb.go
After: none
Change: `ServiceListeners` has an `edge` field after `bus`, a required
`host:port` string under the same validation as `bus`. Its comment says which
services it serves and that `central_url` names it. The comment on `api` says
it serves the operator services only. The generated file is `buf generate`
output (`go tool -modfile=tools/buf/go.mod buf generate`), never edited by
hand.
Tests: `go tool -modfile=tools/buf/go.mod buf lint` passes. Behavior is
proven in U2.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/store/device/v1/service_config.proto spec/proto/flowseer/store/device/v1/README.md`

### U2. Host, lab files, and docs

Files: src/services/device/internal/host/config.go, src/services/device/internal/host/serve.go, src/services/device/internal/host/host.go, src/services/device/internal/host/certificate.go, src/services/device/internal/host/certificate_test.go, src/services/device/internal/host/shutdown_test.go, src/services/device/internal/host/config_test.go, src/services/device/internal/host/host_test.go, src/services/device/internal/host/serve_internal_test.go, src/services/device/internal/host/serveconnect_internal_test.go, src/services/device/test/integration/fixture_test.go, src/services/device/test/integration/bootstrap_env_test.go, src/services/device/test/integration/e2e_test.go, src/services/device/test/integration/capture_test.go, src/services/device/test/integration/runbook_test.go, src/services/device/test/integration/lab_fixtures_test.go, deploy/lab/central.textproto, deploy/lab/README.md, docs/runbooks/lab-icx7150-first-write.md, src/services/device/README.md, docs/architecture/2026-08-20-device-service-and-inventory-direction.md
After: U1
Change: `Config` has an `EdgeAddress` accessor. `serve.go` builds two muxes:
the edge mux holds the `EdgeService`, `DispatchService`, `AuditService`, and
`CaptureEdgeService` routes with their middleware and body bounds unchanged,
and the API mux holds `EdgeAdminService`, `DeviceService`, and
`CaptureService`. The Connect module binds both addresses before serving
either, closes the first if the second fails to bind, and shuts both down
within `shutdownGrace`. It logs the edge address it bound. The generated
certificate names the edge address beside the API and bus addresses
(`certificate.go`, `addSubjectNames`). The lab configuration names an edge
address on loopback and points `central_url` at it. The runbook's operator
calls keep the API address, and its `write-provisioning.sh` step passes the
edge address, since that value becomes the agent's `central_url`. The README's
Deployment section describes the two listeners. The device service record's
sentence on a TLS-terminating ingress becomes TLS passthrough, with the pin
as the reason.
Tests: `host_test.go` gains four cases: an operator procedure on the edge
address returns 404, `Enroll` on the API address returns 404, both addresses
present the same public key digest, and a run whose edge address is already
in use returns an error and leaves the API address free to bind again.
`UploadCapture` on the API address returns 404 as `Enroll` does.
`shutdown_test.go` gains the edge-listener twin of
`TestShutdownCutsAConnectionThatOutlastsTheGrace`. `certificate_test.go`
extends `TestTheGeneratedCertificateNamesWhatTheDeploymentBinds` to the edge
address. `TestAListenerWithoutAPortIsRefused` covers `listeners.edge`. `config_test.go` gains a case
that a file without `listeners.edge` is refused. The integration fixtures
dial the edge address for every edge call (`capture_test.go` passes
`baseURL()` to the edge client and to every agent it starts), and
`e2e_test.go` passes
unchanged in what it asserts. `lab_fixtures_test.go` loads the edited lab
file.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device deploy/lab docs/runbooks/lab-icx7150-first-write.md docs/architecture/2026-08-20-device-service-and-inventory-direction.md`

Waves: U1 | U2

## Verification

`go test -race ./src/services/device/...` passes, and the verifier is green
for every path above. The runbook's `sh` blocks are run by the integration
suite, so a stale address there fails a test.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/services/device/README.md` and the schema README describe the
      edge listener.
- [ ] The parent's `Landed:` line for this phase carries the commit range.
- [ ] This plan's `status` is set, with an outcome note under its title.
- [ ] No plan labels in code.

## Open questions

- Whether a test fixture can still ask for a kernel-chosen edge port. The
  `bus` field cannot, and the fixtures already choose a bus port, so the
  same helper should serve both.
