---
title: Edge Attachment Contracts - Plan
type: feat
date: 2026-09-05
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
execution: code
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-08-20-network-model-structure-direction.md
---

# Edge Attachment Contracts - Plan

> Implemented. The key proof messages live in their own `key_proof.proto`,
> and `IntegrationConfig` gained the `edge` host field with a rule that a
> local-network integration names one, so the amended direction record and
> the schema say the same thing.

## Goal

An edge agent shipped to a customer with a pre-provisioned setup key enrolls
with central on first boot over Connect, proves the same identity on every
later call with a key it generated itself, and keeps that standing through
any length of downtime until an operator retires it. The means is one new
schema package, `flowseer.api.edge.v1`, holding the Edge entity, the signed
assertion the edge sends with each call, and the two Connect services, one
the edge calls and one the operator calls, with the direction record amended
to match. No handler is implemented here; the first host plan implements the
services against these contracts.

Stop if a landed Go package already stores or routes on a host field of
`Integration`, because the Edge entity then needs a migration this plan does
not contain.

## Decisions

- Connect over HTTPS is the edge's relationship with central; NATS is a later
  capability handed over that relationship. Why: the user directed it, and the
  direction record already gives Connect the enrollment job. Bus credential
  issuance is a future RPC on the same service and appears nowhere in this
  plan.
- The Edge is its own top-level entity, distinct from `Integration`, and the
  direction record is amended where it says otherwise: its summary calls
  the edge agent "itself an integration of kind local network", its
  inventory table says the host process is "not an entity of its own", and
  its churn list enrolls an edge by creating a local-network Integration.
  Why: user-confirmed on 2026-09-05. One process hosts several integrations,
  a site that only hosts an on-prem controller adapter would otherwise need
  a hollow local-network Integration to exist as the host, and an
  integration's `credential_ref` describes the platform credential, not the
  process identity. Under the amended record an integration names its host
  by `EdgeGlobalRef` where it runs on an edge.
- The Edge does not join `EntityType` in this change. Why: admission requires
  a cascading delete of attribute values and an existence check, both of
  which need the store the first host plan builds. The conventions doc
  records the deliberate non-admission next to the tenant exception, and
  `api/edge/v1` therefore imports nothing from `api/inventory/v1`.
- Two secrets with different lifetimes. The setup key is a single-use bearer
  secret minted by the operator and consumed at `Enroll`. The edge identity is
  an Ed25519 key pair the edge generates before its first call; central stores
  only the public key. Why: a key in a shipped box cannot be short-lived, and
  central holding no private material means a central breach cannot
  impersonate an edge.
- Every authenticated call carries a fresh signed assertion; there is no
  access token. Why: Ed25519 costs microseconds per call, and dropping the
  token removes a bearer secret, a token store, and a refresh path. Streams
  are checked at open.
- Ed25519 is the only algorithm and is fixed at the verifier. Why: no
  algorithm negotiation means no algorithm-confusion class of bug.
- The assertion is a signed protobuf, carried as `payload` bytes plus
  `signature` bytes, and the verifier checks the signature over the payload
  bytes exactly as received. Why: signing the serialized bytes avoids any
  canonicalization, and a protobuf envelope avoids JWT parsing pitfalls.
- Lifecycle and contact are separate axes. Lifecycle is `PENDING`,
  `ENROLLED`, `RETIRED`; contact is `ACTIVE`, `STALE`, `DORMANT`, derived
  from the last heartbeat. Why: `DeviceLifecycle` already keeps reachability
  out of the lifecycle word, and an edge that comes back after months must
  renew silently, which it can only do if nothing but an operator retires it.
- Retirement is by hand only. Why: user-confirmed; consistent with the
  device offline rule.
- Setup keys bind to one pre-created Edge and carry an operator-set expiry
  defaulting to 180 days. Why: user-confirmed; a bound key gives the shipment
  view for free and a key nobody remembers to revoke should die on its own.
- `Enroll` is idempotent for the same setup key and the same public key, and
  refuses the same setup key with a different public key. Why: an edge that
  crashes between the call and persisting the response must recover, and a
  second public key is what a stolen key looks like.
