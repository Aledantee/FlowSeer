package fh

import (
	"context"
	"encoding/json"
	"net"
	"strings"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type arpSweepFinding struct {
	Action string   `json:"action"`
	Net    string   `json:"net"`
	Count  int      `json:"count"`
	Hosts  []string `json:"hosts"`
}

// RunARPSweep requests the first three hosts of the target's sweep network
// from the attack leg's own IPv4 address. Its verdict confirms the requested
// targets as the inventory without reading any reply. The sweep network comes
// from the target. The three-host bound is probe content.
func RunARPSweep(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldSweepNet); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	// The fixture carries the protocol addresses in their 16-byte form,
	// which gopacket serializes as is. The request keeps that shape until
	// the sweep's source changes with recon.
	srcIP := deps.AttackLeg.Addr().IPv4.To16()
	targets := firstHosts(deps.Target.SweepNet, 3)

	for i, target := range targets {
		pkt, err := craftARPSweep(src, srcIP, target)
		if err != nil {
			return check.Verdict{}, errs.Wrapf(err, "arpsweep: craft frame %d", i)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return check.Verdict{}, errs.Wrapf(err, "arpsweep: send frame %d", i)
		}
	}

	hosts := make([]string, len(targets))
	for i, t := range targets {
		hosts[i] = t.String()
	}

	detail, _ := json.Marshal(arpSweepFinding{
		Action: "arp-sweep",
		Net:    deps.Target.SweepNet.String(),
		Count:  len(targets),
		Hosts:  hosts,
	})
	deps.Emitter.Finding("arp", detail)

	return check.Confirmed("requested "+strings.Join(hosts, ", "), 0), nil
}

// firstHosts lists the first n host addresses of the network, fewer when
// the network is smaller.
func firstHosts(network *net.IPNet, n int) []net.IP {
	base := network.IP.To4()
	if base == nil {
		return nil
	}
	ones, bits := network.Mask.Size()
	hosts := 1<<(bits-ones) - 2
	out := make([]net.IP, 0, n)
	for i := 1; i <= n && i <= hosts; i++ {
		ip := make(net.IP, len(base))
		copy(ip, base)
		ip[3] += byte(i)
		out = append(out, ip.To16())
	}
	return out
}

// craftARPSweep builds a broadcast ARP request for the given target IP.
func craftARPSweep(src net.HardwareAddr, srcIP, target net.IP) ([]byte, error) {
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
		SourceProtAddress: srcIP,
		DstHwAddress:      net.HardwareAddr{0, 0, 0, 0, 0, 0},
		DstProtAddress:    target,
	}
	return craft.Default(eth, arp)
}
