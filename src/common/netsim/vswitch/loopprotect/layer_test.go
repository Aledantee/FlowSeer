package loopprotect_test

import (
	"bytes"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

var switchMAC = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}

func newLayerTable(t *testing.T, names ...string) port.Table {
	t.Helper()

	b := port.NewBuilder()
	for _, name := range names {
		b = b.Add(port.Port{Name: name, Kind: port.Physical})
	}
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

	return tbl
}

func returnedProbe(sendPort string, vid vlan.ID) loopprotect.Probe {
	return loopprotect.Probe{
		OriginMAC: switchMAC,
		VID:       vid,
		Sequence:  1,
		Port:      sendPort,
	}
}

func TestReceiveAppliesActionToNamedPort(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	if got := l.PortInfo("1/1/1").Action; got != "" {
		t.Fatalf("PortInfo before Receive: Action = %q, want empty", got)
	}

	l.Receive(t0, 10, returnedProbe("1/1/1", 10))

	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Errorf("PortInfo after Receive: Action = %q, want %q", got, loopprotect.Block)
	}
}

func TestRecoveryLoopCleared(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {
				Action:   loopprotect.Block,
				Recovery: loopprotect.Recovery{Mode: loopprotect.LoopCleared, Duration: 15 * time.Second},
			},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	// Applied at t0.
	l.Receive(t0, 0, returnedProbe("1/1/1", 0))
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action after first Receive = %q, want Block", got)
	}

	// A probe keeps returning at t+5s and t+10s, restarting the wait each time.
	l.Wake(t0.Add(5 * time.Second))
	l.Receive(t0.Add(5*time.Second), 0, returnedProbe("1/1/1", 0))
	l.Wake(t0.Add(10 * time.Second))
	l.Receive(t0.Add(10*time.Second), 0, returnedProbe("1/1/1", 0))

	// Cable fault just before t+15s and t+20s: no more returned probes after
	// the one at t+10s. The wait, restarted at t+10s, elapses at t+25s, not
	// at the naive t0+15s.
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action just before the wait elapses = %q, want still Block", got)
	}

	l.Wake(t0.Add(20 * time.Second))
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Errorf("Action at t+20s = %q, want still Block (wait elapses at t+25s)", got)
	}

	l.Wake(t0.Add(25 * time.Second))
	if got := l.PortInfo("1/1/1").Action; got != "" {
		t.Errorf("Action at t+25s = %q, want empty (recovered)", got)
	}
}

func TestRecoveryTimer(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {
				Action:   loopprotect.Block,
				Recovery: loopprotect.Recovery{Mode: loopprotect.Timer, Duration: 15 * time.Second},
			},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	l.Receive(t0, 0, returnedProbe("1/1/1", 0))
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action after first Receive = %q, want Block", got)
	}

	// The loop is still cabled: probes keep returning, but Timer takes no
	// notice and lifts unconditionally at t+15s.
	l.Receive(t0.Add(5*time.Second), 0, returnedProbe("1/1/1", 0))
	l.Receive(t0.Add(10*time.Second), 0, returnedProbe("1/1/1", 0))

	l.Wake(t0.Add(15 * time.Second))
	if got := l.PortInfo("1/1/1").Action; got != "" {
		t.Fatalf("Action at t+15s = %q, want empty (Timer lifted)", got)
	}
	if got := l.PortInfo("1/1/1").Recurrences; got != 0 {
		t.Fatalf("Recurrences before reapplication = %d, want 0", got)
	}

	// The next returned probe reapplies the action.
	l.Receive(t0.Add(16*time.Second), 0, returnedProbe("1/1/1", 0))
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action after reapplication = %q, want Block", got)
	}
	if got := l.PortInfo("1/1/1").Recurrences; got != 1 {
		t.Errorf("Recurrences after reapplication = %d, want 1", got)
	}
}

func TestReceiveExpiresElapsedTimerWindowBeforeApplying(t *testing.T) {
	t.Parallel()

	// Evidence that Receive expires an elapsed recovery window itself,
	// using the same rule Wake uses, instead of reading a stale applied
	// state that only a later Wake call would have caught: a probe
	// delivered at exactly the expiry instant must see the window as
	// already lifted, and the action as freshly reapplied.
	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {
				Action:   loopprotect.Block,
				Recovery: loopprotect.Recovery{Mode: loopprotect.Timer, Duration: 15 * time.Second},
			},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	l.Receive(t0, 0, returnedProbe("1/1/1", 0))
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action after first Receive = %q, want Block", got)
	}

	// A probe returns at exactly t0+15s, the instant the window elapses,
	// with no Wake call in between.
	l.Receive(t0.Add(15*time.Second), 0, returnedProbe("1/1/1", 0))

	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action at exact expiry = %q, want still Block (reapplied)", got)
	}
	if got := l.PortInfo("1/1/1").Recurrences; got != 1 {
		t.Errorf("Recurrences at exact expiry = %d, want 1", got)
	}
}

