package vswitch_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func twoPortTable(t *testing.T) port.Table {
	t.Helper()
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}
	return tbl
}

func TestPublicConstructorValidationFieldPaths(t *testing.T) {
	ports := twoPortTable(t)

	t.Run("invalid sub-configurations", func(t *testing.T) {
		// Invalid bridge admission
		invalidBridge := bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {Admission: "BogusAdmission"},
				},
			},
		}
		_, err := vswitch.New(vswitch.Config{Ports: ports, Bridge: &invalidBridge})
		if err == nil {
			t.Fatal("expected error for invalid bridge admission, got nil")
		}
		attrs := errs.Attributes(err)
		if attrs["field"] != "vlan.switchports.1/1/1.admission" {
			t.Errorf("field attr = %v, want vlan.switchports.1/1/1.admission", attrs["field"])
		}

		// Direct capability constructor returns error on invalid config
		_, err = bridge.New(invalidBridge, ports)
		if err == nil {
			t.Fatal("expected bridge.New to return error for invalid admission")
		}
	})

	t.Run("cross-capability references retain stable field paths", func(t *testing.T) {
		// STP requires Bridge
		stpCfg := &stp.Config{
			Ports: map[string]stp.Port{
				"1/1/1": {Priority: 128},
			},
		}
		_, err := vswitch.New(vswitch.Config{Ports: ports, STP: stpCfg})
		if err == nil {
			t.Fatal("expected error for STP without Bridge")
		}
		attrs := errs.Attributes(err)
		if attrs["field"] != "stp" {
			t.Errorf("field attr = %v, want stp", attrs["field"])
		}

		// Multicast requires Bridge VLAN
		mcastCfg := &mcast.Config{
			VLANs: map[vlan.ID]mcast.VLANSnooping{10: {}},
		}
		_, err = vswitch.New(vswitch.Config{Ports: ports, Mcast: mcastCfg})
		if err == nil {
			t.Fatal("expected error for Mcast without Bridge VLAN")
		}
		attrs = errs.Attributes(err)
		if attrs["field"] != "mcast" {
			t.Errorf("field attr = %v, want mcast", attrs["field"])
		}

		// Routed VLAN interface absent from bridge VLAN table
		routingCfg := &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"vlan10": {
							VLAN: 10,
							Prefixes: []netip.Prefix{
								netip.MustParsePrefix("10.0.10.1/24"),
							},
						},
					},
				},
			},
		}
		bridgeCfg := &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: ptrVID(20)},
				},
			},
		}
		_, err = vswitch.New(vswitch.Config{Ports: ports, Bridge: bridgeCfg, Routing: routingCfg})
		if err == nil {
			t.Fatal("expected error for routed VLAN interface absent from bridge VLAN table")
		}
		attrs = errs.Attributes(err)
		if attrs["field"] != "routing.vrfs.default.interfaces.vlan10.vlan" {
			t.Errorf("field attr = %v, want routing.vrfs.default.interfaces.vlan10.vlan", attrs["field"])
		}
	})
}

func ptrVID(v vlan.ID) *vlan.ID {
	return &v
}

func TestConstructionSpecIdentityAndSeedDifferences(t *testing.T) {
	ports := twoPortTable(t)
	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: ptrVID(10)},
					"1/1/2": {PVID: ptrVID(10)},
				},
			},
		},
	}

	sw1, err := vswitch.New(cfg)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}
	sw2, err := vswitch.New(cfg)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}

	spec1 := sw1.Spec()
	spec2 := sw2.Spec()

	if !spec1.Equal(spec2) {
		t.Errorf("expected identical construction specs for identical input, got unequal")
	}

	// Now add different static seeds
	seed1 := bridge.Seed{
		FID:    10,
		MAC:    netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Port:   "1/1/1",
		Static: true,
	}
	specWithSeed := spec1.Clone()
	specWithSeed.Seeds = append(specWithSeed.Seeds, seed1)

	swWithSeed, err := vswitch.NewWithSpec(specWithSeed)
	if err != nil {
		t.Fatalf("vswitch.NewWithSpec: %v", err)
	}

	if spec1.Equal(swWithSeed.Spec()) {
		t.Errorf("expected construction spec with static seed to differ from spec without seed")
	}

	// Verify returned spec clone isolation
	returnedSpec := swWithSeed.Spec()
	returnedSpec.Seeds[0].Port = "mutated"
	if swWithSeed.Spec().Seeds[0].Port == "mutated" {
		t.Errorf("mutating returned spec affected internal switch construction spec")
	}
}

