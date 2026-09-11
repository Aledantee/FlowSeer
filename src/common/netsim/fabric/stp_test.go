package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

type portStatus struct {
	role  stp.Role
	state stp.State
}

func extractRoles(snap fabric.Snapshot) map[string]map[string]portStatus {
	res := make(map[string]map[string]portStatus)
	for devName, dev := range snap.Devices {
		if dev.Roles == nil {
			continue
		}
		res[devName] = make(map[string]portStatus)
		for pName, info := range dev.Roles {
			res[devName][pName] = portStatus{
				role:  info.Role,
				state: info.State,
			}
		}
	}

	return res
}

func rolesAgree(a, b map[string]map[string]portStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for dev, portsA := range a {
		portsB, ok := b[dev]
		if !ok || len(portsA) != len(portsB) {
			return false
		}
		for p, statusA := range portsA {
			if statusB, ok := portsB[p]; !ok || statusA != statusB {
				return false
			}
		}
	}

	return true
}

func newThreeSwitchRingTopology(t *testing.T) (*fabric.Fabric, time.Time, map[string]netaddr.MAC) {
	t.Helper()

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	newPorts := func() port.Table {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		return tbl
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

	macs := map[string]netaddr.MAC{
		"sw1": {0, 0, 0, 0, 1, 1},
		"sw2": {0, 0, 0, 0, 1, 2},
		"sw3": {0, 0, 0, 0, 1, 3},
		"h2":  {0, 0, 0, 0, 2, 2},
		"h3":  {0, 0, 0, 0, 2, 3},
	}

	cfg := fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  newPorts(),
				Bridge: newBridgeCfg(),
				STP: &stp.Config{
					Priority: 4096,
					Address:  macs["sw1"],
					Ports: map[string]stp.Port{
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
			"sw2": {
				Ports:  newPorts(),
				Bridge: newBridgeCfg(),
				STP: &stp.Config{
					Priority: 12288,
					Address:  macs["sw2"],
					Ports: map[string]stp.Port{
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
			"sw3": {
				Ports:  newPorts(),
				Bridge: newBridgeCfg(),
				STP: &stp.Config{
					Priority: 8192,
					Address:  macs["sw3"],
					Ports: map[string]stp.Port{
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h2": {Address: macs["h2"]},
			"h3": {Address: macs["h3"]},
		},
		Cables: []fabric.Cable{
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				LengthMeters: 1.0,
			},
			{
				A:            fabric.Endpoint{Node: "sw2", Port: "1/1/3"},
				B:            fabric.Endpoint{Node: "sw3", Port: "1/1/2"},
				LengthMeters: 1.0,
			},
			{
				A:            fabric.Endpoint{Node: "sw3", Port: "1/1/3"},
				B:            fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
				LengthMeters: 1.0,
			},
			{
				A:            fabric.Endpoint{Node: "h2"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				LengthMeters: 1.0,
			},
			{
				A:            fabric.Endpoint{Node: "h3"},
				B:            fabric.Endpoint{Node: "sw3", Port: "1/1/1"},
				LengthMeters: 1.0,
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	return fab, t0, macs
}

func TestRingConvergenceAndHostDelivery(t *testing.T) {
	fab, _, macs := newThreeSwitchRingTopology(t)

	fab.Run(200)

	snap1 := fab.Snapshot()
	fab.Run(1)
	snap2 := fab.Snapshot()

	if !rolesAgree(extractRoles(snap1), extractRoles(snap2)) {
		t.Fatal("consecutive snapshots after Run(200) do not agree")
	}

	snap := snap2

	sw2P2 := snap.Devices["sw2"].Roles["1/1/2"]
	if sw2P2.Role != stp.RoleRoot || sw2P2.State != stp.StateForwarding {
		t.Errorf("sw2 1/1/2 = (%v, %v), want (Root, Forwarding)", sw2P2.Role, sw2P2.State)
	}

	sw3P3 := snap.Devices["sw3"].Roles["1/1/3"]
	if sw3P3.Role != stp.RoleRoot || sw3P3.State != stp.StateForwarding {
		t.Errorf("sw3 1/1/3 = (%v, %v), want (Root, Forwarding)", sw3P3.Role, sw3P3.State)
	}

	sw3P2 := snap.Devices["sw3"].Roles["1/1/2"]
	sw2P3 := snap.Devices["sw2"].Roles["1/1/3"]
	if sw3P2.Role != stp.RoleDesignated || sw3P2.State != stp.StateForwarding {
		t.Errorf("sw3 1/1/2 = (%v, %v), want (Designated, Forwarding)", sw3P2.Role, sw3P2.State)
	}
	if sw2P3.Role != stp.RoleAlternate || sw2P3.State != stp.StateDiscarding {
		t.Errorf("sw2 1/1/3 = (%v, %v), want (Alternate, Discarding)", sw2P3.Role, sw2P3.State)
	}

	fid, err := fab.Inject(fabric.Injection{
		At:     snap.Clock,
		Origin: fabric.Endpoint{Node: "h2"},
		Frame: ethernet.Frame{
			Src:     macs["h2"],
			Dst:     macs["h3"],
			Payload: []byte("ping"),
		},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	fab.Run(50)

	var frameJourney *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID == fid {
			cp := j
			frameJourney = &cp
			break
		}
	}
	if frameJourney == nil {
		t.Fatalf("journey for frame %d not found", fid)
	}

	if len(frameJourney.Deliveries) != 1 {
		for _, e := range frameJourney.Entries {
			t.Logf("Entry: Kind=%v At=%v Device=%v Port=%v Reason=%v Result=%+v", e.Kind, e.At, e.Device, e.Port, e.Reason, e.Result)
		}
		t.Fatalf("deliveries count = %d, want 1", len(frameJourney.Deliveries))
	}
	if frameJourney.Deliveries[0].Host != "h3" {
		t.Errorf("delivered to %q, want h3", frameJourney.Deliveries[0].Host)
	}

	for _, e := range frameJourney.Entries {
		if e.Kind == fabric.EntryLoop {
			t.Errorf("found loop entry in journey: %+v", e)
		}
	}
}

func TestObservableStepConvergence(t *testing.T) {
	fab, t0, _ := newThreeSwitchRingTopology(t)

	snap0 := fab.Snapshot()
	cabledPorts := []struct {
		dev  string
		port string
	}{
		{"sw1", "1/1/2"},
		{"sw1", "1/1/3"},
		{"sw2", "1/1/2"},
		{"sw2", "1/1/3"},
		{"sw3", "1/1/2"},
		{"sw3", "1/1/3"},
	}
	for _, cp := range cabledPorts {
		info := snap0.Devices[cp.dev].Roles[cp.port]
		if info.Role != stp.RoleDesignated || info.State != stp.StateDiscarding {
			t.Errorf("%s %s in initial snapshot = (%v, %v), want (Designated, Discarding)", cp.dev, cp.port, info.Role, info.State)
		}
	}

	var facingSw1ForwardingAt time.Time

	limit := t0.Add(time.Millisecond)

	for step := 1; step <= 200; step++ {
		_, ok := fab.Step()
		if !ok {
			break
		}

		snap := fab.Snapshot()
		sw1P2 := snap.Devices["sw1"].Roles["1/1/2"]
		sw1P3 := snap.Devices["sw1"].Roles["1/1/3"]
		sw2P2 := snap.Devices["sw2"].Roles["1/1/2"]
		sw3P3 := snap.Devices["sw3"].Roles["1/1/3"]

		if sw1P2.State == stp.StateForwarding &&
			sw1P3.State == stp.StateForwarding &&
			sw2P2.State == stp.StateForwarding &&
			sw3P3.State == stp.StateForwarding {
			if facingSw1ForwardingAt.IsZero() {
				facingSw1ForwardingAt = snap.Clock
			}
			break
		}
	}

	sawProtocolCrossing := false
	for _, j := range fab.Report() {
		if j.Protocol {
			for _, e := range j.Entries {
				if e.Kind == fabric.EntryCrossing {
					sawProtocolCrossing = true
					if e.Serialization <= 0 {
						t.Errorf("expected BPDU crossing serialization > 0, got %v", e.Serialization)
					}
					break
				}
			}
		}
	}

	if !sawProtocolCrossing {
		t.Error("expected to see crossing entries as BPDUs cross cables")
	}

	if facingSw1ForwardingAt.IsZero() {
		t.Fatal("ports facing sw1 did not reach Forwarding within 200 steps")
	}
	if facingSw1ForwardingAt.After(limit) {
		t.Errorf("ports facing sw1 reached Forwarding at %v, want at or before %v", facingSw1ForwardingAt, limit)
	}

	sawProtocolJourney := false
	for _, j := range fab.Report() {
		if j.Protocol {
			sawProtocolJourney = true
			break
		}
	}
	if !sawProtocolJourney {
		t.Error("expected journeys marked Protocol")
	}
}

func TestCutCableReconvergence(t *testing.T) {
	fab, _, macs := newThreeSwitchRingTopology(t)

	fab.Run(200)

	fid1, err := fab.Inject(fabric.Injection{
		At:     fab.Snapshot().Clock,
		Origin: fabric.Endpoint{Node: "h2"},
		Frame: ethernet.Frame{
			Src:     macs["h2"],
			Dst:     macs["h3"],
			Payload: []byte("first-ping"),
		},
	})
	if err != nil {
		t.Fatalf("Inject first frame: %v", err)
	}
	fab.Run(20)

	sw3EntriesBefore := fab.Snapshot().Devices["sw3"].Entries
	learnedOldPath := false
	for _, e := range sw3EntriesBefore {
		if e.MAC == macs["h2"] && e.Port == "1/1/3" {
			learnedOldPath = true
			break
		}
	}
	if !learnedOldPath {
		t.Errorf("sw3 did not learn h2 on old path port 1/1/3; entries=%+v", sw3EntriesBefore)
	}

	err = fab.SetFault(
		fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
		fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
		fabric.Fault{Kind: fabric.FaultCut},
	)
	if err != nil {
		t.Fatalf("SetFault cut: %v", err)
	}

	fab.Run(200)

	snap := fab.Snapshot()

	sw2P3 := snap.Devices["sw2"].Roles["1/1/3"]
	if sw2P3.Role != stp.RoleRoot || sw2P3.State != stp.StateForwarding {
		t.Errorf("sw2 1/1/3 after cut = (%v, %v), want (Root, Forwarding)", sw2P3.Role, sw2P3.State)
	}

	fid2, err := fab.Inject(fabric.Injection{
		At:     snap.Clock,
		Origin: fabric.Endpoint{Node: "h2"},
		Frame: ethernet.Frame{
			Src:     macs["h2"],
			Dst:     macs["h3"],
			Payload: []byte("second-ping"),
		},
	})
	if err != nil {
		t.Fatalf("Inject second frame: %v", err)
	}
	fab.Run(20)

	sw3EntriesAfter := fab.Snapshot().Devices["sw3"].Entries
	for _, e := range sw3EntriesAfter {
		if e.MAC == macs["h2"] && e.Port == "1/1/3" {
			t.Errorf("sw3 still has stale dynamic entry on 1/1/3: %+v", e)
		}
	}
	learnedNewPath := false
	for _, e := range sw3EntriesAfter {
		if e.MAC == macs["h2"] && e.Port == "1/1/2" {
			learnedNewPath = true
			break
		}
	}
	if !learnedNewPath {
		t.Errorf("sw3 did not learn h2 on new path port 1/1/2; entries=%+v", sw3EntriesAfter)
	}

	sw2EntriesAfter := fab.Snapshot().Devices["sw2"].Entries
	for _, e := range sw2EntriesAfter {
		if e.Port == "1/1/2" {
			t.Errorf("sw2 still has dynamic entry on cut port 1/1/2: %+v", e)
		}
	}

	var frameJourney2 *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID == fid2 {
			cp := j
			frameJourney2 = &cp
			break
		}
	}
	if frameJourney2 == nil {
		t.Fatalf("journey for second frame %d not found", fid2)
	}
	if len(frameJourney2.Deliveries) != 1 {
		t.Fatalf("second frame deliveries count = %d, want 1", len(frameJourney2.Deliveries))
	}
	if frameJourney2.Deliveries[0].Host != "h3" {
		t.Errorf("second frame delivered to %q, want h3", frameJourney2.Deliveries[0].Host)
	}
	_ = fid1
}

func TestHubTransparentInRing(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	newSwitchPorts := func() port.Table {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build switch ports: %v", err)
		}
		return tbl
	}

	newHubPorts := func() port.Table {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build hub ports: %v", err)
		}
		return tbl
	}

	vid10 := vlan.ID(10)
	newBridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "VLAN10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {Tagged: []vlan.ID{10}},
					"1/1/3": {Tagged: []vlan.ID{10}},
				},
			},
		}
	}

	macs := map[string]netaddr.MAC{
		"sw1": {0, 0, 0, 0, 1, 1},
		"sw2": {0, 0, 0, 0, 1, 2},
		"sw3": {0, 0, 0, 0, 1, 3},
		"h2":  {0, 0, 0, 0, 2, 2},
		"h3":  {0, 0, 0, 0, 2, 3},
	}

	cfg := fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  newSwitchPorts(),
				Bridge: newBridgeCfg(),
				STP: &stp.Config{
					Priority: 4096,
					Address:  macs["sw1"],
					Ports: map[string]stp.Port{
						"1/1/1": {},
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
			"sw2": {
				Ports:  newSwitchPorts(),
				Bridge: newBridgeCfg(),
				STP: &stp.Config{
					Priority: 12288,
					Address:  macs["sw2"],
					Ports: map[string]stp.Port{
						"1/1/1": {},
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
			"sw3": {
				Ports:  newSwitchPorts(),
				Bridge: newBridgeCfg(),
				STP: &stp.Config{
					Priority: 8192,
					Address:  macs["sw3"],
					Ports: map[string]stp.Port{
						"1/1/1": {},
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
			"hub": {
				Ports: newHubPorts(),
			},
		},
		Hosts: map[string]fabric.Host{
			"h2": {Address: macs["h2"]},
			"h3": {Address: macs["h3"]},
		},
		Cables: []fabric.Cable{
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				LengthMeters: 1.0,
			},
			{
				A:            fabric.Endpoint{Node: "sw2", Port: "1/1/3"},
				B:            fabric.Endpoint{Node: "hub", Port: "1/1/1"},
				LengthMeters: 1.0,
			},
			{
				A:            fabric.Endpoint{Node: "hub", Port: "1/1/2"},
				B:            fabric.Endpoint{Node: "sw3", Port: "1/1/2"},
				LengthMeters: 1.0,
			},
			{
				A:            fabric.Endpoint{Node: "sw3", Port: "1/1/3"},
				B:            fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
				LengthMeters: 1.0,
			},
			{
				A:            fabric.Endpoint{Node: "h2"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				LengthMeters: 1.0,
			},
			{
				A:            fabric.Endpoint{Node: "h3"},
				B:            fabric.Endpoint{Node: "sw3", Port: "1/1/1"},
				LengthMeters: 1.0,
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	snap0 := fab.Snapshot()
	if p2p := snap0.Devices["sw2"].Roles["1/1/3"].PointToPoint; p2p {
		t.Errorf("sw2 1/1/3 PointToPoint = %t, want false facing hub", p2p)
	}
	if p2p := snap0.Devices["sw3"].Roles["1/1/2"].PointToPoint; p2p {
		t.Errorf("sw3 1/1/2 PointToPoint = %t, want false facing hub", p2p)
	}

	twoFwdDelays := 30 * time.Second

	var designatedForwardingAt time.Time
	prevRoles := extractRoles(snap0)

	for step := 1; step <= 500; step++ {
		_, ok := fab.Step()
		if !ok {
			break
		}
		curSnap := fab.Snapshot()
		desig := curSnap.Devices["sw3"].Roles["1/1/2"]
		if desig.State == stp.StateForwarding && designatedForwardingAt.IsZero() {
			designatedForwardingAt = curSnap.Clock
		}
		curRoles := extractRoles(curSnap)
		if rolesAgree(prevRoles, curRoles) && !designatedForwardingAt.IsZero() {
			break
		}
		prevRoles = curRoles
	}

	if designatedForwardingAt.IsZero() {
		t.Fatal("Designated port facing hub never reached Forwarding")
	}

	earliestAllowed := t0.Add(twoFwdDelays)
	if designatedForwardingAt.Before(earliestAllowed) {
		t.Errorf("Designated port reached Forwarding at %v, expected at or after 2 forward delays (%v)", designatedForwardingAt, earliestAllowed)
	}

	finalSnap := fab.Snapshot()
	alt := finalSnap.Devices["sw2"].Roles["1/1/3"]
	if alt.Role != stp.RoleAlternate || alt.State != stp.StateDiscarding {
		t.Errorf("Alternate port sw2 1/1/3 = (%v, %v), want (Alternate, Discarding)", alt.Role, alt.State)
	}
}

func TestBPDUVisibleOnSwitchWithoutLayer(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports1, _ := b1.Build()

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports2, _ := b2.Build()

	mac1 := netaddr.MAC{0, 0, 0, 0, 1, 1}
	mac2 := netaddr.MAC{0, 0, 0, 0, 1, 2}

	fab, err := fabric.New(fabric.Config{
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
				LengthMeters: 1.0,
			},
		},
	})
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	_ = mac2

	for {
		entry, ok := fab.Step()
		if !ok {
			break
		}
		if entry.Device == "sw2" {
			break
		}
	}

	journeys := fab.Report()
	if len(journeys) == 0 {
		t.Fatal("expected at least one journey")
	}

	bpduJourney := journeys[0]
	if !bpduJourney.Protocol {
		t.Errorf("journey.Protocol = %t, want true", bpduJourney.Protocol)
	}

	lastEntry := bpduJourney.Entries[len(bpduJourney.Entries)-1]
	if lastEntry.Kind != fabric.EntryDrop || lastEntry.Reason != "reserved-address" || lastEntry.Device != "sw2" {
		t.Errorf("last entry = %+v, want Drop at sw2 with reserved-address", lastEntry)
	}
}

// TestZeroStartRefusedWithSpanningTree guards the clock: a fabric that runs
// a spanning tree layer needs a start time, or its hellos are dated year 1.
func TestZeroStartRefusedWithSpanningTree(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  tbl,
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Address: netaddr.MAC{0, 0, 0, 0, 1, 1},
					Ports:   map[string]stp.Port{"1/1/1": {}},
				},
			},
		},
	}
	if _, err := fabric.New(cfg); err == nil {
		t.Fatal("New accepted a spanning tree fabric with a zero Start")
	}
}

// TestDeriveWithSpanningTreeQueuesNoStrayProposals is evidence that Derive
// starts the layers only after the cloned ones are in place: a derived
// fabric under the same configuration holds only wake entries, no proposal
// from a layer that was thrown away.
func TestDeriveWithSpanningTreeQueuesNoStrayProposals(t *testing.T) {
	fab, _, _ := newThreeSwitchRingTopology(t)
	fab.Run(200)
	before := fab.Snapshot()

	cfg := fab.Config()
	cfg.Start = before.Clock
	next, err := fabric.Derive(fab, cfg)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	after := next.Snapshot()

	for _, arr := range after.Queue {
		if arr.Kind != fabric.ArrivalWake {
			t.Errorf("derived fabric queued a frame arrival %+v, want wakes only", arr)
		}
	}
	if after.Clock != before.Clock {
		t.Errorf("derived clock = %v, want %v", after.Clock, before.Clock)
	}
	if !rolesAgree(extractRoles(before), extractRoles(after)) {
		t.Errorf("derived roles differ from the source fabric's")
	}
}

// TestCutLagMemberKeepsLagUp is evidence that a fault on one member's cable
// recomputes the LAG's own state: the LAG stays up and its spanning tree
// port keeps forwarding, and only the last member's loss takes it down.
func TestCutLagMemberKeepsLagUp(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	lagPorts := func() port.Table {
		return mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}))
	}
	stpCfg := func(last byte, prio uint16) *stp.Config {
		return &stp.Config{
			Priority: prio,
			Address:  netaddr.MAC{0, 0, 0, 0, 1, last},
			Ports:    map[string]stp.Port{"lag1": {}},
		}
	}
	cfg := fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: lagPorts(), Bridge: &bridge.Config{}, STP: stpCfg(1, 4096)},
			"sw2": {Ports: lagPorts(), Bridge: &bridge.Config{}, STP: stpCfg(2, 32768)},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}},
		},
	}
	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fab.Run(50)

	lagState := func() (port.LinkState, stp.PortInfo) {
		sw := fab.Switch("sw2")
		lag, _ := sw.Ports().Port("lag1")

		return lag.OperStatus, sw.Roles()["lag1"]
	}
	if oper, info := lagState(); oper != port.Up || info.Role != stp.RoleRoot || info.State != stp.StateForwarding {
		t.Fatalf("before cut: lag1 %v %v/%v, want Up Root/Forwarding", oper, info.Role, info.State)
	}

	cut := fabric.Fault{Kind: fabric.FaultCut}
	if err := fab.SetFault(fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, cut); err != nil {
		t.Fatalf("SetFault first member: %v", err)
	}
	if oper, info := lagState(); oper != port.Up || info.Role != stp.RoleRoot || info.State != stp.StateForwarding {
		t.Fatalf("after one member cut: lag1 %v %v/%v, want Up Root/Forwarding", oper, info.Role, info.State)
	}

	if err := fab.SetFault(fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, fabric.Endpoint{Node: "sw2", Port: "1/1/2"}, cut); err != nil {
		t.Fatalf("SetFault second member: %v", err)
	}
	if oper, info := lagState(); oper != port.Down || info.Role != stp.RoleDisabled {
		t.Fatalf("after both members cut: lag1 %v %v, want Down Disabled", oper, info.Role)
	}
}

