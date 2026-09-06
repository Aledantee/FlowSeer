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
// Attribute values and causes are retained by reference. Concurrent use of
// an error requires those values and causes to support concurrent reads;
// extraction returns a new map without copying its values.
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
// "usm-aes" say everything a responder needs. Material that has to be
// attached at all is attached as a secret.Value, which renders redacted
// through this package's log value — the value itself, never the
// configuration struct that holds it.
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
package errs
