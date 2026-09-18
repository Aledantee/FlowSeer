package fabric_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/udp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
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
				Medium:       fabric.MultimodeFiber,
				Fault:        cableFault,
			},
			{
				A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "h2"},
			},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
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
	fab, err := fabric.New(statedPhysical(fabric.Config{
		Switches: map[string]vswitch.Config{"sw1": {Ports: ports}},
		Hosts:    map[string]fabric.Host{"h1": {Address: macH1}, "h2": {Address: macH2}},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}, Fault: fabric.Fault{Kind: fabric.FaultCorruptEveryNth, N: 1}},
		},
	}))
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
	fab, err := fabric.New(statedPhysical(fabric.Config{
		Switches: map[string]vswitch.Config{"sw1": {Ports: ports}},
		Hosts:    map[string]fabric.Host{"h1": {Address: netaddr.MAC{0, 0, 0, 0, 0, 1}, VLAN: &vid10}, "h2": {Address: netaddr.MAC{0, 0, 0, 0, 0, 2}}},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}},
		},
	}))
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
	fab, err := fabric.New(statedPhysical(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: table()}, "sw2": {Ports: table()}, "sw3": {Ports: table()},
		},
		Hosts: map[string]fabric.Host{"h1": {Address: netaddr.MAC{0, 0, 0, 0, 0, 1}}},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw3", Port: "1/1/1"}, LengthMeters: 10},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, LengthMeters: 10},
		},
	}))
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

	steps := fab.Run(10).Steps
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
		fabric.EntryCrossing,
		fabric.EntryHop,
		fabric.EntryCrossing,
		fabric.EntryHop,
		fabric.EntryArrival,
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
		t.Errorf("host crossing = (%q, %q), want (sw1, 1/1/1)", j1.Entries[1].Device, j1.Entries[1].Port)
	}
	if j1.Entries[2].Device != "sw1" || j1.Entries[2].Port != "1/1/1" {
		t.Errorf("sw1 hop = (%q, %q), want (sw1, 1/1/1)", j1.Entries[2].Device, j1.Entries[2].Port)
	}
	if j1.Entries[2].Result.Outcome != trace.Flooded {
		t.Errorf("sw1 outcome = %v, want Flooded", j1.Entries[2].Result.Outcome)
	}
	if j1.Entries[3].Device != "sw2" || j1.Entries[3].Port != "1/1/24" {
		t.Errorf("crossing far end = (%q, %q), want (sw2, 1/1/24)", j1.Entries[3].Device, j1.Entries[3].Port)
	}
	wantLatency := 1494 * time.Nanosecond
	if j1.Entries[3].Latency != wantLatency {
		t.Errorf("crossing latency = %v, want %v", j1.Entries[3].Latency, wantLatency)
	}
	if j1.Entries[4].Device != "sw2" || j1.Entries[4].Port != "1/1/24" {
		t.Errorf("sw2 hop = (%q, %q), want (sw2, 1/1/24)", j1.Entries[4].Device, j1.Entries[4].Port)
	}
	if j1.Entries[4].Result.Outcome != trace.Flooded {
		t.Errorf("sw2 outcome = %v, want Flooded", j1.Entries[4].Result.Outcome)
	}
	if j1.Entries[5].Device != "h2" || j1.Entries[6].Device != "h2" {
		t.Errorf("arrival and delivery devices = %q, %q, want h2", j1.Entries[5].Device, j1.Entries[6].Device)
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

	stepsRev := fab.Run(10).Steps
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
	if j2.Entries[2].Result.Outcome != trace.Forwarded {
		t.Errorf("sw2 reverse hop outcome = %v, want Forwarded", j2.Entries[2].Result.Outcome)
	}
	if j2.Entries[4].Result.Outcome != trace.Forwarded {
		t.Errorf("sw1 reverse hop outcome = %v, want Forwarded", j2.Entries[4].Result.Outcome)
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

	steps := fab.Run(1).Steps
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
	wantAt := t0.Add(2870 * time.Nanosecond)
	if !q0.At.Equal(wantAt) {
		t.Errorf("queued arrival At = %v, want %v", q0.At, wantAt)
	}
	if !snap1.Clock.Equal(t0.Add(672 * time.Nanosecond)) {
		t.Errorf("snap1.Clock = %v, want %v", snap1.Clock, t0.Add(672*time.Nanosecond))
	}

	sw1Entries := snap1.Devices["sw1"].Entries
	if len(sw1Entries) != 1 || sw1Entries[0].MAC != macH1 {
		t.Errorf("sw1 FDB entries = %+v, want h1 MAC", sw1Entries)
	}
	sw2Entries := snap1.Devices["sw2"].Entries
	if len(sw2Entries) != 0 {
		t.Errorf("sw2 FDB entries = %+v, want empty", sw2Entries)
	}

	steps = fab.Run(1).Steps
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
		{device: "sw1", port: "1/1/1", at: t0.Add(672 * time.Nanosecond)},
		{device: "sw2", port: "1/1/1", at: t0.Add(1672 * time.Nanosecond)},
		{device: "sw2", port: "1/1/24", at: t0.Add(2870 * time.Nanosecond)},
		{device: "sw1", port: "1/1/24", at: t0.Add(3870 * time.Nanosecond)},
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

	fab, err := fabric.New(statedPhysical(cfg))
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

	steps := fab.Run(50).Steps
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

		fab, err := fabric.New(statedPhysical(cfg))
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

		fab, err := fabric.New(statedPhysical(cfg))
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

		if len(j.Entries) != 4 {
			t.Fatalf("len(j.Entries) = %d, want 4 (Injection, Crossing, Hop, Drop)", len(j.Entries))
		}
		crossing := j.Entries[1]
		if crossing.Kind != fabric.EntryCrossing {
			t.Errorf("crossing = %+v, want EntryCrossing", crossing)
		}
		hop := j.Entries[2]
		if hop.Kind != fabric.EntryHop || hop.Result.Outcome != trace.Dropped || hop.Result.Reason != bridge.ReasonAdmission {
			t.Errorf("hop = %+v, want Dropped with ReasonAdmission", hop)
		}
		drop := j.Entries[3]
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

	fab, err := fabric.New(statedPhysical(cfg))
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

	res := fab.Run(0)
	if res.Steps != 0 {
		t.Errorf("Run(0) returned %d steps, want 0", res.Steps)
	}
	if res.Stop != fabric.StopNotRun {
		t.Errorf("Run(0) stop = %v, want StopNotRun", res.Stop)
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

// TestInjectAtReflectorOriginNamesTheReflector proves that injecting at a
// reflector's origin reports the reflector as unable to originate an
// injection, distinct from the message an unknown node origin gets: a
// reflector is a node the fabric knows, but one with no host or switch port
// to inject at.
func TestInjectAtReflectorOriginNamesTheReflector(t *testing.T) {
	fab, err := fabric.New(statedPhysical(reflectCopyConfig(t)))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	_, err = fab.Inject(fabric.Injection{
		At:     fixedTime,
		Origin: fabric.Endpoint{Node: "r1"},
		Frame:  ethernet.Frame{Payload: []byte("x")},
	})
	if err == nil {
		t.Fatalf("expected error for reflector origin")
	}
	if !strings.Contains(err.Error(), "cannot originate an injection") {
		t.Errorf("Inject error = %q, want it to name the reflector as unable to originate an injection", err.Error())
	}
	if strings.Contains(err.Error(), "not found in fabric") {
		t.Errorf("Inject error = %q, want it not to claim the reflector is unknown to the fabric", err.Error())
	}
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
	fab, err := fabric.New(statedPhysical(fabric.Config{
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
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	snap := fab.Snapshot()
	var wakeTime time.Time
	for _, arr := range snap.Queue {
		if arr.Kind == fabric.ArrivalWake && arr.Device == "sw1" {
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
	fab, err := fabric.New(statedPhysical(fabric.Config{
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
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	countWakes := func() int {
		n := 0
		for _, arr := range fab.Snapshot().Queue {
			if arr.Kind == fabric.ArrivalWake && arr.Device == "sw1" {
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

func TestSetFaultStoresNormalizedFault(t *testing.T) {
	a := fabric.Endpoint{Node: "sw1", Port: "1/1/24"}
	b := fabric.Endpoint{Node: "sw2", Port: "1/1/24"}
	tests := []struct {
		name  string
		fault fabric.Fault
		want  fabric.Fault
	}{
		{
			name:  "empty kind",
			fault: fabric.Fault{},
			want:  fabric.Fault{Kind: fabric.FaultNone},
		},
		{
			name: "unsorted duplicate sequence",
			fault: fabric.Fault{
				Kind:     fabric.FaultLoseSequence,
				Sequence: []uint{5, 1, 5, 3},
			},
			want: fabric.Fault{
				Kind:     fabric.FaultLoseSequence,
				Sequence: []uint{1, 3, 5},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fab, _, _ := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})
			if err := fab.SetFault(a, b, test.fault); err != nil {
				t.Fatalf("SetFault: %v", err)
			}
			if len(test.fault.Sequence) > 0 {
				test.fault.Sequence[0] = 99
			}

			assertFault := func(where string, got fabric.Fault) {
				t.Helper()
				if got.Kind != test.want.Kind || got.N != test.want.N || !slices.Equal(got.Sequence, test.want.Sequence) {
					t.Errorf("%s fault = %+v, want %+v", where, got, test.want)
				}
			}
			foundLink := false
			for _, link := range fab.Links() {
				if (link.Cable.A == a && link.Cable.B == b) || (link.Cable.A == b && link.Cable.B == a) {
					foundLink = true
					assertFault("live link", link.Fault)
				}
			}
			if !foundLink {
				t.Fatal("live link is absent")
			}

			spec := fab.Spec()
			foundCable := false
			for _, cable := range spec.Cables {
				if (cable.A == a && cable.B == b) || (cable.A == b && cable.B == a) {
					foundCable = true
					assertFault("construction spec", cable.Fault)
				}
			}
			if !foundCable {
				t.Fatal("construction specification cable is absent")
			}
		})
	}
}

func TestTwoSwitchRunWithoutLayerUnchangedByWakeFacility(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})

	for _, arr := range fab.Snapshot().Queue {
		if arr.Kind == fabric.ArrivalWake {
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
		if arr.Kind == fabric.ArrivalWake {
			t.Fatalf("unexpected wake after injection: %+v", arr)
		}
	}

	steps := fab.Run(10).Steps
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

	fab, err := fabric.New(statedPhysical(cfg))
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

	fab, err := fabric.New(statedPhysical(cfg))
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

func TestTrunkBusyClockSerializesConcurrentFloods(t *testing.T) {
	b1 := port.NewBuilder()
	b1.Range("1/1/%d", 1, 24, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable1, err := b1.Build()
	if err != nil {
		t.Fatal(err)
	}

	b2 := port.NewBuilder()
	b2.Range("1/1/%d", 1, 24, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable2, err := b2.Build()
	if err != nil {
		t.Fatal(err)
	}

	vid10 := vlan.ID(10)
	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: pTable1,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "VLAN10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/2":  {PVID: &vid10, Untagged: []vlan.ID{10}},
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
							"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/24": {Tagged: []vlan.ID{10}},
						},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}},
			"h2": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}},
			"h3": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x03}},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "h3"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
				LengthMeters: 300,
				Medium:       fabric.MultimodeFiber,
			},
			{A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h2"}},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	payload46 := make([]byte, 46)

	_, err = fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
			Dst:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Payload: payload46,
		},
	})
	if err != nil {
		t.Fatalf("Inject h1: %v", err)
	}

	_, err = fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h3"},
		Frame: ethernet.Frame{
			Src:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x03},
			Dst:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02},
			Payload: payload46,
		},
	})
	if err != nil {
		t.Fatalf("Inject h3: %v", err)
	}

	step1, ok := fab.Step()
	if !ok {
		t.Fatal("step 1 returned ok=false")
	}
	step2, ok := fab.Step()
	if !ok {
		t.Fatal("step 2 returned ok=false")
	}

	if step1.At != t0.Add(672*time.Nanosecond) {
		t.Errorf("step 1 at sw1 At = %v, want %v", step1.At, t0.Add(672*time.Nanosecond))
	}
	if step2.At != t0.Add(672*time.Nanosecond) {
		t.Errorf("step 2 at sw1 At = %v, want %v", step2.At, t0.Add(672*time.Nanosecond))
	}

	trunkEP := fabric.Endpoint{Node: "sw1", Port: "1/1/24"}
	snap := fab.Snapshot()
	// Same-time frame arrivals precede dequeue events, so neither frame has
	// charged the trunk busy clock at this observation point.
	if got := snap.Queued[trunkEP]; got != 2 {
		t.Errorf("snap.Queued[%v] = %d, want 2", trunkEP, got)
	}
	if gotBusy, ok := snap.Busy[trunkEP]; ok {
		t.Errorf("snap.Busy[%v] = %v, want absent before dequeue", trunkEP, gotBusy)
	}
	for _, host := range []string{"h1", "h3"} {
		if until, ok := snap.Busy[fabric.Endpoint{Node: host}]; ok {
			t.Errorf("snap.Busy[%s] = %v, want absent: its clock is not after Clock %v", host, until, snap.Clock)
		}
	}

	var sw2Hops []fabric.Entry
	for len(sw2Hops) < 2 {
		entry, ok := fab.Step()
		if !ok {
			t.Fatal("queue exhausted before both sw2 hops")
		}
		if entry.Kind == fabric.EntryHop && entry.Device == "sw2" {
			sw2Hops = append(sw2Hops, entry)
		}
	}
	if diff := sw2Hops[1].At.Sub(sw2Hops[0].At); diff != 704*time.Nanosecond {
		t.Errorf("sw2 hops difference = %v, want 704ns (first=%v, second=%v)", diff, sw2Hops[0].At, sw2Hops[1].At)
	}

	journeys := fab.Report()
	if len(journeys) != 2 {
		t.Fatalf("len(journeys) = %d, want 2", len(journeys))
	}

	findTrunkCrossing := func(j fabric.Journey) fabric.Entry {
		for _, e := range j.Entries {
			if e.Kind == fabric.EntryCrossing && e.Device == "sw2" && e.Port == "1/1/24" {
				return e
			}
		}
		t.Fatalf("trunk crossing not found in journey %d", j.FrameID)
		return fabric.Entry{}
	}

	c1 := findTrunkCrossing(journeys[0])
	c2 := findTrunkCrossing(journeys[1])

	if c1.Wait != 0 {
		t.Errorf("first trunk crossing Wait = %v, want 0", c1.Wait)
	}
	if c2.Wait != 704*time.Nanosecond {
		t.Errorf("second trunk crossing Wait = %v, want 704ns", c2.Wait)
	}
}

func TestSerializationAndPropagationTiming(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	payload46 := make([]byte, 46)

	inj := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     macH1,
			Dst:     macH2,
			Payload: payload46,
		},
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
	if len(j.Entries) != 7 {
		t.Fatalf("len(j.Entries) = %d, want 7", len(j.Entries))
	}

	e1 := j.Entries[1]
	if e1.Kind != fabric.EntryCrossing || e1.Device != "sw1" || e1.Port != "1/1/1" {
		t.Errorf("e1 = %+v, want crossing to sw1:1/1/1", e1)
	}
	if e1.Serialization != 672*time.Nanosecond {
		t.Errorf("e1.Serialization = %v, want 672ns", e1.Serialization)
	}
	if e1.Wait != 0 {
		t.Errorf("e1.Wait = %v, want 0", e1.Wait)
	}
	if e1.Latency != 0 {
		t.Errorf("e1.Latency = %v, want 0", e1.Latency)
	}

	e2 := j.Entries[2]
	if e2.Kind != fabric.EntryHop || e2.Device != "sw1" || !e2.At.Equal(t0.Add(672*time.Nanosecond)) {
		t.Errorf("e2 = %+v, want hop on sw1 at t0+672ns", e2)
	}

	e3 := j.Entries[3]
	if e3.Kind != fabric.EntryCrossing || e3.Device != "sw2" || e3.Port != "1/1/24" {
		t.Errorf("e3 = %+v, want crossing to sw2:1/1/24", e3)
	}
	if e3.Serialization != 704*time.Nanosecond {
		t.Errorf("e3.Serialization = %v, want 704ns", e3.Serialization)
	}
	if e3.Latency != 1494*time.Nanosecond {
		t.Errorf("e3.Latency = %v, want 1494ns", e3.Latency)
	}

	e4 := j.Entries[4]
	if e4.Kind != fabric.EntryHop || e4.Device != "sw2" || !e4.At.Equal(t0.Add(2870*time.Nanosecond)) {
		t.Errorf("e4 = %+v, want hop on sw2 at t0+2870ns", e4)
	}

	e5 := j.Entries[5]
	if e5.Kind != fabric.EntryArrival || e5.Device != "h2" || !e5.At.Equal(t0.Add(3542*time.Nanosecond)) {
		t.Errorf("e5 = %+v, want arrival at h2 at t0+3542ns", e5)
	}
	e6 := j.Entries[6]
	if e6.Kind != fabric.EntryDelivery || e6.Device != "h2" || !e6.At.Equal(t0.Add(3542*time.Nanosecond)) {
		t.Errorf("e6 = %+v, want delivery to h2 at t0+3542ns", e6)
	}

	if len(j.Deliveries) != 1 {
		t.Fatalf("len(j.Deliveries) = %d, want 1", len(j.Deliveries))
	}
	if !j.Deliveries[0].At.Equal(t0.Add(3542 * time.Nanosecond)) {
		t.Errorf("delivery At = %v, want t0+3542ns", j.Deliveries[0].At)
	}
}

