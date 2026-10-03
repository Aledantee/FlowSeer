package vswitch

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestSwitchSpeedsInternal(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	pCfg := &phy.Config{
		Ethernet: map[string]phy.Ethernet{
			"1/1/1": {SupportedSpeedsBPS: []uint64{1_000_000_000}},
		},
	}

	sw, err := New(Config{Ports: ports, Phy: pCfg, Bridge: &bridge.Config{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if sw.speeds == nil || len(sw.speeds) != 1 {
		t.Errorf("got resolved speeds %v, want exactly 1", sw.speeds)
	}
}

func TestLinkChangeRecordsInvalidOperStatusAsFault(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	sw, err := New(Config{Ports: ports, Bridge: &bridge.Config{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sw.LinkChange(now, "1/1/1", port.LinkState("bogus"), PointToPointTrue, 1_000_000_000)

	first := sw.operErr
	if first == nil {
		t.Fatal("sw.operErr = nil after an invalid oper status, want a fault")
	}
	if got := errs.Attributes(first)["name"]; got != "1/1/1" {
		t.Errorf("fault names port %v, want 1/1/1", got)
	}

	sw.LinkChange(now.Add(time.Second), "1/1/2", port.LinkState("worse"), PointToPointTrue, 1_000_000_000)
	if sw.operErr != first {
		t.Errorf("sw.operErr = %v after a second fault, want the first %v", sw.operErr, first)
	}
}

func TestLinkChangeValidTransitionRecordsNoFault(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	sw, err := New(Config{Ports: ports, Bridge: &bridge.Config{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sw.LinkChange(now, "1/1/1", port.Down, PointToPointTrue, 1_000_000_000)

	if err := sw.operErr; err != nil {
		t.Errorf("sw.operErr = %v after a valid transition, want nil", err)
	}
}

func TestReleaseHeldFrameOntoDownPortRecordsPortStatusDown(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	macRouter := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	sw, err := New(Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"rp1": {Port: "1/1/1", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.1.1/24")}},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	hf := routing.HeldFrame{
		Interface: "rp1",
		Port:      "1/1/1",
		Cause:     routing.HeldReleased,
		Frame:     ethernet.Frame{Payload: make([]byte, 100)},
	}

	sw.releaseHeldFrame(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), hf)

	drops := sw.DrainNeighborFailures()
	if len(drops) != 1 {
		t.Fatalf("DrainNeighborFailures() returned %d drops, want 1", len(drops))
	}
	if drops[0].Reason != port.ReasonPortDown {
		t.Errorf("drop reason = %v, want %v", drops[0].Reason, port.ReasonPortDown)
	}
	if drops[0].Port != "1/1/1" {
		t.Errorf("drop port = %q, want 1/1/1", drops[0].Port)
	}
	if got, want := drops[0].Step.RuleID, trace.RuleID("port.status.port-down"); got != want {
		t.Errorf("drop step RuleID = %q, want literal %q", got, want)
	}
}
