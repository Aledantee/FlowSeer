package full

// full_test.go tests the four-phase orchestration over HOOKED behaviors.
// Tests substitute recording stubs for the behavior map and the recon
// function, so the orchestration runs without a real interface. The
// scenarios cover the contract's test matrix:
//   - recon → burst → follow-up → report sequencing
//   - burst composition (core always; ra6/MAC/vrid-armed)
//   - daddos as concurrent burst worker (not follow-up)
//   - empty recon still fires core + fallback chain
//   - traversal upgrade pending→confirmed on watch-leg recording
//   - named-absent watch leg fails fast before recon
//   - SIGINT in phase 1 emits partial findings
//   - gate-map table test (both surfaces)

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/netpen/catalog"
	"go.aledante.io/FlowSeer/src/netpen/findings"
	"go.aledante.io/FlowSeer/src/netpen/link"
	"go.aledante.io/FlowSeer/src/netpen/runner"
)

// --- test helpers ---

// mockLeg is an in-memory link.Leg for the orchestration tests.
type mockLeg struct {
	frames chan link.Frame
	closed atomic.Bool
}

func newMockLeg() *mockLeg {
	return &mockLeg{frames: make(chan link.Frame, 16)}
}

func (m *mockLeg) Send(_ context.Context, _ []byte) error {
	if m.closed.Load() {
		return errs.New().Code(link.ErrCodeLegOpen).Msg("leg is closed")
	}
	return nil
}
func (m *mockLeg) SetFilter(_ []link.RawInstruction) error { return nil }
func (m *mockLeg) Receive(ctx context.Context) <-chan link.Frame {
	out := make(chan link.Frame, 16)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case f, ok := <-m.frames:
				if !ok {
					return
				}
				select {
				case out <- f:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func (m *mockLeg) Close() error {
	m.closed.Store(true)
	close(m.frames)
	return nil
}

// recordingBehaviors returns a behavior map where every name maps to a
// recording stub that appends its name to the fired list and emits a
// progress finding. This lets tests assert which behaviors fired and in
// what phase.
func recordingBehaviors(fired *[]string, mu *sync.Mutex) map[string]runner.Behavior {
	names := []string{
		"stproot", "camflood", "dhcpstarve", "gratarp", "dtp", "roguera", "llmnr",
		"daddos", "arpspoof", "vrrp",
		"roguedhcp6", "raguard", "vlanhop", "voicevlan", "vtp", "mvrp", "ghost", "portsteal",
	}
	m := make(map[string]runner.Behavior, len(names))
	for _, n := range names {
		n := n
		m[n] = func(_ context.Context, deps runner.Deps) error {
			mu.Lock()
			*fired = append(*fired, n)
			mu.Unlock()
			deps.Emitter.Progress("test", n+" fired")
			return nil
		}
	}
	return m
}

// allBehaviors registers a stub for every catalog name so the runner
// does not fail with "no behavior function registered".
func allBehaviors() map[string]runner.Behavior {
	entries := catalog.Entries()
	m := make(map[string]runner.Behavior, len(entries))
	for _, e := range entries {
		e := e
		m[e.Name] = func(_ context.Context, deps runner.Deps) error {
			deps.Emitter.Progress("test", e.Name+" fired")
			return nil
		}
	}
	return m
}

// collectRecords drains the Full orchestrator's records after Run.
func collectRecords(f *Full) []findings.Record {
	return f.Records()
}

// findProgress returns the progress record with the given phase, or nil.
func findProgress(recs []findings.Record, phase string) *findings.Record {
	for i := range recs {
		if recs[i].Kind == findings.KindProgress && recs[i].Progress != nil && recs[i].Progress.Phase == phase {
			return &recs[i]
		}
	}
	return nil
}

// hasAttackFired reports whether a behavior with the given name fired
// (its progress record appears in the collected records).
func hasAttackFired(recs []findings.Record, name string) bool {
	for _, r := range recs {
		if r.Kind == findings.KindProgress && r.Attack == name {
			return true
		}
	}
	return false
}

// --- recon → burst → follow-up → report sequencing ---

func TestOrchestrationSequencing(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	var fired []string
	var mu sync.Mutex
	behaviors := recordingBehaviors(&fired, &mu)

	ev := Evidence{
		EvRA6:       true,
		EvVLANs:     []int{10, 20, 30, 40},
		EvVoiceVLAN: 200,
		EvVTPDomain: "test",
		EvVTPRev:    3,
		EvMVRP:      true,
		EvMACs:      []string{"aa:bb:cc:dd:ee:ff"},
		EvVRIDs:     []int{1},
	}

	f := NewFull(FullConfig{
		AttackLeg: leg,
		Duration:  100 * time.Millisecond,
		ScanTime:  50 * time.Millisecond,
		Behaviors: behaviors,
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			return ev, nil
		},
	})

	err := f.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)

	// Phase ordering: recon → burst → follow-ups → report (summary).
	phases := []string{"recon", "burst", "follow-ups"}
	for i, ph := range phases {
		if findProgress(recs, ph) == nil {
			t.Errorf("phase %q (%d) not found in records", ph, i)
		}
	}
	hasSummary := false
	for _, r := range recs {
		if r.Kind == findings.KindSummary {
			hasSummary = true
		}
	}
	if !hasSummary {
		t.Errorf("no summary (report) record in output")
	}

	// Burst phase fired the core + armed workers.
	mu.Lock()
	firedCopy := append([]string(nil), fired...)
	mu.Unlock()

	// Core always fires.
	for _, name := range burstCore {
		if !hasAttackFired(recs, name) {
			t.Errorf("core worker %q did not fire in burst", name)
		}
	}
	// ra6 arms daddos.
	if !hasAttackFired(recs, "daddos") {
		t.Errorf("daddos did not fire (ra6 evidence present)")
	}
	// MACs + no --no-spoof arms arpspoof.
	if !hasAttackFired(recs, "arpspoof") {
		t.Errorf("arpspoof did not fire (MACs present, no --no-spoof)")
	}
	// vrids arms vrrp.
	if !hasAttackFired(recs, "vrrp") {
		t.Errorf("vrrp did not fire (vrids present)")
	}

	// Follow-ups fired (ra6 arms roguedhcp6 + raguard; vlans arms vlanhop
	// first three; voicevlan; vtp; mvrp; portsteal).
	for _, name := range []string{"roguedhcp6", "raguard", "vlanhop", "voicevlan", "vtp", "mvrp", "portsteal"} {
		if !hasAttackFired(recs, name) {
			t.Errorf("follow-up %q did not fire", name)
		}
	}

	_ = firedCopy
}

