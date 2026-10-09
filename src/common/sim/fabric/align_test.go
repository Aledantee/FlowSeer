package fabric

import (
	"fmt"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func locationComparison(a, b []Journey, rootsA, rootsB []FrameID, actions []Action, runA, runB RunResult, snapA, snapB Snapshot) Comparison {
	diff, location, found := diffFabricRuns(actions, rootsA, rootsB, a, b, runA, runB, snapA, snapB)
	disposition := analysis.Equivalent
	if found {
		disposition = analysis.Different
	}
	if runA.Status != analysis.Complete || runB.Status != analysis.Complete {
		disposition = analysis.Inconclusive
	}
	return Comparison{Current: a, Expected: b, Difference: diff, Location: location, Disposition: disposition, Replay: [2]ReplaySpec{{Scenario: Scenario{Actions: actions}}}}
}

func locationAction(index int) Action {
	return Action{Kind: ActionInject, Index: index, At: time.Date(2026, 1, 1, 0, 0, index, 0, time.UTC)}
}

func TestJourneyDifferenceFields(t *testing.T) {
	cases := []struct {
		name, field string
		change      func(*Journey)
	}{
		{"terminal", "State", func(j *Journey) { j.State = JourneyDropped }},
		{"origin kind", "Origin.Kind", func(j *Journey) { j.Origin.Kind = OriginRelease }},
		{"origin mirror", "Origin.Mirror", func(j *Journey) { j.Origin.Mirror = "span" }},
		{"protocol", "Protocol", func(j *Journey) { j.Protocol = true }},
		{"entry kind", "Entries[0].Kind", func(j *Journey) { j.Entries[0].Kind = EntryArrival }},
		{"entry drop kind", "Entries[0].Kind", func(j *Journey) { j.Entries[0].Kind = EntryDrop }},
		{"entry device", "Entries[0].Device", func(j *Journey) { j.Entries[0].Device = "other" }},
		{"drop device", "Entries[0].Device", func(j *Journey) { j.Entries[0].Device = "other" }},
		{"drop port", "Entries[0].Port", func(j *Journey) { j.Entries[0].Port = "other" }},
		{"entry port", "Entries[0].Port", func(j *Journey) { j.Entries[0].Port = "other" }},
		{"cable presence", "Entries[0].Cable.Presence", func(j *Journey) { j.Entries[0].Cable = nil }},
		{"cable A node", "Entries[0].Cable.A.Node", func(j *Journey) { j.Entries[0].Cable.A.Node = "other" }},
		{"cable A port", "Entries[0].Cable.A.Port", func(j *Journey) { j.Entries[0].Cable.A.Port = "other" }},
		{"cable B node", "Entries[0].Cable.B.Node", func(j *Journey) { j.Entries[0].Cable.B.Node = "other" }},
		{"cable B port", "Entries[0].Cable.B.Port", func(j *Journey) { j.Entries[0].Cable.B.Port = "other" }},
		{"cable length", "Entries[0].Cable.LengthMeters", func(j *Journey) { j.Entries[0].Cable.LengthMeters = 1 }},
		{"cable medium", "Entries[0].Cable.Medium", func(j *Journey) { j.Entries[0].Cable.Medium = "changed" }},
		{"cable fault kind", "Entries[0].Cable.Fault.Kind", func(j *Journey) { j.Entries[0].Cable.Fault.Kind = FaultLoseEveryNth }},
		{"cable fault n", "Entries[0].Cable.Fault.N", func(j *Journey) { j.Entries[0].Cable.Fault.N = 1 }},
		{"cable fault sequence", "Entries[0].Cable.Fault.Sequence", func(j *Journey) { j.Entries[0].Cable.Fault.Sequence = []uint{1} }},
		{"reason", "Entries[0].Reason", func(j *Journey) { j.Entries[0].Reason = "refused" }},
		{"pcp", "Entries[0].PCP", func(j *Journey) { j.Entries[0].PCP = 1 }},
		{"at", "Entries[0].At", func(j *Journey) { j.Entries[0].At = time.Unix(1, 0) }},
		{"latency", "Entries[0].Latency", func(j *Journey) { j.Entries[0].Latency = 1 }},
		{"serialization", "Entries[0].Serialization", func(j *Journey) { j.Entries[0].Serialization = 1 }},
		{"wait", "Entries[0].Wait", func(j *Journey) { j.Entries[0].Wait = 1 }},
		{"entry count", "Entries.Length", func(j *Journey) { j.Entries = nil }},
		{"delivery count", "Deliveries.Length", func(j *Journey) { j.Deliveries = nil }},
		{"delivery host", "Deliveries[0].Host", func(j *Journey) { j.Deliveries[0].Host = "other" }},
		{"delivery at", "Deliveries[0].At", func(j *Journey) { j.Deliveries[0].At = time.Unix(1, 0) }},
		{"delivery dst", "Deliveries[0].Frame.Dst", func(j *Journey) { j.Deliveries[0].Frame.Dst = netaddr.MAC{1} }},
		{"delivery src", "Deliveries[0].Frame.Src", func(j *Journey) { j.Deliveries[0].Frame.Src = netaddr.MAC{1} }},
		{"delivery ethertype", "Deliveries[0].Frame.EtherType", func(j *Journey) { j.Deliveries[0].Frame.EtherType = ethernet.EtherTypeIPv4 }},
		{"delivery tags", "Deliveries[0].Frame.Tags", func(j *Journey) { j.Deliveries[0].Frame.Tags = []vlan.Tag{{VID: 1}} }},
		{"delivery payload", "Deliveries[0].Frame.Payload", func(j *Journey) { j.Deliveries[0].Frame.Payload = []byte{1} }},
		{"egress count", "Entries[0].Result.Egress.Length", func(j *Journey) { j.Entries[0].Result.Egress = nil }},
		{"egress dst", "Entries[0].Result.Egress[0].Frame.Dst", func(j *Journey) { j.Entries[0].Result.Egress[0].Frame.Dst = netaddr.MAC{1} }},
		{"egress src", "Entries[0].Result.Egress[0].Frame.Src", func(j *Journey) { j.Entries[0].Result.Egress[0].Frame.Src = netaddr.MAC{1} }},
		{"egress ethertype", "Entries[0].Result.Egress[0].Frame.EtherType", func(j *Journey) { j.Entries[0].Result.Egress[0].Frame.EtherType = ethernet.EtherTypeIPv4 }},
		{"egress tags", "Entries[0].Result.Egress[0].Frame.Tags", func(j *Journey) { j.Entries[0].Result.Egress[0].Frame.Tags = []vlan.Tag{{VID: 1}} }},
		{"egress payload", "Entries[0].Result.Egress[0].Frame.Payload", func(j *Journey) { j.Entries[0].Result.Egress[0].Frame.Payload = []byte{1} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := Journey{FrameID: 1, State: JourneyDelivered, Origin: JourneyOrigin{Kind: OriginInjection}, Entries: []Entry{{Kind: EntryHop, Cable: &Cable{}, Result: &device.Result{Egress: []device.Egress{{}}}}}, Deliveries: []Delivery{{}}}
			if tc.name == "drop device" || tc.name == "drop port" {
				a.Entries[0].Kind = EntryDrop
			}
			b := a.clone()
			tc.change(&b)
			diff, field, found := diffJourney(a, b)
			if !found || diff.Observable == "" || field != tc.field {
				t.Fatalf("difference = %+v, field %q, found %t, want %q", diff, field, found, tc.field)
			}
		})
	}
}

func TestAlignAncestryAndBehavior(t *testing.T) {
	root := func(id FrameID) Journey {
		return Journey{FrameID: id, State: JourneyDelivered, Origin: JourneyOrigin{Kind: OriginInjection}, Entries: []Entry{{Kind: EntryInjection}, {Kind: EntryHop}}}
	}
	child := func(id, parent FrameID, kind JourneyOriginKind, name string) Journey {
		j := root(id)
		j.Origin = JourneyOrigin{Kind: kind, Of: parent, Mirror: name}
		return j
	}
	complete := RunResult{Status: analysis.Complete}
	cases := []struct {
		name                                string
		a, b                                []Journey
		rootsA, rootsB                      []FrameID
		wantIndex, current, expected, entry int
		lineage                             []LineageStep
		field                               string
		wantTrace                           bool
	}{
		{"second root", []Journey{root(10), root(20)}, []Journey{root(110), root(120)}, []FrameID{10, 20}, []FrameID{110, 120}, 2, 1, 1, 1, []LineageStep{{Kind: OriginInjection}}, "Entries[1].Device", true},
		{"second root extra mirror", []Journey{root(10), root(20)}, []Journey{root(110), root(120), child(130, 120, OriginMirror, "span")}, []FrameID{10, 20}, []FrameID{110, 120}, 2, -1, 2, 0, []LineageStep{{Kind: OriginInjection}, {Kind: OriginMirror, Mirror: "span"}}, "Journey", true},
		{"prior ids reorder reports", []Journey{root(10), root(20), child(30, 10, OriginMirror, "span")}, []Journey{root(110), child(120, 110, OriginMirror, "span"), root(130)}, []FrameID{10, 20}, []FrameID{110, 130}, 2, 1, 2, 1, []LineageStep{{Kind: OriginInjection}}, "Entries[1].Device", true},
		{"extra mirror", []Journey{root(10)}, []Journey{root(110), child(120, 110, OriginMirror, "span")}, []FrameID{10}, []FrameID{110}, 1, -1, 1, 0, []LineageStep{{Kind: OriginInjection}, {Kind: OriginMirror, Mirror: "span"}}, "Journey", true},
		{"extra release", []Journey{root(10), child(20, 10, OriginRelease, "")}, []Journey{root(110)}, []FrameID{10}, []FrameID{110}, 1, 1, -1, 0, []LineageStep{{Kind: OriginInjection}, {Kind: OriginRelease}}, "Journey", true},
		{"nested mirrors", []Journey{root(10), child(20, 10, OriginMirror, "span"), child(30, 20, OriginMirror, "nested")}, []Journey{root(110), child(120, 110, OriginMirror, "span"), child(130, 120, OriginMirror, "nested")}, []FrameID{10}, []FrameID{110}, 1, 2, 2, 1, []LineageStep{{Kind: OriginInjection}, {Kind: OriginMirror, Mirror: "span"}, {Kind: OriginMirror, Mirror: "nested"}}, "Entries[1].Device", true},
		{"same name occurrence", []Journey{root(10), child(20, 10, OriginMirror, "span"), child(30, 10, OriginMirror, "span")}, []Journey{root(110), child(120, 110, OriginMirror, "span"), child(130, 110, OriginMirror, "span")}, []FrameID{10}, []FrameID{110}, 1, 2, 2, 1, []LineageStep{{Kind: OriginInjection}, {Kind: OriginMirror, Mirror: "span", Occurrence: 1}}, "Entries[1].Device", true},
		{"terminal equal traces", []Journey{root(10)}, []Journey{root(110)}, []FrameID{10}, []FrameID{110}, 1, 0, 0, 0, []LineageStep{{Kind: OriginInjection}}, "State", false},
		{"delivery equal traces", []Journey{root(10)}, []Journey{root(110)}, []FrameID{10}, []FrameID{110}, 1, 0, 0, 0, []LineageStep{{Kind: OriginInjection}}, "Deliveries.Length", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			switch tc.field {
			case "State":
				tc.b[0].State = JourneyDropped
			case "Deliveries.Length":
				tc.b[0].Deliveries = []Delivery{{Host: "sink"}}
			case "Journey":
			default:
				tc.b[tc.expected].Entries[1].Device = "other"
			}
			actions := []Action{locationAction(1)}
			if len(tc.rootsA) == 2 {
				actions = append(actions, locationAction(2))
			}
			comparison := locationComparison(tc.a, tc.b, tc.rootsA, tc.rootsB, actions, complete, complete, Snapshot{}, Snapshot{})
			if comparison.Disposition != analysis.Different {
				t.Fatalf("disposition = %s, want Different", comparison.Disposition)
			}
			alignment, ok := Align(comparison)
			if !ok || alignment.Location.Field != tc.field || alignment.Location.Injection.Index != tc.wantIndex || !reflect.DeepEqual(alignment.Location.Lineage, tc.lineage) {
				t.Fatalf("alignment = %+v, want injection %d, lineage %+v, field %s", alignment, tc.wantIndex, tc.lineage, tc.field)
			}
			if (alignment.Trace != nil) != tc.wantTrace {
				t.Fatalf("trace = %+v, want present %t", alignment.Trace, tc.wantTrace)
			}
			if tc.wantTrace && (alignment.Trace.CurrentJourney != tc.current || alignment.Trace.ExpectedJourney != tc.expected || alignment.Trace.EntryIndex != tc.entry || !reflect.DeepEqual(alignment.Trace.Lineage, tc.lineage)) {
				t.Errorf("trace = %+v, want journey indices %d/%d, entry %d, lineage %+v", alignment.Trace, tc.current, tc.expected, tc.entry, tc.lineage)
			}
		})
	}
}

