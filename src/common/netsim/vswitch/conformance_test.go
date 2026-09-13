package vswitch_test

import (
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

func TestPlanningConformance(t *testing.T) {
	reg := netsimtest.DefaultRegistry()
	c, ok := reg.Get("planning/port-vlan-change")
	if !ok {
		t.Fatal("planning/port-vlan-change case not found in default registry")
	}

	res := netsimtest.AssertCase(t, c)

	if res.Comparison == nil {
		t.Fatal("res.Comparison is nil")
	}
	if res.Comparison.Same {
		t.Error("expected forwarding comparison to detect divergence, got Same=true")
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

func TestConstructorValidationAndNormalization(t *testing.T) {
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
			FID:    10,
			MAC:    macSeed,
			Port:   "1/1/1",
			Static: true,
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
