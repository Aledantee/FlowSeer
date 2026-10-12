package rawwindow_test

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/rawwindow"
)

const (
	edgeID        = "0192e6a0-0000-7000-8000-0000000000ed"
	epoch         = "0192e6a0-0000-7000-8000-0000000000f2"
	otherEpoch    = "0192e6a0-0000-7000-8000-0000000000f3"
	windowOne     = "0192e6a0-0000-7000-8000-000000000a01"
	windowTwo     = "0192e6a0-0000-7000-8000-000000000a02"
	windowThree   = "0192e6a0-0000-7000-8000-000000000a03"
	deviceA       = "0192e6a0-0000-7000-8000-0000000000d1"
	deviceB       = "0192e6a0-0000-7000-8000-0000000000d2"
	deviceC       = "0192e6a0-0000-7000-8000-0000000000d3"
	integrationI  = "0192e6a0-0000-7000-8000-0000000000c1"
	integrationJ  = "0192e6a0-0000-7000-8000-0000000000c2"
	generation    = 3
	syslogSource  = "syslog"
	otherSource   = "netflow"
	startWallUnix = 1_790_000_000
)

// clocks are the independent wall and elapsed readings a controller runs on.
// A test moves each on its own, which a single clock callback cannot express.
type clocks struct {
	mu      sync.Mutex // guards wall and elapsed
	wall    time.Time
	elapsed time.Duration
}

func newClocks() *clocks {
	return &clocks{wall: time.Unix(startWallUnix, 0)}
}

func (c *clocks) Wall() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall
}

func (c *clocks) Elapsed() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.elapsed
}

// advance moves both clocks, as time passing does.
func (c *clocks) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
	c.elapsed += d
}

// stepWall moves only the wall clock, as an operator or an NTP correction does.
func (c *clocks) stepWall(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
}

func (c *clocks) advanceElapsed(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.elapsed += d
}

func newController(t *testing.T, c *clocks) *rawwindow.Controller {
	t.Helper()
	ctrl, err := rawwindow.NewController(rawwindow.Config{
		BootGeneration: generation,
		Epoch:          epoch,
		Sources:        []string{syslogSource},
		Wall:           c.Wall,
		Elapsed:        c.Elapsed,
	})
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return ctrl
}

func deviceSelector(id string) *rawwindowv1.RawWindowSelector {
	return rawwindowv1.RawWindowSelector_builder{
		Device: inventoryv1.DeviceGlobalRef_builder{
			Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(id)}.Build(),
		}.Build(),
	}.Build()
}

// integrationSelector selects integration I, the integration of every
// observation in these tests.
func integrationSelector() *rawwindowv1.RawWindowSelector {
	return rawwindowv1.RawWindowSelector_builder{
		Integration: inventoryv1.IntegrationGlobalRef_builder{
			Integration: inventoryv1.IntegrationLocalRef_builder{Id: proto.String(integrationI)}.Build(),
		}.Build(),
	}.Build()
}

func sourceSelector(key string) *rawwindowv1.RawWindowSelector {
	return rawwindowv1.RawWindowSelector_builder{
		Source: rawwindowv1.RawWindowSource_builder{Key: proto.String(key)}.Build(),
	}.Build()
}

func windowRef(id string) *rawwindowv1.RawWindowGlobalRef {
	return rawwindowv1.RawWindowGlobalRef_builder{
		Edge:      edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build()}.Build(),
		RawWindow: rawwindowv1.RawWindowLocalRef_builder{Id: proto.String(id)}.Build(),
	}.Build()
}

// open builds a validated start control, as central sends it.
func open(t *testing.T, id string, revision uint64, selector *rawwindowv1.RawWindowSelector, expires time.Time, maxBytes uint64) *edgeingestv1.RawWindowControl {
	t.Helper()
	cfg := rawwindowv1.RawWindowConfig_builder{
		Ref:       windowRef(id),
		Selector:  selector,
		ExpiresAt: timestamppb.New(expires),
		MaxBytes:  proto.Uint64(maxBytes),
		Authorization: rawwindowv1.RawWindowAuthorization_builder{
			RequestedBy: identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://idp.example.com"),
				Subject: proto.String("operator-1"),
			}.Build(),
			Reason: proto.String("intermittent parse failure"),
		}.Build(),
	}.Build()
	return validated(t, edgeingestv1.RawWindowControl_builder{
		BootGeneration: proto.Uint64(generation),
		ProcessEpoch:   proto.String(epoch),
		Ref:            windowRef(id),
		Revision:       proto.Uint64(revision),
		Open:           cfg,
	}.Build())
}

