// Package gnmi is FlowSeer's gNMI client library: Capabilities, Get,
// Set, and Subscribe over openconfig/gnmi protos and gRPC, with
// negotiated encoding and typed decoding into the shared yang model.
//
// This file is the authoritative statement of the package contract;
// if a README ever disagrees, this file wins.
//
// # Lifecycle
//
// [Dial] establishes the gRPC channel — the TLS posture is explicit
// (CA pin, mTLS, plaintext, or the documented insecure opt-in; there
// is no permissive default) — and issues Capabilities to record the
// peer's models and negotiate the encoding: JSON_IETF preferred,
// PROTO fallback (KTD5). Close is idempotent. A Session is safe for
// concurrent use.
//
// # Reads and streams
//
// [Session.Get] serves small targeted reads (R11 identity).
// [Session.Subscribe] returns a pump-backed [Stream]: ONCE backs
// bounded traversals, STREAM backs the Watcher — the device owns the
// cadence, the sync_response marker surfaces as a [SubscribeEvent]
// with Sync set (the Watcher's cold-start-complete signal), and
// server stream termination latches [Stream.Err]. POLL is deferred.
// Reconnect is the caller's action; a re-created stream cold-starts.
//
// # Values and paths
//
// gNMI updates carry either raw JSON_IETF bytes (decoded by the
// yanggen-generated codecs) or scalar TypedValues mapped onto
// [yang.Value]. [yang.Path] maps segment-for-PathElem onto the gNMI
// Path proto; module qualifiers do not travel (gNMI paths are
// name-based), and an explicit origin can be set per request.
//
// # Errors
//
// Failures carry errs codes in the append-only gnmi/ namespace; gRPC
// status codes and failed paths ride along as attributes. Context
// cancellation surfaces as the unwrapped ctx.Err().
package gnmi