func TestConstructionSpecValidatesAndNormalizesSeeds(t *testing.T) {
	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "access", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "outside", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	bridgeConfig := &bridge.Config{VLAN: &bridge.VLAN{
		Table: map[vlan.ID]string{vid10: "ten"},
		Switchports: map[string]bridge.Switchport{
			"lag1":   {PVID: &vid10, Untagged: []vlan.ID{vid10}},
			"access": {PVID: &vid10, Untagged: []vlan.ID{vid10}},
		},
	}}
	validMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	tests := []struct {
		name      string
		config    vswitch.Config
		seeds     []bridge.Seed
		wantField string
	}{
		{
			name:      "relay required",
			config:    vswitch.Config{Ports: ports},
			seeds:     []bridge.Seed{{FID: vid10, MAC: validMAC, Port: "access"}},
			wantField: "seeds",
		},
		{
			name:      "zero MAC",
			config:    vswitch.Config{Ports: ports, Bridge: bridgeConfig},
			seeds:     []bridge.Seed{{FID: vid10, Port: "access"}},
			wantField: "seeds.0.mac",
		},
		{
			name:      "group MAC",
			config:    vswitch.Config{Ports: ports, Bridge: bridgeConfig},
			seeds:     []bridge.Seed{{FID: vid10, MAC: netaddr.MAC{0x01}, Port: "access"}},
			wantField: "seeds.0.mac",
		},
		{
			name:      "unknown port",
			config:    vswitch.Config{Ports: ports, Bridge: bridgeConfig},
			seeds:     []bridge.Seed{{FID: vid10, MAC: validMAC, Port: "missing"}},
			wantField: "seeds.0.port",
		},
		{
			name:      "FID outside VLAN range",
			config:    vswitch.Config{Ports: ports, Bridge: bridgeConfig},
			seeds:     []bridge.Seed{{FID: 0, MAC: validMAC, Port: "access"}},
			wantField: "seeds.0.fid",
		},
		{
			name:      "FID absent from table",
			config:    vswitch.Config{Ports: ports, Bridge: bridgeConfig},
			seeds:     []bridge.Seed{{FID: vid20, MAC: validMAC, Port: "access"}},
			wantField: "seeds.0.fid",
		},
		{
			name:      "port not admitted to FID",
			config:    vswitch.Config{Ports: ports, Bridge: bridgeConfig},
			seeds:     []bridge.Seed{{FID: vid10, MAC: validMAC, Port: "outside"}},
			wantField: "seeds.0.port",
		},
		{
			name:   "nonzero FID without VLAN awareness",
			config: vswitch.Config{Ports: ports, Bridge: &bridge.Config{}},
			seeds: []bridge.Seed{
				{FID: vid10, MAC: validMAC, Port: "access"},
			},
			wantField: "seeds.0.fid",
		},
		{
			name:   "duplicate FID and MAC",
			config: vswitch.Config{Ports: ports, Bridge: bridgeConfig},
			seeds: []bridge.Seed{
				{FID: vid10, MAC: validMAC, Port: "access"},
				{FID: vid10, MAC: validMAC, Port: "lag1"},
			},
			wantField: "seeds.1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{Config: test.config, Seeds: test.seeds})
			if err == nil {
				t.Fatal("NewWithSpec() = nil error, want validation error")
			}
			if got := errs.Attributes(err)["field"]; got != test.wantField {
				t.Errorf("field = %v, want %q", got, test.wantField)
			}
		})
	}

	secondMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}
	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: vswitch.Config{Ports: ports, Bridge: bridgeConfig},
		Seeds: []bridge.Seed{
			{FID: vid10, MAC: secondMAC, Port: "access", Static: true},
			{FID: vid10, MAC: validMAC, Port: "member", Static: true},
		},
	})
	if err != nil {
		t.Fatalf("NewWithSpec() error = %v", err)
	}
	wantSeeds := []bridge.Seed{
		{FID: vid10, MAC: validMAC, Port: "lag1", Static: true},
		{FID: vid10, MAC: secondMAC, Port: "access", Static: true},
	}
	if got := sw.Spec().Seeds; !slices.Equal(got, wantSeeds) {
		t.Errorf("normalized seeds = %+v, want %+v", got, wantSeeds)
	}
}