func TestFabricLegacyBPDUInjectionMigratesPort(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports1 := mustTable(t, b1)

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports2 := mustTable(t, b2)

	macSW1 := netaddr.MAC{0, 0, 0, 0, 1, 1}
	macSW2 := netaddr.MAC{0, 0, 0, 0, 1, 2}
	macH1 := netaddr.MAC{0, 0, 0, 0, 2, 1}

	fab, err := fabric.New(fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports1,
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 4096,
					Address:  macSW1,
					Ports: map[string]stp.Port{
						"1/1/1": {},
					},
				},
			},
			"sw2": {
				Ports:  ports2,
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 32768,
					Address:  macSW2,
					Ports: map[string]stp.Port{
						"1/1/1": {},
						"1/1/2": {},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
		},
		Cables: []fabric.Cable{
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				LengthMeters: 0,
			},
			{
				A:            fabric.Endpoint{Node: "h1"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				LengthMeters: 0,
			},
		},
	})
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	inferiorBridgeID := stp.BridgeID{
		Priority: 61440,
		Address:  netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c},
	}
	legacyBPDU := stp.BPDU{
		Type:         stp.BPDUTypeConfiguration,
		RootID:       inferiorBridgeID,
		BridgeID:     inferiorBridgeID,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	frame := stp.Encode(legacyBPDU, inferiorBridgeID.Address)

	injID, err := fab.Inject(fabric.Injection{
		At:     t0.Add(4 * time.Second),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  frame,
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	for {
		entry, ok := fab.Step()
		if !ok {
			break
		}
		if entry.Device == "sw2" && entry.Port == "1/1/2" && entry.Kind == fabric.EntryHop {
			break
		}
	}

	var injectedJourney *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID == injID {
			cp := j
			injectedJourney = &cp
			break
		}
	}
	if injectedJourney == nil {
		t.Fatalf("injected journey %d not found", injID)
	}

	var replyJourney *fabric.Journey
	for _, j := range fab.Report() {
		if j.Protocol && j.Injection.Origin.Node == "sw2" && j.Injection.Origin.Port == "1/1/2" {
			if j.Injection.At.After(t0.Add(4 * time.Second)) {
				cp := j
				replyJourney = &cp
				break
			}
		}
	}
	if replyJourney == nil {
		t.Fatal("expected reply journey from sw2 on 1/1/2")
	}

	var replyFrame ethernet.Frame
	if len(replyJourney.Deliveries) > 0 {
		replyFrame = replyJourney.Deliveries[0].Frame
	} else {
		replyFrame = replyJourney.Injection.Frame
	}

	decoded, err := stp.Decode(replyFrame)
	if err != nil {
		t.Fatalf("Decode reply frame: %v", err)
	}
	if decoded.Type != stp.BPDUTypeConfiguration {
		t.Errorf("decoded reply BPDU Type = %v, want Configuration", decoded.Type)
	}

	snap := fab.Snapshot()
	if snap.Devices["sw2"].Roles["1/1/2"].SendRSTP {
		t.Errorf("sw2 1/1/2 SendRSTP = true, want false")
	}
}

func TestFabricTxHoldCountLimitsInferiorBPDUReplies(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports1 := mustTable(t, b1)

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports2 := mustTable(t, b2)

	macSW1 := netaddr.MAC{0, 0, 0, 0, 1, 1}
	macSW2 := netaddr.MAC{0, 0, 0, 0, 1, 2}
	macH1 := netaddr.MAC{0, 0, 0, 2, 1, 1}

	fab, err := fabric.New(fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports1,
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 4096,
					Address:  macSW1,
					Ports: map[string]stp.Port{
						"1/1/1": {},
					},
				},
			},
			"sw2": {
				Ports:  ports2,
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority:    32768,
					Address:     macSW2,
					TxHoldCount: 2,
					Ports: map[string]stp.Port{
						"1/1/1": {},
						"1/1/2": {},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
		},
		Cables: []fabric.Cable{
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				LengthMeters: 0,
			},
			{
				A:            fabric.Endpoint{Node: "h1"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				LengthMeters: 0,
			},
		},
	})
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	inferiorBridgeID := stp.BridgeID{
		Priority: 61440,
		Address:  netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c},
	}
	legacyBPDU := stp.BPDU{
		Type:         stp.BPDUTypeConfiguration,
		RootID:       inferiorBridgeID,
		BridgeID:     inferiorBridgeID,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	frame := stp.Encode(legacyBPDU, inferiorBridgeID.Address)

	injectTimes := []time.Duration{
		4100 * time.Millisecond,
		4200 * time.Millisecond,
		4400 * time.Millisecond,
	}
	for _, dt := range injectTimes {
		if _, err := fab.Inject(fabric.Injection{
			At:     t0.Add(dt),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame:  frame,
		}); err != nil {
			t.Fatalf("Inject at %v: %v", dt, err)
		}
	}

	for {
		entry, ok := fab.Step()
		if !ok {
			break
		}
		if entry.Kind == fabric.EntryWake && entry.Device == "sw2" && entry.At.Equal(t0.Add(5*time.Second)) {
			break
		}
	}

	snapAt5s := fab.Snapshot()
	epSW2P2 := fabric.Endpoint{Node: "sw2", Port: "1/1/2"}
	busyUntil, ok := snapAt5s.Busy[epSW2P2]
	if !ok {
		t.Fatalf("expected sw2:1/1/2 to take busy clock at t0+5s")
	}
	wantBusy := t0.Add(5*time.Second + 672*time.Nanosecond)
	if !busyUntil.Equal(wantBusy) {
		t.Errorf("sw2:1/1/2 busy until %v, want %v", busyUntil, wantBusy)
	}

	var replies []fabric.Journey
	for _, j := range fab.Report() {
		if j.Protocol && j.Injection.Origin.Node == "sw2" && j.Injection.Origin.Port == "1/1/2" {
			if j.Injection.At.After(t0.Add(4 * time.Second)) {
				replies = append(replies, j)
			}
		}
	}

	if len(replies) != 2 {
		t.Fatalf("got %d replies from sw2 on 1/1/2 after t0+4s, want 2 (one before 5s, one at 5s, no third)", len(replies))
	}

	wantFirstReplyAt := t0.Add(4100*time.Millisecond + 672*time.Nanosecond)
	if !replies[0].Injection.At.Equal(wantFirstReplyAt) {
		t.Errorf("first reply At = %v, want %v", replies[0].Injection.At, wantFirstReplyAt)
	}
	if !replies[0].Injection.At.Before(t0.Add(5 * time.Second)) {
		t.Errorf("first reply At %v is not before t0+5s", replies[0].Injection.At)
	}

	if !replies[1].Injection.At.Equal(t0.Add(5 * time.Second)) {
		t.Errorf("second reply At = %v, want t0+5s (%v)", replies[1].Injection.At, t0.Add(5*time.Second))
	}

	for _, e := range replies[1].Entries {
		if e.Kind == fabric.EntryCrossing {
			if e.Wait != 0 {
				t.Errorf("crossing Wait = %v, want 0", e.Wait)
			}
			if !e.At.Equal(t0.Add(5 * time.Second)) {
				t.Errorf("crossing At = %v, want %v", e.At, t0.Add(5*time.Second))
			}
		}
	}

	for {
		snap := fab.Snapshot()
		if snap.Clock.After(t0.Add(5500 * time.Millisecond)) {
			break
		}
		if _, ok := fab.Step(); !ok {
			break
		}
	}

	repliesAfter := 0
	for _, j := range fab.Report() {
		if j.Protocol && j.Injection.Origin.Node == "sw2" && j.Injection.Origin.Port == "1/1/2" {
			if j.Injection.At.After(t0.Add(4*time.Second)) && j.Injection.At.Before(t0.Add(6*time.Second)) {
				repliesAfter++
			}
		}
	}
	if repliesAfter != 2 {
		t.Errorf("replies between t0+4s and t0+6s = %d, want 2 (no third reply)", repliesAfter)
	}
}

