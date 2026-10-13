package rawwindowapi_test

import (
	"context"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/rawwindowapi"
)

const (
	tenantA = "0192e6a0-8888-7000-8000-000000000088"
	tenantB = "0192e6a0-8888-7000-8000-000000000099"

	epochOne   = "0192e6a0-0000-7000-8000-00000000e001"
	epochTwo   = "0192e6a0-0000-7000-8000-00000000e002"
	epochThree = "0192e6a0-0000-7000-8000-00000000e003"
)

// testKV is the raw-windows bucket of one hub shared by the whole package. Every
// test names its own edge, so the keys never meet.
var testKV jetstream.KeyValue

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "rawwindowapi-")
	if err != nil {
		return failSetup("create state dir", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    dir,
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  0,
	})
	if err != nil {
		return failSetup("start hub", err)
	}
	defer hub.Close()

	testKV, err = hub.JetStream().KeyValue(context.Background(), edgebus.RawWindowsBucket)
	if err != nil {
		return failSetup("open the raw-windows bucket", err)
	}
	return m.Run()
}

func failSetup(what string, err error) int {
	_, _ = os.Stderr.WriteString("rawwindowapi tests: " + what + ": " + err.Error() + "\n")
	return 1
}

type testClock struct{ nanos atomic.Int64 }

func newClock() *testClock {
	c := &testClock{}
	c.nanos.Store(time.Now().UnixNano())
	return c
}

func (c *testClock) Now() time.Time { return time.Unix(0, c.nanos.Load()).UTC() }

func (c *testClock) Advance(d time.Duration) { c.nanos.Add(int64(d)) }

// fixture is one edge, subscribed or not, with a store whose clock the test
// moves.
type fixture struct {
	t     *testing.T
	clock *testClock
	store *rawwindowapi.Store
	edge  string
	// enrollment is what subscribe passes as the edge's enrollment.
	enrollment string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	clock := newClock()
	return &fixture{t: t, clock: clock, store: rawwindowapi.NewStore(testKV, clock.Now), edge: uuid.NewString()}
}

// subscribedFixture is an edge whose first process, generation 1 and epoch
// one, runs the syslog source.
func subscribedFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.mustSubscribe(1, epochOne, "syslog")
	return f
}

func sources(keys ...string) []*rawwindowv1.RawWindowSource {
	out := make([]*rawwindowv1.RawWindowSource, len(keys))
	for i, key := range keys {
		out[i] = rawwindowv1.RawWindowSource_builder{Key: new(key)}.Build()
	}
	return out
}

func (f *fixture) subscribe(generation uint64, epoch string, keys ...string) (int, error) {
	f.t.Helper()
	return f.store.Subscribe(f.t.Context(), tenantA, f.edge, f.enrollment, generation, epoch, sources(keys...))
}

func (f *fixture) mustSubscribe(generation uint64, epoch string, keys ...string) int {
	f.t.Helper()
	ended, err := f.subscribe(generation, epoch, keys...)
	if err != nil {
		f.t.Fatalf("subscribe generation %d: %v", generation, err)
	}
	return ended
}

func sourceSelector(key string) *rawwindowv1.RawWindowSelector {
	return rawwindowv1.RawWindowSelector_builder{Source: rawwindowv1.RawWindowSource_builder{Key: new(key)}.Build()}.Build()
}

func authorization(reason string) *rawwindowv1.RawWindowAuthorization {
	return rawwindowv1.RawWindowAuthorization_builder{
		RequestedBy: identityv1.OperatorRef_builder{Issuer: new("https://auth.example.test"), Subject: new("operator-1")}.Build(),
		Reason:      new(reason),
	}.Build()
}

// request is an admissible open: a syslog window of 100 bytes that ends in
// thirty minutes, under an allowance far above it.
func (f *fixture) request() rawwindowapi.OpenRequest {
	return rawwindowapi.OpenRequest{
		EdgeID:         f.edge,
		Selector:       sourceSelector("syslog"),
		ExpiresAt:      f.clock.Now().Add(30 * time.Minute),
		MaxBytes:       100,
		Authorization:  authorization("reproduce the parse failure"),
		AllowanceBytes: 10_000,
	}
}

func (f *fixture) tryOpen(edit ...func(*rawwindowapi.OpenRequest)) (*rawwindowv1.RawWindowRecord, error) {
	f.t.Helper()
	req := f.request()
	for _, fn := range edit {
		fn(&req)
	}
	return f.store.Open(f.t.Context(), tenantA, req)
}

func (f *fixture) open(edit ...func(*rawwindowapi.OpenRequest)) *rawwindowv1.RawWindowRecord {
	f.t.Helper()
	window, err := f.tryOpen(edit...)
	if err != nil {
		f.t.Fatalf("open: %v", err)
	}
	if err := protovalidate.Validate(window); err != nil {
		f.t.Fatalf("opened window violates its schema: %v", err)
	}
	return window
}

func windowID(window *rawwindowv1.RawWindowRecord) string {
	return window.GetConfig().GetRef().GetRawWindow().GetId()
}

// stored reads the persisted edge record and checks it against its schema, so
// every test that looks at the bucket also proves the bucket holds valid data.
func (f *fixture) stored() *storev1.RawWindowEdgeRecord {
	f.t.Helper()
	entry, err := testKV.Get(f.t.Context(), tenantA+"."+f.edge)
	if err != nil {
		f.t.Fatalf("read the stored record: %v", err)
	}
	rec := &storev1.RawWindowEdgeRecord{}
	if err := proto.Unmarshal(entry.Value(), rec); err != nil {
		f.t.Fatalf("decode the stored record: %v", err)
	}
	if err := protovalidate.Validate(rec); err != nil {
		f.t.Fatalf("stored record violates its schema: %v", err)
	}
	return rec
}

func (f *fixture) storedEntry(window *rawwindowv1.RawWindowRecord) *storev1.RawWindowEntry {
	f.t.Helper()
	for _, entry := range f.stored().GetWindows() {
		if entry.GetConfig().GetRef().GetRawWindow().GetId() == windowID(window) {
			return entry
		}
	}
	f.t.Fatalf("window %s is not in the stored record", windowID(window))
	return nil
}

