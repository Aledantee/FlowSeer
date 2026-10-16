package host

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1/ingestv1connect"
	telemetryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1/telemetryv1connect"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	agentv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/agent/v1"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/drain"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/otlpreceiver"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/seglog"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/syslogsource"
	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

type ingestHandler struct {
	ingestv1connect.UnimplementedIngestServiceHandler
	received chan string
}

func (h ingestHandler) Deliver(_ context.Context, r *connect.Request[edgeingestv1.DeliverRequest]) (*connect.Response[edgeingestv1.DeliverResponse], error) {
	for _, rec := range r.Msg.GetRecords() {
		if err := protovalidate.Validate(rec); err != nil {
			return nil, err
		}
		h.received <- rec.GetRecordId()
	}
	response := edgeingestv1.DeliverResponse_builder{Settled: proto.Uint32(uint32(len(r.Msg.GetRecords())))}.Build()
	if err := protovalidate.Validate(response); err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

type telemetryHandler struct {
	telemetryv1connect.UnimplementedTelemetryServiceHandler
	called chan struct{}
}

func (h telemetryHandler) Deliver(context.Context, *connect.Request[telemetryv1.DeliverRequest]) (*connect.Response[telemetryv1.DeliverResponse], error) {
	select {
	case h.called <- struct{}{}:
	default:
	}
	response := telemetryv1.DeliverResponse_builder{Settled: proto.Uint32(0)}.Build()
	if err := protovalidate.Validate(response); err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func TestDeliveryModulesKeepIngestFlowingDuringTelemetryOutage(t *testing.T) {
	cfg := &Config{msg: agentv1.AgentConfig_builder{StateDir: proto.String(t.TempDir()), ProvisioningPath: proto.String("/unused/provisioning.textproto")}.Build()}
	if err := protovalidate.Validate(cfg.msg); err != nil {
		t.Fatal(err)
	}
	ingest, telemetry, err := openDeliveryLogs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ingest.Close(); _ = telemetry.Close() }()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	reg, err := registerBufferInstruments(provider.Meter("test"), "telemetry", telemetry.Stats)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Unregister() }()
	received, called := make(chan string, 1), make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.Handle(ingestv1connect.NewIngestServiceHandler(ingestHandler{received: received}))
	mux.Handle(telemetryv1connect.NewTelemetryServiceHandler(telemetryHandler{called: called}))
	server := httptest.NewServer(mux)
	defer server.Close()
	export := telemetryv1.TelemetryExport_builder{Signal: telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_METRICS.Enum(), Body: []byte{10, 0}}.Build()
	if err := protovalidate.Validate(export); err != nil {
		t.Fatal(err)
	}
	body, err := proto.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	if err := telemetry.Append(body); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := 0
	done := make(chan error, 2)
	defer func() {
		cancel()
		for range started {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}()
	assemblies := []deliveryAssembly{
		{name: "ingest", log: ingest, send: drain.Ingest(ingestv1connect.NewIngestServiceClient(server.Client(), server.URL)), decodable: drain.IngestDecodable},
		{name: "telemetry", log: telemetry, send: drain.Telemetry(telemetryv1connect.NewTelemetryServiceClient(server.Client(), server.URL)), decodable: drain.TelemetryDecodable},
	}
	for _, a := range assemblies {
		attempt, err := a.module().Leaf.Setup(ctx)
		if err != nil {
			t.Fatal(err)
		}
		started++
		spawn.Go(ctx, "test delivery module", func() { done <- attempt.Runner(ctx) })
	}
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("telemetry handler not called")
	}
	if n := bufferRecordMetric(t, reader, "telemetry"); n != 1 {
		t.Fatalf("telemetry records = %d, want 1", n)
	}
	if err := telemetry.Append(body); err != nil {
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
	rec, prov, _ := syslogsource.MapRecord(parsed, lanehost.DeviceEntry{
		Device:  inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(syslogTestDeviceID)}.Build()}.Build(),
		Binding: inventoryv1.BindingGlobalRef_builder{Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(syslogTestBindingID)}.Build()}.Build(),
	}, edgeRefOf(syslogTestEdgeID))
	envelope := syslogsource.BuildEnvelope(syslogTestDeviceID, prov, rec, nil)
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
	select {
	case id := <-received:
		if id != syslogTestDeviceID {
			t.Errorf("record id = %s", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ingest blocked behind telemetry")
	}
	if n := bufferRecordMetric(t, reader, "telemetry"); n != 2 {
		t.Errorf("telemetry records = %d, want 2", n)
	}
}

func bufferRecordMetric(t *testing.T, reader *sdkmetric.ManualReader, name string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "flowseer.edge.buffer.records" {
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok || sum.IsMonotonic {
					t.Fatalf("records data = %T, want up-down counter", m.Data)
				}
				for _, dp := range sum.DataPoints {
					if v, ok := dp.Attributes.Value("flowseer.edge.buffer.log"); ok && v.AsString() == name {
						return dp.Value
					}
				}
			}
		}
	}
	t.Fatal("buffer record metric absent")
	return 0
}

