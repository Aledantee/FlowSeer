package netmodel_test

import (
	"slices"
	"testing"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestNetmodel_InvalidFdbEntrySkipped(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	name := "1/1/1"
	iface := interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
	}.Build()

	vid := uint32(10)
	ifname := "1/1/1"
	statusInvalid := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_INVALID
	statusActive := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	kindDynamic := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC
	macBytes := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	fdb1 := switchingv1.FdbEntry_builder{
		NetworkInstance: ptr("default"),
		VlanId:          &vid,
		InterfaceName:   &ifname,
		Status:          &statusInvalid,
		Kind:            &kindDynamic,
		Mac:             addrv1.Eui48Address_builder{Octets: macBytes}.Build(),
	}.Build()

	fdb2 := switchingv1.FdbEntry_builder{
		NetworkInstance: ptr("default"),
		VlanId:          &vid,
		InterfaceName:   &ifname,
		Status:          &statusActive,
		Kind:            &kindDynamic,
		Mac:             addrv1.Eui48Address_builder{Octets: macBytes}.Build(),
	}.Build()

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{iface}, nil, []*switchingv1.FdbEntry{fdb1, fdb2}, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	seeds := res.Spec.Seeds
	report := res.Report

	if len(seeds) != 0 {
		t.Errorf("seeds = %+v, want both unusable rows omitted", seeds)
	}

	foundSkipped := false
	for _, s := range report.Skipped {
		if s.Port == "1/1/1" && s.What == "fdb_entry" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("got Skipped %+v, want invalid FDB entry", report.Skipped)
	}
	if res.Readiness() == analysis.Complete {
		t.Error("readiness = Complete, want unusable active row reported")
	}
	if _, err := vswitch.NewWithSpec(res.Spec); err != nil {
		t.Errorf("NewWithSpec: %v", err)
	}
}

func TestLoadFDBRequiresAuthoritativeKindAndStatus(t *testing.T) {
	vid := uint32(10)
	portName := "1/1/1"
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	dynamic := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC
	unspecifiedKind := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_UNSPECIFIED
	unspecifiedStatus := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_UNSPECIFIED
	unknownKind := switchingv1.FdbEntryKind(99)
	unknownStatus := switchingv1.FdbEntryStatus(99)
	macs := [][]byte{
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x03},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x04},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x05},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x06},
	}
	fdb := []*switchingv1.FdbEntry{
		switchingv1.FdbEntry_builder{
			NetworkInstance: ptr("default"),
			VlanId:          &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[0]}.Build(), Status: &active,
		}.Build(),
		switchingv1.FdbEntry_builder{
			NetworkInstance: ptr("default"),
			VlanId:          &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[1]}.Build(), Kind: &unknownKind, Status: &active,
		}.Build(),
		switchingv1.FdbEntry_builder{
			NetworkInstance: ptr("default"),
			VlanId:          &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[2]}.Build(), Kind: &dynamic,
		}.Build(),
		switchingv1.FdbEntry_builder{
			NetworkInstance: ptr("default"),
			VlanId:          &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[3]}.Build(), Kind: &dynamic, Status: &unknownStatus,
		}.Build(),
		switchingv1.FdbEntry_builder{
			NetworkInstance: ptr("default"),
			VlanId:          &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[4]}.Build(), Kind: &unspecifiedKind, Status: &active,
		}.Build(),
		switchingv1.FdbEntry_builder{
			NetworkInstance: ptr("default"),
			VlanId:          &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[5]}.Build(), Kind: &dynamic, Status: &unspecifiedStatus,
		}.Build(),
	}

	src := netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "fdb-trust"}
	result := (loadInput{ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)}, fdb: fdb}).load(t, src)

	if len(result.Spec.Seeds) != 0 {
		t.Errorf("seeds = %+v, want none", result.Spec.Seeds)
	}
	if result.Readiness() == analysis.Complete {
		t.Fatal("readiness = Complete, want non-Complete")
	}
	if len(result.Report.Skipped) != len(fdb) {
		t.Errorf("skipped rows = %d, want %d: %+v", len(result.Report.Skipped), len(fdb), result.Report.Skipped)
	}
	assertEvidenceSource(t, result, src)
}