func (f *fixture) window(window *rawwindowv1.RawWindowRecord) *rawwindowv1.RawWindowRecord {
	f.t.Helper()
	got, err := f.store.Window(f.t.Context(), tenantA, windowID(window))
	if err != nil {
		f.t.Fatalf("read window: %v", err)
	}
	return got
}

func (f *fixture) owed(generation uint64, epoch string) []*edgeingestv1.RawWindowControl {
	f.t.Helper()
	owed, err := f.store.Owed(f.t.Context(), tenantA, f.edge, generation, epoch)
	if err != nil {
		f.t.Fatalf("owed: %v", err)
	}
	return owed
}

func (f *fixture) report(generation uint64, epoch string, reports ...*edgeingestv1.RawWindowReport) error {
	f.t.Helper()
	return f.store.Report(f.t.Context(), tenantA, f.edge, generation, epoch, reports)
}

func (f *fixture) mustReport(reports ...*edgeingestv1.RawWindowReport) {
	f.t.Helper()
	if err := f.report(1, epochOne, reports...); err != nil {
		f.t.Fatalf("report: %v", err)
	}
}

func (f *fixture) fillLiveWindows(n int) []*rawwindowv1.RawWindowRecord {
	f.t.Helper()
	windows := make([]*rawwindowv1.RawWindowRecord, n)
	for i := range windows {
		windows[i] = f.open()
	}
	return windows
}

// codeOf is the code of err, or the empty code for a nil error, which no
// caller's comparison accepts.
func codeOf(t *testing.T, err error) errs.Code {
	t.Helper()
	if err == nil {
		return ""
	}
	code, ok := errs.CodeOf(err)
	if !ok {
		t.Fatalf("error %v carries no code", err)
	}
	return code
}

func wantCode(t *testing.T, err error, want errs.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %s", want)
	}
	if got := codeOf(t, err); got != want {
		t.Fatalf("error code = %s (%v), want %s", got, err, want)
	}
}

func usage(bytes, records uint64) *rawwindowv1.RawWindowUsage {
	return rawwindowv1.RawWindowUsage_builder{SelectedBytes: new(bytes), SelectedRecords: new(records)}.Build()
}

// activeReport is what an edge process reports for a window it is collecting.
func activeReport(window *rawwindowv1.RawWindowRecord, revision uint64, u *rawwindowv1.RawWindowUsage, activatedAt time.Time) *edgeingestv1.RawWindowReport {
	return edgeingestv1.RawWindowReport_builder{
		Revision: new(revision),
		State: rawwindowv1.RawWindowState_builder{
			Ref:         window.GetConfig().GetRef(),
			Lifecycle:   rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE.Enum(),
			ActivatedAt: timestamppb.New(activatedAt),
			Usage:       u,
		}.Build(),
	}.Build()
}

func terminalReport(window *rawwindowv1.RawWindowRecord, revision uint64, reason rawwindowv1.RawWindowStopReason, u *rawwindowv1.RawWindowUsage, endedAt time.Time) *edgeingestv1.RawWindowReport {
	return edgeingestv1.RawWindowReport_builder{
		Revision: new(revision),
		State: rawwindowv1.RawWindowState_builder{
			Ref:        window.GetConfig().GetRef(),
			Lifecycle:  rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL.Enum(),
			StopReason: reason.Enum(),
			EndedAt:    timestamppb.New(endedAt),
			Usage:      u,
		}.Build(),
	}.Build()
}

func TestOpenRefusesAnEdgeThatNeverSubscribed(t *testing.T) {
	f := newFixture(t)

	_, err := f.tryOpen()

	wantCode(t, err, rawwindowapi.ErrCodeNotSubscribed)
}

func TestFirstSubscribeCreatesTheRecord(t *testing.T) {
	f := newFixture(t)

	if ended := f.mustSubscribe(1, epochOne, "syslog", "netflow"); ended != 0 {
		t.Errorf("first subscribe ended %d windows, want 0", ended)
	}

	rec := f.stored()
	if rec.GetBootGeneration() != 1 || rec.GetProcessEpoch() != epochOne {
		t.Errorf("fence = generation %d epoch %s, want 1 and %s", rec.GetBootGeneration(), rec.GetProcessEpoch(), epochOne)
	}
	if got := len(rec.GetEnabledSources()); got != 2 || rec.GetEnabledSources()[1].GetKey() != "netflow" {
		t.Errorf("enabled sources = %v, want syslog and netflow", rec.GetEnabledSources())
	}
	if !rec.HasControlRevision() || rec.GetControlRevision() != 0 {
		t.Errorf("control revision = %d (present %v), want a present 0", rec.GetControlRevision(), rec.HasControlRevision())
	}
}

func TestSubscribeFencesOlderProcesses(t *testing.T) {
	cases := []struct {
		name       string
		generation uint64
		epoch      string
	}{
		{name: "lower generation", generation: 1, epoch: epochOne},
		{name: "same generation with another epoch", generation: 2, epoch: epochThree},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.mustSubscribe(2, epochTwo, "syslog")

			_, err := f.subscribe(tc.generation, tc.epoch, "syslog")

			wantCode(t, err, rawwindowapi.ErrCodeStaleFence)
			if rec := f.stored(); rec.GetBootGeneration() != 2 || rec.GetProcessEpoch() != epochTwo {
				t.Errorf("a refused subscribe moved the fence to generation %d epoch %s", rec.GetBootGeneration(), rec.GetProcessEpoch())
			}
		})
	}
}

func TestReenrolledEdgeIsAcceptedAsANewProcess(t *testing.T) {
	f := newFixture(t)
	f.enrollment = "enrollment-one"
	f.mustSubscribe(5, epochTwo, "syslog")
	window := f.open()

	// A replacement box enrolls as the same edge and counts its boots from one.
	f.enrollment = "enrollment-two"
	ended, err := f.subscribe(1, epochThree, "syslog")
	if err != nil {
		t.Fatalf("subscribe after re-enrollment: %v", err)
	}

	if ended != 1 {
		t.Errorf("ended = %d, want the previous enrollment's window", ended)
	}
	rec := f.stored()
	if rec.GetBootGeneration() != 1 || rec.GetProcessEpoch() != epochThree || rec.GetEnrollment() != "enrollment-two" {
		t.Errorf("record = generation %d epoch %s enrollment %q, want the request adopted", rec.GetBootGeneration(), rec.GetProcessEpoch(), rec.GetEnrollment())
	}
	if got := f.storedEntry(window).GetState().GetStopReason(); got != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EDGE_RESTART {
		t.Errorf("stop reason = %v, want EDGE_RESTART", got)
	}

	// Within the new enrollment the fence holds again.
	_, err = f.subscribe(1, epochTwo, "syslog")
	wantCode(t, err, rawwindowapi.ErrCodeStaleFence)
}

