package vswitch_test

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TestLoopProtectProbeNeedsProbeEtherType proves that a frame to the probe
// group is a probe only when its EtherType says so: a frame whose payload
// names this switch and its port but carries EtherType 0x0800 is no action
// and is relayed like any other frame to an unregistered group.
func TestLoopProtectProbeNeedsProbeEtherType(t *testing.T) {
	sw := loopProtectTestSwitch(t)
	now := fixedTime

	mac := sw.Config().MAC
	frame := mustEncodeProbe(t, loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "1/1/1"}, mac)
	frame.EtherType = 0x0800

	res := sw.Receive(now, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("outcome = %s, want %s", res.Outcome, trace.Flooded)
	}
	if traceHasRuleID(res.Steps, "loopprotect.probe.return") || traceHasRuleID(res.Steps, "loopprotect.port.block") {
		t.Errorf("a frame with EtherType 0x0800 took a loop-protection action: %+v", res.Steps)
	}
	if len(res.Egress) == 0 {
		t.Errorf("a frame with EtherType 0x0800 was not relayed: %+v", res)
	}
}

// TestLoopProtectOwnProbeConsumedNotFlooded proves that a probe this switch
// sent, returned by an unmanaged loop, is consumed by the loop-protection
// interception rather than relayed: the mechanism only works if the switch
// recognizes and acts on its own probe instead of flooding it like any other
// frame addressed to an unregistered multicast group.
func TestLoopProtectOwnProbeConsumedNotFlooded(t *testing.T) {
	sw := loopProtectTestSwitch(t)
	now := fixedTime

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "1/1/1"}
	frame := mustEncodeProbe(t, probe, mac)

	res := sw.Receive(now, "1/1/1", frame)
	if res.Outcome != trace.Consumed {
		t.Fatalf("own probe outcome = %s, want %s", res.Outcome, trace.Consumed)
	}
	if len(res.Egress) != 0 {
		t.Errorf("own probe produced %d egress transmissions, want 0 (consumed, not flooded)", len(res.Egress))
	}
	if !traceHasRuleID(res.Steps, "loopprotect.probe.return") {
		t.Errorf("own probe steps lack a probe-return step: %+v", res.Steps)
	}
	if !traceHasRuleID(res.Steps, "loopprotect.port.block") {
		t.Errorf("own probe steps lack a port-block step for the newly applied action: %+v", res.Steps)
	}
}

// TestLoopProtectForeignProbeFloodsWithOneClassificationStep proves that a
// probe naming another switch as origin is left untouched by the
// loop-protection interception and falls through to the ordinary relay
// path, where it floods as unregistered multicast. Classifying it once on
// the fall-through, rather than once in the interception and again on the
// fall-through, matters because a corpus comparing the ordered step list
// exactly would fail on a duplicate classification.
func TestLoopProtectForeignProbeFloodsWithOneClassificationStep(t *testing.T) {
	sw := loopProtectTestSwitch(t)
	now := fixedTime

	foreignMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}
	probe := loopprotect.Probe{OriginMAC: foreignMAC, VID: 0, Sequence: 1, Port: "1/1/1"}
	frame := mustEncodeProbe(t, probe, foreignMAC)

	res := sw.Receive(now, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("foreign probe outcome = %s, want %s", res.Outcome, trace.Flooded)
	}

	classifications := 0
	for _, step := range res.Steps {
		if step.Op == trace.OpClassify {
			classifications++
		}
	}
	if classifications != 1 {
		t.Errorf("foreign probe produced %d classification steps, want exactly 1: %+v", classifications, res.Steps)
	}
	if traceHasFactType(res.Steps, "loopprotect.probe") || traceHasFactType(res.Steps, "loopprotect.probe_return") || traceHasFactType(res.Steps, "loopprotect.port_transition") {
		t.Errorf("foreign probe steps carry a loop-protection decision: %+v", res.Steps)
	}
}

