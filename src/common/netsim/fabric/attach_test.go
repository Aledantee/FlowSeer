package fabric_test

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/pcap"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func captureRecords(t *testing.T) []pcap.Record {
	t.Helper()
	wire, err := os.ReadFile("../../net/pcap/testdata/classic_micro_le.pcap")
	if err != nil {
		t.Fatal(err)
	}
	r, err := pcap.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	var records []pcap.Record
	for {
		record, err := r.Next()
		if err == io.EOF {
			return records
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
}

func TestAttachCapturePreservesSpacingAndMACs(t *testing.T) {
	records := captureRecords(t)
	source, err := stream.NewCaptureSource(records)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	fab, _, _ := newTwoSwitchTopology(t, fabric.Fault{})
	cfg := fab.Config()
	cfg.Start = t0
	sw1 := cfg.Switches["sw1"]
	sw1.MAC = netaddr.MAC{2, 0xff, 0, 0, 0, 1}
	cfg.Switches["sw1"] = sw1
	sw2 := cfg.Switches["sw2"]
	sw2.MAC = netaddr.MAC{2, 0xff, 0, 0, 0, 2}
	cfg.Switches["sw2"] = sw2
	h1 := cfg.Hosts["h1"]
	h1.Address = netaddr.MAC{2, 0xaa, 0, 0, 0, 1}
	cfg.Hosts["h1"] = h1
	h2 := cfg.Hosts["h2"]
	h2.Address = netaddr.MAC{2, 0, 0, 0, 0, 2}
	cfg.Hosts["h2"] = h2
	fab, err = fabric.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := fab.AttachStream(fabric.StreamAttachment{
		Origin: fabric.Endpoint{Node: "h1"}, Source: source, Start: 0, Flow: 1, Retention: fabric.RetainJourney,
	}); err != nil {
		t.Fatal(err)
	}
	if result := fab.Run(1_000); result.Stop != fabric.StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", result.Stop, fabric.StopQueueDrained)
	}
	journeys := fab.Report()
	if len(journeys) != 2 {
		t.Fatalf("reported frames = %d, want 2", len(journeys))
	}
	for i, journey := range journeys {
		wantAt := t0.Add(time.Duration(i) * 250 * time.Microsecond)
		if !journey.Injection.At.Equal(wantAt) {
			t.Errorf("frame %d injection = %s, want %s", i, journey.Injection.At, wantAt)
		}
		var wantSrc, wantDst netaddr.MAC
		copy(wantDst[:], records[i].Data[:6])
		copy(wantSrc[:], records[i].Data[6:12])
		if wantSrc == h1.Address {
			t.Fatalf("frame %d captured source %s equals h1 address; source rewrites would go undetected", i, wantSrc)
		}
		if journey.Injection.Frame.Src != wantSrc || journey.Injection.Frame.Dst != wantDst {
			t.Errorf("frame %d MACs = %s -> %s, want %s -> %s", i,
				journey.Injection.Frame.Src, journey.Injection.Frame.Dst, wantSrc, wantDst)
		}
		if len(journey.Deliveries) != 1 {
			t.Errorf("frame %d deliveries = %d, want 1", i, len(journey.Deliveries))
			continue
		}
		// Injection.Frame is the submitted input; delivery holds the frame h2 received.
		delivered := journey.Deliveries[0].Frame
		if delivered.Src != wantSrc || delivered.Dst != wantDst {
			t.Errorf("frame %d delivered MACs = %s -> %s, want %s -> %s", i,
				delivered.Src, delivered.Dst, wantSrc, wantDst)
		}
	}
}

func TestTruncatedCaptureRejectedBeforeAttachment(t *testing.T) {
	records := captureRecords(t)
	records[0].OrigLen = 64
	source, err := stream.NewCaptureSource(records)
	if err == nil || !strings.Contains(err.Error(), "original length") {
		t.Fatalf("NewCaptureSource = %v, %v; want original-length error", source, err)
	}
	if source != nil {
		t.Errorf("NewCaptureSource returned %v with an error, want no attachable source", source)
	}
}