func TestReconnectInTheSameEpochRefreshesOnlyTheSources(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()
	f.mustReport(activeReport(window, 1, usage(40, 2), f.clock.Now()))

	if ended := f.mustSubscribe(1, epochOne, "syslog", "netflow"); ended != 0 {
		t.Errorf("a reconnect ended %d windows, want 0", ended)
	}

	rec := f.stored()
	if len(rec.GetEnabledSources()) != 2 {
		t.Errorf("enabled sources = %v, want the refreshed two", rec.GetEnabledSources())
	}
	got := f.window(window).GetState()
	if got.GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE || got.GetUsage().GetSelectedBytes() != 40 {
		t.Errorf("window after reconnect = %v, want active with its 40 bytes", got)
	}
}

func TestANewGenerationEndsEveryLiveWindowAndReissuesNothing(t *testing.T) {
	f := subscribedFixture(t)
	pending := f.open()
	active := f.open()
	f.mustReport(activeReport(active, 2, usage(10, 1), f.clock.Now()))
	finished := f.open()
	f.mustReport(terminalReport(finished, 3, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_BYTE_BUDGET, usage(100, 5), f.clock.Now()))
	f.clock.Advance(time.Minute)

	if ended := f.mustSubscribe(2, epochTwo, "syslog"); ended != 2 {
		t.Fatalf("new generation ended %d windows, want 2", ended)
	}

	for _, window := range []*rawwindowv1.RawWindowRecord{pending, active} {
		state := f.window(window).GetState()
		if state.GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL ||
			state.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EDGE_RESTART ||
			!state.GetEndedAt().AsTime().Equal(f.clock.Now()) {
			t.Errorf("window %s state = %v, want terminal EDGE_RESTART ended now", windowID(window), state)
		}
		entry := f.storedEntry(window)
		if entry.GetReportedRevision() != entry.GetRevision() {
			t.Errorf("window %s reported revision = %d, want its revision %d so nothing is owed", windowID(window), entry.GetReportedRevision(), entry.GetRevision())
		}
	}
	if reason := f.window(finished).GetState().GetStopReason(); reason != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_BYTE_BUDGET {
		t.Errorf("an already terminal window's reason became %v", reason)
	}
	if owed := f.owed(2, epochTwo); len(owed) != 0 {
		t.Errorf("owed controls after a restart = %v, want none", owed)
	}
}

func TestOpenAdmissionReportsTheFirstFailureInOrder(t *testing.T) {
	unknownSelector := func(context.Context) error {
		return errs.New().Code(rawwindowapi.ErrCodeUnknownSelector).Msg("no such device")
	}
	cases := []struct {
		name       string
		subscribed bool
		live       int
		edit       func(f *fixture, req *rawwindowapi.OpenRequest)
		want       errs.Code
	}{
		{
			name: "an unsubscribed edge before everything else",
			edit: func(f *fixture, req *rawwindowapi.OpenRequest) {
				req.Selector = sourceSelector("nope")
				req.ExpiresAt = f.clock.Now().Add(-time.Minute)
				req.AllowanceBytes = 0
			},
			want: rawwindowapi.ErrCodeNotSubscribed,
		},
		{
			name:       "an unadvertised source before the selector lookup",
			subscribed: true,
			edit: func(f *fixture, req *rawwindowapi.OpenRequest) {
				req.Selector = sourceSelector("nope")
				req.ResolveSelector = unknownSelector
				req.ExpiresAt = f.clock.Now().Add(-time.Minute)
				req.AllowanceBytes = 0
			},
			want: rawwindowapi.ErrCodeUnsupportedSource,
		},
		{
			name:       "an unknown selector before the expiry",
			subscribed: true,
			edit: func(f *fixture, req *rawwindowapi.OpenRequest) {
				req.ResolveSelector = unknownSelector
				req.ExpiresAt = f.clock.Now().Add(-time.Minute)
				req.AllowanceBytes = 0
			},
			want: rawwindowapi.ErrCodeUnknownSelector,
		},
		{
			name:       "a bad expiry before the allowance",
			subscribed: true,
			edit: func(f *fixture, req *rawwindowapi.OpenRequest) {
				req.ExpiresAt = f.clock.Now().Add(-time.Minute)
				req.AllowanceBytes = 0
			},
			want: rawwindowapi.ErrCodeBadExpiry,
		},
		{
			name:       "a missing allowance before the window limit",
			subscribed: true,
			live:       16,
			edit: func(_ *fixture, req *rawwindowapi.OpenRequest) {
				req.AllowanceBytes = 0
			},
			want: rawwindowapi.ErrCodeNoAllowance,
		},
		{
			name:       "the window limit before the byte budget",
			subscribed: true,
			live:       16,
			edit: func(_ *fixture, req *rawwindowapi.OpenRequest) {
				req.MaxBytes = req.AllowanceBytes + 1
			},
			want: rawwindowapi.ErrCodeWindowLimit,
		},
		{
			name:       "the byte budget last",
			subscribed: true,
			edit: func(_ *fixture, req *rawwindowapi.OpenRequest) {
				req.MaxBytes = req.AllowanceBytes + 1
			},
			want: rawwindowapi.ErrCodeBudgetExceeded,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.subscribed {
				f.mustSubscribe(1, epochOne, "syslog")
			}
			f.fillLiveWindows(tc.live)

			_, err := f.tryOpen(func(req *rawwindowapi.OpenRequest) { tc.edit(f, req) })

			wantCode(t, err, tc.want)
		})
	}
}