func closeWindow(t *testing.T, id string, revision uint64, expires time.Time) *edgeingestv1.RawWindowControl {
	t.Helper()
	return validated(t, edgeingestv1.RawWindowControl_builder{
		BootGeneration: proto.Uint64(generation),
		ProcessEpoch:   proto.String(epoch),
		Ref:            windowRef(id),
		Revision:       proto.Uint64(revision),
		Close:          edgeingestv1.RawWindowClose_builder{ExpiresAt: timestamppb.New(expires)}.Build(),
	}.Build())
}

func validated(t *testing.T, control *edgeingestv1.RawWindowControl) *edgeingestv1.RawWindowControl {
	t.Helper()
	if err := protovalidate.Validate(control); err != nil {
		t.Fatalf("control fails its schema rules: %v", err)
	}
	return control
}

func apply(t *testing.T, ctrl *rawwindow.Controller, controls ...*edgeingestv1.RawWindowControl) {
	t.Helper()
	for _, control := range controls {
		if err := ctrl.Apply(control); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
}

// obs is one record of the given size from one device of integration I over the
// syslog source.
func obs(device string, bytes uint64) rawwindow.Observation {
	return rawwindow.Observation{Source: syslogSource, DeviceID: device, IntegrationID: integrationI, Bytes: bytes}
}

// reportOf returns the pending report for a window, or fails.
func reportOf(t *testing.T, ctrl *rawwindow.Controller, id string) *edgeingestv1.RawWindowReport {
	t.Helper()
	for _, r := range ctrl.PendingReports(100) {
		if r.Message.GetState().GetRef().GetRawWindow().GetId() == id {
			return r.Message
		}
	}
	t.Fatalf("no pending report for window %s", id)
	return nil
}

func TestSelectorsMatchOnlyTheirScope(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(10 * time.Minute)
	apply(t, ctrl,
		open(t, windowOne, 1, deviceSelector(deviceA), expires, 1000),
		open(t, windowTwo, 2, integrationSelector(), expires, 1000),
		open(t, windowThree, 3, sourceSelector(syslogSource), expires, 1000),
	)

	tests := []struct {
		name string
		obs  rawwindow.Observation
		want []string
	}{
		{
			name: "device A in integration I pays all three scopes",
			obs:  rawwindow.Observation{Source: syslogSource, DeviceID: deviceA, IntegrationID: integrationI, Bytes: 1},
			want: []string{windowOne, windowTwo, windowThree},
		},
		{
			name: "device B in integration I is outside the device A window",
			obs:  rawwindow.Observation{Source: syslogSource, DeviceID: deviceB, IntegrationID: integrationI, Bytes: 1},
			want: []string{windowTwo, windowThree},
		},
		{
			name: "device C in integration J matches only the source",
			obs:  rawwindow.Observation{Source: syslogSource, DeviceID: deviceC, IntegrationID: integrationJ, Bytes: 1},
			want: []string{windowThree},
		},
		{
			name: "another adapter's record matches no source window",
			obs:  rawwindow.Observation{Source: otherSource, DeviceID: deviceB, IntegrationID: integrationI, Bytes: 1},
			want: []string{windowTwo},
		},
		{
			name: "another adapter's record from device C matches nothing",
			obs:  rawwindow.Observation{Source: otherSource, DeviceID: deviceC, IntegrationID: integrationJ, Bytes: 1},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ctrl.Select(tt.obs); !slices.Equal(got, tt.want) {
				t.Errorf("Select paid %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExpiryIsCheckedAtTheSelectionPoint(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(time.Minute)
	apply(t, ctrl, open(t, windowOne, 1, deviceSelector(deviceA), expires, 1000))

	c.advance(time.Minute - time.Nanosecond)
	if got := ctrl.Select(obs(deviceA, 1)); !slices.Equal(got, []string{windowOne}) {
		t.Fatalf("one tick before expiry paid %v, want the window", got)
	}

	c.advance(time.Nanosecond)
	if got := ctrl.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("at expiry paid %v, want nothing even though no close arrived", got)
	}
	state := reportOf(t, ctrl, windowOne).GetState()
	if state.GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL ||
		state.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED {
		t.Errorf("state = %v, want terminal and expired", state)
	}
	if got := state.GetEndedAt().AsTime(); !got.Equal(expires) {
		t.Errorf("ended_at = %v, want the expiry %v", got, expires)
	}
}

func TestAWindowLastsNoLongerThanItsElapsedDeadline(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	apply(t, ctrl, open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(30*time.Minute), 1000))

	// The wall clock is stepped back, so only the elapsed deadline can end
	// the window.
	c.stepWall(-2 * time.Hour)
	c.advanceElapsed(30*time.Minute - time.Nanosecond)
	if got := ctrl.Select(obs(deviceA, 1)); !slices.Equal(got, []string{windowOne}) {
		t.Fatalf("just inside the elapsed deadline paid %v, want the window", got)
	}

	c.advanceElapsed(time.Nanosecond)
	if got := ctrl.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("at the elapsed deadline paid %v, want nothing", got)
	}
}

func TestTheElapsedDeadlineIsBoundedToOneHour(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	// The expiry is far past anything central admits, as a wrong wall clock
	// can make it look.
	apply(t, ctrl, open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(3*time.Hour), 1000))

	c.advanceElapsed(time.Hour - time.Nanosecond)
	if got := ctrl.Select(obs(deviceA, 1)); !slices.Equal(got, []string{windowOne}) {
		t.Fatalf("just inside an hour paid %v, want the window", got)
	}
	c.advanceElapsed(time.Nanosecond)
	if got := ctrl.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("after an hour paid %v, want nothing although the wall clock allows more", got)
	}
}

