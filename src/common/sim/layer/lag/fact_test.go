package lag_test

import (
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestSelectionFactCapturesConsumedHashTuple(t *testing.T) {
	t.Parallel()

	srcMAC := mustMAC(t, "02:00:00:00:00:01")
	dstMAC := mustMAC(t, "02:00:00:00:00:02")
	tests := []struct {
		name       string
		mode       lag.Mode
		frame      ethernet.Frame
		vid        vlan.ID
		wantFields []string
	}{
		{
			name:  "balance slb",
			mode:  lag.BalanceSLB,
			frame: ethernet.Frame{Src: srcMAC, Dst: dstMAC, EtherType: ethernet.EtherTypeIPv4},
			vid:   99,
			wantFields: []string{
				`hash_basis=270544960`,
				`src="02:00:00:00:00:01"`,
				`vid=99`,
			},
		},
		{
			name:  "balance tcp decoded UDP",
			mode:  lag.BalanceTCP,
			frame: makeUDPFrame(t, srcMAC, dstMAC, "10.0.0.1", "10.0.0.2", 40000, 5000, false),
			wantFields: []string{
				`hash_basis=270544960`,
				`src="02:00:00:00:00:01"`,
				`dst="02:00:00:00:00:02"`,
				`ether_type=2048`,
				`ip_decoded=true`,
				`ip_src="10.0.0.1"`,
				`ip_dst="10.0.0.2"`,
				`ip_protocol=17`,
				`transport_4="9c401388"`,
			},
		},
		{
			name:  "balance tcp rejected IP",
			mode:  lag.BalanceTCP,
			frame: makeUDPFrame(t, srcMAC, dstMAC, "10.0.0.1", "10.0.0.2", 40000, 5000, true),
			wantFields: []string{
				`hash_basis=270544960`,
				`ether_type=2048`,
				`ip_decoded=false`,
				`ip_src=""`,
				`ip_dst=""`,
				`ip_protocol=0`,
				`transport_4=""`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			layer := selectionFactLayer(t, tc.mode, 0x10203040)
			now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
			sel := layer.Select(now, "lag1", tc.frame, tc.vid)
			if !sel.OK {
				t.Fatal("Select() selected = false, want true")
			}
			canonical := layer.SelectionFact("lag1", tc.frame, tc.vid, sel).Canonical()
			for _, field := range tc.wantFields {
				if !strings.Contains(canonical, ";"+field+";") {
					t.Errorf("SelectionFact() = %q, want field %q", canonical, field)
				}
			}
		})
	}
}

func TestSelectionFactDistinguishesConsumedInputsSelectingSameMember(t *testing.T) {
	t.Parallel()

	srcMAC := mustMAC(t, "02:00:00:00:00:01")
	dstMAC := mustMAC(t, "02:00:00:00:00:02")

	t.Run("balance slb source MAC", func(t *testing.T) {
		layer := selectionFactLayer(t, lag.BalanceSLB, 17)
		factsByMember := make(map[string]string)
		for last := byte(1); last <= 3; last++ {
			frame := ethernet.Frame{
				Src:       netaddr.MAC{0x02, 0, 0, 0, 0, last},
				Dst:       dstMAC,
				EtherType: ethernet.EtherTypeIPv4,
			}
			assertCollisionFactDiffers(t, layer, frame, 10, factsByMember)
		}
	})

	t.Run("balance tcp transport bytes", func(t *testing.T) {
		layer := selectionFactLayer(t, lag.BalanceTCP, 17)
		factsByMember := make(map[string]string)
		for srcPort := uint16(40000); srcPort <= 40002; srcPort++ {
			frame := makeUDPFrame(t, srcMAC, dstMAC, "10.0.0.1", "10.0.0.2", srcPort, 5000, false)
			assertCollisionFactDiffers(t, layer, frame, 0, factsByMember)
		}
	})
}

func TestSelectionFactDistinguishesL3AndTransportInputsOnSameMember(t *testing.T) {
	t.Parallel()

	srcMAC := mustMAC(t, "02:00:00:00:00:01")
	dstMAC := mustMAC(t, "02:00:00:00:00:02")
	layer := selectionFactLayer(t, lag.BalanceTCP, 17)
	layer.LinkChange(time.Date(2026, 9, 13, 12, 0, 1, 0, time.UTC), "1/1/2", false)

	frames := []ethernet.Frame{
		makeUDPFrame(t, srcMAC, dstMAC, "10.0.0.1", "10.0.0.2", 40000, 5000, false),
		makeUDPFrame(t, srcMAC, dstMAC, "10.0.0.3", "10.0.0.2", 40000, 5000, false),
		makeUDPFrame(t, srcMAC, dstMAC, "10.0.0.1", "10.0.0.4", 40000, 5000, false),
		makeUDPFrame(t, srcMAC, dstMAC, "10.0.0.1", "10.0.0.2", 40000, 5001, false),
	}
	now := time.Date(2026, 9, 13, 12, 0, 2, 0, time.UTC)
	facts := make(map[string]struct{}, len(frames))
	for _, frame := range frames {
		sel := layer.Select(now, "lag1", frame, 0)
		if !sel.OK || sel.Member != "1/1/1" {
			t.Fatalf("Select() = (%q, %t), want (1/1/1, true)", sel.Member, sel.OK)
		}
		facts[layer.SelectionFact("lag1", frame, 0, sel).Canonical()] = struct{}{}
	}
	if len(facts) != len(frames) {
		t.Errorf("L3 and transport variants produced %d facts, want %d", len(facts), len(frames))
	}
}

// selectionFactLayer builds a two-member lag1 in the given mode, ready for
// Select. BalanceTCP requires negotiated LACP, so for that mode it peers the
// layer with a second one and converges LACP before returning; the peer is
// discarded since these tests only exercise the fact snapshot, not LACP
// evidence.
func selectionFactLayer(t *testing.T, mode lag.Mode, hashBasis uint32) *lag.Layer {
	t.Helper()

	lagCfg := lag.LAG{Mode: mode, HashBasis: hashBasis}
	if mode == lag.BalanceTCP {
		lagCfg.LACP = lag.LACPConfig{Mode: lag.Active, Fast: true}
	}

	layer := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": lagCfg}}, lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:10"))
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	if mode == lag.BalanceTCP {
		peer := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": lagCfg}}, lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:11"))
		convergeLACP(t, layer, peer, now)

		return layer
	}

	layer.LinkChange(now, "1/1/1", true)
	layer.LinkChange(now, "1/1/2", true)

	return layer
}

