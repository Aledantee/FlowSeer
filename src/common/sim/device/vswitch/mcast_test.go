package vswitch_test

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func replaceMLDHopByHop(t *testing.T, frame *ethernet.Frame, hopByHop []byte) {
	t.Helper()

	hdr, payload, err := ip.Decode(frame.Payload)
	if err != nil {
		t.Fatalf("decode IPv6: %v", err)
	}
	headerLength := (int(payload[1]) + 1) * 8
	packet, err := hdr.Encode(append(append([]byte(nil), hopByHop...), payload[headerLength:]...))
	if err != nil {
		t.Fatalf("encode IPv6: %v", err)
	}
	frame.Payload = packet
}

func setMLDType(t *testing.T, frame *ethernet.Frame, typ byte) {
	t.Helper()

	hdr, payload, err := ip.Decode(frame.Payload)
	if err != nil {
		t.Fatalf("decode IPv6: %v", err)
	}
	headerLength := (int(payload[1]) + 1) * 8
	suffix := payload[headerLength:]
	suffix[0] = typ
	suffix[2], suffix[3] = 0, 0
	binary.BigEndian.PutUint16(suffix[2:4], testICMPv6Checksum(hdr, suffix))
}

func testICMPv6Checksum(hdr ip.Header, payload []byte) uint16 {
	pseudo := make([]byte, 40+len(payload))
	src := hdr.Src.As16()
	dst := hdr.Dst.As16()
	copy(pseudo[0:16], src[:])
	copy(pseudo[16:32], dst[:])
	binary.BigEndian.PutUint32(pseudo[32:36], uint32(len(payload)))
	pseudo[39] = 58
	copy(pseudo[40:], payload)

	var sum uint32
	for len(pseudo) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(pseudo[:2]))
		pseudo = pseudo[2:]
	}
	if len(pseudo) == 1 {
		sum += uint32(pseudo[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

func mcastForwardedPorts(res vswitch.ForwardResult) []string {
	var ports []string
	for _, egress := range res.Egress {
		if egress.Dropped == "" {
			ports = append(ports, egress.Port)
		}
	}

	return ports
}

func TestIGMPControlForwardingAndLearning(t *testing.T) {
	group := netip.MustParseAddr("239.1.1.1")
	sw := mustSwitch(t, mcastSwitchConfig(t, nil))
	query := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
		mcastRouterMAC, allHostsMAC, 1,
		igmp.Message{Type: igmp.Query, Version: igmp.V2},
	)
	res := sw.Forward(fixedTime, "1/1/4", query)
	if got := mcastForwardedPorts(res); !slices.Equal(got, []string{"1/1/1", "1/1/2", "1/1/3"}) {
		t.Errorf("query ports = %v, want [1/1/1 1/1/2 1/1/3]", got)
	}
	if routers := sw.RouterPorts(10, fixedTime); len(routers) != 1 || routers[0].Port != "1/1/4" {
		t.Fatalf("RouterPorts(10) = %+v, want 1/1/4", routers)
	}

	report := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, mcastGroupMAC, 1,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	)
	res = sw.Forward(fixedTime.Add(time.Second), "1/1/1", report)
	if got := mcastForwardedPorts(res); !slices.Equal(got, []string{"1/1/4"}) {
		t.Errorf("report ports = %v, want [1/1/4]", got)
	}
	if groups := sw.Groups(10, fixedTime.Add(time.Second)); len(groups) != 1 || groups[0].Group != group || groups[0].Port != "1/1/1" {
		t.Errorf("Groups(10) = %+v, want 239.1.1.1 on 1/1/1", groups)
	}
	legacyGroup := netip.MustParseAddr("239.1.1.2")
	legacy := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.2"), legacyGroup,
		netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}, netaddr.MAC{0x01, 0x00, 0x5e, 0x01, 0x01, 0x02}, 1,
		igmp.Message{Type: igmp.ReportV1, Group: legacyGroup},
	)
	sw.Forward(fixedTime.Add(2*time.Second), "1/1/2", legacy)
	if groups := sw.Groups(10, fixedTime.Add(2*time.Second)); len(groups) != 2 {
		t.Errorf("Groups(10) after IGMPv1 report = %+v, want two memberships", groups)
	}
	if entries := sw.Entries(); len(entries) != 3 {
		t.Errorf("Entries() = %+v, want the query and both report source MACs learned", entries)
	}

	withoutRouter := mustSwitch(t, mcastSwitchConfig(t, nil))
	res = withoutRouter.Forward(fixedTime, "1/1/1", report)
	if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonNoRouterPort {
		t.Errorf("report before query = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonNoRouterPort)
	}
}

