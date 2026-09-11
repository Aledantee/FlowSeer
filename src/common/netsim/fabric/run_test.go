package fabric_test

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func newTwoSwitchTopology(t *testing.T, cableFault fabric.Fault) (*fabric.Fabric, netaddr.MAC, netaddr.MAC) {
	t.Helper()

	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable1, err := b1.Build()
	if err != nil {
		t.Fatalf("build sw1 ports: %v", err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable2, err := b2.Build()
	if err != nil {
		t.Fatalf("build sw2 ports: %v", err)
	}

	vid10 := vlan.ID(10)
	newBridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "VLAN10",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {
						PVID:     &vid10,
						Untagged: []vlan.ID{10},
					},
					"1/1/24": {
						Tagged: []vlan.ID{10},
					},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  pTable1,
				Bridge: newBridgeCfg(),
			},
			"sw2": {
				Ports:  pTable2,
				Bridge: newBridgeCfg(),
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
				LengthMeters: 300,
				Fault:        cableFault,
			},
			{
				A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "h2"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	return fab, macH1, macH2
}

// A corrupting cable whose far end is a host drops the copy with bad-frame
// instead of delivering it, so a corrupted last hop is as visible as any other.
func TestCorruptingCableToAHostDropsTheCopy(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	macH1 := netaddr.MAC{0, 0, 0, 0, 0, 0x01}
	macH2 := netaddr.MAC{0, 0, 0, 0, 0, 0x02}
	fab, err := fabric.New(fabric.Config{
		Switches: map[string]vswitch.Config{"sw1": {Ports: ports}},
		Hosts:    map[string]fabric.Host{"h1": {Address: macH1}, "h2": {Address: macH2}},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}, Fault: fabric.Fault{Kind: fabric.FaultCorruptEveryNth, N: 1}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fab.Inject(fabric.Injection{At: now, Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Dst: macH2, Src: macH1}}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(10)
	j := fab.Report()[0]
	if len(j.Deliveries) != 0 {
		t.Errorf("Deliveries = %+v, want none from a corrupting cable", j.Deliveries)
	}
	last := j.Entries[len(j.Entries)-1]
	if last.Kind != fabric.EntryDrop || last.Reason != fabric.ReasonBadFrame || last.Device != "h2" {
		t.Errorf("last entry = %+v, want a bad-frame drop at h2", last)
	}
}

// A host with a VLAN emits exactly its own tag: a frame the caller tagged
// otherwise arrives with the host's tag alone, never a second one.
func TestVlanHostReplacesTheCallersTag(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	vid10 := vlan.ID(10)
	fab, err := fabric.New(fabric.Config{
		Switches: map[string]vswitch.Config{"sw1": {Ports: ports}},
		Hosts:    map[string]fabric.Host{"h1": {Address: netaddr.MAC{0, 0, 0, 0, 0, 1}, VLAN: &vid10}, "h2": {Address: netaddr.MAC{0, 0, 0, 0, 0, 2}}},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	frame := ethernet.Frame{Dst: netaddr.MAC{0, 0, 0, 0, 0, 2}, Src: netaddr.MAC{0, 0, 0, 0, 0, 1}, Tags: []vlan.Tag{{TPID: 0x8100, VID: 20}}}
	if _, err := fab.Inject(fabric.Injection{At: now, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	queued := fab.Snapshot().Queue
	if len(queued) != 1 || len(queued[0].Frame.Tags) != 1 || queued[0].Frame.Tags[0].VID != 10 {
		t.Errorf("queued frame tags = %+v, want one C-TAG with VID 10", queued)
	}
}

// Copies of one frame keep its sequence, so two copies due at the same instant
// on two devices step in device name order, as the queue rule says.
func TestSameInstantCopiesStepInDeviceOrder(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	table := func() port.Table {
		b := port.NewBuilder()
		b.Range("1/1/%d", 1, 3, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}

		return tbl
	}
	fab, err := fabric.New(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: table()}, "sw2": {Ports: table()}, "sw3": {Ports: table()},
		},
		Hosts: map[string]fabric.Host{"h1": {Address: netaddr.MAC{0, 0, 0, 0, 0, 1}}},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw3", Port: "1/1/1"}, LengthMeters: 10},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, LengthMeters: 10},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fab.Inject(fabric.Injection{At: now, Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Dst: netaddr.MAC{0, 0, 0, 0, 0, 9}, Src: netaddr.MAC{0, 0, 0, 0, 0, 1}}}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Step()
	second, _ := fab.Step()
	third, _ := fab.Step()
	if second.Device != "sw2" || third.Device != "sw3" {
		t.Errorf("hops after the first = %s then %s, want sw2 then sw3", second.Device, third.Device)
	}
}

// A frame the codec cannot encode is refused at injection, where the caller
// can act on it, rather than counted as zero octets in a run.
func TestInjectRefusesAFrameTheCodecRejects(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{})
	bad := ethernet.Frame{Dst: macH2, Src: macH1, Tags: []vlan.Tag{{TPID: 0x9100, VID: 10}}}
	if _, err := fab.Inject(fabric.Injection{Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Frame: bad}); err == nil {
		t.Error("Inject(frame with TPID 0x9100) error = nil, want an error")
	}
}

func TestTwoSwitchFrameForwardingAndReverse(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	inj := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     macH1,
			Dst:     macH2,
			Payload: []byte("ping"),
		},
	}

	fid1, err := fab.Inject(inj)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if fid1 != 1 {
		t.Errorf("fid1 = %d, want 1", fid1)
	}

	steps := fab.Run(10)
	if steps != 2 {
		t.Fatalf("steps = %d, want 2", steps)
	}

	journeys := fab.Report()
	if len(journeys) != 1 {
		t.Fatalf("len(journeys) = %d, want 1", len(journeys))
	}

	j1 := journeys[0]
	if j1.FrameID != fid1 {
		t.Errorf("j1.FrameID = %d, want %d", j1.FrameID, fid1)
	}

	expectedKinds := []fabric.EntryKind{
		fabric.EntryInjection,
		fabric.EntryHop,
		fabric.EntryCrossing,
		fabric.EntryHop,
		fabric.EntryDelivery,
	}
	if len(j1.Entries) != len(expectedKinds) {
		t.Fatalf("len(j1.Entries) = %d, want %d", len(j1.Entries), len(expectedKinds))
	}
	for i, wantKind := range expectedKinds {
		if j1.Entries[i].Kind != wantKind {
			t.Errorf("entry %d Kind = %v, want %v", i, j1.Entries[i].Kind, wantKind)
		}
	}

	if j1.Entries[0].Device != "h1" || j1.Entries[0].Port != "" {
		t.Errorf("injection entry origin = (%q, %q), want (h1, '')", j1.Entries[0].Device, j1.Entries[0].Port)
	}
	if j1.Entries[1].Device != "sw1" || j1.Entries[1].Port != "1/1/1" {
		t.Errorf("sw1 hop = (%q, %q), want (sw1, 1/1/1)", j1.Entries[1].Device, j1.Entries[1].Port)
	}
	if j1.Entries[1].Result.Outcome != trace.Flooded {
		t.Errorf("sw1 outcome = %v, want Flooded", j1.Entries[1].Result.Outcome)
	}
	if j1.Entries[2].Device != "sw2" || j1.Entries[2].Port != "1/1/24" {
		t.Errorf("crossing far end = (%q, %q), want (sw2, 1/1/24)", j1.Entries[2].Device, j1.Entries[2].Port)
	}
	wantLatency := 1501 * time.Nanosecond
	if j1.Entries[2].Latency != wantLatency {
		t.Errorf("crossing latency = %v, want %v", j1.Entries[2].Latency, wantLatency)
	}
	if j1.Entries[3].Device != "sw2" || j1.Entries[3].Port != "1/1/24" {
		t.Errorf("sw2 hop = (%q, %q), want (sw2, 1/1/24)", j1.Entries[3].Device, j1.Entries[3].Port)
	}
	if j1.Entries[3].Result.Outcome != trace.Flooded {
		t.Errorf("sw2 outcome = %v, want Flooded", j1.Entries[3].Result.Outcome)
	}
	if j1.Entries[4].Device != "h2" {
		t.Errorf("delivery device = %q, want h2", j1.Entries[4].Device)
	}

	if len(j1.Deliveries) != 1 {
		t.Fatalf("len(j1.Deliveries) = %d, want 1", len(j1.Deliveries))
	}
	del := j1.Deliveries[0]
	if del.Host != "h2" {
		t.Errorf("del.Host = %q, want h2", del.Host)
	}
	if len(del.Frame.Tags) != 0 {
		t.Errorf("del.Frame.Tags = %v, want untagged", del.Frame.Tags)
	}

	t1 := t0.Add(time.Second)
	injRev := fabric.Injection{
		At:     t1,
		Origin: fabric.Endpoint{Node: "h2"},
		Frame: ethernet.Frame{
			Src:     macH2,
			Dst:     macH1,
			Payload: []byte("pong"),
		},
	}

	fid2, err := fab.Inject(injRev)
	if err != nil {
		t.Fatalf("Inject reverse: %v", err)
	}

	stepsRev := fab.Run(10)
	if stepsRev != 2 {
		t.Fatalf("stepsRev = %d, want 2", stepsRev)
	}

	journeys = fab.Report()
	if len(journeys) != 2 {
		t.Fatalf("len(journeys) = %d, want 2", len(journeys))
	}

	j2 := journeys[1]
	if j2.FrameID != fid2 {
		t.Errorf("j2.FrameID = %d, want %d", j2.FrameID, fid2)
	}
	if j2.Entries[1].Result.Outcome != trace.Forwarded {
		t.Errorf("sw2 reverse hop outcome = %v, want Forwarded", j2.Entries[1].Result.Outcome)
	}
	if j2.Entries[3].Result.Outcome != trace.Forwarded {
		t.Errorf("sw1 reverse hop outcome = %v, want Forwarded", j2.Entries[3].Result.Outcome)
	}
	if len(j2.Deliveries) != 1 || j2.Deliveries[0].Host != "h1" {
		t.Errorf("j2 deliveries = %+v, want 1 delivery to h1", j2.Deliveries)
	}
}

func TestRunBudgetHaltsAndSnapshotTransientState(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	inj := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     macH1,
			Dst:     macH2,
			Payload: []byte("ping"),
		},
	}

	if _, err := fab.Inject(inj); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	steps := fab.Run(1)
	if steps != 1 {
		t.Fatalf("steps = %d, want 1", steps)
	}

	snap1 := fab.Snapshot()
	if len(snap1.Queue) != 1 {
		t.Fatalf("len(snap1.Queue) = %d, want 1", len(snap1.Queue))
	}
	q0 := snap1.Queue[0]
	if q0.Device != "sw2" || q0.Port != "1/1/24" {
		t.Errorf("queued arrival = (%q, %q), want (sw2, 1/1/24)", q0.Device, q0.Port)
	}
	wantAt := t0.Add(1501 * time.Nanosecond)
	if !q0.At.Equal(wantAt) {
		t.Errorf("queued arrival At = %v, want %v", q0.At, wantAt)
	}
	if !snap1.Clock.Equal(t0) {
		t.Errorf("snap1.Clock = %v, want %v", snap1.Clock, t0)
	}

	sw1Entries := snap1.Devices["sw1"].Entries
	if len(sw1Entries) != 1 || sw1Entries[0].MAC != macH1 {
		t.Errorf("sw1 FDB entries = %+v, want h1 MAC", sw1Entries)
	}
	sw2Entries := snap1.Devices["sw2"].Entries
	if len(sw2Entries) != 0 {
		t.Errorf("sw2 FDB entries = %+v, want empty", sw2Entries)
	}

	steps = fab.Run(1)
	if steps != 1 {
		t.Fatalf("second Run(1) steps = %d, want 1", steps)
	}

	snap2 := fab.Snapshot()
	if len(snap2.Queue) != 0 {
		t.Errorf("len(snap2.Queue) = %d, want 0", len(snap2.Queue))
	}
	if !snap2.Clock.Equal(wantAt) {
		t.Errorf("snap2.Clock = %v, want %v", snap2.Clock, wantAt)
	}

	sw2EntriesAfter := snap2.Devices["sw2"].Entries
	if len(sw2EntriesAfter) != 1 || sw2EntriesAfter[0].MAC != macH1 {
		t.Errorf("sw2 FDB entries after step 2 = %+v, want h1 MAC", sw2EntriesAfter)
	}
}

func TestInterleavedFramesByArrivalTime(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	inj1 := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     macH1,
			Dst:     macH2,
			Payload: []byte("frame1"),
		},
	}
	inj2 := fabric.Injection{
		At:     t0.Add(time.Microsecond),
		Origin: fabric.Endpoint{Node: "h2"},
		Frame: ethernet.Frame{
			Src:     macH2,
			Dst:     macH1,
			Payload: []byte("frame2"),
		},
	}

	fid1, err := fab.Inject(inj1)
	if err != nil {
		t.Fatalf("Inject 1: %v", err)
	}
	fid2, err := fab.Inject(inj2)
	if err != nil {
		t.Fatalf("Inject 2: %v", err)
	}

	type stepExpectation struct {
		device string
		port   string
		at     time.Time
	}

	expectedSteps := []stepExpectation{
		{device: "sw1", port: "1/1/1", at: t0},
		{device: "sw2", port: "1/1/1", at: t0.Add(time.Microsecond)},
		{device: "sw2", port: "1/1/24", at: t0.Add(1501 * time.Nanosecond)},
		{device: "sw1", port: "1/1/24", at: t0.Add(2501 * time.Nanosecond)},
	}

	for i, want := range expectedSteps {
		entry, ok := fab.Step()
		if !ok {
			t.Fatalf("step %d returned ok=false", i+1)
		}
		if entry.Device != want.device || entry.Port != want.port {
			t.Errorf("step %d location = (%q, %q), want (%q, %q)", i+1, entry.Device, entry.Port, want.device, want.port)
		}
		if !entry.At.Equal(want.at) {
			t.Errorf("step %d At = %v, want %v", i+1, entry.At, want.at)
		}
	}

	if _, ok := fab.Step(); ok {
		t.Errorf("expected queue to be empty after 4 steps")
	}

	journeys := fab.Report()
	if len(journeys) != 2 {
		t.Fatalf("len(journeys) = %d, want 2", len(journeys))
	}
	if journeys[0].FrameID != fid1 {
		t.Errorf("journeys[0].FrameID = %d, want %d", journeys[0].FrameID, fid1)
	}
	if journeys[1].FrameID != fid2 {
		t.Errorf("journeys[1].FrameID = %d, want %d", journeys[1].FrameID, fid2)
	}
}