func TestDelayOverrideOnTrunk(t *testing.T) {
	testDelay := func(t *testing.T, delayOverride time.Duration, wantHopSw2 time.Duration) {
		b1 := port.NewBuilder()
		b1.Range("1/1/%d", 1, 24, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		pTable1, _ := b1.Build()

		b2 := port.NewBuilder()
		b2.Range("1/1/%d", 1, 24, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
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
								"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
								"1/1/24": {Tagged: []vlan.ID{10}},
							},
						},
					},
				},
			},
			Hosts: map[string]fabric.Host{
				"h1": {Address: macH1},
				"h2": {Address: macH2},
			},
			Cables: []fabric.Cable{
				{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
				{
					A:            fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
					B:            fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
					LengthMeters: 300,
					Medium:       fabric.MultimodeFiber,
					Delay:        &delayOverride,
				},
				{A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h2"}},
			},
		}

		fab, err := fabric.New(statedPhysical(cfg))
		if err != nil {
			t.Fatalf("New fabric: %v", err)
		}

		t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
		payload46 := make([]byte, 46)

		if _, err := fab.Inject(fabric.Injection{
			At:     t0,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Src:     macH1,
				Dst:     macH2,
				Payload: payload46,
			},
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}

		fab.Run(10)

		journeys := fab.Report()
		if len(journeys) != 1 {
			t.Fatalf("len(journeys) = %d, want 1", len(journeys))
		}

		hopSw2 := journeys[0].Entries[4]
		if hopSw2.Device != "sw2" || hopSw2.Kind != fabric.EntryHop {
			t.Fatalf("Entries[4] = %+v, want hop on sw2", hopSw2)
		}
		wantAt := t0.Add(wantHopSw2)
		if !hopSw2.At.Equal(wantAt) {
			t.Errorf("hop on sw2 At = %v, want %v", hopSw2.At, wantAt)
		}
	}

	t.Run("Delay 10 microseconds", func(t *testing.T) {
		testDelay(t, 10*time.Microsecond, 11376*time.Nanosecond)
	})
	t.Run("Delay 0", func(t *testing.T) {
		testDelay(t, 0, 1376*time.Nanosecond)
	})
}

