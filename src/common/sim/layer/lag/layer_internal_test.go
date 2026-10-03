package lag

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func findPending(p []pending, member string) *pending {
	for i := range p {
		if p[i].Member == member {
			return &p[i]
		}
	}

	return nil
}

// TestPendingForEachCause is evidence that Info.pending reports a member for
// each of the three documented reasons it may still change state on its own.
func TestPendingForEachCause(t *testing.T) {
	t.Parallel()

	twoPortTable := func(t *testing.T) port.Table {
		t.Helper()
		b := port.NewBuilder()
		b.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("port.Build: %v", err)
		}
		return tbl
	}

	t.Run("link delay", func(t *testing.T) {
		t.Parallel()
		tbl := twoPortTable(t)
		mac := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
		cfg := Config{LAGs: map[string]LAG{"lag1": {UpDelay: 2 * time.Second}}}
		l, err := New(cfg, layer.Env{Ports: tbl, MAC: mac})
		if err != nil {
			t.Fatalf("lag.New: %v", err)
		}
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)

		found := findPending(l.Info("lag1").pending, "1/1/1")
		if found == nil || found.Cause != pendingLinkDelay || !found.At.Equal(t0.Add(2*time.Second)) {
			t.Fatalf("pending = %+v, want link-delay at %v", found, t0.Add(2*time.Second))
		}
	})

	t.Run("partner expired", func(t *testing.T) {
		t.Parallel()
		tbl := twoPortTable(t)
		mac := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a}
		cfg := Config{LAGs: map[string]LAG{"lag1": {LACP: LACPConfig{Mode: Active, Fast: true}}}}
		l, err := New(cfg, layer.Env{Ports: tbl, MAC: mac})
		if err != nil {
			t.Fatalf("lag.New: %v", err)
		}
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)
		l.Advance(t0.Add(3 * time.Second))

		found := findPending(l.Info("lag1").pending, "1/1/1")
		if found == nil || found.Cause != pendingPartnerExpired || !found.At.After(t0.Add(3*time.Second)) {
			t.Fatalf("pending = %+v, want partner-expired after t0+3s", found)
		}
	})

	t.Run("attached without synchronization", func(t *testing.T) {
		t.Parallel()
		tbl := twoPortTable(t)
		mac := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a}
		cfg := Config{LAGs: map[string]LAG{"lag1": {LACP: LACPConfig{Mode: Active, Fast: true}}}}
		l, err := New(cfg, layer.Env{Ports: tbl, MAC: mac})
		if err != nil {
			t.Fatalf("lag.New: %v", err)
		}
		t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		l.LinkChange(t0, "1/1/1", true)

		partner := lacp.Info{
			SystemPriority: 1,
			SystemID:       netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0b},
			Key:            1,
			PortPriority:   1,
			PortID:         1,
			State:          lacp.StateAggregation, // no StateSynchronization
		}
		l.Receive(t0, "1/1/1", lacp.PDU{Actor: partner})

		info := l.Info("lag1")
		found := findPending(info.pending, "1/1/1")
		if found == nil || found.Cause != pendingUnsynchronized {
			t.Fatalf("pending = %+v, want unsynchronized", found)
		}
		if len(info.Attached) != 1 || info.Attached[0] != "1/1/1" || len(info.Enabled) != 0 {
			t.Fatalf("Attached = %v, Enabled = %v, want attached and not enabled", info.Attached, info.Enabled)
		}
	})
}
