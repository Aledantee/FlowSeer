package lag_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
)

func TestPeriodicUsesPartnerTimeout(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		fast   bool
		peer   lacp.State
		period time.Duration
	}{
		{name: "slow Actor, Short Partner", peer: lacp.StateShortTimeout, period: time.Second},
		{name: "fast Actor, Long Partner", fast: true, period: 30 * time.Second},
		{name: "slow Actor, Long Partner", period: 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Active, Fast: tc.fast}}}}, lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0a"))
			t0 := time.Unix(1700000000, 0)
			if fx := l.LinkChange(t0, "1/1/1", true); len(fx.Emissions) != 1 {
				t.Fatalf("carrier-up emissions = %d, want 1", len(fx.Emissions))
			}
			pdu := lacp.PDU{Actor: lacp.Info{SystemID: mustMAC(t, "02:00:00:00:00:0b"), PortID: 9, State: lacp.StateActive | tc.peer}, Partner: l.PortInfo("1/1/1").Actor}
			l.Receive(t0, "1/1/1", pdu)
			l.Advance(t0.Add(2 * time.Second))
			if info := l.PortInfo("1/1/1"); info.Status != lag.Current || !info.Attached {
				t.Fatalf("member = %+v, want Current and attached", info)
			}
			pdu.Partner = l.PortInfo("1/1/1").Actor
			before := l.PortInfo("1/1/1").LACPDUsTx
			for sec := 3; sec <= 30; sec++ {
				now := t0.Add(time.Duration(sec) * time.Second)
				l.Receive(now.Add(-time.Nanosecond), "1/1/1", pdu)
				fx := l.Advance(now)
				want := 0
				if tc.period == time.Second || sec == 30 {
					want = 1
				}
				if len(fx.Emissions) != want {
					t.Fatalf("periodic emissions at %ds = %d, want %d", sec, len(fx.Emissions), want)
				}
			}
			want := uint64(1)
			if tc.period == time.Second {
				want = 28
			}
			if got := l.PortInfo("1/1/1").LACPDUsTx - before; got != want {
				t.Fatalf("periodic transmit count = %d, want %d", got, want)
			}
		})
	}
}

func TestPeriodicTimeoutTransitions(t *testing.T) {
	t.Parallel()

	l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Active}}}}, lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0a"))
	t0 := time.Unix(1700000000, 0)
	l.LinkChange(t0, "1/1/1", true)
	pdu := lacp.PDU{Actor: lacp.Info{SystemID: mustMAC(t, "02:00:00:00:00:0b"), PortID: 9, State: lacp.StateActive}, Partner: l.PortInfo("1/1/1").Actor}
	l.Receive(t0, "1/1/1", pdu)
	l.Advance(t0.Add(2 * time.Second))
	pdu.Partner = l.PortInfo("1/1/1").Actor
	pdu.Actor.State |= lacp.StateShortTimeout
	if fx := l.Receive(t0.Add(3*time.Second), "1/1/1", pdu); len(fx.Emissions) != 1 {
		t.Fatalf("Long-to-Short emissions = %d, want 1", len(fx.Emissions))
	}
	if next, ok := l.NextWake(); !ok || !next.Equal(t0.Add(4*time.Second)) {
		t.Fatalf("Short NextWake = (%v, %v), want t0+4s", next, ok)
	}
	pdu.Actor.State &^= lacp.StateShortTimeout
	if fx := l.Receive(t0.Add(3500*time.Millisecond), "1/1/1", pdu); len(fx.Emissions) != 0 {
		t.Fatalf("Short-to-Long emissions = %d, want 0", len(fx.Emissions))
	}
	if next, ok := l.NextWake(); !ok || !next.Equal(t0.Add(33500*time.Millisecond)) {
		t.Fatalf("Long NextWake = (%v, %v), want t0+33.5s", next, ok)
	}
	if fx := l.Advance(t0.Add(4 * time.Second)); len(fx.Emissions) != 0 {
		t.Fatalf("canceled fast timer emissions = %d, want 0", len(fx.Emissions))
	}
}

