package loopprotect_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
)

// TestAdvanceEmitsOnlyWhenTheProbeIntervalIsDue is evidence that the layer meters
// its own probes. A switch wakes its layers together, so this Advance runs at
// every spanning tree hello and every recovery expiry as well as at its own
// interval; emitting on each of them would probe far faster than configured.
func TestAdvanceEmitsOnlyWhenTheProbeIntervalIsDue(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
		},
	}, layer.Env{Ports: tbl, MAC: switchMAC})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)
	l.LinkChange(t0, "1/1/1", true)

	due, ok := l.NextWake()
	if !ok {
		t.Fatalf("NextWake after LinkChange: no timer")
	}

	if fx := l.Advance(t0.Add(time.Second)); len(fx.Emissions) != 0 {
		t.Errorf("Advance one second in: %d emissions, want 0", len(fx.Emissions))
	}
	fx := l.Advance(due)
	if len(fx.Emissions) != 1 {
		t.Fatalf("Advance at the due time: %d emissions, want 1", len(fx.Emissions))
	}
	assertProbeFrame(t, fx.Emissions[0], 0)
	if fx := l.Advance(due.Add(time.Second)); len(fx.Emissions) != 0 {
		t.Errorf("Advance one second after the due time: %d emissions, want 0", len(fx.Emissions))
	}
	fx = l.Advance(due.Add(5 * time.Second))
	if len(fx.Emissions) != 1 {
		t.Fatalf("Advance one interval after the due time: %d emissions, want 1", len(fx.Emissions))
	}
	assertProbeFrame(t, fx.Emissions[0], 1)
}

func newCadenceLayer(t *testing.T, cfg loopprotect.Config, names ...string) *loopprotect.Layer {
	t.Helper()

	l, err := loopprotect.New(cfg, layer.Env{Ports: newLayerTable(t, names...), MAC: switchMAC})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return l
}

func wantNextWake(t *testing.T, l *loopprotect.Layer, step string, want time.Time) {
	t.Helper()

	got, ok := l.NextWake()
	if !ok || !got.Equal(want) {
		t.Errorf("NextWake after %s = %v, %v, want %v, true", step, got, ok, want)
	}
}

func wantNoWake(t *testing.T, l *loopprotect.Layer, step string) {
	t.Helper()

	if got, ok := l.NextWake(); ok {
		t.Errorf("NextWake after %s = %v, true, want no timer", step, got)
	}
}

// TestReceiveArmsTheRecoveryDeadline pins that a layer no other call has
// touched still reports the recovery deadline Receive set.
func TestReceiveArmsTheRecoveryDeadline(t *testing.T) {
	t.Parallel()

	l := newCadenceLayer(t, loopprotect.Config{
		Interval: 60 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Timer, Duration: 30 * time.Second}},
		},
	}, "1/1/1")

	t0 := time.Unix(1_700_000_000, 0)
	l.Receive(t0, loopprotect.Return{VID: 0}, returnedProbe("1/1/1", 0))

	wantNextWake(t, l, "Receive", t0.Add(30*time.Second))
}

// TestNextWakeReportsProbesOnlyWhileSomePortEmits pins that the probe time
// leaves NextWake while every port is under an applied Disable, and that each
// lift of the last stopped port starts the interval over.
func TestNextWakeReportsProbesOnlyWhileSomePortEmits(t *testing.T) {
	t.Parallel()

	const interval = 60 * time.Second

	disable := func(rec loopprotect.Recovery) loopprotect.Config {
		return loopprotect.Config{
			Interval: interval,
			Ports:    map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Disable, Recovery: rec}},
		}
	}
	t0 := time.Unix(1_700_000_000, 0)
	t1 := t0.Add(10 * time.Second)
	probe := returnedProbe("1/1/1", 0)

	t.Run("Clear", func(t *testing.T) {
		t.Parallel()

		l := newCadenceLayer(t, disable(loopprotect.Recovery{Mode: loopprotect.Manual}), "1/1/1")
		l.Advance(t0)
		l.Receive(t0.Add(5*time.Second), loopprotect.Return{}, probe)
		wantNoWake(t, l, "Disable applied")

		if !l.Clear(t1, "1/1/1") {
			t.Fatalf("Clear = false, want true")
		}
		wantNextWake(t, l, "Clear", t1.Add(interval))
	})

	t.Run("link down", func(t *testing.T) {
		t.Parallel()

		l := newCadenceLayer(t, disable(loopprotect.Recovery{Mode: loopprotect.Manual}), "1/1/1")
		l.Advance(t0)
		l.Receive(t0.Add(5*time.Second), loopprotect.Return{}, probe)
		wantNoWake(t, l, "Disable applied")

		l.LinkChange(t1, "1/1/1", false)
		wantNextWake(t, l, "link down", t1.Add(interval))
	})

	t.Run("Advance lifts a Timer window", func(t *testing.T) {
		t.Parallel()

		l := newCadenceLayer(t, disable(loopprotect.Recovery{Mode: loopprotect.Timer, Duration: 20 * time.Second}), "1/1/1")
		l.Advance(t0)
		l.Receive(t0.Add(5*time.Second), loopprotect.Return{}, probe)
		wantNextWake(t, l, "Disable applied", t0.Add(25*time.Second))

		lift := t0.Add(25 * time.Second)
		if fx := l.Advance(lift); len(fx.Emissions) != 0 {
			t.Errorf("Advance at the lift emitted %d frames, want 0", len(fx.Emissions))
		}
		wantNextWake(t, l, "Advance at the lift", lift.Add(interval))
	})

	t.Run("a layer without ports", func(t *testing.T) {
		t.Parallel()

		l := newCadenceLayer(t, loopprotect.Config{Interval: interval})
		l.Advance(t0)
		wantNoWake(t, l, "Advance")
	})
}

