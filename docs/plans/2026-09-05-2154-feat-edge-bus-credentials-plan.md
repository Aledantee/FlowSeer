---
title: Edge Bus Attachment and Credential Delivery - Plan
type: feat
date: 2026-09-05
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
amends: none
---

# Edge Bus Attachment and Credential Delivery - Plan

> Implemented. U6's credential provider ended up using
> `golang.org/x/sys/unix.Openat` with `O_NOFOLLOW` rather than
> `os.OpenRoot`/`os.OpenInRoot`: `os.Root` follows a symlink that stays
> inside the root, which is the right rule for a directory tree in
> general but not for a single credential file, where the requirement is
> that the leaf itself is never a symlink. `O_NOFOLLOW` gives that
> refusal atomically at the open call. The trade-off, recorded in the
> package doc comment, is that a Kubernetes secret volume's native layout
> (each key published as a symlink through a rotated `..data` directory)
> needs its own adapter in front of this package rather than being read
> directly.

## Goal

An edge that has enrolled can attach to the NATS bus and receive the two
kinds of device credential decision 9 describes, all over the same
authenticated `EdgeService` Connect channel and never on the bus itself. The
means: three new RPCs on `EdgeService` (`AttachBus`, `AcquireReadCredential`,
`OpenDeviceSubmission`), the assertion extended to bind the Connect procedure
and a request-body hash so a captured assertion cannot be replayed against a
different call, the credential and host-trust handles `device/policy/v1`'s
README already reserves, and a Go verifier plus a mounted-file credential
provider under `src/services/device/internal/`. Stop if a landed Go package
already parses `EdgeAssertion` without the two new fields, because the wire
change then needs a migration this plan does not contain — the research pass
for this plan found no such code.

## Decisions

- The three new RPCs stay on `EdgeService` in `edge_service.proto`; their
  messages live in two new files, `bus.proto` and `credential.proto`,
  matching the existing split where `key_proof.proto` holds `KeyProof` out
  of `edge_service.proto`. Why: one concern per file is the package's
  established pattern.
- `AcquireReadCredentialRequest` and `OpenDeviceSubmissionRequest` name the
  device and the binding with plain UUID strings, not `DeviceGlobalRef` or a
  binding ref message. Why: `api/inventory` imports `api/edge` (decision 12
  of the verified-access record), so `api/edge` importing `api/inventory`'s
  ref types back would cycle the graph. `device/policy/v1` imports nothing,
  so `api/edge` may import it freely for the new handles.
- The credential material itself (`DeviceCredential.material`) is opaque
  `bytes`. Why: SNMPv3 auth/priv keys and an SSH password are shaped
  differently, and no schema for either exists yet; a typed credential body
  is a later plan's job once the SNMP and SSH edge adapters need one.
- `AttachBus`'s subject map is `map<string, string>`, not enumerated typed
  fields. Why: no NATS subject-naming schema exists anywhere in `spec/`
  today (confirmed by search); enumerating a vocabulary here would invent
  the bus wiring plan this task is not scoped to write. The README states
  the map's keys are owned by whatever module builds the leaf node.
- `OpenDeviceSubmission`'s first response delivers `SubmissionGrant` once and
  every later response is an `AuthorityPulse`; the stream never repeats a
  grant. Why: the task requires "delivers the submission credential once and
  then authority pulses," and a repeated grant would double-issue a one-use
  secret. The monotonic-deadline requirement is documented service behavior,
  not a wire-validated invariant, because protovalidate has no cross-message
  state within a stream.
- The assertion's two new fields are `procedure` (the full Connect method
  name, e.g. `/flowseer.api.edge.v1.EdgeService/Heartbeat`) and
  `body_sha256` (32 bytes, SHA-256 of the uncompressed request message
  bytes). Both join the existing nine-step order as two new steps between
  the assertion's own validation and the lifecycle check, because they are
  checked against the call in progress, not against stored state. Why: this
  is exactly the binding the assertion doc's own open sentence names
  ("the first host plan decides whether to bind the method as well") and
  the pattern `docs/solutions/conventions/sign-protobuf-payload-bytes-never-fields.md`
  says the package README owns.
- The worked vector in the README and the pinned conformance test both
  change, because the payload shape changed. The new vector reuses every
  existing fixed input (same key seed, edge id, audience, timestamps,
  nonce) and adds a fixed `procedure` and `body_sha256` chosen for the
  vector's own worked example rather than a real RPC's body. Why: keeping
  every other input fixed isolates the diff to the two new fields for a
  reader comparing old and new vectors.
