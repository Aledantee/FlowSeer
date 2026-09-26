package errs

import (
	"runtime"
	"strconv"
)

// maxStackDepth bounds a captured stack. Deeper frames are the runtime and
// test harness, which no reader of a log needs.
const maxStackDepth = 32

// stack holds unsymbolized program counters. Symbolization is deferred to
// [stack.frames] because most errors are handled, not logged.
type stack []uintptr

// capture records the program counters of the call site that owns the error.
// It is called only from [Builder.build] and [wrap], which every public
// constructor reaches through exactly one frame — capture, build or wrap, the
// constructor, then the site the stack should name.
func capture() stack {
	pcs := make([]uintptr, maxStackDepth)
	n := runtime.Callers(4, pcs)

	return pcs[:n]
}

// frames symbolizes the stack as "function (file:line)" entries, outermost
// call site first.
func (s stack) frames() []string {
	if len(s) == 0 {
		return nil
	}

	out := make([]string, 0, len(s))
	frames := runtime.CallersFrames(s)

	for {
		frame, more := frames.Next()
		if frame.Function != "" || frame.File != "" {
			out = append(out, formatFrame(frame))
		}
		if !more {
			break
		}
	}

	return out
}

func formatFrame(f runtime.Frame) string {
	name := f.Function
	if name == "" {
		name = "unknown"
	}

	return name + " (" + f.File + ":" + strconv.Itoa(f.Line) + ")"
}

// errorStack returns the stack captured by an [*Error] or [*wrapError], or nil
// if err is nil or carries no stack.
func errorStack(err error) stack {
	switch e := err.(type) {
	case *Error:
		if e != nil {
			return e.stack
		}
	case *wrapError:
		if e != nil {
			return e.stack
		}
	}

	return nil
}

// anyStack reports whether any error in the given causes already carries a
// stack, which is what makes a wrap skip capture.
func anyStack(causes []error) bool {
	for _, cause := range causes {
		found := false

		walk(cause, func(e error) bool {
			if len(errorStack(e)) > 0 {
				found = true

				return false
			}

			return true
		})

		if found {
			return true
		}
	}

	return false
}

// stacks returns every stack in err's chain, in traversal order. A joined
// tree of independently created origins legitimately holds more than one.
func stacks(err error) []stack {
	var out []stack

	walk(err, func(e error) bool {
		if s := errorStack(e); len(s) > 0 {
			out = append(out, s)
		}

		return true
	})

	return out
}