func assertCollisionFactDiffers(t *testing.T, layer *lag.Layer, frame ethernet.Frame, vid vlan.ID, factsByMember map[string]string) {
	t.Helper()

	now := time.Date(2026, 9, 13, 12, 0, 1, 0, time.UTC)
	sel := layer.Select(now, "lag1", frame, vid)
	if !sel.OK {
		t.Fatal("Select() selected = false, want true")
	}
	canonical := layer.SelectionFact("lag1", frame, vid, sel).Canonical()
	if previous, collision := factsByMember[sel.Member]; collision && previous == canonical {
		t.Errorf("different consumed inputs selecting %q produced equal facts %q", sel.Member, canonical)
	}
	factsByMember[sel.Member] = canonical

	if len(factsByMember) > 2 {
		t.Fatalf("selection used more than two members: %v", factsByMember)
	}
}

func TestMarkerDecodeFact(t *testing.T) {
	t.Parallel()

	f := ethernet.Frame{EtherType: ethernet.EtherTypeSlowProtocols, Payload: make([]byte, 110)}
	fact := lag.MarkerDecodeFact(f, true, "")
	if fact.TypeID() != "lag.marker_decode" {
		t.Fatalf("TypeID() = %q, want lag.marker_decode", fact.TypeID())
	}
	want := `ether_type=34825;payload_len=110;valid=true;reason=""`
	if fact.Canonical() != want {
		t.Fatalf("Canonical() = %q, want %q", fact.Canonical(), want)
	}

	factInvalid := lag.MarkerDecodeFact(f, false, lag.ReasonUnsupportedLACPDU)
	wantInvalid := `ether_type=34825;payload_len=110;valid=false;reason="unsupported-lacpdu"`
	if factInvalid.Canonical() != wantInvalid {
		t.Fatalf("Canonical() = %q, want %q", factInvalid.Canonical(), wantInvalid)
	}
}

func TestSlowProtocolsDecodeFactSelectsSubtype(t *testing.T) {
	t.Parallel()

	reason := lag.ReasonUnsupportedLACPDU
	lacpFrame := ethernet.Frame{
		EtherType: ethernet.EtherTypeSlowProtocols,
		Payload:   []byte{lacp.SubtypeLACP},
	}
	markerFrame := ethernet.Frame{
		EtherType: ethernet.EtherTypeSlowProtocols,
		Payload:   []byte{lacp.SubtypeMarker},
	}

	lacpFact := lag.SlowProtocolsDecodeFact(lacpFrame, false, reason)
	if !trace.EqualFact(lacpFact, lag.LACPDecodeFact(lacpFrame, false, reason)) {
		t.Fatalf("SlowProtocolsDecodeFact() for LACP = %#v, want LACP decode fact", lacpFact)
	}

	markerFact := lag.SlowProtocolsDecodeFact(markerFrame, false, reason)
	if !trace.EqualFact(markerFact, lag.MarkerDecodeFact(markerFrame, false, reason)) {
		t.Fatalf("SlowProtocolsDecodeFact() for Marker = %#v, want Marker decode fact", markerFact)
	}
	if trace.EqualFact(lacpFact, markerFact) {
		t.Fatal("SlowProtocolsDecodeFact() returned the same fact for LACP and Marker")
	}
}