// --- burst composition: core always; ra6/MAC/vrid-armed ---

func TestBurstComposition_CoreAlways(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	// Empty evidence: only the core fires.
	f := NewFull(FullConfig{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Behaviors: allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			return Evidence{}, nil
		},
	})

	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	for _, name := range burstCore {
		if !hasAttackFired(recs, name) {
			t.Errorf("core worker %q did not fire on empty recon", name)
		}
	}
	// Armed workers do NOT fire on empty recon.
	for _, name := range []string{"daddos", "arpspoof", "vrrp"} {
		if hasAttackFired(recs, name) {
			t.Errorf("armed worker %q fired on empty recon (should not)", name)
		}
	}
}

func TestBurstComposition_RA6ArmsDaddos(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(FullConfig{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Behaviors: allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			return Evidence{EvRA6: true}, nil
		},
	})

	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	if !hasAttackFired(recs, "daddos") {
		t.Errorf("daddos did not fire (ra6 evidence present)")
	}
	// daddos is a BURST worker, NOT a follow-up: the follow-up phase
	// should not contain a separate daddos dispatch.
	if !hasAttackFired(recs, "roguedhcp6") || !hasAttackFired(recs, "raguard") {
		t.Errorf("ra6 follow-ups (roguedhcp6, raguard) should also fire")
	}
}

func TestBurstComposition_NoSpoofDisarmsArpspoof(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(FullConfig{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		NoSpoof:   true,
		Behaviors: allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			return Evidence{EvMACs: []string{"aa:bb:cc:dd:ee:ff"}}, nil
		},
	})

	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	if hasAttackFired(recs, "arpspoof") {
		t.Errorf("arpspoof fired with --no-spoof (should be disarmed)")
	}
	// portsteal is also disarmed by --no-spoof (resolved MACs arm it).
	if hasAttackFired(recs, "portsteal") {
		t.Errorf("portsteal fired with --no-spoof (should be disarmed)")
	}
}

