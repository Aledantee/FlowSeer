package full

// full_test.go tests the four-phase orchestration over HOOKED behaviors.
// Tests substitute a recording catalog for the shipped one and a stub for
// the recon function, so the orchestration runs without a real interface.
// The scenarios cover the contract's test matrix:
//   - recon → burst → follow-up → report sequencing
//   - burst composition (core always; ra6/MAC/vrid-armed)
//   - daddos as concurrent burst worker (not follow-up)
//   - empty recon still fires core + fallback chain
//   - the summary counts the verdicts the phases' runners emit
//   - the ceiling refuses an entry while the rest of the burst runs
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
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

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

func (m *mockLeg) Addr() link.Addr { return link.Addr{} }

func (m *mockLeg) Close() error {
	m.closed.Store(true)
	close(m.frames)
	return nil
}

// stubCatalog returns the shipped catalog with every Run replaced by
// stub(name), so the orchestration dispatches real entries, with their
// impact and restore, without sending a frame. A stub of an actively
// restored entry arms one no-op step so the teardown gate admits it.
func stubCatalog(stub func(name string) check.Run) catalog.Catalog {
	bs := attacks.Behaviors()
	for i := range bs {
		if bs[i].Orchestration {
			continue
		}
		name := bs[i].Name
		inner := stub(name)
		bs[i].Run = func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
			if deps.Teardown != nil {
				deps.Teardown.Arm("test-restore", func(context.Context) error { return nil })
			}
			return inner(ctx, deps)
		}
	}
	return catalog.MustNew(bs...)
}

// recordingCatalog stubs every behavior to append its name to the fired
// list, emit a progress record, and return a pending verdict. This lets
// tests assert which behaviors fired and in what phase.
func recordingCatalog(fired *[]string, mu *sync.Mutex) catalog.Catalog {
	return stubCatalog(func(name string) check.Run {
		return func(_ context.Context, deps check.Deps) (check.Verdict, error) {
			mu.Lock()
			*fired = append(*fired, name)
			mu.Unlock()
			deps.Emitter.Progress("test", name+" fired")
			return check.Pending("stub"), nil
		}
	})
}

// allCatalog stubs every behavior to emit a progress record and return a
// pending verdict.
func allCatalog() catalog.Catalog {
	return stubCatalog(func(name string) check.Run {
		return func(_ context.Context, deps check.Deps) (check.Verdict, error) {
			deps.Emitter.Progress("test", name+" fired")
			return check.Pending("stub"), nil
		}
	})
}

// runFull runs the orchestrator while draining its live channel, as the
// command does, so a run whose records outnumber the channel's buffer
// cannot block on an unread channel.
func runFull(ctx context.Context, f *Full) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range f.RecordChan() {
		}
	}()
	err := f.Run(ctx)
	<-done
	return err
}

// collectRecords returns the Full orchestrator's records after Run.
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
		if r.Kind == findings.KindProgress && r.Attack == name && r.Progress != nil && r.Progress.Phase == "test" {
			return true
		}
	}
	return false
}

// summaryOf returns the run's summary record payload, or nil.
func summaryOf(recs []findings.Record) *findings.Summary {
	for _, r := range recs {
		if r.Kind == findings.KindSummary {
			return r.Summary
		}
	}
	return nil
}

func TestOrchestrationSequencing(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	var fired []string
	var mu sync.Mutex

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

	f := NewFull(Config{
		AttackLeg: leg,
		Duration:  100 * time.Millisecond,
		ScanTime:  50 * time.Millisecond,
		Catalog:   recordingCatalog(&fired, &mu),
		MaxImpact: check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			return ev, nil
		},
	})

	err := runFull(context.Background(), f)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)

	phases := []string{"recon", "burst", "follow-ups"}
	for i, ph := range phases {
		if findProgress(recs, ph) == nil {
			t.Errorf("phase %q (%d) not found in records", ph, i)
		}
	}
	if summaryOf(recs) == nil {
		t.Errorf("no summary (report) record in output")
	}

	for _, name := range burstCore {
		if !hasAttackFired(recs, name) {
			t.Errorf("core worker %q did not fire in burst", name)
		}
	}
	if !hasAttackFired(recs, "daddos") {
		t.Errorf("daddos did not fire (ra6 evidence present)")
	}
	if !hasAttackFired(recs, "arpspoof") {
		t.Errorf("arpspoof did not fire (MACs present, no --no-spoof)")
	}
	if !hasAttackFired(recs, "vrrp") {
		t.Errorf("vrrp did not fire (vrids present)")
	}

	for _, name := range []string{"roguedhcp6", "raguard", "vlanhop", "voicevlan", "vtp", "mvrp", "portsteal"} {
		if !hasAttackFired(recs, name) {
			t.Errorf("follow-up %q did not fire", name)
		}
	}
}

