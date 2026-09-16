package fabric_test

import (
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// loopProtectSelfLoopPorts builds the port table shared by the self-loop
// fixtures: two ports cabled directly to each other forming the loop, and
// two more carrying a host each.
func loopProtectSelfLoopPorts(t *testing.T) port.Table {
	t.Helper()

	return mustFabricPortTable(t,
		port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
		port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
		port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
		port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
	)
}

func loopProtectSelfLoopConfig(t *testing.T, action loopprotect.Action) (fabric.Config, netaddr.MAC) {
	t.Helper()

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := fabric.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  loopProtectSelfLoopPorts(t),
				Bridge: &bridge.Config{},
				LoopProtect: &loopprotect.Config{
					Interval: 5 * time.Second,
					Ports: map[string]loopprotect.Port{
						"1/1/1": {Action: action},
						"1/1/2": {Action: action},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, B: fabric.Endpoint{Node: "h1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/4"}, B: fabric.Endpoint{Node: "h2"}},
		},
	}

	return cfg, macH1
}

// runToQuiescence runs the fabric until its arrival queue drains on its own
// or the budget is exhausted, whichever comes first, and fails the test if
// a scheduling fault occurred.
func runToQuiescence(t *testing.T, fab *fabric.Fabric, budget int) {
	t.Helper()

	fab.Run(budget)
	if err := fab.Err(); err != nil {
		t.Fatalf("fabric run fault: %v", err)
	}
}

// TestLoopProtectSelfLoopBlockActsOnExactlyOnePort proves Block's two-port
// self-loop behavior: a switch with two of its own ports cabled directly
// together forms a two-port loop. Each port's own probe returns on the
// other, but only the first one processed applies Block, and Block's denial
// of both learning and forwarding on that port stops the second probe's
// payload from ever being classified, so exactly one port is acted on.
func TestLoopProtectSelfLoopBlockActsOnExactlyOnePort(t *testing.T) {
	cfg, macH1 := loopProtectSelfLoopConfig(t, loopprotect.Block)

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	runToQuiescence(t, fab, 200)

	blocked, _ := loopProtectBlockedPort(t, fab, "1/1/1", "1/1/2")

	// The property under test: the bridge's ordinary ingress gate check in
	// the interception is what stops both ports from being acted on. With
	// that check skipped, a second own-probe would be classified anyway and
	// loopProtectBlockedPort would find both ports blocked, returning ""
	// for both instead of naming exactly one.
	if blocked == "" {
		t.Fatalf("no port was blocked; want exactly one of 1/1/1, 1/1/2 acted on")
	}

	now := fab.Snapshot().Clock.Add(time.Second)
	broadcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	fid, err := fab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Dst: broadcast, Src: macH1, EtherType: 0x0800, Payload: []byte{1, 2, 3, 4}},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	runToQuiescence(t, fab, 200)

	deliveries := 0
	for _, j := range fab.Report() {
		if j.FrameID != fid {
			continue
		}
		for _, d := range j.Deliveries {
			if d.Host == "h2" {
				deliveries++
			}
		}
	}
	if deliveries != 1 {
		t.Errorf("h2 received %d copies of the post-block broadcast, want exactly 1", deliveries)
	}
}

// TestLoopProtectSelfLoopNoLearnActsOnBothPorts proves NoLearn's two-port
// self-loop behavior: NoLearn denies learning but keeps forwarding, so
// neither probe's return is intercepted by the gate the way Block's is, and
// both ports end up carrying the action.
func TestLoopProtectSelfLoopNoLearnActsOnBothPorts(t *testing.T) {
	cfg, _ := loopProtectSelfLoopConfig(t, loopprotect.NoLearn)

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	runToQuiescence(t, fab, 200)

	for _, name := range []string{"1/1/1", "1/1/2"} {
		if loopProtectPortBlocked(t, fab, name) {
			t.Errorf("port %s denied forwarding under NoLearn, want it still forwarding (NoLearn only denies learning)", name)
		}
	}

	for _, name := range []string{"1/1/1", "1/1/2"} {
		if !traceLoopProtectBlockStepSeen(t, fab, name) {
			t.Errorf("port %s never recorded a loop-protection port.block step, want both ports acted on under NoLearn", name)
		}
	}
}