func TestOpenExpiryBoundary(t *testing.T) {
	f := subscribedFixture(t)
	now := f.clock.Now()

	for _, tc := range []struct {
		name    string
		expires time.Time
		want    errs.Code
	}{
		{name: "now", expires: now, want: rawwindowapi.ErrCodeBadExpiry},
		{name: "one tick after now", expires: now.Add(time.Nanosecond)},
		{name: "exactly one hour", expires: now.Add(time.Hour)},
		{name: "one tick beyond one hour", expires: now.Add(time.Hour + time.Nanosecond), want: rawwindowapi.ErrCodeBadExpiry},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.tryOpen(func(req *rawwindowapi.OpenRequest) {
				req.ExpiresAt = tc.expires
				req.AllowanceBytes = 1_000_000
			})

			if tc.want == "" {
				if err != nil {
					t.Fatalf("open expiring at %v: %v", tc.expires.Sub(now), err)
				}
				return
			}
			wantCode(t, err, tc.want)
		})
	}
}

func TestOpenRecordsAPendingWindowWithItsOwnRevision(t *testing.T) {
	f := subscribedFixture(t)

	first := f.open()
	second := f.open()

	for i, window := range []*rawwindowv1.RawWindowRecord{first, second} {
		if got := window.GetState().GetLifecycle(); got != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
			t.Errorf("window %d lifecycle = %v, want pending", i, got)
		}
		if got := window.GetConfig().GetRef().GetEdge().GetEdge().GetId(); got != f.edge {
			t.Errorf("window %d edge = %s, want %s", i, got, f.edge)
		}
		if _, err := uuid.Parse(windowID(window)); err != nil {
			t.Errorf("window %d id %q is not a uuid", i, windowID(window))
		}
	}
	if windowID(first) == windowID(second) {
		t.Error("two windows share one id")
	}
	rec := f.stored()
	if rec.GetControlRevision() != 2 {
		t.Errorf("control revision = %d, want 2", rec.GetControlRevision())
	}
	if got := f.storedEntry(first).GetRevision(); got != 1 {
		t.Errorf("first window revision = %d, want 1", got)
	}
	if got := f.storedEntry(second).GetRevision(); got != 2 {
		t.Errorf("second window revision = %d, want 2", got)
	}
}

func TestConcurrentOpensRaceAtTheLiveWindowLimit(t *testing.T) {
	f := subscribedFixture(t)
	const racers = 20

	results := make(chan error, racers)
	var wg sync.WaitGroup
	for range racers {
		wg.Go(func() {
			_, err := f.store.Open(t.Context(), tenantA, f.request())
			results <- err
		})
	}
	wg.Wait()
	close(results)

	admitted, refused := 0, 0
	for err := range results {
		switch {
		case err == nil:
			admitted++
		case codeOf(t, err) == rawwindowapi.ErrCodeWindowLimit:
			refused++
		default:
			t.Errorf("open failed with %v, want admitted or the window limit", err)
		}
	}
	if admitted != 16 || refused != racers-16 {
		t.Fatalf("admitted %d and refused %d, want 16 and %d", admitted, refused, racers-16)
	}

	rec := f.stored()
	if len(rec.GetWindows()) != 16 || rec.GetControlRevision() != 16 {
		t.Fatalf("stored %d windows at control revision %d, want 16 at 16", len(rec.GetWindows()), rec.GetControlRevision())
	}
	seen := map[uint64]bool{}
	for _, entry := range rec.GetWindows() {
		seen[entry.GetRevision()] = true
	}
	if len(seen) != 16 {
		t.Errorf("windows drew %d distinct revisions, want 16", len(seen))
	}
}

func TestAggregateByteAdmission(t *testing.T) {
	f := subscribedFixture(t)
	budget := func(n uint64) func(*rawwindowapi.OpenRequest) {
		return func(req *rawwindowapi.OpenRequest) { req.MaxBytes, req.AllowanceBytes = n, 10 }
	}

	f.open(budget(6))
	f.open(budget(4))

	_, err := f.tryOpen(budget(1))
	wantCode(t, err, rawwindowapi.ErrCodeBudgetExceeded)
	if got := len(f.stored().GetWindows()); got != 2 {
		t.Errorf("a refused open left %d windows, want 2", got)
	}
}

func TestAnOverflowingBudgetIsRefusedNotWrapped(t *testing.T) {
	f := subscribedFixture(t)
	budget := func(n uint64) func(*rawwindowapi.OpenRequest) {
		return func(req *rawwindowapi.OpenRequest) { req.MaxBytes, req.AllowanceBytes = n, math.MaxUint64 }
	}
	f.open(budget(5))

	_, err := f.tryOpen(budget(math.MaxUint64))

	wantCode(t, err, rawwindowapi.ErrCodeBudgetExceeded)
}

func TestWithoutAnAllowanceNothingOpens(t *testing.T) {
	f := subscribedFixture(t)

	_, err := f.tryOpen(func(req *rawwindowapi.OpenRequest) { req.AllowanceBytes = 0 })

	wantCode(t, err, rawwindowapi.ErrCodeNoAllowance)
	if got := len(f.stored().GetWindows()); got != 0 {
		t.Errorf("a refused open stored %d windows, want 0", got)
	}
}

func TestAReservationOutlivesALostCloseAndReportedUsage(t *testing.T) {
	f := subscribedFixture(t)
	tenBytes := func(req *rawwindowapi.OpenRequest) { req.MaxBytes, req.AllowanceBytes = 10, 10 }
	oneByte := func(req *rawwindowapi.OpenRequest) { req.MaxBytes, req.AllowanceBytes = 1, 10 }
	window := f.open(tenBytes)

	if _, err := f.tryOpen(oneByte); codeOf(t, err) != rawwindowapi.ErrCodeBudgetExceeded {
		t.Fatalf("a second window beside the full reservation: %v, want the budget refusal", err)
	}

	f.mustReport(activeReport(window, 1, usage(10, 3), f.clock.Now()))
	if _, err := f.tryOpen(oneByte); codeOf(t, err) != rawwindowapi.ErrCodeBudgetExceeded {
		t.Fatalf("after the edge reported all ten bytes spent: %v, want the budget refusal", err)
	}

	if _, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(window)); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := f.tryOpen(oneByte); codeOf(t, err) != rawwindowapi.ErrCodeBudgetExceeded {
		t.Fatalf("with the close not yet delivered: %v, want the budget refusal", err)
	}

	f.mustReport(terminalReport(window, 2, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_OPERATOR, usage(10, 3), f.clock.Now()))
	f.open(oneByte)
}