func TestClearResetsRecurrenceTracking(t *testing.T) {
	t.Parallel()

	// Evidence that Clear resets everApplied along with applied: the next
	// application after a Clear is not a Timer recurrence, since Clear,
	// not an elapsed Timer window, ended the previous one.
	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {
				Action:   loopprotect.Block,
				Recovery: loopprotect.Recovery{Mode: loopprotect.Timer, Duration: 15 * time.Second},
			},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	l.Receive(t0, 0, returnedProbe("1/1/1", 0))
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action after first Receive = %q, want Block", got)
	}

	if ok := l.Clear(t0.Add(time.Second), "1/1/1"); !ok {
		t.Fatalf("Clear = false, want true")
	}

	// The loop is still cabled: the very next returned probe reapplies the
	// action.
	l.Receive(t0.Add(2*time.Second), 0, returnedProbe("1/1/1", 0))
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action after reapplication = %q, want Block", got)
	}
	if got := l.PortInfo("1/1/1").Recurrences; got != 0 {
		t.Errorf("Recurrences after Clear then reapplication = %d, want 0 (Clear, not a Timer recovery, ended the prior application)", got)
	}
}

func TestReceiveFlushesOnTransitionIntoDenyingAction(t *testing.T) {
	t.Parallel()

	// Evidence that Receive flushes the port's learned entries exactly
	// once, on the transition into a forwarding-denying action, and not
	// again for a repeat probe on a port already carrying that action.
	tbl := newLayerTable(t, "block")
	l, err := loopprotect.New(loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"block": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	fx := l.Receive(t0, 0, returnedProbe("block", 0))
	if len(fx.Flush) != 1 {
		t.Fatalf("Flush on transition = %d targets, want 1: %+v", len(fx.Flush), fx.Flush)
	}
	if fx.Flush[0].Port != "block" || len(fx.Flush[0].FIDs) != 0 {
		t.Errorf("Flush target = %+v, want {Port: block, FIDs: nil}", fx.Flush[0])
	}

	fx = l.Receive(t0.Add(time.Second), 0, returnedProbe("block", 0))
	if len(fx.Flush) != 0 {
		t.Errorf("Flush on repeat probe = %d targets, want 0: %+v", len(fx.Flush), fx.Flush)
	}
}

func TestReceiveNoLearnNeverFlushes(t *testing.T) {
	t.Parallel()

	// Evidence that NoLearn, which keeps forwarding, never flushes: the
	// entries it learned remain valid.
	tbl := newLayerTable(t, "nolearn")
	l, err := loopprotect.New(loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"nolearn": {Action: loopprotect.NoLearn},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	fx := l.Receive(t0, 0, returnedProbe("nolearn", 0))
	if len(fx.Flush) != 0 {
		t.Errorf("Flush for NoLearn = %d targets, want 0: %+v", len(fx.Flush), fx.Flush)
	}

	// The positive half: NoLearn was actually applied, denying learning
	// while still forwarding, rather than the absence of a flush meaning
	// nothing happened at all.
	if got := l.PortInfo("nolearn").Action; got != loopprotect.NoLearn {
		t.Errorf("PortInfo(nolearn).Action = %q, want %q", got, loopprotect.NoLearn)
	}
	if l.Learns("nolearn", 0) {
		t.Errorf("Learns(nolearn) = true, want false once NoLearn is applied")
	}
	if !l.Forwards("nolearn", 0) {
		t.Errorf("Forwards(nolearn) = false, want true: NoLearn keeps forwarding")
	}
}

func TestRecoveryManual(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {
				Action:   loopprotect.Block,
				Recovery: loopprotect.Recovery{Mode: loopprotect.Manual},
			},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	l.Receive(t0, 0, returnedProbe("1/1/1", 0))
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action after Receive = %q, want Block", got)
	}

	// Still applied one hour later: Manual never auto-lifts.
	l.Wake(t0.Add(time.Hour))
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action after 1h = %q, want still Block", got)
	}

	// An up report alone does not clear it.
	l.LinkChange(t0.Add(time.Hour), "1/1/1", true)
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action after up report alone = %q, want still Block", got)
	}

	// A link down and then up clears it.
	l.LinkChange(t0.Add(time.Hour+time.Second), "1/1/1", false)
	l.LinkChange(t0.Add(time.Hour+2*time.Second), "1/1/1", true)
	if got := l.PortInfo("1/1/1").Action; got != "" {
		t.Errorf("Action after link cycle = %q, want empty", got)
	}
}

