package errs

// UserMessage returns the client-facing message from err's chain: the
// outermost one set with [Builder.UserMsg], or "" when none is.
//
// It is the message a boundary facing untrusted callers sends in place of
// the internal one, which names hosts, engine IDs, and call paths. The
// outermost wins because the level closest to the caller knows best what
// that caller was trying to do.
func UserMessage(err error) string {
	return firstString(err, func(e *Error) string { return e.userMsg })
}

// Hint returns the outermost remedy set with [Builder.Hint], or "" when
// none is. It accompanies [UserMessage] and is client-safe under the same
// rule.
func Hint(err error) string {
	return firstString(err, func(e *Error) string { return e.hint })
}

// firstString returns the first non-empty value get yields, walking the
// chain outermost first and joined branches left to right — the traversal
// [Attributes] and [Error.LogValue] share.
func firstString(err error, get func(*Error) string) string {
	var found string

	walk(err, func(e *Error) bool {
		if s := get(e); s != "" {
			found = s
			return false
		}

		return true
	})

	return found
}
