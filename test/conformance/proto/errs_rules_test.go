package conformance

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
)

func errorPayload() errsv1.ErrorPayload_builder {
	return errsv1.ErrorPayload_builder{
		Code:    proto.String("snmp/priv-decrypt"),
		Message: proto.String("privacy decryption failed"),
		SafeAttributes: map[string]*structpb.Value{
			"proto": structpb.NewStringValue("usm-aes"),
		},
		UserMessage: proto.String("could not read the device"),
		Hint:        proto.String("check the device's USM credentials"),
		Retry:       errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE.Enum(),
	}
}

func TestErrorPayloadRules(t *testing.T) {
	withCause := errorPayload()
	withCause.Causes = []*errsv1.ErrorPayload{
		errsv1.ErrorPayload_builder{Message: proto.String("connection reset")}.Build(),
	}

	tooManyCauses := errorPayload()
	var causes []*errsv1.ErrorPayload
	for range 17 {
		causes = append(causes, errsv1.ErrorPayload_builder{Message: proto.String("cause")}.Build())
	}
	tooManyCauses.Causes = causes

	tooManyAttrs := errorPayload()
	attrs := make(map[string]*structpb.Value, 33)
	for i := range 33 {
		attrs[fmt.Sprintf("attr-%d", i)] = structpb.NewBoolValue(true)
	}
	tooManyAttrs.SafeAttributes = attrs

	longStackFrame := errorPayload()
	longStackFrame.Stack = []string{strings.Repeat("f", 513)}

	tooManyStackFrames := errorPayload()
	var frames []string
	for range 33 {
		frames = append(frames, "frame")
	}
	tooManyStackFrames.Stack = frames

	tests := []validationCase{
		{name: "leaf error with just a message is valid", message: errsv1.ErrorPayload_builder{Message: proto.String("boom")}.Build(), wantValid: true},
		{name: "a full internal payload is valid", message: errorPayload().Build(), wantValid: true},
		{name: "a payload with a cause is valid", message: withCause.Build(), wantValid: true},
		{name: "an empty payload is valid", message: errsv1.ErrorPayload_builder{}.Build(), wantValid: true},
		{name: "more than 16 causes is rejected", message: tooManyCauses.Build()},
		{name: "more than 32 safe attributes is rejected", message: tooManyAttrs.Build()},
		{name: "a stack frame over 512 characters is rejected", message: longStackFrame.Build()},
		{name: "more than 32 stack frames is rejected", message: tooManyStackFrames.Build()},
	}

	runValidationCases(t, tests)
}