// TestLoopProtectGatedPortDropsBeforeDetection proves the mechanism that
// keeps a two-port loop from blocking both ports: once a port's action is
// applied, the bridge's ordinary ingress gate denies both learning and
// forwarding on it, so a probe arriving there next is dropped by the gate
// before the interception ever calls Receive again.
func TestLoopProtectGatedPortDropsBeforeDetection(t *testing.T) {
	sw := loopProtectTestSwitch(t)
	now := fixedTime

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "1/1/1"}
	frame := mustEncodeProbe(t, probe, mac)

	first := sw.Receive(now, "1/1/1", frame)
	if first.Outcome != trace.Consumed {
		t.Fatalf("first probe outcome = %s, want %s", first.Outcome, trace.Consumed)
	}

	second := sw.Receive(now, "1/1/1", frame)
	if second.Outcome != trace.Dropped || second.Reason != bridge.ReasonPortBlocked {
		t.Fatalf("second probe on the now-blocked port = %s/%s, want %s/%s",
			second.Outcome, second.Reason, trace.Dropped, bridge.ReasonPortBlocked)
	}
	if traceHasRuleID(second.Steps, "loopprotect.probe.return") {
		t.Errorf("second probe ran detection after the port was already blocked: %+v", second.Steps)
	}
}

// TestLoopProtectManualClearsOnLAGPortCycle proves that a Manual recovery
// applied to a protected LAG port clears when the LAG's link cycles down and
// back up, the same way it clears on a physical port: updateLagState now
// notifies loop protection of the LAG's aggregate link state, not just
// spanning tree, so a LAG-only switch's protected port is not stuck applied
// forever once blocked.
func TestLoopProtectManualClearsOnLAGPortCycle(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {Mode: lag.BalanceSLB, Members: map[string]lag.Member{"1/1/1": {}, "1/1/2": {}}},
		}},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"lag1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
			},
		},
	})

	now := fixedTime
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "lag1"}
	res := captureOutput(callOutputs, sw, sw.Receive(now, "lag1", mustEncodeProbe(t, probe, mac)))
	if res.Outcome != trace.Consumed {
		t.Fatalf("probe outcome = %s, want %s", res.Outcome, trace.Consumed)
	}

	other := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x55}
	broadcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	data := ethernet.Frame{Dst: broadcast, Src: other, EtherType: 0x0800, Payload: []byte{1, 2, 3}}

	blocked := captureOutput(callOutputs, sw, sw.Receive(now, "lag1", data))
	if blocked.Outcome != trace.Dropped || blocked.Reason != bridge.ReasonPortBlocked {
		t.Fatalf("frame on lag1 after Block applied = %s/%s, want %s/%s",
			blocked.Outcome, blocked.Reason, trace.Dropped, bridge.ReasonPortBlocked)
	}
	captureLinkOutput(t, callOutputs, sw, now, "1/1/1", port.Down)
	captureLinkOutput(t, callOutputs, sw, now, "1/1/2", port.Down)
	captureLinkOutput(t, callOutputs, sw, now, "1/1/1", port.Up)
	captureLinkOutput(t, callOutputs, sw, now, "1/1/2", port.Up)

	// lag1 is the fixture's only non-member port, so an unblocked broadcast
	// off it has nowhere left to flood to: the positive outcome here is
	// Dropped/no-egress, not the port-blocked drop the precondition proved.
	after := captureOutput(callOutputs, sw, sw.Receive(now, "lag1", data))
	if after.Outcome != trace.Dropped || after.Reason != bridge.ReasonNoEgress {
		t.Fatalf("frame on lag1 after a port cycle = %s/%s, want %s/%s: the Manual action should have cleared",
			after.Outcome, after.Reason, trace.Dropped, bridge.ReasonNoEgress)
	}
}