func TestMulticastReportIgnoresExpiredRouterPorts(t *testing.T) {
	for _, protocol := range []string{"IGMP", "MLD"} {
		t.Run(protocol, func(t *testing.T) {
			sw := mustSwitch(t, mcastSwitchConfig(t, nil))
			group := netip.MustParseAddr("239.1.1.1")
			query := makeIGMPControlFrame(t,
				netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
				mcastRouterMAC, allHostsMAC, 1, igmp.Message{Type: igmp.Query, Version: igmp.V2},
			)
			report := makeIGMPControlFrame(t,
				netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, mcastGroupMAC, 1,
				igmp.Message{Type: igmp.ReportV2, Group: group},
			)
			if protocol == "MLD" {
				group = netip.MustParseAddr("ff05::1")
				query = makeMLDControlFrame(t,
					netip.MustParseAddr("fe80::4"), netip.MustParseAddr("ff02::1"),
					mcastRouterMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
					mld.Message{Type: mld.Query, Version: mld.V1},
				)
				report = makeMLDControlFrame(t,
					netip.MustParseAddr("fe80::1"), group,
					mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
					mld.Message{Type: mld.ReportV1, Group: group},
				)
			}
			sw.Forward(fixedTime, "1/1/4", query)
			if routers := sw.RouterPorts(10, fixedTime); len(routers) != 1 || routers[0].Port != "1/1/4" {
				t.Fatalf("live router ports = %+v, want 1/1/4", routers)
			}
			if ports := mcastForwardedPorts(sw.Forward(fixedTime.Add(time.Second), "1/1/1", report)); !slices.Equal(ports, []string{"1/1/4"}) {
				t.Fatalf("live report ports = %v, want [1/1/4]", ports)
			}
			res := sw.Forward(fixedTime.Add(300*time.Second), "1/1/1", report)
			if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonNoRouterPort || len(mcastForwardedPorts(res)) != 0 {
				t.Errorf("expired report = %s/%s, ports %v, want Dropped/no-router-port and no ports", res.Outcome, res.Reason, mcastForwardedPorts(res))
			}
		})
	}
}

func TestMulticastQueryEvidenceIgnoresExpiredRouterPorts(t *testing.T) {
	cfg := mcastSwitchConfig(t, nil)
	snooping := cfg.Mcast.VLANs[10]
	snooping.RouterPortInterval = 10 * time.Second
	snooping.RouterPorts = []string{"1/1/3"}
	cfg.Mcast.VLANs[10] = snooping
	sw := mustSwitch(t, cfg)
	group := netip.MustParseAddr("239.1.1.1")
	sw.Forward(fixedTime, "1/1/4", makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
		mcastRouterMAC, allHostsMAC, 1, igmp.Message{Type: igmp.Query, Version: igmp.V2},
	))
	if routers := sw.RouterPorts(10, fixedTime); len(routers) != 2 {
		t.Fatalf("live routers = %+v, want configured and learned router ports", routers)
	}
	sw.Forward(fixedTime.Add(time.Second), "1/1/1", makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, mcastGroupMAC, 1,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	))
	sw.Forward(fixedTime.Add(2*time.Second), "1/1/1", makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("224.0.0.2"), mcastHostMAC, netaddr.MAC{1, 0, 0x5e, 0, 0, 2}, 1,
		igmp.Message{Type: igmp.Leave, Group: group},
	))
	res := sw.Peek(fixedTime.Add(15*time.Second), "1/1/2", ethernet.Frame{
		Src: mcastHostMAC, Dst: mcastGroupMAC, EtherType: ethernet.EtherTypeIPv4,
		Payload: makeIPv4Packet(t, netip.MustParseAddr("10.0.0.1"), group, 32, []byte("data")),
	})
	for _, issue := range res.Metadata.Issues() {
		if issue.Code != vswitch.IssueMcastQueryUnobserved {
			continue
		}
		if len(issue.Evidence) != 1 {
			t.Fatalf("query issue evidence = %v, want only the configured router port", issue.Evidence)
		}
		evidence, ok := res.Metadata.Evidence().Lookup(issue.Evidence[0])
		if !ok || !strings.Contains(evidence.Context, `name=\"1/1/3\"`) || strings.Contains(evidence.Context, "1/1/4") {
			t.Errorf("query issue evidence = %+v, found %v, want configured port 1/1/3", evidence, ok)
		}
		return
	}
	t.Fatalf("issues = %+v, want mcast-query-unobserved for the live group", res.Metadata.Issues())
}