func TestRecoveryManualClearedByClear(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	l.Receive(t0, 0, returnedProbe("1/1/1", 0))
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Fatalf("Action after Receive = %q, want Block", got)
	}

	if ok := l.Clear(t0.Add(time.Second), "no-such-port"); ok {
		t.Errorf("Clear on unknown port = true, want false")
	}
	if ok := l.Clear(t0.Add(time.Second), "1/1/1"); !ok {
		t.Errorf("Clear on applied port = false, want true")
	}
	if got := l.PortInfo("1/1/1").Action; got != "" {
		t.Errorf("Action after Clear = %q, want empty", got)
	}
	if ok := l.Clear(t0.Add(2*time.Second), "1/1/1"); ok {
		t.Errorf("second Clear = true, want false (nothing applied)")
	}
}

func TestRecoveryDefaultPerAction(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "block", "nolearn", "disable")
	cfg := loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"block":   {Action: loopprotect.Block},
			"nolearn": {Action: loopprotect.NoLearn},
			"disable": {Action: loopprotect.Disable},
		},
	}
	if _, err := loopprotect.New(cfg, tbl, switchMAC); err != nil {
		t.Fatalf("New: %v", err)
	}

	normalized := cfg.Normalize()
	tests := []struct {
		port string
		want loopprotect.RecoveryMode
	}{
		{"block", loopprotect.LoopCleared},
		{"nolearn", loopprotect.LoopCleared},
		{"disable", loopprotect.Manual},
	}
	for _, tc := range tests {
		if got := normalized.Ports[tc.port].Recovery.Mode; got != tc.want {
			t.Errorf("Ports[%q].Recovery.Mode = %s, want %s", tc.port, got, tc.want)
		}
	}
}

func TestInterVLAN(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	probe := loopprotect.Probe{OriginMAC: switchMAC, VID: 10, Sequence: 1, Port: "1/1/1"}
	l.Receive(t0, 20, probe)

	if got := l.PortInfo("1/1/1").InterVLAN; !got {
		t.Errorf("InterVLAN = %v, want true", got)
	}
}

func TestInterVLANFalseWhenMatching(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	probe := loopprotect.Probe{OriginMAC: switchMAC, VID: 10, Sequence: 1, Port: "1/1/1"}
	l.Receive(t0, 10, probe)

	if got := l.PortInfo("1/1/1").InterVLAN; got {
		t.Errorf("InterVLAN = %v, want false", got)
	}
}

func TestWakeEmitsProbesForProtectedPortsInSortedOrder(t *testing.T) {
	t.Parallel()

	// Evidence that Wake gates emission on the applied action, not the
	// configured one: a Disable-configured port that has never had the
	// action applied still probes, since a returned probe is the only way
	// Disable can ever be applied.
	tbl := newLayerTable(t, "1/1/3", "1/1/1", "1/1/2")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/3": {Action: loopprotect.Block},
			"1/1/1": {Action: loopprotect.NoLearn},
			"1/1/2": {Action: loopprotect.Disable},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)
	l.Wake(t0)
	fx := l.Wake(t0.Add(5 * time.Second))

	if len(fx.Emissions) != 3 {
		t.Fatalf("Wake() emitted %d frames, want 3 (never-applied Disable port still probes): %+v", len(fx.Emissions), fx.Emissions)
	}
	if fx.Emissions[0].Port != "1/1/1" || fx.Emissions[1].Port != "1/1/2" || fx.Emissions[2].Port != "1/1/3" {
		t.Errorf("emission order = [%s, %s, %s], want [1/1/1, 1/1/2, 1/1/3] (sorted)",
			fx.Emissions[0].Port, fx.Emissions[1].Port, fx.Emissions[2].Port)
	}
}

