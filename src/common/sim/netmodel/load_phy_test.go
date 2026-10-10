package netmodel_test

import (
	"slices"
	"testing"

	"buf.build/go/protovalidate"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestNetmodel_PortWithoutPoeDetailSkipped(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	name := "1/1/1"
	poeSup := true
	role := phyv1.PoeRole_POE_ROLE_PSE
	group1 := uint32(1)
	power := uint64(100_000_000_000)

	budget := phyv1.PseBudget_builder{
		PseGroup:       &group1,
		PowerNanowatts: &power,
	}.Build()

	iface := interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Ethernet: phyv1.EthernetFacet_builder{
				Copper: phyv1.CopperFacet_builder{
					Poe: phyv1.PoeFacet_builder{
						Supported: &poeSup,
						Role:      &role,
					}.Build(),
					// Deliberately missing PoeDetail
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{iface}, nil, nil, []*phyv1.PseBudget{budget}, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	foundSkipped := false
	for _, s := range report.Skipped {
		if s.Port == "1/1/1" && s.What == "poe" && s.Why == "missing poe_detail" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("got Skipped %+v, want poe without detail skipped", report.Skipped)
	}

	if cfg.Phy != nil && cfg.Phy.PoE != nil && len(cfg.Phy.PoE.Ports) != 0 {
		t.Errorf("got %d PoE ports loaded, want 0", len(cfg.Phy.PoE.Ports))
	}
}

func TestPoeExportAndLoadRoundTrip(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	mtu0 := uint32(0)
	grp := uint32(1)

	loadExported := func(t *testing.T, phyCfg phy.Config, alloc phy.Allocation) netmodel.Result {
		t.Helper()
		budgets, facets, err := netmodel.Poe(phyCfg, alloc)
		if err != nil {
			t.Fatalf("Poe export failed: %v", err)
		}
		for _, b := range budgets {
			if err := protovalidate.Validate(b); err != nil {
				t.Fatalf("budget protovalidate failed: %v", err)
			}
		}
		for name, f := range facets {
			if err := protovalidate.Validate(f); err != nil {
				t.Fatalf("facet %s protovalidate failed: %v", name, err)
			}
		}

		names := make([]string, 0, len(facets))
		for name := range facets {
			names = append(names, name)
		}
		slices.Sort(names)
		ifaces := make([]*interfacev1.Interface, 0, len(facets))
		for _, name := range names {
			pName := name
			iface := interfacev1.Interface_builder{
				Name:        &pName,
				AdminStatus: &adminUp,
				OperStatus:  &operUp,
				Mtu:         &mtu0,
				Physical: interfacev1.PhysicalInterface_builder{
					Ethernet: phyv1.EthernetFacet_builder{
						Copper: phyv1.CopperFacet_builder{
							Poe: facets[pName],
							PoeDetail: phyv1.PoePortDetail_builder{
								PseGroup: &grp,
								PsePort:  ptr(uint32(1)),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
			if err := protovalidate.Validate(iface); err != nil {
				t.Fatalf("iface %s protovalidate failed: %v", pName, err)
			}
			ifaces = append(ifaces, iface)
		}

		res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, budgets, nil, nil, nil, nil, nil, nil, nil, []trace.Layer{phy.LayerNamePoE})
		if err != nil {
			t.Fatalf("netmodel.Load failed: %v", err)
		}
		return res
	}

	t.Run("delivered", func(t *testing.T) {
		phyCfg := phy.Config{
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerNanowatts: 100_000_000_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityCritical, PD: phy.PDAttached, PDClass: phy.Class(2)},
				},
			},
		}
		alloc := phyCfg.Allocate()
		if alloc.Ports["1/1/1"].State != phy.PowerDelivered {
			t.Fatalf("got power state %v, want PowerDelivered", alloc.Ports["1/1/1"].State)
		}

		res := loadExported(t, phyCfg, alloc)
		p, ok := res.Spec.Config.Phy.PoE.Ports["1/1/1"]
		if !ok {
			t.Fatal("port 1/1/1 missing from loaded config")
		}
		if p.PD != phy.PDAttached {
			t.Errorf("PD = %v, want Attached", p.PD)
		}
		if p.PDClass == nil || *p.PDClass != 2 {
			t.Errorf("PDClass = %v, want 2", p.PDClass)
		}
		for _, a := range res.Metadata.Assumptions() {
			if a.Statement == "absence is inferred from the searching status" {
				t.Errorf("unexpected searching assumption: %+v", a)
			}
		}
	})

	t.Run("disabled", func(t *testing.T) {
		phyCfg := phy.Config{
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerNanowatts: 100_000_000_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {Group: "1", MaxClass: 8, Enabled: false, Priority: phy.PriorityCritical, PD: phy.PDAttached, PDClass: phy.Class(2)},
				},
			},
		}
		alloc := phyCfg.Allocate()
		if alloc.Ports["1/1/1"].State != phy.PowerDenied || alloc.Ports["1/1/1"].Denial != phy.ReasonDisabled {
			t.Fatalf("got port allocation %+v, want PowerDenied(disabled)", alloc.Ports["1/1/1"])
		}

		res := loadExported(t, phyCfg, alloc)
		p, ok := res.Spec.Config.Phy.PoE.Ports["1/1/1"]
		if !ok {
			t.Fatal("port 1/1/1 missing from loaded config")
		}
		if p.PD != phy.PDUnknown {
			t.Errorf("PD = %v, want Unknown", p.PD)
		}
		if p.PDClass != nil {
			t.Errorf("PDClass = %v, want nil", p.PDClass)
		}
		for _, a := range res.Metadata.Assumptions() {
			if a.Statement == "absence is inferred from the searching status" {
				t.Errorf("unexpected searching assumption: %+v", a)
			}
		}
	})

	t.Run("no device", func(t *testing.T) {
		phyCfg := phy.Config{
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerNanowatts: 100_000_000_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityCritical, PD: phy.PDAbsent},
				},
			},
		}
		alloc := phyCfg.Allocate()
		if alloc.Ports["1/1/1"].State != phy.PowerNoDevice {
			t.Fatalf("got power state %v, want PowerNoDevice", alloc.Ports["1/1/1"].State)
		}

		res := loadExported(t, phyCfg, alloc)
		p, ok := res.Spec.Config.Phy.PoE.Ports["1/1/1"]
		if !ok {
			t.Fatal("port 1/1/1 missing from loaded config")
		}
		if p.PD != phy.PDAbsent {
			t.Errorf("PD = %v, want Absent", p.PD)
		}
		if p.PDClass != nil {
			t.Errorf("PDClass = %v, want nil", p.PDClass)
		}

		portScope := analysis.PortScope("sw1", "1/1/1")
		var portAssumptions []analysis.Assumption
		for _, a := range res.Metadata.Assumptions() {
			if a.Scope.Compare(portScope) == 0 && a.Statement == "absence is inferred from the searching status" {
				portAssumptions = append(portAssumptions, a)
			}
		}
		if len(portAssumptions) != 1 {
			t.Fatalf("port assumptions count = %d, want 1", len(portAssumptions))
		}
		if portAssumptions[0].Statement != "absence is inferred from the searching status" {
			t.Errorf("statement = %q, want %q", portAssumptions[0].Statement, "absence is inferred from the searching status")
		}
	})

	t.Run("unknown", func(t *testing.T) {
		phyCfg := phy.Config{
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerNanowatts: 100_000_000_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityCritical, PD: phy.PDUnknown},
				},
			},
		}
		alloc := phyCfg.Allocate()
		if alloc.Ports["1/1/1"].State != phy.PowerUnknown {
			t.Fatalf("got power state %v, want PowerUnknown", alloc.Ports["1/1/1"].State)
		}

		res := loadExported(t, phyCfg, alloc)
		p, ok := res.Spec.Config.Phy.PoE.Ports["1/1/1"]
		if !ok {
			t.Fatal("port 1/1/1 missing from loaded config")
		}
		if p.PD != phy.PDUnknown {
			t.Errorf("PD = %v, want Unknown", p.PD)
		}
		if p.PDClass != nil {
			t.Errorf("PDClass = %v, want nil", p.PDClass)
		}
		for _, a := range res.Metadata.Assumptions() {
			if a.Statement == "absence is inferred from the searching status" {
				t.Errorf("unexpected searching assumption: %+v", a)
			}
		}
	})

	t.Run("budget denial", func(t *testing.T) {
		phyCfg := phy.Config{
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerNanowatts: 10_000_000_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityCritical, PD: phy.PDAttached, PDClass: phy.Class(4)},
				},
			},
		}
		alloc := phyCfg.Allocate()
		if alloc.Ports["1/1/1"].State != phy.PowerDenied || alloc.Ports["1/1/1"].Denial != phy.ReasonBudget {
			t.Fatalf("got port allocation %+v, want PowerDenied(budget)", alloc.Ports["1/1/1"])
		}

		res := loadExported(t, phyCfg, alloc)
		p, ok := res.Spec.Config.Phy.PoE.Ports["1/1/1"]
		if !ok {
			t.Fatal("port 1/1/1 missing from loaded config")
		}
		if p.PD != phy.PDAbsent {
			t.Errorf("PD = %v, want Absent", p.PD)
		}
		if p.PDClass != nil {
			t.Errorf("PDClass = %v, want nil", p.PDClass)
		}

		portScope := analysis.PortScope("sw1", "1/1/1")
		var portAssumptions []analysis.Assumption
		for _, a := range res.Metadata.Assumptions() {
			if a.Scope.Compare(portScope) == 0 && a.Statement == "absence is inferred from the searching status" {
				portAssumptions = append(portAssumptions, a)
			}
		}
		if len(portAssumptions) != 1 {
			t.Fatalf("port assumptions count = %d, want 1", len(portAssumptions))
		}
		if portAssumptions[0].Statement != "absence is inferred from the searching status" {
			t.Errorf("statement = %q, want %q", portAssumptions[0].Statement, "absence is inferred from the searching status")
		}
	})
}

