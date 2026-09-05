---
title: A Signature Over a Protobuf Message Covers Carried Payload Bytes, Never Re-Serialized Fields
date: 2026-09-05
last_verified: 2026-09-05
category: conventions
module: spec/proto/flowseer/api/edge/v1
problem_type: convention
component: data_model
severity: high
applies_when:
  - "adding a signature, MAC, or proof of possession over a protobuf message anywhere in FlowSeer (an assertion, a credential, an envelope, a receipt)"
  - "writing or reviewing a verifier that must look up a key by an identifier carried inside the signed message"
  - "a schema comment or README says a signature is 'over the serialized <field> and <field>' or that a payload is 'parsed only after the signature verifies'"
related_components: [api_layer, documentation, testing_framework]
tags: [protobuf, signatures, ed25519, canonicalization, edge, verifier, schema-comments]
---

# A Signature Over a Protobuf Message Covers Carried Payload Bytes, Never Re-Serialized Fields

## The situation

The edge attachment contracts needed two signed things: the per-call
assertion an edge sends central, and the proof of possession an edge gives
when it registers a key. The first draft of the rekey proof was specified
as a signature "over the serialized `edge` and `new_public_key` fields", and
the README's verifier order said the payload is "parsed only after the
signature verifies". Both read naturally and both are wrong in ways that
only show up when someone implements the verifier.

## What is true, and why

Protobuf serialization is not canonical. Field order, unknown fields, map
order, and packed encodings can all differ between producers, and Go only
promises stable bytes within one binary:

```go
// google.golang.org/protobuf@v1.36.9/proto/encode.go:34-40
// Deterministic controls whether the same message will always be
// serialized to the same bytes within the same binary.
//
// Setting this option guarantees that repeated serialization of
// the same message will return the same bytes, and that different
// processes of the same binary (which may be executing on different
// machines) will serialize equal messages to the same bytes.
```

A verifier that rebuilds "the serialized fields" from a parsed message and
checks a signature over that reconstruction rejects valid signatures from
any producer whose encoder differs, including a future version of our own.
The only bytes the verifier can trust to match what was signed are the
bytes it received. So the signed thing is carried as an opaque `payload`
and the signature sits beside it:

```protobuf
// spec/proto/flowseer/api/edge/v1/assertion.proto:47-61
message SignedEdgeAssertion {
  // The serialized EdgeAssertion, signed as these exact bytes. Must be
  // present.
  bytes payload = 1 [ ... ];
  // Ed25519 signature over payload by the edge's registered key, 64 bytes.
  // Must be present.
  bytes signature = 2 [ ... ];
}
```

The second trap follows from the first. When the key the verifier needs is
chosen by an identifier inside the payload (the edge id here), the payload
must be parsed before the signature can be checked. "Parse only after
verifying" is therefore impossible as stated. The rule that is actually
achievable: parse the payload to read the key selector and nothing else,
look up the key, verify over the raw payload bytes, and only then read and
trust the remaining claims (audience, timestamps, nonce). The parse before
verification is untrusted input handling, so it must not allocate or branch
on anything but the selector.

## How to apply it

- Model every signed message as `<Thing>` plus `Signed<Thing>` (or
  `<Thing>Proof`) with `bytes payload` and `bytes signature`, and say in
  the payload comment that it is signed "as these exact bytes".
  `key_proof.proto` is the second instance of the shape.
- Never write "signature over the serialized X and Y fields" in a schema
  comment, a README, or a plan. Name the payload message instead.
- Document the verifier's order as: decode envelope, parse payload for the
  key selector, look up key, verify signature over `payload`, then validate
  and trust the parsed claims.
- Producers may serialize with `proto.MarshalOptions{Deterministic: true}`
  for stable test vectors, but the verifier must never depend on that.
- Pin the shape with a test that signs the payload bytes and verifies over
  `GetPayload()` as carried, not over a re-marshal:

```go
// test/conformance/proto/api_edge_rules_test.go:316-333
payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(edgeAssertion(30 * time.Second))
...
signed := edgev1.SignedEdgeAssertion_builder{
    Payload:   payload,
    Signature: ed25519.Sign(private, payload),
}.Build()
...
if !ed25519.Verify(private.Public().(ed25519.PublicKey), signed.GetPayload(), signed.GetSignature()) {
```

## Evidence

- `spec/proto/flowseer/api/edge/v1/assertion.proto:47-61` and
  `spec/proto/flowseer/api/edge/v1/key_proof.proto:31-45` carry the
  payload-plus-signature shape.
- `test/conformance/proto/api_edge_rules_test.go:316-333` signs and
  verifies over the carried bytes.
- The independent review of plan
  `docs/plans/2026-09-05-1715-feat-edge-attachment-contracts-plan.md`
  found the "over the serialized fields" wording in the rekey proof, and the
  review of the implementation found the "parsed only after the signature
  verifies" order in the package README; both were the same mistake in two
  places, on 2026-09-05.

## What it does not cover

- Which claims a signed message must bind (audience, method, nonce, time
  window) is a threat-model question the package README owns.
- Signing schemes other than Ed25519, and key storage on the signer.
- Canonical serialization schemes that would make field-level signatures
  sound; FlowSeer does not use one and this solution says not to start.
