package connecterr_test

import (
	"errors"
	"strings"
	"testing"

	connect "connectrpc.com/connect"

	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
)

var (
	errCodeRefused   = errs.NewCode("connecterrtest/refused")
	errCodeUnmapped  = errs.NewCode("connecterrtest/unmapped")
	errCodeWithVoice = errs.NewCode("connecterrtest/with-voice")
)

var table = connecterr.Table{
	errCodeRefused:   {Code: connect.CodePermissionDenied, UserMsg: "the request was refused"},
	errCodeWithVoice: {Code: connect.CodeFailedPrecondition, UserMsg: "the table's message"},
}

// internalDetail is text a caller must never see: it stands for the transport
// address, the bucket name, or the line of a credential file that an errs
// cause chain renders into Error().
const internalDetail = "nats://10.42.0.7:4222 refused: user tegi"

func refused() error {
	return errs.From(errors.New(internalDetail)).Code(errCodeRefused).
		Attr("edge", "edge-1").PubAttr("retry_after", "5s").Msg("read the edge record")
}

func TestWrapSendsNoInternalTextToTheCaller(t *testing.T) {
	err := table.Wrap(refused())

	if strings.Contains(err.Error(), internalDetail) {
		t.Fatalf("the caller's error carries the cause chain: %q", err.Error())
	}
	if strings.Contains(err.Error(), "read the edge record") {
		t.Fatalf("the caller's error carries the internal message: %q", err.Error())
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Wrap returned %T, want a *connect.Error", err)
	}
	if got, want := connectErr.Message(), "the request was refused"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

func TestWrapAnswersTheTablesCode(t *testing.T) {
	if got := connect.CodeOf(table.Wrap(refused())); got != connect.CodePermissionDenied {
		t.Errorf("code = %v, want permission denied", got)
	}
}

// An unmapped code is Internal and says nothing, so a failure nobody has
// classified is never reported as something the caller can fix.
func TestWrapAnswersInternalForACodeTheTableDoesNotName(t *testing.T) {
	err := table.Wrap(errs.New().Code(errCodeUnmapped).Msg("the store lost its lease"))

	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Errorf("code = %v, want internal", got)
	}
	if strings.Contains(err.Error(), "lease") {
		t.Errorf("an unmapped failure named its cause: %q", err.Error())
	}
}

func TestWrapAnswersInternalForAnErrorCarryingNoCode(t *testing.T) {
	if got := connect.CodeOf(table.Wrap(errors.New(internalDetail))); got != connect.CodeInternal {
		t.Errorf("code = %v, want internal", got)
	}
}

// A message the failing site wrote for this caller beats the table's, because
// that site knows what the caller was trying to do.
func TestWrapPrefersTheErrorsOwnUserMessage(t *testing.T) {
	err := table.Wrap(errs.New().Code(errCodeWithVoice).UserMsg("the horizon has passed").Msg("internal"))

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Wrap returned %T, want a *connect.Error", err)
	}
	if got, want := connectErr.Message(), "the horizon has passed"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

func TestWrapKeepsTheFailureReachableInProcess(t *testing.T) {
	internal := refused()
	err := table.Wrap(internal)

	if !errors.Is(err, internal) {
		t.Error("the wrapped error no longer unwraps to the failure it stands for")
	}
	if code, ok := errs.CodeOf(err); !ok || code != errCodeRefused {
		t.Errorf("errs.CodeOf = %q, %v; want %q", code, ok, errCodeRefused)
	}
}

// The detail is what makes the code, the retry disposition, and the
// client-safe attributes readable by a client that decodes it, and it is the
// only place any of them cross.
func TestWrapCarriesTheClientPayloadAsADetail(t *testing.T) {
	var connectErr *connect.Error
	if !errors.As(table.Wrap(refused()), &connectErr) {
		t.Fatal("Wrap returned no *connect.Error")
	}
	details := connectErr.Details()
	if len(details) != 1 {
		t.Fatalf("details = %d, want 1", len(details))
	}
	msg, err := details[0].Value()
	if err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	payload, ok := msg.(*errsv1.ErrorPayload)
	if !ok {
		t.Fatalf("detail is %T, want an ErrorPayload", msg)
	}
	if got := payload.GetCode(); got != errCodeRefused.String() {
		t.Errorf("payload code = %q, want %q", got, errCodeRefused)
	}
	if got, want := payload.GetUserMessage(), "the request was refused"; got != want {
		t.Errorf("payload user message = %q, want %q", got, want)
	}
	if payload.GetMessage() != "" || len(payload.GetCauses()) != 0 || len(payload.GetStack()) != 0 {
		t.Error("the payload carries internal content")
	}
	if _, leaked := payload.GetSafeAttributes()["edge"]; leaked {
		t.Error("the payload carries an internal attribute")
	}
	if _, ok := payload.GetSafeAttributes()["retry_after"]; !ok {
		t.Error("the payload dropped a client-safe attribute")
	}
}

func TestWrapReturnsNilForNil(t *testing.T) {
	if err := table.Wrap(nil); err != nil {
		t.Errorf("Wrap(nil) = %v, want nil", err)
	}
}

// WrapAs answers for a boundary whose code does not depend on what failed, so
// the failure's own code must not change the answer or the text.
func TestWrapAsUsesTheCallSitesCodeAndMessage(t *testing.T) {
	err := connecterr.WrapAs(connect.CodeUnauthenticated, "the call is not authenticated", refused())

	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want unauthenticated", got)
	}
	if got, want := err.Error(), connect.CodeUnauthenticated.String()+": the call is not authenticated"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), internalDetail) {
		t.Errorf("the caller's error carries the cause chain: %q", err.Error())
	}
}

// The generic fallback is the honest answer for a failure the caller can do
// nothing about: it names no host, no bucket, and no file.
func TestAMappingWithNoMessageFallsBackToTheGenericOne(t *testing.T) {
	only := connecterr.Table{errCodeRefused: {Code: connect.CodeInternal}}

	var connectErr *connect.Error
	if !errors.As(only.Wrap(refused()), &connectErr) {
		t.Fatal("Wrap returned no *connect.Error")
	}
	generic := errs.EncodeForClient(errors.New("x")).GetUserMessage()
	if got := connectErr.Message(); got != generic {
		t.Errorf("message = %q, want the generic %q", got, generic)
	}
}
