package vswitch

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestDeriveCapabilityMatrix(t *testing.T) {
	const (
		withPhy = 1 << iota
		withLAG
		withSTP
		withLoopProtect
		withMcast
		withRouting
		withFilter
		withTraffic
	)
	base := newTestSwitchForFork(t).Spec().Config
	frames := forkMatrixFrames(t, base.MAC)
	built, skipped, comparisons := 0, 0, 0
	for mask := range 256 {
		if mask&withFilter != 0 && mask&withRouting == 0 {
			skipped++
			continue
		}
		t.Run(fmt.Sprintf("capabilities_%02x", mask), func(t *testing.T) {
			cfg := base.Clone()
			if mask&withPhy == 0 {
				cfg.Phy = nil
			}
			if mask&withLAG == 0 {
				cfg.LAG = nil
				builder := port.NewBuilder()
				for _, p := range cfg.Ports.Ports() {
					if p.Kind != port.LAG && p.LagParent == "" {
						builder.Add(p)
					}
				}
				var err error
				cfg.Ports, err = builder.Build()
				if err != nil {
					t.Fatal(err)
				}
				delete(cfg.Bridge.VLAN.Switchports, "lag1")
			}
			if mask&withSTP == 0 {
				cfg.STP = nil
			}
			if mask&withLoopProtect == 0 {
				cfg.LoopProtect = nil
			}
			if mask&withMcast == 0 {
				cfg.Mcast = nil
			}
			if mask&withRouting == 0 {
				cfg.Routing = nil
			}
			if mask&withFilter == 0 {
				cfg.Filter = nil
			}
			if mask&withTraffic == 0 {
				cfg.Traffic = nil
			}
			source, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}

			built++
			now := time.Unix(1000, 0)
			source.Start(now)
			for i, probe := range frames {
				source.Receive(now.Add(time.Duration(i+1)*time.Second), probe.ingress, probe.frame)
			}
			fork := source.Fork().(*Switch)
			derived, _, err := Derive(source, source.Spec(), now)
			if err != nil {
				t.Fatal(err)
			}
			ret := reflect.ValueOf(derived.Retention())
			for i := range ret.NumField() {
				got := ret.Field(i).Interface().(LayerRetention)
				if !got.Kept || got.Difference != "" {
					t.Errorf("%s retention = %+v, want kept", ret.Type().Field(i).Name, got)
				}
			}
			for i, probe := range frames {
				at := now.Add(time.Duration(i+9) * time.Second)
				got, want := derived.Receive(at, probe.ingress, probe.frame), fork.Receive(at, probe.ingress, probe.frame)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: derived output = %+v, fork output = %+v", probe.name, got, want)
				}
				comparisons++
			}
		})
	}
	if built != 192 || skipped != 64 || comparisons != 1536 {
		t.Fatalf("built/skipped/comparisons = %d/%d/%d, want 192/64/1536", built, skipped, comparisons)
	}
	t.Logf("%d derives, %d skipped subsets, %d output comparisons", built, skipped, comparisons)
}