func TestADuplicateStartNeverExtendsTheElapsedDeadline(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	start := open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(30*time.Minute), 1000)
	apply(t, ctrl, start)

	// The wall clock stays put, so a renewed deadline would show as time the
	// window should not have.
	c.advanceElapsed(20 * time.Minute)
	apply(t, ctrl, start)
	c.advanceElapsed(10 * time.Minute)

	if got := ctrl.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("a duplicate start renewed the deadline: paid %v after the original 30 minutes", got)
	}
}

func TestDelayedDeliveryGivesTheWindowOnlyItsRemainingTime(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(10 * time.Minute)
	// The start reaches the edge eight minutes into the window.
	c.advance(8 * time.Minute)
	apply(t, ctrl, open(t, windowOne, 1, deviceSelector(deviceA), expires, 1000))

	c.advance(2*time.Minute - time.Nanosecond)
	if got := ctrl.Select(obs(deviceA, 1)); !slices.Equal(got, []string{windowOne}) {
		t.Fatalf("inside the remaining time paid %v, want the window", got)
	}
	c.advance(time.Nanosecond)
	if got := ctrl.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("past the original expiry paid %v, want nothing", got)
	}
}

func TestAStartAfterItsExpiryEndsAtOnceAndRetainsNothing(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(time.Minute)
	c.advance(time.Minute)

	apply(t, ctrl, open(t, windowOne, 1, deviceSelector(deviceA), expires, 1000))

	if got := ctrl.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("a window delivered after its expiry paid %v", got)
	}
	state := reportOf(t, ctrl, windowOne).GetState()
	if state.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED || state.HasActivatedAt() {
		t.Errorf("state = %v, want expired and never activated", state)
	}
}

func TestAWallClockStepBackCannotReviveAReclaimedWindow(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(time.Minute)
	start := open(t, windowOne, 1, deviceSelector(deviceA), expires, 1000)
	apply(t, ctrl, start)

	c.advance(time.Minute)
	ctrl.Sweep()
	ctrl.Reported(ctrl.PendingReports(10))
	ctrl.Sweep()
	if got := len(ctrl.PendingReports(10)); got != 0 {
		t.Fatalf("pending reports after the acknowledged expiry = %d, want the terminal window reclaimed", got)
	}

	c.stepWall(-time.Hour)
	apply(t, ctrl, start)

	if got := ctrl.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("a start replayed after a clock step back paid %v", got)
	}
	state := reportOf(t, ctrl, windowOne).GetState()
	if state.GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL {
		t.Errorf("state = %v, want terminal", state)
	}
}

