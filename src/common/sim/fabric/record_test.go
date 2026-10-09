package fabric_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func newTestTwoHostFabric(t *testing.T, t0 time.Time) *fabric.Fabric {
	t.Helper()
	macH1 := netaddr.MAC{0, 0, 0, 0, 0, 1}
	macH2 := netaddr.MAC{0, 0, 0, 0, 0, 2}

	ports, _ := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()

	cfg := assembly.Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports,
				Bridge: &bridge.Config{},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}},
		},
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}
	if err := fab.Device("sw1").(*vswitch.Switch).Learn([]bridge.Seed{
		{MAC: macH2, Port: "1/1/2", Lifetime: bridge.Static},
	}); err != nil {
		t.Fatalf("Learn: %v", err)
	}
	return fab
}

func TestRecordRefusesDisagreeingFrame(t *testing.T) {
	frame := ethernet.Frame{Payload: []byte("captured")}
	raw, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	rec := fabric.Record{Origin: fabric.Endpoint{Node: "h1"}, Frame: &frame, Bytes: raw}
	if err := rec.Validate(); err != nil {
		t.Fatalf("Validate matching frame: %v", err)
	}
	frame.Dst[5] = 1
	if err := rec.Validate(); err == nil {
		t.Fatal("Validate accepted Frame that disagrees with Bytes")
	}
	frame.Tags = []vlan.Tag{{VID: 0xffff}}
	if err := rec.Validate(); err == nil {
		t.Fatal("Validate accepted unencodable Frame alongside Bytes")
	}
}

func TestRecordRefusesCapturedAboveOriginal(t *testing.T) {
	rec := fabric.Record{Origin: fabric.Endpoint{Node: "h1"}, Frame: &ethernet.Frame{}, CapturedLen: 64, OriginalLen: 64}
	if err := rec.Validate(); err != nil {
		t.Fatalf("Validate equal lengths: %v", err)
	}
	rec.CapturedLen = 65
	if err := rec.Validate(); err == nil {
		t.Fatal("Validate accepted CapturedLen above OriginalLen")
	}
}

func TestRecordCloneOwnsFrameValue(t *testing.T) {
	frame := ethernet.Frame{Src: netaddr.MAC{0, 0, 0, 0, 0, 1}, Payload: []byte("immutable")}
	rec := fabric.Record{Origin: fabric.Endpoint{Node: "h1"}, Frame: &frame, Bytes: []byte{1}}
	clone := rec.Clone()
	clone.Frame.Src[5] = 2
	clone.Bytes[0] = 2
	if rec.Frame.Src[5] != 1 || rec.Bytes[0] != 1 {
		t.Fatalf("Clone changed original: source = %v, bytes = %v", rec.Frame.Src, rec.Bytes)
	}
	if string(clone.Frame.Payload) != "immutable" {
		t.Fatalf("Clone payload = %q, want immutable", clone.Frame.Payload)
	}
}

func TestRecordDecodedFrame(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestTwoHostFabric(t, t0)

	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: []byte("decoded-test-payload"),
	}

	rec := fabric.Record{
		At:     t0,
		Source: "capture.pcap",
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  &frame,
	}

	sc := fabric.Scenario{
		Name: "record-decoded",
		Actions: []fabric.Action{
			{
				ID:     "record_test-1",
				At:     t0,
				Kind:   fabric.ActionRecord,
				Record: &rec,
			},
		},
	}

	res, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 10, Window: 0})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != fabric.StopQueueDrained {
		t.Errorf("res.Stop = %v, want StopQueueDrained", res.Stop)
	}
	journeys := fab.Report()
	if len(journeys) != 1 {
		t.Fatalf("len(journeys) = %d, want 1", len(journeys))
	}
	if journeys[0].State != fabric.JourneyDelivered {
		t.Errorf("journeys[0].State = %v, want %v", journeys[0].State, fabric.JourneyDelivered)
	}
}

func TestRecordRawBytes(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestTwoHostFabric(t, t0)

	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: []byte("bytes-test-payload"),
	}
	raw, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	rec := fabric.Record{
		At:     t0,
		Source: "raw.pcap",
		Origin: fabric.Endpoint{Node: "h1"},
		Bytes:  raw,
	}

	sc := fabric.Scenario{
		Name: "record-raw-bytes",
		Actions: []fabric.Action{
			{
				ID:     "record_test-2",
				At:     t0,
				Kind:   fabric.ActionRecord,
				Record: &rec,
			},
		},
	}

	res, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 10, Window: 0})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != fabric.StopQueueDrained {
		t.Errorf("res.Stop = %v, want StopQueueDrained", res.Stop)
	}
	journeys := fab.Report()
	if len(journeys) != 1 {
		t.Fatalf("len(journeys) = %d, want 1", len(journeys))
	}
	if journeys[0].State != fabric.JourneyDelivered {
		t.Errorf("journeys[0].State = %v, want %v", journeys[0].State, fabric.JourneyDelivered)
	}
}