func TestBurstComposition_VRRIDsArmVrrp(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(FullConfig{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Behaviors: allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			return Evidence{EvVRIDs: []int{10}}, nil
		},
	})

	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	if !hasAttackFired(recs, "vrrp") {
		t.Errorf("vrrp did not fire (vrids present)")
	}
}

// --- daddos is a burst worker, not a follow-up ---

func TestDaddosIsBurstWorkerNotFollowUp(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	var fired []string
	var mu sync.Mutex
	behaviors := recordingBehaviors(&fired, &mu)

	f := NewFull(FullConfig{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Behaviors: behaviors,
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			return Evidence{EvRA6: true}, nil
		},
	})

	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// daddos should appear exactly once (in the burst), not again in
	// follow-ups.
	count := 0
	for _, n := range fired {
		if n == "daddos" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("daddos fired %d times, want 1 (burst only, not follow-up)", count)
	}
}

// --- empty recon fires core + fallback chain ---

func TestEmptyReconFiresCoreAndFallback(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(FullConfig{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Behaviors: allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			return Evidence{}, nil
		},
	})

	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	// Core always fires.
	for _, name := range burstCore {
		if !hasAttackFired(recs, name) {
			t.Errorf("core worker %q did not fire on empty recon", name)
		}
	}
	// No follow-ups fire on empty recon.
	for _, name := range []string{"roguedhcp6", "raguard", "vlanhop", "voicevlan", "vtp", "mvrp", "ghost", "portsteal"} {
		if hasAttackFired(recs, name) {
			t.Errorf("follow-up %q fired on empty recon (should not)", name)
		}
	}
	// Summary record exists.
	hasSummary := false
	for _, r := range recs {
		if r.Kind == findings.KindSummary {
			hasSummary = true
		}
	}
	if !hasSummary {
		t.Errorf("no summary record in output")
	}
}

// --- traversal upgrade pending→confirmed on watch-leg recording ---

func TestTraversalPendingWithoutWatchLeg(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(FullConfig{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Behaviors: allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			return Evidence{EvRA6: true}, nil
		},
	})

	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// No watch leg: verdicts should be pending.
	f.mu.Lock()
	verds := append([]verdict(nil), f.verds...)
	f.mu.Unlock()

	if len(verds) == 0 {
		t.Fatal("no verdicts recorded")
	}
	for _, v := range verds {
		if v.kind != findings.KindPending {
			t.Errorf("verdict for %q: kind = %s, want pending (no watch leg)", v.attack, v.kind)
		}
	}
}

func TestTraversalConfirmedWithWatchLegRecording(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()
	wleg := newMockLeg()
	defer func() { _ = wleg.Close() }()

	f := NewFull(FullConfig{
		AttackLeg: leg,
		WatchLeg:  wleg,
		Duration:  50 * time.Millisecond,
		Behaviors: allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			// Simulate watch-leg recording (frames crossed the fabric).
			return Evidence{EvRA6: true, "watch-recording": true}, nil
		},
	})

	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	f.mu.Lock()
	verds := append([]verdict(nil), f.verds...)
	f.mu.Unlock()

	if len(verds) == 0 {
		t.Fatal("no verdicts recorded")
	}
	for _, v := range verds {
		if v.kind != findings.KindFinding {
			t.Errorf("verdict for %q: kind = %s, want finding (confirmed)", v.attack, v.kind)
		}
	}
}

func TestTraversalResistedWithWatchLegNoRecording(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()
	wleg := newMockLeg()
	defer func() { _ = wleg.Close() }()

	f := NewFull(FullConfig{
		AttackLeg: leg,
		WatchLeg:  wleg,
		Duration:  50 * time.Millisecond,
		Behaviors: allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			// Watch leg present but no recording (no frames crossed).
			return Evidence{EvRA6: true}, nil
		},
	})

	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	f.mu.Lock()
	verds := append([]verdict(nil), f.verds...)
	f.mu.Unlock()

	for _, v := range verds {
		if v.kind != findings.KindResisted {
			t.Errorf("verdict for %q: kind = %s, want resisted (watch leg, no traversal)", v.attack, v.kind)
		}
	}
}

// --- named-absent watch leg fails fast before recon ---

