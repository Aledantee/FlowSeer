// Package rawwindow is the edge's raw window controller. It holds the windows
// central opened for this agent process, matches each observed record to them,
// and debits their byte budgets at the point an adapter selects original bytes.
//
// A window ends on its own: when its wall-clock expiry passes, when an elapsed
// deadline fixed at first admission passes, or when a record does not fit its
// remaining budget. None of those needs central, and a controller that loses
// its stream keeps enforcing all three.
package rawwindow

import (
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes the controller returns.
var (
	// ErrCodeConfig is a controller built without a boot generation or epoch.
	ErrCodeConfig = errs.NewCode("agent/raw-window-config")
	// ErrCodeControl is a control the controller refuses: one that fails its
	// schema rules or belongs to another boot generation or epoch.
	ErrCodeControl = errs.NewCode("agent/raw-window-control")
)

// maxElapsed bounds a window's elapsed deadline whatever its wall-clock expiry
// says, so a wrong wall clock cannot extend collection past an hour.
const maxElapsed = time.Hour

// MaxWindowsPerRecord is the most windows one record can name. It is the
// max_items of RawEvidence.windows in ingest_record.proto, so a record that
// paid more windows would fail envelope validation and stop the source.
const MaxWindowsPerRecord = 16

// Config configures a [Controller].
//
// Wall and Elapsed are independent clocks. Wall is the calendar time a window's
// expiry is written in and may be stepped by an operator or an NTP correction.
// Elapsed is time since the controller started and only moves forward. Nil Wall
// uses [time.Now], and nil Elapsed uses [time.Since] on a reading taken when the
// controller is built, which carries the monotonic clock.
type Config struct {
	// BootGeneration is the durably advanced counter this process runs under.
	// Must be positive.
	BootGeneration uint64
	// Epoch is this process's UUID. Must be present.
	Epoch string
	// Sources are the ingestion source keys this process runs.
	Sources []string
	Wall    func() time.Time
	Elapsed func() time.Duration
	// Logger receives window lifecycle events. Nil discards.
	Logger *slog.Logger
}

// Observation describes one record at the point an adapter selects original
// bytes for it.
type Observation struct {
	// Source is the adapter's ingestion source key.
	Source string
	// DeviceID and IntegrationID are the device and the integration of the
	// binding the sender resolved to.
	DeviceID      string
	IntegrationID string
	// Bytes is the length of the original payload.
	Bytes uint64
}

// Report is one window's cumulative state as the controller owes it to
// central. Hand the reports back to [Controller.Reported] once central
// accepted them.
type Report struct {
	// Message is the report to send.
	Message *edgeingestv1.RawWindowReport
	version uint64
}

// Controller holds the raw windows of one agent process. A Controller is safe
// for concurrent use.
type Controller struct {
	generation uint64
	epoch      string
	sources    []string
	wall       func() time.Time
	elapsed    func() time.Duration
	log        *slog.Logger
	changed    chan struct{}

	mu       sync.Mutex // guards wallHigh, windows, and ended
	wallHigh time.Time
	windows  map[string]*window
	ended    bool // a window ended since the reporter was last woken
}

// window is one window's state. A terminal window stays in the table until its
// expiry has passed and central has its final report, so a delayed start of the
// same window cannot reopen it.
type window struct {
	ref       *rawwindowv1.RawWindowGlobalRef
	selector  *rawwindowv1.RawWindowSelector
	expiresAt time.Time
	deadline  time.Duration
	maxBytes  uint64
	usedBytes uint64
	records   uint64
	revision  uint64

	terminal    bool
	stop        rawwindowv1.RawWindowStopReason
	activatedAt time.Time
	endedAt     time.Time

	version  uint64
	reported uint64
}

// NewController builds a controller with no windows.
func NewController(cfg Config) (*Controller, error) {
	if cfg.BootGeneration == 0 {
		return nil, errs.New().Code(ErrCodeConfig).Msg("raw window controller needs a positive boot generation")
	}
	if _, err := uuid.Parse(cfg.Epoch); err != nil {
		return nil, errs.From(err).Code(ErrCodeConfig).Msg("raw window controller needs a UUID epoch")
	}
	c := &Controller{
		generation: cfg.BootGeneration,
		epoch:      cfg.Epoch,
		sources:    slices.Sorted(slices.Values(cfg.Sources)),
		wall:       cfg.Wall,
		elapsed:    cfg.Elapsed,
		log:        cfg.Logger,
		changed:    make(chan struct{}, 1),
		windows:    make(map[string]*window),
	}
	if c.wall == nil {
		c.wall = time.Now
	}
	if c.elapsed == nil {
		start := time.Now()
		c.elapsed = func() time.Duration { return time.Since(start) }
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	return c, nil
}

// BootGeneration is the generation central fences this process on.
func (c *Controller) BootGeneration() uint64 { return c.generation }

// Epoch is this process's epoch.
func (c *Controller) Epoch() string { return c.epoch }

// Sources returns the ingestion source keys this process runs, sorted.
func (c *Controller) Sources() []string { return slices.Clone(c.sources) }

// Changed returns a channel that holds a value after a window's state changed,
// so a reporter wakes to send what [Controller.PendingReports] holds.
func (c *Controller) Changed() <-chan struct{} { return c.changed }

func (c *Controller) signal() {
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

// now is the wall clock's reading, held at its high-water mark. A step
// backwards would otherwise let a window whose expiry already passed look
// admissible again once its record was reclaimed.
func (c *Controller) now() time.Time {
	if t := c.wall(); t.After(c.wallHigh) {
		c.wallHigh = t
	}
	return c.wallHigh
}

// Apply installs one control from central. A start for a window the controller
// already holds reports its current state and renews neither its counters nor
// its deadlines. A close ends the window and leaves a tombstone through its
// original expiry. A control with a lower revision than the window's is
// ignored. Apply returns [ErrCodeControl] for a control that fails its schema
// rules or names another boot generation or epoch, and changes nothing.
func (c *Controller) Apply(control *edgeingestv1.RawWindowControl) error {
	if err := protovalidate.Validate(control); err != nil {
		return errs.From(err).Code(ErrCodeControl).Msg("raw window control fails its schema rules")
	}
	if control.GetBootGeneration() != c.generation || control.GetProcessEpoch() != c.epoch {
		return errs.New().Code(ErrCodeControl).
			Attr("generation", control.GetBootGeneration()).
			Msg("raw window control belongs to another boot generation or epoch")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	elapsed := c.elapsed()

	id := control.GetRef().GetRawWindow().GetId()
	w := c.windows[id]
	if w != nil && control.GetRevision() < w.revision {
		return nil
	}
	if control.HasClose() {
		c.applyClose(w, control, now)
	} else {
		c.applyOpen(w, control, now, elapsed)
	}
	c.signal()
	return nil
}

func (c *Controller) applyOpen(w *window, control *edgeingestv1.RawWindowControl, now time.Time, elapsed time.Duration) {
	if w != nil {
		w.revision = control.GetRevision()
		w.version++
		return
	}

	cfg := control.GetOpen()
	w = &window{
		ref:       control.GetRef(),
		selector:  cfg.GetSelector(),
		expiresAt: cfg.GetExpiresAt().AsTime(),
		maxBytes:  cfg.GetMaxBytes(),
		revision:  control.GetRevision(),
	}
	c.windows[w.ref.GetRawWindow().GetId()] = w

	remaining := w.expiresAt.Sub(now)
	if remaining <= 0 {
		c.end(w, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED, w.expiresAt)
		return
	}
	w.deadline = elapsed + min(remaining, maxElapsed)
	w.activatedAt = now
	w.version++
	c.log.Info("raw window started",
		slog.String("otel.event.name", "flowseer.edge.raw_window.started"),
		slog.String("flowseer.raw_window.id", w.ref.GetRawWindow().GetId()),
		slog.Uint64("flowseer.raw_window.max_bytes", w.maxBytes),
		slog.Int64("flowseer.raw_window.remaining_ms", min(remaining, maxElapsed).Milliseconds()))
}

func (c *Controller) applyClose(w *window, control *edgeingestv1.RawWindowControl, now time.Time) {
	if w == nil {
		w = &window{
			ref:       control.GetRef(),
			expiresAt: control.GetClose().GetExpiresAt().AsTime(),
		}
		c.windows[w.ref.GetRawWindow().GetId()] = w
	}
	w.revision = control.GetRevision()
	if w.terminal {
		w.version++
		return
	}
	c.end(w, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_OPERATOR, now)
}

// end moves a window to its terminal state and queues its report.
func (c *Controller) end(w *window, reason rawwindowv1.RawWindowStopReason, at time.Time) {
	w.terminal = true
	w.stop = reason
	w.endedAt = at
	w.version++
	c.ended = true
	c.log.Info("raw window ended",
		slog.String("otel.event.name", "flowseer.edge.raw_window.ended"),
		slog.String("flowseer.raw_window.id", w.ref.GetRawWindow().GetId()),
		slog.String("flowseer.raw_window.stop_reason", reason.String()),
		slog.Uint64("flowseer.raw_window.selected_records", w.records))
}

// expire ends every window whose wall-clock expiry or elapsed deadline has
// passed. A window whose expiry equals the reading has expired.
func (c *Controller) expire(now time.Time, elapsed time.Duration) {
	for _, w := range c.windows {
		if w.terminal {
			continue
		}
		switch {
		case !now.Before(w.expiresAt):
			c.end(w, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED, w.expiresAt)
		case elapsed >= w.deadline:
			c.end(w, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED, now)
		}
	}
}

// Select decides which windows keep the original bytes of one record and
// charges each of them the record's length, once, before the adapter publishes
// it. It returns the ids of the windows that paid, sorted and distinct, or nil
// when no window retains the record. It returns at most [MaxWindowsPerRecord]
// ids: a window beyond them in id order is neither charged nor ended for this
// record, because an edge whose clock runs behind central's can hold more
// matching windows than the schema allows one record to name.
//
// A matching window whose remaining budget cannot hold the whole record ends
// without being charged, and without truncating the record for another window.
// A window the charge brings to zero remaining ends at once. A retry of the
// same publication must not call Select again: the debit is already made.
func (c *Controller) Select(obs Observation) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.expire(now, c.elapsed())

	var paid []string
	for _, id := range slices.Sorted(maps.Keys(c.windows)) {
		w := c.windows[id]
		if w.terminal || !w.matches(obs) {
			continue
		}
		if len(paid) == MaxWindowsPerRecord {
			break
		}
		if obs.Bytes > w.maxBytes-w.usedBytes {
			c.end(w, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_BYTE_BUDGET, now)
			continue
		}
		w.usedBytes += obs.Bytes
		w.records++
		w.version++
		paid = append(paid, id)
		if w.usedBytes == w.maxBytes {
			c.end(w, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_BYTE_BUDGET, now)
		}
	}
	c.signalIfEnded()
	return paid
}

// signalIfEnded wakes a reporter after a window ended. Usage counters change
// on every selected record, so they wait for the reporter's timer rather than
// waking it once per record.
func (c *Controller) signalIfEnded() {
	if c.ended {
		c.ended = false
		c.signal()
	}
}

func (w *window) matches(obs Observation) bool {
	switch w.selector.WhichScope() {
	case rawwindowv1.RawWindowSelector_Device_case:
		return w.selector.GetDevice().GetDevice().GetId() == obs.DeviceID
	case rawwindowv1.RawWindowSelector_Integration_case:
		return w.selector.GetIntegration().GetIntegration().GetId() == obs.IntegrationID
	case rawwindowv1.RawWindowSelector_Source_case:
		return w.selector.GetSource().GetKey() == obs.Source
	case rawwindowv1.RawWindowSelector_Scope_not_set_case:
		return false
	default:
		return false
	}
}

// Sweep ends the windows that have expired and drops the terminal windows whose
// expiry has passed and whose end central has acknowledged. A reporter calls it
// on a timer so central learns of an expiry even when no record arrives.
func (c *Controller) Sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.expire(now, c.elapsed())
	c.reap(now)
	c.signalIfEnded()
}

// reap forgets a terminal window once nothing can reopen it: its expiry has
// passed, so a late start is refused as expired, and central has its report.
func (c *Controller) reap(now time.Time) {
	for id, w := range c.windows {
		if w.terminal && w.version == w.reported && !now.Before(w.expiresAt) {
			delete(c.windows, id)
		}
	}
}

// PendingReports returns up to limit cumulative reports for windows whose state
// central has not acknowledged, in window id order. A report is cumulative, so
// sending one twice is harmless.
func (c *Controller) PendingReports(limit int) []Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expire(c.now(), c.elapsed())

	var reports []Report
	for _, id := range slices.Sorted(maps.Keys(c.windows)) {
		if len(reports) == limit {
			break
		}
		w := c.windows[id]
		if w.version == w.reported {
			continue
		}
		reports = append(reports, Report{Message: w.report(), version: w.version})
	}
	return reports
}

// Reported records that central accepted these reports. A window that changed
// since a report was built stays owed.
func (c *Controller) Reported(reports []Report) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range reports {
		w := c.windows[r.Message.GetState().GetRef().GetRawWindow().GetId()]
		if w != nil && r.version > w.reported {
			w.reported = r.version
		}
	}
	c.reap(c.now())
}

func (w *window) report() *edgeingestv1.RawWindowReport {
	state := rawwindowv1.RawWindowState_builder{
		Ref:       w.ref,
		Lifecycle: rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE.Enum(),
		Usage: rawwindowv1.RawWindowUsage_builder{
			SelectedBytes:   proto.Uint64(w.usedBytes),
			SelectedRecords: proto.Uint64(w.records),
		}.Build(),
	}
	if !w.activatedAt.IsZero() {
		state.ActivatedAt = timestamppb.New(w.activatedAt)
	}
	if w.terminal {
		state.Lifecycle = rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL.Enum()
		state.StopReason = w.stop.Enum()
		state.EndedAt = timestamppb.New(w.endedAt)
	}
	return edgeingestv1.RawWindowReport_builder{
		Revision: proto.Uint64(w.revision),
		State:    state.Build(),
	}.Build()
}