func TestMulticastControlTraceDescribesGroupRecordTransition(t *testing.T) {
	tests := []struct {
		name        string
		firstGroup  netip.Addr
		secondGroup netip.Addr
		frame       func(*testing.T, netip.Addr) ethernet.Frame
	}{
		{
			name:        "IGMPv3",
			firstGroup:  netip.MustParseAddr("239.1.1.1"),
			secondGroup: netip.MustParseAddr("239.1.1.2"),
			frame: func(t *testing.T, group netip.Addr) ethernet.Frame {
				return makeIGMPControlFrame(t,
					netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("224.0.0.22"),
					mcastHostMAC, netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x16}, 1,
					igmp.Message{Type: igmp.ReportV3, Records: []igmp.GroupRecord{{
						Type: igmp.ChangeToExcludeMode, Group: group, Sources: []netip.Addr{netip.MustParseAddr("10.0.0.9")},
					}}},
				)
			},
		},
		{
			name:        "MLDv2",
			firstGroup:  netip.MustParseAddr("ff05::1"),
			secondGroup: netip.MustParseAddr("ff05::2"),
			frame: func(t *testing.T, group netip.Addr) ethernet.Frame {
				return makeMLDControlFrame(t,
					netip.MustParseAddr("fe80::1"), netip.MustParseAddr("ff02::16"),
					mcastHostMAC, netaddr.MAC{0x33, 0x33, 0x00, 0x00, 0x00, 0x16}, 1, true,
					mld.Message{Type: mld.ReportV2, Records: []mld.AddressRecord{{
						Type: mld.ChangeToExcludeMode, Group: group, Sources: []netip.Addr{netip.MustParseAddr("2001:db8::9")},
					}}},
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := mustSwitch(t, mcastSwitchConfig(t, nil)).Forward(fixedTime, "1/1/1", test.frame(t, test.firstGroup))
			second := mustSwitch(t, mcastSwitchConfig(t, nil)).Forward(fixedTime, "1/1/1", test.frame(t, test.secondGroup))
			if first.Equal(second.Trace) {
				t.Fatal("equal-length reports for distinct groups produced equal traces")
			}

			found := false
			for _, step := range first.Steps {
				facts := append(slices.Clone(step.Inputs), step.Outputs...)
				for _, fact := range facts {
					if fact.TypeID() != "mcast.control_message" {
						continue
					}
					found = strings.Contains(fact.Canonical(), test.firstGroup.String()) &&
						strings.Contains(fact.Canonical(), "record_type=") &&
						strings.Contains(fact.Canonical(), "sources=[")
				}
			}
			if !found {
				t.Errorf("steps = %+v, want decoded multicast control transition for %s", first.Steps, test.firstGroup)
			}
		})
	}
}

func TestIGMPControlRejectsBadFramesWithoutState(t *testing.T) {
	group := netip.MustParseAddr("239.1.1.1")
	report := igmp.Message{Type: igmp.ReportV2, Group: group}

	tests := []struct {
		name  string
		frame func(*testing.T) ethernet.Frame
	}{
		{
			name: "bad checksum",
			frame: func(t *testing.T) ethernet.Frame {
				frame := makeIGMPControlFrame(t, netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, mcastGroupMAC, 1, report)
				frame.Payload[len(frame.Payload)-1] ^= 0xff
				return frame
			},
		},
		{
			name: "bad IPv4 header checksum",
			frame: func(t *testing.T) ethernet.Frame {
				frame := makeIGMPControlFrame(t, netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, mcastGroupMAC, 1, report)
				frame.Payload[10] ^= 0xff
				return frame
			},
		},
		{
			name: "EtherType and version mismatch",
			frame: func(t *testing.T) ethernet.Frame {
				frame := makeIGMPControlFrame(t, netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, mcastGroupMAC, 1, report)
				frame.Payload[0] = 6<<4 | frame.Payload[0]&0x0f
				return frame
			},
		},
		{
			name: "ttl two",
			frame: func(t *testing.T) ethernet.Frame {
				return makeIGMPControlFrame(t, netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, mcastGroupMAC, 2, report)
			},
		},
		{
			name: "unicast destination",
			frame: func(t *testing.T) ethernet.Frame {
				return makeIGMPControlFrame(t, netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2"), mcastHostMAC, mcastGroupMAC, 1, report)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sw := mustSwitch(t, mcastSwitchConfig(t, nil))
			res := sw.Forward(fixedTime, "1/1/1", tt.frame(t))
			if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonBadControl {
				t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonBadControl)
			}
			if len(sw.Entries()) != 0 || len(sw.Groups(10, fixedTime)) != 0 || len(sw.RouterPorts(10, fixedTime)) != 0 {
				t.Errorf("bad control changed state: entries=%+v groups=%+v routers=%+v", sw.Entries(), sw.Groups(10, fixedTime), sw.RouterPorts(10, fixedTime))
			}
		})
	}
}

func TestMalformedMLDCandidateDoesNotLearn(t *testing.T) {
	group := netip.MustParseAddr("ff05::1")
	frame := makeMLDControlFrame(t,
		netip.MustParseAddr("fe80::1"), group,
		mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
		mld.Message{Type: mld.ReportV1, Group: group},
	)
	frame.Payload = frame.Payload[:ip.V6HeaderLen+1]

	sw := mustSwitch(t, mcastSwitchConfig(t, nil))
	res := sw.Forward(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonBadControl {
		t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonBadControl)
	}
	if len(sw.Entries()) != 0 || len(sw.Groups(10, fixedTime)) != 0 || len(sw.RouterPorts(10, fixedTime)) != 0 {
		t.Errorf("truncated MLD control changed state: entries=%+v groups=%+v routers=%+v", sw.Entries(), sw.Groups(10, fixedTime), sw.RouterPorts(10, fixedTime))
	}
}

