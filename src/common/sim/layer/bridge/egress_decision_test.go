package bridge_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestAbsentIngressRetainsCallerPort(t *testing.T) {
	br := mustNewBridge(t, bridge.Config{}, buildTestPorts(t, 2))
	_, res, ok := br.Ingress(testTime0, "absent", ethernet.Frame{Src: macA, Dst: macB}, true, true)
	if ok || res.Outcome != trace.Dropped || res.Reason != port.ReasonPortDown || res.Ingress != "absent" {
		t.Fatalf("Ingress = %t/%s/%s on %q, want false/Dropped/port-down on absent", ok, res.Outcome, res.Reason, res.Ingress)
	}
	if len(res.Steps) != 1 || res.Steps[0].Subject.Key != "absent" {
		t.Fatalf("Steps = %+v, want one refusal naming absent", res.Steps)
	}
}

func TestDownEgressDecisionOnUnicastAndFlood(t *testing.T) {
	for _, mode := range []string{"unicast", "flood", "blocked", "all-down", "outside-vlan", "unicast-outside-vlan"} {
		t.Run(mode, func(t *testing.T) {
			cfg := bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "ten", 20: "twenty"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
					"1/1/3": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				},
			}}
			if mode == "outside-vlan" {
				cfg.VLAN.Switchports["1/1/3"] = bridge.Switchport{PVID: mustVLAN(20), Untagged: []vlan.ID{20}}
			}
			if mode == "unicast-outside-vlan" {
				cfg.VLAN.Switchports["1/1/3"] = bridge.Switchport{PVID: mustVLAN(10)}
			}
			br := mustNewBridge(t, cfg, buildTestPorts(t, 3))
			if mode == "unicast" || mode == "unicast-outside-vlan" {
				mustLearn(t, br, []bridge.Seed{{FID: 10, MAC: macB, Port: "1/1/3", Lifetime: bridge.Static}})
			}
			if err := br.SetOperStatus("1/1/3", port.Down); err != nil {
				t.Fatal(err)
			}
			if mode == "all-down" {
				if err := br.SetOperStatus("1/1/2", port.Down); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "blocked" {
				br.SetGate(testGate{forwards: map[string]bool{"1/1/1": true}}, analysis.WholeScope())
			}
			dst := netaddr.MAC{255, 255, 255, 255, 255, 255}
			if mode == "unicast" || mode == "unicast-outside-vlan" {
				dst = macB
			}
			res := br.Forward(testTime0, "1/1/1", ethernet.Frame{Src: macA, Dst: dst, Payload: []byte("frame")})
			wantOutcome, wantReason := trace.Flooded, trace.Reason("")
			wantPort, wantDrop := "1/1/2", trace.Reason("")
			switch mode {
			case "unicast", "unicast-outside-vlan":
				wantOutcome, wantReason, wantPort, wantDrop = trace.Dropped, port.ReasonPortDown, "1/1/3", port.ReasonPortDown
			case "blocked":
				wantOutcome, wantReason, wantDrop = trace.Dropped, bridge.ReasonPortBlocked, bridge.ReasonPortBlocked
			case "all-down":
				wantOutcome, wantReason = trace.Dropped, bridge.ReasonNoEgress
			}
			if res.Outcome != wantOutcome || res.Reason != wantReason {
				t.Errorf("result = %s/%s, want %s/%s", res.Outcome, res.Reason, wantOutcome, wantReason)
			}
			if mode == "all-down" {
				if len(res.Egress) != 0 {
					t.Errorf("Egress = %+v, want none", res.Egress)
				}
			} else if len(res.Egress) != 1 || res.Egress[0].Port != wantPort || res.Egress[0].Dropped != wantDrop {
				t.Errorf("Egress = %+v, want %s/%s alone", res.Egress, wantPort, wantDrop)
			}
			count, downIndex, replicateIndex := 0, -1, -1
			for i, step := range res.Steps {
				if step.Op == trace.OpReplicate {
					replicateIndex = i
				}
				if step.Subject.Kind != "port" || step.Subject.Key != "1/1/3" {
					continue
				}
				count++
				downIndex = i
				if mode == "outside-vlan" {
					t.Errorf("outside VLAN port has step %+v", step)
					continue
				}
				if step.Layer != bridge.LayerName || step.RuleID != bridge.RulePortDown || step.Op != trace.OpDrop {
					t.Errorf("down step = %s/%s/%s, want relay/port-down/drop", step.Layer, step.RuleID, step.Op)
				}
				if len(step.Inputs) != 1 || step.Inputs[0].TypeID() != "port.forwarding" || step.Inputs[0].Canonical() != `name="1/1/3";present=true;kind="Physical";admin="Up";oper="Down";mtu=0;lag_parent="";eligible=false;reason="port-down"` {
					t.Errorf("Inputs = %+v, want forwarding fact", step.Inputs)
				}
				if len(step.Outputs) != 1 || step.Outputs[0].TypeID() != "bridge.egress_decision" || step.Outputs[0].Canonical() != `port="1/1/3";member="";fid=10;eligible=false;reason="port-down"` {
					t.Errorf("Outputs = %+v, want down egress decision", step.Outputs)
				}
			}
			if mode == "flood" && (downIndex < 0 || replicateIndex < 0 || downIndex >= replicateIndex) {
				t.Errorf("down step index = %d, replicate index = %d, want down before replication", downIndex, replicateIndex)
			}
			wantCount := 1
			if mode == "outside-vlan" {
				wantCount = 0
			}
			if count != wantCount {
				t.Errorf("1/1/3 steps = %d, want %d", count, wantCount)
			}
		})
	}
}

func TestEgressGateProtectedAndMTUPrecedence(t *testing.T) {
	for _, blocked := range []bool{true, false} {
		name := "protected-before-mtu"
		if blocked {
			name = "gate-before-protected"
		}
		t.Run(name, func(t *testing.T) {
			builder := port.NewBuilder()
			builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
			builder.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 1500})
			ports, err := builder.Build()
			if err != nil {
				t.Fatal(err)
			}
			br := mustNewBridge(t, bridge.Config{ProtectedPorts: []string{"1/1/1", "1/1/2"}}, ports)
			mustLearn(t, br, []bridge.Seed{{MAC: macB, Port: "1/1/2", Lifetime: bridge.Static}})
			if blocked {
				br.SetGate(testGate{forwards: map[string]bool{"1/1/1": true}}, analysis.WholeScope())
			}
			res := br.Forward(testTime0, "1/1/1", ethernet.Frame{Src: macA, Dst: macB, Payload: make([]byte, 1501)})
			wantReason, wantRule := bridge.ReasonProtected, bridge.RuleProtected
			if blocked {
				wantReason, wantRule = bridge.ReasonPortBlocked, bridge.RulePortBlocked
			}
			if res.Outcome != trace.Dropped || res.Reason != wantReason || len(res.Egress) != 1 || res.Egress[0].Port != "1/1/2" || res.Egress[0].Dropped != wantReason {
				t.Fatalf("Result = %+v, want Dropped/%s and one dropped Egress on 1/1/2", res, wantReason)
			}
			step := res.Steps[len(res.Steps)-1]
			if step.Layer != bridge.LayerName || step.RuleID != wantRule || step.Op != trace.OpDrop || step.Subject.Key != "1/1/2" {
				t.Errorf("last step = %+v, want relay/%s/drop on 1/1/2", step, wantRule)
			}
		})
	}
}
