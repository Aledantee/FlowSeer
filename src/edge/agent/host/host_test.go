package host_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1/attachv1connect"
	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1/ingestv1connect"
	telemetryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1/telemetryv1connect"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/agent/host"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/identity"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/syslogsource"
	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

const testEdgeID = "0192e6a0-0000-7000-8000-0000000000ed"

// orderedCentral records the procedure of every call and whether it arrived
// with an assertion, which is what the agent's startup order is visible as
// from the other end.
type orderedCentral struct {
	attachv1connect.UnimplementedEdgeServiceHandler

	mu        sync.Mutex
	calls     []call
	refuse    bool
	anchors   [][]byte
	signed    chan struct{}
	ingest    chan *ingestv1.IngestRecord
	telemetry chan struct{}
}

type call struct {
	procedure string
	signed    bool
}

func (c *orderedCentral) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.calls = append(c.calls, call{
			procedure: filepath.Base(r.URL.Path),
			signed:    r.Header.Get("Authorization") != "",
		})
		if r.Header.Get("Authorization") != "" && c.signed != nil {
			select {
			case c.signed <- struct{}{}:
			default:
			}
		}
		c.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (c *orderedCentral) seen() []call {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]call(nil), c.calls...)
}

func (c *orderedCentral) Enroll(
	_ context.Context, _ *connect.Request[attachv1.EnrollRequest],
) (*connect.Response[attachv1.EnrollResponse], error) {
	if c.refuse {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("the request was refused"))
	}
	anchors := c.anchors
	if anchors == nil {
		anchors = [][]byte{make([]byte, 32)}
	}
	return connect.NewResponse(attachv1.EnrollResponse_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
		}.Build(),
		Audience:     proto.String("flowseer-central"),
		TrustAnchors: anchors,
	}.Build()), nil
}

type orderedIngest struct {
	ingestv1connect.UnimplementedIngestServiceHandler
	received chan *ingestv1.IngestRecord
}