func streamSource(t *testing.T, frame ethernet.Frame, count int, fps uint64) stream.Source {
	t.Helper()
	source, err := (stream.Spec{Frame: frame, Rate: stream.Rate{FramesPerSecond: fps}, Count: count}).Source()
	if err != nil {
		t.Fatalf("stream source: %v", err)
	}
	return source
}

func TestAttachStreamPlacesFramesAtFabricEpoch(t *testing.T) {
	t0 := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	cfg := fab.Config()
	cfg.Start = t0
	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fab.AttachStream(fabric.StreamAttachment{
		Origin: fabric.Endpoint{Node: "h1"}, Source: streamSource(t, flowFrame(src, dst), 3, 10_000),
		Start: 7 * time.Millisecond, Flow: 1, Retention: fabric.RetainJourney,
	}); err != nil {
		t.Fatalf("AttachStream: %v", err)
	}
	if result := fab.Run(1000); result.Stop != fabric.StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", result.Stop, fabric.StopQueueDrained)
	}
	journeys := fab.Report()
	if len(journeys) != 3 {
		t.Fatalf("reported frames = %d, want 3", len(journeys))
	}
	for n, journey := range journeys {
		want := t0.Add(7*time.Millisecond + time.Duration(n)*100*time.Microsecond)
		if got := journey.Injection.At; !got.Equal(want) {
			t.Errorf("frame %d injection = %s, want %s", n, got, want)
		}
	}
}

func TestAttachStreamRefusals(t *testing.T) {
	t0 := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	frame := flowFrame(src, dst)
	base := fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "h1"}, Source: streamSource(t, frame, 1, 10_000), Flow: 1}
	tests := []struct {
		name string
		edit func(*fabric.StreamAttachment)
		want string
	}{
		{"nil source", func(a *fabric.StreamAttachment) { a.Source = nil }, "source"},
		{"zero flow", func(a *fabric.StreamAttachment) { a.Flow = 0 }, "flow"},
		{"invalid retention", func(a *fabric.StreamAttachment) { a.Retention = 9 }, "retention"},
		{"unknown node", func(a *fabric.StreamAttachment) { a.Origin.Node = "absent" }, "not found"},
		{"host port", func(a *fabric.StreamAttachment) { a.Origin.Port = "1/1/1" }, "empty port"},
		{"missing switch port", func(a *fabric.StreamAttachment) { a.Origin = fabric.Endpoint{Node: "sw1", Port: "missing"} }, "not found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			att := base
			tc.edit(&att)
			if err := fab.AttachStream(att); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("AttachStream error = %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("reflector origin", func(t *testing.T) {
		cfg := fab.Config()
		cfg.Reflectors = map[string]fabric.Reflector{"r1": {Address: netaddr.MAC{2, 0, 0, 0, 0, 10}}}
		withReflector, err := fabric.New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		att := base
		att.Origin = fabric.Endpoint{Node: "r1"}
		if err := withReflector.AttachStream(att); err == nil || !strings.Contains(err.Error(), "cannot originate") {
			t.Errorf("AttachStream error = %v, want reflector refusal", err)
		}
	})
	if _, err := fab.Inject(fabric.Injection{At: t0, Origin: base.Origin, Frame: frame}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if _, ok := fab.Step(); !ok {
		t.Fatal("Step did not advance the clock")
	}
	if err := fab.AttachStream(base); err == nil || !strings.Contains(err.Error(), "clock") {
		t.Errorf("AttachStream before clock error = %v, want clock refusal", err)
	}
}

func TestAttachedStreamEndsAtDownHostLink(t *testing.T) {
	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	if err := fab.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "h1"}, Source: streamSource(t, flowFrame(src, dst), 4, 10_000), Flow: 1, Retention: fabric.RetainJourney}); err != nil {
		t.Fatalf("AttachStream: %v", err)
	}
	for len(fab.Report()) < 2 {
		if _, ok := fab.Step(); !ok {
			t.Fatal("Step stopped before the second frame")
		}
	}
	if err := fab.SetFault(fabric.Endpoint{Node: "h1"}, fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, fabric.Fault{Kind: fabric.FaultCut}); err != nil {
		t.Fatalf("SetFault: %v", err)
	}
	fab.Run(1000)
	if got := len(fab.Report()); got != 2 {
		t.Errorf("reported frames = %d, want 2 before the cut", got)
	}
}