func TestNamedAbsentWatchLegFailsFast(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	reconCalled := false
	f := NewFull(FullConfig{
		AttackLeg:     leg,
		WatchLegNamed: "eth9",
		WatchLeg:      nil,
		Duration:      50 * time.Millisecond,
		Behaviors:     allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			reconCalled = true
			return Evidence{}, nil
		},
	})

	err := f.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for named-but-absent watch leg")
	}

	if reconCalled {
		t.Error("recon was called before the watch-leg check; should fail fast first")
	}

	// The error should carry the watch-leg-missing code.
	if c, ok := errs.CodeOf(err); !ok || c != catalog.ErrCodeWatchLegMissing {
		t.Errorf("error code: got %v, want %s", c, catalog.ErrCodeWatchLegMissing)
	}

	recs := collectRecords(f)
	hasErr := false
	for _, r := range recs {
		if r.Kind == findings.KindError && r.Error != nil && r.Error.Code == catalog.ErrCodeWatchLegMissing.String() {
			hasErr = true
		}
	}
	if !hasErr {
		t.Errorf("no error record with watch-leg-missing code in output")
	}
}

// --- SIGINT in phase 1 emits partial findings ---

func TestSIGINTInPhase1EmitsPartialFindings(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	ctx, cancel := context.WithCancel(context.Background())

	f := NewFull(FullConfig{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Behaviors: allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			// Simulate SIGINT arriving during recon: cancel the context.
			cancel()
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})

	err := f.Run(ctx)
	// Recon returning ctx.Err() is a runtime failure.
	if err == nil {
		t.Fatal("expected error from recon cancellation")
	}

	recs := collectRecords(f)
	// Partial findings: at least the recon-start progress record.
	hasPartial := false
	for _, r := range recs {
		if r.Kind == findings.KindProgress && r.Progress != nil && r.Progress.Phase == "recon" {
			hasPartial = true
		}
	}
	if !hasPartial {
		t.Errorf("no partial findings emitted on SIGINT during recon; got %d records", len(recs))
	}
}

// --- gate-map table test (both surfaces) ---

func TestGateMapBurstArming(t *testing.T) {
	cases := []struct {
		name    string
		ev      Evidence
		noSpoof bool
		want    []string // armed worker names (in order: core + armed)
	}{
		{
			name: "empty recon",
			ev:   Evidence{},
			want: append([]string(nil), burstCore...),
		},
		{
			name: "ra6 arms daddos",
			ev:   Evidence{EvRA6: true},
			want: append(append([]string(nil), burstCore...), "daddos"),
		},
		{
			name: "macs arm arpspoof",
			ev:   Evidence{EvMACs: []string{"aa:bb:cc:dd:ee:ff"}},
			want: append(append([]string(nil), burstCore...), "arpspoof"),
		},
		{
			name:    "macs + no-spoof disarms arpspoof",
			ev:      Evidence{EvMACs: []string{"aa:bb:cc:dd:ee:ff"}},
			noSpoof: true,
			want:    append([]string(nil), burstCore...),
		},
		{
			name: "vrids arm vrrp",
			ev:   Evidence{EvVRIDs: []int{1}},
			want: append(append([]string(nil), burstCore...), "vrrp"),
		},
		{
			name: "all armed",
			ev:   Evidence{EvRA6: true, EvMACs: []string{"aa"}, EvVRIDs: []int{1}},
			want: append(append([]string(nil), burstCore...), "daddos", "arpspoof", "vrrp"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := armBurst(tc.ev, tc.noSpoof)
			gotNames := make([]string, len(got))
			for i, r := range got {
				gotNames[i] = r.Name
			}
			if len(gotNames) != len(tc.want) {
				t.Fatalf("armed count: got %d (%v), want %d (%v)", len(gotNames), gotNames, len(tc.want), tc.want)
			}
			for i, name := range tc.want {
				if gotNames[i] != name {
					t.Errorf("armed[%d]: got %q, want %q", i, gotNames[i], name)
				}
			}
		})
	}
}

