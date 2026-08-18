package errs

import "fmt"

// Builder accumulates an error's payload. Every method returns a new
// Builder, so a partially built value can be reused as the base of several
// errors. The chain terminates in [Builder.Msg] or [Builder.Msgf], which
// produce the error itself.
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
func (b Builder) Attr(key string, val any) Builder {
	return b.appendAttr(attr{key: key, val: val})
}

// PubAttr attaches a client-safe key-value pair: one a boundary may expose
// to an untrusted caller. It appears in both [Attributes] and
// [SafeAttributes].
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
