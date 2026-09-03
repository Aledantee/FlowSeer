package fh

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type arpSweepFinding struct {
	Action string   `json:"action"`
	Net    string   `json:"net"`
	Count  int      `json:"count"`
	Hosts  []string `json:"hosts"`
}

// RunARPSweep sends requests to 172.16.0.1 through 172.16.0.3. Its finding
// lists requested targets, not discovered hosts; replies are not read.
// Craft and send failures are returned with operation context.
func RunARPSweep(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()
	// The fixture sweep range is 172.16.0.1–3.
	targets := []net.IP{
		net.IPv4(172, 16, 0, 1),
		net.IPv4(172, 16, 0, 2),
		net.IPv4(172, 16, 0, 3),
	}

	for i, target := range targets {
		pkt, err := craftARPSweep(src, target)
		if err != nil {
			return fmt.Errorf("arpsweep: craft frame %d: %w", i, err)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return fmt.Errorf("arpsweep: send frame %d: %w", i, err)
		}
	}

	hosts := make([]string, len(targets))
	for i, t := range targets {
		hosts[i] = t.String()
	}

	detail, _ := json.Marshal(arpSweepFinding{
		Action: "arp-sweep",
		Net:    "172.16.0.0/24",
		Count:  len(targets),
		Hosts:  hosts,
	})
	deps.Emitter.Finding("arp", detail)

	return nil
}

// craftARPSweep builds a broadcast ARP request for the given target IP.
func craftARPSweep(src net.HardwareAddr, target net.IP) ([]byte, error) {
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
		Operation:         1, // request
		SourceHwAddress:   src,
		SourceProtAddress: gwIP,
		DstHwAddress:      net.HardwareAddr{0, 0, 0, 0, 0, 0},
		DstProtAddress:    target,
	}
	return craftDefault(eth, arp)
}
