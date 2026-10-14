package vswitch_test

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestPeekReadsFDBAtFrameTime(t *testing.T) {
	for _, priorAdvance := range []bool{false, true} {
		for _, age := range []time.Duration{299 * time.Second, 301 * time.Second} {
			t.Run(age.String()+"/advance="+strconv.FormatBool(priorAdvance), func(t *testing.T) {
				ports := mustTable(t, port.NewBuilder().Range("1/1/%d", 1, 3,
					port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
				sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})
				mustSwitchLearn(t, sw, []bridge.Seed{{MAC: macH1, Port: "1/1/1", LearnedAt: fixedTime}})
				now := fixedTime.Add(age)
				if priorAdvance {
					sw.Advance(now)
				}
				entries, counters := sw.Entries(), sw.RelayCounters()
				res := sw.Peek(now, "1/1/2", ethernet.Frame{Src: macH2, Dst: macH1})
				wantRule, wantPorts := bridge.RuleUnicastHit, []string{"1/1/1"}
				if age > 300*time.Second {
					wantRule, wantPorts = bridge.RuleUnicastMiss, []string{"1/1/1", "1/1/3"}
				}
				found := false
				for _, step := range res.Steps {
					if step.RuleID == wantRule {
						found = true
					}
				}
				var gotPorts []string
				for _, out := range res.Egress {
					if out.Dropped != "" {
						t.Errorf("Peek dropped egress: %+v", out)
					}
					gotPorts = append(gotPorts, out.Port)
				}
				if !found || !reflect.DeepEqual(gotPorts, wantPorts) {
					t.Errorf("Peek = %+v, want rule %s and ports %v", res, wantRule, wantPorts)
				}
				if !reflect.DeepEqual(sw.Entries(), entries) || sw.RelayCounters() != counters {
					t.Fatal("Peek changed entries or counters")
				}
			})
		}
	}
}
