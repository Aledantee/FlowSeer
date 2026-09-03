package errs

import "fmt"

// Builder accumulates an error's payload. Every method returns a new
// Builder, so a partially built value can be reused as the base of several
// errors. The chain terminates in [Builder.Msg] or [Builder.Msgf], which
// produce the error itself.
// The zero value is ready to use. A shared Builder supports concurrent method
// calls when its causes and attribute values are safe for concurrent reads.
type Builder Error

// New returns an empty Builder.
func New() Builder {
	return Builder{}
}

// From returns a Builder seeded with err as its cause, so the built error
// wraps err and matches it under [errors.Is]. A nil err yields an empty
// Builder.
func From(err error) Builder {
	if err == nil {
		return Builder{}
	}

	return Builder{causes: []error{err}}
}

// Attr attaches an internal key-value pair. Internal attributes reach logs
// and trusted peers but never a client-facing boundary; use
// [Builder.PubAttr] for values a client may see. Never attach raw secret
// material — attach a length or a protocol name instead.
// Values are retained without copying; see [Attributes].
func (b Builder) Attr(key string, val any) Builder {
	return b.appendAttr(attr{key: key, val: val})
}

// PubAttr attaches a client-safe key-value pair: one a boundary may expose
// to an untrusted caller. It appears in both [Attributes] and
// [SafeAttributes].
// Values are retained without copying; see [Attributes].
func (b Builder) PubAttr(key string, val any) Builder {
	return b.appendAttr(attr{key: key, val: val, safe: true})
}

// Cause attaches causes, which join the built error's [errors.Is] chain.
// Nil causes are dropped, so a call with no non-nil argument is a no-op.
func (b Builder) Cause(causes ...error) Builder {
	for _, cause := range causes {
		if cause == nil {
			continue
		}

		b.causes = append(clip(b.causes), cause)
	}

	return b
}

// Code sets the error's code, its identity across process boundaries.
// See [NewCode].
func (b Builder) Code(c Code) Builder {
	b.code = c
	return b
}

// UserMsg sets the client-facing message: what an end user or an untrusted
// caller is told, where [Builder.Msg] says what actually failed. It is
// client-safe by definition and names no internal host, engine ID, or call
// path — a boundary sends it in place of the internal message, so write it
// for someone who cannot see the log.
//
// The outermost user message in a chain wins; see [UserMessage].
func (b Builder) UserMsg(msg string) Builder {
	b.userMsg = msg
	return b
}

// Hint sets the remedy that accompanies [Builder.UserMsg]: what to do about
// the failure, where the user message says what happened. It is client-safe
// under the same rule and resolves outermost-first; see [Hint].
func (b Builder) Hint(hint string) Builder {
	b.hint = hint
	return b
}

// ExitCode sets the status a process should exit with when this error ends
// it. Values must be positive — zero and negative are ignored, since zero
// means "unset" and would report success. See [ExitCode] for resolution.
func (b Builder) ExitCode(code int) Builder {
	if code > 0 {
		b.exitCode = code
	}

	return b
}

// Retryable marks the failure as transient: a poll loop, a broker redelivery,
// or an RPC boundary may try the operation again. See [Retryable].
func (b Builder) Retryable() Builder {
	b.retry = retryYes
	return b
}

// Fatal marks the failure as permanent, so a wrapper can overrule a
// transient cause — a retryable dial failure that has exhausted its budget
// becomes fatal at the level that gave up. See [Retryable].
func (b Builder) Fatal() Builder {
	b.retry = retryNo
	return b
}

// Msg sets the message and returns the finished error. It captures an
// origin stack unless a cause already carries one.
func (b Builder) Msg(msg string) error {
	return b.build(msg)
}

// Msgf sets the message to format rendered with args and returns the
// finished error, capturing a stack under the same rule as [Builder.Msg].
func (b Builder) Msgf(format string, args ...any) error {
	return b.build(fmt.Sprintf(format, args...))
}

// build finishes the error. Its callers must sit exactly one frame below the
// call site the captured stack should name (see [capture]).
func (b Builder) build(msg string) error {
	b.msg = msg

	if !anyStack(b.causes) {
		b.stack = capture()
	}

	e := Error(b)

	return &e
}

func (b Builder) appendAttr(a attr) Builder {
	b.attrs = append(clip(b.attrs), a)
	return b
}

// clip caps s so the next append copies instead of writing into a backing
// array a sibling Builder may share.
func clip[T any](s []T) []T {
	return s[:len(s):len(s)]
}
