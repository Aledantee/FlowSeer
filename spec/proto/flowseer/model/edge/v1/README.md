# Edge

`flowseer.model.edge.v1` holds the Edge entity: the enrolled edge process
through which FlowSeer runs integrations inside a site's network, and
everything that names it — its ref pair, its lifecycle, the setup key and
registered key it carries, the assertion it signs on every call, the proof
of possession that registers a key, and the provisioning file that ships
with it before its first boot. The two Connect services around it — the one
an edge calls to enroll and stay attached, and the one an operator calls to
create, provision, and retire edges — live in
[`api/edge/v1`](../../../api/edge/v1/README.md), which imports this package
for the entity and returns `EdgeRecord` from every call that hands back an
edge.

## Two secrets, two lifetimes

The setup key is what ships in the box. An operator creates the edge, gets
the key once inside an `EdgeProvisioning` message, and writes it into the
device before it leaves the warehouse. It may sit there for months, so it
has an expiry the operator chooses (180 days when unset), it can be revoked
at any time, and it is consumed by exactly one enrollment. The key string is
`fse1_` followed by a 26-character identifier and a 52-character secret,
both lowercase base32 without padding, for 128 and 256 random bits. Central
stores the SHA-256 of the whole string and the identifier in clear; the
identifier is safe to log and appears on `SetupKey.id`.

The edge identity is an Ed25519 key pair the edge generates itself before
its first call. `Enroll` registers the public key; the private half never
leaves the device, and central holds nothing that lets it impersonate an
edge. The key is long-lived. `Rekey` replaces it on request. The edge
persists the new pair before the call and keeps the old one until the
response arrives, and a repeated `Rekey` naming the key already registered
succeeds, so a crash between the call and persisting the answer recovers
by calling again with either key. A suspected compromise is handled by
retiring the edge and enrolling it again with a fresh setup key, because a
compromised key cannot be trusted to sign its
own replacement.

Both registering calls carry a `KeyProof`: the serialized `KeyProofPayload`
plus a signature over those exact bytes by the key being registered. At
enrollment the payload is bound to the setup key identifier, at rekey to the
nonce of the assertion on the same call, so a captured proof cannot be
replayed into a different registration.

## The assertion header

Every call except `Enroll` carries a fresh `SignedEdgeAssertion` in the
Authorization header with the `FlowSeer-Edge` scheme:

```
Authorization: FlowSeer-Edge <base64 of SignedEdgeAssertion, standard alphabet, no padding>
```

The verifier checks, in this order, and stops at the first failure:

1. The header decodes to a `SignedEdgeAssertion` that passes validation.
2. The payload is parsed to read the edge ref and nothing else; every
   other claim in it is untrusted input until step 3 passes.
3. The signature verifies over `payload` exactly as received, with the key
   stored on that edge.
4. The parsed `EdgeAssertion` passes its own validation: the 60 second
   window, the nonce length, and the `procedure` and `body_sha256` shape.
5. `procedure` equals the full Connect method name of the RPC being
   invoked, for example `/flowseer.api.edge.v1.EdgeService/Heartbeat`.
6. `body_sha256` equals the SHA-256 of the HTTP request body exactly as
   received. The edge sends requests uncompressed and central refuses a
   compressed one, so the bytes hashed are the bytes on the wire; for a
   server-stream call the body is the one enveloped request message.
7. The edge's lifecycle is enrolled. A retired edge fails here, before any
   handler runs.
8. `audience` equals the central deployment's configured value.
9. `issued_at` is within the configured clock skew of central's clock.
10. `expires_at` is in the future.
11. The nonce has not been seen for this edge within the validity window.

The assertion binds the edge, the audience, the window, the nonce, the
Connect procedure, and the request body. Two calls, or a captured
assertion replayed against a different RPC or a mutated body, therefore
fail at step 5 or 6 even within the assertion's own 60-second window.
Pinned TLS remains the boundary that keeps an assertion from being
captured at all; the binding here is what a captured assertion cannot buy
past its intended call.