func TestUnsupportedIGMPFloodsWithoutSnoopingMutation(t *testing.T) {
	group := netip.MustParseAddr("239.1.1.1")
	frame := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, mcastGroupMAC, 1,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	)
	payload := frame.Payload[ip.V4HeaderLen:]
	payload[0] = 0x30
	payload[2], payload[3] = 0, 0
	var sum uint32
	for i := 0; i < len(payload); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(payload[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	binary.BigEndian.PutUint16(payload[2:4], ^uint16(sum))

	sw := mustSwitch(t, mcastSwitchConfig(t, nil))
	res := sw.Forward(fixedTime, "1/1/1", frame)
	if got := mcastForwardedPorts(res); !slices.Equal(got, []string{"1/1/2", "1/1/3", "1/1/4"}) {
		t.Errorf("unsupported IGMP ports = %v, want every other VLAN port", got)
	}
	if len(sw.Groups(10, fixedTime)) != 0 || len(sw.RouterPorts(10, fixedTime)) != 0 {
		t.Errorf("unsupported IGMP changed snooping state: groups=%+v routers=%+v", sw.Groups(10, fixedTime), sw.RouterPorts(10, fixedTime))
	}
	if entries := sw.Entries(); len(entries) != 1 || entries[0].MAC != mcastHostMAC {
		t.Errorf("Entries() = %+v, want ordinary MAC learning", entries)
	}
}

func TestUnsupportedMLDFloodsWithoutSnoopingMutation(t *testing.T) {
	group := netip.MustParseAddr("ff05::1")
	frame := makeMLDControlFrame(t,
		netip.MustParseAddr("fe80::1"), group,
		mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
		mld.Message{Type: mld.ReportV1, Group: group},
	)
	setMLDType(t, &frame, 200)

	sw := mustSwitch(t, mcastSwitchConfig(t, new(false)))
	res := sw.Forward(fixedTime, "1/1/1", frame)
	if got := mcastForwardedPorts(res); !slices.Equal(got, []string{"1/1/2", "1/1/3", "1/1/4"}) {
		t.Errorf("unsupported MLD ports = %v, want every other VLAN port", got)
	}
	for _, egress := range res.Egress {
		got := egress.Frame
		if got.Dst != frame.Dst || got.Src != frame.Src || got.EtherType != frame.EtherType ||
			!slices.Equal(got.Tags, frame.Tags) || !slices.Equal(got.Payload, frame.Payload) {
			t.Errorf("unsupported MLD frame on %s = %+v, want unchanged %+v", egress.Port, got, frame)
		}
	}
	if len(sw.Groups(10, fixedTime)) != 0 || len(sw.RouterPorts(10, fixedTime)) != 0 {
		t.Errorf("unsupported MLD changed snooping state: groups=%+v routers=%+v", sw.Groups(10, fixedTime), sw.RouterPorts(10, fixedTime))
	}
	if entries := sw.Entries(); len(entries) != 1 || entries[0].MAC != mcastHostMAC {
		t.Errorf("Entries() = %+v, want ordinary MAC learning", entries)
	}
}

func TestUnsupportedMLDRequiresLinkLocalSource(t *testing.T) {
	group := netip.MustParseAddr("ff05::1")
	for _, source := range []netip.Addr{netip.MustParseAddr("fe80::1"), netip.MustParseAddr("2001:db8::1")} {
		t.Run(source.String(), func(t *testing.T) {
			frame := makeMLDControlFrame(t, source, group,
				mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
				mld.Message{Type: mld.ReportV1, Group: group})
			setMLDType(t, &frame, 200)
			sw := mustSwitch(t, mcastSwitchConfig(t, nil))
			res := sw.Forward(fixedTime, "1/1/1", frame)
			if source.IsLinkLocalUnicast() {
				if got := mcastForwardedPorts(res); !slices.Equal(got, []string{"1/1/2", "1/1/3", "1/1/4"}) {
					t.Errorf("unsupported MLD ports = %v, want every other VLAN port", got)
				}
			} else {
				if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonBadControl || len(mcastForwardedPorts(res)) != 0 {
					t.Errorf("Forward = %s/%s, ports = %v, want Dropped/%s with no forwarded ports", res.Outcome, res.Reason, mcastForwardedPorts(res), mcast.ReasonBadControl)
				}
			}
		})
	}
}

func TestMLDReportAcceptsUnspecifiedSource(t *testing.T) {
	group := netip.MustParseAddr("ff05::1")
	for _, version := range []mld.Type{mld.ReportV1, mld.ReportV2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			sw := mustSwitch(t, mcastSwitchConfig(t, nil))
			message := mld.Message{Type: version, Group: group}
			if version == mld.ReportV2 {
				message = mld.Message{Type: version, Records: []mld.AddressRecord{{Type: mld.ModeIsExclude, Group: group}}}
			}
			frame := makeMLDControlFrame(t, netip.IPv6Unspecified(), netip.MustParseAddr("ff02::16"), mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 0x16}, 1, true, message)
			sw.Forward(fixedTime, "1/1/1", frame)
			if entries := sw.Groups(10, fixedTime); len(entries) != 1 || entries[0].Group != group || entries[0].Port != "1/1/1" {
				t.Errorf("Groups = %+v, want membership from unspecified report source", entries)
			}
		})
	}
}

func TestMLDUnspecifiedQueryAndDoneRefused(t *testing.T) {
	group := netip.MustParseAddr("ff05::1")
	for _, messageType := range []mld.Type{mld.Query, mld.Done} {
		t.Run(fmt.Sprint(messageType), func(t *testing.T) {
			sw := mustSwitch(t, mcastSwitchConfig(t, nil))
			frame := makeMLDControlFrame(t, netip.IPv6Unspecified(), group, mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true, mld.Message{Type: messageType, Version: mld.V1, Group: group})
			result := sw.Forward(fixedTime, "1/1/1", frame)
			if result.Outcome != trace.Dropped || result.Reason != mcast.ReasonBadControl {
				t.Errorf("Forward = %s/%s, want Dropped/%s", result.Outcome, result.Reason, mcast.ReasonBadControl)
			}
		})
	}
}

