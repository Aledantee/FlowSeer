// Package netconf is FlowSeer's NETCONF client library: a session
// over an RFC 6241/6242 transport with capability-aware datastore
// handling, candidate/lock/validate/commit/rollback orchestration,
// and subtree-filtered reads returning payloads the yanggen-generated
// codecs decode.
//
// This file is the authoritative statement of the package contract;
// if a README ever disagrees, this file wins.
//
// # Transport
//
// The wire envelope is nemith.io/netconf behind the [Transport] seam.
// [Dial] builds the production SSH transport; [NewSession]
// accepts any Transport, which is how tests script a fake and how a
// house transport could replace the dependency without touching
// callers.
//
// # Session lifecycle
//
// A [Session] is Dialing → Ready → Closed. Establishment parses the
// server hello and records the capability set; every RPC afterwards
// runs under the caller's context plus the configured RPCTimeout. A
// background keepalive (when enabled) probes the peer between RPCs so
// a dead transport latches an error via [Session.Err] instead of
// blocking the next caller indefinitely. Close is idempotent and
// safe to call concurrently with in-flight RPCs. RPCs issued after
// Close return [ErrSessionClosed] once local validation passes;
// capability accessors remain usable. A Session is safe for
// concurrent use; the transport serializes outbound messages.
//
// # Datastores and the edit flow
//
// Datastore writability is capability-driven per session: a
// candidate-mode peer (IOS-XE) edits the candidate datastore under
// lock; a writable-running peer edits running directly; a peer with
// neither surfaces [ErrCodeUnsupported]. [Session.Apply] runs the
// full cycle: lock, edit-config, validate (when the peer supports
// it), commit, unlock. After a locked edit fails, it attempts cleanup
// with independent RPC deadlines, even if the caller canceled. A
// failed or timed-out commit can leave its outcome unknown; see
// [Session.Apply] for cleanup limits. Lock contention maps
// to the retryable [ErrCodeLockDenied].
//
// # Errors
//
// Failures carry errs codes in the append-only netconf/ namespace.
// Device rpc-errors keep tag, severity, path, and message as
// attributes on [ErrCodeRPC]-coded errors. Context cancellation
// surfaces as the unwrapped ctx.Err().
package netconf