There is no access token. Signing costs microseconds and the assertion is
tied to a single call, so nothing bearer-shaped lives on the edge except
the private key itself. Streams are checked when they open.

The worked header below is computed from a fixed vector, and
`test/conformance/proto/model_edge_rules_test.go` fails when this file no
longer carries it: private key from a seed of 32 zero bytes, edge id
`0192e6a0-0000-7000-8000-0000000000ed`, audience `flowseer-central`,
issued at 2026-09-05T12:00:00Z, expiring 30 seconds later, nonce bytes
`00` through `0f`, procedure
`/flowseer.api.edge.v1.EdgeService/Heartbeat`, body_sha256 the SHA-256 of
an empty body, deterministic serialization.

```
Authorization: FlowSeer-Edge Cq0BCigKJgokMDE5MmU2YTAtMDAwMC03MDAwLTgwMDAtMDAwMDAwMDAwMGVkEhBmbG93c2Vlci1jZW50cmFsGgYIwIjw1AYiBgjeiPDUBioQAAECAwQFBgcICQoLDA0ODzIrL2Zsb3dzZWVyLmFwaS5lZGdlLnYxLkVkZ2VTZXJ2aWNlL0hlYXJ0YmVhdDog47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFUSQBNz5MF25z/nq9bRdOT4oJEWA17sTJTatD0nn1TIfYuyFyicifeP7cpA1NCP0JzyZsCjx+MxN4zhY+4dpgi8WwU
```

A second worked header covers a server-stream open with a non-empty body,
so a middleware that hashes the HTTP body bytes can check itself against
it: the same key, edge, audience, window, and nonce, procedure
`/flowseer.api.edge.v1.EdgeService/OpenDeviceSubmission`, and `body_sha256`
over the Connect-enveloped `OpenDeviceSubmissionRequest` for device
`0192e6a0-0000-7000-8000-0000000000d1`, binding
`0192e6a0-0000-7000-8000-0000000000b1`, sequence 42 (a zero flags byte, the
big-endian message length, then the deterministic message bytes). The
vector's body is a deterministic marshal; a middleware checking itself
against it hashes the bytes it received, never a re-marshal of the decoded
message, since a client's encoding need not be deterministic.

```
Authorization: FlowSeer-Edge CrgBCigKJgokMDE5MmU2YTAtMDAwMC03MDAwLTgwMDAtMDAwMDAwMDAwMGVkEhBmbG93c2Vlci1jZW50cmFsGgYIwIjw1AYiBgjeiPDUBioQAAECAwQFBgcICQoLDA0ODzI2L2Zsb3dzZWVyLmFwaS5lZGdlLnYxLkVkZ2VTZXJ2aWNlL09wZW5EZXZpY2VTdWJtaXNzaW9uOiBeiE+EkKO3xyVNss7plVtArK4AMWo9MlZn0egw06XT8BJA7CbFLzvZmR/jh2rq6sc+fB89EXI+K99j9O69oKmbsQc2AwMZpsAPlXngwrtopUzc5itWFa+FDYXe2DB+ZQOZBg
```

Ed25519 is the only algorithm and the verifier has no way to be told
another one. A boxed device may boot with a dead clock, so `Enroll`,
`Rekey`, and `Heartbeat` return `server_time` and the edge uses it for
assertion timestamps until its own clock is synchronized. That is safe only
because the edge pins central's certificate: `EdgeProvisioning` ships one
or more SPKI SHA-256 digests, `EnrollResponse` replaces the set, and an edge
refuses a chain that matches none. Corporate TLS interception on a customer
network would otherwise read every assertion.

The assertion middleware writes its stable error code in the
`FlowSeer-Refusal-Code` response header. This matters for `edge/clock-skew`:
the middleware answers before Connect, so the generated client otherwise sees
only a generic HTTP 401. The edge's pinned transport pairs that code with the
standard HTTP `Date` header, corrects its assertion clock, and retries the
rejected request once. Other refusal codes are not retried there.

