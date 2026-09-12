package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
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

func TestFabricPerHopReadinessMetadataPropagation(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Unknown, OperStatus: port.Unknown})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Down})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	vid10 := vlan.ID(10)
	cfg := fabric.Config{
		Start: time.Now(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: ports,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "vlan10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10},
							"1/1/2": {PVID: &vid10},
							"1/1/3": {PVID: &vid10},
						},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {
				Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
				VLAN:    &vid10,
			},
			"h2": {
				Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x22},
				VLAN:    &vid10,
			},
			"h3": {
				Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x33},
				VLAN:    &vid10,
			},
		},
		Cables: []fabric.Cable{
			{
				A:      fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:      fabric.Endpoint{Node: "h1"},
				Medium: fabric.TwistedPair,
			},
			{
				A:      fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:      fabric.Endpoint{Node: "h2"},
				Medium: fabric.TwistedPair,
			},
			{
				A:      fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
				B:      fabric.Endpoint{Node: "h3"},
				Medium: fabric.TwistedPair,
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}

	// 1. Ingress arriving on unknown oper status port 1/1/2
	_, err = fab.Inject(fabric.Injection{
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
		Frame:  frame,
	})
	if err != nil {
		t.Fatalf("Inject sw1 1/1/2: %v", err)
	}

	// 2. Ingress arriving on known down port 1/1/3
	_, err = fab.Inject(fabric.Injection{
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
		Frame:  frame,
	})
	if err != nil {
		t.Fatalf("Inject sw1 1/1/3: %v", err)
	}

	// 3. Ingress arriving on known up port 1/1/1
	_, err = fab.Inject(fabric.Injection{
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame:  frame,
	})
	if err != nil {
		t.Fatalf("Inject sw1 1/1/1: %v", err)
	}

	fab.Run(20)

	reports := fab.Report()
	if len(reports) != 3 {
		t.Fatalf("got %d journeys, want 3", len(reports))
	}

	findHopResult := func(j fabric.Journey, port string) *vswitch.ForwardResult {
		for _, e := range j.Entries {
			if e.Kind == fabric.EntryHop && e.Port == port && e.Result != nil {
				return e.Result
			}
		}
		return nil
	}

	// Journey 0 (from h2, arriving on 1/1/2 with Unknown oper status):
	// Must propagate Incomplete metadata and must not forward
	resUnknown := findHopResult(reports[0], "1/1/2")
	if resUnknown == nil {
		t.Fatalf("journey 0 missing EntryHop on 1/1/2: %+v", reports[0].Entries)
	}
	if resUnknown.Metadata.Status() != analysis.Incomplete {
		t.Errorf("journey 0 hop result status = %v, want Incomplete", resUnknown.Metadata.Status())
	}
	if issues := resUnknown.Metadata.Issues(); len(issues) == 0 {
		t.Errorf("journey 0 hop result has no issues, want at least one Incomplete issue")
	}

	// Journey 1 (from h3, arriving on 1/1/3 with Down oper status):
	// Definite domain drop with Complete analysis status
	resDown := findHopResult(reports[1], "1/1/3")
	if resDown == nil {
		t.Fatalf("journey 1 missing EntryHop on 1/1/3: %+v", reports[1].Entries)
	}
	if resDown.Outcome != trace.Dropped {
		t.Errorf("journey 1 hop result outcome = %v, want Dropped", resDown.Outcome)
	}
	if resDown.Reason != port.ReasonPortDown {
		t.Errorf("journey 1 hop result reason = %v, want %v", resDown.Reason, port.ReasonPortDown)
	}
	if resDown.Metadata.Status() != analysis.Complete {
		t.Errorf("journey 1 hop result status = %v, want Complete", resDown.Metadata.Status())
	}

	// Journey 2 (from h1, arriving on 1/1/1 with Up oper status):
	// Must remain Complete and unaffected by sibling port uncertainty
	resUp := findHopResult(reports[2], "1/1/1")
	if resUp == nil {
		t.Fatalf("journey 2 missing EntryHop on 1/1/1: %+v", reports[2].Entries)
	}
	if resUp.Metadata.Status() != analysis.Complete {
		t.Errorf("journey 2 hop result status = %v, want Complete", resUp.Metadata.Status())
	}
}