// TestLoopProtectTrunkWithPVIDEmitsAProbe proves that a protected trunk port
// with no configured VLANs still probes on a VLAN-aware bridge, once it
// carries a PVID: the layer emits VID 0 for such a port, and the switch
// resolves that to the port's PVID before asking the bridge to originate the
// frame, so the probe is not silently discarded for naming a VID the bridge
// does not admit.
func TestLoopProtectTrunkWithPVIDEmitsAProbe(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	pvid := vlan.ID(10)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "a", 20: "b"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{10}, Tagged: []vlan.ID{20}},
			},
		}},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block},
			},
		},
	})

	now := fixedTime
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)
	captureOutput(callOutputs, sw, sw.Advance(now.Add(loopprotect.DefaultInterval)))
	emissions := takeEmissions(callOutputs, sw)

	found := false
	for _, em := range emissions {
		if em.Port != "1/1/1" {
			continue
		}
		probe, err := loopprotect.Decode(em.Frame)
		if err == nil {
			if probe.VID != 10 {
				t.Fatalf("probe VID = %d, want port PVID 10", probe.VID)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("emissions after Wake = %+v, want a decodable loop-protection probe on 1/1/1", emissions)
	}
}

func TestLoopProtectProbeWithNoConfiguredVLANCarriesPortPVID(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	pvid := vlan.ID(10)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "a", 20: "b"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{10}, Tagged: []vlan.ID{20}},
			},
		}},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block},
			},
		},
	})

	now := fixedTime
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)
	captureOutput(callOutputs, sw, sw.Advance(now.Add(loopprotect.DefaultInterval)))
	emissions := takeEmissions(callOutputs, sw)

	found := false
	for _, em := range emissions {
		if em.Port != "1/1/1" {
			continue
		}
		probe, err := loopprotect.Decode(em.Frame)
		if err != nil {
			continue
		}
		if probe.VID != 10 {
			t.Fatalf("probe VID = %d, want port PVID 10", probe.VID)
		}
		found = true
	}
	if !found {
		t.Fatalf("emissions after Wake = %+v, want a decodable loop-protection probe on 1/1/1", emissions)
	}
}

// loopProtectReturnFactString locates the "loopprotect.probe.return" step in
// res's trace and returns its output fact's canonical string, or "" if the
// step is not present.
func loopProtectReturnFactString(t *testing.T, res vswitch.Output) string {
	t.Helper()

	for _, step := range res.Steps {
		if step.RuleID == "loopprotect.probe.return" && len(step.Outputs) > 0 {
			return step.Outputs[0].Canonical()
		}
	}

	return ""
}

// TestLoopProtectAccessPortReturnedProbeIsNotInterVLAN proves that a probe
// emitted on a port with no configured VLANs (VID 0, resolved to the port's
// PVID) does not falsely report an inter-VLAN loop when it returns on a
// VLAN-aware bridge. It drives the real emission path (Wake, Drain, then
// Forward with the frame the switch actually put on the wire): the switch
// must re-encode the probe's payload with the resolved VID before
// transmitting, so the payload names the VLAN the frame rides and a return
// classified into that VLAN is not inter-VLAN.
func TestLoopProtectAccessPortReturnedProbeIsNotInterVLAN(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	pvid := vlan.ID(10)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "a"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{10}},
			},
		}},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				// No VLANs configured: Wake emits VID 0 for this port.
				"1/1/1": {Action: loopprotect.Block},
			},
		},
	})

	now := fixedTime
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)
	captureOutput(callOutputs, sw, sw.Advance(now.Add(loopprotect.DefaultInterval)))
	emissions := takeEmissions(callOutputs, sw)

	var probeFrame ethernet.Frame
	found := false
	for _, em := range emissions {
		if em.Port != "1/1/1" {
			continue
		}
		if _, err := loopprotect.Decode(em.Frame); err == nil {
			probeFrame = em.Frame
			found = true
		}
	}
	if !found {
		t.Fatalf("emissions after Wake = %+v, want a decodable loop-protection probe on 1/1/1", emissions)
	}

	// An unmanaged loop hands the same probe back on the port it left from.
	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", probeFrame))
	if res.Outcome != trace.Consumed {
		t.Fatalf("returned probe Outcome = %s, want %s", res.Outcome, trace.Consumed)
	}

	factStr := loopProtectReturnFactString(t, res)
	if factStr == "" {
		t.Fatalf("trace has no loopprotect.probe.return step: %+v", res.Steps)
	}
	if !strings.Contains(factStr, "sent_vid=10;returned_vid=10") {
		t.Errorf("loopprotect.probe.return fact = %q, want sent_vid and returned_vid to agree at 10", factStr)
	}
	if !strings.Contains(factStr, "after={action=\"Block\";inter_vlan=false") {
		t.Errorf("loopprotect.probe.return fact = %q, want after.inter_vlan=false", factStr)
	}
}

