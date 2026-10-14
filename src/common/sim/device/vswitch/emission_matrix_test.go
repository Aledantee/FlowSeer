package vswitch

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
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
	b.Add(port.Port{Name: "twin", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	var err error
	base.Ports, err = b.Build()
	if err != nil {
		t.Fatal(err)
	}
	vid := vlan.ID(10)
	// The twin carries the mirror output's configuration but is not reserved, so
	// whatever the mirror output must not receive, the twin must.
	for _, name := range []string{"monitor", "twin"} {
		base.Bridge.VLAN.Switchports[name] = bridge.Switchport{PVID: &vid, Untagged: []vlan.ID{10}}
		base.STP.Ports[name] = stp.Port{AdminEdge: true}
		base.LoopProtect.Ports[name] = loopprotect.Port{Action: loopprotect.Block, VLANs: []vlan.ID{10}}
	}
	l := base.LAG.LAGs["lag1"]
	l.LACP.Mode = lag.Active
	base.LAG.LAGs["lag1"] = l
	frames := forkMatrixFrames(t, base.MAC)
	unresolved := forkFilterFrame(t, base.MAC)
	unresolvedPayload, err := (ip.Header{Src: netip.MustParseAddr("10.0.10.51"), Dst: netip.MustParseAddr("10.0.10.77"), HopLimit: 64, Protocol: 17, V4: &ip.V4{}}).Encode([]byte("unresolved"))
	if err != nil {
		t.Fatal(err)
	}
	unresolved.Payload = unresolvedPayload
	// It arrives first so that no loop-protection probe has blocked its ingress.
	frames = append([]forkMatrixFrame{{"routed-unresolved", "1/1/1", unresolved}}, frames...)
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
			var twinBPDU, twinProbe, twinSolicit bool
			check := func(name string, out device.Output) {
				t.Helper()
				for _, em := range out.Emissions {
					switch em.Port {
					case "monitor":
						t.Errorf("%s emitted on mirror output: %+v", name, em)
					case "twin":
						if _, err := bpdu.Decode(em.Frame); err == nil {
							twinBPDU = true
						}
						if _, err := loopprotect.Decode(em.Frame); err == nil {
							twinProbe = true
						}
						if msg, err := arp.Decode(em.Frame); err == nil && msg.Operation == arp.Request && msg.TargetAddr == netip.MustParseAddr("10.0.10.77") {
							twinSolicit = true
						}
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
			if want := mask&1 != 0; twinBPDU != want {
				t.Errorf("twin received a BPDU = %v, want %v", twinBPDU, want)
			}
			if want := mask&4 != 0; twinProbe != want {
				t.Errorf("twin received a loop-protection probe = %v, want %v", twinProbe, want)
			}
			if want := mask&8 != 0; twinSolicit != want {
				t.Errorf("twin received the solicitation for the unresolved address = %v, want %v", twinSolicit, want)
			}
		})
	}
	if subsets != 16 || starts != 16 || frameChecks != 144 || spans != 16 {
		t.Fatalf("subsets/starts/frames/spans = %d/%d/%d/%d, want 16/16/144/16", subsets, starts, frameChecks, spans)
	}
	t.Logf("%d emission subsets, %d starts, %d frame checks, %d sixty-second advances", subsets, starts, frameChecks, spans)
}
