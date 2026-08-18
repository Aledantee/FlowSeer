// Package errs is FlowSeer's error package.
//
// # Identity
//
// What this package is: a small, owned error type built for a distributed
// system. Its semantic payload is four fields — message, [Code], attributes,
// causes — and nothing else; a captured stack rides along as diagnostics,
// never as payload. It is stdlib-native: errors implement Unwrap, compose
// with [errors.Is], [errors.As] and [errors.Join], and there is no parallel
// matching API to learn.
//
// What this package is not: a presentation layer. There are no user-facing
// messages, hints, exit codes, tags, timestamps, trace IDs, or printers.
// Rendering belongs to the log handler and to the RPC boundary.
//
// # Surface
//
// Sentinels are package-level values built with [Msg] or [Msgf]:
//
//	var ErrException = errs.Msg("VarBind is an SNMPv2 exception variant")
//
// Context is added with the error-first [Wrap] and [Wrapf], which return nil
// for a nil error so a call site may wrap unconditionally:
//
//	return errs.Wrapf(err, "dial %s", target)
//
// Anything richer goes through the builder — [New] for a fresh error,
// [From] for one that wraps an existing error — terminating in
// [Builder.Msg] or [Builder.Msgf]:
//
//	return errs.From(err).
//	    Code(ErrCodePrivDecrypt).
//	    Attr("engine_id", id).
//	    PubAttr("proto", "usm-aes").
//	    Msg("privacy decryption failed")
//
// Readers extract with [Attributes], [SafeAttributes], and [CodeOf].
//
// # Codes
//
// A [Code] is an error's identity across a process boundary. Two errors
// carrying the same code match under [errors.Is] even when they share no
// pointer identity, which is what lets a peer's decoded error match the
// local sentinel it stands for — both ends compile the same codes, so no
// type registry is needed.
//
// That makes codes a wire contract, governed by one discipline: they are
// append-only, never renamed, and never reused for a different meaning.
// Declare them at package level with [NewCode], named "<package>/<name>"
// with lowercase segments:
//
//	var ErrCodePrivDecrypt = errs.NewCode("snmp/priv-decrypt")
//
// [NewCode] panics on a malformed or already-registered name, so a
// collision fails the first run of any binary linking both declarations.
// Because that registry only sees linked packages, the repo-wide gate is a
// source scan in this package's tests, which reads every NewCode string
// literal in non-test code and asserts global uniqueness and format —
// declare codes with a literal argument, or the gate cannot check them.
//
// # Attributes and safety
//
// Attributes are flat key-value pairs, appended per error and merged only
// on extraction. The traversal is fixed: outermost error first, joined
// branches left to right, and the first value seen for a key wins, so a
// wrapper's attribute overrides the same key on its cause. [Attributes]
// and [Error.LogValue] share that traversal and therefore cannot disagree.
// Errors from other packages are traversed through but contribute nothing.
//
// Attributes are internal by default. [Builder.PubAttr] marks one
// client-safe, and [SafeAttributes] returns exactly that subset — safety is
// decided where the value is attached, not guessed at the boundary. The
// subset is strict: a key the traversal awards to an internal attribute is
// absent from [SafeAttributes] rather than falling through to a safe value
// deeper in the chain, so the two extractors never disagree about a key.
//
// Raw secret material never becomes an attribute and never reaches a
// message: no keys, salts, passwords, or derived key bytes. Attach the
// length and the protocol name instead — "key_len", 16 and "proto",
// "usm-aes" say everything a responder needs.
//
// # Stacks
//
// A stack is captured once, at origin: [Builder.Msg], [Builder.Msgf],
// [Wrap], and [Wrapf] record program counters when no cause already carries
// a stack. Sentinels built with [Msg] capture nothing, because a stack
// recorded at package init names the declaration, not the failure. A tree
// joined from independently created origins legitimately holds one stack
// per origin.
//
// Capture stores program counters only; symbolization happens when the
// error is rendered into a log record, which is the sole surface stacks
// reach today. Stacks are never part of the semantic payload and never
// travel toward a client.
//
// # Wire design
//
// Errors will cross process boundaries — Connect RPC, brokers — and the
// core API above already carries what that needs. The transport itself is
// deliberately not implemented here; it lands with its first consumer. The
// design it must follow:
//
// The proto message carries the code, the message, the client-safe
// attributes, and the cause chain. An optional stack field is populated
// only on trusted internal transit (service to service, broker) and is
// always absent on a message headed toward a client.
//
// Decoding is total. A cause whose type the decoder does not recognize
// degrades to a generic opaque leaf that preserves its message, code, and
// safe attributes rather than being dropped — the chain's shape survives
// even when its types do not. A code the local binary does not know
// surfaces as an internal error, never as a silent unknown that some
// [errors.Is] might match by accident.
//
// Message text and the cause chain are trusted-internal content: they name
// internal hosts, engine IDs, and call paths. A boundary facing untrusted
// clients exposes only the code, the client-safe attributes, and a
// sanitized or generic message.
//
// Decoded errors are accepted only from authenticated, integrity-protected
// peers. A peer-supplied code or attribute is diagnostic input and never
// drives an authorization decision.
package errs