func TestSinglemodeFiberTenGigabitTiming(t *testing.T) {
	eth1G10G := phy.Ethernet{SupportedSpeedsBPS: []uint64{1_000_000_000, 10_000_000_000}}

	b1 := port.NewBuilder()
	b1.Range("1/1/%d", 1, 24, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable1, _ := b1.Build()

	b2 := port.NewBuilder()
	b2.Range("1/1/%d", 1, 24, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable2, _ := b2.Build()

	vid10 := vlan.ID(10)
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: pTable1,
				Phy:   &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/24": eth1G10G}},
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
				Phy:   &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/24": eth1G10G}},
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
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
				LengthMeters: 10000,
				Medium:       fabric.SinglemodeFiber,
			},
			{A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h2"}},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	links := fab.Links()
	var trunkLink *fabric.Link
	for i := range links {
		l := &links[i]
		if (l.A.Endpoint == fabric.Endpoint{Node: "sw1", Port: "1/1/24"} && l.B.Endpoint == fabric.Endpoint{Node: "sw2", Port: "1/1/24"}) ||
			(l.B.Endpoint == fabric.Endpoint{Node: "sw1", Port: "1/1/24"} && l.A.Endpoint == fabric.Endpoint{Node: "sw2", Port: "1/1/24"}) {
			trunkLink = l
			break
		}
	}
	if trunkLink == nil {
		t.Fatal("trunk link not found")
	}
	if trunkLink.A.Speed.SpeedBPS != 10_000_000_000 {
		t.Fatalf("trunk negotiated speed = %d, want 10G", trunkLink.A.Speed.SpeedBPS)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	payload46 := make([]byte, 46)

	if _, err := fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     macH1,
			Dst:     macH2,
			Payload: payload46,
		},
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	fab.Run(10)

	journeys := fab.Report()
	if len(journeys) != 1 {
		t.Fatalf("len(journeys) = %d, want 1", len(journeys))
	}

	trunkCrossing := journeys[0].Entries[3]
	if trunkCrossing.Kind != fabric.EntryCrossing || trunkCrossing.Device != "sw2" || trunkCrossing.Port != "1/1/24" {
		t.Fatalf("Entries[3] = %+v, want trunk crossing to sw2:1/1/24", trunkCrossing)
	}
	if trunkCrossing.Serialization != 71*time.Nanosecond {
		t.Errorf("trunk crossing Serialization = %v, want 71ns", trunkCrossing.Serialization)
	}
	if trunkCrossing.Latency != 49786*time.Nanosecond {
		t.Errorf("trunk crossing Latency = %v, want 49786ns", trunkCrossing.Latency)
	}
}

func TestHostBusyClockSerializesSubsequentInjections(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	payload46 := make([]byte, 46)

	_, err := fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     macH1,
			Dst:     macH2,
			Payload: payload46,
		},
	})
	if err != nil {
		t.Fatalf("Inject 1: %v", err)
	}

	_, err = fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     macH1,
			Dst:     macH2,
			Payload: payload46,
		},
	})
	if err != nil {
		t.Fatalf("Inject 2: %v", err)
	}

	step1, ok := fab.Step()
	if !ok {
		t.Fatal("step 1 ok=false")
	}
	dequeue, ok := fab.Step()
	if !ok {
		t.Fatal("step 2 ok=false")
	}
	// The host dequeue is now an observable step between the two frame arrivals.
	if dequeue.Kind != fabric.EntryDequeue || dequeue.Device != "h1" {
		t.Fatalf("step 2 = %+v, want h1 dequeue", dequeue)
	}
	step2, ok := fab.Step()
	if !ok {
		t.Fatal("step 3 ok=false")
	}

	if !step1.At.Equal(t0.Add(672 * time.Nanosecond)) {
		t.Errorf("step 1 At = %v, want t0+672ns", step1.At)
	}
	if !step2.At.Equal(t0.Add(1344 * time.Nanosecond)) {
		t.Errorf("step 2 At = %v, want t0+1344ns", step2.At)
	}

	journeys := fab.Report()
	if len(journeys) != 2 {
		t.Fatalf("len(journeys) = %d, want 2", len(journeys))
	}

	j2HostCrossing := journeys[1].Entries[1]
	if j2HostCrossing.Kind != fabric.EntryCrossing {
		t.Fatalf("j2.Entries[1] = %+v, want EntryCrossing", j2HostCrossing)
	}
	if j2HostCrossing.Wait != 672*time.Nanosecond {
		t.Errorf("second journey host crossing Wait = %v, want 672ns", j2HostCrossing.Wait)
	}
}

