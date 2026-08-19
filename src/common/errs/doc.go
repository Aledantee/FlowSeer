// Package errs is FlowSeer's error package.
//
// # Identity
//
// What this package is: a small, owned error type built for a distributed
// system. Its core payload is message, [Code], attributes, and causes; a
// captured stack rides along as diagnostics, never as payload. It is
// stdlib-native: errors implement Unwrap, compose with [errors.Is],
// [errors.As] and [errors.Join], and there is no parallel matching API to
// learn.
//
// Four further fields ride along because a mechanism consumes them, not
// because a reader might like them: the client-facing user message and hint
// a boundary sends in place of the internal text, the exit code a process
// ends with, and the retry disposition a poll loop or a broker reads. That
// is the bar a field has to clear here — [Code] clears it for [errors.Is],
// client-safe attributes for the boundary filter, these four for the
// process, the retry loop, and the client.
//
// What this package is not: a presentation layer, and not a bag of
// metadata. There are no printers, tags, timestamps, or trace IDs — the log
// handler renders, and OpenTelemetry propagates trace identity through
// [context.Context], where it cannot go stale against a copy on the error.
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
// Readers extract with [Attributes], [SafeAttributes], [CodeOf],
// [UserMessage], [Hint], [ExitCode], and [Retryable].
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
// # User messages and hints
//
// The message an [Error] carries names hosts, engine IDs, and call paths: it
// is written for a log an operator reads, and it never reaches an untrusted
// caller. [Builder.UserMsg] sets what that caller is told instead, and
// [Builder.Hint] sets the remedy that goes with it — what happened, then
// what to do about it:
//
//	return errs.From(err).
//	    Code(ErrCodePrivDecrypt).
//	    UserMsg("could not read the device").
//	    Hint("check the device's USM credentials").
//	    Msg("privacy decryption failed")
//
// Both are client-safe by definition, both resolve outermost-first through
// [UserMessage] and [Hint], and both are empty when the chain sets neither —
// a boundary that finds nothing falls back to a generic string rather than
// leaking the internal message.
//
// Writing them is a judgment, not a formality: the level closest to the
// caller knows what that caller was trying to do, which is why the outermost
// wins.
//
// # Exit codes and retries
//
// [Builder.ExitCode] sets the status a process ends with. [ExitCode]
// resolves it outermost-first and falls back to 1 for any error that sets
// none, so a main function can exit on an error without asking whether one
// was set. It returns 0 only for a nil error. The value is process-local: a
// peer's exit status says nothing about this process, so it never travels
// over the wire.
//
// [Builder.Retryable] and [Builder.Fatal] express whether the failure is
// worth trying again, and [Retryable] resolves the outermost error that
// expressed a view. That lets a wrapper which has spent its retry budget
// mark itself fatal over a transient cause. A chain where nothing expressed
// a view is not retryable — retrying is the claim that needs making, and a
// caller that retries a permanent failure loops forever.
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
// attributes, the user message, the hint, the retry disposition, and the
// cause chain. An optional stack field is populated only on trusted internal
// transit (service to service, broker) and is always absent on a message
// headed toward a client. The exit code is not carried at all — it describes
// the process that failed, not the failure.
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
// clients exposes only the code, the client-safe attributes, and the user
// message and hint — the sanitized text an author already wrote for exactly
// this, falling back to a generic string when the chain carries none.
//
// The retry disposition crosses too, because it is what maps an error onto a
// retryable RPC status; like every peer-supplied field it is a hint about
// the peer, never an input to a local authorization decision.
//
// Decoded errors are accepted only from authenticated, integrity-protected
// peers. A peer-supplied code or attribute is diagnostic input and never
// drives an authorization decision.
package errs