func TestGlobalDifferenceLocations(t *testing.T) {
	cases := []struct {
		name, subject, field string
		area                 DifferenceArea
		mutate               func(*RunResult, *Snapshot)
	}{
		{"stop", "run", "Stop", DifferenceRun, func(r *RunResult, _ *Snapshot) { r.Stop = StopConverged }},
		{"status", "run", "Status", DifferenceRun, func(r *RunResult, _ *Snapshot) { r.Status = analysis.Incomplete }},
		{"arrivals", "run", "Pending.Arrivals", DifferenceRun, func(r *RunResult, _ *Snapshot) { r.Pending.Arrivals = 1 }},
		{"wakes", "run", "Pending.Wakes", DifferenceRun, func(r *RunResult, _ *Snapshot) { r.Pending.Wakes = 1 }},
		{"egress", "run", "Pending.Egress", DifferenceRun, func(r *RunResult, _ *Snapshot) { r.Pending.Egress = 1 }},
		{"journeys", "run", "Pending.Journeys", DifferenceRun, func(r *RunResult, _ *Snapshot) { r.Pending.Journeys = 1 }},
		{"issue count", "run", "Issues.Length", DifferenceRun, func(r *RunResult, _ *Snapshot) { r.Issues = nil }},
		{"issue code", "run", "Issues[0].Code", DifferenceRun, func(r *RunResult, _ *Snapshot) { r.Issues[0].Code = "other" }},
		{"issue status", "run", "Issues[0].Status", DifferenceRun, func(r *RunResult, _ *Snapshot) { r.Issues[0].Status = analysis.Incomplete }},
		{"issue scope", "run", "Issues[0].Scope", DifferenceRun, func(r *RunResult, _ *Snapshot) { r.Issues[0].Scope = analysis.NodeScope("other") }},
		{"device missing", "sw", "Device", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { delete(s.Devices, "sw") }},
		{"device added", "added", "Device", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { s.Devices["added"] = device.State{} }},
		{"fdb count", "sw", "Entries.Length", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { d := s.Devices["sw"]; d.Entries = nil; s.Devices["sw"] = d }},
		{"fdb key", "sw", "Entries.[\"00:00:00:00:00:00\"/\"0\"/\"other\"]", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Entries[0].Port = "other"
			s.Devices["sw"] = d
		}},
		{"port count", "sw", "Ports.Length", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { d := s.Devices["sw"]; d.Ports = nil; s.Devices["sw"] = d }},
		{"port name", "sw", "Ports.[\"other\"]", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { d := s.Devices["sw"]; d.Ports[0].Name = "other"; s.Devices["sw"] = d }},
		{"port admin", "sw", "Ports.[\"p\"].AdminStatus", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Ports[0].AdminStatus = port.Down
			s.Devices["sw"] = d
		}},
		{"port oper", "sw", "Ports.[\"p\"].OperStatus", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Ports[0].OperStatus = port.Down
			s.Devices["sw"] = d
		}},
		{"neighbor count", "sw", "Neighbors.Length", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { d := s.Devices["sw"]; d.Neighbors = nil; s.Devices["sw"] = d }},
		{"neighbor key", "sw", "Neighbors.[\"\"/\"other\"/\"192.0.2.1\"]", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Neighbors[0].Interface = "other"
			s.Devices["sw"] = d
		}},
		{"neighbor mac", "sw", "Neighbors.[\"\"/\"p\"/\"192.0.2.1\"].MAC", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Neighbors[0].MAC = netaddr.MAC{1}
			s.Devices["sw"] = d
		}},
		{"neighbor state", "sw", "Neighbors.[\"\"/\"p\"/\"192.0.2.1\"].State", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Neighbors[0].State = "changed"
			s.Devices["sw"] = d
		}},
		{"role missing", "sw", "Roles.[\"p\"]", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { d := s.Devices["sw"]; delete(d.Roles, "p"); s.Devices["sw"] = d }},
		{"role added", "sw", "Roles.[\"other\"]", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["other"] = device.PortInfo{}
			s.Devices["sw"] = d
		}},
		{"role state", "sw", "Roles.[\"p\"].State", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{State: device.StateForwarding}
			s.Devices["sw"] = d
		}},
		{"role Tree.Kind", "sw", "Roles.[\"p\"].Tree.Kind", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{Tree: device.TreeRef{Kind: device.TreeVLAN}}
			s.Devices["sw"] = d
		}},
		{"role Tree.ID", "sw", "Roles.[\"p\"].Tree.ID", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{Tree: device.TreeRef{ID: 1}}
			s.Devices["sw"] = d
		}},
		{"role Role", "sw", "Roles.[\"p\"].Role", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{Role: "Root"}
			s.Devices["sw"] = d
		}},
		{"role BlockReason", "sw", "Roles.[\"p\"].BlockReason", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{BlockReason: "guard"}
			s.Devices["sw"] = d
		}},
		{"role Priority", "sw", "Roles.[\"p\"].Priority", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{Priority: 1}
			s.Devices["sw"] = d
		}},
		{"role PathCost", "sw", "Roles.[\"p\"].PathCost", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{PathCost: 1}
			s.Devices["sw"] = d
		}},
		{"role DesignatedRoot.Priority", "sw", "Roles.[\"p\"].DesignatedRoot.Priority", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{DesignatedRoot: bpdu.BridgeID{Priority: 1}}
			s.Devices["sw"] = d
		}},
		{"role DesignatedRoot.Address", "sw", "Roles.[\"p\"].DesignatedRoot.Address", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{DesignatedRoot: bpdu.BridgeID{Address: netaddr.MAC{1}}}
			s.Devices["sw"] = d
		}},
		{"role Designated.Priority", "sw", "Roles.[\"p\"].Designated.Priority", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{Designated: bpdu.BridgeID{Priority: 1}}
			s.Devices["sw"] = d
		}},
		{"role Designated.Address", "sw", "Roles.[\"p\"].Designated.Address", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{Designated: bpdu.BridgeID{Address: netaddr.MAC{1}}}
			s.Devices["sw"] = d
		}},
		{"role DesignatedPort", "sw", "Roles.[\"p\"].DesignatedPort", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{DesignatedPort: 1}
			s.Devices["sw"] = d
		}},
		{"role DesignatedCost", "sw", "Roles.[\"p\"].DesignatedCost", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{DesignatedCost: 1}
			s.Devices["sw"] = d
		}},
		{"role PointToPoint", "sw", "Roles.[\"p\"].PointToPoint", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{PointToPoint: true}
			s.Devices["sw"] = d
		}},
		{"role Edge", "sw", "Roles.[\"p\"].Edge", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{Edge: true}
			s.Devices["sw"] = d
		}},
		{"role ForwardTransitions", "sw", "Roles.[\"p\"].ForwardTransitions", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{ForwardTransitions: 1}
			s.Devices["sw"] = d
		}},
		{"role TxBPDUs", "sw", "Roles.[\"p\"].TxBPDUs", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{TxBPDUs: 1}
			s.Devices["sw"] = d
		}},
		{"role RxBPDUs", "sw", "Roles.[\"p\"].RxBPDUs", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{RxBPDUs: 1}
			s.Devices["sw"] = d
		}},
		{"role BadBPDUs", "sw", "Roles.[\"p\"].BadBPDUs", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{BadBPDUs: 1}
			s.Devices["sw"] = d
		}},
		{"role SendRSTP", "sw", "Roles.[\"p\"].SendRSTP", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) {
			d := s.Devices["sw"]
			d.Roles["p"] = device.PortInfo{SendRSTP: true}
			s.Devices["sw"] = d
		}},
		{"link count", "fabric", "Links.Length", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { s.Links = nil }},
		{"link endpoint", "fabric", "Links.[other:-b:]", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { s.Links[0].A.Node = "other" }},
		{"link A speed", "fabric", "Links.[a:-b:].A.Speed.SpeedBPS", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { s.Links[0].A.Speed.SpeedBPS = 1 }},
		{"link B speed", "fabric", "Links.[a:-b:].B.Speed.SpeedBPS", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { s.Links[0].B.Speed.SpeedBPS = 1 }},
		{"link A oper", "fabric", "Links.[a:-b:].A.Oper", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { s.Links[0].A.Oper = port.Down }},
		{"link B oper", "fabric", "Links.[a:-b:].B.Oper", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { s.Links[0].B.Oper = port.Down }},
		{"link fault kind", "fabric", "Links.[a:-b:].Fault.Kind", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { s.Links[0].Fault.Kind = FaultLoseEveryNth }},
		{"link fault n", "fabric", "Links.[a:-b:].Fault.N", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { s.Links[0].Fault.N = 1 }},
		{"link fault sequence", "fabric", "Links.[a:-b:].Fault.Sequence", DifferenceSnapshot, func(_ *RunResult, s *Snapshot) { s.Links[0].Fault.Sequence = []uint{1} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			makeRun := func() RunResult {
				return RunResult{Status: analysis.Complete, Issues: []analysis.Issue{{Code: "test"}}}
			}
			makeSnapshot := func() Snapshot {
				return Snapshot{Devices: map[string]device.State{"sw": {Entries: []device.Entry{{Port: "p"}}, Ports: []port.Port{{Name: "p", AdminStatus: port.Up, OperStatus: port.Up}}, Neighbors: []device.NeighborEntry{{Interface: "p", Addr: netip.MustParseAddr("192.0.2.1")}}, Roles: map[string]device.PortInfo{"p": {}}}}, Links: []Link{{A: LinkEnd{Endpoint: Endpoint{Node: "a"}}, B: LinkEnd{Endpoint: Endpoint{Node: "b"}}}}}
			}
			a, b := makeRun(), makeRun()
			sa, sb := makeSnapshot(), makeSnapshot()
			tc.mutate(&b, &sb)
			comparison := locationComparison(nil, nil, nil, nil, nil, a, b, sa, sb)
			diagnostic := tc.name == "role ForwardTransitions" || tc.name == "role TxBPDUs" || tc.name == "role RxBPDUs" || tc.name == "role BadBPDUs"
			wantDisposition := analysis.Different
			if diagnostic {
				wantDisposition = analysis.Equivalent
			}
			if tc.name == "status" {
				wantDisposition = analysis.Inconclusive
			}
			if comparison.Disposition != wantDisposition {
				t.Fatalf("disposition = %s, want %s", comparison.Disposition, wantDisposition)
			}
			if diagnostic {
				if comparison.Difference.Observable != "" || !reflect.DeepEqual(comparison.Location, DifferenceLocation{}) {
					t.Errorf("diagnostic difference = %+v at %+v, want none", comparison.Difference, comparison.Location)
				}
				if alignment, ok := Align(comparison); ok {
					t.Errorf("diagnostic alignment = %+v, want none", alignment)
				}
				return
			}
			want := DifferenceLocation{Area: tc.area, Subject: tc.subject, Field: tc.field}
			if !reflect.DeepEqual(comparison.Location, want) {
				t.Fatalf("location = %+v, want %+v", comparison.Location, want)
			}
			alignment, ok := Align(comparison)
			if tc.name == "status" {
				if ok || alignment.Trace != nil {
					t.Errorf("alignment = %+v, %t, want no causal divergence", alignment, ok)
				}
				return
			}
			if !ok || alignment.Trace != nil || !reflect.DeepEqual(alignment.Location, want) {
				t.Errorf("alignment = %+v, %t, want behavioral location %+v without trace", alignment, ok, want)
			}
		})
	}
	t.Run("injection count", func(t *testing.T) {
		complete := RunResult{Status: analysis.Complete}
		comparison := locationComparison(nil, nil, []FrameID{1}, nil, []Action{locationAction(1)}, complete, complete, Snapshot{}, Snapshot{})
		if comparison.Disposition != analysis.Different {
			t.Fatalf("disposition = %s, want Different", comparison.Disposition)
		}
		want := DifferenceLocation{Area: DifferenceRun, Subject: "run", Field: "Injections.Length"}
		if !reflect.DeepEqual(comparison.Location, want) {
			t.Fatalf("location = %+v, want %+v", comparison.Location, want)
		}
		alignment, ok := Align(comparison)
		if !ok || alignment.Trace != nil || !reflect.DeepEqual(alignment.Location, want) {
			t.Errorf("alignment = %+v, %t, want %+v without trace", alignment, ok, want)
		}
	})
}

