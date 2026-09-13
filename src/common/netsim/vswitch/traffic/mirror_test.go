package traffic_test

import (
	"bytes"
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func TestCopiesToPortSelectsIngressAndTruncates(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte{0x5a}, 200)
	received := ethernet.Frame{
		Dst:       netaddr.MAC{0x02, 0, 0, 0, 0, 2},
		Src:       netaddr.MAC{0x02, 0, 0, 0, 0, 1},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   payload,
	}
	cfg := traffic.Config{Mirrors: []traffic.Mirror{{
		Name:           "m1",
		SelectSrcPorts: []string{"1/1/1"},
		OutputPort:     "1/1/4",
		SnapLen:        64,
	}}}

	copies := traffic.Copies(cfg, nil, "1/1/1", vlan.ID(10), received, nil)
	if len(copies) != 1 {
		t.Fatalf("Copies returned %d copies, want 1", len(copies))
	}
	if copies[0].Mirror != "m1" || copies[0].Port != "1/1/4" {
		t.Errorf("copy destination = %q/%q, want %q/%q", copies[0].Mirror, copies[0].Port, "m1", "1/1/4")
	}
	if copies[0].VLAN != 0 {
		t.Errorf("direct-output copy VLAN = %d, want zero", copies[0].VLAN)
	}
	encoded, err := copies[0].Frame.Encode()
	if err != nil {
		t.Fatalf("encode copied frame: %v", err)
	}
	if len(encoded) != 64 {
		t.Errorf("encoded copy length = %d, want 64", len(encoded))
	}
	if !bytes.Equal(copies[0].Frame.Payload, payload[:50]) {
		t.Errorf("copied payload = %x, want first 50 octets", copies[0].Frame.Payload)
	}
	if got := traffic.Copies(cfg, nil, "1/1/3", vlan.ID(10), received, nil); len(got) != 0 {
		t.Errorf("Copies returned %d copies from an unselected ingress, want 0", len(got))
	}
}

func TestCopiesTruncatePayloadWithoutShorteningHeaders(t *testing.T) {
	t.Parallel()

	received := ethernet.Frame{
		Dst:       netaddr.MAC{0x02, 0, 0, 0, 0, 2},
		Src:       netaddr.MAC{0x02, 0, 0, 0, 0, 1},
		Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte{1, 2, 3, 4},
	}
	cfg := traffic.Config{Mirrors: []traffic.Mirror{{Name: "m1", SelectAll: true, OutputPort: "1/1/4", SnapLen: 20}}}

	copied := traffic.Copies(cfg, nil, "1/1/1", 10, received, nil)[0]
	encoded, err := copied.Frame.Encode()
	if err != nil {
		t.Fatalf("encode tagged copy: %v", err)
	}
	if len(encoded) != 20 || !bytes.Equal(copied.Frame.Payload, []byte{1, 2}) {
		t.Errorf("tagged copy length/payload = %d/%v, want 20/[1 2]", len(encoded), copied.Frame.Payload)
	}

	received.Tags = []vlan.Tag{
		{TPID: uint16(ethernet.EtherTypeProviderBridging), VID: 10},
		{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20},
	}
	cfg.Mirrors[0].SnapLen = 18
	copied = traffic.Copies(cfg, nil, "1/1/1", 10, received, nil)[0]
	encoded, err = copied.Frame.Encode()
	if err != nil {
		t.Fatalf("encode stacked-tag copy: %v", err)
	}
	if len(encoded) != 22 || len(copied.Frame.Payload) != 0 {
		t.Errorf("stacked-tag copy length/payload = %d/%v, want 22/empty", len(encoded), copied.Frame.Payload)
	}
}

func TestCopiesSelectsSuccessfulEgressAndVLAN(t *testing.T) {
	t.Parallel()

	received := ethernet.Frame{
		Dst:       netaddr.MAC{0x02, 0, 0, 0, 0, 2},
		Src:       netaddr.MAC{0x02, 0, 0, 0, 0, 1},
		EtherType: ethernet.EtherTypeIPv4,
	}
	cfg := traffic.Config{Mirrors: []traffic.Mirror{{
		Name:           "m1",
		SelectDstPorts: []string{"1/1/24"},
		SelectVLANs:    []vlan.ID{20},
		OutputPort:     "1/1/4",
	}}}

	forwarded := []bridge.Egress{{Port: "1/1/24"}}
	if got := traffic.Copies(cfg, nil, "1/1/1", vlan.ID(20), received, forwarded); len(got) != 1 {
		t.Errorf("Copies returned %d copies for selected egress, want 1", len(got))
	}
	dropped := []bridge.Egress{{Port: "1/1/24", Dropped: trace.Reason("filtered")}}
	if got := traffic.Copies(cfg, nil, "1/1/1", vlan.ID(20), received, dropped); len(got) != 0 {
		t.Errorf("Copies returned %d copies for dropped egress, want 0", len(got))
	}
	if got := traffic.Copies(cfg, nil, "1/1/1", vlan.ID(10), received, forwarded); len(got) != 0 {
		t.Errorf("Copies returned %d copies for unselected VLAN, want 0", len(got))
	}
}