func TestExpiryReleasesAReservation(t *testing.T) {
	f := subscribedFixture(t)
	tenBytes := func(req *rawwindowapi.OpenRequest) { req.MaxBytes, req.AllowanceBytes = 10, 10 }
	f.open(tenBytes)

	f.clock.Advance(31 * time.Minute)

	f.open(tenBytes)
}

func TestRestartingCentralKeepsTheWindowsAndWhatIsOwed(t *testing.T) {
	f := subscribedFixture(t)
	first := f.open()
	second := f.open()
	f.mustReport(activeReport(first, 1, usage(7, 1), f.clock.Now()))
	if _, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(first)); err != nil {
		t.Fatalf("close: %v", err)
	}

	restarted := &fixture{t: t, clock: f.clock, store: rawwindowapi.NewStore(testKV, f.clock.Now), edge: f.edge}

	owed := restarted.owed(1, epochOne)
	if len(owed) != 2 {
		t.Fatalf("owed after a restart = %d controls, want 2", len(owed))
	}
	if owed[0].GetRef().GetRawWindow().GetId() != windowID(first) || owed[0].GetClose() == nil || owed[0].GetRevision() != 3 {
		t.Errorf("first control = %v, want the close of the first window at revision 3", owed[0])
	}
	if owed[1].GetRef().GetRawWindow().GetId() != windowID(second) || owed[1].GetOpen() == nil || owed[1].GetRevision() != 2 {
		t.Errorf("second control = %v, want the open of the second window at revision 2", owed[1])
	}
	if got := restarted.window(first).GetState().GetUsage().GetSelectedBytes(); got != 7 {
		t.Errorf("usage after a restart = %d, want the 7 bytes reported before it", got)
	}
}

func TestOwedControlsAreOpensAndClosesAtTheLatestRevision(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()

	opens := f.owed(1, epochOne)
	if len(opens) != 1 || opens[0].GetOpen() == nil || opens[0].GetRevision() != 1 ||
		!proto.Equal(opens[0].GetOpen(), window.GetConfig()) {
		t.Fatalf("owed = %v, want one open at revision 1 carrying the window's configuration", opens)
	}
	if opens[0].GetBootGeneration() != 1 || opens[0].GetProcessEpoch() != epochOne {
		t.Errorf("control fence = %d/%s, want 1/%s", opens[0].GetBootGeneration(), opens[0].GetProcessEpoch(), epochOne)
	}
	if err := protovalidate.Validate(opens[0]); err != nil {
		t.Errorf("owed open violates its schema: %v", err)
	}

	if _, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(window)); err != nil {
		t.Fatalf("close: %v", err)
	}

	closes := f.owed(1, epochOne)
	if len(closes) != 1 || closes[0].GetClose() == nil || closes[0].GetOpen() != nil || closes[0].GetRevision() != 2 {
		t.Fatalf("owed after close = %v, want only a close at revision 2", closes)
	}
	if !closes[0].GetClose().GetExpiresAt().AsTime().Equal(window.GetConfig().GetExpiresAt().AsTime()) {
		t.Errorf("close expiry = %v, want the original %v", closes[0].GetClose().GetExpiresAt().AsTime(), window.GetConfig().GetExpiresAt().AsTime())
	}
	if err := protovalidate.Validate(closes[0]); err != nil {
		t.Errorf("owed close violates its schema: %v", err)
	}
}

func TestAnotherWindowsRevisionNeverDiscardsAnUndeliveredStart(t *testing.T) {
	f := subscribedFixture(t)
	first := f.open()
	second := f.open()
	if _, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(second)); err != nil {
		t.Fatalf("close: %v", err)
	}

	owed := f.owed(1, epochOne)

	if len(owed) != 2 {
		t.Fatalf("owed = %d controls, want 2", len(owed))
	}
	if owed[0].GetRef().GetRawWindow().GetId() != windowID(first) || owed[0].GetOpen() == nil || owed[0].GetRevision() != 1 {
		t.Errorf("first control = %v, want the undelivered open at revision 1", owed[0])
	}
	if owed[1].GetRef().GetRawWindow().GetId() != windowID(second) || owed[1].GetClose() == nil || owed[1].GetRevision() != 3 {
		t.Errorf("second control = %v, want the close at revision 3", owed[1])
	}
}

func TestOwedRefusesAProcessThatWasSuperseded(t *testing.T) {
	f := subscribedFixture(t)
	f.mustSubscribe(2, epochTwo, "syslog")

	_, err := f.store.Owed(t.Context(), tenantA, f.edge, 1, epochOne)

	wantCode(t, err, rawwindowapi.ErrCodeSuperseded)
}

func TestCloseIsDurableBeforeAnyDelivery(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()
	f.clock.Advance(time.Minute)

	closed, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(window))
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	entry := f.storedEntry(window)
	if !entry.HasCloseRequestedAt() || !entry.GetCloseRequestedAt().AsTime().Equal(f.clock.Now()) {
		t.Errorf("close_requested_at = %v, want now", entry.GetCloseRequestedAt())
	}
	if entry.GetRevision() != 2 || f.stored().GetControlRevision() != 2 {
		t.Errorf("window revision %d and control revision %d, want both 2", entry.GetRevision(), f.stored().GetControlRevision())
	}
	if closed.GetState().GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
		t.Errorf("a closed window not yet reported is %v, want it still pending", closed.GetState().GetLifecycle())
	}
}

func TestClosingTwiceOrClosingATerminalWindowChangesNothing(t *testing.T) {
	f := subscribedFixture(t)
	closedTwice := f.open()
	finished := f.open()
	f.mustReport(terminalReport(finished, 2, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_BYTE_BUDGET, usage(100, 4), f.clock.Now()))
	if _, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(closedTwice)); err != nil {
		t.Fatalf("first close: %v", err)
	}
	before := f.stored()

	again, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(closedTwice))
	if err != nil {
		t.Fatalf("second close: %v", err)
	}
	ended, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(finished))
	if err != nil {
		t.Fatalf("close of a terminal window: %v", err)
	}

	if !proto.Equal(before, f.stored()) {
		t.Error("closing a closed or a terminal window rewrote the record")
	}
	if windowID(again) != windowID(closedTwice) || windowID(ended) != windowID(finished) {
		t.Error("a no-op close returned another window")
	}
	if got := ended.GetState().GetStopReason(); got != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_BYTE_BUDGET {
		t.Errorf("closing a terminal window changed its reason to %v", got)
	}
}