func TestInjectFromHostWithReachExceededCableRecordsDrop(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable, _ := b.Build()

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	cfg := statedPhysical(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: pTable},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
		},
		Cables: []fabric.Cable{
			{
				A:            fabric.Endpoint{Node: "h1"},
				B:            fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				LengthMeters: 300,
				Medium:       fabric.TwistedPair,
			},
		},
	})

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fid, err := fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     macH1,
			Dst:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
			Payload: make([]byte, 46),
		},
	})
	if err != nil {
		t.Fatalf("Inject from a host on a Down link: %v", err)
	}
	if len(fab.Snapshot().Queue) != 0 {
		t.Errorf("Snapshot().Queue has %d items, want 0", len(fab.Snapshot().Queue))
	}

	journeys := fab.Report()
	if len(journeys) != 1 || journeys[0].FrameID != fid {
		t.Fatalf("Report() = %+v, want one journey for frame %d", journeys, fid)
	}
	entries := journeys[0].Entries
	if len(entries) != 2 || entries[0].Kind != fabric.EntryInjection {
		t.Fatalf("journey entries = %+v, want injection then drop", entries)
	}
	drop := entries[1]
	if drop.Kind != fabric.EntryDrop || drop.Reason != fabric.ReasonReachExceeded || drop.Device != "h1" || !drop.At.Equal(t0) {
		t.Errorf("second entry = %+v, want drop at h1 at t0 with reason reach-exceeded", drop)
	}
	if drop.Cable == nil || drop.Cable.B != (fabric.Endpoint{Node: "sw1", Port: "1/1/1"}) {
		t.Errorf("drop cable = %+v, want the host's cable", drop.Cable)
	}
}

