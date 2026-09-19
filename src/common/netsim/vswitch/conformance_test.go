package vswitch_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Conformance group: result-canonicality
func TestConformanceResultCanonicality(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	macA := netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	macB := netaddr.MAC{0x02, 0, 0, 0, 0, 2}
	v10 := vlan.ID(10)

	buildSwitch := func(portNames []string) *vswitch.Switch {
		b := port.NewBuilder()
		for _, name := range portNames {
			b.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		}
		pTable, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}

		swports := make(map[string]bridge.Switchport, len(portNames))
		for _, name := range portNames {
			swports[name] = bridge.Switchport{
				PVID:     &v10,
				Untagged: []vlan.ID{10},
			}
		}

		sw, err := vswitch.New(vswitch.Config{
			Ports: pTable,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table:       map[vlan.ID]string{10: "VLAN10"},
					Switchports: swports,
				},
			},
		})
		if err != nil {
			t.Fatalf("vswitch.New: %v", err)
		}
		return sw
	}

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test-payload"),
	}

	orderA := []string{"1/1/1", "1/1/2", "1/1/3"}
	orderB := netsimtest.PermuteOrder(orderA, 42)

	// Randomized map and port insertion order
	swA := buildSwitch(orderA)
	swB := buildSwitch(orderB)

	// Current-first and candidate-first execution
	resCur := swA.Forward(t0, "1/1/1", frame)
	resCand := swB.Forward(t0, "1/1/1", frame)

	cmpAB := vswitch.CompareResults(resCur, resCand)
	if cmpAB.Disposition != analysis.Equivalent {
		t.Errorf("CompareResults(resCur, resCand) = %v, want Equivalent", cmpAB.Disposition)
	}

	cmpBA := vswitch.CompareResults(resCand, resCur)
	if cmpBA.Disposition != analysis.Equivalent {
		t.Errorf("CompareResults(resCand, resCur) = %v, want Equivalent", cmpBA.Disposition)
	}

	if len(resCur.Egress) != len(resCand.Egress) {
		t.Fatalf("egress counts mismatch: %d vs %d", len(resCur.Egress), len(resCand.Egress))
	}
	for i := range resCur.Egress {
		if resCur.Egress[i].Port != resCand.Egress[i].Port {
			t.Errorf("egress[%d] port = %q, want %q", i, resCur.Egress[i].Port, resCand.Egress[i].Port)
		}
	}
}

// Conformance group: ordering-determinism
func TestConformanceOrderingDeterminism(t *testing.T) {
	t.Parallel()

	rawPorts := []string{"1/1/5", "1/1/1", "1/1/3", "1/1/2", "1/1/4"}

	// Build port table with multiple permuted orders
	for seed := int64(1); seed <= 5; seed++ {
		shuffled := netsimtest.PermuteOrder(rawPorts, seed)
		b := port.NewBuilder()
		for _, name := range shuffled {
			b.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		}
		pTable, err := b.Build()
		if err != nil {
			t.Fatalf("seed %d build: %v", seed, err)
		}

		ports := pTable.Ports()
		gotNames := make([]string, len(ports))
		for i, p := range ports {
			gotNames[i] = p.Name
		}
		wantNames := []string{"1/1/1", "1/1/2", "1/1/3", "1/1/4", "1/1/5"}
		if !slices.Equal(gotNames, wantNames) {
			t.Errorf("seed %d: gotNames = %v, want %v", seed, gotNames, wantNames)
		}
	}
}

// Conformance group: trace-fact-stability
func TestConformanceTraceFactStability(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	macA := netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	macB := netaddr.MAC{0x02, 0, 0, 0, 0, 2}
	v10 := vlan.ID(10)

	buildSw := func() *vswitch.Switch {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		pTable, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}

		sw, err := vswitch.New(vswitch.Config{
			Ports: pTable,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "VLAN10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &v10, Untagged: []vlan.ID{10}},
						"1/1/2": {PVID: &v10, Untagged: []vlan.ID{10}},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("vswitch.New: %v", err)
		}
		return sw
	}

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("payload"),
	}

	sw1 := buildSw()
	sw2 := buildSw()
	res1 := sw1.Forward(t0, "1/1/1", frame)
	res2 := sw2.Forward(t0, "1/1/1", frame)

	if len(res1.Steps) != len(res2.Steps) {
		t.Fatalf("steps count mismatch: %d vs %d", len(res1.Steps), len(res2.Steps))
	}
	for i := range res1.Steps {
		if !trace.EqualStep(res1.Steps[i], res2.Steps[i]) {
			t.Errorf("step %d mismatch: %+v vs %+v", i, res1.Steps[i], res2.Steps[i])
		}
	}
}

// Conformance group: derive-invalidation
func TestConformanceDeriveInvalidation(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	macA := netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	macB := netaddr.MAC{0x02, 0, 0, 0, 0, 2}
	v10 := vlan.ID(10)

	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	sw, err := vswitch.New(vswitch.Config{
		Ports: pTable,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "VLAN10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &v10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &v10, Untagged: []vlan.ID{10}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("payload"),
	}

	// 1. Initial state: port 1/1/2 is UP, flooded packet egresses out 1/1/2
	resUp := sw.Forward(t0, "1/1/1", frame)
	if len(resUp.Egress) != 1 || resUp.Egress[0].Port != "1/1/2" {
		t.Fatalf("initial egress = %+v, want egress on 1/1/2", resUp.Egress)
	}

	// 2. Invalidation: set port 1/1/2 Down
	if err := sw.SetOperStatus("1/1/2", port.Down); err != nil {
		t.Fatalf("SetOperStatus: %v", err)
	}

	// 3. Derived state invalidated: packet must not egress on down port
	resDown := sw.Forward(t0, "1/1/1", frame)
	for _, eg := range resDown.Egress {
		if eg.Port == "1/1/2" && eg.Dropped == "" {
			t.Fatalf("egress on down port 1/1/2 was not invalidated: %+v", resDown.Egress)
		}
	}
}
