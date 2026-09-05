# Edge

The `flowseer.api.edge.v1` package holds the Edge entity and the two Connect
services around it: the one an edge process calls to enroll and stay
attached, and the one an operator calls to create, provision, and retire
edges. It is the first Connect service package in the repository; the
[protobuf conventions](../../../../../../docs/conventions/protobuf.md) and
the [device-service direction record](../../../../../../docs/architecture/2026-08-20-device-service-and-inventory-direction.md)
say where it sits.

An edge is the process, not an integration. The local-network integration
and any on-prem controller adapter a site needs run on the edge and point
at it; the edge's own concerns are identity, liveness, and provisioning.
Nothing here carries device credentials, bus credentials, or the execute
and event contracts. Those land in their own packages once a host exists.

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
   window and the nonce length.
5. The edge's lifecycle is enrolled. A retired edge fails here, before any
   handler runs.
6. `audience` equals the central deployment's configured value.
7. `issued_at` is within the configured clock skew of central's clock.
8. `expires_at` is in the future.
9. The nonce has not been seen for this edge within the validity window.

The assertion binds the edge, the audience, the window, and the nonce. It
does not bind the RPC method or the request body, so within the window a
captured assertion could be replayed against a different RPC. Pinned TLS
is the boundary that keeps assertions from being captured; the first host
plan decides whether to bind the method as well.

There is no access token. Signing costs microseconds and the assertion is
tied to a single call, so nothing bearer-shaped lives on the edge except
the private key itself. Streams are checked when they open.

The worked header below is computed from a fixed vector, and
`test/conformance/proto/api_edge_rules_test.go` fails when this file no
longer carries it: private key from a seed of 32 zero bytes, edge id
`0192e6a0-0000-7000-8000-0000000000ed`, audience `flowseer-central`,
issued at 2026-09-05T12:00:00Z, expiring 30 seconds later, nonce bytes
`00` through `0f`, deterministic serialization.

```
Authorization: FlowSeer-Edge Cl4KKAomCiQwMTkyZTZhMC0wMDAwLTcwMDAtODAwMC0wMDAwMDAwMDAwZWQSEGZsb3dzZWVyLWNlbnRyYWwaBgjAiPDUBiIGCN6I8NQGKhAAAQIDBAUGBwgJCgsMDQ4PEkDSPlwXrZxLQqB1dM30swJ5bEuiUaV7Opureng8HcHVr81DzJPJLy+6yX275eUL9DALuNzGadQUN/vyXVrm/OwA
```

Ed25519 is the only algorithm and the verifier has no way to be told
another one. A boxed device may boot with a dead clock, so `Enroll`,
`Rekey`, and `Heartbeat` return `server_time` and the edge uses it for
assertion timestamps until its own clock is synchronized. That is safe only
because the edge pins central's certificate: `EdgeProvisioning` ships one
or more SPKI SHA-256 digests, `EnrollResponse` replaces the set, and an edge
refuses a chain that matches none. Corporate TLS interception on a customer
network would otherwise read every assertion.

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

An enrolled edge that lost its private key cannot rekey, because rekey is
signed by the key it lost. The path is `RetireEdge`, `IssueSetupKey`, a new
provisioning file, and a fresh `Enroll`; the edge keeps its id and history.

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
the ability to mint setup keys, which is already the most privileged
operator action and is audited; it does not yield any edge's identity.

A setup key read out of a shipped box lets the reader enroll as that edge
once. The real device then fails its own enrollment visibly, and the
operator retires the edge. A private key read off an edge's disk is that
edge's identity until the operator retires it. Both attacks need physical
access and buy the attacker the right to be managed as one site's edge:
every call is authorized as the edge the assertion names, never as the
tenant, so the blast radius is that edge's own record and whatever the
integrations it hosts are later allowed to reach.

## Deliberately absent

The Edge does not join `EntityType` in `api/inventory/v1` yet. Admission
obliges a cascading delete of attribute values and an existence check that
need the edge store, which arrives with the first host. Until then nothing
may reference an edge through an `EntityRef`; the conventions doc records
the exception next to the tenant one.

Bus credential issuance is not here. When the integration fabric lands, an
authenticated RPC on `EdgeService` hands the edge its account, user
credential, and subject map; the assertion is what authorizes that call,
and nothing in this package changes for it.