## Lifecycle and contact

Lifecycle is what an operator or an enrollment did. Contact is how recently
the edge was heard from, derived from `last_seen_at` against thresholds the
service holds per tenant. The two never share a word, the way device
lifecycle and binding reachability do not.

| From | To | Cause |
| --- | --- | --- |
| none | `PENDING` | `CreateEdge` issues the first setup key |
| `PENDING` | `ENROLLED` | `Enroll` consumes the key and registers the public key |
| `PENDING` | `PENDING` | `IssueSetupKey` replaces an unused key; no event, the setup key record changes |
| `ENROLLED` | `ENROLLED` | `Rekey` replaces the public key; no event, `enrolled_at` changes |
| `ENROLLED` | `RETIRED` | `RetireEdge` |
| `PENDING` | `RETIRED` | `RetireEdge` on an edge that never enrolled |
| `RETIRED` | `PENDING` | `IssueSetupKey` on a retired edge |

Retiring an edge ends its standing and withdraws any outstanding setup key.
It ends no device work: a mutation the edge was carrying stays open on its
lane record, because abandoning live work across a fleet destroys something
no operator asked to lose and hides a device that may carry a half-applied
change. So `RetireEdgeResponse` names what it orphaned — the device and the
sequence for each open mutation — and the operator ends each one through
`DeviceService.AbandonMutation`. `DeviceService.ListEdgeOpenMutations` answers
the same question afterwards, with the phase each mutation stopped at. What
retirement does resolve is the pending hold resolutions those devices owed
the edge: a hold is an instruction to an edge to clear its own, and a retired
edge will never take it.

An enrolled edge that lost its private key cannot rekey, because rekey is
signed by the key it lost. The path is `RetireEdge`, `IssueSetupKey`, a new
provisioning file, and a fresh `Enroll`; the edge keeps its id and history.

A heartbeat also records what the edge is running and whether it is holding
data it could not deliver, so an operator sees a backlog without waiting for
the edge to give up on it. Each heartbeat describes the edge as it is at that
moment: one reporting nothing buffered clears the timestamp rather than
leaving the last one standing.

Contact walks `ACTIVE`, `STALE`, `DORMANT` and back on the next heartbeat.
A dormant edge is still enrolled. Its next call is authenticated exactly
like any other, so an edge that was unplugged for a year reattaches without
operator action. Nothing but `RetireEdge` ends an edge's standing.

`Enroll` is idempotent for one edge: the same setup key with the same public
key returns the same response, so an edge that crashes between the call and
persisting the answer recovers by calling again. The same setup key with a
different public key is refused, and the service records it, because that is
what a stolen key looks like. The edge must therefore persist its key pair
before its first `Enroll`.

## What central holds and what an attacker gets

Central stores public keys and setup key hashes. A central breach yields
the ability to mint setup keys, which is the most privileged operator
action there is; it does not yield any edge's identity.

Minting leaves no trail. The audit stream FlowSeer writes is device-scoped
— it holds what was done to a device — so nothing today records that an
operator created an edge, issued it a setup key, revoked one, or retired
the edge, and someone reconstructing an incident cannot answer those
questions from FlowSeer at all. The trail belongs in an operator-action
scope of its own, with its own retention and access; writing it to the
device stream would put the answer in the wrong place permanently.

A setup key read out of a shipped box lets the reader enroll as that edge
once. The real device then fails its own enrollment visibly, and the
operator retires the edge. A private key read off an edge's disk is that
edge's identity until the operator retires it. Both attacks need physical
access and buy the attacker the right to be managed as one site's edge:
every call is authorized as the edge the assertion names, never as the
tenant, so the blast radius is that edge's own record and whatever the
integrations it hosts are later allowed to reach.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: api/capture, api/edge, model/access, model/inventory,
store/device

Deliberately absent:

- A Connect service. The two services that create, enroll, and operate an
  edge live in `api/edge/v1`; this package holds only the entity every
  boundary that names an edge agrees on, never the calls that act on it.
