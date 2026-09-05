package errs

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
)

// ErrCodeUnknownRemote marks an error [Decode] reconstructed from a peer's
// wire code that this binary does not register. Decode never carries the
// raw wire string into the reconstructed Error's own code: an unregistered
// string, kept unchanged, could coincidentally equal a code this binary
// registers for something unrelated, and errors.Is would then match by
// accident rather than by the peer's actual intent. The original string
// survives as an internal "wire_code" attribute for diagnostics.
var ErrCodeUnknownRemote = NewCode("errs/unknown-remote-code")

// genericUserMessage is what EncodeForClient reports when the chain names
// no user message, per the error-wire record: a client-facing boundary
// falls back to a generic string rather than leaking the internal message.
const genericUserMessage = "an internal error occurred"

// Encode renders err for trusted internal transit: service to service, or a
// broker envelope. Both peers trust each other, so the message text and a
// symbolized stack per origin ride along with the recursive cause chain.
// Only client-safe attributes ever reach the wire, on this path too — an
// attribute never marked [Builder.PubAttr] never leaves the process that
// attached it.
func Encode(err error) *errsv1.ErrorPayload {
	return encodeNode(err)
}

// EncodeForClient renders err for a boundary facing an untrusted caller: the
// code, the client-safe attributes, and the user message and hint, falling
// back to a generic string when the chain names none. No message text, no
// stack, and no cause chain cross this boundary — [CodeOf], [SafeAttributes],
// [UserMessage], [Hint], and the outermost retry disposition already resolve
// the whole chain to the single value a caller needs.
func EncodeForClient(err error) *errsv1.ErrorPayload {
	if err == nil {
		return nil
	}

	userMsg := UserMessage(err)
	if userMsg == "" {
		userMsg = genericUserMessage
	}

	builder := errsv1.ErrorPayload_builder{
		SafeAttributes: attrsToStruct(SafeAttributes(err)),
		UserMessage:    proto.String(userMsg),
		Retry:          retryToWire(retryOf(err)).Enum(),
	}
	if code, ok := CodeOf(err); ok {
		builder.Code = proto.String(code.String())
	}
	if hint := Hint(err); hint != "" {
		builder.Hint = proto.String(hint)
	}

	return builder.Build()
}

// Decode reconstructs an [error] from a wire payload built by [Encode] or
// [EncodeForClient]. Decoding is total: an unrecognized wire code becomes
// [ErrCodeUnknownRemote] rather than a silent unknown some errors.Is might
// match by accident, and every cause decodes through the same recursive
// shape, so a leaf the encoder collapsed from a foreign error type decodes
// to a plain coded-nothing Error carrying just its message.
func Decode(payload *errsv1.ErrorPayload) error {
	if payload == nil {
		return nil
	}

	e := decodeNode(payload)

	return &e
}

func encodeNode(err error) *errsv1.ErrorPayload {
	if err == nil {
		return nil
	}

	builder := errsv1.ErrorPayload_builder{
		Message: proto.String(err.Error()),
	}

	//goland:noinspection GoTypeAssertionOnErrors
	if e, ok := err.(*Error); ok && e != nil {
		builder.Message = proto.String(e.msg)
		if e.code != "" {
			builder.Code = proto.String(e.code.String())
		}
		if safe := safeAttrStruct(e.attrs); len(safe) > 0 {
			builder.SafeAttributes = safe
		}
		if e.userMsg != "" {
			builder.UserMessage = proto.String(e.userMsg)
		}
		if e.hint != "" {
			builder.Hint = proto.String(e.hint)
		}
		if e.retry != retryUnset {
			builder.Retry = retryToWire(e.retry).Enum()
		}
		if frames := e.stack.frames(); len(frames) > 0 {
			builder.Stack = boundStackFrames(frames)
		}
		for _, cause := range e.causes {
			builder.Causes = append(builder.Causes, encodeNode(cause))
		}

		return builder.Build()
	}

	// A foreign error carries no code or safe attributes of its own, but it
	// may wrap one that does — code-style.md blesses fmt.Errorf("...: %w",
	// err) "where nothing structured is needed", so a coded *Error commonly
	// sits one level below a plain wrapper. Unwrapping here, the same way
	// walk does, keeps that code and its causes on the wire instead of
	// collapsing the whole branch to one leaf carrying only rendered text.
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		if next := u.Unwrap(); next != nil {
			builder.Causes = []*errsv1.ErrorPayload{encodeNode(next)}
		}
	case interface{ Unwrap() []error }:
		for _, next := range u.Unwrap() {
			builder.Causes = append(builder.Causes, encodeNode(next))
		}
	}

	return builder.Build()
}