func TestInjectFromHostOnUnknownLinkRecordsUnresolved(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable, _ := b.Build()

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	fab, err := fabric.New(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: pTable},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Medium: fabric.TwistedPair},
		},
	})
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fid, err := fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Src: macH1, Dst: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}},
	})
	if err != nil {
		t.Fatalf("Inject from a host on an Unknown link: %v", err)
	}
	if len(fab.Snapshot().Queue) != 0 {
		t.Errorf("Snapshot().Queue has %d items, want 0", len(fab.Snapshot().Queue))
	}

	journeys := fab.Report()
	if len(journeys) != 1 || journeys[0].FrameID != fid {
		t.Fatalf("Report() = %+v, want one journey for frame %d", journeys, fid)
	}
	entries := journeys[0].Entries
	if len(entries) != 2 {
		t.Fatalf("journey entries = %+v, want injection then unresolved", entries)
	}
	if got := entries[1]; got.Kind != fabric.EntryUnresolved || got.Reason != phy.ReasonCapabilityUnknown || got.Device != "h1" || got.Cable == nil {
		t.Errorf("second entry = %+v, want unresolved at h1 with reason capability-unknown and the cable", got)
	}
	if len(journeys[0].Deliveries) != 0 {
		t.Errorf("deliveries = %+v, want none", journeys[0].Deliveries)
	}
}

