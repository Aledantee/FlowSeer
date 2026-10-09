package cable_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestFaultPositionCountsBothDirections(t *testing.T) {
	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	macA := netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	macB := netaddr.MAC{0x02, 0, 0, 0, 0, 2}
	cfg := fabric.Config{
		Start: start,
		Hosts: map[string]fabric.Host{
			"a": {Address: macA, Accept: fabric.HostAccept{Promiscuous: true}},
			"b": {Address: macB, Accept: fabric.HostAccept{Promiscuous: true}},
		},
		Cables: []fabric.Cable{{
			A: fabric.Endpoint{Node: "a"}, B: fabric.Endpoint{Node: "b"},
			Cable: cable.Cable{Medium: cable.TwistedPair, Fault: cable.Fault{Kind: cable.FaultLoseSequence, Sequence: []uint{2, 3}}},
		}},
		PhyAssumption: gigabitCopper(),
	}
	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	links := fab.Links()
	if len(links) != 1 || links[0].A.Oper != port.Up || links[0].B.Oper != port.Up {
		t.Fatalf("links = %+v, want one Up link", links)
	}
	id := bpdu.BridgeID{Priority: 32768, Address: macA}
	protocol, err := bpdu.Encode(bpdu.BPDU{
		Type: bpdu.TypeConfiguration, RootID: id, BridgeID: id, PortID: 0x8001,
		HelloTime: 2 * time.Second, MaxAge: 20 * time.Second, ForwardDelay: 15 * time.Second,
	}, macA)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		origin string
		frame  ethernet.Frame
		want   fabric.JourneyState
	}{
		{name: "position 1 BPDU A to B", origin: "a", frame: protocol, want: fabric.JourneyDelivered},
		{name: "position 2 data B to A", origin: "b", frame: ethernet.Frame{Src: macB, Dst: macA, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("two")}, want: fabric.JourneyDropped},
		{name: "position 3 data A to B", origin: "a", frame: ethernet.Frame{Src: macA, Dst: macB, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("three")}, want: fabric.JourneyDropped},
		{name: "position 4 data B to A", origin: "b", frame: ethernet.Frame{Src: macB, Dst: macA, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("four")}, want: fabric.JourneyDelivered},
	}
	// Apply the four transmissions in position order, advancing after each.
	checked := 0
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fid, err := fab.Inject(fabric.Injection{At: start.Add(time.Duration(i) * time.Millisecond), Origin: fabric.Endpoint{Node: tc.origin}, Frame: tc.frame})
			if err != nil {
				t.Fatal(err)
			}
			result := fab.Run(16)
			if result.Stop != fabric.StopQueueDrained {
				t.Fatalf("Run stop = %v, want QueueDrained", result.Stop)
			}
			journeys := fab.Report()
			if len(journeys) != i+1 {
				t.Fatalf("journeys = %d, want %d", len(journeys), i+1)
			}
			j := journeys[i]
			if j.FrameID != fid {
				t.Fatalf("journey FrameID = %d, want %d", j.FrameID, fid)
			}
			if j.State != tc.want {
				t.Fatalf("journey state = %v, want %v", j.State, tc.want)
			}
			losses := 0
			for _, entry := range j.Entries {
				if entry.Kind != fabric.EntryLoss {
					continue
				}
				losses++
				if entry.Reason != cable.ReasonCableLoss {
					t.Errorf("loss reason = %v, want cable-loss", entry.Reason)
				}
			}
			wantLosses := 0
			if tc.want == fabric.JourneyDropped {
				wantLosses = 1
			}
			if losses != wantLosses {
				t.Fatalf("loss entries = %d, want %d", losses, wantLosses)
			}
			checked++
		})
	}
	if checked != 4 {
		t.Fatalf("checked positions = %d, want 4", checked)
	}
	if journeys := fab.Report(); len(journeys) != 4 {
		t.Errorf("journeys = %d, want 4", len(journeys))
	}
}