func TestCableLossFault(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{
		Kind: fabric.FaultLoseEveryNth,
		N:    2,
	})

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	inj1 := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Src: macH1, Dst: macH2, Payload: []byte("frame1")},
	}
	inj2 := fabric.Injection{
		At:     t0.Add(time.Second),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Src: macH1, Dst: macH2, Payload: []byte("frame2")},
	}

	if _, err := fab.Inject(inj1); err != nil {
		t.Fatalf("Inject 1: %v", err)
	}
	if _, err := fab.Inject(inj2); err != nil {
		t.Fatalf("Inject 2: %v", err)
	}

	fab.Run(20)

	journeys := fab.Report()
	if len(journeys) != 2 {
		t.Fatalf("len(journeys) = %d, want 2", len(journeys))
	}

	j1 := journeys[0]
	if len(j1.Deliveries) != 1 {
		t.Fatalf("frame 1 deliveries = %d, want 1", len(j1.Deliveries))
	}

	j2 := journeys[1]
	if len(j2.Deliveries) != 0 {
		t.Errorf("frame 2 deliveries = %d, want 0", len(j2.Deliveries))
	}

	lastEntry := j2.Entries[len(j2.Entries)-1]
	if lastEntry.Kind != fabric.EntryLoss {
		t.Errorf("frame 2 last entry Kind = %v, want Loss", lastEntry.Kind)
	}
	if lastEntry.Reason != fabric.ReasonCableLoss {
		t.Errorf("frame 2 last entry Reason = %v, want %v", lastEntry.Reason, fabric.ReasonCableLoss)
	}
	if lastEntry.Cable == nil {
		t.Errorf("frame 2 last entry Cable is nil")
	}

	for _, e := range j2.Entries {
		if e.Device == "sw2" {
			t.Errorf("frame 2 unexpectedly contains sw2 hop")
		}
	}
}

