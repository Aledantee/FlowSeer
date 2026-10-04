package host

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/policy/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	agentv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/agent/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

const (
	syslogTestEdgeID    = "0192e6a0-0000-7000-8000-0000000000e1"
	syslogTestDeviceID  = "0192e6a0-0000-7000-8000-0000000000d1"
	syslogTestBindingID = "0192e6a0-0000-7000-8000-0000000000b1"
)

// seedListing applies every row of one listing to the index in one call, as
// the onboarder does after central answers, with no lane involved.
func seedListing(t *testing.T, index *lanehost.DeviceIndex, rows ...*attachv1.ListedDevice) {
	t.Helper()
	for _, row := range rows {
		if err := protovalidate.Validate(row); err != nil {
			t.Fatalf("listed device %s: %v", row.GetDeviceId(), err)
		}
	}
	index.ApplyListing(rows)
}

// syslogHostConfig is an agent configuration naming the given listeners and
// raw policy, built directly because the file loader is not what these tests
// exercise.
func syslogHostConfig(raw *agentv1.AgentSyslog_builder, addresses ...string) *Config {
	listeners := make([]*agentv1.AgentSyslogListener, len(addresses))
	for i, address := range addresses {
		listeners[i] = agentv1.AgentSyslogListener_builder{Address: proto.String(address)}.Build()
	}
	raw.Listeners = listeners
	return &Config{msg: agentv1.AgentConfig_builder{Syslog: raw.Build()}.Build()}
}

// freeUDPAddress returns a loopback address with a port nothing holds. The
// source binds it a moment later, which a test that cannot read the bound
// address back out of the assembly has no way around.
func freeUDPAddress(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a UDP port: %v", err)
	}
	defer func() { _ = conn.Close() }()
	return conn.LocalAddr().String()
}

type publication struct {
	subject string
	data    []byte
	msgID   string
}

// recordingPublisher stands in for the edge buffer's leaf node and remembers
// what the syslog module published to it.
type recordingPublisher struct {
	published chan publication
}

func newRecordingPublisher() *recordingPublisher {
	return &recordingPublisher{published: make(chan publication, 16)}
}

func (p *recordingPublisher) Subject(name string) string { return "test." + name }

func (p *recordingPublisher) Publish(_ context.Context, subject string, data []byte, msgID string) error {
	p.published <- publication{subject: subject, data: data, msgID: msgID}
	return nil
}

func (p *recordingPublisher) next(t *testing.T) (publication, *ingestv1.IngestRecord) {
	t.Helper()
	select {
	case pub := <-p.published:
		record := &ingestv1.IngestRecord{}
		if err := proto.Unmarshal(pub.data, record); err != nil {
			t.Fatalf("published data is not an IngestRecord: %v", err)
		}
		return pub, record
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the syslog module to publish")
		return publication{}, nil
	}
}

func TestSyslogReceiverInstruments_NameUnitAndStatistic(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")

	// Each statistic carries its own value, so an instrument reading the wrong
	// one is a different number rather than a coincidence of zeros.
	stats := syslog.Stats{
		Received:           1,
		Oversized:          2,
		FramingErrors:      3,
		UDPDropped:         4,
		PressureClosed:     5,
		ConnectionRejected: 6,
	}
	if err := registerSyslogReceiverInstruments(meter, func() syslog.Stats { return stats }); err != nil {
		t.Fatalf("registerSyslogReceiverInstruments: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("reader.Collect: %v", err)
	}

	type observed struct {
		unit  string
		value int64
	}
	got := make(map[string]observed)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) != 1 {
				t.Errorf("%s is not a one-point integer sum: %T", m.Name, m.Data)
				continue
			}
			got[m.Name] = observed{unit: m.Unit, value: sum.DataPoints[0].Value}
		}
	}

	want := map[string]observed{
		"flowseer.edge.syslog.receiver.received":             {unit: "{frame}", value: 1},
		"flowseer.edge.syslog.receiver.oversized":            {unit: "{frame}", value: 2},
		"flowseer.edge.syslog.receiver.framing_errors":       {unit: "{frame}", value: 3},
		"flowseer.edge.syslog.receiver.udp_dropped":          {unit: "{datagram}", value: 4},
		"flowseer.edge.syslog.receiver.pressure_closed":      {unit: "{connection}", value: 5},
		"flowseer.edge.syslog.receiver.connections_rejected": {unit: "{connection}", value: 6},
	}
	if len(got) != len(want) {
		t.Errorf("registered %d instruments, want %d: %v", len(got), len(want), got)
	}
	for name, w := range want {
		if g, ok := got[name]; !ok || g != w {
			t.Errorf("%s = %+v (registered %t), want %+v", name, g, ok, w)
		}
	}
}