- Both `Enroll` and `Rekey` carry a proof of possession of the key being
  registered: a `payload` of serialized bytes plus a `signature` by the new
  key over those bytes exactly as received, the same shape as the assertion.
  Why: nobody registers a key they do not hold, and the verifier must never
  re-serialize to check a signature.
- Connect heartbeats are the liveness source until bus attach exists. When
  the bus lands, its plan decides whether announce replaces or feeds the
  heartbeat; the contact enum stays either way. Why: the direction record
  names announce as the one source of "who's alive", and this plan must not
  leave two.
- The setup key is a system-issued record, `SetupKey` with id, status,
  `issued_at`, and `expires_at`, defined beside the triad and carried on
  `EdgeState`. The operator's chosen expiry is an RPC parameter at issue
  time, not stored intent, because the key is consumed once. The SHA-256 of
  the key lives only in the service store and never in a message.
- The shipped provisioning file is a protobuf message the operator RPC
  returns, carrying the central URL, the setup key, and the pinned trust
  anchor. Why: the file format is then a schema, not a convention, and the
  pin is what keeps a customer network's TLS interception from reading
  assertions.
- `Enroll` and `Heartbeat` responses carry server time. Why: a boxed device
  can boot with a dead clock, and assertion timestamps need a trusted time
  source until NTP catches up.
- Authorization is per edge id, derived from the verified assertion; the edge
  never names its tenant. Why: the ambient tenancy convention.
- Device credentials, execute, events, and bus credentials are out of scope.
  Why: each is its own contract and this plan stays under the size limit.

## Requirements

1. R1. `flowseer/api/edge/v1/edge.proto` defines `EdgeLocalRef`,
   `EdgeGlobalRef`, `EdgeLifecycle`, `EdgeContact`, `EdgeConfig`,
   `EdgeState`, and `EdgeEvent`. `EdgeConfig` holds name and description.
   `EdgeState` holds lifecycle, contact, the Ed25519 public key, the current
   `SetupKey` record, `enrolled_at`, and `last_seen_at`.
   Example: an `EdgeState` with lifecycle `ENROLLED` and no `public_key`
   fails validation with id `edge_state.enrolled_has_key`.
2. R2. `EdgeContact` values `ACTIVE`, `STALE`, `DORMANT`, with the thresholds
   left to the service and named in the README as tenant policy. Example: a
   `PENDING` edge carries contact `UNSPECIFIED` and passes; an `ENROLLED`
   edge with contact `UNSPECIFIED` fails `edge_state.enrolled_has_contact`.
3. R3. `flowseer/api/edge/v1/assertion.proto` defines `EdgeAssertion` with
   `edge` ref, `audience`, `issued_at`, `expires_at`, `nonce` (16 bytes), and
   `SignedEdgeAssertion` with `payload` and `signature` (64 bytes). Validity
   is at most 60 seconds. Example: an assertion with `expires_at` 120 seconds
   after `issued_at` fails `edge_assertion.short_lived`.
4. R4. The README fixes the header: `Authorization: FlowSeer-Edge <base64
   of SignedEdgeAssertion>`, standard base64 without padding, and lists the
   verifier's checks in order: decode, signature over `payload` with the
   stored key, edge lifecycle `ENROLLED`, audience equals the central's
   configured audience, `issued_at` within skew, `expires_at` in the future,
   nonce unseen within the validity window. Example: the README shows a
   worked request with a real base64 value from a fixed test vector.
5. R5. `flowseer/api/edge/v1/provisioning.proto` defines `EdgeProvisioning`
   with `central_url`, `setup_key`, and `trust_anchors` (repeated SPKI SHA-256
   pins, 32 bytes each, at least one). Example: a provisioning message with an
   empty `trust_anchors` fails validation.
6. R6. The setup key is the string `fse1_<id>_<secret>`, id 26 and secret 52
   lowercase base32 characters without padding, covering 128 and 256 random
   bits. Central stores the SHA-256 of the whole string. The README states
   this and that the id alone is safe to log. Example: the pattern rule on
   `EdgeProvisioning.setup_key` rejects `fse1_abc`.
