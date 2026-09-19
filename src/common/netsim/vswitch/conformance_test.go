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
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
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

func TestPlanningConformance(t *testing.T) {
	t.Parallel()
	reg := netsimtest.DefaultRegistry()
	c, ok := reg.Get("planning/port-vlan-change")
	if !ok {
		t.Fatal("planning/port-vlan-change case not found in default registry")
	}

	res := netsimtest.AssertCase(t, c)

	if res.Comparison == nil {
		t.Fatal("res.Comparison is nil")
	}
	if res.Comparison.Disposition != analysis.Different {
		t.Errorf("expected forwarding comparison to detect divergence, got Disposition=%v", res.Comparison.Disposition)
	}
	if res.Comparison.Current.Outcome != trace.Forwarded {
		t.Errorf("Current outcome = %v, want %v", res.Comparison.Current.Outcome, trace.Forwarded)
	}
	if res.Comparison.Expected.Outcome != trace.Dropped {
		t.Errorf("Expected outcome = %v, want %v", res.Comparison.Expected.Outcome, trace.Dropped)
	}

	// Verify typed facts in diff without string parsing.
	var pvidFound, vlansFound bool
	for _, chg := range res.Changes {
		if chg.Subject.Kind == "port" && chg.Subject.Key == "1/1/1" {
			if chg.Field == "pvid" {
				if trace.EqualFact(chg.From, bridge.PVIDFact(10)) && trace.EqualFact(chg.To, bridge.PVIDFact(20)) {
					pvidFound = true
				}
			}
			if chg.Field == "untagged_vlan_ids" {
				if trace.EqualFact(chg.From, bridge.VLANsFact([]vlan.ID{10})) && trace.EqualFact(chg.To, bridge.VLANsFact([]vlan.ID{20})) {
					vlansFound = true
				}
			}
		}
	}
	if !pvidFound {
		t.Error("diff did not emit expected typed bridge.PVIDFact(10 -> 20)")
	}
	if !vlansFound {
		t.Error("diff did not emit expected typed bridge.VLANsFact([10] -> [20])")
	}
}

func TestTroubleshootingConformance(t *testing.T) {
	t.Parallel()
	reg := netsimtest.DefaultRegistry()
	c, ok := reg.Get("troubleshooting/unicast-fdb-forwarding")
	if !ok {
		t.Fatal("troubleshooting/unicast-fdb-forwarding case not found in default registry")
	}

	res := netsimtest.AssertCase(t, c)

	if res.Forward == nil {
		t.Fatal("res.Forward is nil")
	}
	if res.Forward.Outcome != trace.Forwarded {
		t.Errorf("res.Forward.Outcome = %v, want %v", res.Forward.Outcome, trace.Forwarded)
	}
	if res.Forward.Reason != "" {
		t.Errorf("res.Forward.Reason = %q, want empty", res.Forward.Reason)
	}
	if res.Forward.Metadata.Status() != analysis.Complete {
		t.Errorf("res.Forward.Metadata.Status() = %v, want %v", res.Forward.Metadata.Status(), analysis.Complete)
	}

	// Decisive lookup step must match unicast-hit on H2.
	var hitStepFound bool
	for _, step := range res.Steps {
		if step.Op == trace.OpLookup && step.RuleID == "unicast-hit" {
			hitStepFound = true
			if step.Subject.Kind != "mac" || step.Subject.Key != "00:11:22:33:44:02" {
				t.Errorf("unicast-hit subject = %s, want mac:00:11:22:33:44:02", step.Subject)
			}
		}
	}
	if !hitStepFound {
		t.Error("steps missing decisive OpLookup unicast-hit rule")
	}
}

