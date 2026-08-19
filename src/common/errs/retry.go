package errs

// retry is an error's retry disposition. The zero value means the error
// says nothing about retrying, which is what lets a wrapper's [Builder.Fatal]
// overrule a transient cause while an undecided wrapper defers to it.
type retry uint8

const (
	retryUnset retry = iota
	retryYes
	retryNo
)

// Retryable reports whether err describes a transient failure worth trying
// again — a poll loop's backoff, a broker's redelivery, an RPC boundary
// choosing a retryable status code.
//
// The outermost error that expressed a disposition decides, so a wrapper
// that has exhausted its retry budget can mark itself [Builder.Fatal] over a
// retryable cause. A chain where nothing expressed one is not retryable:
// retrying is the claim that needs making, and a caller that retries a
// permanent failure loops forever.
func Retryable(err error) bool {
	return retryOf(err) == retryYes
}

// retryOf returns the outermost disposition the chain expresses, or
// retryUnset when none does — the distinction [Retryable]'s bool collapses.
func retryOf(err error) retry {
	found := retryUnset

	walk(err, func(e *Error) bool {
		if e.retry == retryUnset {
			return true
		}

		found = e.retry

		return false
	})

	return found
}
