package lag

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func firstPending(p []pending) *pending {
	if len(p) > 0 {
		return &p[0]
	}

	return nil
}

// TestPendingForEachCause is evidence that Info.pending reports a member for
// each of the four documented reasons it may still change state on its own.
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

		found := firstPending(l.Info("lag1").pending)
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
		l.Advance(t0.Add(2 * time.Second))

		found := firstPending(l.Info("lag1").pending)
		if found == nil || found.Cause != pendingPartnerExpired || !found.At.Equal(t0.Add(3*time.Second)) {
			t.Fatalf("pending = %+v, want partner-expired at t0+3s", found)
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
		l.Advance(t0.Add(2 * time.Second))

		info := l.Info("lag1")
		found := firstPending(info.pending)
		if found == nil || found.Cause != pendingUnsynchronized {
			t.Fatalf("pending = %+v, want unsynchronized", found)
		}
		if len(info.Attached) != 1 || info.Attached[0] != "1/1/1" || len(info.Enabled) != 0 {
			t.Fatalf("Attached = %v, Enabled = %v, want attached and not enabled", info.Attached, info.Enabled)
		}
	})

	t.Run("aggregate wait", func(t *testing.T) {
		t.Parallel()
		tbl := twoPortTable(t)
		cfg := Config{LAGs: map[string]LAG{"lag1": {LACP: LACPConfig{Mode: Active}}}}
		l, err := New(cfg, layer.Env{Ports: tbl, MAC: netaddr.MAC{2, 0, 0, 0, 0, 10}})
		if err != nil {
			t.Fatalf("lag.New: %v", err)
		}
		t0 := time.Unix(1700000000, 0)
		l.LinkChange(t0, "1/1/1", true)
		l.Receive(t0, "1/1/1", lacp.PDU{Actor: lacp.Info{
			SystemPriority: 1,
			SystemID:       netaddr.MAC{2, 0, 0, 0, 0, 11},
			Key:            1,
			PortID:         9,
			State:          lacp.StateActive | lacp.StateAggregation | lacp.StateSynchronization,
		}, Partner: l.PortInfo("1/1/1").Actor})

		found := firstPending(l.Info("lag1").pending)
		if found == nil || found.Cause != pendingAggregateWait || !found.At.Equal(t0.Add(2*time.Second)) {
			t.Fatalf("pending = %+v, want aggregate-wait at t0+2s", found)
		}
		if next, ok := l.NextWake(); !ok || !next.Equal(t0.Add(2*time.Second)) {
			t.Fatalf("NextWake = (%v, %v), want t0+2s", next, ok)
		}
	})
}

// TestReceiveRequestsReselectionOnIdentityChange is evidence that Receive
// unselects a member to reselect when the partner's System, Key, Port, or
// Aggregation changes, and leaves it untouched when the state bits that do
// not form the LAG ID change.
func TestReceiveRequestsReselectionOnIdentityChange(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		change   func(*lacp.Info)
		reselect bool
	}{
		{name: "same identity"},
		{name: "system", change: func(i *lacp.Info) { i.SystemID[5]++ }, reselect: true},
		{name: "system priority", change: func(i *lacp.Info) { i.SystemPriority++ }, reselect: true},
		{name: "key", change: func(i *lacp.Info) { i.Key++ }, reselect: true},
		{name: "port", change: func(i *lacp.Info) { i.PortID++ }, reselect: true},
		{name: "port priority", change: func(i *lacp.Info) { i.PortPriority++ }, reselect: true},
		{name: "aggregation", change: func(i *lacp.Info) { i.State &^= lacp.StateAggregation }, reselect: true},
		{name: "other state bits", change: func(i *lacp.Info) { i.State ^= lacp.StateActive | lacp.StateShortTimeout | lacp.StateSynchronization }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tbl, err := port.NewBuilder().Add(port.Port{Name: "lag1", Kind: port.LAG}).
				Add(port.Port{Name: "a", Kind: port.Physical, LagParent: "lag1"}).Build()
			if err != nil {
				t.Fatal(err)
			}
			l, err := New(Config{LAGs: map[string]LAG{"lag1": {LACP: LACPConfig{Mode: Active, Fast: true}}}}, layer.Env{Ports: tbl})
			if err != nil {
				t.Fatal(err)
			}
			t0 := time.Unix(1700000000, 0)
			l.LinkChange(t0, "a", true)
			pdu := lacp.PDU{Actor: lacp.Info{SystemID: netaddr.MAC{2, 0, 0, 0, 0, 1}, SystemPriority: 1, Key: 7, PortID: 9, PortPriority: 1, State: lacp.StateActive | lacp.StateAggregation}}
			l.Receive(t0, "a", pdu)
			l.Advance(t0.Add(2 * time.Second))
			m := l.members["a"]
			if tc.change != nil {
				tc.change(&pdu.Actor)
			}
			l.Receive(t0.Add(3*time.Second), "a", pdu)
			reselected := m.mux == muxWaiting && !m.attached
			if m.status != Current || reselected != tc.reselect {
				t.Fatalf("status = %v, reselected = %t, want Current and %t", m.status, reselected, tc.reselect)
			}
			cloned := l.Clone()
			if (cloned.members["a"].mux == muxWaiting) != tc.reselect {
				t.Fatalf("clone mux waiting = %t, want %t", cloned.members["a"].mux == muxWaiting, tc.reselect)
			}
		})
	}
}