func TestBPDUCrossingCarriesSerialization(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports1, err := b1.Build()
	if err != nil {
		t.Fatal(err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports2, err := b2.Build()
	if err != nil {
		t.Fatal(err)
	}

	mac1 := netaddr.MAC{0, 0, 0, 0, 1, 1}
	fab, err := fabric.New(statedPhysical(fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports1,
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 4096,
					Address:  mac1,
					Ports: map[string]stp.Port{
						"1/1/1": {},
					},
				},
			},
			"sw2": {
				Ports:  ports2,
				Bridge: &bridge.Config{},
			},
		},
		Cables: []fabric.Cable{
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				LengthMeters: 100,
				Medium:       fabric.TwistedPair,
			},
		},
	}))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	for {
		entry, ok := fab.Step()
		if !ok || entry.Device == "sw2" {
			break
		}
	}

	journeys := fab.Report()
	if len(journeys) == 0 {
		t.Fatal("expected at least one journey")
	}

	var bpduJourney *fabric.Journey
	for i := range journeys {
		if journeys[i].Protocol {
			bpduJourney = &journeys[i]
			break
		}
	}
	if bpduJourney == nil {
		t.Fatal("expected a protocol journey")
	}

	var crossing *fabric.Entry
	for i := range bpduJourney.Entries {
		if bpduJourney.Entries[i].Kind == fabric.EntryCrossing {
			crossing = &bpduJourney.Entries[i]
			break
		}
	}
	if crossing == nil {
		t.Fatal("expected crossing entry in BPDU journey")
	}
	if crossing.Serialization != 672*time.Nanosecond {
		t.Errorf("crossing.Serialization = %v, want 672ns", crossing.Serialization)
	}
	if crossing.Latency != 521*time.Nanosecond {
		t.Errorf("crossing.Latency = %v, want 521ns", crossing.Latency)
	}
}

func TestSnapshotRelayCounters(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 3, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}

	vid10 := vlan.ID(10)
	cfg := fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: ports,
				Bridge: &bridge.Config{
					MaxEntries: 2,
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "VLAN10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
						},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: netaddr.MAC{0, 0, 0, 0, 0, 1}},
			"h2": {Address: netaddr.MAC{0, 0, 0, 0, 0, 2}},
			"h3": {Address: netaddr.MAC{0, 0, 0, 0, 0, 3}},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
			{A: fabric.Endpoint{Node: "h3"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macB := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
	macC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x03}
	macDst := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

	injections := []fabric.Injection{
		{
			At:     t0,
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			Frame:  ethernet.Frame{Dst: macDst, Src: macA},
		},
		{
			At:     t0.Add(time.Second),
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
			Frame:  ethernet.Frame{Dst: macDst, Src: macB},
		},
		{
			At:     t0.Add(2 * time.Second),
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			Frame:  ethernet.Frame{Dst: macDst, Src: macC},
		},
	}

	for _, inj := range injections {
		if _, err := fab.Inject(inj); err != nil {
			t.Fatalf("Inject at %v: %v", inj.At, err)
		}
	}

	fab.Run(10)

	snap := fab.Snapshot()
	sw1, ok := snap.Devices["sw1"]
	if !ok {
		t.Fatal("missing sw1 in snapshot devices")
	}

	if sw1.RelayCounters.Learned != 3 {
		t.Errorf("RelayCounters.Learned = %d, want 3", sw1.RelayCounters.Learned)
	}
	if sw1.RelayCounters.Evicted != 1 {
		t.Errorf("RelayCounters.Evicted = %d, want 1", sw1.RelayCounters.Evicted)
	}
}

func TestQueuedFrameOnCutCableRecordsLossAndDrains(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{})
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	frame := ethernet.Frame{Src: macH1, Dst: macH2, Payload: make([]byte, 46)}

	if _, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatalf("Inject first frame: %v", err)
	}
	queuedID, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame})
	if err != nil {
		t.Fatalf("Inject queued frame: %v", err)
	}
	hostEnd := fabric.Endpoint{Node: "h1"}
	switchEnd := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	if err := fab.SetFault(hostEnd, switchEnd, fabric.Fault{Kind: fabric.FaultCut}); err != nil {
		t.Fatalf("SetFault: %v", err)
	}
	fab.Run(50)

	for _, journey := range fab.Report() {
		if journey.FrameID != queuedID {
			continue
		}
		for _, entry := range journey.Entries {
			if entry.Kind == fabric.EntryLoss && entry.Reason == fabric.ReasonCableLoss {
				if got := fab.Snapshot().Queued[hostEnd]; got != 0 {
					t.Errorf("queued host frames = %d, want 0", got)
				}
				return
			}
		}
	}
	t.Error("queued frame has no cable-loss entry")
}

func TestInjectRefusesTimeBeforeRunningClock(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{})
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	frame := ethernet.Frame{Src: macH1, Dst: macH2, Payload: make([]byte, 46)}
	if _, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatalf("Inject initial frame: %v", err)
	}
	if _, ok := fab.Step(); !ok {
		t.Fatal("Step returned no entry")
	}
	before := fab.Snapshot()

	if _, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err == nil {
		t.Fatal("Inject before running clock succeeded, want error")
	}
	after := fab.Snapshot()
	if len(after.Queue) != len(before.Queue) || len(after.Queued) != len(before.Queued) {
		t.Errorf("rejected injection changed pending work: before=%+v after=%+v", before, after)
	}
}

var (
	reflectR1Address = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x01, 0xaa}
	reflectH1Address = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x01, 0x01}
	reflectH2Address = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x01, 0x02}
	reflectH3Address = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x01, 0x03}
)

