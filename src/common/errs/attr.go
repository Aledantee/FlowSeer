package errs

// attr is one flat key-value pair. safe marks a value a boundary may expose
// to an untrusted client.
type attr struct {
	key  string
	val  any
	safe bool
}

// Attributes returns the attributes of err's whole chain, merged. It returns
// an empty map for a nil error or one carrying no attributes.
//
// The chain is traversed outermost first, joined branches left to right, and
// the first value seen for a key wins — so a wrapper's attribute overrides
// the same key on its cause. Errors from other packages are traversed
// through but contribute nothing. [LogValue] uses the same traversal, so
// logs and this map never disagree.
func Attributes(err error) map[string]any {
	return collect(err, false)
}

// SafeAttributes returns the subset of [Attributes] marked client-safe with
// [Builder.PubAttr], under the same traversal. It is what a boundary facing
// untrusted callers may expose.
func SafeAttributes(err error) map[string]any {
	return collect(err, true)
}

func collect(err error, safeOnly bool) map[string]any {
	merged := make(map[string]any)

	eachAttr(err, func(a attr) {
		if safeOnly && !a.safe {
			return
		}
		if _, seen := merged[a.key]; seen {
			return
		}

		merged[a.key] = a.val
	})

	return merged
}

// eachAttr calls fn for every attribute in err's chain in traversal order,
// including keys seen more than once.
func eachAttr(err error, fn func(attr)) {
	walk(err, func(e *Error) bool {
		for _, a := range e.attrs {
			fn(a)
		}

		return true
	})
}

// walk visits every [Error] in err's tree outermost first, joined branches
// left to right, and stops early when fn returns false. Errors from other
// packages are unwrapped through but not visited.
func walk(err error, fn func(*Error) bool) {
	for err != nil {
		//goland:noinspection GoTypeAssertionOnErrors
		if e, ok := err.(*Error); ok && e != nil && !fn(e) {
			return
		}

		switch u := err.(type) {
		case interface{ Unwrap() error }:
			err = u.Unwrap()
		case interface{ Unwrap() []error }:
			for _, branch := range u.Unwrap() {
				if !walkBranch(branch, fn) {
					return
				}
			}

			return
		default:
			return
		}
	}
}

// walkBranch walks one branch of a joined tree and reports whether the
// traversal should continue.
func walkBranch(err error, fn func(*Error) bool) bool {
	stopped := false

	walk(err, func(e *Error) bool {
		if fn(e) {
			return true
		}

		stopped = true

		return false
	})

	return !stopped
}