func TestBurstComposition_CoreAlways(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(Config{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Catalog:   allCatalog(),
		MaxImpact: check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			return Evidence{}, nil
		},
	})

	if err := runFull(context.Background(), f); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	for _, name := range burstCore {
		if !hasAttackFired(recs, name) {
			t.Errorf("core worker %q did not fire on empty recon", name)
		}
	}
	for _, name := range []string{"daddos", "arpspoof", "vrrp"} {
		if hasAttackFired(recs, name) {
			t.Errorf("armed worker %q fired on empty recon (should not)", name)
		}
	}
}

func TestBurstComposition_RA6ArmsDaddos(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(Config{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Catalog:   allCatalog(),
		MaxImpact: check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			return Evidence{EvRA6: true}, nil
		},
	})

	if err := runFull(context.Background(), f); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	if !hasAttackFired(recs, "daddos") {
		t.Errorf("daddos did not fire (ra6 evidence present)")
	}
	if !hasAttackFired(recs, "roguedhcp6") || !hasAttackFired(recs, "raguard") {
		t.Errorf("ra6 follow-ups (roguedhcp6, raguard) should also fire")
	}
}

func TestBurstComposition_NoSpoofDisarmsArpspoof(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(Config{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		NoSpoof:   true,
		Catalog:   allCatalog(),
		MaxImpact: check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			return Evidence{EvMACs: []string{"aa:bb:cc:dd:ee:ff"}}, nil
		},
	})

	if err := runFull(context.Background(), f); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	if hasAttackFired(recs, "arpspoof") {
		t.Errorf("arpspoof fired with --no-spoof (should be disarmed)")
	}
	if hasAttackFired(recs, "portsteal") {
		t.Errorf("portsteal fired with --no-spoof (should be disarmed)")
	}
}

func TestBurstComposition_VRRIDsArmVrrp(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(Config{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Catalog:   allCatalog(),
		MaxImpact: check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			return Evidence{EvVRIDs: []int{10}}, nil
		},
	})

	if err := runFull(context.Background(), f); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	if !hasAttackFired(recs, "vrrp") {
		t.Errorf("vrrp did not fire (vrids present)")
	}
}

func TestDaddosIsBurstWorkerNotFollowUp(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	var fired []string
	var mu sync.Mutex

	f := NewFull(Config{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Catalog:   recordingCatalog(&fired, &mu),
		MaxImpact: check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			return Evidence{EvRA6: true}, nil
		},
	})

	if err := runFull(context.Background(), f); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

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

func TestEmptyReconFiresCoreAndFallback(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(Config{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Catalog:   allCatalog(),
		MaxImpact: check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			return Evidence{}, nil
		},
	})

	if err := runFull(context.Background(), f); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	for _, name := range burstCore {
		if !hasAttackFired(recs, name) {
			t.Errorf("core worker %q did not fire on empty recon", name)
		}
	}
	for _, name := range []string{"roguedhcp6", "raguard", "vlanhop", "voicevlan", "vtp", "mvrp", "ghost", "portsteal"} {
		if hasAttackFired(recs, name) {
			t.Errorf("follow-up %q fired on empty recon (should not)", name)
		}
	}
	if summaryOf(recs) == nil {
		t.Errorf("no summary record in output")
	}
}

// TestSummaryCountsMixedVerdicts pins that the summary tallies the verdict
// records the phases' runners emitted, one column per kind: the burst's
// stubs return a mix of confirmed, resisted, pending, and skipped, and a
// stub whose target check fails is counted as skipped while the run
// continues.
func TestSummaryCountsMixedVerdicts(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	verdictFor := map[string]check.Verdict{
		"stproot":    check.Confirmed("root claimed", time.Second),
		"camflood":   check.Resisted("no fail-open", time.Second),
		"dhcpstarve": check.Pending("no observable"),
		"gratarp":    check.Pending("no observable"),
		"dtp":        check.Resisted("stayed access", time.Second),
		"roguera":    check.Confirmed("router accepted", time.Second),
	}
	f := NewFull(Config{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Catalog: stubCatalog(func(name string) check.Run {
			return func(_ context.Context, deps check.Deps) (check.Verdict, error) {
				if name == "llmnr" {
					return check.Verdict{}, deps.Target.Need(check.FieldVictim)
				}
				deps.Emitter.Finding("test", []byte(`{}`))
				return verdictFor[name], nil
			}
		}),
		MaxImpact: check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			return Evidence{}, nil
		},
	})

	if err := runFull(context.Background(), f); err != nil {
		t.Fatalf("Run: %v", err)
	}
	recs := collectRecords(f)
	got := summaryOf(recs)
	if got == nil {
		t.Fatal("no summary record")
	}
	want := findings.Summary{Attacks: 7, Findings: 6, Confirmed: 2, Resisted: 2, Pending: 2, Skipped: 1}
	if *got != want {
		t.Errorf("summary = %+v, want %+v", *got, want)
	}
	verdicts := 0
	for _, r := range recs {
		if r.Kind == findings.KindVerdict {
			verdicts++
		}
	}
	if verdicts != 7 {
		t.Errorf("verdict records = %d, want one per burst worker", verdicts)
	}
}

