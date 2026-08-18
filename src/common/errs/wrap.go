package errs

import "fmt"

// Wrap returns an error that adds msg as context to err, keeping err in the
// [errors.Is] chain. It returns nil when err is nil, so a call site may wrap
// unconditionally.
//
// The wrapped error comes first, matching [fmt.Errorf] reading order.
func Wrap(err error, msg string) error {
	if err == nil {
		return nil
	}

	return From(err).build(msg)
}

// Wrapf is [Wrap] with a formatted message: the wrapped error first, then
// the format string and its arguments.
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}

	return From(err).build(fmt.Sprintf(format, args...))
}