// loopProtectPortBlocked injects a distinguishable unicast frame directly
// into the named port and reports whether the bridge's ingress gate denied
// it as port-blocked.
func loopProtectPortBlocked(t *testing.T, fab *fabric.Fabric, portName string) bool {
	t.Helper()

	now := fab.Snapshot().Clock.Add(time.Millisecond)
	src := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x30}
	dst := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x31}
	fid, err := fab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "sw1", Port: portName},
		Frame:  ethernet.Frame{Dst: dst, Src: src, EtherType: 0x0800, Payload: []byte{9, 9, 9}},
	})
	if err != nil {
		t.Fatalf("Inject on %s: %v", portName, err)
	}
	runToQuiescence(t, fab, 50)

	for _, j := range fab.Report() {
		if j.FrameID != fid {
			continue
		}
		for _, e := range j.Entries {
			// Only the ingress-level drop, on the injected port itself,
			// proves the gate denied that port. An egress-level drop on
			// the *other* looped port, recorded while this frame floods
			// out of a still-admitted ingress, is a different thing.
			if e.Kind == fabric.EntryDrop && e.Port == portName && e.Reason == bridge.ReasonPortBlocked {
				return true
			}
		}
	}
	return false
}

// loopProtectBlockedPort reports which of two ports, if either,
// loopProtectPortBlocked finds denied. It returns empty strings when neither
// port is denied, which is also what a regression that silences both ports'
// denial would produce, so a caller that needs to tell those two cases apart
// checks loopProtectPortBlocked per port directly instead.
func loopProtectBlockedPort(t *testing.T, fab *fabric.Fabric, a, b string) (blocked, clean string) {
	t.Helper()

	aBlocked := loopProtectPortBlocked(t, fab, a)
	bBlocked := loopProtectPortBlocked(t, fab, b)

	switch {
	case aBlocked && !bBlocked:
		return a, b
	case bBlocked && !aBlocked:
		return b, a
	default:
		return "", ""
	}
}

func traceLoopProtectBlockStepSeen(t *testing.T, fab *fabric.Fabric, portName string) bool {
	t.Helper()

	for _, j := range fab.Report() {
		for _, e := range j.Entries {
			if e.Result == nil {
				continue
			}
			for _, step := range e.Result.Steps {
				if step.RuleID != "loopprotect.port.block" {
					continue
				}
				if step.Subject.Kind == "port" && step.Subject.Key == portName {
					return true
				}
			}
		}
	}

	return false
}