func TestClosingAnUnknownWindowIsNotFound(t *testing.T) {
	f := subscribedFixture(t)

	_, err := f.store.Close(t.Context(), tenantA, f.edge, uuid.NewString())

	wantCode(t, err, rawwindowapi.ErrCodeNotFound)
}

func TestACloseSupersedesAnUndeliveredStart(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()
	if _, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(window)); err != nil {
		t.Fatalf("close: %v", err)
	}

	owed := f.owed(1, epochOne)

	if len(owed) != 1 || owed[0].GetClose() == nil || owed[0].GetRevision() != 2 {
		t.Fatalf("owed = %v, want exactly the close at revision 2 and no start", owed)
	}
}

func TestAClosedWindowHoldsItsSlotUntilItsOriginalExpiry(t *testing.T) {
	f := subscribedFixture(t)
	windows := f.fillLiveWindows(16)
	if _, err := f.tryOpen(); codeOf(t, err) != rawwindowapi.ErrCodeWindowLimit {
		t.Fatalf("a seventeenth window: %v, want the limit", err)
	}

	if _, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(windows[0])); err != nil {
		t.Fatalf("close: %v", err)
	}
	f.mustReport(terminalReport(windows[0], 17, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_OPERATOR, usage(0, 0), f.clock.Now()))
	f.clock.Advance(10 * time.Minute)

	if _, err := f.tryOpen(); codeOf(t, err) != rawwindowapi.ErrCodeWindowLimit {
		t.Fatalf("after the close was acknowledged and before its expiry: %v, want the limit", err)
	}

	f.clock.Advance(21 * time.Minute)
	f.open()
}

func TestAWindowTheEdgeEndedEarlyHoldsItsSlotUntilExpiry(t *testing.T) {
	f := subscribedFixture(t)
	windows := f.fillLiveWindows(16)
	f.mustReport(terminalReport(windows[0], 1, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_BYTE_BUDGET, usage(100, 9), f.clock.Now()))

	if _, err := f.tryOpen(); codeOf(t, err) != rawwindowapi.ErrCodeWindowLimit {
		t.Fatalf("with one window ended early: %v, want the limit", err)
	}
}

func TestExpiryIsAppliedLazilyOnReadAndPersistedOnWrite(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()
	expiresAt := window.GetConfig().GetExpiresAt().AsTime()
	f.clock.Advance(31 * time.Minute)
	before, err := testKV.Get(t.Context(), tenantA+"."+f.edge)
	if err != nil {
		t.Fatalf("read the stored record: %v", err)
	}

	state := f.window(window).GetState()

	if state.GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL ||
		state.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED ||
		!state.GetEndedAt().AsTime().Equal(expiresAt) {
		t.Fatalf("expired window state = %v, want terminal EXPIRED ended at its expiry %v", state, expiresAt)
	}
	if owed := f.owed(1, epochOne); len(owed) != 0 {
		t.Errorf("an expired window is owed %v, want nothing", owed)
	}
	after, err := testKV.Get(t.Context(), tenantA+"."+f.edge)
	if err != nil {
		t.Fatalf("read the stored record: %v", err)
	}
	if after.Revision() != before.Revision() {
		t.Error("a read persisted the expiry")
	}

	f.mustSubscribe(1, epochOne, "syslog", "netflow")

	entry := f.storedEntry(window)
	if entry.GetState().GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL ||
		entry.GetState().GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED ||
		entry.GetReportedRevision() != entry.GetRevision() {
		t.Errorf("stored entry after a write = %v, want terminal EXPIRED owed nothing", entry)
	}
}

func TestPendingStaysPendingUntilTheEdgeAcknowledges(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()

	f.owed(1, epochOne)
	if got := f.window(window).GetState().GetLifecycle(); got != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
		t.Fatalf("lifecycle after the start was handed out = %v, want pending", got)
	}

	activatedAt := f.clock.Now().Add(time.Second)
	f.mustReport(activeReport(window, 1, usage(0, 0), activatedAt))

	state := f.window(window).GetState()
	if state.GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE || !state.GetActivatedAt().AsTime().Equal(activatedAt) {
		t.Errorf("state after the active report = %v, want active at %v", state, activatedAt)
	}
	if owed := f.owed(1, epochOne); len(owed) != 0 {
		t.Errorf("owed after the acknowledgement = %v, want none", owed)
	}
}

func TestAnActiveReportFixesTheActivationTimeOnce(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()
	first := f.clock.Now().Add(time.Second)

	f.mustReport(activeReport(window, 1, usage(1, 1), first))
	f.mustReport(activeReport(window, 1, usage(2, 2), first.Add(time.Minute)))

	state := f.window(window).GetState()
	if !state.GetActivatedAt().AsTime().Equal(first) {
		t.Errorf("activated_at = %v, want the first report's %v", state.GetActivatedAt().AsTime(), first)
	}
	if state.GetUsage().GetSelectedBytes() != 2 {
		t.Errorf("usage = %v, want the later counters", state.GetUsage())
	}
}

func TestUsageNeverDecreasesPerCounter(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()
	f.mustReport(activeReport(window, 1, usage(50, 3), f.clock.Now()))

	f.mustReport(activeReport(window, 1, usage(20, 9), f.clock.Now()))

	got := f.window(window).GetState().GetUsage()
	if got.GetSelectedBytes() != 50 || got.GetSelectedRecords() != 9 {
		t.Errorf("usage = %d bytes in %d records, want the per-counter maxima 50 and 9", got.GetSelectedBytes(), got.GetSelectedRecords())
	}
}