// TestLoopProtectCrossVLANReturnedProbeIsInterVLAN proves the genuine
// positive this package must still detect: a probe sent on one VLAN that
// returns classified into a different VLAN is a real inter-VLAN loop. The
// probe here is built and fed back by hand naming VID 10 as the VLAN it was
// sent on, then delivered untagged so the port's own PVID (20) classifies
// the return into a different VLAN, reproducing what a real inter-VLAN loop
// looks like on the wire.
func TestLoopProtectCrossVLANReturnedProbeIsInterVLAN(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	pvid := vlan.ID(20)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "a", 20: "b"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{20}, Tagged: []vlan.ID{10}},
			},
		}},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{10}},
			},
		},
	})

	now := fixedTime
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 10, Sequence: 1, Port: "1/1/1"}
	// Delivered untagged, so bridge ingress classifies it by the port's
	// PVID (20) rather than by the VID 10 the payload names as sent.
	frame := mustEncodeProbe(t, probe, mac)

	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", frame))
	if res.Outcome != trace.Consumed {
		t.Fatalf("returned probe Outcome = %s, want %s", res.Outcome, trace.Consumed)
	}

	factStr := loopProtectReturnFactString(t, res)
	if factStr == "" {
		t.Fatalf("trace has no loopprotect.probe.return step: %+v", res.Steps)
	}
	if !strings.Contains(factStr, "sent_vid=10;returned_vid=20") {
		t.Fatalf("loopprotect.probe.return fact = %q, want sent_vid=10;returned_vid=20", factStr)
	}
	if !strings.Contains(factStr, "inter_vlan=true") {
		t.Errorf("loopprotect.probe.return fact = %q, want inter_vlan=true", factStr)
	}
}

// TestLoopProtectAsymmetricVLANReturnedProbeIsNotInterVLAN proves the false
// positive this package must not report: a port that is an untagged member
// of two VLANs (the "asymmetric VLAN" shared-uplink configuration IEEE
// 802.1Q permits, since untagged egress is a per-VLAN port set while ingress
// classification is a per-port PVID scalar) sends a probe on one of those
// VLANs and gets it back classified into the other by its own PVID. Nothing
// bridged the two VLANs: the numbers are local to this port on either side
// of an untagged wire. The loop itself is still real, so the action must
// still apply; only the VLAN finding is false.
func TestLoopProtectAsymmetricVLANReturnedProbeIsNotInterVLAN(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	pvid := vlan.ID(10)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "a", 20: "b"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{10, 20}},
			},
		}},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{20}},
			},
		},
	})

	now := fixedTime
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 20, Sequence: 1, Port: "1/1/1"}
	// Delivered untagged, so bridge ingress classifies it by the port's
	// PVID (10) even though the port also untags egress for VLAN 20.
	frame := mustEncodeProbe(t, probe, mac)

	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", frame))
	if res.Outcome != trace.Consumed {
		t.Fatalf("returned probe Outcome = %s, want %s", res.Outcome, trace.Consumed)
	}

	factStr := loopProtectReturnFactString(t, res)
	if factStr == "" {
		t.Fatalf("trace has no loopprotect.probe.return step: %+v", res.Steps)
	}
	if !strings.Contains(factStr, "sent_vid=20;returned_vid=10") {
		t.Fatalf("loopprotect.probe.return fact = %q, want sent_vid=20;returned_vid=10", factStr)
	}
	if !strings.Contains(factStr, "after={action=\"Block\";inter_vlan=false") {
		t.Errorf("loopprotect.probe.return fact = %q, want after.inter_vlan=false with the Block action still applied", factStr)
	}
}