func TestGateMapFollowUpSelection(t *testing.T) {
	cases := []struct {
		name        string
		ev          Evidence
		hasWatchLeg bool
		wantNames   []string // follow-up names in order
	}{
		{
			name:      "empty recon, no watch leg",
			ev:        Evidence{},
			wantNames: nil,
		},
		{
			name:        "watch leg arms ghost",
			ev:          Evidence{},
			hasWatchLeg: true,
			wantNames:   []string{"ghost"},
		},
		{
			name:      "ra6 arms roguedhcp6 + raguard",
			ev:        Evidence{EvRA6: true},
			wantNames: []string{"roguedhcp6", "raguard"},
		},
		{
			name:      "vlans arms vlanhop first three",
			ev:        Evidence{EvVLANs: []int{10, 20, 30, 40}},
			wantNames: []string{"vlanhop", "vlanhop", "vlanhop"},
		},
		{
			name:      "voicevlan arms voicevlan",
			ev:        Evidence{EvVoiceVLAN: 200},
			wantNames: []string{"voicevlan"},
		},
		{
			name:      "vtp domain+rev arms vtp",
			ev:        Evidence{EvVTPDomain: "test", EvVTPRev: 3},
			wantNames: []string{"vtp"},
		},
		{
			name:      "vtp domain without rev does not arm",
			ev:        Evidence{EvVTPDomain: "test"},
			wantNames: nil,
		},
		{
			name:      "mvrp arms mvrp",
			ev:        Evidence{EvMVRP: true},
			wantNames: []string{"mvrp"},
		},
		{
			name:      "macs arm portsteal",
			ev:        Evidence{EvMACs: []string{"aa:bb:cc:dd:ee:ff"}},
			wantNames: []string{"portsteal"},
		},
		{
			name:        "all evidence + watch leg",
			ev:          Evidence{EvRA6: true, EvVLANs: []int{10, 20}, EvVoiceVLAN: 200, EvVTPDomain: "d", EvVTPRev: 1, EvMVRP: true, EvMACs: []string{"aa"}},
			hasWatchLeg: true,
			wantNames:   []string{"ghost", "vlanhop", "vlanhop", "voicevlan", "vtp", "roguedhcp6", "raguard", "mvrp", "portsteal"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := selectFollowUps(tc.ev, tc.hasWatchLeg, false)
			gotNames := make([]string, len(got))
			for i, fu := range got {
				gotNames[i] = fu.ref.Name
			}
			if len(gotNames) != len(tc.wantNames) {
				t.Fatalf("follow-up count: got %d (%v), want %d (%v)", len(gotNames), gotNames, len(tc.wantNames), tc.wantNames)
			}
			for i, name := range tc.wantNames {
				if gotNames[i] != name {
					t.Errorf("follow-up[%d]: got %q, want %q", i, gotNames[i], name)
				}
			}
		})
	}
}

// --- vtp is SAFE mode only (no permanent mode under orchestration) ---

func TestVTPIsSafeModeOnly(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(FullConfig{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Behaviors: allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			return Evidence{EvVTPDomain: "test", EvVTPRev: 3}, nil
		},
	})

	if err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	// vtp fires as the mode-less (SAFE) base, not as --wipe or --set.
	for _, r := range recs {
		if r.Attack == "vtp" && r.Mode != "" {
			t.Errorf("vtp fired with mode %q under orchestration; should be SAFE mode only", r.Mode)
		}
	}
}

// --- scan: named-absent watch leg fails fast ---

func TestScanNamedAbsentWatchLegFailsFast(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	scanCalled := false
	s := NewScan(ScanConfig{
		AttackLeg:     leg,
		WatchLegNamed: "eth9",
		ScanFn: func(_ context.Context, _ ScanConfig, emit func(findings.Record)) error {
			scanCalled = true
			return nil
		},
	})

	err := s.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for named-but-absent watch leg")
	}
	if scanCalled {
		t.Error("scan body was called before the watch-leg check")
	}
}

// --- scan: happy path emits findings ---

func TestScanHappyPath(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	s := NewScan(ScanConfig{
		AttackLeg: leg,
		Time:      10 * time.Millisecond,
		ScanFn: func(_ context.Context, _ ScanConfig, emit func(findings.Record)) error {
			emit(findings.NewRecord(findings.KindFinding))
			return nil
		},
	})

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := s.Records()
	if len(recs) < 2 {
		t.Fatalf("expected at least 2 records (progress + finding), got %d", len(recs))
	}
}

// --- recon returning an error surfaces it ---

func TestReconErrorSurfaces(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	reconErr := errors.New("recon boom")
	f := NewFull(FullConfig{
		AttackLeg: leg,
		Behaviors: allBehaviors(),
		ReconFn: func(_ context.Context, _ FullConfig) (Evidence, error) {
			return nil, reconErr
		},
	})

	err := f.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from recon failure")
	}
}