func TestNextWakeKeepsProbeTimeWithADisabledSibling(t *testing.T) {
	t.Parallel()

	const interval = 60 * time.Second
	l := newCadenceLayer(t, loopprotect.Config{
		Interval: interval,
		Ports: map[string]loopprotect.Port{
			"a": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
			"b": {Action: loopprotect.Disable, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
		},
	}, "a", "b")
	t0 := time.Unix(1_700_000_000, 0)
	l.Advance(t0)
	l.Receive(t0.Add(time.Second), loopprotect.Return{}, returnedProbe("b", 0))
	if got := l.PortInfo("b").Action; got != loopprotect.Disable {
		t.Fatalf("b Action = %q, want Disable", got)
	}
	wantNextWake(t, l, "Disable applied to b", t0.Add(interval))
}

func TestAppliedBlockAndNoLearnKeepProbing(t *testing.T) {
	t.Parallel()

	for _, action := range []loopprotect.Action{loopprotect.Block, loopprotect.NoLearn} {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			const interval = 60 * time.Second
			l := newCadenceLayer(t, loopprotect.Config{
				Interval: interval,
				Ports: map[string]loopprotect.Port{
					"a": {Action: action, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
				},
			}, "a")
			t0 := time.Unix(1_700_000_000, 0)
			l.Advance(t0)
			l.Receive(t0.Add(time.Second), loopprotect.Return{}, returnedProbe("a", 0))
			if got := l.PortInfo("a").Action; got != action {
				t.Fatalf("Action = %q, want %q", got, action)
			}
			due := t0.Add(interval)
			wantNextWake(t, l, "action applied", due)
			fx := l.Advance(due)
			if len(fx.Emissions) != 1 {
				t.Fatalf("Advance at probe time emitted %d frames, want 1", len(fx.Emissions))
			}
			if got := fx.Emissions[0].Port; got != "a" {
				t.Errorf("emission port = %q, want a", got)
			}
			assertProbeFrame(t, fx.Emissions[0], 0)
		})
	}
}

// TestCloneKeepsNextWake pins that a clone taken after Receive reports the
// deadline its source reports.
func TestCloneKeepsNextWake(t *testing.T) {
	t.Parallel()

	l := newCadenceLayer(t, loopprotect.Config{
		Interval: 60 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Timer, Duration: 30 * time.Second}},
		},
	}, "1/1/1")

	t0 := time.Unix(1_700_000_000, 0)
	l.Receive(t0, loopprotect.Return{VID: 0}, returnedProbe("1/1/1", 0))

	want, wantOK := l.NextWake()
	got, gotOK := l.Clone().NextWake()
	if got != want || gotOK != wantOK {
		t.Errorf("clone NextWake = %v, %v, source = %v, %v", got, gotOK, want, wantOK)
	}
	if !wantOK {
		t.Errorf("source NextWake after Receive reports no timer")
	}
}

// TestClearResetsInterVLANAndALiftKeepsIt pins which lifts reset the
// inter-VLAN observation: Clear does, a Timer window and a link cycle do not.
func TestClearResetsInterVLANAndALiftKeepsIt(t *testing.T) {
	t.Parallel()

	t0 := time.Unix(1_700_000_000, 0)
	cross := loopprotect.Return{VID: 20}
	probe := loopprotect.Probe{OriginMAC: switchMAC, VID: 10, Sequence: 1, Port: "1/1/1"}

	cases := []struct {
		name string
		rec  loopprotect.Recovery
		lift func(*loopprotect.Layer)
		want bool
	}{
		{"Clear", loopprotect.Recovery{Mode: loopprotect.Manual}, func(l *loopprotect.Layer) { l.Clear(t0.Add(time.Second), "1/1/1") }, false},
		{"Timer window", loopprotect.Recovery{Mode: loopprotect.Timer, Duration: 10 * time.Second}, func(l *loopprotect.Layer) { l.Advance(t0.Add(10 * time.Second)) }, true},
		{"link cycle", loopprotect.Recovery{Mode: loopprotect.Manual}, func(l *loopprotect.Layer) { l.LinkChange(t0.Add(time.Second), "1/1/1", false) }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := newCadenceLayer(t, loopprotect.Config{
				Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block, Recovery: tc.rec}},
			}, "1/1/1")
			l.Receive(t0, cross, probe)
			if !l.PortInfo("1/1/1").InterVLAN {
				t.Fatalf("InterVLAN after the return = false, want true")
			}

			tc.lift(l)
			if got := l.PortInfo("1/1/1"); got.Action != "" || got.InterVLAN != tc.want {
				t.Errorf("after the lift Action = %q, InterVLAN = %v, want empty and %v", got.Action, got.InterVLAN, tc.want)
			}
		})
	}
}