func TestBudgetsCoverTheWholeScope(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	apply(t, ctrl, open(t, windowOne, 1, integrationSelector(), c.Wall().Add(time.Hour), 10))

	if got := ctrl.Select(obs(deviceA, 6)); !slices.Equal(got, []string{windowOne}) {
		t.Fatalf("six bytes from A paid %v, want the window", got)
	}
	if got := ctrl.Select(obs(deviceB, 4)); !slices.Equal(got, []string{windowOne}) {
		t.Fatalf("four bytes from B paid %v, want the window", got)
	}

	for _, device := range []string{deviceA, deviceB} {
		if got := ctrl.Select(obs(device, 1)); got != nil {
			t.Errorf("the next byte from %s paid %v, want nothing", device, got)
		}
	}
	state := reportOf(t, ctrl, windowOne).GetState()
	if state.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_BYTE_BUDGET {
		t.Errorf("stop reason = %v, want the byte budget", state.GetStopReason())
	}
	if state.GetUsage().GetSelectedBytes() != 10 || state.GetUsage().GetSelectedRecords() != 2 {
		t.Errorf("usage = %v, want 10 bytes in 2 records", state.GetUsage())
	}
}

func TestARecordLargerThanTheRemainderIsNotTruncated(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	apply(t, ctrl, open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(time.Hour), 10))

	if got := ctrl.Select(obs(deviceA, 5)); !slices.Equal(got, []string{windowOne}) {
		t.Fatalf("five bytes paid %v, want the window", got)
	}
	if got := ctrl.Select(obs(deviceA, 6)); got != nil {
		t.Errorf("six bytes with five remaining paid %v, want nothing and no truncation", got)
	}
	if got := ctrl.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("one byte after the window ended paid %v, want nothing", got)
	}
	state := reportOf(t, ctrl, windowOne).GetState()
	if state.GetUsage().GetSelectedBytes() != 5 {
		t.Errorf("selected bytes = %d, want only the five that fit", state.GetUsage().GetSelectedBytes())
	}
}

func TestEmptyDatagramsAreSelectedWithoutDebit(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	apply(t, ctrl, open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(time.Hour), 10))

	if got := ctrl.Select(obs(deviceA, 0)); !slices.Equal(got, []string{windowOne}) {
		t.Fatalf("an empty datagram paid %v, want the window", got)
	}
	usage := reportOf(t, ctrl, windowOne).GetState().GetUsage()
	if usage.GetSelectedBytes() != 0 || usage.GetSelectedRecords() != 1 {
		t.Errorf("usage = %v, want 0 bytes in 1 record", usage)
	}
}

func TestOverlappingWindowsKeepOneCopyAndIndependentAccounts(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(time.Hour)
	apply(t, ctrl,
		open(t, windowTwo, 2, deviceSelector(deviceA), expires, 4),
		open(t, windowOne, 1, integrationSelector(), expires, 4),
	)

	got := ctrl.Select(obs(deviceA, 4))
	if want := []string{windowOne, windowTwo}; !slices.Equal(got, want) {
		t.Fatalf("a four-byte record paid %v, want the sorted refs %v", got, want)
	}
	for _, id := range []string{windowOne, windowTwo} {
		if used := reportOf(t, ctrl, id).GetState().GetUsage().GetSelectedBytes(); used != 4 {
			t.Errorf("window %s debited %d bytes, want 4", id, used)
		}
	}
}

func TestAnExhaustedWindowStopsWhileAnotherRetainsTheRecord(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(time.Hour)
	apply(t, ctrl,
		open(t, windowOne, 1, integrationSelector(), expires, 3),
		open(t, windowTwo, 2, deviceSelector(deviceA), expires, 4),
	)

	got := ctrl.Select(obs(deviceA, 4))
	if want := []string{windowTwo}; !slices.Equal(got, want) {
		t.Fatalf("a four-byte record paid %v, want only %v", got, want)
	}
	one := reportOf(t, ctrl, windowOne).GetState()
	if one.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_BYTE_BUDGET {
		t.Errorf("the three-byte window stop reason = %v, want the byte budget", one.GetStopReason())
	}
	if one.GetUsage().GetSelectedBytes() != 0 {
		t.Errorf("the window that could not fit the record was charged %d bytes", one.GetUsage().GetSelectedBytes())
	}
}