func TestMDNSMulticastConformance(t *testing.T) {
	t.Parallel()
	reg := netsimtest.DefaultRegistry()

	t.Run("ipv4 floods under snooping", func(t *testing.T) {
		c, ok := reg.Get("troubleshooting/mdns-ipv4-floods-under-snooping")
		if !ok {
			t.Fatal("troubleshooting/mdns-ipv4-floods-under-snooping case not found in default registry")
		}

		res := netsimtest.AssertCase(t, c)
		if res.Forward == nil {
			t.Fatal("res.Forward is nil")
		}

		wantPorts := []string{"p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9"}
		gotPorts := make([]string, len(res.Forward.Egress))
		for i, egress := range res.Forward.Egress {
			gotPorts[i] = egress.Port
		}
		if !slices.Equal(gotPorts, wantPorts) {
			t.Errorf("forwarded ports = %v, want %v", gotPorts, wantPorts)
		}
	})

	t.Run("ipv6 unregistered router ports", func(t *testing.T) {
		c, ok := reg.Get("troubleshooting/mdns-ipv6-unregistered-router-ports")
		if !ok {
			t.Fatal("troubleshooting/mdns-ipv6-unregistered-router-ports case not found in default registry")
		}

		res := netsimtest.AssertCase(t, c)
		if res.Forward == nil {
			t.Fatal("res.Forward is nil")
		}

		wantPorts := []string{"p9"}
		gotPorts := make([]string, len(res.Forward.Egress))
		for i, egress := range res.Forward.Egress {
			gotPorts[i] = egress.Port
		}
		if !slices.Equal(gotPorts, wantPorts) {
			t.Errorf("forwarded ports = %v, want %v", gotPorts, wantPorts)
		}
	})
}

func TestConstructorValidationAndNormalization(t *testing.T) {
	t.Parallel()

	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("port.NewBuilder failed: %v", err)
	}

	// STP without bridge is rejected.
	_, err = vswitch.New(vswitch.Config{
		Ports: ports,
		STP:   &stp.Config{},
	})
	if err == nil {
		t.Error("vswitch.New with STP but no Bridge did not return an error")
	}

	// Port table referencing invalid switchport interface is rejected.
	vid10 := vlan.ID(10)
	invalidCfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "prod"},
				Switchports: map[string]bridge.Switchport{
					"1/1/nonexistent": {PVID: &vid10},
				},
			},
		},
	}
	_, err = vswitch.New(invalidCfg)
	if err == nil {
		t.Error("vswitch.New with invalid switchport port reference did not return an error")
	}

	// Valid ConstructionSpec with seeds restores preloaded FDB.
	validCfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "prod"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	}
	macSeed := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}
	spec := vswitch.ConstructionSpec{
		Config: validCfg,
		Seeds: []bridge.Seed{{
			FID:      10,
			MAC:      macSeed,
			Port:     "1/1/1",
			Lifetime: bridge.Static,
		}},
	}
	sw, err := vswitch.NewWithSpec(spec)
	if err != nil {
		t.Fatalf("vswitch.NewWithSpec failed: %v", err)
	}

	entries := sw.Entries()
	if len(entries) != 1 || entries[0].MAC != macSeed {
		t.Errorf("sw.Entries() = %v, want 1 entry with MAC %v", entries, macSeed)
	}
}

func TestForwardResultSeparatesDomainOutcomeFromReadiness(t *testing.T) {
	t.Parallel()

	// A known-down port produces a definite domain drop (Outcome: Dropped, Reason: port-down)
	// while retaining Complete analysis readiness.
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	vid10 := vlan.ID(10)
	sw, err := vswitch.New(vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "prod"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("vswitch.New failed: %v", err)
	}

	now := time.Unix(1700000000, 0)
	f := ethernet.Frame{
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}

	res := sw.Forward(now, "1/1/1", f)
	if res.Outcome != trace.Dropped {
		t.Errorf("Outcome = %v, want %v", res.Outcome, trace.Dropped)
	}
	if res.Reason != port.ReasonPortDown {
		t.Errorf("Reason = %v, want %v", res.Reason, port.ReasonPortDown)
	}
	if res.Metadata.Status() != analysis.Complete {
		t.Errorf("readiness status = %v, want %v (known-down port is authoritative drop)", res.Metadata.Status(), analysis.Complete)
	}
}
