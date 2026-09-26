package errs

import (
	"fmt"
	"log/slog"
)

// wrapError is the unexported wrapper returned by [Wrap] and [Wrapf]. It holds
// context, the wrapped cause, and an optional origin stack, with Error() and
// Unwrap(). It defines no As or Is method so [errors.As] falls through to the
// innermost [*Error] while [errors.Is] walks the cause chain.
type wrapError struct {
	msg   string
	err   error
	stack stack
}

func (w *wrapError) Error() string {
	if w == nil {
		return "<nil>"
	}
	if w.err == nil {
		return w.msg
	}

	return w.msg + ": " + w.err.Error()
}

func (w *wrapError) Unwrap() error {
	if w == nil {
		return nil
	}

	return w.err
}

// LogValue implements [slog.LogValuer] so a wrapped error renders the full
// structured error tree when logged.
func (w *wrapError) LogValue() slog.Value {
	if w == nil {
		return slog.Value{}
	}

	attrs := []slog.Attr{slog.String(msgKey, w.Error())}

	if code, ok := CodeOf(w); ok {
		attrs = append(attrs, slog.String(codeKey, code.String()))
	}

	attrs = append(attrs, dispositionAttrs(w)...)

	if merged := mergedLogAttrs(w); len(merged) > 0 {
		attrs = append(attrs, slog.GroupAttrs(attrsKey, merged...))
	}

	if captured := stacks(w); len(captured) > 0 {
		attrs = append(attrs, stackAttr(captured))
	}

	return slog.GroupValue(attrs...)
}

// wrap finishes the wrapper. Its callers must sit exactly one frame below the
// call site the captured stack should name (see [capture]).
func wrap(err error, msg string) error {
	var s stack
	if !anyStack([]error{err}) {
		s = capture()
	}

	return &wrapError{
		msg:   msg,
		err:   err,
		stack: s,
	}
}

// Wrap returns an error that adds msg as context to err, keeping err in the
// [errors.Is] chain. It returns nil when err is nil, so a call site may wrap
// unconditionally.
//
// The returned value is an unexported wrapper type holding the message, the
// cause, and an origin stack when err carries none. It does not return [*Error]
// and defines no As method, so [errors.As] reaches the innermost [*Error] in
// the cause chain.
//
// The wrapped error comes first, matching [fmt.Errorf] reading order.
func Wrap(err error, msg string) error {
	if err == nil {
		return nil
	}

	return wrap(err, msg)
}

// Wrapf is [Wrap] with a formatted message: the wrapped error first, then
// the format string and its arguments.
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}

	return wrap(err, fmt.Sprintf(format, args...))
}