func TestAReplayedStartCannotRenewSpentBudget(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	start := open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(time.Hour), 10)
	apply(t, ctrl, start)
	ctrl.Select(obs(deviceA, 6))

	apply(t, ctrl, start)

	if got := ctrl.Select(obs(deviceA, 4)); !slices.Equal(got, []string{windowOne}) {
		t.Fatalf("four bytes paid %v, want the window: four of ten remain", got)
	}
	if got := ctrl.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("one more byte paid %v after a replayed start, want nothing: the budget is spent", got)
	}
}

func TestADelayedStartCannotReopenAClosedWindow(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(time.Hour)
	apply(t, ctrl,
		open(t, windowOne, 6, deviceSelector(deviceA), expires, 100),
		closeWindow(t, windowOne, 8, expires),
		open(t, windowOne, 7, deviceSelector(deviceA), expires, 100),
	)

	if got := ctrl.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("a start at revision 7 after a close at 8 paid %v", got)
	}
	report := reportOf(t, ctrl, windowOne)
	if report.GetRevision() != 8 {
		t.Errorf("report revision = %d, want the close's 8", report.GetRevision())
	}
}

func TestACloseThatArrivesFirstLeavesATombstone(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(time.Hour)
	apply(t, ctrl, closeWindow(t, windowOne, 5, expires))

	apply(t, ctrl, open(t, windowOne, 4, deviceSelector(deviceA), expires, 100))

	if got := ctrl.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("a start that lost the race with its close paid %v", got)
	}
	state := reportOf(t, ctrl, windowOne).GetState()
	if state.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_OPERATOR {
		t.Errorf("stop reason = %v, want the operator's close", state.GetStopReason())
	}
}

func TestAnotherWindowsRevisionDoesNotDiscardAnUndeliveredStart(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(time.Hour)
	apply(t, ctrl,
		open(t, windowTwo, 9, deviceSelector(deviceB), expires, 100),
		open(t, windowOne, 5, deviceSelector(deviceA), expires, 100),
	)

	if got := ctrl.Select(obs(deviceA, 1)); !slices.Equal(got, []string{windowOne}) {
		t.Errorf("a start with a lower revision than another window's paid %v, want the window", got)
	}
}

func TestAProcessRestartStartsWithNoWindows(t *testing.T) {
	c := newClocks()
	first := newController(t, c)
	apply(t, first, open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(time.Hour), 10))
	first.Select(obs(deviceA, 6))

	restarted := newController(t, c)

	if got := restarted.Select(obs(deviceA, 1)); got != nil {
		t.Errorf("a restarted controller paid %v, want nothing until a new window opens", got)
	}
	if got := len(restarted.PendingReports(10)); got != 0 {
		t.Errorf("a restarted controller owes %d reports, want none", got)
	}
}

func TestControlsOfAnotherGenerationOrEpochAreRefused(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(time.Hour)

	tests := []struct {
		name   string
		mutate func(*edgeingestv1.RawWindowControl)
	}{
		{name: "older generation", mutate: func(m *edgeingestv1.RawWindowControl) { m.SetBootGeneration(generation - 1) }},
		{name: "newer generation", mutate: func(m *edgeingestv1.RawWindowControl) { m.SetBootGeneration(generation + 1) }},
		{name: "other epoch", mutate: func(m *edgeingestv1.RawWindowControl) { m.SetProcessEpoch(otherEpoch) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			control := open(t, windowOne, 1, deviceSelector(deviceA), expires, 100)
			tt.mutate(control)

			err := ctrl.Apply(control)
			if code, ok := errs.CodeOf(err); !ok || code != rawwindow.ErrCodeControl {
				t.Fatalf("Apply error = %v, want code %v", err, rawwindow.ErrCodeControl)
			}
			if got := ctrl.Select(obs(deviceA, 1)); got != nil {
				t.Errorf("a refused control installed a window that paid %v", got)
			}
		})
	}
}

func TestAControlFailingItsSchemaRulesIsRefused(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	control := open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(time.Hour), 100)
	control.ClearRevision()

	err := ctrl.Apply(control)
	if code, ok := errs.CodeOf(err); !ok || code != rawwindow.ErrCodeControl {
		t.Errorf("Apply error = %v, want code %v", err, rawwindow.ErrCodeControl)
	}
}