// reflectCopyConfig cables a reflector directly to three hosts, one per
// attachment, so each attachment's fate is unambiguous: no switch and no
// shared port stand between a copy and its destination. Attachment "a" is
// the arrival side (VLAN 10); "b" (VLAN 20) has an IPv4 address and answers
// on h2; "c" (VLAN 30) carries only an IPv6 address, so an IPv4 query gives
// it no copy.
func reflectCopyConfig(t *testing.T) fabric.Config {
	t.Helper()
	vid10, vid20, vid30 := vlan.ID(10), vlan.ID(20), vlan.ID(30)

	return fabric.Config{
		Hosts: map[string]fabric.Host{
			"h1": {Address: reflectH1Address, VLAN: &vid10},
			"h2": {
				Address: reflectH2Address, VLAN: &vid20,
				IP:     &fabric.HostIP{Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.5/24")}},
				Accept: fabric.HostAccept{Multicast: []netaddr.MAC{reflectorGroupMAC}},
			},
			"h3": {Address: reflectH3Address, VLAN: &vid30},
		},
		Reflectors: map[string]fabric.Reflector{
			"r1": {
				Address: reflectR1Address,
				Ports:   map[string]phy.Ethernet{"rp1": {}, "rp2": {}, "rp3": {}},
				Attachments: map[string]fabric.Attachment{
					"a": {Port: "rp1", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")}},
					"b": {Port: "rp2", VLAN: &vid20, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.9/24")}},
					"c": {Port: "rp3", VLAN: &vid30, Addresses: []netip.Prefix{netip.MustParsePrefix("fd00::9/64")}},
				},
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "r1", Port: "rp1"}},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "r1", Port: "rp2"}},
			{A: fabric.Endpoint{Node: "h3"}, B: fabric.Endpoint{Node: "r1", Port: "rp3"}},
		},
	}
}

func journeyHasKind(entries []fabric.Entry, kind fabric.EntryKind) bool {
	return slices.ContainsFunc(entries, func(e fabric.Entry) bool { return e.Kind == kind })
}

// TestReflectorOriginatesACopyPerOtherAttachmentAndDropsWithoutAnAddress
// proves that an mDNS query arriving on one attachment produces exactly one
// copy, onto the other attachment whose address family it shares, rebuilt
// end to end, and a drop naming the attachment with no address of that
// family instead of a second copy.
func TestReflectorOriginatesACopyPerOtherAttachmentAndDropsWithoutAnAddress(t *testing.T) {
	fab, err := fabric.New(statedPhysical(reflectCopyConfig(t)))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}
	frame := mdnsFrame(t, reflectH1Address, nil, reflectorGroupMAC, reflectorGroupAddr, 17, 5353)
	parentID, err := fab.Inject(fabric.Injection{At: fixedTime, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(20)

	journeys := fab.Report()
	var parent fabric.Journey
	for _, j := range journeys {
		if j.FrameID == parentID {
			parent = j
		}
	}
	if parent.FrameID == 0 {
		t.Fatalf("parent journey %d not found", parentID)
	}

	if !slices.ContainsFunc(parent.Entries, func(e fabric.Entry) bool {
		return e.Kind == fabric.EntryDrop && e.Device == "r1" && e.Port == "rp3" && e.Reason == fabric.ReasonReflectorNoAddress
	}) {
		t.Errorf("parent entries = %+v, want a no-address drop naming rp3", parent.Entries)
	}

	var copyJourney fabric.Journey
	for _, j := range journeys {
		if j.FrameID != parentID {
			copyJourney = j
			break
		}
	}
	if copyJourney.FrameID == 0 {
		t.Fatalf("no copy journey found among: %+v", journeys)
	}

	ipHeader, ipPayload, err := ip.Decode(copyJourney.Injection.Frame.Payload)
	if err != nil {
		t.Fatalf("decode copy IP header: %v", err)
	}
	if !ipHeader.Src.Is4() || ipHeader.Src != netip.MustParseAddr("10.0.20.9") {
		t.Errorf("copy IP src = %s, want attachment b's address", ipHeader.Src)
	}
	if ipHeader.HopLimit != 255 {
		t.Errorf("copy hop limit = %d, want 255", ipHeader.HopLimit)
	}
	udpHeader, _, err := udp.Decode(ipPayload)
	if err != nil {
		t.Fatalf("decode copy UDP header: %v", err)
	}
	if udpHeader.SrcPort != 5353 {
		t.Errorf("copy UDP source port = %d, want 5353", udpHeader.SrcPort)
	}
	wantTags := []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20}}
	if !slices.Equal(copyJourney.Injection.Frame.Tags, wantTags) {
		t.Errorf("copy tags = %+v, want %+v", copyJourney.Injection.Frame.Tags, wantTags)
	}
	if copyJourney.Injection.Frame.Src != reflectR1Address {
		t.Errorf("copy source MAC = %s, want the reflector's own %s", copyJourney.Injection.Frame.Src, reflectR1Address)
	}

	if len(copyJourney.Deliveries) != 1 || copyJourney.Deliveries[0].Host != "h2" {
		t.Fatalf("copy deliveries = %+v, want h2 to decode it", copyJourney.Deliveries)
	}
	if journeyHasKind(copyJourney.Entries, fabric.EntryUnresolved) {
		t.Errorf("copy entries = %+v, want no undecodable arrival at h2", copyJourney.Entries)
	}
}