// TestFabricMcheckQueuesTheRSTReply is evidence that a management check
// through the fabric emits at the fabric clock and reschedules the wake,
// rather than leaving the reply buffered until an unrelated step.
func TestFabricMcheckQueuesTheRSTReply(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, _ := b.Build()
	fab, err := fabric.New(fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports,
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 32768,
					Address:  netaddr.MAC{0x02, 0, 0, 0, 0, 0x02},
					Ports:    map[string]stp.Port{"1/1/1": {}},
				},
			},
		},
		Hosts:  map[string]fabric.Host{"h1": {Address: netaddr.MAC{0x02, 0, 0, 0, 0, 0x11}}},
		Cables: []fabric.Cable{{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h1"}}},
	})
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}
	inferior := stp.BridgeID{Priority: 61440, Address: netaddr.MAC{0x02, 0, 0, 0, 0, 0x0c}}
	legacy := stp.BPDU{
		Type: stp.BPDUTypeConfiguration, RootID: inferior, BridgeID: inferior, PortID: 0x8001,
		HelloTime: 2 * time.Second, MaxAge: 20 * time.Second, ForwardDelay: 15 * time.Second,
	}
	legacy.SetRole(stp.RoleDesignated)
	if _, err := fab.Inject(fabric.Injection{
		At: t0.Add(4 * time.Second), Origin: fabric.Endpoint{Node: "h1"},
		Frame: stp.Encode(legacy, netaddr.MAC{0x02, 0, 0, 0, 0, 0x0c}),
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	for {
		entry, ok := fab.Step()
		if !ok || entry.At.After(t0.Add(5*time.Second)) {
			break
		}
	}
	if fab.Snapshot().Devices["sw1"].Roles["1/1/1"].SendRSTP {
		t.Fatal("the port did not migrate")
	}
	clock := fab.Snapshot().Clock
	if err := fab.Mcheck("sw1", "1/1/1"); err != nil {
		t.Fatalf("Mcheck: %v", err)
	}
	if !fab.Snapshot().Devices["sw1"].Roles["1/1/1"].SendRSTP {
		t.Fatal("Mcheck left the port in compatibility mode")
	}
	var found bool
	for _, j := range fab.Report() {
		if j.Protocol && j.Injection.Origin.Node == "sw1" && j.Injection.At.Equal(clock) {
			bpdu, err := stp.Decode(j.Injection.Frame)
			if err == nil && bpdu.Type == stp.BPDUTypeRapid {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no RST BPDU journey dated the fabric clock %v after Mcheck", clock)
	}
	if err := fab.Mcheck("h1", ""); err == nil {
		t.Error("Mcheck on a host returned no error")
	}
}