func TestSwitchLearnRejectsInvalidSeedsWithoutRecordingThem(t *testing.T) {
	ports := twoPortTable(t)
	sw, err := vswitch.New(vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = sw.Learn([]bridge.Seed{{MAC: netaddr.MAC{0x01}, Port: "1/1/1"}})
	if err == nil {
		t.Fatal("Learn() = nil error, want invalid MAC error")
	}
	if got := errs.Attributes(err)["field"]; got != "seeds.0.mac" {
		t.Errorf("field = %v, want seeds.0.mac", got)
	}
	if got := sw.Spec().Seeds; len(got) != 0 {
		t.Errorf("Spec().Seeds = %+v, want none", got)
	}
}

func TestForwardMetadataKeepsEvidenceLinkedAssumptions(t *testing.T) {
	ports := twoPortTable(t)
	catalog, ref := (analysis.EvidenceCatalog{}).Add(analysis.Evidence{
		Kind:    "snapshot",
		Origin:  "device-state",
		Context: "conflicting output state",
	})
	metadata := analysis.NewMetadata(
		analysis.NodeScope("sw1"),
		[]analysis.Issue{{
			Code:     "test.output-conflict",
			Status:   analysis.Unstable,
			Scope:    analysis.PortScope("sw1", "1/1/2"),
			Message:  "output state conflicts",
			Evidence: []trace.EvidenceRef{ref},
		}},
		catalog,
		[]analysis.Assumption{{
			Scope:     analysis.FieldScope(analysis.NodeScope("sw1"), "snapshot-window"),
			Statement: "conflicting rows came from the same snapshot window",
			Evidence:  []trace.EvidenceRef{ref},
		}},
	)
	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config:   vswitch.Config{Ports: ports},
		NodeID:   "sw1",
		Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	forwarded := sw.Peek(fixedTime, "1/1/1", ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x01, 0x02, 0x03, 0x04, 0x05},
		Dst: netaddr.MAC{0x00, 0x01, 0x02, 0x03, 0x04, 0x06},
	})
	if assumptions := forwarded.Metadata.Assumptions(); len(assumptions) != 1 || assumptions[0].Statement != "conflicting rows came from the same snapshot window" {
		t.Fatalf("forward assumptions = %+v, want evidence-linked snapshot assumption", assumptions)
	}
	if _, ok := forwarded.Metadata.Evidence().Lookup(ref); !ok {
		t.Errorf("forward evidence catalog does not contain %s", ref)
	}
}

