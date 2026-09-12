package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func TestFabricConstructionSpecAndPropagation(t *testing.T) {
	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	p1, _ := b1.Build()

	vid10 := vlan.ID(10)
	cfg := fabric.Config{
		Start: time.Now(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: p1,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "vlan10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10},
							"1/1/2": {PVID: &vid10},
						},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {
				Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
				VLAN:    &vid10,
			},
		},
		Cables: []fabric.Cable{
			{
				A:      fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:      fabric.Endpoint{Node: "h1"},
				Medium: fabric.TwistedPair,
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	spec := fab.Spec()
	if spec.Config.Switches == nil {
		t.Errorf("expected spec to contain normalized switches")
	}

	// Verify clone isolation
	specCopy := spec.Clone()
	if !spec.Equal(specCopy) {
		t.Errorf("cloned spec should equal original")
	}

	// Inject frame and run
	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}
	_, err = fab.Inject(fabric.Injection{
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  frame,
	})
	if err != nil {
		t.Fatalf("fab.Inject: %v", err)
	}

	fab.Run(10)

	report := fab.Report()
	if len(report) != 1 {
		t.Fatalf("got %d journeys, want 1", len(report))
	}
	journey := report[0]

	// Verify journey entry results are vswitch.ForwardResult envelopes with metadata
	var foundHopResult bool
	for _, entry := range journey.Entries {
		if entry.Kind == fabric.EntryHop && entry.Result != nil {
			foundHopResult = true
			if entry.Result.Metadata.Status() != analysis.Complete {
				t.Errorf("hop forward result status = %v, want Complete", entry.Result.Metadata.Status())
			}
		}
	}
	if !foundHopResult {
		t.Errorf("journey missing EntryHop with non-nil Result: %+v", journey.Entries)
	}
}
