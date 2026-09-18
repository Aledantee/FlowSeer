package host

import (
	"context"
	"errors"
	"sort"
	"strings"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
)

// ErrCodeInvalidRequest is a request that does not satisfy its own schema
// rules.
var ErrCodeInvalidRequest = errs.NewCode("host/invalid-request")

// ValidatingInterceptor refuses a request that fails its schema rules before
// any handler acts on it.
//
// The handlers rely on those rules and none of them re-state them, which is
// the right division — the schema is where a rule belongs — but it only holds
// if something enforces them. Nothing did: an intent with no idempotency key
// reached the journal and burned one of the record's sixty-four remembered
// slots on an empty string that the schema says must be a uuid, and an intent
// with no change arm was admitted with nothing to apply, took the device's
// lane, and left no expectation behind when it verified.
//
// The caller is told which fields failed and which rule each broke, and never
// the values it sent back. Field paths and rule identifiers are this service's
// own vocabulary; the values are the caller's data, and echoing data into an
// error message is how it reaches a log that was never meant to hold it.
func ValidatingInterceptor() connect.Interceptor {
	return validatingInterceptor{}
}

type validatingInterceptor struct{}

func (v validatingInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := validateMessage(req.Any()); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// WrapStreamingHandler validates nothing, and neither streaming handler runs
// protovalidate on its open message.
//
// What each does instead is check the fields it uses against something
// authoritative, which is the property to hold when a streaming handler joins
// this list. Subscribe and SubscribeCaptureAssignments ignore their requests
// entirely and work from the edge identity the assertion middleware
// established. OpenDeviceSubmission reads device_id, binding_id and sequence
// and resolves each against the registry and the lane record. UploadCapture
// resolves its first chunk's session ref against the session record and the
// edge its own in-stream assertion names, and holds every later chunk to that
// same session and edge. TailCaptureSession
// and DownloadCaptureSession read a session id and resolve it against the
// record before it reaches a store or a path. A field nobody reads is a field
// no constraint on it could protect.
//
// So this is a choice rather than a limitation — an interceptor can wrap
// StreamingHandlerConn.Receive and validate the message as it is read — and
// it is recorded as one. The previous comment said the handlers validate what
// they receive, which is not what they do, and would have let a handler that
// started trusting an unchecked field look covered.
func (v validatingInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// WrapStreamingClient passes through: this service issues no Connect calls.
func (v validatingInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func validateMessage(payload any) error {
	msg, ok := payload.(proto.Message)
	if !ok {
		return nil // not a protobuf request; nothing here can judge it
	}
	err := protovalidate.Validate(msg)
	if err == nil {
		return nil
	}

	return connecterr.WrapAs(connect.CodeInvalidArgument, "",
		errs.From(err).Code(ErrCodeInvalidRequest).
			UserMsg("the request does not satisfy its schema rules: "+violationSummary(err)).
			Attr("procedure_message", string(msg.ProtoReflect().Descriptor().FullName())).
			Msg("request fails its schema rules"))
}

// violationSummary names what failed, as "field: rule", sorted and deduplicated
// so one request produces one stable sentence. It carries no field value.
func violationSummary(err error) string {
	var validation *protovalidate.ValidationError
	if !errors.As(err, &validation) {
		return "the request could not be validated"
	}

	seen := make(map[string]struct{}, len(validation.Violations))
	named := make([]string, 0, len(validation.Violations))
	for _, violation := range validation.Violations {
		field := fieldPath(violation.Proto.GetField())
		if field == "" {
			field = "the message"
		}
		entry := field + ": " + violation.Proto.GetRuleId()
		if _, dup := seen[entry]; dup {
			continue
		}
		seen[entry] = struct{}{}
		named = append(named, entry)
	}
	sort.Strings(named)
	return strings.Join(named, "; ")
}

// fieldPath renders a violation's field as a dotted name — "interface.name" —
// from the path's field names alone.
//
// Two things it deliberately does not do. It does not call the message's own
// String(), which is the prototext form of the whole FieldPath
// (`elements:{field_name:"description"}`) rather than a field name, and was
// what this produced before. And it does not use protovalidate's own
// FieldPathString, which appends subscripts: an index is harmless, but a map
// key is a caller-supplied value, and this string goes into the message
// returned to that caller under a promise that it carries no field values.
// No validated request message has a map today, which is the only reason
// that was latent rather than live.
func fieldPath(path *validate.FieldPath) string {
	elements := path.GetElements()
	names := make([]string, 0, len(elements))
	for _, element := range elements {
		if name := element.GetFieldName(); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ".")
}