func TestMLDControlValidatesOuterHeader(t *testing.T) {
	group := netip.MustParseAddr("ff05::1")
	tests := []struct {
		name        string
		source      netip.Addr
		hopLimit    uint8
		routerAlert bool
	}{
		{name: "missing Router Alert", source: netip.MustParseAddr("fe80::1"), hopLimit: 1},
		{name: "hop limit two", source: netip.MustParseAddr("fe80::1"), hopLimit: 2, routerAlert: true},
		{name: "non-link-local source", source: netip.MustParseAddr("2001:db8::1"), hopLimit: 1, routerAlert: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := makeMLDControlFrame(t,
				tt.source, group,
				mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, tt.hopLimit, tt.routerAlert,
				mld.Message{Type: mld.ReportV1, Group: group},
			)
			sw := mustSwitch(t, mcastSwitchConfig(t, nil))
			res := sw.Forward(fixedTime, "1/1/1", frame)
			if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonBadControl {
				t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonBadControl)
			}
			if len(sw.Entries()) != 0 || len(sw.Groups(10, fixedTime)) != 0 {
				t.Errorf("bad MLD changed state: entries=%+v groups=%+v", sw.Entries(), sw.Groups(10, fixedTime))
			}
		})
	}
}

func TestMLDRouterAlertValidation(t *testing.T) {
	group := netip.MustParseAddr("ff05::1")
	tests := []struct {
		name     string
		hopByHop []byte
	}{
		{name: "non-MLD value", hopByHop: []byte{58, 0, 5, 2, 0, 1, 1, 0}},
		{name: "duplicate", hopByHop: []byte{58, 1, 5, 2, 0, 0, 5, 2, 0, 0, 1, 0, 0, 0, 0, 0}},
		{name: "malformed trailing option", hopByHop: []byte{58, 0, 5, 2, 0, 0, 1, 4}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frame := makeMLDControlFrame(t,
				netip.MustParseAddr("fe80::1"), group,
				mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
				mld.Message{Type: mld.ReportV1, Group: group},
			)
			replaceMLDHopByHop(t, &frame, tc.hopByHop)

			sw := mustSwitch(t, mcastSwitchConfig(t, nil))
			res := sw.Forward(fixedTime, "1/1/1", frame)
			if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonBadControl {
				t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonBadControl)
			}
			if len(sw.Entries()) != 0 || len(sw.Groups(10, fixedTime)) != 0 || len(sw.RouterPorts(10, fixedTime)) != 0 {
				t.Errorf("invalid Router Alert changed state: entries=%+v groups=%+v routers=%+v", sw.Entries(), sw.Groups(10, fixedTime), sw.RouterPorts(10, fixedTime))
			}
		})
	}
}

func TestSnoopedControlPrecedesRoutedVLANOwnership(t *testing.T) {
	group := netip.MustParseAddr("239.1.1.1")
	cfg := mcastSwitchConfig(t, nil)
	cfg.Routing = &routing.Config{VRFs: map[string]routing.VRF{
		"default": {
			Interfaces: map[string]routing.Interface{
				"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.254/24")}},
			},
		},
	}}
	frame := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, macRouter, 2,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	)

	sw := mustSwitch(t, cfg)
	res := sw.Forward(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonBadControl {
		t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonBadControl)
	}
	if len(sw.Entries()) != 0 || len(sw.Groups(10, fixedTime)) != 0 || len(sw.RouterPorts(10, fixedTime)) != 0 {
		t.Errorf("routed bad control changed state: entries=%+v groups=%+v routers=%+v", sw.Entries(), sw.Groups(10, fixedTime), sw.RouterPorts(10, fixedTime))
	}
}