// runModule runs one module under the real runtime with its setup replaced by
// a stub, and reports whether the runtime set it up. The module's gate and name
// are the declaration under test; the stub keeps the runtime from binding
// anything.
func runModule(t *testing.T, module service.Module) (setUp bool, err error) {
	t.Helper()

	started := make(chan struct{})
	var once sync.Once
	module.Leaf = &service.Leaf{Setup: func(context.Context) (service.Attempt, error) {
		return service.Attempt{Runner: func(ctx context.Context) error {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return nil
		}}, nil
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	spawn.Go(ctx, "run module", func() {
		done <- service.Run(ctx, service.Config{
			Identity: service.Identity{Name: serviceName, Namespace: serviceNamespace, Version: "v0-test"},
			Logger:   slog.New(slog.DiscardHandler),
			Modules:  []service.Module{module},
		})
	})

	select {
	case <-started:
		cancel()
		<-done
		return true, nil
	case err := <-done:
		return false, err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the module to start or the runtime to refuse it")
		return false, nil
	}
}

func TestModules_SyslogGateFollowsTheConfiguredListeners(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     *Config
		wantRun bool
	}{
		{name: "no syslog block", cfg: &Config{msg: &agentv1.AgentConfig{}}, wantRun: false},
		{
			name:    "one listener",
			cfg:     syslogHostConfig(&agentv1.AgentSyslog_builder{}, "127.0.0.1:514"),
			wantRun: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mods := modules(&assembly{}, &captureAssembly{}, &syslogAssembly{cfg: tt.cfg})
			setUp, err := runModule(t, mods[2])
			if setUp != tt.wantRun {
				t.Fatalf("syslog module set up = %t (runtime error %v), want %t", setUp, err, tt.wantRun)
			}
			if !tt.wantRun {
				if code, _ := errs.CodeOf(err); code.String() != "service/empty-effective-tree" {
					t.Errorf("runtime error code = %v (err %v), want the empty tree refusal", code, err)
				}
			}
		})
	}
}