func (h orderedIngest) Deliver(_ context.Context, r *connect.Request[edgeingestv1.DeliverRequest]) (*connect.Response[edgeingestv1.DeliverResponse], error) {
	for _, record := range r.Msg.GetRecords() {
		if err := protovalidate.Validate(record); err != nil {
			return nil, err
		}
		if h.received != nil {
			select {
			case h.received <- record:
			default:
			}
		}
	}
	response := edgeingestv1.DeliverResponse_builder{Settled: proto.Uint32(uint32(len(r.Msg.GetRecords())))}.Build()
	if err := protovalidate.Validate(response); err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

type orderedTelemetry struct {
	telemetryv1connect.UnimplementedTelemetryServiceHandler
	refused chan struct{}
}

func (h orderedTelemetry) Deliver(_ context.Context, r *connect.Request[telemetryv1.DeliverRequest]) (*connect.Response[telemetryv1.DeliverResponse], error) {
	settled := uint32(len(r.Msg.GetExports()))
	if h.refused != nil {
		select {
		case h.refused <- struct{}{}:
		default:
		}
		settled = 0
	}
	response := telemetryv1.DeliverResponse_builder{Settled: proto.Uint32(settled)}.Build()
	if err := protovalidate.Validate(response); err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

// agentAgainst stands central up and writes the two files an agent is
// deployed with, returning its loaded configuration.
func agentAgainst(t *testing.T, central *orderedCentral) *host.Config {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := attachv1connect.NewEdgeServiceHandler(central)
	mux.Handle(path, central.record(handler))
	mux.Handle(ingestv1connect.NewIngestServiceHandler(orderedIngest{received: central.ingest}))
	mux.Handle(telemetryv1connect.NewTelemetryServiceHandler(orderedTelemetry{refused: central.telemetry}))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	dir := t.TempDir()
	provisioning := filepath.Join(dir, "provisioning.textproto")
	if err := os.WriteFile(provisioning, []byte(`central_url: "`+server.URL+`"
setup_key: "`+testSetupKey+`"
trust_anchors: "`+testAnchor+`"
`), 0o600); err != nil {
		t.Fatalf("write provisioning: %v", err)
	}
	configPath := filepath.Join(dir, "agent.textproto")
	if err := os.WriteFile(configPath, []byte(`state_dir: "`+filepath.Join(dir, "state")+`"
provisioning_path: "`+provisioning+`"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := host.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// Every call after enrollment uses the registered identity.
func TestTheAgentEnrollsFirstAndSignsEverythingAfterwards(t *testing.T) {
	central := &orderedCentral{signed: make(chan struct{}, 1)}
	cfg := agentAgainst(t, central)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	spawn.Go(ctx, "agent", func() { done <- host.Run(ctx, cfg, "v0-test", host.Options{}) })
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not stop during cleanup")
		}
	}()
	select {
	case <-central.signed:
	case err := <-done:
		done <- err
		t.Fatalf("agent stopped before cancellation: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no signed call")
	}
	seen := central.seen()
	if len(seen) < 2 || seen[0].procedure != "Enroll" || seen[0].signed || !seen[1].signed {
		t.Fatalf("calls = %v, want unsigned Enroll then signed call", seen)
	}
	for _, call := range seen {
		if call.procedure == "AttachBus" {
			t.Error("agent called AttachBus")
		}
	}
	cancel()
	select {
	case err := <-done:
		done <- err
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// TestAnAgentThatCannotEnrollStopsThere. Without an identity there is nothing
// to carry on with: every other call is signed by the key enrollment
// registers, so an agent that logged this and continued would make one failing
// call after another with no way to recover. It is the one failure that ends
// the process.
func TestAnAgentThatCannotEnrollStopsThere(t *testing.T) {
	central := &orderedCentral{refuse: true}
	cfg := agentAgainst(t, central)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := host.Run(ctx, cfg, "v0-test", host.Options{})

	if code, _ := errs.CodeOf(err); code != identity.ErrCodeEnroll {
		t.Fatalf("Run() code = %v (err %v), want %v", code, err, identity.ErrCodeEnroll)
	}
	if procedures := central.seen(); len(procedures) != 1 || procedures[0].procedure != "Enroll" {
		t.Errorf("central saw %v, want the run to stop at Enroll", procedures)
	}
}

// An empty enrollment anchor set is refused before building the signed client.
func TestAnEdgeWithNoAnchorsIsRefusedAtStart(t *testing.T) {
	central := &orderedCentral{anchors: [][]byte{}}
	cfg := agentAgainst(t, central)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := host.Run(ctx, cfg, "v0-test", host.Options{})
	if code, _ := errs.CodeOf(err); code != host.ErrCodeStart {
		t.Fatalf("Run = %v, want agent/start", err)
	}
	if seen := central.seen(); len(seen) != 1 || seen[0].procedure != "Enroll" {
		t.Errorf("calls = %v, want only Enroll", seen)
	}
}

func TestHostKeepsSyslogFlowingDuringTelemetryOutage(t *testing.T) {
	central := &orderedCentral{ingest: make(chan *ingestv1.IngestRecord, 1), telemetry: make(chan struct{}, 1)}
	cfg := agentAgainst(t, central)
	ingest, telemetry, err := host.OpenDeliveryLogsForTest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	opts := syslogsource.ReceiverOptions()
	opts.Parse.Limits = opts.Limits
	parser, err := syslog.NewParser(opts.Parse)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.Parse([]byte("<34>1 - - - - - - device record"), syslog.Observation{ReceivedAt: time.Now(), Peer: netip.MustParseAddrPort("192.0.2.1:514"), Transport: syslog.UDP})
	if err != nil {
		t.Fatal(err)
	}
	const deviceID = "0192e6a0-0000-7000-8000-000000000001"
	record, provenance, _ := syslogsource.MapRecord(parsed, lanehost.DeviceEntry{
		Device:  inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(deviceID)}.Build()}.Build(),
		Binding: inventoryv1.BindingGlobalRef_builder{Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-000000000002")}.Build()}.Build(),
	}, edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build()}.Build())
	envelope := syslogsource.BuildEnvelope(deviceID, provenance, record, nil)
	if err := protovalidate.Validate(envelope); err != nil {
		t.Fatal(err)
	}
	data, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := ingest.Append(data); err != nil {
		t.Fatal(err)
	}
	export := telemetryv1.TelemetryExport_builder{Signal: telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_METRICS.Enum(), Body: []byte{10, 0}}.Build()
	if err := protovalidate.Validate(export); err != nil {
		t.Fatal(err)
	}
	data, err = proto.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	if err := telemetry.Append(data); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ingest.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	spawn.Go(ctx, "agent", func() { done <- host.Run(ctx, cfg, "v0-test", host.Options{Logger: slog.New(slog.DiscardHandler)}) })
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not stop")
		}
	}()
	select {
	case <-central.telemetry:
	case <-time.After(5 * time.Second):
		t.Fatal("telemetry handler not called")
	}
	select {
	case got := <-central.ingest:
		if !proto.Equal(got, envelope) {
			t.Errorf("delivered record = %v, want %v", got, envelope)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("syslog record did not reach ingest during telemetry outage")
	}
}