func TestRecordTruncatedIssueAndState(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestTwoHostFabric(t, t0)

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

	rec := fabric.Record{
		At:          t0,
		Source:      "sensor-eth0.pcap",
		Origin:      fabric.Endpoint{Node: "h1"},
		Bytes:       raw,
		CapturedLen: 64,
		OriginalLen: 1500,
	}

	sc := fabric.Scenario{
		Name: "record-truncated",
		Actions: []fabric.Action{
			{
				ID:     "record_test-3",
				At:     t0,
				Kind:   fabric.ActionRecord,
				Record: &rec,
			},
		},
	}

	res, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 10, Window: 0})
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
	if j.State != fabric.JourneyTruncated {
		t.Errorf("j.State = %v, want JourneyTruncated", j.State)
	}

	issues := j.Metadata.Issues()
	truncIssueIdx := slices.IndexFunc(issues, func(iss analysis.Issue) bool {
		return iss.Code == fabric.IssueTruncatedRecord
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
	fab := newTestTwoHostFabric(t, t0)

	rec := fabric.Record{
		At:     t0,
		Source: "corrupt.pcap",
		Origin: fabric.Endpoint{Node: "h1"},
		Bytes:  []byte{0x01, 0x02, 0x03}, // Too short to be a valid ethernet header
	}

	sc := fabric.Scenario{
		Name: "record-undecodable",
		Actions: []fabric.Action{
			{
				ID:     "record_test-4",
				At:     t0,
				Kind:   fabric.ActionRecord,
				Record: &rec,
			},
		},
	}

	if err := sc.Validate(); err == nil {
		t.Fatal("sc.Validate() succeeded on undecodable bytes, want error")
	}

	_, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 10, Window: 0})
	if err == nil {
		t.Fatal("fab.RunScenario(sc) succeeded on undecodable bytes, want error")
	}
	if *fabric.ExportFabricStepped(fab) {
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

	base := fabric.Record{
		At:          t0,
		Source:      "src1",
		Origin:      fabric.Endpoint{Node: "h1"},
		Frame:       baseFrame,
		Bytes:       []byte("raw-bytes-1"),
		CapturedLen: 64,
		OriginalLen: 128,
	}

	fields := []struct {
		name     string
		mutate   func(r *fabric.Record)
		expField string
	}{
		{
			name:     "at",
			mutate:   func(r *fabric.Record) { r.At = t0.Add(time.Second) },
			expField: "at",
		},
		{
			name:     "source",
			mutate:   func(r *fabric.Record) { r.Source = "src2" },
			expField: "source",
		},
		{
			name:     "origin",
			mutate:   func(r *fabric.Record) { r.Origin = fabric.Endpoint{Node: "h2"} },
			expField: "origin",
		},
		{
			name:     "bytes",
			mutate:   func(r *fabric.Record) { r.Bytes = []byte("raw-bytes-2") },
			expField: "bytes",
		},
		{
			name:     "captured_len",
			mutate:   func(r *fabric.Record) { r.CapturedLen = 70 },
			expField: "captured_len",
		},
		{
			name:     "original_len",
			mutate:   func(r *fabric.Record) { r.OriginalLen = 256 },
			expField: "original_len",
		},
		{
			name:     "frame",
			mutate:   func(r *fabric.Record) { r.Frame = otherFrame },
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

	rec := fabric.Record{
		At:     t0,
		Source: "test.pcap",
		Origin: fabric.Endpoint{Node: "h1"},
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

func TestRecordNormalizeFrameDefaults(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: []byte("frame-defaults"),
	}
	raw, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	rec := fabric.Record{
		At:     t0,
		Source: "test.pcap",
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  &frame,
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
}

func TestRecordNormalizeEncodeError(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	invalidFrame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: []byte("invalid-frame"),
		Tags: []vlan.Tag{
			{VID: 0xFFFF},
		},
	}

	rec := fabric.Record{
		At:     t0,
		Source: "invalid.pcap",
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  &invalidFrame,
	}

	_, err := rec.Normalize()
	if err == nil {
		t.Fatal("Normalize with unencodable frame succeeded, want error")
	}
	if !strings.Contains(err.Error(), "encode record frame") {
		t.Errorf("err = %q, want it to contain 'encode record frame'", err.Error())
	}
}
