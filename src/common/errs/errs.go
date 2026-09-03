package errs

import (
	"fmt"
	"strings"
)

// Error is the concrete error type this package produces. Its semantic
// payload is a message, an optional [Code], flat attributes, and causes,
// plus four fields a mechanism consumes rather than a reader: the
// client-facing user message and hint, a process exit code, and a retry
// disposition. The captured stack is diagnostic only and never payload.
//
// An Error's own fields are immutable once built. Concurrent use requires
// its causes and attribute values to be safe for concurrent reads; they are
// retained by reference. Callers must not modify the slice [Error.Unwrap]
// returns. Build one with [New], [From], [Msg], [Msgf], [Wrap], or [Wrapf].
// The zero value renders as an empty message and carries nothing.
type Error struct {
	msg      string
	code     Code
	attrs    []attr
	causes   []error
	userMsg  string
	hint     string
	exitCode int
	retry    retry
	stack    stack
}

// Msg returns a new error carrying msg and nothing else. It is the
// constructor for package-level sentinels: unlike the builder terminals it
// records no stack, because a stack captured at package init describes the
// declaration site rather than the failure.
func Msg(msg string) error {
	return &Error{msg: msg}
}

// Msgf returns a sentinel error whose message is format rendered with args.
// Like [Msg] it records no stack.
func Msgf(format string, args ...any) error {
	return &Error{msg: fmt.Sprintf(format, args...)}
}

// Error renders the error's own message followed by its causes: a single
// cause is appended after ": ", several are appended as ": [first; second]".
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}

	if len(e.causes) == 0 {
		return e.msg
	}

	var b strings.Builder
	b.WriteString(e.msg)
	b.WriteString(": ")

	if len(e.causes) == 1 {
		b.WriteString(e.causes[0].Error())
		return b.String()
	}

	b.WriteByte('[')
	for i, cause := range e.causes {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(cause.Error())
	}
	b.WriteByte(']')

	return b.String()
}

// Unwrap returns the error's causes so [errors.Is] and [errors.As] can walk
// them. The result is nil when the error has no causes.
//
// The returned slice aliases the error's own storage and must not be
// modified — the same contract the standard library's [errors.Join] result
// carries. Unwrap is called once per node on every [errors.Is] and
// [errors.As] walk, so copying here would put an allocation on the hottest
// path this package has; the constraint buys that back.
func (e *Error) Unwrap() []error {
	if e == nil || len(e.causes) == 0 {
		return nil
	}

	return e.causes
}

// Is reports whether target is an [Error] carrying the same nonempty
// [Code]. Code equality is the cross-boundary identity rule (see [NewCode]):
// an error reconstructed from the wire shares no pointer identity with the
// sentinel it stands for, but it does share its code. Errors without a code
// match by identity through [errors.Is], which checks identity before calling Is.
func (e *Error) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}

	if e.code == "" {
		return false
	}

	//goland:noinspection GoTypeAssertionOnErrors
	t, ok := target.(*Error)

	return ok && t != nil && t.code == e.code
}