func TestMulticastDataResolutionAndFloodExceptions(t *testing.T) {
	flood := false
	sw := mustSwitch(t, mcastSwitchConfig(t, &flood))
	group := netip.MustParseAddr("239.2.2.2")
	data := func(dst netip.Addr, dstMAC netaddr.MAC, etherType ethernet.EtherType) ethernet.Frame {
		frame := ethernet.Frame{Dst: dstMAC, Src: netaddr.MAC{0, 1, 2, 3, 4, 5}, EtherType: etherType}
		if etherType == ethernet.EtherTypeIPv4 {
			frame.Payload = makeIPv4Packet(t, netip.MustParseAddr("10.0.0.3"), dst, 32, []byte("data"))
		}
		return frame
	}
	ipv6Data := func(dst netip.Addr) ethernet.Frame {
		addr := dst.As16()
		return ethernet.Frame{
			Dst:       netaddr.MAC{0x33, 0x33, addr[12], addr[13], addr[14], addr[15]},
			Src:       mcastHostMAC,
			EtherType: ethernet.EtherTypeIPv6,
			Payload:   makeIPv6Packet(t, netip.MustParseAddr("2001:db8::3"), dst, 32, []byte("data")),
		}
	}

	res := sw.Forward(fixedTime, "1/1/3", data(group, netaddr.MAC{0x01, 0x00, 0x5e, 0x02, 0x02, 0x02}, ethernet.EtherTypeIPv4))
	if res.Reason != mcast.ReasonUnregistered {
		t.Errorf("unregistered group without router reason = %q, want %q", res.Reason, mcast.ReasonUnregistered)
	}
	if !traceHasFactType(res.Steps, "mcast.membership_decision") {
		t.Errorf("unregistered multicast trace has no membership decision: %+v", res.Steps)
	}

	query := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
		mcastRouterMAC, allHostsMAC, 1,
		igmp.Message{Type: igmp.Query, Version: igmp.V2},
	)
	sw.Forward(fixedTime, "1/1/4", query)
	res = sw.Forward(fixedTime, "1/1/3", data(group, netaddr.MAC{0x01, 0x00, 0x5e, 0x02, 0x02, 0x02}, ethernet.EtherTypeIPv4))
	if got := mcastForwardedPorts(res); !slices.Equal(got, []string{"1/1/4"}) {
		t.Errorf("unregistered group with router ports = %v, want [1/1/4]", got)
	}
	if !traceHasFactType(res.Steps, "mcast.membership_decision") {
		t.Errorf("router-port multicast trace has no membership decision: %+v", res.Steps)
	}

	for name, frame := range map[string]ethernet.Frame{
		"IPv4 link-local group": data(netip.MustParseAddr("224.0.0.5"), netaddr.MAC{0x01, 0x00, 0x5e, 0, 0, 5}, ethernet.EtherTypeIPv4),
		"IPv6 all nodes":        ipv6Data(netip.MustParseAddr("ff02::1")),
		"IPv6 scope zero":       ipv6Data(netip.MustParseAddr("ff00::1")),
		"IPv6 scope one":        ipv6Data(netip.MustParseAddr("ff01::1")),
		"limited broadcast":     data(netip.MustParseAddr("255.255.255.255"), netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, ethernet.EtherTypeIPv4),
		"non-IP group":          {Dst: netaddr.MAC{0x01, 0, 0, 0, 0, 1}, Src: mcastHostMAC, EtherType: ethernet.EtherTypeARP},
	} {
		t.Run(name, func(t *testing.T) {
			got := mcastForwardedPorts(sw.Forward(fixedTime, "1/1/3", frame))
			if !slices.Equal(got, []string{"1/1/1", "1/1/2", "1/1/4"}) {
				t.Errorf("forwarded ports = %v, want ordinary flood", got)
			}
		})
	}
}

func TestPeekLeavesMulticastAndMACTablesUntouched(t *testing.T) {
	group := netip.MustParseAddr("239.1.1.1")
	sw := mustSwitch(t, mcastSwitchConfig(t, nil))
	report := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, mcastGroupMAC, 1,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	)
	sw.Peek(fixedTime, "1/1/1", report)
	if len(sw.Entries()) != 0 || len(sw.Groups(10, fixedTime)) != 0 || len(sw.RouterPorts(10, fixedTime)) != 0 {
		t.Errorf("Peek changed state: entries=%+v groups=%+v routers=%+v", sw.Entries(), sw.Groups(10, fixedTime), sw.RouterPorts(10, fixedTime))
	}
	query := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
		mcastRouterMAC, allHostsMAC, 1, igmp.Message{Type: igmp.Query, Version: igmp.V2},
	)
	sw.Forward(fixedTime, "1/1/4", query)
	sw.Forward(fixedTime, "1/1/1", report)
	beforeMACs := sw.Entries()
	beforeGroups, beforeRouters := sw.Groups(10, fixedTime), sw.RouterPorts(10, fixedTime)
	if len(beforeMACs) != 2 || len(beforeGroups) != 1 || len(beforeRouters) != 1 {
		t.Fatalf("live state = (%+v, %+v, %+v), want two MACs, one group and one router", beforeMACs, beforeGroups, beforeRouters)
	}
	now := fixedTime.Add(300 * time.Second)
	data := ethernet.Frame{
		Src: mcastHostMAC, Dst: mcastGroupMAC, EtherType: ethernet.EtherTypeIPv4,
		Payload: makeIPv4Packet(t, netip.MustParseAddr("10.0.0.1"), group, 32, []byte("data")),
	}
	if ports := mcastForwardedPorts(sw.Peek(now, "1/1/3", data)); !slices.Equal(ports, []string{"1/1/1", "1/1/2", "1/1/4"}) {
		t.Errorf("expired data Peek ports = %v, want unregistered flood", ports)
	}
	sw.Peek(now, "1/1/2", report)
	if groups, routers := sw.Groups(10, now), sw.RouterPorts(10, now); len(groups) != 0 || len(routers) != 0 {
		t.Errorf("expired reads = (%+v, %+v), want no groups or routers", groups, routers)
	}
	if got := sw.Entries(); !reflect.DeepEqual(got, beforeMACs) {
		t.Errorf("Peek changed MACs = %+v, want %+v", got, beforeMACs)
	}
	if got := sw.Groups(10, fixedTime); !reflect.DeepEqual(got, beforeGroups) {
		t.Errorf("reads changed stored groups = %+v, want %+v", got, beforeGroups)
	}
	if got := sw.RouterPorts(10, fixedTime); !reflect.DeepEqual(got, beforeRouters) {
		t.Errorf("reads changed stored routers = %+v, want %+v", got, beforeRouters)
	}
}