// TestCeilingRefusesWithinTheBurst pins that the orchestrator's ceiling
// reaches each phase's runner: under a ceiling of none, stproot is refused
// and counted as skipped while ghost, which changes nothing, runs on the
// watch leg.
func TestCeilingRefusesWithinTheBurst(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()
	watch := newMockLeg()
	defer func() { _ = watch.Close() }()

	var fired []string
	var mu sync.Mutex
	f := NewFull(Config{
		AttackLeg: leg,
		WatchLeg:  watch,
		Duration:  50 * time.Millisecond,
		Catalog:   recordingCatalog(&fired, &mu),
		MaxImpact: check.ImpactNone,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			return Evidence{}, nil
		},
	})

	if err := runFull(context.Background(), f); err != nil {
		t.Fatalf("Run: %v", err)
	}
	recs := collectRecords(f)
	if hasAttackFired(recs, "stproot") {
		t.Error("stproot fired under a ceiling of none")
	}
	if !hasAttackFired(recs, "ghost") {
		t.Error("ghost did not fire under a ceiling of none")
	}
	refused := false
	for _, r := range recs {
		if r.Kind == findings.KindRefusal && r.Attack == "stproot" && r.Refusal != nil && r.Refusal.Reason == "impact temporary exceeds ceiling none" {
			refused = true
		}
	}
	if !refused {
		t.Error("no refusal record for stproot naming the ceiling")
	}
	got := summaryOf(recs)
	if got == nil || got.Skipped != len(burstCore) || got.Pending != 1 {
		t.Errorf("summary = %+v, want the %d core workers skipped and ghost pending", got, len(burstCore))
	}
}

func TestNamedAbsentWatchLegFailsFast(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	reconCalled := false
	f := NewFull(Config{
		AttackLeg:     leg,
		WatchLegNamed: "eth9",
		WatchLeg:      nil,
		Duration:      50 * time.Millisecond,
		Catalog:       allCatalog(),
		MaxImpact:     check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			reconCalled = true
			return Evidence{}, nil
		},
	})

	err := runFull(context.Background(), f)
	if err == nil {
		t.Fatal("expected error for named-but-absent watch leg")
	}

	if reconCalled {
		t.Error("recon was called before the watch-leg check; should fail fast first")
	}

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

func TestSIGINTInPhase1EmitsPartialFindings(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	ctx, cancel := context.WithCancel(context.Background())

	f := NewFull(Config{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Catalog:   allCatalog(),
		MaxImpact: check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			cancel()
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})

	err := runFull(ctx, f)
	if err == nil {
		t.Fatal("expected error from recon cancellation")
	}

	recs := collectRecords(f)
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

func TestVTPIsSafeModeOnly(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	f := NewFull(Config{
		AttackLeg: leg,
		Duration:  50 * time.Millisecond,
		Catalog:   allCatalog(),
		MaxImpact: check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			return Evidence{EvVTPDomain: "test", EvVTPRev: 3}, nil
		},
	})

	if err := runFull(context.Background(), f); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs := collectRecords(f)
	for _, r := range recs {
		if r.Attack == "vtp" && r.Mode != "" {
			t.Errorf("vtp fired with mode %q under orchestration; should be SAFE mode only", r.Mode)
		}
	}
}

func TestScanNamedAbsentWatchLegFailsFast(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	scanCalled := false
	s := NewScan(ScanConfig{
		AttackLeg:     leg,
		WatchLegNamed: "eth9",
		ScanFn: func(_ context.Context, _ ScanConfig, _ func(findings.Record)) error {
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
		t.Fatalf("got %d records, want at least 2 (progress + finding)", len(recs))
	}
}

func TestReconErrorSurfaces(t *testing.T) {
	leg := newMockLeg()
	defer func() { _ = leg.Close() }()

	reconErr := errors.New("recon boom")
	f := NewFull(Config{
		AttackLeg: leg,
		Catalog:   allCatalog(),
		MaxImpact: check.ImpactTemporary,
		ReconFn: func(_ context.Context, _ Config) (Evidence, error) {
			return nil, reconErr
		},
	})

	err := runFull(context.Background(), f)
	if err == nil {
		t.Fatal("expected error from recon failure")
	}
}