func TestWakeStopsProbingOnceDisableIsApplied(t *testing.T) {
	t.Parallel()

	// Evidence that emission gates on the applied action: a Disable port
	// probes until a returned probe applies the action, then stops.
	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Disable, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)
	l.Wake(t0)
	fx := l.Wake(t0.Add(5 * time.Second))
	if len(fx.Emissions) != 1 {
		t.Fatalf("Wake() before the action is applied emitted %d frames, want 1", len(fx.Emissions))
	}

	l.Receive(t0.Add(5*time.Second), 0, returnedProbe("1/1/1", 0))
	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Disable {
		t.Fatalf("Action after Receive = %q, want Disable", got)
	}

	fx = l.Wake(t0.Add(10 * time.Second))
	if len(fx.Emissions) != 0 {
		t.Errorf("Wake() after the action is applied emitted %d frames, want 0: %+v", len(fx.Emissions), fx.Emissions)
	}
}

func TestWakeEmitsPerVLAN(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1", "1/1/2")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{20, 10}},
			"1/1/2": {Action: loopprotect.Block},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)
	l.Wake(t0)
	fx := l.Wake(t0.Add(5 * time.Second))

	var vidsFor1 []vlan.ID
	var vidsFor2 []vlan.ID
	for _, e := range fx.Emissions {
		switch e.Port {
		case "1/1/1":
			vidsFor1 = append(vidsFor1, e.VID)
		case "1/1/2":
			vidsFor2 = append(vidsFor2, e.VID)
		}
	}

	if len(vidsFor1) != 2 || vidsFor1[0] != 10 || vidsFor1[1] != 20 {
		t.Errorf("VIDs for 1/1/1 = %v, want [10 20] (sorted)", vidsFor1)
	}
	if len(vidsFor2) != 1 || vidsFor2[0] != 0 {
		t.Errorf("VIDs for 1/1/2 = %v, want [0] (no VLANs configured)", vidsFor2)
	}
}

func TestWakeSequenceNumbersIncreasePerPort(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	l.Wake(t0)
	fx1 := l.Wake(t0.Add(5 * time.Second))
	fx2 := l.Wake(t0.Add(10 * time.Second))

	if len(fx1.Emissions) != 1 || len(fx2.Emissions) != 1 {
		t.Fatalf("expected one emission per wake, got %d and %d", len(fx1.Emissions), len(fx2.Emissions))
	}

	p1, err := loopprotect.Decode(loopprotect.Encode(fx1.Emissions[0].Probe, switchMAC))
	if err != nil {
		t.Fatalf("Decode(fx1): %v", err)
	}
	p2, err := loopprotect.Decode(loopprotect.Encode(fx2.Emissions[0].Probe, switchMAC))
	if err != nil {
		t.Fatalf("Decode(fx2): %v", err)
	}

	if p1.Sequence == p2.Sequence {
		t.Errorf("sequence numbers equal across wakes: %d", p1.Sequence)
	}
	if p2.Sequence <= p1.Sequence {
		t.Errorf("sequence did not increase: %d then %d", p1.Sequence, p2.Sequence)
	}

	// Frames are otherwise identical: same ports, same everything but the
	// sequence field it carries.
	p1.Sequence = 0
	p2.Sequence = 0
	if p1 != p2 {
		t.Errorf("frames differ by more than sequence: %+v vs %+v", p1, p2)
	}
	f1 := loopprotect.Encode(fx1.Emissions[0].Probe, switchMAC)
	f2 := loopprotect.Encode(fx2.Emissions[0].Probe, switchMAC)
	if !bytes.Equal(f1.Dst[:], f2.Dst[:]) {
		t.Errorf("Dst differs across wakes")
	}
}