func TestLoadKeepsExplicitActiveFDBKindsAndDeduplicatesEqualRows(t *testing.T) {
	vid := uint32(10)
	portName := "1/1/1"
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE

	for _, tt := range []struct {
		name     string
		kind     switchingv1.FdbEntryKind
		isStatic bool
	}{
		{name: "dynamic", kind: switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC},
		{name: "static", kind: switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC, isStatic: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			row := switchingv1.FdbEntry_builder{
				NetworkInstance: ptr("default"),
				VlanId:          &vid, InterfaceName: &portName,
				Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(), Kind: &tt.kind, Status: &active,
			}.Build()
			input := loadInput{
				ifaces: []*interfacev1.Interface{switchedPhysicalInterface(portName, vid)},
				fdb:    []*switchingv1.FdbEntry{row, row},
			}
			input.validate(t)
			result := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})

			isStatic := result.Spec.Seeds[0].Lifetime == bridge.Static
			if len(result.Spec.Seeds) != 1 || isStatic != tt.isStatic {
				t.Errorf("seeds = %+v, want one seed with static=%t", result.Spec.Seeds, tt.isStatic)
			}
			if len(result.Report.Conflicts) != 0 || result.Readiness() != analysis.Complete {
				t.Errorf("conflicts = %+v, readiness = %v, want no conflict and Complete", result.Report.Conflicts, result.Readiness())
			}
		})
	}
}

func TestLoadOmitsFDBRowsTheCompletedSwitchCannotConstruct(t *testing.T) {
	portName := "1/1/1"
	vid10 := uint32(10)
	vid20 := uint32(20)
	vlan10 := switchingv1.Vlan_builder{NetworkInstance: ptr("default"), Id: &vid10, Name: ptr("ten")}.Build()

	tests := []struct {
		name      string
		input     loadInput
		wantSeeds int
	}{
		{
			name: "relay absent",
			input: loadInput{
				ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
				want:   []trace.Layer{phy.LayerName},
			},
		},
		{
			name: "VLAN awareness absent",
			input: loadInput{
				ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
				want:   []trace.Layer{bridge.LayerName},
			},
		},
		{
			name: "VLAN absent from table",
			input: loadInput{
				ifaces: []*interfacev1.Interface{switchedPhysicalInterface(portName, vid20)},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
			},
		},
		{
			name: "routed port",
			input: loadInput{
				ifaces: []*interfacev1.Interface{routedPhysicalInterface(portName)},
				vlans:  []*switchingv1.Vlan{vlan10},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
			},
		},
		{
			name: "switchport does not admit VLAN",
			input: loadInput{
				ifaces: []*interfacev1.Interface{switchedPhysicalInterface(portName, vid20)},
				vlans:  []*switchingv1.Vlan{vlan10},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
			},
		},
		{
			name: "admitted switchport",
			input: loadInput{
				ifaces: []*interfacev1.Interface{switchedPhysicalInterface(portName, vid10)},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
			},
			wantSeeds: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.input.validate(t)
			result := test.input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot"})

			if got := len(result.Spec.Seeds); got != test.wantSeeds {
				t.Errorf("seed count = %d, want %d: %+v", got, test.wantSeeds, result.Spec.Seeds)
			}
			normalized, err := result.Spec.Normalize()
			if err != nil {
				t.Fatalf("Spec.Normalize: %v", err)
			}
			if !result.Spec.Equal(normalized) {
				t.Errorf("Load returned a non-normalized spec:\n got: %+v\nwant: %+v", result.Spec, normalized)
			}
			if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
				t.Fatalf("NewWithSpec: %v", err)
			}

			if test.wantSeeds > 0 {
				return
			}
			if result.Readiness() == analysis.Complete {
				t.Fatal("readiness = Complete, want scoped FDB omission")
			}
			if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
				return skip.Port == portName && skip.What == "fdb_entry" && len(skip.Evidence) > 0
			}) {
				t.Errorf("skipped = %+v, want evidenced fdb_entry omission on %q", result.Report.Skipped, portName)
			}
			issues := result.Metadata.IssuesFor(analysis.PortScope("sw1", portName))
			if len(issues) == 0 || len(issues[0].Evidence) == 0 {
				t.Errorf("issues = %+v, want evidenced port-scoped issue", issues)
			}
		})
	}
}
