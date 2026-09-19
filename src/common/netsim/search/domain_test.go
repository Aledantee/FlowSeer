package search

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

var (
	_ Domain = (*L2TrafficDomain)(nil)
	_ Domain = (*TimedFaultDomain)(nil)
)

func TestTupleString(t *testing.T) {
	t.Parallel()

	tuple := Tuple("test-tuple-123")
	if tuple.String() != "test-tuple-123" {
		t.Errorf("Tuple.String() = %q, want test-tuple-123", tuple.String())
	}
}

func TestCandidateCloneIsolation(t *testing.T) {
	t.Parallel()

	orig := Candidate{
		Tuple: "t1",
		Scenario: []fabric.Injection{
			{
				At:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Origin: fabric.Endpoint{Node: "h1", Port: "eth0"},
				Frame: ethernet.Frame{
					Dst:       netaddr.MAC{0x02, 0, 0, 0, 0, 2},
					Src:       netaddr.MAC{0x02, 0, 0, 0, 0, 1},
					Tags:      []vlan.Tag{{VID: 10}},
					EtherType: ethernet.EtherTypeIPv4,
					Payload:   []byte{1, 2, 3},
				},
				Packet: &fabric.Packet{
					Payload: []byte{4, 5, 6},
				},
			},
		},
		Faults: []TimedFault{
			{
				At: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
				A:  fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:  fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				Fault: fabric.Fault{
					Kind:     fabric.FaultLoseSequence,
					Sequence: []uint{1, 3},
				},
			},
		},
	}

	cloned := orig.Clone()

	// Mutate clone
	cloned.Tuple = "t2"
	cloned.Scenario[0].Frame.Tags[0].VID = 20
	cloned.Scenario[0].Frame.Payload[0] = 99
	cloned.Scenario[0].Packet.Payload[0] = 88
	cloned.Faults[0].Fault.Sequence[0] = 77

	// Verify original untouched
	if orig.Tuple != "t1" {
		t.Errorf("orig.Tuple was mutated to %s", orig.Tuple)
	}
	if orig.Scenario[0].Frame.Tags[0].VID != 10 {
		t.Errorf("orig.Scenario tag VID was mutated: %d", orig.Scenario[0].Frame.Tags[0].VID)
	}
	if orig.Scenario[0].Frame.Payload[0] != 1 {
		t.Errorf("orig.Scenario payload was mutated: %d", orig.Scenario[0].Frame.Payload[0])
	}
	if orig.Scenario[0].Packet.Payload[0] != 4 {
		t.Errorf("orig.Scenario packet was mutated: %d", orig.Scenario[0].Packet.Payload[0])
	}
	if orig.Faults[0].Fault.Sequence[0] != 1 {
		t.Errorf("orig.Faults sequence was mutated: %d", orig.Faults[0].Fault.Sequence[0])
	}
}

type dummyL3Domain struct {
	Domain
}

func (d *dummyL3Domain) RoutedSubnets() []netip.Prefix {
	return nil
}

var _ L3Domain = (*dummyL3Domain)(nil)