func TestAlignOriginalIndicesAndDiagnosticVerdicts(t *testing.T) {
	action := locationAction(7)
	action.At = action.At.In(time.FixedZone("offset", 3600))
	a := Journey{FrameID: 10, State: JourneyDelivered, Origin: JourneyOrigin{Kind: OriginInjection}, Entries: []Entry{{Kind: EntryInjection}, {Kind: EntryQueueThreshold}, {Kind: EntryHop, Device: "sw"}}}
	b := a.clone()
	b.FrameID = 100
	b.Entries[2].Device = "other"
	complete := RunResult{Status: analysis.Complete}
	comparison := locationComparison([]Journey{a}, []Journey{b}, []FrameID{10}, []FrameID{100}, []Action{action}, complete, complete, Snapshot{}, Snapshot{})
	if comparison.Disposition != analysis.Different {
		t.Fatalf("disposition = %s, want Different", comparison.Disposition)
	}
	alignment, ok := Align(comparison)
	if !ok || alignment.Trace == nil || alignment.Trace.EntryIndex != 2 || alignment.Location.Field != "Entries[1].Device" || alignment.Trace.Injection.At.Location() != time.UTC || alignment.Trace.Injection.Index != 7 {
		t.Fatalf("alignment = %+v trace %+v, want original entry 2, behavioral entry 1, UTC injection 7", alignment, alignment.Trace)
	}
	b = a.clone()
	b.Entries[2].Step = &trace.Step{RuleID: "diagnostic"}
	comparison = locationComparison([]Journey{a}, []Journey{b}, []FrameID{10}, []FrameID{10}, []Action{action}, complete, complete, Snapshot{}, Snapshot{})
	if comparison.Disposition != analysis.Equivalent {
		t.Fatalf("disposition = %s, want Equivalent", comparison.Disposition)
	}
	if alignment, ok := Align(comparison); ok || alignment.Trace != nil {
		t.Errorf("equivalent alignment = %+v, %t, want false", alignment, ok)
	}
	comparison.Disposition = analysis.Inconclusive
	alignment, ok = Align(comparison)
	if !ok || alignment.Trace == nil || alignment.Trace.EntryIndex != 2 || alignment.Location.Area != "" {
		t.Errorf("inconclusive alignment = %+v, %t, want diagnostic entry 2 without behavioral location", alignment, ok)
	}
	if _, ok := Align(Comparison{Disposition: analysis.Inconclusive, Err: fmt.Errorf("invalid input")}); ok {
		t.Error("error comparison alignment = true, want false")
	}
}