- The Go verifier and the mounted-file credential provider are new packages,
  `src/services/device/internal/edge` and
  `src/services/device/internal/credential`, under a service directory that
  does not exist yet. Why: `src/services/device` is the control-plane
  device service decision 12 places `api/device` behind, and the README
  already says the verifier "arrives with the first host." No handler for
  the RPCs themselves lands in this plan; only the verifier and the
  credential provider, which a later plan wires into a Connect interceptor
  and a handler. Unconfirmed: which future plan wires them; flagged under
  Open questions.
- The mounted-file credential provider treats the mount root as untrusted
  down to the last path segment: it opens the root directory once and
  resolves every credential key with a true `O_NOFOLLOW` openat relative
  to that held descriptor (`golang.org/x/sys/unix.Openat`, since Go's own
  `os.Root` follows an in-root symlink rather than refusing it) so a
  symlink at that exact path is refused by the kernel call itself, and refuses
  any file whose mode grants group or world access. Why: no existing
  pattern in this repo does mounted-secret reads (confirmed by search), and
  a Kubernetes-style secret mount is exactly the symlink-race shape
  `os.OpenInRoot` exists to close.

## Requirements

1. `AttachBus` returns a NATS account JWT, a user credential, a subject
   map, and at least one cluster URL, and refuses to validate with zero
   cluster URLs. Acceptance: `AttachBusResponse{cluster_urls: []}` fails
   `buf.validate.field.repeated.min_items`.
2. `AcquireReadCredential` requires the device id, the binding id, and the
   pinned `AccessPolicyHandle`, and returns a `DeviceCredential` naming a
   `CredentialHandle`, a `HostTrustHandle`, and an `expires_at` in the
   future relative to issuance. Acceptance: a request missing
   `access_policy` fails required-field validation.
3. `OpenDeviceSubmission` requires `sequence >= 1` and streams
   `SubmissionGrant` exactly once before any `AuthorityPulse`. Acceptance:
   the conformance test constructs a two-message stream (`grant`, `pulse`)
   and a proto unit test rejects a `sequence` of 0.
4. `EdgeAssertion` validates only when `procedure` is a non-empty Connect
   method path and `body_sha256` is exactly 32 bytes. Acceptance: the
   conformance suite's existing `validationCase` table gains cases for a
   missing `procedure` and a 31-byte `body_sha256`.
5. The README's verifier order lists the procedure and body-hash checks as
   two of eleven ordered steps, and the worked vector's base64 header
   matches what `TestEdgeAssertionHeaderVector` computes from the same
   fixed inputs plus the new fields. Acceptance: the test still passes and
   still fails if the README's literal header diverges from what the test
   computes.
6. `device/policy/v1` gains `CredentialHandle` and `HostTrustHandle`, each
   `{key, version}` with the same field rules as `AccessPolicyHandle`.
   Acceptance: `buf lint` passes and a conformance case rejects a `key`
   containing `/`.
7. The Go verifier in `src/services/device/internal/edge` reproduces the
   README's eleven-step order exactly, rejects a signature over tampered
   payload bytes, rejects a nonce replay within the validity window, and
   rejects a procedure or body-hash mismatch. Acceptance: a table test
   with one case per rejected step, each asserting the specific error and
   that no later step ran.
8. The Go credential provider in `src/services/device/internal/credential`
   reads a credential by key from a mounted root, refusing: a key with a
   path separator or `..`, a symlinked credential file, a file replaced
   with a symlink between stat and read, a file mode with any group or
   world bit set, and a credential whose sidecar metadata (version, or
   equivalent) fails to parse after the file opens. Acceptance: one test
   per refusal, using `t.TempDir()` and `os.Symlink` to construct each
   failing fixture; no test touches a real mount or a lab device.
9. Secrets never appear in a committed fixture. Acceptance: `grep` over the
   new test files for the literal test key material used, confined to
   values generated at test run time or clearly synthetic (`ed25519.NewKeyFromSeed`
   over a fixed all-zero seed is documented precedent from the existing
   vector and is not a secret).

## Out of scope

- A handler implementation for any of the three RPCs, or a Connect
  interceptor that calls the new verifier. Those need a running device
  service host, which this plan does not stand up.
- The NATS leaf-node embedding itself, the subject-naming vocabulary the
  bus module will use, and JetStream store-and-forward. Those are the
  edge-agent host's job per the device-service direction record.
- A typed credential body for SNMPv3 or SSH. `DeviceCredential.material`
  stays opaque bytes until an adapter needs a shape.