func TestForwardResultEnvelopeAndUnknownOperStatus(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: ptrVID(10)},
					"1/1/2": {PVID: ptrVID(10)},
					"1/1/3": {PVID: ptrVID(10)},
				},
			},
		},
	}

	sw, err := vswitch.New(cfg)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}

	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x01, 0x02, 0x03, 0x04, 0x05},
		Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}
	now := time.Now()

	// Ingress on Unknown oper status port: produces Incomplete issue and does not forward
	resUnknown := sw.Forward(now, "1/1/1", frame)
	if resUnknown.Metadata.Status() != analysis.Incomplete {
		t.Errorf("unknown oper status Forward status = %v, want Incomplete", resUnknown.Metadata.Status())
	}
	if resUnknown.Outcome == trace.Forwarded || resUnknown.Outcome == trace.Flooded {
		t.Errorf("unknown oper status forwarded frame, outcome = %v", resUnknown.Outcome)
	}
	issues := resUnknown.Metadata.Issues()
	if len(issues) == 0 {
		t.Fatal("expected at least one issue for unknown oper status, got 0")
	}
	if !issues[0].Scope.Overlaps(analysis.PortScope("", "1/1/1")) {
		t.Errorf("issue scope = %v, want overlap with port 1/1/1", issues[0].Scope)
	}

	// Peek also returns the same envelope without mutation
	peekUnknown := sw.Peek(now, "1/1/1", frame)
	if peekUnknown.Metadata.Status() != analysis.Incomplete {
		t.Errorf("Peek status = %v, want Incomplete", peekUnknown.Metadata.Status())
	}

	// Ingress on Known Down port: definite domain drop, NOT mislabeled as incomplete
	resDown := sw.Forward(now, "1/1/2", frame)
	if resDown.Outcome != trace.Dropped {
		t.Errorf("known down port outcome = %v, want Dropped", resDown.Outcome)
	}
	if resDown.Reason != port.ReasonPortDown {
		t.Errorf("known down port reason = %v, want %v", resDown.Reason, port.ReasonPortDown)
	}
	if resDown.Metadata.Status() != analysis.Complete {
		t.Errorf("known down port Forward status = %v, want Complete (not incomplete)", resDown.Metadata.Status())
	}

	// Ingress on Known Up port: sibling port uncertainty does NOT taint unrelated port
	resUp := sw.Forward(now, "1/1/3", frame)
	if resUp.Metadata.Status() != analysis.Complete {
		t.Errorf("unrelated known-up port Forward status = %v, want Complete", resUp.Metadata.Status())
	}
	if len(resUp.Metadata.Issues()) != 0 {
		t.Errorf("unrelated known-up port has unexpected issues: %+v", resUp.Metadata.Issues())
	}
}

func TestRepresentativeSemanticTraceSteps(t *testing.T) {
	ports := twoPortTable(t)
	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: ptrVID(10)},
					"1/1/2": {PVID: ptrVID(10)},
				},
			},
		},
		Traffic: &traffic.Config{
			Mirrors: []traffic.Mirror{
				{
					Name:           "m1",
					SelectSrcPorts: []string{"1/1/1"},
					OutputPort:     "1/1/2",
				},
			},
		},
	}

	sw, err := vswitch.New(cfg)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}

	now := time.Now()
	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Dst: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
	}

	res := sw.Forward(now, "1/1/1", frame)
	// Check that we have typed trace steps exposing decisive rules without string parsing
	var hasClassify, hasLearn, hasLookup bool
	for _, step := range res.Steps {
		if step.Op == trace.OpClassify && step.RuleID != "" {
			hasClassify = true
			if step.Subject.Kind == "" {
				t.Errorf("classify step has empty subject kind: %+v", step)
			}
		}
		if step.Op == trace.OpLearn && step.RuleID != "" {
			hasLearn = true
			if step.Subject.Kind != "mac" {
				t.Errorf("learn step subject kind = %q, want 'mac'", step.Subject.Kind)
			}
		}
		if (step.Op == trace.OpLookup || step.Op == trace.OpReplicate) && step.RuleID != "" {
			hasLookup = true
		}
	}

	if !hasClassify {
		t.Errorf("trace missing classify step with RuleID: %+v", res.Steps)
	}
	if !hasLearn {
		t.Errorf("trace missing learn step with RuleID: %+v", res.Steps)
	}
	if !hasLookup {
		t.Errorf("trace missing lookup/replicate step with RuleID: %+v", res.Steps)
	}

	// Mirror copies returned
	copies := sw.Copies()
	if len(copies) != 1 {
		t.Fatalf("got %d mirror copies, want 1", len(copies))
	}
}