func TestReceiveAnswersStaleEcho(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		change func(*lacp.Info)
		want   int
	}{
		{name: "system", change: func(i *lacp.Info) { i.SystemID[5]++ }, want: 1},
		{name: "system priority", change: func(i *lacp.Info) { i.SystemPriority++ }, want: 1},
		{name: "key", change: func(i *lacp.Info) { i.Key++ }, want: 1},
		{name: "port", change: func(i *lacp.Info) { i.PortID++ }, want: 1},
		{name: "port priority", change: func(i *lacp.Info) { i.PortPriority++ }, want: 1},
		{name: "activity", change: func(i *lacp.Info) { i.State ^= lacp.StateActive }, want: 1},
		{name: "timeout", change: func(i *lacp.Info) { i.State ^= lacp.StateShortTimeout }, want: 1},
		{name: "synchronization", change: func(i *lacp.Info) { i.State ^= lacp.StateSynchronization }, want: 1},
		{name: "aggregation", change: func(i *lacp.Info) { i.State ^= lacp.StateAggregation }, want: 1},
		{name: "other state bits", change: func(i *lacp.Info) {
			i.State ^= lacp.StateCollecting | lacp.StateDistributing | lacp.StateDefaulted | lacp.StateExpired
		}},
		{name: "current echo", change: func(_ *lacp.Info) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Active}}}}, lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0a"))
			t0 := time.Unix(1700000000, 0)
			l.LinkChange(t0, "1/1/1", true)
			pdu := lacp.PDU{Actor: lacp.Info{SystemID: mustMAC(t, "02:00:00:00:00:0b"), PortID: 9, State: lacp.StateActive}, Partner: l.PortInfo("1/1/1").Actor}
			l.Receive(t0, "1/1/1", pdu)
			l.Advance(t0.Add(2 * time.Second))
			before := l.PortInfo("1/1/1")
			if before.Status != lag.Current || !before.Attached {
				t.Fatalf("member = %+v, want Current and attached", before)
			}
			pdu.Partner = before.Actor
			tc.change(&pdu.Partner)
			fx := l.Receive(t0.Add(2100*time.Millisecond), "1/1/1", pdu)
			if len(fx.Emissions) != tc.want {
				t.Fatalf("echo response emissions = %d, want %d", len(fx.Emissions), tc.want)
			}
			if tc.want == 1 {
				response, err := lacp.Decode(fx.Emissions[0].Frame)
				if err != nil || response.Actor != before.Actor {
					t.Fatalf("response Actor = %+v, error = %v, want %+v", response.Actor, err, before.Actor)
				}
			}
		})
	}
}

func TestTransmitLimitSendsLatestState(t *testing.T) {
	t.Parallel()

	l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Active}}}}, lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0a"))
	t0 := time.Unix(1700000000, 0)
	if fx := l.LinkChange(t0, "1/1/1", true); len(fx.Emissions) != 1 {
		t.Fatalf("first emissions = %d, want 1", len(fx.Emissions))
	}
	pdu := lacp.PDU{Actor: lacp.Info{SystemID: mustMAC(t, "02:00:00:00:00:0b"), Key: 7, PortID: 9, State: lacp.StateActive | lacp.StateAggregation}, Partner: l.PortInfo("1/1/1").Actor}
	if fx := l.Receive(t0.Add(100*time.Millisecond), "1/1/1", pdu); len(fx.Emissions) != 1 {
		t.Fatalf("second emissions = %d, want 1", len(fx.Emissions))
	}
	l.LinkChange(t0.Add(200*time.Millisecond), "1/1/1", false)
	if fx := l.LinkChange(t0.Add(200*time.Millisecond), "1/1/1", true); len(fx.Emissions) != 1 {
		t.Fatalf("third emissions = %d, want 1", len(fx.Emissions))
	}
	if fx := l.Receive(t0.Add(300*time.Millisecond), "1/1/1", pdu); len(fx.Emissions) != 0 {
		t.Fatalf("fourth emissions = %d, want 0 until window opens", len(fx.Emissions))
	}
	pdu.Actor.Key = 8
	if fx := l.Receive(t0.Add(400*time.Millisecond), "1/1/1", pdu); len(fx.Emissions) != 0 {
		t.Fatalf("latest state emissions = %d, want 0 until window opens", len(fx.Emissions))
	}
	if next, ok := l.NextWake(); !ok || !next.Equal(t0.Add(time.Second)) {
		t.Fatalf("delayed NextWake = (%v, %v), want t0+1s", next, ok)
	}
	if fx := l.Advance(t0.Add(time.Second - time.Nanosecond)); len(fx.Emissions) != 0 {
		t.Fatalf("before window emissions = %d, want 0", len(fx.Emissions))
	}
	clone := l.Clone()
	if fx := clone.Advance(t0.Add(time.Second)); len(fx.Emissions) != 1 {
		t.Fatalf("clone deferred emissions = %d, want 1", len(fx.Emissions))
	}
	fx := l.Advance(t0.Add(time.Second))
	if len(fx.Emissions) != 1 {
		t.Fatalf("deferred emissions = %d, want 1", len(fx.Emissions))
	}
	response, err := lacp.Decode(fx.Emissions[0].Frame)
	info := l.PortInfo("1/1/1")
	if err != nil || response.Actor != info.Actor || response.Partner != info.Partner || response.Partner.Key != 8 || info.LACPDUsTx != 4 {
		t.Fatalf("deferred PDU = %+v, member = %+v, error = %v, want latest state and four transmissions", response, info, err)
	}
	pdu.Partner = info.Actor
	pdu.Partner.Key++
	if fx := l.Receive(t0.Add(1050*time.Millisecond), "1/1/1", pdu); len(fx.Emissions) != 0 {
		t.Fatalf("rolling window emissions = %d, want 0", len(fx.Emissions))
	}
	if next, ok := l.NextWake(); !ok || !next.Equal(t0.Add(1100*time.Millisecond)) {
		t.Fatalf("rolling NextWake = (%v, %v), want t0+1.1s", next, ok)
	}
	if fx := l.Advance(t0.Add(1100 * time.Millisecond)); len(fx.Emissions) != 1 {
		t.Fatalf("rolling deferred emissions = %d, want 1", len(fx.Emissions))
	}
}