- Revocation of an in-flight `OpenDeviceSubmission` stream from the
  operator side, beyond the `AuthorityPulse` vocabulary already carrying an
  unauthorized state. The mechanism that decides when to send one is a
  later plan's job.

## Units

### U1. Credential and host-trust handles
Files: `spec/proto/flowseer/device/policy/v1/handle.proto`,
`spec/proto/flowseer/device/policy/v1/README.md`
After: none
Change: `handle.proto` gains `CredentialHandle` and `HostTrustHandle`,
each `{key, version}` with the field rules `AccessPolicyHandle` already
uses. The README's "deliberately absent" note for these two handles is
replaced with a line describing them, since they now exist.
Tests: `test/conformance/proto` gains validation cases for both messages
(good key/version, bad key pattern, version 0).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/device/policy/v1`

### U2. Bus attachment contract
Files: `spec/proto/flowseer/api/edge/v1/bus.proto` (new),
`spec/proto/flowseer/api/edge/v1/edge_service.proto`,
`spec/proto/flowseer/api/edge/v1/README.md`
After: none
Change: `bus.proto` defines `AttachBusRequest` (empty — the assertion
already names the edge) and `AttachBusResponse` (`account_jwt` bytes,
`user_credential` bytes, `subjects` map<string,string>, `cluster_urls`
repeated string, min 1). `edge_service.proto` imports it and adds
`rpc AttachBus(AttachBusRequest) returns (AttachBusResponse);`. The README
gains a "Bus attachment" section describing the response's four parts and
that it is one lifecycle, separate from credential delivery and
revocation.
Tests: conformance cases for zero `cluster_urls`, an empty `subjects` map
(allowed — a fresh edge may attach before any binding exists), and a
present `account_jwt`/`user_credential`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/edge/v1`

### U3. Read credential and submission credential contracts
Files: `spec/proto/flowseer/api/edge/v1/credential.proto` (new),
`spec/proto/flowseer/api/edge/v1/edge_service.proto`,
`spec/proto/flowseer/api/edge/v1/README.md`
After: U1 (imports the new handles)
Change: `credential.proto` defines `DeviceCredential` (`credential`
`CredentialHandle`, `material` bytes min_len 1), `AcquireReadCredentialRequest`
(`device_id`, `binding_id` uuid strings, `access_policy`
`AccessPolicyHandle`), `AcquireReadCredentialResponse` (`credential`
`DeviceCredential`, `host_trust` `HostTrustHandle`, `expires_at`
timestamp), `OpenDeviceSubmissionRequest` (`device_id`, `binding_id` uuid
strings, `sequence` uint64 gte 1), `SubmissionAuthority` enum
(`UNSPECIFIED`, `AUTHORIZED`, `REVOKED`), `SubmissionGrant` (`credential`
`DeviceCredential`, `host_trust` `HostTrustHandle`, `deadline` timestamp),
`AuthorityPulse` (`authority` `SubmissionAuthority`, `deadline` timestamp),
`OpenDeviceSubmissionResponse` (oneof `update { grant | pulse }`,
required). `edge_service.proto` adds
`rpc AcquireReadCredential(...) returns (...)` and
`rpc OpenDeviceSubmission(...) returns (stream OpenDeviceSubmissionResponse);`.
The README gains "Read credential delivery" and "Submission credential
delivery" sections stating the per-operation lifecycle, the one-use grant,
and that revocation is the third, separate lifecycle (an `AuthorityPulse`
carrying `REVOKED`).
Tests: conformance cases for `sequence = 0` rejected, a missing
`access_policy` rejected, an `OpenDeviceSubmissionResponse` with no oneof
set rejected.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/edge/v1`

### U4. Assertion binds procedure and body hash
Files: `spec/proto/flowseer/api/edge/v1/assertion.proto`,
`spec/proto/flowseer/api/edge/v1/README.md`,
`test/conformance/proto/api_edge_rules_test.go`
After: none
Change: `EdgeAssertion` gains `string procedure = 6` (required, min_len 1,
max_len 256, pattern `^/[^/]+/[^/]+$`) and `bytes body_sha256 = 7`
(required, len 32). The README's verifier order becomes eleven steps: the
two new checks (procedure equals the invoked RPC's full method name;
body_sha256 equals SHA-256 of the uncompressed request bytes as received)
sit between the assertion's own validation and the lifecycle check. The
worked vector is regenerated with fixed `procedure` and `body_sha256`
values and both the README's base64 literal and the test's expectations
change together. The README's closing sentence about the method/body not
being bound is replaced with a sentence stating that they now are, and
that pinned TLS is still the boundary against a captured assertion being
replayed within its 60-second window against the same call.
Tests: `TestEdgeAssertionHeaderVector` recomputes and re-pins the new
vector; new `validationCase` entries for empty `procedure`, a `procedure`
without a leading slash, and a 31-byte `body_sha256`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/edge/v1 test/conformance/proto`