func TestTelemetryReceiverPrecedesTheRuntimeExport(t *testing.T) {
	log, err := seglog.Open(seglog.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	receiver, err := otlpreceiver.Start(log)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = receiver.Close(context.Background()) }()
	cfg := runtimeConfig("v0-test", slog.New(slog.DiscardHandler), receiver.Endpoint(), nil)
	if cfg.Telemetry.Endpoint != receiver.Endpoint() {
		t.Fatalf("endpoint = %q, want %q", cfg.Telemetry.Endpoint, receiver.Endpoint())
	}
	resp, err := http.Post(cfg.Telemetry.Endpoint+"/v1/metrics", "application/x-protobuf", bytes.NewReader([]byte{10, 0}))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	stats, err := log.Stats()
	if err != nil || stats.Records != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	segment, err := log.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	export := new(telemetryv1.TelemetryExport)
	if err := proto.Unmarshal(segment.Records[0], export); err != nil {
		t.Fatal(err)
	}
	if err := protovalidate.Validate(export); err != nil {
		t.Fatal(err)
	}
	if export.GetSignal() != telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_METRICS || !bytes.Equal(export.GetBody(), []byte{10, 0}) {
		t.Fatalf("export = %v", export)
	}
}

func TestBufferInstrumentsReadTheLogSnapshot(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	registration, err := registerBufferInstruments(provider.Meter("test"), "ingest", func() (seglog.Stats, error) {
		return seglog.Stats{Records: 3, Bytes: 4096, OldestAge: 2500 * time.Millisecond, DiscardedRecords: 7, CorruptSegments: 2}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registration.Unregister() }()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		unit      string
		value     float64
		monotonic bool
	}{
		"flowseer.edge.buffer.records":    {"{record}", 3, false},
		"flowseer.edge.buffer.size":       {"By", 4096, false},
		"flowseer.edge.buffer.oldest_age": {"s", 2.5, false},
		"flowseer.edge.buffer.discarded":  {"{record}", 7, true},
		"flowseer.edge.buffer.corrupt":    {"{segment}", 2, true},
	}
	found := 0
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			expected, ok := want[m.Name]
			if !ok {
				continue
			}
			found++
			if m.Unit != expected.unit {
				t.Errorf("%s unit = %s, want %s", m.Name, m.Unit, expected.unit)
			}
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				if data.IsMonotonic != expected.monotonic || len(data.DataPoints) != 1 {
					t.Errorf("%s data = %+v", m.Name, data)
					continue
				}
				point := data.DataPoints[0]
				if float64(point.Value) != expected.value {
					t.Errorf("%s = %d, want %v", m.Name, point.Value, expected.value)
				}
				if v, _ := point.Attributes.Value("flowseer.edge.buffer.log"); v.AsString() != "ingest" {
					t.Errorf("%s log = %v", m.Name, v)
				}
			case metricdata.Gauge[float64]:
				if m.Name != "flowseer.edge.buffer.oldest_age" || len(data.DataPoints) != 1 {
					t.Errorf("unexpected gauge %s", m.Name)
					continue
				}
				point := data.DataPoints[0]
				if point.Value != 2.5 {
					t.Errorf("oldest_age = %v, want 2.5", point.Value)
				}
				if v, _ := point.Attributes.Value("flowseer.edge.buffer.log"); v.AsString() != "ingest" {
					t.Errorf("age log = %v", v)
				}
			default:
				t.Errorf("%s data = %T", m.Name, m.Data)
			}
		}
	}
	if found != 5 {
		t.Errorf("buffer instruments = %d, want 5", found)
	}
}
