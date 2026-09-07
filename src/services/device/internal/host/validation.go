package host

import (
	"context"
	"errors"
	"sort"
	"strings"

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
// authoritative: Subscribe ignores its request entirely and works from the
// edge identity the assertion middleware established, and OpenDeviceSubmission
// reads device_id, binding_id and sequence and resolves each against the
// registry and the lane record. A field nobody reads is a field no constraint
// on it could protect.
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
		field := violation.Proto.GetField().String()
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