func TestATerminalStateDominatesLaterReports(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()
	if _, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(window)); err != nil {
		t.Fatalf("close: %v", err)
	}
	endedAt := f.clock.Now().Add(time.Second)
	f.mustReport(terminalReport(window, 2, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_OPERATOR, usage(30, 2), endedAt))

	f.mustReport(activeReport(window, 2, usage(35, 3), f.clock.Now()))
	f.mustReport(activeReport(window, 1, usage(99, 9), f.clock.Now()))
	f.mustReport(terminalReport(window, 2, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_BYTE_BUDGET, usage(30, 2), endedAt.Add(time.Hour)))

	state := f.window(window).GetState()
	if state.GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL ||
		state.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_OPERATOR ||
		!state.GetEndedAt().AsTime().Equal(endedAt) {
		t.Errorf("state = %v, want the first terminal report kept", state)
	}
	if state.GetUsage().GetSelectedBytes() != 35 {
		t.Errorf("selected bytes = %d, want the 35 the later same-revision report raised it to", state.GetUsage().GetSelectedBytes())
	}
}

func TestAReportBelowTheReportedRevisionIsIgnored(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()
	if _, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(window)); err != nil {
		t.Fatalf("close: %v", err)
	}
	f.mustReport(activeReport(window, 2, usage(10, 1), f.clock.Now()))

	f.mustReport(activeReport(window, 1, usage(80, 8), f.clock.Now()))

	entry := f.storedEntry(window)
	if entry.GetReportedRevision() != 2 || entry.GetState().GetUsage().GetSelectedBytes() != 10 {
		t.Errorf("reported revision %d and usage %v, want 2 and the 10 bytes the revision-2 report carried",
			entry.GetReportedRevision(), entry.GetState().GetUsage())
	}
}

func TestAReportAckKeepsALaterCloseOwed(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()
	if _, err := f.store.Close(t.Context(), tenantA, f.edge, windowID(window)); err != nil {
		t.Fatalf("close: %v", err)
	}

	f.mustReport(activeReport(window, 1, usage(5, 1), f.clock.Now()))

	owed := f.owed(1, epochOne)
	if len(owed) != 1 || owed[0].GetClose() == nil || owed[0].GetRevision() != 2 {
		t.Fatalf("owed = %v, want the close at revision 2 until the edge reports it", owed)
	}
	if got := f.storedEntry(window).GetReportedRevision(); got != 1 {
		t.Errorf("reported revision = %d, want 1", got)
	}
}

func TestAReportAboveTheIssuedRevisionIsRefusedWhole(t *testing.T) {
	f := subscribedFixture(t)
	first := f.open()
	second := f.open()

	err := f.report(1, epochOne,
		activeReport(first, 1, usage(10, 1), f.clock.Now()),
		activeReport(second, 3, usage(10, 1), f.clock.Now()))

	wantCode(t, err, rawwindowapi.ErrCodeBadReport)
	if got := f.window(first).GetState().GetLifecycle(); got != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
		t.Errorf("the valid half of a refused report took effect: first window is %v", got)
	}
}

func TestAReportCannotSayPendingOrEndTerminalWithoutAReason(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()
	pending := edgeingestv1.RawWindowReport_builder{
		Revision: new(uint64(1)),
		State: rawwindowv1.RawWindowState_builder{
			Ref:       window.GetConfig().GetRef(),
			Lifecycle: rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING.Enum(),
		}.Build(),
	}.Build()
	noReason := edgeingestv1.RawWindowReport_builder{
		Revision: new(uint64(1)),
		State: rawwindowv1.RawWindowState_builder{
			Ref:       window.GetConfig().GetRef(),
			Lifecycle: rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL.Enum(),
			EndedAt:   timestamppb.New(f.clock.Now()),
		}.Build(),
	}.Build()

	for name, report := range map[string]*edgeingestv1.RawWindowReport{"pending": pending, "terminal without a reason": noReason} {
		t.Run(name, func(t *testing.T) {
			wantCode(t, f.report(1, epochOne, report), rawwindowapi.ErrCodeBadReport)
		})
	}
}

func TestAReportFromAnotherProcessIsRefused(t *testing.T) {
	f := subscribedFixture(t)
	window := f.open()
	f.mustSubscribe(2, epochTwo, "syslog")
	report := activeReport(window, 1, usage(1, 1), f.clock.Now())

	for _, tc := range []struct {
		name       string
		generation uint64
		epoch      string
	}{
		{name: "the superseded generation", generation: 1, epoch: epochOne},
		{name: "the current generation with another epoch", generation: 2, epoch: epochThree},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantCode(t, f.report(tc.generation, tc.epoch, report), rawwindowapi.ErrCodeStaleFence)
		})
	}
}

func TestAReportNamingAnotherEdgesWindowIsRefused(t *testing.T) {
	mine := subscribedFixture(t)
	theirs := subscribedFixture(t)
	theirWindow := theirs.open()

	t.Run("by the foreign edge in the reference", func(t *testing.T) {
		err := mine.report(1, epochOne, activeReport(theirWindow, 1, usage(1, 1), mine.clock.Now()))

		wantCode(t, err, rawwindowapi.ErrCodeForeignWindow)
	})

	t.Run("by the foreign window under the caller's own edge", func(t *testing.T) {
		disguised := proto.Clone(theirWindow).(*rawwindowv1.RawWindowRecord)
		disguised.GetConfig().GetRef().SetEdge(edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: new(mine.edge)}.Build()}.Build())

		if err := mine.report(1, epochOne, activeReport(disguised, 1, usage(1, 1), mine.clock.Now())); err != nil {
			t.Fatalf("report: %v", err)
		}

		if got := theirs.window(theirWindow).GetState().GetLifecycle(); got != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
			t.Errorf("the other edge's window is %v after a report from this edge, want pending", got)
		}
	})
}

func TestWindowIsFoundAcrossTheTenantsEdgesAndOnlyThere(t *testing.T) {
	first := subscribedFixture(t)
	second := subscribedFixture(t)
	first.open()
	want := second.open()

	got, err := first.store.Window(t.Context(), tenantA, windowID(want))
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if got.GetConfig().GetRef().GetEdge().GetEdge().GetId() != second.edge {
		t.Errorf("window edge = %s, want the second edge %s", got.GetConfig().GetRef().GetEdge().GetEdge().GetId(), second.edge)
	}

	_, err = first.store.Window(t.Context(), tenantB, windowID(want))
	wantCode(t, err, rawwindowapi.ErrCodeNotFound)
}