func TestMulticastControlRunsThroughTrafficFinishing(t *testing.T) {
	cfg := mcastSwitchConfig(t, nil)
	vlanCfg := cfg.Mcast.VLANs[10]
	vlanCfg.RouterPorts = []string{"1/1/3"}
	cfg.Mcast.VLANs[10] = vlanCfg
	cfg.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{
		Name: "control", SelectSrcPorts: []string{"1/1/1"}, OutputPort: "1/1/3",
	}}}
	group := netip.MustParseAddr("239.1.1.1")
	report := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, mcastGroupMAC, 1,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	)

	sw := mustSwitch(t, cfg)
	res := sw.Forward(fixedTime, "1/1/1", report)
	if res.Outcome != trace.Dropped || res.Reason != traffic.ReasonMirrorOutput {
		t.Errorf("control result = %s/%s, want Dropped/%s", res.Outcome, res.Reason, traffic.ReasonMirrorOutput)
	}
	if len(res.Egress) != 1 || res.Egress[0].Port != "1/1/3" || res.Egress[0].Dropped != traffic.ReasonMirrorOutput {
		t.Errorf("control egress = %+v, want mirror-output drop on 1/1/3", res.Egress)
	}
	if copies := sw.Copies(); len(copies) != 1 || copies[0].Mirror != "control" || copies[0].Port != "1/1/3" {
		t.Errorf("Copies() = %+v, want control mirror on 1/1/3", copies)
	}
}

func TestMulticastValidationAndDerivation(t *testing.T) {
	base := mcastSwitchConfig(t, nil)
	if caps := base.Capabilities(); !slices.Contains(caps, mcast.LayerName) {
		t.Errorf("Capabilities() = %v, want mcast", caps)
	}

	t.Run("requires VLAN-aware bridge", func(t *testing.T) {
		cfg := base.Clone()
		cfg.Bridge = &bridge.Config{}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate accepted multicast without VLAN-aware bridge")
		}
	})

	t.Run("requires snooped VLAN in bridge table", func(t *testing.T) {
		cfg := base.Clone()
		cfg.Mcast.VLANs[20] = mcast.VLANSnooping{}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate accepted a snooped VLAN absent from the bridge table")
		}
	})

	t.Run("refuses physical LAG member as router port", func(t *testing.T) {
		pvid := vlan.ID(10)
		cfg := base.Clone()
		cfg.Ports = mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}))
		cfg.Bridge.VLAN.Switchports = map[string]bridge.Switchport{"lag1": {PVID: &pvid, Untagged: []vlan.ID{10}}}
		cfg.Mcast.VLANs[10] = mcast.VLANSnooping{RouterPorts: []string{"1/1/1"}}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate accepted a physical LAG member as a router port")
		}
	})

	t.Run("requires router port forwarding membership", func(t *testing.T) {
		cfg := base.Clone()
		delete(cfg.Bridge.VLAN.Switchports, "1/1/4")
		cfg.Mcast.VLANs[10] = mcast.VLANSnooping{RouterPorts: []string{"1/1/4"}}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate accepted a router port outside the snooped VLAN")
		}
	})

	group := netip.MustParseAddr("239.1.1.1")
	cur := mustSwitch(t, base)
	cur.Forward(fixedTime, "1/1/1", makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, mcastGroupMAC, 1,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	))

	kept, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: base.Clone()})
	if err != nil {
		t.Fatalf("Derive retaining multicast state: %v", err)
	}
	if groups := kept.Groups(10, fixedTime); len(groups) != 1 || groups[0].GroupExpires != fixedTime.Add(mcast.DefaultMembershipInterval) {
		t.Errorf("derived Groups(10) = %+v, want retained entry with original expiry", groups)
	}

	removed := base.Clone()
	delete(removed.Mcast.VLANs, 10)
	dropped, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: removed})
	if err != nil {
		t.Fatalf("Derive removing snooped VLAN: %v", err)
	}
	if groups := dropped.Groups(10, fixedTime); len(groups) != 0 {
		t.Errorf("derived Groups(10) = %+v, want none after snooping removal", groups)
	}

	query := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
		mcastRouterMAC, allHostsMAC, 1,
		igmp.Message{Type: igmp.Query, Version: igmp.V2},
	)
	cur.Forward(fixedTime, "1/1/4", query)
	changed := base.Clone()
	changedMcast := changed.Mcast.VLANs[10]
	changedMcast.MembershipInterval = 30 * time.Second
	changedMcast.RouterPortInterval = 45 * time.Second
	changedMcast.RouterPorts = []string{"1/1/3"}
	changed.Mcast.VLANs[10] = changedMcast
	retained, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: changed})
	if err != nil {
		t.Fatalf("Derive changing multicast configuration: %v", err)
	}
	if groups := retained.Groups(10, fixedTime); len(groups) != 1 || groups[0].GroupExpires != fixedTime.Add(mcast.DefaultMembershipInterval) {
		t.Errorf("Groups(10) after configuration change = %+v, want retained original expiry", groups)
	}
	routers := retained.RouterPorts(10, fixedTime)
	if len(routers) != 2 || routers[0].Port != "1/1/3" || routers[0].Lifetime != mcast.Static ||
		routers[1].Port != "1/1/4" || routers[1].Lifetime == mcast.Static || routers[1].Expires != fixedTime.Add(mcast.DefaultMembershipInterval) {
		t.Errorf("RouterPorts(10) after configuration change = %+v, want new static 1/1/3 and retained learned 1/1/4", routers)
	}
}