7. R7. `flowseer/api/edge/v1/edge_service.proto` defines `EdgeService` with
   `Enroll`, `Rekey`, and `Heartbeat`, each with its own request and
   response. `EnrollRequest` carries the setup key and a `KeyProof`:
   `payload` (a serialized `KeyProofPayload` with `setup_key_id` and
   `public_key`) and `signature` (64 bytes) by that public key over the
   payload bytes. `EnrollResponse` carries the edge ref, `server_time`, the
   audience, and `trust_anchors`. `RekeyRequest` carries only a `KeyProof`
   whose payload names the new public key and the assertion nonce of the
   call; the calling edge is the one the assertion identifies. `RekeyResponse`
   carries `server_time`. `HeartbeatRequest` carries agent version and a
   buffered-since timestamp; `HeartbeatResponse` carries `server_time`.
   Example: a `KeyProofPayload` whose `public_key` is not 32 bytes fails
   validation.
8. R8. `flowseer/api/edge/v1/edge_admin_service.proto` defines
   `EdgeAdminService` with `CreateEdge` (returns the Edge and its
   provisioning), `IssueSetupKey` (replaces the setup key on a `PENDING` or
   `RETIRED` edge and returns provisioning), `RevokeSetupKey`, `RetireEdge`,
   `GetEdge`, and `ListEdges`. `CreateEdgeRequest` carries name,
   description, and an optional setup key `expires_at`;
   `IssueSetupKeyRequest` carries the edge ref and the same optional
   `expires_at`. Example: `IssueSetupKeyRequest` with `expires_at` unset
   yields a key expiring in 180 days, and the README says so.
9. R9. The README records the state transitions and which RPC causes each:
   `CreateEdge` to `PENDING`, `Enroll` to `ENROLLED`, `RetireEdge` to
   `RETIRED`, `IssueSetupKey` on a retired edge back to `PENDING`. A retired
   edge fails at the verifier, not at a handler. An enrolled edge that lost
   its private key recovers by `RetireEdge`, `IssueSetupKey`, and a fresh
   `Enroll`, and the table says so. Example: a table in the README with one
   row per transition.
10. R10. The device-service direction record describes this design wherever
    it touches enrollment or the edge's channel: the summary paragraph's
    "Connect kept for" list, the "Connect keeps three jobs" bullet, and the
    enrollment bullet that lists what `Enroll` returns all change to a
    long-lived single-use setup key, an edge-generated key, per-call signed
    assertions over Connect, heartbeat over Connect as the liveness source
    until bus attach lands, pinning in the shipped file, and NATS credentials
    as a later RPC. The open question on credential handling stays. Example:
    the record no longer says "short-TTL" of the setup key or that `Enroll`
    returns a JWT seed.
11. R11. `CONCEPTS.md` gains an Edge entry and its Integration entry no
    longer calls the edge agent an integration. `docs/conventions/protobuf.md`
    names the edge package where it says refs live beside the triad, and
    records the Edge's deliberate non-admission to `EntityType` next to the
    tenant exception, naming the store work that lifts it. Example: grep for
    "edge agent" in `CONCEPTS.md` finds only the Edge entry.
12. R12. `buf generate` produces Go and Connect code for the package and
    `go build ./...` passes with `connectrpc.com/connect` added to `go.mod`.
13. R13. The network-model-structure direction record's package tree gains
    `api/edge/v1` with the note that it is the first Connect service package,
    and the dependency graph records that it imports no FlowSeer package and
    that the event envelope must never import it. Example: the record's
    sentence that service paths "need an accepted amendment before the first
    schema lands" is replaced by the settled path.

## Out of scope

- Any handler, store, or verifier in Go. The first host plan implements
  `EdgeService` and its verifier interceptor.
- Bus credential issuance, execute, events, and device credential delivery.
- A reusable setup key that creates edges at first contact.
- Automatic retirement of dormant edges.
- TPM or secure-element key storage on the edge.
- The provisioning file's on-disk encoding on the edge; the schema fixes the
  content, the edge agent chooses JSON or binary.

## Units

### U1. Edge entity family

Files: `spec/proto/flowseer/api/edge/v1/edge.proto`

Change: defines the refs, the two enums, and the triad per R1 and R2, with
CEL rules `edge_state.enrolled_has_key`, `edge_state.enrolled_has_contact`,
and `edge_event.lifecycle_changes` mirroring `integration_event`. The public
key is `bytes` of length 32. Setup key status is an enum `SetupKeyStatus`
with `ISSUED`, `CONSUMED`, `REVOKED`, `EXPIRED`.

