package fabric

import (
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func newTestTwoHostFabric(t *testing.T, t0 time.Time) (*Fabric, Config) {
	t.Helper()
	macH1 := netaddr.MAC{0, 0, 0, 0, 0, 1}
	macH2 := netaddr.MAC{0, 0, 0, 0, 0, 2}

	ports, _ := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()

	cfg := Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports,
				Bridge: &bridge.Config{},
			},
		},
		Hosts: map[string]Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []Cable{
			{A: Endpoint{Node: "sw1", Port: "1/1/1"}, B: Endpoint{Node: "h1"}},
			{A: Endpoint{Node: "sw1", Port: "1/1/2"}, B: Endpoint{Node: "h2"}},
		},
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}
	if err := fab.Switch("sw1").Learn([]bridge.Seed{
		{MAC: macH2, Port: "1/1/2", Lifetime: bridge.Static},
	}); err != nil {
		t.Fatalf("Learn: %v", err)
	}
	return fab, cfg
}

func TestRecordDecodedFrame(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestTwoHostFabric(t, t0)

	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: []byte("decoded-test-payload"),
	}

	rec := Record{
		At:     t0,
		Source: "capture.pcap",
		Origin: Endpoint{Node: "h1"},
		Frame:  &frame,
	}

	sc := Scenario{
		Name: "record-decoded",
		Spec: ConstructionSpec{
			Start:         cfg.Start,
			Switches:      fab.Spec().Switches,
			Hosts:         cfg.Hosts,
			Cables:        cfg.Cables,
			PhyAssumption: cfg.PhyAssumption,
		},
		Actions: []Action{
			{
				At:     t0,
				Kind:   ActionRecord,
				Record: &rec,
			},
		},
		Budget: 10,
	}

	res, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != StopQueueDrained {
		t.Errorf("res.Stop = %v, want StopQueueDrained", res.Stop)
	}
	journeys := fab.Report()
	if len(journeys) != 1 {
		t.Fatalf("len(journeys) = %d, want 1", len(journeys))
	}
	if journeys[0].State != JourneyDelivered {
		t.Errorf("journeys[0].State = %v, want %v", journeys[0].State, JourneyDelivered)
	}
}

func TestRecordRawBytes(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestTwoHostFabric(t, t0)

	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: []byte("bytes-test-payload"),
	}
	raw, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	rec := Record{
		At:     t0,
		Source: "raw.pcap",
		Origin: Endpoint{Node: "h1"},
		Bytes:  raw,
	}

	sc := Scenario{
		Name: "record-raw-bytes",
		Spec: ConstructionSpec{
			Start:         cfg.Start,
			Switches:      fab.Spec().Switches,
			Hosts:         cfg.Hosts,
			Cables:        cfg.Cables,
			PhyAssumption: cfg.PhyAssumption,
		},
		Actions: []Action{
			{
				At:     t0,
				Kind:   ActionRecord,
				Record: &rec,
			},
		},
		Budget: 10,
	}

	res, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != StopQueueDrained {
		t.Errorf("res.Stop = %v, want StopQueueDrained", res.Stop)
	}
	journeys := fab.Report()
	if len(journeys) != 1 {
		t.Fatalf("len(journeys) = %d, want 1", len(journeys))
	}
	if journeys[0].State != JourneyDelivered {
		t.Errorf("journeys[0].State = %v, want %v", journeys[0].State, JourneyDelivered)
	}
}

func TestRecordTruncatedIssueAndState(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestTwoHostFabric(t, t0)

	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: make([]byte, 50), // 14 byte header + 50 byte payload = 64 bytes
	}
	raw, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(raw) != 64 {
		t.Fatalf("len(raw) = %d, want 64", len(raw))
	}

	rec := Record{
		At:          t0,
		Source:      "sensor-eth0.pcap",
		Origin:      Endpoint{Node: "h1"},
		Bytes:       raw,
		CapturedLen: 64,
		OriginalLen: 1500,
	}

	sc := Scenario{
		Name: "record-truncated",
		Spec: ConstructionSpec{
			Start:         cfg.Start,
			Switches:      fab.Spec().Switches,
			Hosts:         cfg.Hosts,
			Cables:        cfg.Cables,
			PhyAssumption: cfg.PhyAssumption,
		},
		Actions: []Action{
			{
				At:     t0,
				Kind:   ActionRecord,
				Record: &rec,
			},
		},
		Budget: 10,
	}

	res, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Status != analysis.Incomplete {
		t.Errorf("res.Status = %v, want Incomplete", res.Status)
	}

	journeys := fab.Report()
	if len(journeys) != 1 {
		t.Fatalf("len(journeys) = %d, want 1", len(journeys))
	}
	j := journeys[0]
	if j.State != JourneyTruncated {
		t.Errorf("j.State = %v, want JourneyTruncated", j.State)
	}

	issues := j.Metadata.Issues()
	truncIssueIdx := slices.IndexFunc(issues, func(iss analysis.Issue) bool {
		return iss.Code == IssueTruncatedRecord
	})
	if truncIssueIdx < 0 {
		t.Fatalf("j.Metadata.Issues missing IssueTruncatedRecord: %+v", issues)
	}
	truncIssue := issues[truncIssueIdx]
	if truncIssue.Status != analysis.Incomplete {
		t.Errorf("truncIssue.Status = %v, want Incomplete", truncIssue.Status)
	}
	if !strings.Contains(truncIssue.Message, "sensor-eth0.pcap") {
		t.Errorf("truncIssue.Message %q does not name source %q", truncIssue.Message, "sensor-eth0.pcap")
	}
}