func TestAttachedStreamUnknownHostLinkIsUnresolved(t *testing.T) {
	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	cfg := fab.Config()
	cfg.PhyAssumption = nil
	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := fab.Snapshot().Links[0].A.Oper; got != port.Unknown {
		t.Fatalf("host link = %s, want Unknown", got)
	}
	if err := fab.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "h1"}, Source: streamSource(t, flowFrame(src, dst), 2, 10_000), Flow: 1, Retention: fabric.RetainJourney}); err != nil {
		t.Fatalf("AttachStream: %v", err)
	}
	fab.Run(100)
	if got := len(fab.Report()); got != 2 {
		t.Fatalf("reported frames = %d, want 2", got)
	}
	for n, journey := range fab.Report() {
		if len(journey.Entries) < 2 || journey.Entries[1].Kind != fabric.EntryUnresolved {
			t.Errorf("frame %d entries = %+v, want unresolved", n, journey.Entries)
		}
	}
}

func TestAttachedStreamFlowEqualsEagerInjection(t *testing.T) {
	lazy, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	eager, _, _ := newTwoSwitchTopology(t, fabric.Fault{})
	frame := flowFrame(src, dst)
	source := streamSource(t, frame, 8, 10_000)
	if err := lazy.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "h1"}, Source: source.Clone(), Flow: 7, Retention: fabric.RetainAggregate}); err != nil {
		t.Fatalf("AttachStream: %v", err)
	}
	for {
		at, next, ok := source.Next()
		if !ok {
			break
		}
		if _, err := eager.Inject(fabric.Injection{At: eager.Config().Start.Add(at), Origin: fabric.Endpoint{Node: "h1"}, Frame: next, Flow: 7, Retention: fabric.RetainAggregate}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
	}
	lazy.Run(1000)
	eager.Run(1000)
	if got, want := lazy.Flows(), eager.Flows(); !reflect.DeepEqual(got, want) {
		t.Error("attached and eager flow statistics differ")
		for id := range got {
			t.Errorf("flow %d: lazy offered=%d delivered=%v latency=%+v metadata=%v; eager offered=%d delivered=%v latency=%+v metadata=%v", id,
				got[id].Offered, got[id].Delivered, got[id].Latency, got[id].Metadata.Status(),
				want[id].Offered, want[id].Delivered, want[id].Latency, want[id].Metadata.Status())
		}
	}
}

