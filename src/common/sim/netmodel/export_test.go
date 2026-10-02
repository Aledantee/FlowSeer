package netmodel_test

import (
	"testing"

	"buf.build/go/protovalidate"

	instancev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/instance/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
)

// TestExportNamesTheDefaultInstance holds the single-bridge export: the switch
// reports one DEFAULT instance named "default", and every VLAN and FDB row it
// exports names that instance.
func TestExportNamesTheDefaultInstance(t *testing.T) {
	accessPort := func(name string, pvid uint32) *interfacev1.Interface {
		return interfacev1.Interface_builder{
			Name:        ptr(name),
			AdminStatus: interfacev1.AdminStatus_ADMIN_STATUS_UP.Enum(),
			OperStatus:  interfacev1.OperStatus_OPER_STATUS_UP.Enum(),
			Physical: interfacev1.PhysicalInterface_builder{
				Switchport: switchingv1.SwitchportFacet_builder{
					Pvid:             ptr(pvid),
					UntaggedVlanIds:  []uint32{pvid},
					FrameAdmission:   switchingv1.FrameAdmission_FRAME_ADMISSION_ALL.Enum(),
					IngressFiltering: ptr(false),
				}.Build(),
			}.Build(),
		}.Build()
	}
	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{Id: ptr(uint32(10)), Name: ptr("ten"), NetworkInstance: ptr("default")}.Build(),
		switchingv1.Vlan_builder{Id: ptr(uint32(20)), NetworkInstance: ptr("default")}.Build(),
	}
	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"},
		[]*interfacev1.Interface{accessPort("1/1/1", 10), accessPort("1/1/2", 10), accessPort("1/1/3", 20)},
		vlans, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("netmodel.Load: %v", err)
	}
	sw, err := vswitch.New(res.Spec.Config)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}
	sw.Forward(testTime, "1/1/1", ethernet.Frame{
		Dst:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		Src:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Payload: []byte("untagged packet"),
	})

	instances := netmodel.NetworkInstances(sw)
	if len(instances) != 1 {
		t.Fatalf("got %d network instances, want 1", len(instances))
	}
	if got := instances[0]; got.GetName() != "default" ||
		got.GetKind() != instancev1.NetworkInstanceKind_NETWORK_INSTANCE_KIND_DEFAULT {
		t.Errorf("got instance %q of kind %v, want \"default\" of kind DEFAULT", got.GetName(), got.GetKind())
	}
	if err := protovalidate.Validate(instances[0]); err != nil {
		t.Errorf("network instance is invalid: %v", err)
	}

	exportedVlans := netmodel.Vlans(sw)
	if len(exportedVlans) != 2 {
		t.Fatalf("got %d VLAN rows, want 2", len(exportedVlans))
	}
	if exportedVlans[0].GetId() != 10 || exportedVlans[0].GetName() != "ten" ||
		exportedVlans[1].GetId() != 20 || exportedVlans[1].HasName() {
		t.Errorf("got VLAN rows %v, want 10 named ten then 20 without a name", exportedVlans)
	}
	for _, v := range exportedVlans {
		if v.GetNetworkInstance() != "default" {
			t.Errorf("VLAN %d names instance %q, want \"default\"", v.GetId(), v.GetNetworkInstance())
		}
		if err := protovalidate.Validate(v); err != nil {
			t.Errorf("VLAN %d is invalid: %v", v.GetId(), err)
		}
	}

	fdb, err := netmodel.FdbEntries(sw.Entries())
	if err != nil {
		t.Fatalf("FdbEntries: %v", err)
	}
	if len(fdb) == 0 {
		t.Fatal("got no FDB rows, want the learned source address")
	}
	for _, e := range fdb {
		if e.GetNetworkInstance() != "default" {
			t.Errorf("FDB row for VLAN %d names instance %q, want \"default\"", e.GetVlanId(), e.GetNetworkInstance())
		}
		if err := protovalidate.Validate(e); err != nil {
			t.Errorf("FDB row is invalid: %v", err)
		}
	}
}

func TestExportWithoutSwitch(t *testing.T) {
	if got := netmodel.NetworkInstances(nil); got != nil {
		t.Errorf("NetworkInstances(nil) = %v, want nil", got)
	}
	if got := netmodel.Vlans(nil); got != nil {
		t.Errorf("Vlans(nil) = %v, want nil", got)
	}
}