func TestSyslogAssembly_SetupPublishesAnIngestRecordNamingThisEdge(t *testing.T) {
	t.Parallel()

	address := freeUDPAddress(t)
	cfg := syslogHostConfig(&agentv1.AgentSyslog_builder{
		RawFailuresPerMinute: proto.Uint32(1),
		RawSampleEvery:       proto.Uint32(1000),
	}, address)

	index := lanehost.NewDeviceIndex()
	loopback := netip.MustParseAddr("127.0.0.1").As4()
	seedListing(t, index, attachv1.ListedDevice_builder{
		DeviceId:  proto.String(syslogTestDeviceID),
		BindingId: proto.String(syslogTestBindingID),
		Ip:        addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: loopback[:]}.Build()}.Build(),
		AccessPolicy: policyv1.AccessPolicyHandle_builder{
			Key: proto.String("icx7150-lab"), Version: proto.Uint64(3),
		}.Build(),
	}.Build())
	publisher := newRecordingPublisher()
	fixed := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	lane := &assembly{cfg: cfg, opts: Options{Clock: func() time.Time { return fixed }}, edgeID: syslogTestEdgeID, index: index}
	sa := syslogAssemblyFor(lane, publisher)

	attempt, err := sa.setup(context.Background())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if attempt.Runner == nil {
		t.Fatal("setup returned an attempt with no runner")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	spawn.Go(ctx, "syslog runner", func() { done <- attempt.Runner(ctx) })

	conn, err := net.Dial("udp", address)
	if err != nil {
		t.Fatalf("dial the listener: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// One message that parses, then two that do not. The configured policy
	// keeps raw evidence for one failure a minute, so the second failure
	// carries none; the defaults of twenty would keep both.
	garbage := []byte("not a syslog message")
	for _, payload := range [][]byte{
		[]byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - link down"),
		garbage,
		garbage,
	} {
		if _, err := conn.Write(payload); err != nil {
			t.Fatalf("send a datagram: %v", err)
		}
	}

	wantRaw := []bool{false, true, false}
	for i, wantRaw := range wantRaw {
		pub, record := publisher.next(t)
		if pub.subject != "test.ingest.syslog" {
			t.Errorf("record %d subject = %q, want the ingest.syslog subject the leaf names", i, pub.subject)
		}
		if pub.msgID != record.GetRecordId() {
			t.Errorf("record %d bus message id = %q, want the record id %q", i, pub.msgID, record.GetRecordId())
		}
		if got := record.GetProvenance().GetEdge().GetEdge().GetId(); got != syslogTestEdgeID {
			t.Errorf("record %d names edge %q, want %q", i, got, syslogTestEdgeID)
		}
		if got := record.GetProvenance().GetBinding().GetBinding().GetId(); got != syslogTestBindingID {
			t.Errorf("record %d binding = %q, want the indexed device's %q", i, got, syslogTestBindingID)
		}
		if got := record.GetSyslog().GetDevice().GetDevice().GetId(); got != syslogTestDeviceID {
			t.Errorf("record %d device = %q, want the indexed device's %q", i, got, syslogTestDeviceID)
		}
		if record.HasRaw() != wantRaw {
			t.Fatalf("record %d has raw evidence = %t, want %t", i, record.HasRaw(), wantRaw)
		}
		if wantRaw {
			raw := record.GetRaw()
			if string(raw.GetData()) != string(garbage) || raw.GetReason() != ingestv1.RawReason_RAW_REASON_PARSE_FAILURE {
				t.Errorf("record %d raw = %v, want the datagram and the parse failure reason", i, raw)
			}
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runner: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the runner to return")
	}
}

func TestSyslogAssembly_SetupWithoutListenersSurfacesTheSourcesRefusal(t *testing.T) {
	t.Parallel()

	lane := &assembly{cfg: &Config{msg: &agentv1.AgentConfig{}}, edgeID: syslogTestEdgeID, index: lanehost.NewDeviceIndex()}
	attempt, err := syslogAssemblyFor(lane, newRecordingPublisher()).setup(context.Background())
	if err == nil {
		t.Fatalf("setup succeeded with a runner %t, want the source's refusal to run without a listener", attempt.Runner != nil)
	}
	if !strings.Contains(err.Error(), "at least one listener") {
		t.Errorf("setup error = %q, want the source's own refusal to run without a listener", err)
	}
}

func TestSyslogAssembly_SharesTheLaneOnboardersIndex(t *testing.T) {
	t.Parallel()

	lane := &assembly{cfg: &Config{msg: &agentv1.AgentConfig{}}, edgeID: syslogTestEdgeID, index: lanehost.NewDeviceIndex()}
	sa := syslogAssemblyFor(lane, newRecordingPublisher())

	if sa.index != lane.index {
		t.Error("the syslog module resolves senders through a different index than the lane holds")
	}
	onboard := lane.onboardConfig(nil, slog.New(slog.DiscardHandler))
	if onboard.Index != sa.index {
		t.Error("the lane's onboarder fills a different index than the syslog module reads")
	}
	if got := onboard.Edge.GetEdge().GetId(); got != syslogTestEdgeID {
		t.Errorf("the onboarder names edge %q, want %q", got, syslogTestEdgeID)
	}
}