func TestPeriodicDeadlineSurvivesNTT(t *testing.T) {
	t.Parallel()

	l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Active}}}}, lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0a"))
	t0 := time.Unix(1700000000, 0)
	l.LinkChange(t0, "1/1/1", true)
	pdu := lacp.PDU{Actor: lacp.Info{SystemID: mustMAC(t, "02:00:00:00:00:0b"), PortID: 9, State: lacp.StateActive | lacp.StateShortTimeout}, Partner: l.PortInfo("1/1/1").Actor}
	l.Receive(t0.Add(100*time.Millisecond), "1/1/1", pdu)
	pdu.Partner = l.PortInfo("1/1/1").Actor
	pdu.Partner.Key++
	if fx := l.Receive(t0.Add(600*time.Millisecond), "1/1/1", pdu); len(fx.Emissions) != 1 || l.PortInfo("1/1/1").LACPDUsTx != 3 {
		t.Fatalf("stale echo emissions = %+v, transmit count = %d, want third transmission", fx.Emissions, l.PortInfo("1/1/1").LACPDUsTx)
	}
	if next, ok := l.NextWake(); !ok || !next.Equal(t0.Add(time.Second)) {
		t.Fatalf("NextWake after NTT = (%v, %v), want original periodic deadline at t0+1s", next, ok)
	}
	if fx := l.Advance(t0.Add(time.Second)); len(fx.Emissions) != 1 {
		t.Fatalf("periodic emissions = %d, want 1", len(fx.Emissions))
	}
}

func TestPeriodicStopsWhenInactive(t *testing.T) {
	t.Parallel()

	l := mustNewLAG(t, lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Passive}}}}, lagTwoPortTable(t), mustMAC(t, "02:00:00:00:00:0a"))
	t0 := time.Unix(1700000000, 0)
	if fx := l.LinkChange(t0, "1/1/1", true); len(fx.Emissions) != 0 {
		t.Fatalf("passive carrier-up emissions = %d, want 0", len(fx.Emissions))
	}
	pdu := lacp.PDU{Actor: lacp.Info{SystemID: mustMAC(t, "02:00:00:00:00:0b"), PortID: 9, State: lacp.StateActive | lacp.StateShortTimeout}}
	for _, ms := range []int{0, 100, 200} {
		if fx := l.Receive(t0.Add(time.Duration(ms)*time.Millisecond), "1/1/1", pdu); len(fx.Emissions) != 1 {
			t.Fatalf("active Partner emissions at %dms = %d, want 1", ms, len(fx.Emissions))
		}
	}
	if fx := l.Receive(t0.Add(300*time.Millisecond), "1/1/1", pdu); len(fx.Emissions) != 0 {
		t.Fatalf("pending fourth emissions = %d, want 0", len(fx.Emissions))
	}
	if next, ok := l.NextWake(); !ok || !next.Equal(t0.Add(time.Second)) {
		t.Fatalf("pending NextWake = (%v, %v), want t0+1s", next, ok)
	}
	pdu.Actor.State &^= lacp.StateActive
	if fx := l.Receive(t0.Add(400*time.Millisecond), "1/1/1", pdu); len(fx.Emissions) != 0 {
		t.Fatalf("inactive emissions = %d, want 0", len(fx.Emissions))
	}
	if fx := l.Advance(t0.Add(2100 * time.Millisecond)); len(fx.Emissions) != 0 {
		t.Fatalf("inactive deferred emissions = %d, want 0", len(fx.Emissions))
	}
	if info := l.PortInfo("1/1/1"); info.Status != lag.Current || !info.Attached || info.LACPDUsTx != 3 {
		t.Fatalf("inactive member = %+v, want Current, attached, and three transmissions", info)
	}
	if next, ok := l.NextWake(); !ok || !next.Equal(t0.Add(90400*time.Millisecond)) {
		t.Fatalf("inactive NextWake = (%v, %v), want receive timeout at t0+90.4s", next, ok)
	}
	l.LinkChange(t0.Add(2200*time.Millisecond), "1/1/1", false)
	if next, ok := l.NextWake(); ok {
		t.Fatalf("carrier-down NextWake = (%v, %v), want no timer", next, ok)
	}
}