// TestMulticastLeaveTimingThroughForward is evidence for this change: a
// non-fast leave keeps forwarding for LMQT after an observed group-specific
// query, for the full membership interval and mcast-query-unobserved without
// one, and Complete regardless when the VLAN has no router port.
func TestMulticastLeaveTimingThroughForward(t *testing.T) {
	group := netip.MustParseAddr("239.6.6.6")
	groupMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x06, 0x06, 0x06}
	allRoutersMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x02}

	build := func(t *testing.T, withRouter bool) *vswitch.Switch {
		t.Helper()
		pvid := vlan.ID(10)
		ports := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "p9", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
		snooping := mcast.VLANSnooping{LastMemberQueryInterval: time.Second, LastMemberQueryCount: 2}
		if withRouter {
			snooping.RouterPorts = []string{"p9"}
		}
		return mustSwitch(t, vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{
					"p1": {PVID: &pvid, Untagged: []vlan.ID{10}},
					"p9": {PVID: &pvid, Untagged: []vlan.ID{10}},
				},
			}},
			Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: snooping}},
		})
	}

	dataFrame := func(t *testing.T, source netip.Addr) ethernet.Frame {
		t.Helper()

		return ethernet.Frame{
			Dst:       groupMAC,
			Src:       mcastRouterMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   makeIPv4Packet(t, source, group, 32, []byte("data")),
		}
	}

	t0 := fixedTime
	leaveAt := t0.Add(10 * time.Second)
	other := netip.MustParseAddr("10.9.9.9")

	join := func(t *testing.T, sw *vswitch.Switch) {
		sw.Forward(t0, "p1", makeIGMPControlFrame(t,
			netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, groupMAC, 1,
			igmp.Message{Type: igmp.ReportV2, Group: group}))
	}
	leave := func(t *testing.T, sw *vswitch.Switch) {
		sw.Forward(leaveAt, "p1", makeIGMPControlFrame(t,
			netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("224.0.0.2"), mcastHostMAC, allRoutersMAC, 1,
			igmp.Message{Type: igmp.Leave, Group: group}))
	}

	t.Run("observed query shortens the leave to LMQT", func(t *testing.T) {
		sw := build(t, true)
		join(t, sw)
		leave(t, sw)
		sw.Forward(leaveAt, "p9", makeIGMPControlFrame(t,
			netip.MustParseAddr("10.0.0.254"), group, mcastRouterMAC, groupMAC, 1,
			igmp.Message{Type: igmp.Query, Version: igmp.V2, Group: group}))

		res1 := sw.Forward(leaveAt.Add(time.Second), "p9", dataFrame(t, other))
		if !slices.Contains(mcastForwardedPorts(res1), "p1") {
			t.Errorf("egress at t+1s = false, want true")
		}
		res2 := sw.Forward(leaveAt.Add(2*time.Second), "p9", dataFrame(t, other))
		if groups := sw.Groups(10, leaveAt.Add(2*time.Second)); len(groups) != 0 {
			t.Errorf("groups at t+2s = %+v, want expired membership removed from the read", groups)
		}
		if res2.Outcome != trace.Flooded || !slices.Equal(mcastForwardedPorts(res2), []string{"p1"}) {
			t.Errorf("forward at t+2s = %s, ports %v, want unregistered flood to p1", res2.Outcome, mcastForwardedPorts(res2))
		}
	})

	t.Run("without a query the leave runs the full interval and flags the gap", func(t *testing.T) {
		sw := build(t, true)
		join(t, sw)
		leave(t, sw)

		res := sw.Forward(leaveAt.Add(3*time.Second), "p9", dataFrame(t, other))
		if !slices.Contains(mcastForwardedPorts(res), "p1") {
			t.Errorf("egress at t+3s = false, want true")
		}
		if !hasIssueCode(res.Metadata.Issues(), vswitch.IssueMcastQueryUnobserved) {
			t.Errorf("issues at t+3s = %+v, want mcast-query-unobserved", res.Metadata.Issues())
		}
		if res.Metadata.Status() != analysis.Incomplete {
			t.Errorf("status at t+3s = %v, want Incomplete", res.Metadata.Status())
		}
	})

	t.Run("with no router port nothing is flagged", func(t *testing.T) {
		sw := build(t, false)
		join(t, sw)
		leave(t, sw)

		res := sw.Forward(leaveAt.Add(3*time.Second), "p9", dataFrame(t, other))
		if !slices.Contains(mcastForwardedPorts(res), "p1") {
			t.Errorf("egress at t+3s = false, want true")
		}
		if hasIssueCode(res.Metadata.Issues(), vswitch.IssueMcastQueryUnobserved) {
			t.Errorf("issues at t+3s = %+v, want no mcast-query-unobserved", res.Metadata.Issues())
		}
		if res.Metadata.Status() != analysis.Complete {
			t.Errorf("status at t+3s = %v, want Complete", res.Metadata.Status())
		}
	})
}
