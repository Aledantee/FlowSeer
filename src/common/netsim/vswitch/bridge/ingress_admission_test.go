package bridge_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
)

// untaggedIngressFrame returns a frame with no VLAN tag, the shape
// AdmitsVIDOnIngress's tagged=false rows are tested against.
func untaggedIngressFrame() ethernet.Frame {
	return ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("data"),
	}
}

// taggedIngressFrame returns a frame carrying a single 802.1Q tag for vid,
// the shape AdmitsVIDOnIngress's tagged=true rows are tested against.
func taggedIngressFrame(vid vlan.ID) ethernet.Frame {
	return ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		Tags:      []vlan.Tag{{TPID: 0x8100, VID: vid}},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("data"),
	}
}

// TestAdmitsVIDOnIngressAgreesWithBridgeIngress drives AdmitsVIDOnIngress and
// Bridge.Ingress over the same switchport shapes and frames, and asserts they
// agree on whether the frame is admitted. Agreement, not a hand-written
// expectation, is the property under test: a table of expectations would
// only pin this test's own reading of Ingress, not Ingress itself.
func TestAdmitsVIDOnIngressAgreesWithBridgeIngress(t *testing.T) {
	ports := buildTestPorts(t, 2)

	cases := []struct {
		name        string
		switchports map[string]bridge.Switchport
		table       map[vlan.ID]string
		vid         vlan.ID
		tagged      bool
		frame       ethernet.Frame
	}{
		{
			name: "access port with PVID in Untagged admits the untagged frame",
			switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
			table:  map[vlan.ID]string{10: "ten"},
			vid:    10,
			tagged: false,
			frame:  untaggedIngressFrame(),
		},
		{
			name: "access port with PVID absent from Untagged drops the untagged frame under ingress filtering",
			switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), IngressFiltering: true},
			},
			table:  map[vlan.ID]string{10: "ten"},
			vid:    10,
			tagged: false,
			frame:  untaggedIngressFrame(),
		},
		{
			name: "a port with no PVID drops the untagged frame",
			switchports: map[string]bridge.Switchport{
				"1/1/1": {},
			},
			table:  map[vlan.ID]string{10: "ten"},
			vid:    10,
			tagged: false,
			frame:  untaggedIngressFrame(),
		},
		{
			name: "a trunk with IngressFiltering drops a non-member tagged frame",
			switchports: map[string]bridge.Switchport{
				"1/1/1": {IngressFiltering: true, Tagged: []vlan.ID{20}},
			},
			table:  map[vlan.ID]string{20: "twenty", 30: "thirty"},
			vid:    30,
			tagged: true,
			frame:  taggedIngressFrame(30),
		},
		{
			name: "a trunk without IngressFiltering admits a table VID in neither Tagged nor Untagged",
			switchports: map[string]bridge.Switchport{
				"1/1/1": {Tagged: []vlan.ID{20}},
			},
			table:  map[vlan.ID]string{20: "twenty", 30: "thirty"},
			vid:    30,
			tagged: true,
			frame:  taggedIngressFrame(30),
		},
		{
			name: "a VID outside VLAN.Table is dropped",
			switchports: map[string]bridge.Switchport{
				"1/1/1": {Tagged: []vlan.ID{20}},
			},
			table:  map[vlan.ID]string{20: "twenty"},
			vid:    99,
			tagged: true,
			frame:  taggedIngressFrame(99),
		},
		{
			name: "Admission TaggedOnly drops an untagged frame",
			switchports: map[string]bridge.Switchport{
				"1/1/1": {Admission: bridge.TaggedOnly, PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
			table:  map[vlan.ID]string{10: "ten"},
			vid:    10,
			tagged: false,
			frame:  untaggedIngressFrame(),
		},
		{
			name: "Admission UntaggedAndPriorityTaggedOnly drops a tagged frame",
			switchports: map[string]bridge.Switchport{
				"1/1/1": {Admission: bridge.UntaggedAndPriorityTaggedOnly, Tagged: []vlan.ID{20}},
			},
			table:  map[vlan.ID]string{20: "twenty"},
			vid:    20,
			tagged: true,
			frame:  taggedIngressFrame(20),
		},
		{
			name:        "a port absent from Switchports is judged against the zero Switchport",
			switchports: map[string]bridge.Switchport{},
			table:       map[vlan.ID]string{10: "ten"},
			vid:         10,
			tagged:      false,
			frame:       untaggedIngressFrame(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := bridge.Config{VLAN: &bridge.VLAN{Table: tc.table, Switchports: tc.switchports}}
			br := mustNewBridge(t, cfg, ports)

			_, _, ok := br.Ingress(testTime0, "1/1/1", tc.frame, false, false)
			got := cfg.VLAN.AdmitsVIDOnIngress("1/1/1", tc.vid, tc.tagged)

			if got != ok {
				t.Errorf("AdmitsVIDOnIngress(%q, %d, %v) = %v, Bridge.Ingress ok = %v, want agreement", "1/1/1", tc.vid, tc.tagged, got, ok)
			}
		})
	}
}

// TestAdmitsVIDOnIngressRefusesATunnelPortBridgeIngressAdmits is the one
// documented disagreement between AdmitsVIDOnIngress and Bridge.Ingress: an
// untagged frame arriving on a tunnel port is classified by Bridge.Ingress
// into the tunnel's service VLAN and admitted, because the VLAN it names
// belongs to the customer's own spanning tree rather than to this bridge.
// AdmitsVIDOnIngress answers false for every tunnel port regardless.
func TestAdmitsVIDOnIngressRefusesATunnelPortBridgeIngressAdmits(t *testing.T) {
	ports := buildTestPorts(t, 2)
	cfg := bridge.Config{VLAN: &bridge.VLAN{
		Table: map[vlan.ID]string{50: "service"},
		Switchports: map[string]bridge.Switchport{
			"1/1/1": {Tunnel: &bridge.Tunnel{VID: 50}},
		},
	}}
	br := mustNewBridge(t, cfg, ports)

	_, _, ok := br.Ingress(testTime0, "1/1/1", untaggedIngressFrame(), false, false)
	if !ok {
		t.Fatalf("Bridge.Ingress ok = false, want true: this test needs Bridge.Ingress to admit the frame so the disagreement is real")
	}

	if got := cfg.VLAN.AdmitsVIDOnIngress("1/1/1", 50, false); got {
		t.Errorf("AdmitsVIDOnIngress(%q, 50, false) = true on a tunnel port, want false", "1/1/1")
	}
}