func TestCableCorruptionFault(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{
		Kind: fabric.FaultCorruptEveryNth,
		N:    1,
	})

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	inj := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Src: macH1, Dst: macH2, Payload: []byte("corrupt-me")},
	}

	if _, err := fab.Inject(inj); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	fab.Run(10)

	journeys := fab.Report()
	if len(journeys) != 1 {
		t.Fatalf("len(journeys) = %d, want 1", len(journeys))
	}
	j := journeys[0]
	if len(j.Deliveries) != 0 {
		t.Errorf("corrupt frame had %d deliveries, want 0", len(j.Deliveries))
	}

	lastEntry := j.Entries[len(j.Entries)-1]
	if lastEntry.Kind != fabric.EntryDrop {
		t.Errorf("last entry Kind = %v, want Drop", lastEntry.Kind)
	}
	if lastEntry.Device != "sw2" || lastEntry.Port != "1/1/24" {
		t.Errorf("last entry location = (%q, %q), want (sw2, 1/1/24)", lastEntry.Device, lastEntry.Port)
	}
	if lastEntry.Reason != fabric.ReasonBadFrame {
		t.Errorf("last entry Reason = %v, want %v", lastEntry.Reason, fabric.ReasonBadFrame)
	}
}

func TestLoopDetectionAndStepBudget(t *testing.T) {
	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable1, err := b1.Build()
	if err != nil {
		t.Fatalf("build sw1: %v", err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable2, err := b2.Build()
	if err != nil {
		t.Fatalf("build sw2: %v", err)
	}

	vid10 := vlan.ID(10)
	newBridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "VLAN10",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {
						PVID:     &vid10,
						Untagged: []vlan.ID{10},
					},
					"1/1/2": {
						Tagged: []vlan.ID{10},
					},
					"1/1/3": {
						Tagged: []vlan.ID{10},
					},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macUnknown := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: pTable1, Bridge: newBridgeCfg()},
			"sw2": {Ports: pTable2, Bridge: newBridgeCfg()},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				LengthMeters: 10,
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/3"},
				LengthMeters: 10,
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	inj := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Src: macH1, Dst: macUnknown, Payload: []byte("storm")},
	}

	if _, err := fab.Inject(inj); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	steps := fab.Run(50)
	if steps != 50 {
		t.Errorf("steps = %d, want 50", steps)
	}

	snap := fab.Snapshot()
	if len(snap.Queue) == 0 {
		t.Errorf("expected queue to be non-empty after budget halt")
	}

	journeys := fab.Report()
	if len(journeys) != 1 {
		t.Fatalf("len(journeys) = %d, want 1", len(journeys))
	}

	var foundLoop bool
	for _, e := range journeys[0].Entries {
		if e.Kind == fabric.EntryLoop {
			foundLoop = true
			if e.Device == "" || e.Port == "" {
				t.Errorf("loop entry device/port empty: (%q, %q)", e.Device, e.Port)
			}
			break
		}
	}
	if !foundLoop {
		t.Errorf("journey does not contain any loop entry")
	}
}

