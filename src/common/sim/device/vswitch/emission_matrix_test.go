package vswitch

import (
	"fmt"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestEmissionCapabilityMatrix(t *testing.T) {
	base := newTestSwitchForFork(t).Spec().Config
	base.Phy, base.Mcast, base.Filter = nil, nil, nil
	base.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{Name: "span", SelectAll: true, OutputPort: "monitor"}}}
	b := port.NewBuilder()
	for _, p := range base.Ports.Ports() {
		b.Add(p)
	}
	b.Add(port.Port{Name: "monitor", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	var err error
	base.Ports, err = b.Build()
	if err != nil {
		t.Fatal(err)
	}
	vid := vlan.ID(10)
	base.Bridge.VLAN.Switchports["monitor"] = bridge.Switchport{PVID: &vid, Untagged: []vlan.ID{10}}
	base.STP.Ports["monitor"] = stp.Port{AdminEdge: true}
	base.LoopProtect.Ports["monitor"] = loopprotect.Port{Action: loopprotect.Block, VLANs: []vlan.ID{10}}
	l := base.LAG.LAGs["lag1"]
	l.LACP.Mode = lag.Active
	base.LAG.LAGs["lag1"] = l
	frames := forkMatrixFrames(t, base.MAC)
	subsets, starts, frameChecks, spans := 0, 0, 0, 0
	for mask := range 16 {
		t.Run(fmt.Sprintf("subset_%02x", mask), func(t *testing.T) {
			cfg := base.Clone()
			if mask&1 == 0 {
				cfg.STP = nil
			}
			if mask&2 == 0 {
				cfg.LAG = nil
				builder := port.NewBuilder()
				for _, p := range cfg.Ports.Ports() {
					if p.Kind != port.LAG && p.LagParent == "" {
						builder.Add(p)
					}
				}
				cfg.Ports, err = builder.Build()
				if err != nil {
					t.Fatal(err)
				}
				delete(cfg.Bridge.VLAN.Switchports, "lag1")
			}
			if mask&4 == 0 {
				cfg.LoopProtect = nil
			}
			if mask&8 == 0 {
				cfg.Routing = nil
			}
			sw, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			subsets++
			check := func(name string, out Output) {
				t.Helper()
				for _, em := range out.Emissions {
					if em.Port == "monitor" {
						t.Errorf("%s emitted on mirror output: %+v", name, em)
					}
				}
			}
			now := time.Unix(1000, 0)
			check("start", sw.Start(now))
			starts++
			for _, f := range frames {
				check(f.name, sw.Receive(now, f.ingress, f.frame))
				frameChecks++
			}
			end := now.Add(60 * time.Second)
			for {
				wake, ok := sw.NextWake()
				if !ok || wake.After(end) {
					break
				}
				if !wake.After(now) {
					t.Fatalf("wake %v did not advance past %v", wake, now)
				}
				now = wake
				check("wake", sw.Advance(now))
			}
			check("sixty seconds", sw.Advance(end))
			spans++
		})
	}
	if subsets != 16 || starts != 16 || frameChecks != 128 || spans != 16 {
		t.Fatalf("subsets/starts/frames/spans = %d/%d/%d/%d, want 16/16/128/16", subsets, starts, frameChecks, spans)
	}
	t.Logf("%d emission subsets, %d starts, %d frame checks, %d sixty-second advances", subsets, starts, frameChecks, spans)
}
