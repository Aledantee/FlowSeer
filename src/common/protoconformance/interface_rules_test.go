package protoconformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
)

func TestInterfaceRules(t *testing.T) {
	tests := []validationCase{
		{
			name:      "kind arm absent",
			message:   interfacev1.Interface_builder{Name: proto.String("GigabitEthernet1/0/1")}.Build(),
			wantValid: false,
		},
		{
			name: "switched physical port carries no IP facet",
			message: interfacev1.Interface_builder{
				Name: proto.String("GigabitEthernet1/0/1"),
				Physical: interfacev1.PhysicalInterface_builder{
					Switchport: switchingv1.SwitchportFacet_builder{
						Pvid: proto.Uint32(20),
					}.Build(),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "switched virtual interface routes its VLAN",
			message: interfacev1.Interface_builder{
				Name: proto.String("Vlan20"),
				Vlan: interfacev1.VlanInterface_builder{
					VlanId: proto.Uint32(20),
				}.Build(),
				Ip: ipv1.IpFacet_builder{
					Ipv4: ipv1.Ipv4Facet_builder{Enabled: proto.Bool(true)}.Build(),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "name absent",
			message: interfacev1.Interface_builder{
				Loopback: interfacev1.LoopbackInterface_builder{}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "name empty",
			message: interfacev1.Interface_builder{
				Name:     proto.String(""),
				Loopback: interfacev1.LoopbackInterface_builder{}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name:      "routed VLAN identifier zero",
			message:   interfacev1.VlanInterface_builder{VlanId: proto.Uint32(0)}.Build(),
			wantValid: false,
		},
		{
			name:      "routed VLAN identifier reserved",
			message:   interfacev1.VlanInterface_builder{VlanId: proto.Uint32(4095)}.Build(),
			wantValid: false,
		},
		{
			name:      "lowest routed VLAN identifier",
			message:   interfacev1.VlanInterface_builder{VlanId: proto.Uint32(1)}.Build(),
			wantValid: true,
		},
		{
			name:      "highest routed VLAN identifier",
			message:   interfacev1.VlanInterface_builder{VlanId: proto.Uint32(4094)}.Build(),
			wantValid: true,
		},
		{
			name:      "routed VLAN identifier absent",
			message:   interfacev1.VlanInterface_builder{}.Build(),
			wantValid: false,
		},
		{
			name: "subinterface parent empty",
			message: interfacev1.Subinterface_builder{
				Parent: proto.String(""),
			}.Build(),
			wantValid: false,
		},
		{
			name: "unknown nonzero oper status remains valid",
			message: interfacev1.Interface_builder{
				Name:       proto.String("GigabitEthernet1/0/1"),
				OperStatus: interfacev1.OperStatus(99).Enum(),
				Physical:   interfacev1.PhysicalInterface_builder{}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "unknown nonzero admin status remains valid",
			message: interfacev1.Interface_builder{
				Name:        proto.String("GigabitEthernet1/0/1"),
				AdminStatus: interfacev1.AdminStatus(99).Enum(),
				Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "other interface preserves an unclassified ifType",
			message: interfacev1.Interface_builder{
				Name:  proto.String("wifi0"),
				Other: interfacev1.OtherInterface_builder{IfType: proto.Uint32(71)}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name:      "other interface without an ifType",
			message:   interfacev1.OtherInterface_builder{}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestInterfaceExplicitZeroMtuKeepsPresence(t *testing.T) {
	iface := interfacev1.Interface_builder{
		Name:     proto.String("Loopback0"),
		Mtu:      proto.Uint32(0),
		Loopback: interfacev1.LoopbackInterface_builder{}.Build(),
	}.Build()

	wire, err := proto.Marshal(iface)
	if err != nil {
		t.Fatalf("marshaling interface: %v", err)
	}

	decoded := &interfacev1.Interface{}
	if err := proto.Unmarshal(wire, decoded); err != nil {
		t.Fatalf("unmarshaling interface: %v", err)
	}

	if !decoded.HasMtu() {
		t.Error("got HasMtu()=false for an explicitly set zero MTU, want true")
	}
	if got := decoded.GetMtu(); got != 0 {
		t.Errorf("got MTU %d, want 0", got)
	}
}

func TestSubinterfaceKeepsTagOrder(t *testing.T) {
	outer := switchingv1.VlanTag_builder{
		Tpid:   packetv1.EtherType_ETHER_TYPE_PROVIDER_BRIDGING.Enum(),
		VlanId: proto.Uint32(100),
		Pcp:    proto.Uint32(0),
		Dei:    proto.Bool(false),
	}.Build()
	inner := switchingv1.VlanTag_builder{
		Tpid:   packetv1.EtherType_ETHER_TYPE_DOT1Q.Enum(),
		VlanId: proto.Uint32(200),
		Pcp:    proto.Uint32(0),
		Dei:    proto.Bool(false),
	}.Build()

	iface := interfacev1.Interface_builder{
		Name: proto.String("GigabitEthernet1/0/1.100"),
		Sub: interfacev1.Subinterface_builder{
			Parent: proto.String("GigabitEthernet1/0/1"),
			Encapsulation: switchingv1.VlanTagStack_builder{
				Tags: []*switchingv1.VlanTag{outer, inner},
			}.Build(),
		}.Build(),
	}.Build()

	runValidationCases(t, []validationCase{
		{name: "QinQ subinterface", message: iface, wantValid: true},
	})

	wire, err := proto.Marshal(iface)
	if err != nil {
		t.Fatalf("marshaling interface: %v", err)
	}

	decoded := &interfacev1.Interface{}
	if err := proto.Unmarshal(wire, decoded); err != nil {
		t.Fatalf("unmarshaling interface: %v", err)
	}

	tags := decoded.GetSub().GetEncapsulation().GetTags()
	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2", len(tags))
	}
	if got := tags[0].GetVlanId(); got != 100 {
		t.Errorf("got outermost VID %d, want 100", got)
	}
	if got := tags[0].GetTpid(); got != packetv1.EtherType_ETHER_TYPE_PROVIDER_BRIDGING {
		t.Errorf("got outermost TPID %v, want provider bridging", got)
	}
	if got := tags[1].GetVlanId(); got != 200 {
		t.Errorf("got innermost VID %d, want 200", got)
	}
}