func TestAlignInconclusiveDiscardsBehavioralLocation(t *testing.T) {
	a := Journey{FrameID: 10, Origin: JourneyOrigin{Kind: OriginInjection}, Entries: []Entry{{Kind: EntryInjection}, {Kind: EntryHop, Device: "sw"}}}
	b := a.clone()
	b.Entries[1].Device = "other"
	comparison := locationComparison([]Journey{a}, []Journey{b}, []FrameID{10}, []FrameID{10}, []Action{locationAction(7)}, RunResult{Status: analysis.Incomplete}, RunResult{Status: analysis.Complete}, Snapshot{}, Snapshot{})
	wantLocation := DifferenceLocation{Area: DifferenceJourney, Injection: new(InjectionLocation{Index: 7, At: time.Date(2026, 1, 1, 0, 0, 7, 0, time.UTC)}), Lineage: []LineageStep{{Kind: OriginInjection}}, Subject: "journey", Field: "Entries[1].Device"}
	wantDifference := analysis.Difference{Observable: "path", Current: "sw", Expected: "other"}
	if comparison.Disposition != analysis.Inconclusive || !reflect.DeepEqual(comparison.Location, wantLocation) || comparison.Difference != wantDifference {
		t.Fatalf("comparison = %+v, want Inconclusive with location %+v and difference %+v", comparison, wantLocation, wantDifference)
	}
	alignment, ok := Align(comparison)
	if !ok || alignment.Trace == nil || alignment.Trace.EntryIndex != 1 || alignment.Trace.CurrentJourney != 0 || alignment.Trace.ExpectedJourney != 0 || alignment.Trace.Injection.Index != 7 {
		t.Fatalf("alignment = %+v, trace %+v, %t, want entry 1, journeys 0/0, injection 7", alignment, alignment.Trace, ok)
	}
	if !reflect.DeepEqual(alignment.Location, DifferenceLocation{}) {
		t.Errorf("location = %+v, want zero location", alignment.Location)
	}
}