func TestHostTagFormHandling(t *testing.T) {
	t.Run("TaggedHostDeliveryReceivesCTag", func(t *testing.T) {
		b1 := port.NewBuilder()
		b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b1.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		pTable1, _ := b1.Build()

		b2 := port.NewBuilder()
		b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b2.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		pTable2, _ := b2.Build()

		vid10 := vlan.ID(10)
		macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
		macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

		cfg := fabric.Config{
			Switches: map[string]vswitch.Config{
				"sw1": {
					Ports: pTable1,
					Bridge: &bridge.Config{
						VLAN: &bridge.VLAN{
							Table: map[vlan.ID]string{10: "VLAN10"},
							Switchports: map[string]bridge.Switchport{
								"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
								"1/1/24": {Tagged: []vlan.ID{10}},
							},
						},
					},
				},
				"sw2": {
					Ports: pTable2,
					Bridge: &bridge.Config{
						VLAN: &bridge.VLAN{
							Table: map[vlan.ID]string{10: "VLAN10"},
							Switchports: map[string]bridge.Switchport{
								"1/1/1":  {Tagged: []vlan.ID{10}},
								"1/1/24": {Tagged: []vlan.ID{10}},
							},
						},
					},
				},
			},
			Hosts: map[string]fabric.Host{
				"h1": {Address: macH1},
				"h2": {Address: macH2, VLAN: &vid10},
			},
			Cables: []fabric.Cable{
				{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
				{A: fabric.Endpoint{Node: "sw1", Port: "1/1/24"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/24"}},
				{A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h2"}},
			},
		}

		fab, err := fabric.New(cfg)
		if err != nil {
			t.Fatalf("New fabric: %v", err)
		}

		t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
		inj := fabric.Injection{
			At:     t0,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame:  ethernet.Frame{Src: macH1, Dst: macH2, Payload: []byte("ping")},
		}
		if _, err := fab.Inject(inj); err != nil {
			t.Fatalf("Inject: %v", err)
		}

		fab.Run(10)

		journeys := fab.Report()
		if len(journeys) != 1 || len(journeys[0].Deliveries) != 1 {
			t.Fatalf("expected 1 journey with 1 delivery, got %+v", journeys)
		}

		del := journeys[0].Deliveries[0]
		if len(del.Frame.Tags) != 1 || del.Frame.Tags[0].VID != 10 {
			t.Errorf("del.Frame.Tags = %v, want C-TAG 10", del.Frame.Tags)
		}
	})

	t.Run("TaggedHostIntoAccessPortDroppedAtHopOne", func(t *testing.T) {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		pTable, _ := b.Build()

		vid10 := vlan.ID(10)
		macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}

		cfg := fabric.Config{
			Switches: map[string]vswitch.Config{
				"sw1": {
					Ports: pTable,
					Bridge: &bridge.Config{
						VLAN: &bridge.VLAN{
							Table: map[vlan.ID]string{10: "VLAN10"},
							Switchports: map[string]bridge.Switchport{
								"1/1/1": {
									PVID:      &vid10,
									Untagged:  []vlan.ID{10},
									Admission: bridge.UntaggedAndPriorityTaggedOnly,
								},
							},
						},
					},
				},
			},
			Hosts: map[string]fabric.Host{
				"h1": {Address: macH1, VLAN: &vid10},
			},
			Cables: []fabric.Cable{
				{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			},
		}

		fab, err := fabric.New(cfg)
		if err != nil {
			t.Fatalf("New fabric: %v", err)
		}

		t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
		inj := fabric.Injection{
			At:     t0,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame:  ethernet.Frame{Src: macH1, Dst: netaddr.MAC{1, 2, 3, 4, 5, 6}, Payload: []byte("drop-me")},
		}
		if _, err := fab.Inject(inj); err != nil {
			t.Fatalf("Inject: %v", err)
		}

		fab.Run(5)

		journeys := fab.Report()
		if len(journeys) != 1 {
			t.Fatalf("len(journeys) = %d, want 1", len(journeys))
		}
		j := journeys[0]
		if len(j.Deliveries) != 0 {
			t.Errorf("deliveries = %d, want 0", len(j.Deliveries))
		}

		if len(j.Entries) != 3 {
			t.Fatalf("len(j.Entries) = %d, want 3 (Injection, Hop, Drop)", len(j.Entries))
		}
		hop := j.Entries[1]
		if hop.Kind != fabric.EntryHop || hop.Result.Outcome != trace.Dropped || hop.Result.Reason != bridge.ReasonAdmission {
			t.Errorf("hop = %+v, want Dropped with ReasonAdmission", hop)
		}
		drop := j.Entries[2]
		if drop.Kind != fabric.EntryDrop || drop.Reason != bridge.ReasonAdmission {
			t.Errorf("drop = %+v, want Drop with ReasonAdmission", drop)
		}
	})
}

func TestDevicePortCaptureReplay(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable, _ := b.Build()

	vid10 := vlan.ID(10)
	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: pTable,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "VLAN10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
						},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: netaddr.MAC{1, 2, 3, 4, 5, 6}},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	taggedFrame := ethernet.Frame{
		Src: netaddr.MAC{1, 2, 3, 4, 5, 6},
		Dst: netaddr.MAC{6, 5, 4, 3, 2, 1},
		Tags: []vlan.Tag{
			{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10},
		},
		Payload: []byte("replay"),
	}

	inj := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame:  taggedFrame,
	}

	if _, err := fab.Inject(inj); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	snap := fab.Snapshot()
	if len(snap.Queue) != 1 {
		t.Fatalf("len(snap.Queue) = %d, want 1", len(snap.Queue))
	}
	arr := snap.Queue[0]
	if arr.Device != "sw1" || arr.Port != "1/1/1" {
		t.Errorf("arrival location = (%q, %q), want (sw1, 1/1/1)", arr.Device, arr.Port)
	}
	if len(arr.Frame.Tags) != 1 || arr.Frame.Tags[0].VID != 10 {
		t.Errorf("arr.Frame.Tags = %v, want C-TAG 10 preserved as given", arr.Frame.Tags)
	}
}

func TestRunZeroSteps(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	inj := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Src: macH1, Dst: macH2, Payload: []byte("ping")},
	}
	if _, err := fab.Inject(inj); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	snapBefore := fab.Snapshot()
	if len(snapBefore.Queue) != 1 {
		t.Fatalf("queue before Run(0) = %d, want 1", len(snapBefore.Queue))
	}

	n := fab.Run(0)
	if n != 0 {
		t.Errorf("Run(0) returned %d, want 0", n)
	}

	snapAfter := fab.Snapshot()
	if len(snapAfter.Queue) != len(snapBefore.Queue) {
		t.Errorf("queue after Run(0) = %d, want %d", len(snapAfter.Queue), len(snapBefore.Queue))
	}
}