func TestCopiesToVLANUsesEachSwitchportTagForm(t *testing.T) {
	t.Parallel()

	outputVLAN := vlan.ID(99)
	received := ethernet.Frame{
		Dst:       netaddr.MAC{0x02, 0, 0, 0, 0, 2},
		Src:       netaddr.MAC{0x02, 0, 0, 0, 0, 1},
		Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), PCP: 5, DEI: true, VID: 10}},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte{1, 2, 3},
	}
	cfg := traffic.Config{Mirrors: []traffic.Mirror{{
		Name:           "m2",
		SelectSrcPorts: []string{"1/1/1"},
		OutputVLAN:     &outputVLAN,
	}}}
	vlans := &bridge.VLAN{Switchports: map[string]bridge.Switchport{
		"1/1/1":  {Untagged: []vlan.ID{99}},
		"1/1/24": {Tagged: []vlan.ID{99}},
		"1/1/4":  {Untagged: []vlan.ID{99}},
	}}

	copies := traffic.Copies(cfg, vlans, "1/1/1", vlan.ID(10), received, nil)
	if len(copies) != 2 {
		t.Fatalf("Copies returned %d copies, want 2", len(copies))
	}
	if got := []string{copies[0].Port, copies[1].Port}; !slices.Equal(got, []string{"1/1/24", "1/1/4"}) {
		t.Fatalf("copy ports = %v, want [1/1/24 1/1/4]", got)
	}
	for _, copy := range copies {
		if copy.VLAN != outputVLAN {
			t.Errorf("copy on %s VLAN = %d, want %d", copy.Port, copy.VLAN, outputVLAN)
		}
	}
	tagged := copies[0].Frame.Tags
	if len(tagged) != 1 {
		t.Fatalf("tagged copy has %d tags, want 1", len(tagged))
	}
	wantTag := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), PCP: 5, DEI: true, VID: 99}
	if tagged[0] != wantTag {
		t.Errorf("tagged copy outer tag = %+v, want %+v", tagged[0], wantTag)
	}
	if len(copies[1].Frame.Tags) != 0 {
		t.Errorf("untagged copy tags = %+v, want none", copies[1].Frame.Tags)
	}

	reserved := received
	reserved.Dst = netaddr.MAC{0x01, 0x80, 0xc2, 0, 0, 0x0e}
	if got := traffic.Copies(cfg, vlans, "1/1/1", vlan.ID(10), reserved, nil); len(got) != 0 {
		t.Errorf("Copies returned %d VLAN copies for reserved destination, want 0", len(got))
	}
}

func TestCopiesToVLANUsesTunnelAndKeepsInnerTags(t *testing.T) {
	t.Parallel()

	outputVLAN := vlan.ID(99)
	inner := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), PCP: 2, VID: 20}
	received := ethernet.Frame{
		Dst:       netaddr.MAC{0x02, 0, 0, 0, 0, 2},
		Src:       netaddr.MAC{0x02, 0, 0, 0, 0, 1},
		Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeProviderBridging), PCP: 7, VID: 10}, inner},
		EtherType: ethernet.EtherTypeIPv4,
	}
	cfg := traffic.Config{Mirrors: []traffic.Mirror{{
		Name: "m2", SelectAll: true, OutputVLAN: &outputVLAN,
	}}}
	vlans := &bridge.VLAN{Switchports: map[string]bridge.Switchport{
		"1/1/24": {Tunnel: &bridge.Tunnel{VID: 99}},
	}}

	copies := traffic.Copies(cfg, vlans, "1/1/1", vlan.ID(10), received, nil)
	if len(copies) != 1 {
		t.Fatalf("Copies returned %d copies, want 1", len(copies))
	}
	if copies[0].VLAN != outputVLAN {
		t.Errorf("tunnel copy VLAN = %d, want %d", copies[0].VLAN, outputVLAN)
	}
	if !slices.Equal(copies[0].Frame.Tags, []vlan.Tag{inner}) {
		t.Errorf("tunnel copy tags = %+v, want inner tag %+v", copies[0].Frame.Tags, inner)
	}
}

func TestCopiesPreservesMirrorOrder(t *testing.T) {
	t.Parallel()

	received := ethernet.Frame{
		Dst:       netaddr.MAC{0x02, 0, 0, 0, 0, 2},
		Src:       netaddr.MAC{0x02, 0, 0, 0, 0, 1},
		EtherType: ethernet.EtherTypeIPv4,
	}
	cfg := traffic.Config{Mirrors: []traffic.Mirror{
		{Name: "first", SelectAll: true, OutputPort: "1/1/4"},
		{Name: "second", SelectAll: true, OutputPort: "1/1/5"},
	}}

	copies := traffic.Copies(cfg, nil, "1/1/1", vlan.ID(10), received, nil)
	if len(copies) != 2 {
		t.Fatalf("Copies returned %d copies, want 2", len(copies))
	}
	if got := []string{copies[0].Mirror, copies[1].Mirror}; !slices.Equal(got, []string{"first", "second"}) {
		t.Errorf("mirror order = %v, want [first second]", got)
	}
}