func TestNewControllerNeedsAGenerationAndAnEpoch(t *testing.T) {
	tests := []struct {
		name string
		cfg  rawwindow.Config
	}{
		{name: "no generation", cfg: rawwindow.Config{Epoch: epoch}},
		{name: "no epoch", cfg: rawwindow.Config{BootGeneration: 1}},
		{name: "epoch that is not a uuid", cfg: rawwindow.Config{BootGeneration: 1, Epoch: "epoch-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := rawwindow.NewController(tt.cfg)
			if code, ok := errs.CodeOf(err); !ok || code != rawwindow.ErrCodeConfig {
				t.Errorf("NewController error = %v, want code %v", err, rawwindow.ErrCodeConfig)
			}
		})
	}
}

func TestReportsAreCumulativeAndOwedUntilAcknowledged(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	apply(t, ctrl, open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(time.Hour), 100))

	first := ctrl.PendingReports(10)
	if len(first) != 1 || first[0].Message.GetState().GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE {
		t.Fatalf("pending reports = %v, want one active window", first)
	}
	if err := protovalidate.Validate(first[0].Message); err != nil {
		t.Fatalf("report fails its schema rules: %v", err)
	}

	// The report is lost: the same state is owed again with the same counters.
	again := ctrl.PendingReports(10)
	if len(again) != 1 {
		t.Fatalf("pending reports after a lost report = %d, want 1", len(again))
	}

	ctrl.Select(obs(deviceA, 7))
	ctrl.Reported(first)
	owed := ctrl.PendingReports(10)
	if len(owed) != 1 || owed[0].Message.GetState().GetUsage().GetSelectedBytes() != 7 {
		t.Fatalf("pending reports after a change since the acknowledged one = %v, want the 7-byte state", owed)
	}

	ctrl.Reported(owed)
	if got := len(ctrl.PendingReports(10)); got != 0 {
		t.Errorf("pending reports after acknowledgement = %d, want none", got)
	}
}

func TestPendingReportsHonorTheLimit(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	expires := c.Wall().Add(time.Hour)
	apply(t, ctrl,
		open(t, windowOne, 1, deviceSelector(deviceA), expires, 100),
		open(t, windowTwo, 2, deviceSelector(deviceB), expires, 100),
		open(t, windowThree, 3, deviceSelector(deviceC), expires, 100),
	)

	if got := len(ctrl.PendingReports(2)); got != 2 {
		t.Errorf("PendingReports(2) returned %d reports, want 2", got)
	}
}

func TestSweepReportsAnExpiryWithoutAnyTraffic(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	apply(t, ctrl, open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(time.Minute), 100))
	ctrl.Reported(ctrl.PendingReports(10))

	c.advance(time.Minute)
	ctrl.Sweep()

	state := reportOf(t, ctrl, windowOne).GetState()
	if state.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED {
		t.Errorf("state = %v, want the expiry reported with no record selected", state)
	}
	select {
	case <-ctrl.Changed():
	default:
		t.Error("Changed holds no value after the expiry, so a reporter would not wake")
	}
}

func TestConcurrentSelectionNeverOverspendsTheBudget(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	const budget = 1000
	apply(t, ctrl, open(t, windowOne, 1, integrationSelector(), c.Wall().Add(time.Hour), budget))

	var (
		paid  atomic.Uint64
		units atomic.Uint64
	)
	devices := []string{deviceA, deviceB, deviceC, deviceA}
	done := make(chan struct{}, len(devices))
	for i, device := range devices {
		spawn.Go(context.Background(), "select-"+strconv.Itoa(i), func() {
			for range 400 {
				if len(ctrl.Select(obs(device, 3))) > 0 {
					paid.Add(3)
					units.Add(1)
				}
			}
			done <- struct{}{}
		}, spawn.ReportTo(func(err error) {
			t.Error(err)
			done <- struct{}{}
		}))
	}
	for range devices {
		<-done
	}

	if got := paid.Load(); got > budget {
		t.Errorf("records paid %d bytes, want at most the %d byte budget", got, budget)
	}
	state := reportOf(t, ctrl, windowOne).GetState()
	if got := state.GetUsage().GetSelectedBytes(); got != paid.Load() {
		t.Errorf("window usage = %d bytes, want the %d callers saw paid", got, paid.Load())
	}
	if got := state.GetUsage().GetSelectedRecords(); got != units.Load() {
		t.Errorf("window usage = %d records, want %d", got, units.Load())
	}
	if state.GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL {
		t.Errorf("lifecycle = %v, want the window ended once 999 of 1000 bytes were spent and 3 no longer fit", state.GetLifecycle())
	}
}