func TestInjectInvalidOrigins(t *testing.T) {
	fab, _, _ := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	dummyFrame := ethernet.Frame{Payload: []byte("x")}

	t.Run("AbsentHostOrSwitch", func(t *testing.T) {
		_, err := fab.Inject(fabric.Injection{
			At:     t0,
			Origin: fabric.Endpoint{Node: "ghost"},
			Frame:  dummyFrame,
		})
		if err == nil {
			t.Errorf("expected error for absent origin node")
		}
	})

	t.Run("HostWithPortName", func(t *testing.T) {
		_, err := fab.Inject(fabric.Injection{
			At:     t0,
			Origin: fabric.Endpoint{Node: "h1", Port: "eth0"},
			Frame:  dummyFrame,
		})
		if err == nil {
			t.Errorf("expected error for host with port name")
		}
	})

	t.Run("SwitchPortNotFound", func(t *testing.T) {
		_, err := fab.Inject(fabric.Injection{
			At:     t0,
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/99"},
			Frame:  dummyFrame,
		})
		if err == nil {
			t.Errorf("expected error for unknown switch port")
		}
	})
}

func TestWakePrecedesFrameAtSameInstant(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}

	mac := netaddr.MAC{0, 0, 0, 0, 0, 1}
	fab, err := fabric.New(fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports,
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 4096,
					Address:  mac,
					Ports: map[string]stp.Port{
						"1/1/1": {},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	snap := fab.Snapshot()
	var wakeTime time.Time
	for _, arr := range snap.Queue {
		if arr.Wake && arr.Device == "sw1" {
			wakeTime = arr.At
			break
		}
	}
	if wakeTime.IsZero() {
		t.Fatal("expected a wake in the queue")
	}

	_, err = fab.Inject(fabric.Injection{
		At:     wakeTime,
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame: ethernet.Frame{
			Dst: netaddr.MAC{0, 0, 0, 0, 0, 2},
			Src: mac,
		},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	for {
		entry, ok := fab.Step()
		if !ok {
			t.Fatal("queue drained before reaching wakeTime")
		}
		if entry.At.Equal(wakeTime) {
			if entry.Kind != fabric.EntryWake {
				t.Fatalf("first entry at %v was %v, want %v", wakeTime, entry.Kind, fabric.EntryWake)
			}
			break
		}
	}
}

func TestRescheduledWakeLeavesOneQueueEntry(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}

	mac := netaddr.MAC{0, 0, 0, 0, 0, 1}
	fab, err := fabric.New(fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports,
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 4096,
					Address:  mac,
					Ports: map[string]stp.Port{
						"1/1/1": {},
						"1/1/2": {},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	countWakes := func() int {
		n := 0
		for _, arr := range fab.Snapshot().Queue {
			if arr.Wake && arr.Device == "sw1" {
				n++
			}
		}
		return n
	}

	if n := countWakes(); n != 1 {
		t.Fatalf("initial queue has %d wakes for sw1, want 1", n)
	}

	fab.Step()

	if n := countWakes(); n != 1 {
		t.Fatalf("after step, queue has %d wakes for sw1, want 1", n)
	}
}

func TestSetFaultUpdatesOperationalStatusAndRefusesUnknownPair(t *testing.T) {
	fab, _, _ := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})

	snapBefore := fab.Snapshot()
	found := false
	for _, l := range snapBefore.Links {
		if l.A.Node == "sw1" && l.B.Node == "sw2" {
			if l.A.Oper != port.Up || l.B.Oper != port.Up {
				t.Fatalf("initial link not Up: A=%v, B=%v", l.A.Oper, l.B.Oper)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("cable sw1-sw2 not found")
	}

	err := fab.SetFault(
		fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
		fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
		fabric.Fault{Kind: fabric.FaultCut},
	)
	if err != nil {
		t.Fatalf("SetFault: %v", err)
	}

	snapAfter := fab.Snapshot()
	for _, l := range snapAfter.Links {
		if (l.Cable.A.Node == "sw1" && l.Cable.B.Node == "sw2") || (l.Cable.A.Node == "sw2" && l.Cable.B.Node == "sw1") {
			if l.A.Oper != port.Down || l.B.Oper != port.Down {
				t.Errorf("after SetFault cut, link ends are A=%v, B=%v, want Down", l.A.Oper, l.B.Oper)
			}
			if l.A.Reason != fabric.ReasonCut || l.B.Reason != fabric.ReasonCut {
				t.Errorf("after SetFault cut, reasons are A=%v, B=%v, want cut", l.A.Reason, l.B.Reason)
			}
		}
	}

	errUnknown := fab.SetFault(
		fabric.Endpoint{Node: "sw1", Port: "1/1/99"},
		fabric.Endpoint{Node: "sw2", Port: "1/1/99"},
		fabric.Fault{Kind: fabric.FaultCut},
	)
	if errUnknown == nil {
		t.Error("SetFault expected error for unknown endpoints, got nil")
	}
}

func TestTwoSwitchRunWithoutLayerUnchangedByWakeFacility(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})

	for _, arr := range fab.Snapshot().Queue {
		if arr.Wake {
			t.Fatalf("unexpected wake in queue on fabric without STP: %+v", arr)
		}
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	_, err := fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Src: macH1, Dst: macH2, Payload: []byte("ping")},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	for _, arr := range fab.Snapshot().Queue {
		if arr.Wake {
			t.Fatalf("unexpected wake after injection: %+v", arr)
		}
	}

	steps := fab.Run(10)
	if steps == 0 {
		t.Fatal("expected Run to take steps")
	}

	for _, j := range fab.Report() {
		for _, e := range j.Entries {
			if e.Kind == fabric.EntryWake {
				t.Errorf("found EntryWake in journey of non-STP fabric: %+v", e)
			}
		}
	}
}

func TestInjectMalformedPacketRejected(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
	gateway := netip.MustParseAddr("10.0.10.1")

	cfg := fabric.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: ports},
		},
		Hosts: map[string]fabric.Host{
			"h1": {
				Address: macH1,
				IP: &fabric.HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   gateway,
					Neighbors: map[netip.Addr]netaddr.MAC{
						gateway: {0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
					},
				},
			},
			"h2": {
				Address: macH2,
			},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				A: fabric.Endpoint{Node: "h2"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	validPacket := &fabric.Packet{
		To:       gateway,
		Protocol: 17,
		Payload:  []byte("test"),
	}

	// 1. Frame field set alongside Packet.
	_, err = fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Dst: macH2},
		Packet: validPacket,
	})
	if err == nil {
		t.Error("Inject with Frame.Dst set alongside Packet succeeded, want error")
	}

	_, err = fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Payload: []byte("frame-payload")},
		Packet: validPacket,
	})
	if err == nil {
		t.Error("Inject with Frame.Payload set alongside Packet succeeded, want error")
	}

	// 2. Switch origin with Packet.
	_, err = fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Packet: validPacket,
	})
	if err == nil {
		t.Error("Inject with switch origin and Packet succeeded, want error")
	}

	// 3. Host without an IP stack.
	_, err = fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h2"},
		Packet: validPacket,
	})
	if err == nil {
		t.Error("Inject from host without IP stack succeeded, want error")
	}

	// 4. Host origin with Port set.
	_, err = fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1", Port: "1/1/1"},
		Packet: validPacket,
	})
	if err == nil {
		t.Error("Inject from host with Port set succeeded, want error")
	}
}

