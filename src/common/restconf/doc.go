// Package restconf is FlowSeer's RFC 8040 client library: API-root
// discovery, reads and edits in RFC 7951 JSON, ETag-guarded writes
// with read-back, and typed decoding of ietf-restconf:errors
// payloads.
//
// This file is the authoritative statement of the package contract;
// if a README ever disagrees, this file wins.
//
// # Lifecycle
//
// [Dial] discovers the peer's API root (RFC 8040 §3.1: the
// /.well-known/host-meta XRD link, with a /restconf probe fallback)
// and returns a [Session]. RESTCONF is stateless HTTP, so unlike the
// NETCONF session there is no latched terminal error: every request
// fails or succeeds on its own, and reconnect semantics are the HTTP
// client's. Close releases pooled connections and is idempotent. A
// Session is safe for concurrent use.
//
// # Reads
//
// [Session.Get] renders a [yang.Path] as an RFC 8040 data-resource
// URI and returns the response body — RFC 7951 JSON the
// yanggen-generated codecs decode. The depth and fields query
// parameters are sent only when the caller asks for them
// ([GetOptions]); whether the peer honors them is a device property
// (the ICX assumption is verified on lab hardware), and callers fall
// back to client-side pruning when it does not.
//
// # Writes
//
// [Session.Put] and [Session.Patch] follow KTD9: capture the
// resource's ETag with a conditional GET, send If-Match when the peer
// provided one, and re-read the resource afterwards — the returned
// [WriteResult] carries the read-back body so the caller can prove
// the edit by diff rather than trusting the status code (R12).
// Missing ETag support degrades to an unconditional write plus
// read-back. 409 and 412 responses surface as the retryable
// [ErrCodeConflict].
//
// # Errors
//
// Failures carry errs codes in the append-only restconf/ namespace.
// A conformant ietf-restconf:errors body decodes into typed fields
// (tag, app-tag, path, message as attributes); a malformed body is
// preserved raw in the error's attributes for the conformance corpus
// (the expected ICX quirk). Context cancellation surfaces as the
// unwrapped ctx.Err().
package restconf
