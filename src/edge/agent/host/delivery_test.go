package host

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/signal"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/drain"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/otlpreceiver"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/seglog"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/syslogsource"
	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

type faultingDeliveryLog struct {
	drain.Log
	operation string
	recovered atomic.Bool
	failures  atomic.Int64
}

func (l *faultingDeliveryLog) fail(operation string) error {
	if operation == l.operation && !l.recovered.Load() {
		l.failures.Add(1)
		return errs.New().Code(seglog.ErrCodeIO).Msg("storage unavailable")
	}
	return nil
}

func (l *faultingDeliveryLog) Next(ctx context.Context) (seglog.Segment, error) {
	if err := l.fail("next"); err != nil {
		return seglog.Segment{}, err
	}
	return l.Log.Next(ctx)
}

func (l *faultingDeliveryLog) Ack(sequence uint64) error {
	if err := l.fail("ack"); err != nil {
		return err
	}
	return l.Log.Ack(sequence)
}

func TestDeliveryPanicStaysInItsBranch(t *testing.T) {
	_, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()
	synctest.Test(t, func(t *testing.T) {
		telemetry, err := seglog.Open(seglog.Config{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = telemetry.Close() }()
		ingest, err := seglog.Open(seglog.Config{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ingest.Close() }()
		if err := telemetry.Append([]byte("queued")); err != nil {
			t.Fatal(err)
		}
		var panics atomic.Int64
		failing := deliveryAssembly{name: "telemetry", log: telemetry, send: func(context.Context, [][]byte) (int, error) {
			panics.Add(1)
			panic("delivery panic")
		}}
		delivered := make(chan struct{}, 1)
		other := deliveryAssembly{name: "ingest", log: ingest, send: func(_ context.Context, records [][]byte) (int, error) {
			delivered <- struct{}{}
			return len(records), nil
		}}
		reader := sdkmetric.NewManualReader()
		provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
		defer func() { _ = provider.Shutdown(context.Background()) }()
		cfg := service.Config{
			Identity: service.Identity{Name: "agent", Namespace: "test", Version: "test"},
			Logger:   slog.New(slog.DiscardHandler), MeterProvider: provider,
			Modules: []service.Module{failing.module(), other.module()},
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		var runErr error
		spawn.Go(ctx, "test panicking delivery", func() { defer close(done); runErr = service.Run(ctx, cfg) })
		defer func() { cancel(); <-done }()
		time.Sleep(4 * time.Minute)
		synctest.Wait()
		select {
		case <-done:
			t.Fatalf("service.Run ended during persistent drain panic: %v", runErr)
		default:
		}
		if got := panics.Load(); got < 8 {
			t.Fatalf("send panics = %d, want at least 8 supervised attempts", got)
		}
		if err := ingest.Append([]byte("independent")); err != nil {
			t.Fatal(err)
		}
		select {
		case <-delivered:
		case <-time.After(time.Second):
			t.Fatal("ingest did not deliver during telemetry panic")
		}
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(ctx, &rm); err != nil {
			t.Fatal(err)
		}
		var restarts, branchRestarts int64
		for _, scope := range rm.ScopeMetrics {
			for _, m := range scope.Metrics {
				if m.Name != "flowseer.service.module.lifecycle.transitions" {
					continue
				}
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok {
					continue
				}
				for _, dp := range sum.DataPoints {
					path, _ := dp.Attributes.Value("flowseer.module.path")
					action, _ := dp.Attributes.Value("flowseer.module.lifecycle.action")
					outcome, _ := dp.Attributes.Value("flowseer.module.lifecycle.outcome")
					if action.AsString() != "restart" {
						continue
					}
					if path.AsString() == "agent/delivery_telemetry/drain" && outcome.AsString() == "panic" {
						restarts += dp.Value
					}
					if path.AsString() == "agent/delivery_telemetry" {
						branchRestarts += dp.Value
					}
				}
			}
		}
		if restarts < 8 {
			t.Errorf("recorded panic restarts = %d, want at least 8", restarts)
		}
		if branchRestarts != 0 {
			t.Errorf("telemetry branch restarts = %d, want 0", branchRestarts)
		}
		cancel()
		<-done
		readCtx, stopRead := context.WithTimeout(context.Background(), time.Second)
		defer stopRead()
		segment, err := telemetry.Next(readCtx)
		if err != nil || len(segment.Records) != 1 || string(segment.Records[0]) != "queued" {
			t.Fatalf("segment after drain panics = %+v, %v, want the queued record still readable", segment, err)
		}
	})
}

type countingDeliveryLog struct {
	drain.Log
	reads atomic.Int64
}

func (l *countingDeliveryLog) Stats() (seglog.Stats, error) {
	l.reads.Add(1)
	return seglog.Stats{}, nil
}

func TestDeliveryRestartKeepsOneBufferCallback(t *testing.T) {
	_, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()

	synctest.Test(t, func(t *testing.T) {
		buffer, err := seglog.Open(seglog.Config{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = buffer.Close() }()
		fault := &faultingDeliveryLog{Log: buffer, operation: "next"}
		log := &countingDeliveryLog{Log: fault}
		a := deliveryAssembly{name: "telemetry", log: log, send: func(_ context.Context, records [][]byte) (int, error) {
			return len(records), nil
		}}
		reader := sdkmetric.NewManualReader()
		provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
		defer func() { _ = provider.Shutdown(context.Background()) }()
		cfg := service.Config{
			Identity: service.Identity{Name: "agent", Namespace: "test", Version: "test"},
			Logger:   slog.New(slog.DiscardHandler), MeterProvider: provider,
			Modules: []service.Module{a.module()},
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		var runErr error
		spawn.Go(ctx, "test restarted delivery", func() { defer close(done); runErr = service.Run(ctx, cfg) })
		defer func() { cancel(); <-done }()
		time.Sleep(31 * time.Second)
		synctest.Wait()
		if got := fault.failures.Load(); got < 2 {
			t.Fatalf("storage failures = %d, want at least 2 supervised attempts", got)
		}
		fault.recovered.Store(true)
		time.Sleep(31 * time.Second)
		synctest.Wait()
		select {
		case <-done:
			t.Fatalf("service.Run ended after drain restart: %v", runErr)
		default:
		}
		before := log.reads.Load()
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(ctx, &rm); err != nil {
			t.Fatal(err)
		}
		if got := log.reads.Load() - before; got != 1 {
			t.Errorf("stats reads per collection after restart = %d, want 1", got)
		}
	})
}

func TestDeliveryCanceledBeforeRunnerRegistersNoBufferCallback(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &countingDeliveryLog{}
	a := deliveryAssembly{name: "telemetry", log: log}
	var setups, runners atomic.Int64
	module := service.Module{Name: "delivery", Leaf: &service.Leaf{Setup: func(ctx context.Context) (service.Attempt, error) {
		attempt, err := a.setup(ctx)
		if err != nil {
			return service.Attempt{}, err
		}
		setups.Add(1)
		runner := attempt.Runner
		attempt.Runner = func(ctx context.Context) error {
			runners.Add(1)
			return runner(ctx)
		}
		cancel()
		return attempt, nil
	}}}
	if err := service.Run(ctx, service.Config{
		Identity: service.Identity{Name: "agent", Namespace: "test", Version: "test"},
		Logger:   slog.New(slog.DiscardHandler), MeterProvider: provider,
		Modules: []service.Module{module},
	}); err != nil {
		t.Fatal(err)
	}
	if got := setups.Load(); got != 1 {
		t.Fatalf("delivery setups = %d, want 1", got)
	}
	if got := runners.Load(); got != 0 {
		t.Fatalf("delivery runner starts = %d, want 0", got)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	if got := log.reads.Load(); got != 0 {
		t.Errorf("stats reads after skipped runner = %d, want 0", got)
	}
}

func TestDeliveryStorageFaultStaysInItsBranch(t *testing.T) {
	// Initialize the process-wide signal dispatcher outside the fake-clock bubble.
	_, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()
	for _, tc := range []struct{ name, operation string }{
		{"ingest", "next"},
		{"ingest", "ack"},
		{"telemetry", "next"},
		{"telemetry", "ack"},
	} {
		t.Run(tc.name+"/"+tc.operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				broken, err := seglog.Open(seglog.Config{Dir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = broken.Close() }()
				healthy, err := seglog.Open(seglog.Config{Dir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = healthy.Close() }()
				if err := broken.Append([]byte("queued")); err != nil {
					t.Fatal(err)
				}
				fault := &faultingDeliveryLog{Log: broken, operation: tc.operation}
				otherName := "telemetry"
				if tc.name == "telemetry" {
					otherName = "ingest"
				}
				delivered := make(chan struct{}, 1)
				failing := deliveryAssembly{name: tc.name, log: fault, send: func(_ context.Context, records [][]byte) (int, error) {
					return len(records), nil
				}}
				other := deliveryAssembly{name: otherName, log: healthy, send: func(_ context.Context, records [][]byte) (int, error) {
					delivered <- struct{}{}
					return len(records), nil
				}}
				reader := sdkmetric.NewManualReader()
				provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
				defer func() { _ = provider.Shutdown(context.Background()) }()
				var sentinelStarts atomic.Int64
				cfg := service.Config{
					Identity: service.Identity{Name: "agent", Namespace: "test", Version: "test"},
					Logger:   slog.New(slog.DiscardHandler), MeterProvider: provider,
					Modules: []service.Module{failing.module(), other.module(), {
						Name: "sentinel", Leaf: &service.Leaf{Setup: func(context.Context) (service.Attempt, error) {
							sentinelStarts.Add(1)
							return service.Attempt{Runner: func(ctx context.Context) error { <-ctx.Done(); return nil }}, nil
						}},
					}},
				}
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				var runErr error
				spawn.Go(ctx, "test supervised delivery", func() { defer close(done); runErr = service.Run(ctx, cfg) })
				defer func() { cancel(); <-done }()
				time.Sleep(4 * time.Minute)
				synctest.Wait()
				select {
				case <-done:
					t.Fatalf("service.Run ended during persistent storage fault: %v", runErr)
				default:
				}
				if got := fault.failures.Load(); got < 8 {
					t.Fatalf("storage failures = %d, want at least 8 supervised attempts", got)
				}
				if err := healthy.Append([]byte("independent")); err != nil {
					t.Fatal(err)
				}
				select {
				case <-delivered:
				case <-time.After(time.Second):
					t.Fatal("other drain did not deliver during storage fault")
				}
				if got := sentinelStarts.Load(); got != 1 {
					t.Errorf("sentinel starts = %d, want 1", got)
				}
				var rm metricdata.ResourceMetrics
				if err := reader.Collect(ctx, &rm); err != nil {
					t.Fatal(err)
				}
				var restarts int64
				for _, scope := range rm.ScopeMetrics {
					for _, m := range scope.Metrics {
						if m.Name != "flowseer.service.module.lifecycle.transitions" {
							continue
						}
						sum, ok := m.Data.(metricdata.Sum[int64])
						if !ok {
							continue
						}
						for _, dp := range sum.DataPoints {
							path, _ := dp.Attributes.Value("flowseer.module.path")
							action, _ := dp.Attributes.Value("flowseer.module.lifecycle.action")
							outcome, _ := dp.Attributes.Value("flowseer.module.lifecycle.outcome")
							if path.AsString() == "agent/delivery_"+tc.name+"/drain" && action.AsString() == "restart" && outcome.AsString() == "error" {
								restarts += dp.Value
							}
						}
					}
				}
				if restarts < 8 {
					t.Errorf("recorded error restarts = %d, want at least 8", restarts)
				}
				fault.recovered.Store(true)
				time.Sleep(31 * time.Second)
				synctest.Wait()
				stats, err := broken.Stats()
				if err != nil || stats.Records != 0 {
					t.Errorf("recovered log stats = %+v, %v, want empty", stats, err)
				}
			})
		})
	}
}

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
		attempt, err := a.setup(ctx)
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
					if v, ok := dp.Attributes.Value(drain.LogKey); ok && v.AsString() == name {
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
				if v, _ := point.Attributes.Value(drain.LogKey); v.AsString() != "ingest" {
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
				if v, _ := point.Attributes.Value(drain.LogKey); v.AsString() != "ingest" {
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