func TestRecordUndecodableBytesError(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestTwoHostFabric(t, t0)

	rec := Record{
		At:     t0,
		Source: "corrupt.pcap",
		Origin: Endpoint{Node: "h1"},
		Bytes:  []byte{0x01, 0x02, 0x03}, // Too short to be a valid ethernet header
	}

	sc := Scenario{
		Name: "record-undecodable",
		Spec: ConstructionSpec{
			Start:         cfg.Start,
			Switches:      fab.Spec().Switches,
			Hosts:         cfg.Hosts,
			Cables:        cfg.Cables,
			PhyAssumption: cfg.PhyAssumption,
		},
		Actions: []Action{
			{
				At:     t0,
				Kind:   ActionRecord,
				Record: &rec,
			},
		},
		Budget: 10,
	}

	if err := sc.Validate(); err == nil {
		t.Fatal("sc.Validate() succeeded on undecodable bytes, want error")
	}

	_, err := fab.RunScenario(sc)
	if err == nil {
		t.Fatal("fab.RunScenario(sc) succeeded on undecodable bytes, want error")
	}
	if fab.stepped {
		t.Errorf("fab was stepped when scenario was invalid")
	}
}

func TestRecordDiffFields(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	baseFrame := &ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: []byte("diff-payload-1"),
	}
	otherFrame := &ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 3},
		Payload: []byte("diff-payload-2"),
	}

	base := Record{
		At:          t0,
		Source:      "src1",
		Origin:      Endpoint{Node: "h1"},
		Frame:       baseFrame,
		Bytes:       []byte("raw-bytes-1"),
		CapturedLen: 64,
		OriginalLen: 128,
	}

	fields := []struct {
		name     string
		mutate   func(r *Record)
		expField string
	}{
		{
			name:     "at",
			mutate:   func(r *Record) { r.At = t0.Add(time.Second) },
			expField: "at",
		},
		{
			name:     "source",
			mutate:   func(r *Record) { r.Source = "src2" },
			expField: "source",
		},
		{
			name:     "origin",
			mutate:   func(r *Record) { r.Origin = Endpoint{Node: "h2"} },
			expField: "origin",
		},
		{
			name:     "bytes",
			mutate:   func(r *Record) { r.Bytes = []byte("raw-bytes-2") },
			expField: "bytes",
		},
		{
			name:     "captured_len",
			mutate:   func(r *Record) { r.CapturedLen = 70 },
			expField: "captured_len",
		},
		{
			name:     "original_len",
			mutate:   func(r *Record) { r.OriginalLen = 256 },
			expField: "original_len",
		},
		{
			name:     "frame",
			mutate:   func(r *Record) { r.Frame = otherFrame },
			expField: "frame",
		},
	}

	for _, tc := range fields {
		t.Run(tc.name, func(t *testing.T) {
			mutated := base.Clone()
			tc.mutate(&mutated)
			changes := base.Diff(mutated)
			found := slices.ContainsFunc(changes, func(ch trace.Change) bool {
				return ch.Field == tc.expField
			})
			if !found {
				t.Errorf("base.Diff(mutated) missing field %q: %+v", tc.expField, changes)
			}
		})
	}
}

func TestRecordNormalizeDefaults(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: []byte("default-lengths"),
	}
	raw, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	rec := Record{
		At:     t0,
		Source: "test.pcap",
		Origin: Endpoint{Node: "h1"},
		Bytes:  raw,
	}

	norm, err := rec.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if norm.CapturedLen != len(raw) {
		t.Errorf("norm.CapturedLen = %d, want %d", norm.CapturedLen, len(raw))
	}
	if norm.OriginalLen != len(raw) {
		t.Errorf("norm.OriginalLen = %d, want %d", norm.OriginalLen, len(raw))
	}
	if norm.Frame == nil {
		t.Fatalf("norm.Frame = nil, want decoded Frame")
	}
	if norm.Frame.Dst != frame.Dst {
		t.Errorf("norm.Frame.Dst = %v, want %v", norm.Frame.Dst, frame.Dst)
	}
}
