package errs

import (
	"log/slog"
	"strconv"
)

// Log group and field keys. They are fixed so log queries can rely on them.
const (
	msgKey   = "msg"
	codeKey  = "code"
	attrsKey = "attributes"
	stackKey = "stack"
)

// LogValue renders the whole error tree as one group: the rendered message,
// the chain's [Code] if any, the merged attributes under "attributes", and
// the captured stacks under "stack" with symbolized frames.
//
// Attributes are merged with the traversal [Attributes] uses, so the two
// surfaces cannot disagree, and every key appears once. LogValue never
// panics on nil or oddly shaped attribute values. Note that internal
// attributes reach the log — logs are a trusted surface.
func (e *Error) LogValue() slog.Value {
	if e == nil {
		return slog.Value{}
	}

	attrs := []slog.Attr{slog.String(msgKey, e.Error())}

	if code, ok := CodeOf(e); ok {
		attrs = append(attrs, slog.String(codeKey, code.String()))
	}

	if merged := mergedLogAttrs(e); len(merged) > 0 {
		attrs = append(attrs, slog.GroupAttrs(attrsKey, merged...))
	}

	if captured := stacks(e); len(captured) > 0 {
		attrs = append(attrs, stackAttr(captured))
	}

	return slog.GroupValue(attrs...)
}

// mergedLogAttrs flattens the tree's attributes in traversal order, keeping
// the first value seen for each key.
func mergedLogAttrs(err error) []slog.Attr {
	var (
		out  []slog.Attr
		seen = make(map[string]struct{})
	)

	eachAttr(err, func(a attr) {
		if _, dup := seen[a.key]; dup {
			return
		}

		seen[a.key] = struct{}{}
		out = append(out, slog.Any(a.key, a.val))
	})

	return out
}

// stackAttr renders captured stacks under one key: the frames directly for a
// single origin, numbered groups when a joined tree carries several.
func stackAttr(captured []stack) slog.Attr {
	if len(captured) == 1 {
		return slog.Any(stackKey, captured[0].frames())
	}

	origins := make([]slog.Attr, 0, len(captured))
	for i, s := range captured {
		origins = append(origins, slog.Any(strconv.Itoa(i), s.frames()))
	}

	return slog.GroupAttrs(stackKey, origins...)
}