### U5. Go edge-assertion verifier
Files: `src/services/device/internal/edge/verifier.go`,
`src/services/device/internal/edge/verifier_test.go`,
`src/services/device/internal/edge/doc.go`
After: U4
Change: a `Verifier` that takes the raw Authorization header value, the
invoked procedure's full method name, and the uncompressed request bytes,
and returns the parsed `EdgeAssertion` or an `errs` error identifying which
of the eleven steps failed. It holds an injected key lookup
(`func(edgeID string) (ed25519.PublicKey, EdgeLifecycle, error)`) and a
nonce-replay cache scoped to the validity window, so the package carries no
storage dependency. Error codes are declared with `errs.NewCode` per
rejected reason (bad header, bad signature, key-lookup failure, malformed
assertion, wrong procedure, wrong body hash, retired edge, wrong audience,
clock skew, expired, replayed nonce), never one generic code, so a caller
can tell a stale clock from a replay or a transient key-store outage from
an authentication failure; a key-lookup failure is additionally marked
`Retryable()`.
Tests: one table-test case per rejected reason in `verifier_test.go`, each
built by mutating exactly one field of an otherwise-valid signed assertion
(or, for the signature case, tampering the signed payload bytes directly)
and asserting the specific `errs.Code`; one case combines two invalid
fields to prove the earlier step's code wins.
(a later, also-invalid field in the same case is not checked). A
happy-path case using the README's own worked vector confirms the verifier
accepts what the README claims is valid.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/edge`

### U6. Go mounted-file credential provider
Files: `src/services/device/internal/credential/provider.go`,
`src/services/device/internal/credential/provider_test.go`,
`src/services/device/internal/credential/doc.go`
After: none
Change: a `Provider` over a root directory, opened once with `os.Open` and
held for the Provider's lifetime. `Get(key string, wantVersion uint64)`
validates `key` against the same one-path-segment pattern `device/policy/v1`
uses for a handle's `key` field, opens `key` and `key.meta.json` relative
to the held root descriptor with `unix.Openat(..., O_NOFOLLOW)` (a symlink
at that exact path fails the open call itself with `ELOOP`, never followed),
reads each open file's `os.FileInfo` and refuses `Mode()&0077 != 0` (any
group or world bit), refuses a `.meta.json` that fails to parse or whose
declared version does not match the handle passed by the caller, and
re-resolves the material's path once more after reading the metadata,
refusing if it no longer names the same file, so a rotation landing
between the two independent opens cannot pair stale material with the new
metadata's version.
Tests: valid read; key with `/` refused before any open; key `..` refused;
symlinked credential file refused; a race case that replaces the file with
a symlink after the provider's `Get` starts (using a slow-open hook or a
goroutine racing the read, tolerant of scheduling — asserts the read never
follows the swapped-in target's content by using a canary payload only the
original file has, not on precise timing); a deterministic rotation-between-
opens case using an injected test hook; mode 0640 refused; mode 0600
accepted; unparsable metadata refused; version mismatch refused. No test
opens a real mount or a lab path.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/credential`

## Verification

- `buf lint` and `buf generate` from the repository root.
- `go test -race ./...` for the affected Go packages, plus the full
  `test/conformance/proto` suite.
- `.claude/skills/verify-change/scripts/verify-change.sh -- <every path
  touched>` before the final commit.
- Manual: none; no lab device is touched by this plan.

## Definition of done

- Verifier green for every changed path.
- `spec/proto/flowseer/device/policy/v1/README.md` and
  `spec/proto/flowseer/api/edge/v1/README.md` describe the new shapes and
  the three separate lifecycles (bus handoff, credential delivery,
  revocation).
- This plan's `status` set to `implemented` with an outcome note under its
  title.
- No plan labels in code, comments, or commit messages.

## Open questions

- Which future plan wires `src/services/device/internal/edge` and
  `internal/credential` into an actual Connect handler and interceptor.
  Unconfirmed; taking the recommendation that this is out of scope here
  because no device-service host exists yet to host them.
- Whether `AttachBusResponse.subjects` should instead be a small typed
  message once the bus module's subject vocabulary is designed.
  Unconfirmed; taking the recommendation to keep it a map now and revisit
  when that module's plan exists, per the "Deliberately absent" pattern
  used elsewhere in these packages.
