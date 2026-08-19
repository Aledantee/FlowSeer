package errs

// ExitCode returns the status a process should exit with because of err.
//
// It is 0 for a nil error, the outermost code set with [Builder.ExitCode]
// when the chain carries one, and 1 otherwise — the conventional "failed"
// status, so a caller may exit on any error without checking first. The
// outermost wins, matching every other extractor here: the level that
// decided to end the process outranks the one that first noticed trouble.
//
// The value is process-local and never crosses the wire; a remote peer's
// exit status says nothing about this process.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}

	if code, ok := exitCodeOf(err); ok {
		return code
	}

	return 1
}

// exitCodeOf returns the outermost exit code the chain sets and reports
// whether one was set at all, which [ExitCode]'s default of 1 hides.
func exitCodeOf(err error) (int, bool) {
	var (
		found int
		ok    bool
	)

	walk(err, func(e *Error) bool {
		if e.exitCode <= 0 {
			return true
		}

		found, ok = e.exitCode, true

		return false
	})

	return found, ok
}