// wireMaxStackFrames matches spec/proto/flowseer/errs/v1/error.proto's
// stack field bound. runtime.CallersFrames can expand a single captured
// program counter into several frames across an inlined call, so a
// maxStackDepth-sized capture can symbolize into more strings than that —
// bounding here keeps every encoded payload valid against its own schema.
const wireMaxStackFrames = 32

func boundStackFrames(frames []string) []string {
	if len(frames) > wireMaxStackFrames {
		return frames[:wireMaxStackFrames]
	}

	return frames
}

func decodeNode(payload *errsv1.ErrorPayload) Error {
	e := Error{
		msg:     payload.GetMessage(),
		userMsg: payload.GetUserMessage(),
		hint:    payload.GetHint(),
		retry:   retryFromWire(payload.GetRetry()),
		attrs:   structToSafeAttrs(payload.GetSafeAttributes()),
	}

	if code := payload.GetCode(); code != "" {
		if _, known := registry.Load(code); known {
			e.code = Code(code)
		} else {
			e.code = ErrCodeUnknownRemote
			e.attrs = append(e.attrs, attr{key: "wire_code", val: code})
		}
	}

	if frames := payload.GetStack(); len(frames) > 0 {
		e.attrs = append(e.attrs, attr{key: "remote_stack", val: frames})
	}

	for _, cause := range payload.GetCauses() {
		decoded := decodeNode(cause)
		e.causes = append(e.causes, &decoded)
	}

	return e
}

// safeAttrStruct converts only the client-safe attributes in attrs to their
// wire form; an internal attribute is skipped entirely, never encoded.
func safeAttrStruct(attrs []attr) map[string]*structpb.Value {
	if len(attrs) == 0 {
		return nil
	}

	out := make(map[string]*structpb.Value, len(attrs))
	for _, a := range attrs {
		if !a.safe {
			continue
		}
		out[a.key] = toValue(a.val)
	}
	if len(out) == 0 {
		return nil
	}

	return out
}

// attrsToStruct converts an already-merged safe-attribute map, as
// [SafeAttributes] returns it, to its wire form.
func attrsToStruct(attrs map[string]any) map[string]*structpb.Value {
	if len(attrs) == 0 {
		return nil
	}

	out := make(map[string]*structpb.Value, len(attrs))
	for k, v := range attrs {
		out[k] = toValue(v)
	}

	return out
}

// toValue renders an attribute value for the wire. structpb.NewValue covers
// every scalar this package's call sites attach; anything it rejects falls
// back to its fmt.Sprint text so encoding a safe attribute never fails.
func toValue(v any) *structpb.Value {
	val, err := structpb.NewValue(v)
	if err != nil {
		return structpb.NewStringValue(fmt.Sprint(v))
	}

	return val
}

// structToSafeAttrs converts a decoded wire attribute map back to attrs,
// all marked safe: everything that reached the wire was safe by
// construction, since an internal attribute is never encoded.
func structToSafeAttrs(m map[string]*structpb.Value) []attr {
	if len(m) == 0 {
		return nil
	}

	out := make([]attr, 0, len(m))
	for k, v := range m {
		out = append(out, attr{key: k, val: v.AsInterface(), safe: true})
	}

	return out
}

func retryToWire(r retry) errsv1.RetryDisposition {
	switch r {
	case retryYes:
		return errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE
	case retryNo:
		return errsv1.RetryDisposition_RETRY_DISPOSITION_PERMANENT
	default:
		return errsv1.RetryDisposition_RETRY_DISPOSITION_UNSPECIFIED
	}
}

func retryFromWire(r errsv1.RetryDisposition) retry {
	switch r {
	case errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE:
		return retryYes
	case errsv1.RetryDisposition_RETRY_DISPOSITION_PERMANENT:
		return retryNo
	default:
		return retryUnset
	}
}
