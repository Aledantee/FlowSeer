// Package edge verifies the SignedEdgeAssertion every authenticated
// EdgeService call carries, in the eleven-step order
// spec/proto/flowseer/model/edge/v1/README.md pins: decode and validate the
// envelope, read the edge ref from the payload, verify the signature over
// the raw payload bytes with that edge's stored key, validate the parsed
// assertion's own shape, check the invoked Connect procedure and the
// request body hash, and only then the edge's lifecycle, the audience, the
// clock window, and the nonce.
//
// The package holds no storage of its own: a [KeyLookup] supplies the
// caller's registered public key and lifecycle, and nonce replay is
// tracked in memory, scoped to each assertion's own validity window.
package edge