func TestTheBucketHoldsOneRecordPerTenantAndEdge(t *testing.T) {
	f := subscribedFixture(t)
	f.open()
	f.open()

	keys, err := testKV.Keys(t.Context())
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	count := 0
	for _, key := range keys {
		if key == tenantA+"."+f.edge {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("bucket holds %d keys for the edge, want the one %s.%s", count, tenantA, f.edge)
	}
}

// openCycle opens count windows that each end in one hour, so a test that
// advances the clock past that expires the whole cycle together.
func (f *fixture) openCycle(count int, reason string) []*rawwindowv1.RawWindowRecord {
	f.t.Helper()
	windows := make([]*rawwindowv1.RawWindowRecord, count)
	for i := range windows {
		windows[i] = f.open(func(req *rawwindowapi.OpenRequest) {
			req.ExpiresAt = f.clock.Now().Add(time.Hour)
			req.Authorization = authorization(reason)
		})
	}
	return windows
}

func TestExpiredRowsAreKeptForADayAndThenDropped(t *testing.T) {
	f := subscribedFixture(t)
	window := f.openCycle(1, "short")[0]
	f.clock.Advance(time.Hour)
	f.mustSubscribe(1, epochOne, "syslog", "a")
	if got := len(f.stored().GetWindows()); got != 1 {
		t.Fatalf("stored rows right after expiry = %d, want the expired one kept", got)
	}

	f.clock.Advance(24 * time.Hour)
	f.mustSubscribe(1, epochOne, "syslog", "b")
	if got := len(f.stored().GetWindows()); got != 1 {
		t.Fatalf("stored rows exactly 24 hours after it ended = %d, want it kept", got)
	}

	f.clock.Advance(time.Nanosecond)
	f.mustSubscribe(1, epochOne, "syslog", "c")
	if got := len(f.stored().GetWindows()); got != 0 {
		t.Fatalf("stored rows more than 24 hours after it ended = %d, want none", got)
	}
	if _, err := f.store.Window(t.Context(), tenantA, windowID(window)); codeOf(t, err) != rawwindowapi.ErrCodeNotFound {
		t.Errorf("window read after pruning: %v, want not found", err)
	}
}

func TestOnlyTheNewestSixtyFourTerminalRowsStay(t *testing.T) {
	f := subscribedFixture(t)
	var all []*rawwindowv1.RawWindowRecord
	for range 5 {
		all = append(all, f.openCycle(16, "short")...)
		f.clock.Advance(time.Hour + time.Minute)
	}

	f.mustSubscribe(1, epochOne, "syslog", "again")

	rec := f.stored()
	if len(rec.GetWindows()) != 64 {
		t.Fatalf("stored rows = %d, want the newest 64 of 80", len(rec.GetWindows()))
	}
	kept := map[string]bool{}
	for _, entry := range rec.GetWindows() {
		kept[entry.GetConfig().GetRef().GetRawWindow().GetId()] = true
	}
	for i, window := range all {
		if wantKept := i >= 16; kept[windowID(window)] != wantKept {
			t.Errorf("window %d kept = %v, want %v: the first cycle is the oldest", i, kept[windowID(window)], wantKept)
		}
	}
}

// fourByteRunes is a reason of 2048 characters that serializes to 8 KiB, which
// the schema's character bound allows.
var fourByteRunes = strings.Repeat("\U0001D11E", 2048)

func TestTheSerializedRecordStaysBoundedByEvictingTheOldestExpiredRows(t *testing.T) {
	f := subscribedFixture(t)
	var all []*rawwindowv1.RawWindowRecord
	for range 4 {
		all = append(all, f.openCycle(16, fourByteRunes)...)
		f.clock.Advance(time.Hour + time.Minute)
	}
	live := f.openCycle(1, fourByteRunes)[0]

	entry, err := testKV.Get(t.Context(), tenantA+"."+f.edge)
	if err != nil {
		t.Fatalf("read the stored record: %v", err)
	}
	if got := len(entry.Value()); got > 256<<10 {
		t.Fatalf("stored record is %d bytes, want at most 256 KiB", got)
	}

	rec := f.stored()
	kept := map[string]bool{}
	for _, row := range rec.GetWindows() {
		kept[row.GetConfig().GetRef().GetRawWindow().GetId()] = true
	}
	if !kept[windowID(live)] {
		t.Error("the live window was evicted")
	}
	evicted, sawKept := 0, false
	for i, window := range all {
		if kept[windowID(window)] {
			sawKept = true
			continue
		}
		evicted++
		if sawKept {
			t.Errorf("window %d was evicted while an older row stayed, want oldest first", i)
		}
	}
	if evicted == 0 {
		t.Fatal("nothing was evicted, so the fixture did not reach the bound")
	}
	if !kept[windowID(all[len(all)-1])] {
		t.Error("the newest expired row was evicted")
	}
}

func TestAnOpenThatStillExceedsTheBoundWithNothingToEvictIsRefused(t *testing.T) {
	f := subscribedFixture(t)
	oversized := strings.Repeat("x", 300<<10)

	_, err := f.tryOpen(func(req *rawwindowapi.OpenRequest) { req.Authorization = authorization(oversized) })

	wantCode(t, err, rawwindowapi.ErrCodeRecordFull)
	if got := len(f.stored().GetWindows()); got != 0 {
		t.Errorf("a refused open stored %d windows, want 0", got)
	}
}

func TestMalformedKeysAreRefusedBeforeTheBucketIsTouched(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	_, err := f.store.Subscribe(ctx, "not.a.tenant", f.edge, "", 1, epochOne, nil)
	wantCode(t, err, rawwindowapi.ErrCodeBadKey)

	_, err = f.store.Subscribe(ctx, tenantA, "edge.with.dots", "", 1, epochOne, nil)
	wantCode(t, err, rawwindowapi.ErrCodeBadKey)

	_, err = f.store.Window(ctx, tenantA, "not-a-uuid")
	wantCode(t, err, rawwindowapi.ErrCodeBadKey)
}
