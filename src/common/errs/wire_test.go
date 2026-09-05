package errs

import (
	"errors"
	"fmt"
	"testing"
)

var errWireTestCode = NewCode("errs/wire-test-code")

func TestEncodeNilIsNil(t *testing.T) {
	if got := Encode(nil); got != nil {
		t.Errorf("Encode(nil) = %v, want nil", got)
	}
	if got := EncodeForClient(nil); got != nil {
		t.Errorf("EncodeForClient(nil) = %v, want nil", got)
	}
}

func TestDecodeNilIsNil(t *testing.T) {
	if got := Decode(nil); got != nil {
		t.Errorf("Decode(nil) = %v, want nil", got)
	}
}

// TestEncodeDecodeRoundTrip pins the total-decode requirement: a chain
// mixing errs's own type and a foreign leaf survives Encode then Decode with
// its code, its safe attributes, and its cause shape intact.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	foreign := fmt.Errorf("dial tcp 10.0.0.5:161: connection refused")
	original := From(foreign).
		Code(errWireTestCode).
		Attr("engine_id", "8000000001").
		PubAttr("proto", "usm-aes").
		Msg("privacy decryption failed")

	payload := Encode(original)
	decoded := Decode(payload)

	if !errors.Is(decoded, &Error{code: errWireTestCode}) {
		t.Errorf("decoded error does not match code %q", errWireTestCode)
	}

	if got, want := decoded.Error(), original.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	safe := SafeAttributes(decoded)
	if len(safe) != 1 {
		t.Fatalf("SafeAttributes(decoded) = %v, want exactly one entry", safe)
	}
	if safe["proto"] != "usm-aes" {
		t.Errorf(`SafeAttributes(decoded)["proto"] = %v, want "usm-aes"`, safe["proto"])
	}
	if _, internal := safe["engine_id"]; internal {
		t.Error("the internal engine_id attribute crossed the wire")
	}

	//goland:noinspection GoTypeAssertionOnErrors
	e, ok := decoded.(*Error)
	if !ok {
		t.Fatalf("decoded is %T, want *Error", decoded)
	}
	if len(e.causes) != 1 {
		t.Fatalf("decoded has %d causes, want 1", len(e.causes))
	}

	//goland:noinspection GoTypeAssertionOnErrors
	cause, ok := e.causes[0].(*Error)
	if !ok {
		t.Fatalf("decoded cause is %T, want *Error", e.causes[0])
	}
	if cause.code != "" {
		t.Errorf("decoded foreign leaf carries code %q, want none", cause.code)
	}
	if cause.msg != foreign.Error() {
		t.Errorf("decoded foreign leaf message = %q, want %q", cause.msg, foreign.Error())
	}
}

// TestDecodeUnknownCodeSurfacesAsInternal pins the error-wire record's rule
// that an unregistered wire code never becomes the decoded error's own code
// verbatim, so it cannot coincidentally match an unrelated local code under
// errors.Is.
func TestDecodeUnknownCodeSurfacesAsInternal(t *testing.T) {
	original := New().Code("some-other-service/never-registered-here").Msg("boom")

	decoded := Decode(Encode(original))

	if !errors.Is(decoded, &Error{code: ErrCodeUnknownRemote}) {
		t.Error("decoded error does not match ErrCodeUnknownRemote")
	}

	attrs := Attributes(decoded)
	if attrs["wire_code"] != "some-other-service/never-registered-here" {
		t.Errorf(`Attributes(decoded)["wire_code"] = %v, want the original wire code`, attrs["wire_code"])
	}
}

// TestDecodeKnownCodeMatches pins that a code this binary does register
// decodes to that exact code, not to ErrCodeUnknownRemote.
func TestDecodeKnownCodeMatches(t *testing.T) {
	original := New().Code(errWireTestCode).Msg("boom")

	decoded := Decode(Encode(original))

	if !errors.Is(decoded, &Error{code: errWireTestCode}) {
		t.Errorf("decoded error does not match %q", errWireTestCode)
	}
	if errors.Is(decoded, &Error{code: ErrCodeUnknownRemote}) {
		t.Error("a known code was decoded as ErrCodeUnknownRemote")
	}
}