func TestHostIPv6PacketToOffLinkViaGateway(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macGW := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x02}
	gateway := netip.MustParseAddr("2001:db8:1::1")
	offLinkDst := netip.MustParseAddr("2001:db8:99::1")

	cfg := fabric.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: ports},
		},
		Hosts: map[string]fabric.Host{
			"h1": {
				Address: macH1,
				IP: &fabric.HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8:1::7/64")},
					Gateway:   gateway,
					Neighbors: map[netip.Addr]netaddr.MAC{
						gateway: macGW,
					},
				},
			},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	fid, err := fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{
			To:       offLinkDst,
			Protocol: 6,
			Payload:  []byte("tcp-ipv6-payload"),
		},
	})
	if err != nil {
		t.Fatalf("Inject IPv6 packet: %v", err)
	}

	snap := fab.Snapshot()
	if len(snap.Queue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(snap.Queue))
	}

	arr := snap.Queue[0]
	if arr.FrameID != fid {
		t.Errorf("arr.FrameID = %d, want %d", arr.FrameID, fid)
	}
	if arr.Frame.Dst != macGW {
		t.Errorf("arr.Frame.Dst = %v, want gateway MAC %v", arr.Frame.Dst, macGW)
	}
	if arr.Frame.EtherType != ethernet.EtherTypeIPv6 {
		t.Errorf("arr.Frame.EtherType = %v, want IPv6", arr.Frame.EtherType)
	}

	hdr, payload, err := ip.Decode(arr.Frame.Payload)
	if err != nil {
		t.Fatalf("decode queued IPv6 payload: %v", err)
	}
	if hdr.HopLimit != 64 {
		t.Errorf("originated HopLimit = %d, want 64", hdr.HopLimit)
	}
	if hdr.Src != netip.MustParseAddr("2001:db8:1::7") {
		t.Errorf("hdr.Src = %v, want 2001:db8:1::7", hdr.Src)
	}
	if hdr.Dst != offLinkDst {
		t.Errorf("hdr.Dst = %v, want %v", hdr.Dst, offLinkDst)
	}
	if string(payload) != "tcp-ipv6-payload" {
		t.Errorf("payload = %q, want %q", string(payload), "tcp-ipv6-payload")
	}
}

func TestHostRoutingConfigDefaultRoutesPresent(t *testing.T) {
	gateway := netip.MustParseAddr("10.0.10.1")
	h := fabric.Host{
		Address: netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		IP: &fabric.HostIP{
			Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
			Gateway:   gateway,
			Neighbors: map[netip.Addr]netaddr.MAC{
				gateway: {0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
			},
		},
	}

	rtCfg, tbl := fabric.HostRoutingConfig("h1", h)
	if err := rtCfg.Validate(tbl); err != nil {
		t.Fatalf("HostRoutingConfig failed Validate: %v", err)
	}

	vrf, ok := rtCfg.VRFs[routing.DefaultVRF]
	if !ok {
		t.Fatalf("missing VRF %q in translated config", routing.DefaultVRF)
	}

	var foundDefaultRoute bool
	for _, r := range vrf.Routes {
		if r.Prefix == netip.MustParsePrefix("0.0.0.0/0") && r.NextHop == gateway {
			foundDefaultRoute = true
			break
		}
	}
	if !foundDefaultRoute {
		t.Errorf("default route 0.0.0.0/0 -> %v not found in routes: %+v", gateway, vrf.Routes)
	}
}