func TestAttachedLagStreamsMatchEagerDeliveries(t *testing.T) {
	t0 := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	lagCfg := &lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.BalanceSLB}}}
	lazy, _, dst := newLagTopology(t, t0, lagCfg, lagCfg)
	eager, _, _ := newLagTopology(t, t0, lagCfg, lagCfg)
	type offered struct {
		at     time.Time
		stream int
		frame  ethernet.Frame
	}
	var frames []offered
	for i := range 4 {
		frame := flowFrame(netaddr.MAC{0x02, 0, 0, 0, 1, byte(i + 1)}, dst)
		source := streamSource(t, frame, 40, 1_000)
		if err := lazy.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "h1"}, Source: source.Clone(), Flow: fabric.FlowID(i + 1), Retention: fabric.RetainJourney}); err != nil {
			t.Fatalf("AttachStream %d: %v", i, err)
		}
		for {
			at, next, ok := source.Next()
			if !ok {
				break
			}
			frames = append(frames, offered{t0.Add(at), i, next})
		}
	}
	slices.SortFunc(frames, func(a, b offered) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return a.stream - b.stream
	})
	for _, item := range frames {
		if _, err := eager.Inject(fabric.Injection{At: item.at, Origin: fabric.Endpoint{Node: "h1"}, Frame: item.frame, Flow: fabric.FlowID(item.stream + 1), Retention: fabric.RetainJourney}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
	}
	lazy.Run(100_000)
	eager.Run(100_000)
	if got, want := lazy.Flows(), eager.Flows(); !reflect.DeepEqual(got, want) {
		t.Error("attached and eager LAG flow statistics differ")
		for id := range got {
			t.Errorf("flow %d: lazy offered=%d delivered=%v latency=%+v metadata=%v; eager offered=%d delivered=%v latency=%+v metadata=%v", id,
				got[id].Offered, got[id].Delivered, got[id].Latency, got[id].Metadata.Status(),
				want[id].Offered, want[id].Delivered, want[id].Latency, want[id].Metadata.Status())
		}
	}
	deliveries := func(f *fabric.Fabric) map[fabric.FlowID][]time.Time {
		out := make(map[fabric.FlowID][]time.Time)
		for _, journey := range f.Report() {
			for _, delivery := range journey.Deliveries {
				out[journey.Injection.Flow] = append(out[journey.Injection.Flow], delivery.At)
			}
		}
		for flow := range out {
			slices.SortFunc(out[flow], time.Time.Compare)
		}
		return out
	}
	lazyDeliveries, eagerDeliveries := deliveries(lazy), deliveries(eager)
	if got, want := lazyDeliveries, eagerDeliveries; !reflect.DeepEqual(got, want) {
		t.Error("attached and eager LAG delivery times differ")
		for id := range got {
			t.Errorf("flow %d: lazy first/last=%v/%v, eager first/last=%v/%v", id,
				got[id][0], got[id][len(got[id])-1], want[id][0], want[id][len(want[id])-1])
		}
	}
	for flow := fabric.FlowID(1); flow <= 4; flow++ {
		if got := len(lazyDeliveries[flow]); got != 40 {
			t.Errorf("flow %d deliveries = %d, want 40", flow, got)
		}
	}
}

func TestAttachedStreamStepKeepsClockMonotonic(t *testing.T) {
	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	start := fab.Config().Start
	frame := flowFrame(src, dst)
	if _, err := fab.Inject(fabric.Injection{At: start.Add(50 * time.Microsecond), Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Frame: frame}); err != nil {
		t.Fatalf("Inject switch: %v", err)
	}
	if _, err := fab.Inject(fabric.Injection{At: start.Add(time.Millisecond), Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatalf("Inject host: %v", err)
	}
	if err := fab.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "h1"}, Source: streamSource(t, frame, 1, 10_000), Flow: 1}); err != nil {
		t.Fatalf("AttachStream: %v", err)
	}
	var previous time.Time
	steps := 0
	for range 20 {
		entry, ok := fab.Step()
		if !ok {
			break
		}
		if entry.At.Before(previous) {
			t.Fatalf("Step time moved backward from %s to %s", previous, entry.At)
		}
		previous = entry.At
		steps++
	}
	if steps < 2 {
		t.Fatalf("Step processed %d arrivals, want at least two", steps)
	}
}