func TestGateForEachAction(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "block", "nolearn", "disable", "unprotected")
	l, err := loopprotect.New(loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"block":   {Action: loopprotect.Block},
			"nolearn": {Action: loopprotect.NoLearn},
			"disable": {Action: loopprotect.Disable},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)

	tests := []struct {
		name         string
		port         string
		wantLearns   bool
		wantForwards bool
	}{
		{"unprotected port allows both", "unprotected", true, true},
		{"protected but unapplied port allows both", "block", true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := l.Learns(tc.port, 10); got != tc.wantLearns {
				t.Errorf("Learns(%q) = %v, want %v", tc.port, got, tc.wantLearns)
			}
			if got := l.Forwards(tc.port, 10); got != tc.wantForwards {
				t.Errorf("Forwards(%q) = %v, want %v", tc.port, got, tc.wantForwards)
			}
		})
	}

	l.Receive(t0, 0, returnedProbe("block", 0))
	l.Receive(t0, 0, returnedProbe("nolearn", 0))
	l.Receive(t0, 0, returnedProbe("disable", 0))

	applied := []struct {
		name         string
		port         string
		wantLearns   bool
		wantForwards bool
	}{
		{"Block denies both", "block", false, false},
		{"NoLearn denies learns only", "nolearn", false, true},
		{"Disable denies both", "disable", false, false},
	}
	for _, tc := range applied {
		t.Run(tc.name, func(t *testing.T) {
			if got := l.Learns(tc.port, 10); got != tc.wantLearns {
				t.Errorf("Learns(%q) = %v, want %v", tc.port, got, tc.wantLearns)
			}
			if got := l.Forwards(tc.port, 10); got != tc.wantForwards {
				t.Errorf("Forwards(%q) = %v, want %v", tc.port, got, tc.wantForwards)
			}
		})
	}
}

func TestForwardingFactPerDenial(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "block", "nolearn", "disable")
	l, err := loopprotect.New(loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"block":   {Action: loopprotect.Block},
			"nolearn": {Action: loopprotect.NoLearn},
			"disable": {Action: loopprotect.Disable},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)
	l.Receive(t0, 0, returnedProbe("block", 0))
	l.Receive(t0, 0, returnedProbe("nolearn", 0))
	l.Receive(t0, 0, returnedProbe("disable", 0))

	seen := map[string]bool{}
	for _, name := range []string{"block", "nolearn", "disable"} {
		learns := l.Learns(name, 10)
		forwards := l.Forwards(name, 10)
		fact := l.ForwardingFact(name, 10, learns, forwards)
		if fact == nil {
			t.Fatalf("ForwardingFact(%q) = nil", name)
		}
		canon := fact.Canonical()
		if canon == "" {
			t.Errorf("ForwardingFact(%q).Canonical() is empty", name)
		}
		if seen[canon] {
			t.Errorf("ForwardingFact(%q) duplicates a previous fact: %s", name, canon)
		}
		seen[canon] = true
	}
}

func TestBlockedPortKeepsProbingDisabledDoesNot(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "block", "disable")
	l, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"block":   {Action: loopprotect.Block},
			"disable": {Action: loopprotect.Disable},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)
	l.Wake(t0)
	l.Receive(t0, 0, returnedProbe("block", 0))
	l.Receive(t0, 0, returnedProbe("disable", 0))

	if got := l.PortInfo("block").Action; got != loopprotect.Block {
		t.Fatalf("block port Action = %q, want Block", got)
	}
	if got := l.PortInfo("disable").Action; got != loopprotect.Disable {
		t.Fatalf("disable port Action = %q, want Disable", got)
	}

	fx := l.Wake(t0.Add(5 * time.Second))

	blockSeen := false
	for _, e := range fx.Emissions {
		if e.Port == "block" {
			blockSeen = true
		}
		if e.Port == "disable" {
			t.Errorf("Disable-acted port emitted a probe")
		}
	}
	if !blockSeen {
		t.Errorf("Block-acted port did not emit a probe")
	}
}

func TestCloneIndependence(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)
	l.Receive(t0, 0, returnedProbe("1/1/1", 0))

	cp := l.Clone()
	cp.Clear(t0, "1/1/1")

	if got := l.PortInfo("1/1/1").Action; got != loopprotect.Block {
		t.Errorf("original layer mutated by clearing the clone: Action = %q, want Block", got)
	}
	if got := cp.PortInfo("1/1/1").Action; got != "" {
		t.Errorf("clone Action = %q, want empty", got)
	}
}

func TestReceiveIgnoresUntrackedPort(t *testing.T) {
	t.Parallel()

	tbl := newLayerTable(t, "1/1/1")
	l, err := loopprotect.New(loopprotect.Config{
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
		},
	}, tbl, switchMAC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)
	probe := loopprotect.Probe{OriginMAC: switchMAC, VID: 0, Sequence: 1, Port: "not-protected"}

	fx := l.Receive(t0, 0, probe)
	if len(fx.Emissions) != 0 {
		t.Errorf("Receive for untracked port emitted %d frames, want 0", len(fx.Emissions))
	}
	if got := l.PortInfo("not-protected"); got != (loopprotect.PortInfo{}) {
		t.Errorf("PortInfo(untracked) = %+v, want zero value", got)
	}
}