Tests: buf lint; `test/conformance/proto/api_edge_rules_test.go`, shaped
like `api_inventory_rules_test.go`, with one valid and one invalid case per
CEL rule.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/edge/v1/edge.proto`

### U2. Assertion and provisioning messages

Files: `spec/proto/flowseer/api/edge/v1/assertion.proto`,
`spec/proto/flowseer/api/edge/v1/provisioning.proto`

Change: defines `EdgeAssertion`, `SignedEdgeAssertion`, and
`EdgeProvisioning` per R3, R5, and R6, with the setup key pattern
`^fse1_[a-z2-7]{26}_[a-z2-7]{52}$`.

Tests: one valid and one invalid case per rule in
`test/conformance/proto/api_edge_rules_test.go`; the header test vector of
R4 is computed there too.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/edge/v1`

### U3. The two services

Files: `spec/proto/flowseer/api/edge/v1/edge_service.proto`,
`spec/proto/flowseer/api/edge/v1/edge_admin_service.proto`

Change: defines `EdgeService` and `EdgeAdminService` per R7 and R8. Every
RPC has its own request and response message. Request messages validate
byte lengths and the setup key pattern; `ListEdgesRequest` carries a page
size and token.

Tests: buf lint; request validation cases in the conformance test.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/edge/v1`

### U4. Generation and the Connect dependency

Files: `go.mod`, `go.sum`, `generated/go/proto/flowseer/api/edge/v1/`

Change: `go get connectrpc.com/connect` at the current release, then
`buf generate`. The build passes with no Go code referencing the package.

Tests: `go build ./...`, `go vet ./...`.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- go.mod spec/proto/flowseer/api/edge/v1`

### U5. Package README and repository docs

Files: `spec/proto/flowseer/api/edge/v1/README.md`, `CONCEPTS.md`,
`docs/conventions/protobuf.md`,
`docs/architecture/2026-08-20-device-service-and-inventory-direction.md`,
`docs/architecture/2026-08-20-network-model-structure-direction.md`,
`spec/proto/flowseer/api/inventory/v1/README.md`,
`spec/proto/flowseer/api/inventory/v1/integration.proto`

Change: the README covers R4, R6, and R9 with a worked header example from
a fixed test vector, the transition table, the verifier check order, the
threat model in three paragraphs (setup key in a box, private key on disk,
what central holds), and what is deliberately absent (bus credentials,
device credentials). The device-service record changes per R10 and, for
the entity question, per the Decisions section: the summary paragraph, the
inventory table row for Integration, the "host process ... not an entity
of its own" sentence, and the "Enroll an edge agent" churn bullet all say
what the Decisions say. The network-model record changes per R13. The
integration file comment, the inventory README's Integrations section, and
the `CONCEPTS.md` Integration entry stop calling the edge agent an
integration where the Decisions make it an Edge.

Tests: none beyond the doc checks in the verifier.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/edge/v1/README.md CONCEPTS.md docs/conventions/protobuf.md docs/architecture/2026-08-20-device-service-and-inventory-direction.md`

## Verification

```bash
buf lint
buf generate
go build ./... && go vet ./...
go test -race ./test/conformance/proto/...
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/edge/v1 go.mod CONCEPTS.md docs/conventions/protobuf.md docs/architecture/2026-08-20-device-service-and-inventory-direction.md
```

The test vector for the README: a fixed Ed25519 seed of 32 zero bytes, an
`EdgeAssertion` with a fixed edge id, audience `flowseer-central`, fixed
timestamps, and a fixed nonce. The conformance test computes the header
value, then reads the README and fails when that value is not in it, so the
README cannot drift from the code.

## Definition of done

- Verifier green for every changed path.
- Package README written; inventory README, conventions doc, `CONCEPTS.md`,
  and the direction record updated in the same change.
- The message-sync hook reports no missing triad or ref members.
- `> Implemented.` outcome note added under this plan's title.
- R and U labels appear only in this plan, never in code, comments, or
  commit messages.

## Open questions

- The audience string. The plan uses the central deployment's configured
  value and `flowseer-central` in the test vector; the first host plan picks
  the real one.
- Whether `ListEdges` needs filtering by lifecycle and contact in the first
  cut. The plan gives it only paging.