// reflectLoopConfig cables two reflectors directly to each other on a trunk
// carrying VLANs 10 and 20, with h1 feeding VLAN 10 queries into r1's own
// attachment on a separate port. This exercises a two-reflector, two-VLAN
// loop, and it is also the reflector-to-reflector cable the Decisions
// require the far end to enqueue rather than call inline: r1 and r2 are
// cabled to each other with no switch between them.
func reflectLoopConfig(t *testing.T) fabric.Config {
	t.Helper()
	vid10, vid20 := vlan.ID(10), vlan.ID(20)

	return fabric.Config{
		Hosts: map[string]fabric.Host{
			"h1": {Address: reflectH1Address, VLAN: &vid10},
		},
		Reflectors: map[string]fabric.Reflector{
			"r1": {
				Address: reflectR1Address,
				Ports:   map[string]phy.Ethernet{"up": {}, "trunk": {}},
				Attachments: map[string]fabric.Attachment{
					"up":  {Port: "up", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")}},
					"t10": {Port: "trunk", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.10/24")}},
					"t20": {Port: "trunk", VLAN: &vid20, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.10/24")}},
				},
			},
			"r2": {
				Address: netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x02, 0xbb},
				Ports:   map[string]phy.Ethernet{"trunk": {}},
				Attachments: map[string]fabric.Attachment{
					"t10": {Port: "trunk", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.20/24")}},
					"t20": {Port: "trunk", VLAN: &vid20, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.20/24")}},
				},
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "r1", Port: "up"}},
			{A: fabric.Endpoint{Node: "r1", Port: "trunk"}, B: fabric.Endpoint{Node: "r2", Port: "trunk"}},
		},
	}
}

// TestTwoReflectorsSharingTwoVLANsLoopAndHaltOnBudget proves that r1 and r2
// keep reflecting each other's copies back and forth over their direct
// trunk, an EntryLoop lands once entered is exhausted, and the run stops on
// Run's budget rather than draining the queue or overflowing the stack.
func TestTwoReflectorsSharingTwoVLANsLoopAndHaltOnBudget(t *testing.T) {
	fab, err := fabric.New(statedPhysical(reflectLoopConfig(t)))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}
	frame := mdnsFrame(t, reflectH1Address, nil, reflectorGroupMAC, reflectorGroupAddr, 17, 5353)
	if _, err := fab.Inject(fabric.Injection{At: fixedTime, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	const budget = 40
	steps := fab.Run(budget).Steps
	if steps != budget {
		t.Fatalf("Run(%d) = %d, want the full budget: the loop should not drain the queue", budget, steps)
	}
	if err := fab.Err(); err != nil {
		t.Fatalf("Err() = %v, want no scheduling fault", err)
	}
	if got := fab.Snapshot().Queue; len(got) == 0 {
		t.Error("queue empty after the loop's budget, want pending work still queued")
	}

	foundLoop := false
	for _, j := range fab.Report() {
		if journeyHasKind(j.Entries, fabric.EntryLoop) {
			foundLoop = true
		}
	}
	if !foundLoop {
		t.Error("no journey carries an EntryLoop, want the two-reflector loop to surface")
	}
}

// reflectSiblingConfig cables one reflector to a switch over a single trunk
// port carrying three VLANs, with h1 feeding VLAN 10 into the switch. The
// two sibling copies r1 originates both leave via the same trunk port and
// arrive at the same switch port endpoint, which is exactly the case a
// shared (rather than seeded) re-entry set reports as a false loop on the
// second sibling.
func reflectSiblingConfig(t *testing.T) fabric.Config {
	t.Helper()
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	vid10, vid20, vid30 := vlan.ID(10), vlan.ID(20), vlan.ID(30)

	return fabric.Config{
		Switches: map[string]vswitch.Config{"sw1": {Ports: mustTable(t, b)}},
		Hosts: map[string]fabric.Host{
			"h1": {Address: reflectH1Address, VLAN: &vid10},
		},
		Reflectors: map[string]fabric.Reflector{
			"r1": {
				Address: reflectR1Address,
				Ports:   map[string]phy.Ethernet{"trunk": {}},
				Attachments: map[string]fabric.Attachment{
					"a": {Port: "trunk", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")}},
					"b": {Port: "trunk", VLAN: &vid20, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.9/24")}},
					"c": {Port: "trunk", VLAN: &vid30, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.30.9/24")}},
				},
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "r1", Port: "trunk"}},
		},
	}
}

// TestReflectorSiblingCopiesAreNotALoop proves that two sibling copies
// crossing into the same next-hop endpoint from the same attachment are not
// a loop. Seeding each copy's entered set from a clone of the parent's is
// what keeps them apart; sharing one set across the family would mark the
// switch port entered when the first sibling arrives and falsely flag the
// second.
func TestReflectorSiblingCopiesAreNotALoop(t *testing.T) {
	fab, err := fabric.New(statedPhysical(reflectSiblingConfig(t)))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}
	frame := mdnsFrame(t, reflectH1Address, nil, reflectorGroupMAC, reflectorGroupAddr, 17, 5353)
	parentID, err := fab.Inject(fabric.Injection{At: fixedTime, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(20)

	journeys := fab.Report()
	var copies []fabric.Journey
	for _, j := range journeys {
		if j.FrameID != parentID {
			copies = append(copies, j)
		}
	}
	if len(copies) != 2 {
		t.Fatalf("copies = %d, want exactly 2 siblings: %+v", len(copies), copies)
	}
	for _, copy := range copies {
		if journeyHasKind(copy.Entries, fabric.EntryLoop) {
			t.Errorf("sibling copy %d entries = %+v, want no EntryLoop", copy.FrameID, copy.Entries)
		}
	}
}