// TestDefaultedRequestsReselectionAfterLearnedPartner keeps a learned zero
// Partner attached, stops forwarding while Expired, and resumes forwarding
// after defaulting, while a different Partner re-enters fallback through Mux
// WAITING.
func TestDefaultedRequestsReselectionAfterLearnedPartner(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().Add(port.Port{Name: "lag1", Kind: port.LAG}).
		Add(port.Port{Name: "a", Kind: port.Physical, LagParent: "lag1"}).Build()
	if err != nil {
		t.Fatal(err)
	}
	l, err := New(Config{LAGs: map[string]LAG{"lag1": {LACP: LACPConfig{Mode: Active, Fast: true, Fallback: true}}}}, layer.Env{Ports: tbl})
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1700000000, 0)
	l.LinkChange(t0, "a", true)
	m := l.members["a"]
	l.Receive(t0, "a", lacp.PDU{
		Actor:   lacp.Info{State: lacp.StateSynchronization | lacp.StateCollecting},
		Partner: m.actor,
	})
	l.Advance(t0.Add(2 * time.Second))
	if !m.attached || !m.enabled {
		t.Fatalf("learned zero partner before defaulting: attached = %t, enabled = %t, want attached and enabled", m.attached, m.enabled)
	}
	l.Advance(t0.Add(3 * time.Second))
	if m.status != Expired || !m.attached || m.enabled {
		t.Fatalf("zero partner expiry: status = %v, attached = %t, enabled = %t, want Expired, attached, and disabled", m.status, m.attached, m.enabled)
	}
	l.Advance(t0.Add(6 * time.Second))
	if m.status != Defaulted || !m.attached || !m.enabled || m.mux == muxWaiting {
		t.Fatalf("zero partner defaulting: status = %v, mux = %v, attached = %t, enabled = %t, want Defaulted, attached, enabled, and not WAITING", m.status, m.mux, m.attached, m.enabled)
	}
	l.Receive(t0.Add(7*time.Second), "a", lacp.PDU{Actor: lacp.Info{SystemID: netaddr.MAC{2, 0, 0, 0, 0, 1}, PortID: 9, State: lacp.StateActive | lacp.StateAggregation}})
	l.Advance(t0.Add(9 * time.Second))
	if !m.attached {
		t.Fatal("learned member did not attach")
	}
	l.Advance(t0.Add(10 * time.Second))
	if m.status != Expired {
		t.Fatalf("expiry: status = %v, want Expired", m.status)
	}
	l.Advance(t0.Add(13 * time.Second))
	if m.status != Defaulted || !m.selected || m.mux != muxWaiting || m.attached || m.enabled || m.actor.State&lacp.StateSynchronization != 0 {
		t.Fatalf("learned partner defaulting: status = %v, selected = %t, mux = %v, attached = %t, enabled = %t, actor state = %#x, want Defaulted in WAITING with synchronization clear", m.status, m.selected, m.mux, m.attached, m.enabled, uint8(m.actor.State))
	}
	l.Advance(t0.Add(15 * time.Second))
	if !m.attached || !m.enabled || m.actor.State&lacp.StateSynchronization == 0 {
		t.Fatalf("fallback after learned partner defaulting: attached = %t, enabled = %t, actor state = %#x, want attached and enabled after aggregate wait", m.attached, m.enabled, uint8(m.actor.State))
	}
}