// TestLoopProtectWithRSTPDetectsNothing is evidence that a port spanning tree
// holds discarding reports no loop. Two switches are joined by two parallel
// links, and both run RSTP and loop protection over the same two ports. RSTP
// converges the redundant path away, and no probe ever returns: a probe leaves
// only where the tree forwards the VLAN, so the discarding port sends none,
// and a probe that reaches a discarding port on the far side dies at its
// ingress gate. The only block anywhere in the fabric is the tree's own.
func TestLoopProtectWithRSTPDetectsNothing(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	newPorts := func() port.Table {
		return mustFabricPortTable(t,
			port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
			port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
			port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
		)
	}

	macSw1 := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x01, 0x01}
	macSw2 := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x01, 0x02}

	newLoopProtectCfg := func() *loopprotect.Config {
		return &loopprotect.Config{
			Interval: 5 * time.Second,
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block},
				"1/1/2": {Action: loopprotect.Block},
			},
		}
	}

	cfg := fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  newPorts(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 4096,
					Address:  macSw1,
					Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
				},
				LoopProtect: newLoopProtectCfg(),
			},
			"sw2": {
				Ports:  newPorts(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 8192,
					Address:  macSw2,
					Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
				},
				LoopProtect: newLoopProtectCfg(),
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	deadline := t0.Add(90 * time.Second)
	for steps := 0; steps < 20000; steps++ {
		if !fab.Snapshot().Clock.Before(deadline) {
			break
		}
		if _, ok := fab.Step(); !ok {
			break
		}
	}
	if err := fab.Err(); err != nil {
		t.Fatalf("fabric run fault: %v", err)
	}
	if fab.Snapshot().Clock.Before(deadline) {
		t.Fatalf("fabric queue drained at %v before reaching %v; scenario needs periodic STP hellos to keep advancing", fab.Snapshot().Clock, deadline)
	}

	// sw1 is the root: as root, its own designated vector always beats
	// whatever it hears back from sw2, so both of its ports stay Designated.
	// The redundant link is discarded on sw2's side instead, where one port
	// loses the root-port race to the other and blocks.
	snap := fab.Snapshot()
	blocked := 0
	for _, name := range []string{"1/1/1", "1/1/2"} {
		info := snap.Devices["sw2"].Roles[name]
		if info.State == stp.StateDiscarding && (info.Role == stp.RoleAlternate || info.Role == stp.RoleBackup) {
			blocked++
		}
	}
	if blocked != 1 {
		t.Fatalf("sw2 has %d blocked port(s) among its two loop ports, want exactly 1: %+v", blocked, snap.Devices["sw2"].Roles)
	}

	// Assert the state before the outcome: this precondition proves loop
	// protection actually ran and emitted probes before RSTP converged. Without
	// it, the absence loop below would pass just as well if loop protection
	// never probed at all, which is the exact regression this test needs to
	// catch.
	sawProbe := false
	for _, j := range fab.Report() {
		if !j.Protocol {
			continue
		}
		if _, err := loopprotect.Decode(j.Injection.Frame); err == nil {
			sawProbe = true
			break
		}
	}
	if !sawProbe {
		t.Fatalf("fabric never emitted a loop-protection probe, want at least one before asserting none returned")
	}

	for _, j := range fab.Report() {
		for _, e := range j.Entries {
			if e.Result == nil {
				continue
			}
			for _, step := range e.Result.Steps {
				if hasLoopProtectFact(step) {
					t.Fatalf("found a loop-protection decision fact after RSTP converged: %+v", step)
				}
			}
		}
	}
}

func hasLoopProtectFact(step trace.Step) bool {
	for _, facts := range [][]trace.Fact{step.Inputs, step.Outputs} {
		for _, fact := range facts {
			if fact != nil && fact.TypeID() == "vswitch.loopprotect_decision" {
				return true
			}
		}
	}
	return false
}

// TestLoopProtectInterVLANReturn proves the inter-VLAN finding: a switch
// sends its own probe on VID 10 out a port whose native VLAN is 10,
// and the self-loop cable delivers it untagged into a port whose native
// VLAN is 20, so the bridge classifies the return under VID 20. The
// interception's returned-probe fact must carry both VLANs, since that is
// the only place the inter-VLAN finding is visible.
func TestLoopProtectInterVLANReturn(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)

	cfg := fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: mustFabricPortTable(t,
					port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
					port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
				),
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/2": {PVID: &vid20, Untagged: []vlan.ID{20}},
						},
					},
				},
				LoopProtect: &loopprotect.Config{
					Interval: 5 * time.Second,
					Ports: map[string]loopprotect.Port{
						"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{10}},
						"1/1/2": {Action: loopprotect.Block, VLANs: []vlan.ID{20}},
					},
				},
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	runToQuiescence(t, fab, 200)

	found := false
	for _, j := range fab.Report() {
		for _, e := range j.Entries {
			if e.Result == nil {
				continue
			}
			for _, step := range e.Result.Steps {
				if step.RuleID != "loopprotect.probe.return" {
					continue
				}
				for _, fact := range step.Outputs {
					if fact == nil {
						continue
					}
					c := fact.Canonical()
					if containsAll(c, "sent_vid=10", "returned_vid=20", "inter_vlan=true") {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Fatalf("no probe-return fact recorded a VID 10 -> VID 20 inter-VLAN return")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
