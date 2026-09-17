package host_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	connect "connectrpc.com/connect"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/agent/host"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
)

// TestALaneCanBeAssembledFromOutsideTheAccessModule is the property the
// module's own tests cannot express. They live under the module's directory,
// so they may name every type in its internal packages; this package may not,
// and a seam whose type it cannot name is a seam it cannot fill.
//
// Three fields were unfillable when this test was written. The submission
// credential source returned an interface declared in internal/credential, so
// no outside type could write the method at all. The telemetry view had no
// reachable constructor, so a host could pass only nil and run with the
// lane's spans and metrics silently off. And the reporter named no device, so
// a host was handed a checkpoint acknowledgement it could not address.
//
// It is a compile-time property first and a behavioral one second: if the
// seams cannot be filled, this file does not build, which is the failure.
// What it asserts beyond that is that the lane it built is the real one.
func TestALaneCanBeAssembledFromOutsideTheAccessModule(t *testing.T) {
	client := edgev1connect.NewEdgeServiceClient(http.DefaultClient, "https://central.example.test")
	read, submission := access.NewConnectCredentials(client)
	telemetry, err := access.NewTelemetry(access.TelemetryConfig{})
	if err != nil {
		t.Fatalf("NewTelemetry: %v", err)
	}

	lane := access.NewLane(access.Config{
		QueueCapacity:         4,
		ReadCredentials:       read,
		SubmissionCredentials: submission,
		Telemetry:             telemetry,
		Reporter:              host.LaneReporterForTest(discardOutbound{}),
		Audit:                 auditNoop{},
		Clock:                 time.Now,
		OperationTimeout:      time.Second,
	})
	t.Cleanup(func() { _, _ = lane.Close(context.Background()) })

	// The lane is real: a device it was never told about is refused by its
	// own code rather than by a nil dereference somewhere.
	_, err = lane.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   readRequest(),
	})
	if code, _ := errs.CodeOf(err); code != access.ErrCodeUnknownDevice {
		t.Fatalf("Submit() code = %v (err %v), want %v", code, err, access.ErrCodeUnknownDevice)
	}
}

// TestTheCredentialSourcesReachCentral proves NewConnectCredentials returns
// sources wired to the client it was given, rather than two values that
// merely satisfy the interfaces. A source that dialed nothing would pass the
// assembly test above and fail every read in the field.
func TestTheCredentialSourcesReachCentral(t *testing.T) {
	central := &recordingEdge{}
	mux := http.NewServeMux()
	path, handler := edgev1connect.NewEdgeServiceHandler(central)
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	read, _ := access.NewConnectCredentials(edgev1connect.NewEdgeServiceClient(server.Client(), server.URL))
	if _, err := read.AcquireReadCredential(context.Background(), "0192e6a0-0000-7000-8000-0000000000d1",
		"0192e6a0-0000-7000-8000-0000000000b1", nil); err != nil {
		t.Fatalf("AcquireReadCredential: %v", err)
	}

	if got := central.acquired(); got != "0192e6a0-0000-7000-8000-0000000000d1" {
		t.Errorf("central saw device %q, want the one the source was asked for", got)
	}
}

// TestEachReportIsAddressedToItsDevice covers the adapter the reporter seam
// exists for. Central requires a device on every report and none of the three
// messages carries one, so this mapping is the only place the two are joined.
func TestEachReportIsAddressedToItsDevice(t *testing.T) {
	out := &recordingOutbound{}
	reporter := host.LaneReporterForTest(out)
	ctx := context.Background()

	result := &integrationv1.ExecuteResult{}
	result.SetSequence(7)
	reporter.Reported(ctx, "dev-1", result)

	ack := &integrationv1.CheckpointAck{}
	ack.SetSequence(7)
	reporter.CheckpointAcked(ctx, "dev-2", ack)

	hold := &integrationv1.HoldResolvedAck{}
	hold.SetSequence(7)
	reporter.HoldResolvedAcked(ctx, "dev-3", hold)

	reports := out.reports()
	if len(reports) != 3 {
		t.Fatalf("reports = %d, want 3", len(reports))
	}
	for i, want := range []string{"dev-1", "dev-2", "dev-3"} {
		if got := reports[i].GetDeviceId(); got != want {
			t.Errorf("report %d addressed to %q, want %q", i, got, want)
		}
	}
	if !reports[0].HasResult() || !reports[1].HasCheckpointAck() || !reports[2].HasHoldResolvedAck() {
		t.Errorf("each report must carry its own arm: got %v", reports)
	}
}

type auditNoop struct{}

func (auditNoop) Emit(context.Context, *eventv1.DeviceOperationEvent) error { return nil }

type recordingOutbound struct {
	seen []*integrationv1.ReportRequest
}

func (r *recordingOutbound) Report(_ context.Context, report *integrationv1.ReportRequest) {
	r.seen = append(r.seen, report)
}

func (r *recordingOutbound) reports() []*integrationv1.ReportRequest { return r.seen }

// recordingEdge answers AcquireReadCredential and remembers who it was for.
type recordingEdge struct {
	edgev1connect.UnimplementedEdgeServiceHandler

	device string
}

func (e *recordingEdge) AcquireReadCredential(
	_ context.Context, req *connect.Request[edgev1.AcquireReadCredentialRequest],
) (*connect.Response[edgev1.AcquireReadCredentialResponse], error) {
	e.device = req.Msg.GetDeviceId()
	return connect.NewResponse(&edgev1.AcquireReadCredentialResponse{}), nil
}

func (e *recordingEdge) acquired() string { return e.device }

func readRequest() *integrationv1.ExecuteRequest {
	intent := &accessv1.InterfaceReadIntent{}
	intent.SetInterfaceName("ethernet 1/1/1")
	typed := &accessv1.TypedRead{}
	typed.SetInterface(intent)
	request := &integrationv1.ExecuteRequest{}
	request.SetRead(typed)
	request.SetSequence(1)
	return request
}

// discardOutbound stands in for the queue where a test drives the lane and
// asserts nothing about what was reported. Not nil: a reporter that answered
// a missing queue by dropping the report would make the wiring mistake it
// exists to prevent invisible in production too.
type discardOutbound struct{}

func (discardOutbound) Report(context.Context, *integrationv1.ReportRequest) {}