// TestLoopProtectValidateAgreesWithEgressAdmission is evidence that
// vswitch.Config.Validate and bridge.Switchport.CarriesVID never disagree
// about whether a protected port carries the VLAN it probes: for every
// switchport shape below, either Validate refuses the configuration or a
// probe for that port actually reaches the switch's emitted frames after a
// Wake, never neither. A PVID by itself does not admit a VID on egress (only
// Tagged, Untagged, or a tunnel's VID does), so a config that only checked
// PVID at validation time could accept a setup that then emits nothing.
func TestLoopProtectValidateAgreesWithEgressAdmission(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	vid10 := vlan.ID(10)

	newTable := func(names ...string) port.Table {
		b := port.NewBuilder()
		for _, name := range names {
			b = b.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		}
		return mustTable(t, b)
	}

	tests := []struct {
		name       string
		cfg        vswitch.Config
		wantRefuse bool
	}{
		{
			name: "VLAN-unaware bridge probes untagged",
			cfg: vswitch.Config{
				Ports:  newTable("1/1/1"),
				Bridge: &bridge.Config{},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block}},
				},
			},
		},
		{
			name: "access port with PVID in Untagged",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "a"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block}},
				},
			},
		},
		{
			name: "trunk with VLAN in Tagged and explicit VLANs",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "a", 20: "b"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Tagged: []vlan.ID{20}},
					},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{
						"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{20}},
					},
				},
			},
		},
		{
			name: "trunk PVID in neither list, no explicit VLANs",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "a", 20: "b"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Tagged: []vlan.ID{20}},
					},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block}},
				},
			},
			wantRefuse: true,
		},
		{
			name: "trunk PVID in neither list, with explicit VLANs naming the PVID",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "a", 20: "b"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Tagged: []vlan.ID{20}},
					},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{
						"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{10}},
					},
				},
			},
			wantRefuse: true,
		},
		{
			name: "tunnel switchport with no PVID and no explicit VLANs",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "a"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Tunnel: &bridge.Tunnel{VID: 10}},
					},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block}},
				},
			},
		},
		{
			name: "protected port with no switchport entry at all",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table:       map[vlan.ID]string{10: "a"},
					Switchports: map[string]bridge.Switchport{},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block}},
				},
			},
			wantRefuse: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sw, err := vswitch.New(tt.cfg)
			if tt.wantRefuse {
				if err == nil {
					t.Fatalf("vswitch.New = nil error, want the configuration refused")
				}
				return
			}
			if err != nil {
				t.Fatalf("vswitch.New: %v", err)
			}

			now := fixedTime
			captureOutput(callOutputs, sw, sw.Start(now))
			takeEmissions(callOutputs, sw)
			captureOutput(callOutputs, sw, sw.Advance(now.Add(loopprotect.DefaultInterval)))
			emissions := takeEmissions(callOutputs, sw)

			found := false
			for _, em := range emissions {
				if em.Port != "1/1/1" {
					continue
				}
				if _, err := loopprotect.Decode(em.Frame); err == nil {
					found = true
				}
			}
			if !found {
				t.Fatalf("emissions after Wake = %+v, want a decodable loop-protection probe on 1/1/1", emissions)
			}
		})
	}
}

// TestLoopProtectAppliedActionFlushesLearnedEntries proves that applying a
// loop-protection action flushes the forwarding entries the loop taught the
// blocked port: without the flush, a later unicast to that host keeps
// following the stale entry into the now-blocked port and is dropped as
// port-blocked instead of flooding to relocate it.
func TestLoopProtectAppliedActionFlushesLearnedEntries(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
			},
		},
	})
	now := fixedTime

	host := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x42}
	mustSwitchLearn(t, sw, []bridge.Seed{
		{FID: 0, MAC: host, Port: "1/1/1", LearnedAt: now},
	})

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "1/1/1"}
	sw.Receive(now, "1/1/1", mustEncodeProbe(t, probe, mac))

	other := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x55}
	data := ethernet.Frame{Dst: host, Src: other, EtherType: 0x0800, Payload: []byte{1, 2, 3}}

	res := sw.Receive(now, "1/1/2", data)
	if res.Outcome != trace.Flooded {
		t.Fatalf("unicast to the host learned through the loop = %s, want %s (stale entry flushed)", res.Outcome, trace.Flooded)
	}
	floodedTo13 := false
	for _, eg := range res.Egress {
		if eg.Port == "1/1/3" {
			floodedTo13 = true
		}
	}
	if !floodedTo13 {
		t.Errorf("flood egress = %+v, want 1/1/3 among the targets", res.Egress)
	}
}

func traceHasRuleID(steps []trace.Step, ruleID trace.RuleID) bool {
	for _, step := range steps {
		if step.RuleID == ruleID {
			return true
		}
	}

	return false
}