func TestMixedEagerAndAttachedHostDeliveriesMatchEager(t *testing.T) {
	attached, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	eager, _, _ := newTwoSwitchTopology(t, fabric.Fault{})
	start := attached.Config().Start
	frame := flowFrame(src, dst)
	switchInjection := fabric.Injection{At: start.Add(50 * time.Microsecond), Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Frame: frame}
	lateHost := fabric.Injection{At: start.Add(time.Millisecond), Origin: fabric.Endpoint{Node: "h1"}, Frame: frame, Flow: 2}
	streamHost := fabric.Injection{At: start.Add(200 * time.Microsecond), Origin: fabric.Endpoint{Node: "h1"}, Frame: frame, Flow: 1}
	for _, inj := range []fabric.Injection{switchInjection, lateHost} {
		if _, err := attached.Inject(inj); err != nil {
			t.Fatalf("attached Inject: %v", err)
		}
	}
	if err := attached.AttachStream(fabric.StreamAttachment{Origin: streamHost.Origin, Source: streamSource(t, frame, 1, 10_000), Start: 200 * time.Microsecond, Flow: 1}); err != nil {
		t.Fatalf("AttachStream: %v", err)
	}
	for _, inj := range []fabric.Injection{switchInjection, streamHost, lateHost} {
		if _, err := eager.Inject(inj); err != nil {
			t.Fatalf("eager Inject: %v", err)
		}
	}
	attached.Run(1000)
	eager.Run(1000)
	for _, flow := range []fabric.FlowID{1, 2} {
		var got, want []time.Time
		for _, journey := range attached.Report() {
			if journey.Injection.Flow == flow {
				for _, delivery := range journey.Deliveries {
					got = append(got, delivery.At)
				}
			}
		}
		for _, journey := range eager.Report() {
			if journey.Injection.Flow == flow {
				for _, delivery := range journey.Deliveries {
					want = append(want, delivery.At)
				}
			}
		}
		if len(want) == 0 || !reflect.DeepEqual(got, want) {
			t.Errorf("flow %d delivery times = %v, want %v", flow, got, want)
		}
	}
}

func TestEagerHostInjectionsReleaseInTimeOrder(t *testing.T) {
	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	start := fab.Config().Start
	frame := flowFrame(src, dst)
	if _, err := fab.Inject(fabric.Injection{At: start, Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Frame: frame}); err != nil {
		t.Fatalf("Inject switch: %v", err)
	}
	for _, offset := range []time.Duration{100 * time.Microsecond, 50 * time.Microsecond} {
		if _, err := fab.Inject(fabric.Injection{At: start.Add(offset), Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
			t.Fatalf("Inject at %s: %v", offset, err)
		}
	}
	fab.Run(1000)
	journeys := fab.Report()
	if len(journeys) != 3 || len(journeys[1].Deliveries) == 0 || len(journeys[2].Deliveries) == 0 {
		t.Fatalf("journeys = %+v, want two host deliveries", journeys)
	}
	if !journeys[2].Deliveries[0].At.Before(journeys[1].Deliveries[0].At) {
		t.Fatalf("50us delivery %s is not before 100us delivery %s", journeys[2].Deliveries[0].At, journeys[1].Deliveries[0].At)
	}
}

func TestEagerHostInjectionChecksLinkAtRelease(t *testing.T) {
	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	start := fab.Config().Start
	if _, err := fab.Inject(fabric.Injection{At: start, Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Frame: flowFrame(src, dst)}); err != nil {
		t.Fatalf("Inject switch: %v", err)
	}
	if _, err := fab.Inject(fabric.Injection{At: start.Add(100 * time.Microsecond), Origin: fabric.Endpoint{Node: "h1"}, Frame: flowFrame(src, dst)}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if err := fab.SetFault(fabric.Endpoint{Node: "h1"}, fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, fabric.Fault{Kind: fabric.FaultCut}); err != nil {
		t.Fatalf("SetFault: %v", err)
	}
	fab.Run(1000)
	journeys := fab.Report()
	if len(journeys) != 2 || len(journeys[1].Entries) < 2 || journeys[1].Entries[1].Kind != fabric.EntryDrop {
		t.Fatalf("journeys = %+v, want a host link drop after injection", journeys)
	}
}

func TestRunScenarioRefusesActionsWithAttachedStream(t *testing.T) {
	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	start := fab.Config().Start
	if err := fab.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "h1"}, Source: streamSource(t, flowFrame(src, dst), 1, 10_000), Start: 5 * time.Millisecond, Flow: 1}); err != nil {
		t.Fatalf("AttachStream: %v", err)
	}
	_, err := fab.RunScenario(fabric.Scenario{
		Name: "stream with action", Budget: 100,
		Actions: []fabric.Action{{
			At: start.Add(time.Millisecond), Kind: fabric.ActionFault,
			Fault: &fabric.FaultAction{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Fault: fabric.Fault{Kind: fabric.FaultCut}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "attached stream") {
		t.Fatalf("RunScenario error = %v, want attached stream refusal", err)
	}
}