func TestLoad_PoeLimitInNanowatts(t *testing.T) {
	// The requested limit reaches the loaded port in nanowatts, the unit
	// every phy power value shares.
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	mtu0 := uint32(0)
	grp := uint32(1)
	power := uint64(100_000_000_000)
	name := "1/1/1"

	iface := interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Mtu:         &mtu0,
		Physical: interfacev1.PhysicalInterface_builder{
			Ethernet: phyv1.EthernetFacet_builder{
				Copper: phyv1.CopperFacet_builder{
					PoeSettings: phyv1.PoeSettings_builder{PowerLimitNanowatts: ptr(uint64(15_400_000_000))}.Build(),
					PoeDetail:   phyv1.PoePortDetail_builder{PseGroup: &grp, PsePort: ptr(uint32(1))}.Build(),
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
	budget := phyv1.PseBudget_builder{PseGroup: &grp, PowerNanowatts: &power}.Build()

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{iface}, nil, nil, []*phyv1.PseBudget{budget}, nil, nil, nil, nil, nil, nil, nil, []trace.Layer{phy.LayerNamePoE})
	if err != nil {
		t.Fatalf("netmodel.Load: %v", err)
	}

	p, ok := res.Spec.Config.Phy.PoE.Ports[name]
	if !ok {
		t.Fatalf("port %s missing from loaded config", name)
	}

	if p.Limit == nil {
		t.Fatal("Limit is absent, want 15400000000 nanowatts")
	}

	if *p.Limit != 15_400_000_000 {
		t.Errorf("Limit = %d, want 15400000000 nanowatts", *p.Limit)
	}
}

func TestLoadAutoNegotiationSupportedAbsence(t *testing.T) {
	name := "1/1/1"
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	speeds := []uint64{1_000_000_000}

	iface := interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Mtu:         &mtu,
		Physical: interfacev1.PhysicalInterface_builder{
			Ethernet: phyv1.EthernetFacet_builder{
				Capabilities: phyv1.EthernetCapabilities_builder{
					SupportedSpeedsBps: speeds,
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()

	input := loadInput{ifaces: []*interfacev1.Interface{iface}, want: []trace.Layer{phy.LayerName}}
	input.validate(t)

	res := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})
	eth, ok := res.Spec.Config.Phy.Ethernet[name]
	if !ok {
		t.Fatalf("port %s missing from ethernet config", name)
	}
	if eth.AutoNegotiationSupported != phy.CapabilityUnknown {
		t.Errorf("auto_negotiation_supported = %q, want %q", eth.AutoNegotiationSupported, phy.CapabilityUnknown)
	}

	portScope := analysis.PortScope("sw1", name)
	for _, issue := range res.Metadata.Issues() {
		if issue.Scope.Compare(portScope) == 0 {
			t.Errorf("unexpected issue on port: %+v", issue)
		}
	}
	for _, assumption := range res.Metadata.Assumptions() {
		if assumption.Scope.Compare(portScope) == 0 {
			t.Errorf("unexpected assumption on port: %+v", assumption)
		}
	}
}

func TestLoadPoeStatusMapping(t *testing.T) {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	supported := true
	role := phyv1.PoeRole_POE_ROLE_PSE
	group := uint32(1)
	power := uint64(100_000_000_000)

	delivering := phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER
	searching := phyv1.PoeStatus_POE_STATUS_SEARCHING
	disabled := phyv1.PoeStatus_POE_STATUS_DISABLED
	testingStatus := phyv1.PoeStatus_POE_STATUS_TEST
	fault := phyv1.PoeStatus_POE_STATUS_FAULT
	otherFault := phyv1.PoeStatus_POE_STATUS_OTHER_FAULT
	unspecified := phyv1.PoeStatus_POE_STATUS_UNSPECIFIED
	unknownEnum := phyv1.PoeStatus(99)

	tests := []struct {
		name           string
		status         *phyv1.PoeStatus
		powerClass     *uint32
		wantPD         phy.PDState
		wantClass      *uint8
		wantAssumption bool
	}{
		{
			name:           "delivering power with class",
			status:         &delivering,
			powerClass:     ptr(uint32(3)),
			wantPD:         phy.PDAttached,
			wantClass:      phy.Class(3),
			wantAssumption: false,
		},
		{
			name:           "searching without class",
			status:         &searching,
			wantPD:         phy.PDAbsent,
			wantAssumption: true,
		},
		{
			name:           "disabled",
			status:         &disabled,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "test",
			status:         &testingStatus,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "fault",
			status:         &fault,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "other fault",
			status:         &otherFault,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "unspecified",
			status:         &unspecified,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "unrecognized enum",
			status:         &unknownEnum,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "absent status",
			status:         nil,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			portName := "1/1/1"
			facetBuilder := phyv1.PoeFacet_builder{
				Supported:  &supported,
				Role:       &role,
				PowerClass: tt.powerClass,
			}
			if tt.status != nil {
				facetBuilder.Status = tt.status
			}

			iface := interfacev1.Interface_builder{
				Name:        &portName,
				AdminStatus: &admin,
				OperStatus:  &oper,
				Mtu:         &mtu,
				Physical: interfacev1.PhysicalInterface_builder{
					Ethernet: phyv1.EthernetFacet_builder{
						Copper: phyv1.CopperFacet_builder{
							Poe:       facetBuilder.Build(),
							PoeDetail: phyv1.PoePortDetail_builder{PseGroup: &group, PsePort: ptr(uint32(1))}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			input := loadInput{
				ifaces: []*interfacev1.Interface{iface},
				budgets: []*phyv1.PseBudget{
					phyv1.PseBudget_builder{PseGroup: &group, PowerNanowatts: &power}.Build(),
				},
				want: []trace.Layer{phy.LayerNamePoE},
			}
			input.validate(t)

			res := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})
			portScope := analysis.PortScope("sw1", portName)

			poePort, ok := res.Spec.Config.Phy.PoE.Ports[portName]
			if !ok {
				t.Fatalf("port %s missing from PoE ports", portName)
			}
			if poePort.PD != tt.wantPD {
				t.Errorf("port PD = %q, want %q", poePort.PD, tt.wantPD)
			}
			if tt.wantClass == nil {
				if poePort.PDClass != nil {
					t.Errorf("port PDClass = %v, want nil", *poePort.PDClass)
				}
			} else {
				if poePort.PDClass == nil || *poePort.PDClass != *tt.wantClass {
					t.Errorf("port PDClass = %v, want %v", poePort.PDClass, *tt.wantClass)
				}
			}

			for _, issue := range res.Metadata.Issues() {
				if issue.Scope.Compare(portScope) == 0 && (issue.Code == netmodel.IssuePowerClassWithoutDelivery || issue.Code == netmodel.IssueUnsupportedPowerClass) {
					t.Errorf("unexpected issue on port: %+v", issue)
				}
			}

			var searchingAssumptions []analysis.Assumption
			for _, a := range res.Metadata.AssumptionsFor(portScope) {
				if a.Scope.Compare(portScope) == 0 && a.Statement == "absence is inferred from the searching status" {
					searchingAssumptions = append(searchingAssumptions, a)
				}
			}
			if tt.wantAssumption {
				if len(searchingAssumptions) != 1 {
					t.Fatalf("searching assumptions count = %d, want 1; assumptions: %+v", len(searchingAssumptions), res.Metadata.Assumptions())
				}
				a := searchingAssumptions[0]
				if len(a.Evidence) == 0 {
					t.Error("assumption has no evidence")
				} else if _, found := res.Metadata.Evidence().Lookup(a.Evidence[0]); !found {
					t.Error("assumption evidence not in catalog")
				}
			} else if len(searchingAssumptions) != 0 {
				t.Errorf("unexpected searching assumptions on port: %+v", searchingAssumptions)
			}
		})
	}
}

func TestLoadPoePowerClassWithoutDelivery(t *testing.T) {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	supported := true
	role := phyv1.PoeRole_POE_ROLE_PSE
	group := uint32(1)
	power := uint64(100_000_000_000)

	searching := phyv1.PoeStatus_POE_STATUS_SEARCHING
	disabled := phyv1.PoeStatus_POE_STATUS_DISABLED

	tests := []struct {
		name           string
		status         *phyv1.PoeStatus
		wantPD         phy.PDState
		wantAssumption bool
	}{
		{
			name:           "searching with power class",
			status:         &searching,
			wantPD:         phy.PDAbsent,
			wantAssumption: true,
		},
		{
			name:           "disabled with power class",
			status:         &disabled,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "absent status with power class",
			status:         nil,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			portName := "1/1/1"
			facetBuilder := phyv1.PoeFacet_builder{
				Supported:  &supported,
				Role:       &role,
				PowerClass: ptr(uint32(3)),
			}
			if tt.status != nil {
				facetBuilder.Status = tt.status
			}

			iface := interfacev1.Interface_builder{
				Name:        &portName,
				AdminStatus: &admin,
				OperStatus:  &oper,
				Mtu:         &mtu,
				Physical: interfacev1.PhysicalInterface_builder{
					Ethernet: phyv1.EthernetFacet_builder{
						Copper: phyv1.CopperFacet_builder{
							Poe:       facetBuilder.Build(),
							PoeDetail: phyv1.PoePortDetail_builder{PseGroup: &group, PsePort: ptr(uint32(1))}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			input := loadInput{
				ifaces: []*interfacev1.Interface{iface},
				budgets: []*phyv1.PseBudget{
					phyv1.PseBudget_builder{PseGroup: &group, PowerNanowatts: &power}.Build(),
				},
				want: []trace.Layer{phy.LayerNamePoE},
			}
			input.validate(t)

			res := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})
			portScope := analysis.PortScope("sw1", portName)

			poePort, ok := res.Spec.Config.Phy.PoE.Ports[portName]
			if !ok {
				t.Fatalf("port %s missing from PoE ports", portName)
			}
			if poePort.PD != tt.wantPD {
				t.Errorf("port PD = %q, want %q", poePort.PD, tt.wantPD)
			}
			if poePort.PDClass != nil {
				t.Errorf("port PDClass = %v, want nil (skipped)", *poePort.PDClass)
			}

			issues := res.Metadata.Issues()
			foundIssue := false
			for _, issue := range issues {
				if issue.Code == netmodel.IssuePowerClassWithoutDelivery {
					foundIssue = true
					if issue.Scope.Compare(portScope) != 0 {
						t.Errorf("issue scope = %s, want %s", issue.Scope, portScope)
					}
					if len(issue.Evidence) == 0 {
						t.Error("issue has no evidence")
					} else if _, found := res.Metadata.Evidence().Lookup(issue.Evidence[0]); !found {
						t.Error("issue evidence not in catalog")
					}
				}
			}
			if !foundIssue {
				t.Fatalf("missing IssuePowerClassWithoutDelivery in issues: %+v", issues)
			}

			var searchingAssumptions []analysis.Assumption
			for _, a := range res.Metadata.AssumptionsFor(portScope) {
				if a.Scope.Compare(portScope) == 0 && a.Statement == "absence is inferred from the searching status" {
					searchingAssumptions = append(searchingAssumptions, a)
				}
			}
			if tt.wantAssumption {
				if len(searchingAssumptions) != 1 {
					t.Fatalf("port assumptions count = %d, want 1; assumptions: %+v", len(searchingAssumptions), res.Metadata.Assumptions())
				}
				if searchingAssumptions[0].Statement != "absence is inferred from the searching status" {
					t.Errorf("assumption statement = %q, want %q", searchingAssumptions[0].Statement, "absence is inferred from the searching status")
				}
			} else if len(searchingAssumptions) != 0 {
				t.Errorf("unexpected searching assumptions on port: %+v", searchingAssumptions)
			}
		})
	}
}