// TestEncodeForClientOmitsInternalContent pins the error-wire record's
// client-boundary rule: no message text, no stack, and the user message
// falls back to a generic string when the chain names none.
func TestEncodeForClientOmitsInternalContent(t *testing.T) {
	noUserMsg := New().Msg("db conn refused at 10.0.0.5")

	payload := EncodeForClient(noUserMsg)

	if payload.GetMessage() != "" {
		t.Errorf("EncodeForClient set Message = %q, want empty", payload.GetMessage())
	}
	if len(payload.GetStack()) != 0 {
		t.Errorf("EncodeForClient set Stack = %v, want empty", payload.GetStack())
	}
	if len(payload.GetCauses()) != 0 {
		t.Errorf("EncodeForClient set Causes = %v, want empty", payload.GetCauses())
	}
	if payload.GetUserMessage() != genericUserMessage {
		t.Errorf("EncodeForClient UserMessage = %q, want the generic fallback %q", payload.GetUserMessage(), genericUserMessage)
	}
}

func TestEncodeForClientKeepsSetUserMessageAndHint(t *testing.T) {
	withMsg := From(New().Code(errWireTestCode).Attr("engine_id", "8000000001").PubAttr("device", "icx7150").Msg("privacy decryption failed")).
		UserMsg("could not read the device").
		Hint("check the device's USM credentials").
		Retryable().
		Msg("wrapped")

	payload := EncodeForClient(withMsg)

	if payload.GetUserMessage() != "could not read the device" {
		t.Errorf("UserMessage = %q, want the outermost user message", payload.GetUserMessage())
	}
	if payload.GetHint() != "check the device's USM credentials" {
		t.Errorf("Hint = %q, want the outermost hint", payload.GetHint())
	}
	if payload.GetCode() != errWireTestCode.String() {
		t.Errorf("Code = %q, want %q", payload.GetCode(), errWireTestCode)
	}
	if payload.GetSafeAttributes()["device"].GetStringValue() != "icx7150" {
		t.Errorf("SafeAttributes[device] = %v, want %q", payload.GetSafeAttributes()["device"], "icx7150")
	}
	if _, internal := payload.GetSafeAttributes()["engine_id"]; internal {
		t.Error("EncodeForClient leaked the internal engine_id attribute")
	}
}

// TestEncodePreservesStackOnlyOnTrustedTransit pins that Encode carries a
// symbolized stack while EncodeForClient never does.
func TestEncodePreservesStackOnlyOnTrustedTransit(t *testing.T) {
	err := New().Msg("boom")

	internal := Encode(err)
	if len(internal.GetStack()) == 0 {
		t.Error("Encode carried no stack for a freshly captured error")
	}

	client := EncodeForClient(err)
	if len(client.GetStack()) != 0 {
		t.Error("EncodeForClient carried a stack")
	}
}

// TestBoundStackFramesEnforcesTheSchemaLimit pins that Encode never emits
// more frames than spec/proto/flowseer/errs/v1/error.proto's stack bound,
// even though runtime.CallersFrames can expand a single captured program
// counter into several frames across an inlined call, so a
// maxStackDepth-sized capture is not on its own a bound on frame count.
func TestBoundStackFramesEnforcesTheSchemaLimit(t *testing.T) {
	frames := make([]string, wireMaxStackFrames+5)
	for i := range frames {
		frames[i] = fmt.Sprintf("frame %d", i)
	}

	bounded := boundStackFrames(frames)

	if len(bounded) != wireMaxStackFrames {
		t.Errorf("boundStackFrames returned %d frames, want %d", len(bounded), wireMaxStackFrames)
	}
}

// TestEncodeUnwrapsForeignWrapperOverCodedError pins that a plain wrapper
// (fmt.Errorf("...: %w", err), which code-style.md blesses "where nothing
// structured is needed") does not collapse a coded *Error beneath it to an
// opaque leaf: the code and cause chain still cross the wire.
func TestEncodeUnwrapsForeignWrapperOverCodedError(t *testing.T) {
	coded := New().Code(errWireTestCode).Msg("privacy decryption failed")
	wrapped := fmt.Errorf("open session: %w", coded)

	decoded := Decode(Encode(wrapped))

	if !errors.Is(decoded, &Error{code: errWireTestCode}) {
		t.Error("a coded error beneath a foreign wrapper did not survive Encode/Decode")
	}
}