func TestAlignExtraEntryAfterMatchingPrefix(t *testing.T) {
	a := Journey{FrameID: 10, Origin: JourneyOrigin{Kind: OriginInjection}, Entries: []Entry{{Kind: EntryInjection}, {Kind: EntryHop, Device: "sw"}}}
	b := a.clone()
	b.Entries = append(b.Entries, Entry{Kind: EntryDelivery, Device: "sink"})
	complete := RunResult{Status: analysis.Complete}
	comparison := locationComparison([]Journey{a}, []Journey{b}, []FrameID{10}, []FrameID{10}, []Action{locationAction(1)}, complete, complete, Snapshot{}, Snapshot{})
	if comparison.Disposition != analysis.Different || comparison.Location.Field != "Entries.Length" {
		t.Fatalf("comparison = %+v, want Different at Entries.Length", comparison)
	}
	alignment, ok := Align(comparison)
	if !ok || alignment.Trace == nil || alignment.Trace.EntryIndex != 2 || alignment.Trace.CurrentJourney != 0 || alignment.Trace.ExpectedJourney != 0 || alignment.Trace.Injection.Index != 1 {
		t.Errorf("alignment = %+v, trace %+v, %t, want entry 2, journeys 0/0, injection 1", alignment, alignment.Trace, ok)
	}
}
