package rawwindowapi

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
)

const (
	// casRetries bounds the compare-and-swap attempts of one update. Every
	// attempt that loses did so to another writer's success, so a contender
	// among n racing writers loses at most n-1 times and settles inside the
	// bound while fewer than casRetries writers race one edge.
	casRetries = 32

	// maxLiveWindows is how many unexpired windows one edge may hold, a close
	// tombstone and a window the edge ended early among them.
	maxLiveWindows = 16
	// maxTerminalRows is how many expired rows stay readable.
	maxTerminalRows = 64
	// terminalRetention is how long an expired row stays readable.
	terminalRetention = 24 * time.Hour
	// maxRecordBytes bounds one serialized edge record. The bus accepts a 1 MiB
	// payload by default, and the record is written whole on every change.
	maxRecordBytes = 256 << 10
	// maxWindowLifetime is the furthest expiry an open may name, measured from
	// the moment central admits it.
	maxWindowLifetime = time.Hour
)

// Registry is what the store and the operator service ask of the device
// registry to place a selector on an edge. The registry's interpretation of a
// binding stays its own: central compares ids and never reads an integration's
// kind.
type Registry interface {
	// EdgeID is the one edge the registry describes.
	EdgeID() string
	// IntegrationID is the integration that owns every binding the registry
	// lists.
	IntegrationID() string
	// Hosts reports whether the edge hosts the device.
	Hosts(ctx context.Context, edgeID, deviceID string) (bool, error)
}

// Store keeps the raw windows of every edge as one record per tenant and edge in
// a JetStream key-value bucket, written with compare-and-swap so that one
// update admits a window and bounds the edge's aggregate against concurrent
// opens. A Store is safe for concurrent use when its clock is.
//
// Expiry is lazy. Every read and write first treats a window past its expiry as
// terminal, and a write persists that. Nothing sweeps the bucket.
type Store struct {
	kv    jetstream.KeyValue
	clock func() time.Time
}

// NewStore returns a Store over the raw-windows bucket. The clock must be
// non-nil and safe for concurrent use.
func NewStore(kv jetstream.KeyValue, clock func() time.Time) *Store {
	return &Store{kv: kv, clock: clock}
}

// OpenRequest is one admission request. Everything the store decides on is in
// it, so the decision is made in one update.
type OpenRequest struct {
	// EdgeID names the edge that keeps the bytes.
	EdgeID string
	// Selector names the records the window keeps bytes for.
	Selector *rawwindowv1.RawWindowSelector
	// ExpiresAt is when collection ends. It must lie after the store's clock
	// and at most one hour after it.
	ExpiresAt time.Time
	// MaxBytes is the window's byte budget.
	MaxBytes uint64
	// Authorization names who asked and why. The caller sets requested_by from
	// the authenticated principal.
	Authorization *rawwindowv1.RawWindowAuthorization
	// AllowanceBytes is the deployment's aggregate allowance for one edge. Zero
	// means the deployment configures none and every open is refused.
	AllowanceBytes uint64
	// ResolveSelector reports whether the edge has the device or integration
	// the selector names, with a [ErrCodeUnknownSelector] error when it does
	// not. It runs after the edge and source checks and before the expiry and
	// capacity checks, and runs again when the update retries. Nil accepts
	// every selector.
	ResolveSelector func(ctx context.Context) error
}

// Subscribe fences the edge's process and records the sources it runs. The
// first subscription creates the record. A greater boot generation, or any
// generation under another enrollment, is a new process: every window that was
// not yet terminal ends with
// RAW_WINDOW_STOP_REASON_EDGE_RESTART and nothing is reissued, because replaying
// a start into a process with empty memory would hand its budget back. The same
// generation with the same epoch is a reconnect and refreshes the sources only.
//
// The enrollment names the edge's current enrollment, so a replacement box that
// enrolls as the same edge starts its boot generations over without being
// fenced out by the retired one. Within one enrollment, a lower generation or
// the same generation with another epoch fails with [ErrCodeStaleFence]. It
// returns how many windows a new process ended.
func (s *Store) Subscribe(ctx context.Context, tenantID, edgeID, enrollment string, generation uint64, epoch string, sources []*rawwindowv1.RawWindowSource) (int, error) {
	key, err := edgeKey(tenantID, edgeID)
	if err != nil {
		return 0, err
	}

	ended := 0
	err = s.mutate(ctx, key, func(rec *storev1.RawWindowEdgeRecord, now time.Time) (bool, error) {
		ended = 0
		current := rec.GetBootGeneration()
		reenrolled := rec.GetProcessEpoch() != "" && enrollment != rec.GetEnrollment()
		switch {
		case reenrolled:
			// A new enrollment is a new process whatever its generation says.
		case generation < current, generation == current && epoch != rec.GetProcessEpoch():
			return false, errs.New().Code(ErrCodeStaleFence).
				Attr("edge", edgeID).Attr("generation", generation).Attr("current_generation", current).
				Msg("subscription does not outrank the stored process")
		case generation == current:
			if slices.EqualFunc(rec.GetEnabledSources(), sources, func(a, b *rawwindowv1.RawWindowSource) bool { return proto.Equal(a, b) }) {
				return false, nil
			}
			rec.SetEnabledSources(cloneAll(sources))
			return true, nil
		}

		ended = endProcess(rec, now)
		rec.SetBootGeneration(generation)
		rec.SetProcessEpoch(epoch)
		if enrollment == "" {
			rec.ClearEnrollment()
		} else {
			rec.SetEnrollment(enrollment)
		}
		rec.SetEnabledSources(cloneAll(sources))
		if !rec.HasControlRevision() {
			rec.SetControlRevision(0)
		}
		return true, nil
	})
	return ended, err
}

// Open admits one window in a single update and returns it pending. The checks
// run in this order and the first failure is returned: the edge has
// subscribed ([ErrCodeNotSubscribed]), a source selector names an advertised
// source ([ErrCodeUnsupportedSource]), the selector's device or integration
// resolves, the expiry lies in (now, now+1h] ([ErrCodeBadExpiry]), the
// deployment configures an allowance ([ErrCodeNoAllowance]), the edge holds
// fewer than 16 unexpired windows ([ErrCodeWindowLimit]), and the reserved
// budgets plus MaxBytes fit the allowance ([ErrCodeBudgetExceeded]).
//
// A window reserves its full MaxBytes until it is terminal or expired, so a
// close that has not been delivered still reserves, and reported usage never
// frees room. A record that would exceed its serialized bound after every
// expired row was evicted fails with [ErrCodeRecordFull].
func (s *Store) Open(ctx context.Context, tenantID string, req OpenRequest) (*rawwindowv1.RawWindowRecord, error) {
	key, err := edgeKey(tenantID, req.EdgeID)
	if err != nil {
		return nil, err
	}

	var admitted *storev1.RawWindowEntry
	err = s.mutate(ctx, key, func(rec *storev1.RawWindowEdgeRecord, now time.Time) (bool, error) {
		if rec.GetProcessEpoch() == "" {
			return false, errs.New().Code(ErrCodeNotSubscribed).Attr("edge", req.EdgeID).Msg("edge has not subscribed")
		}
		if source := req.Selector.GetSource(); source != nil && !advertises(rec, source.GetKey()) {
			return false, errs.New().Code(ErrCodeUnsupportedSource).Attr("edge", req.EdgeID).Attr("source", source.GetKey()).
				Msg("edge does not run the source")
		}
		if req.ResolveSelector != nil {
			if err := req.ResolveSelector(ctx); err != nil {
				return false, err
			}
		}
		if !req.ExpiresAt.After(now) || req.ExpiresAt.After(now.Add(maxWindowLifetime)) {
			return false, errs.New().Code(ErrCodeBadExpiry).Attr("edge", req.EdgeID).
				Msg("expiry must be after now and at most one hour after now")
		}
		if req.AllowanceBytes == 0 {
			return false, errs.New().Code(ErrCodeNoAllowance).Attr("edge", req.EdgeID).
				Msg("no raw window byte allowance is configured")
		}
		if liveWindows(rec, now) >= maxLiveWindows {
			return false, errs.New().Code(ErrCodeWindowLimit).Attr("edge", req.EdgeID).
				Msg("edge holds the most unexpired windows it may")
		}
		reserved := reservedBytes(rec)
		if reserved > req.AllowanceBytes || req.MaxBytes > req.AllowanceBytes-reserved {
			return false, errs.New().Code(ErrCodeBudgetExceeded).Attr("edge", req.EdgeID).
				Attr("allowance", req.AllowanceBytes).Attr("reserved", reserved).Attr("requested", req.MaxBytes).
				Msg("budget does not fit the edge allowance")
		}

		revision := rec.GetControlRevision() + 1
		ref := rawwindowv1.RawWindowGlobalRef_builder{
			Edge:      edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: new(req.EdgeID)}.Build()}.Build(),
			RawWindow: rawwindowv1.RawWindowLocalRef_builder{Id: new(uuid.NewString())}.Build(),
		}.Build()
		admitted = storev1.RawWindowEntry_builder{
			Config: rawwindowv1.RawWindowConfig_builder{
				Ref:           ref,
				Selector:      proto.Clone(req.Selector).(*rawwindowv1.RawWindowSelector),
				ExpiresAt:     timestamppb.New(req.ExpiresAt),
				MaxBytes:      new(req.MaxBytes),
				Authorization: proto.Clone(req.Authorization).(*rawwindowv1.RawWindowAuthorization),
			}.Build(),
			State: rawwindowv1.RawWindowState_builder{
				Ref:       ref,
				Lifecycle: rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING.Enum(),
			}.Build(),
			Revision: new(revision),
		}.Build()
		rec.SetControlRevision(revision)
		rec.SetWindows(append(rec.GetWindows(), admitted))
		return true, fit(rec, now)
	})
	if err != nil {
		return nil, err
	}
	return recordOf(admitted), nil
}

// Window returns the window with the given id from any of the tenant's edges,
// with expiry applied as of now. It fails with [ErrCodeNotFound] when the tenant
// has no such window, including one pruned from history.
func (s *Store) Window(ctx context.Context, tenantID, windowID string) (*rawwindowv1.RawWindowRecord, error) {
	if err := tenant.Validate(tenantID); err != nil {
		return nil, errs.From(err).Code(ErrCodeBadKey).Attr("tenant", tenantID).Msg("validate tenant")
	}
	if _, err := uuid.Parse(windowID); err != nil {
		return nil, errs.From(err).Code(ErrCodeBadKey).Attr("window", windowID).Msg("raw window identifier is not a uuid")
	}

	keys, err := s.tenantKeys(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		rec, _, err := s.read(ctx, key)
		if err != nil {
			return nil, err
		}
		if entry := entryOf(rec, windowID); entry != nil {
			return recordOf(entry), nil
		}
	}
	return nil, errs.New().Code(ErrCodeNotFound).Attr("window", windowID).Msg("raw window not found")
}

// Close records that the operator wants the window to stop, bumps the window's
// revision so a close control is owed, and returns the window. The desired state
// is durable before anything is delivered. Closing a terminal window or one
// already closed changes nothing and returns it. A close holds the window's
// slot until its original expiry. It fails with [ErrCodeNotFound] when the edge
// has no such window.
func (s *Store) Close(ctx context.Context, tenantID, edgeID, windowID string) (*rawwindowv1.RawWindowRecord, error) {
	key, err := edgeKey(tenantID, edgeID)
	if err != nil {
		return nil, err
	}

	var closed *storev1.RawWindowEntry
	err = s.mutate(ctx, key, func(rec *storev1.RawWindowEdgeRecord, now time.Time) (bool, error) {
		closed = entryOf(rec, windowID)
		if closed == nil {
			return false, errs.New().Code(ErrCodeNotFound).Attr("window", windowID).Msg("raw window not found")
		}
		if closed.GetState().GetLifecycle() == rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL || closed.HasCloseRequestedAt() {
			return false, nil
		}

		revision := rec.GetControlRevision() + 1
		rec.SetControlRevision(revision)
		closed.SetRevision(revision)
		closed.SetCloseRequestedAt(timestamppb.New(now))
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return recordOf(closed), nil
}

// Report applies the edge process's cumulative state for the windows it holds.
// The whole request is refused when the generation or epoch is not the stored
// fence ([ErrCodeStaleFence]), when a report names a window of another edge
// ([ErrCodeForeignWindow]), reports a lifecycle a process cannot report, or
// reports a revision above the window's ([ErrCodeBadReport]). A report for a
// window the record no longer holds is skipped.
//
// Per window, a report below the revision already reported is ignored. Usage
// only grows, a terminal state is never reversed or overwritten by a later
// report, and the first active report fixes the activation time. Pending changes
// only when the edge reports.
func (s *Store) Report(ctx context.Context, tenantID, edgeID string, generation uint64, epoch string, reports []*edgeingestv1.RawWindowReport) error {
	key, err := edgeKey(tenantID, edgeID)
	if err != nil {
		return err
	}

	err = s.mutate(ctx, key, func(rec *storev1.RawWindowEdgeRecord, now time.Time) (bool, error) {
		if err := checkFence(rec, generation, epoch, ErrCodeStaleFence); err != nil {
			return false, err
		}

		type target struct {
			entry  *storev1.RawWindowEntry
			report *edgeingestv1.RawWindowReport
		}
		var targets []target
		for _, report := range reports {
			state := report.GetState()
			if state.GetRef().GetEdge().GetEdge().GetId() != edgeID {
				return false, errs.New().Code(ErrCodeForeignWindow).Attr("edge", edgeID).
					Attr("window", state.GetRef().GetRawWindow().GetId()).Msg("report names a window of another edge")
			}
			if err := checkReportable(state); err != nil {
				return false, err
			}
			entry := entryOf(rec, state.GetRef().GetRawWindow().GetId())
			if entry == nil {
				continue
			}
			if report.GetRevision() > entry.GetRevision() {
				return false, errs.New().Code(ErrCodeBadReport).Attr("window", state.GetRef().GetRawWindow().GetId()).
					Attr("reported", report.GetRevision()).Attr("issued", entry.GetRevision()).
					Msg("report revision is above the revision issued")
			}
			targets = append(targets, target{entry, report})
		}

		changed := false
		for _, t := range targets {
			before := proto.Clone(t.entry)
			applyReport(t.entry, t.report, now)
			changed = changed || !proto.Equal(before, t.entry)
		}
		return changed, nil
	})
	return err
}

// Owed returns the controls the edge process still owes a delivery for, in the
// order the windows were opened, and fails with [ErrCodeSuperseded] when the
// generation and epoch are no longer the stored fence. A window is owed when it
// is not terminal and its revision is above the one the edge reported: an open
// carrying the immutable configuration while no close was requested, otherwise
// a close carrying the original expiry. Each window has one control at its
// latest revision, so a greater revision supersedes the older ones and another
// window's revision never discards an undelivered start.
//
// It reads the record every call, so a caller that checks before each send
// learns of a newer subscription before it sends into the old stream.
func (s *Store) Owed(ctx context.Context, tenantID, edgeID string, generation uint64, epoch string) ([]*edgeingestv1.RawWindowControl, error) {
	key, err := edgeKey(tenantID, edgeID)
	if err != nil {
		return nil, err
	}
	rec, _, err := s.read(ctx, key)
	if err != nil {
		return nil, err
	}
	if err := checkFence(rec, generation, epoch, ErrCodeSuperseded); err != nil {
		return nil, err
	}

	var owed []*edgeingestv1.RawWindowControl
	for _, entry := range rec.GetWindows() {
		if entry.GetState().GetLifecycle() == rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL ||
			entry.GetRevision() <= entry.GetReportedRevision() {
			continue
		}
		control := edgeingestv1.RawWindowControl_builder{
			BootGeneration: new(generation),
			ProcessEpoch:   new(epoch),
			Ref:            proto.Clone(entry.GetConfig().GetRef()).(*rawwindowv1.RawWindowGlobalRef),
			Revision:       new(entry.GetRevision()),
		}
		if entry.HasCloseRequestedAt() {
			control.Close = edgeingestv1.RawWindowClose_builder{
				ExpiresAt: proto.Clone(entry.GetConfig().GetExpiresAt()).(*timestamppb.Timestamp),
			}.Build()
		} else {
			control.Open = proto.Clone(entry.GetConfig()).(*rawwindowv1.RawWindowConfig)
		}
		owed = append(owed, control.Build())
	}
	return owed, nil
}

type mutation func(rec *storev1.RawWindowEdgeRecord, now time.Time) (changed bool, err error)

// mutate runs fn over the latest record, with expiry and pruning applied, and
// writes the result with compare-and-swap, retrying a lost race. An absent
// record is an empty one that the first write creates. fn reports whether it
// changed the record, and an unchanged record is not written back.
func (s *Store) mutate(ctx context.Context, key string, fn mutation) error {
	for range casRetries {
		rec, revision, err := s.read(ctx, key)
		if err != nil {
			return err
		}

		changed, err := fn(rec, s.clock())
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}

		data, err := proto.Marshal(rec)
		if err != nil {
			return errs.From(err).Code(ErrCodeDecode).Attr("key", key).Msg("encode raw window record")
		}

		if revision == 0 {
			_, err = s.kv.Create(ctx, key, data)
			if errors.Is(err, jetstream.ErrKeyExists) {
				continue
			}
		} else {
			_, err = s.kv.Update(ctx, key, data, revision)
			if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
				continue
			}
		}
		if err != nil {
			return errs.From(err).Code(ErrCodeStore).Attr("key", key).Msg("write raw window record")
		}
		return nil
	}
	return errs.New().Code(ErrCodeConflict).Attr("key", key).Msg("raw window record update did not settle")
}

// read loads one edge's record with expiry and pruning applied in memory. An
// absent key is an empty record at revision zero.
func (s *Store) read(ctx context.Context, key string) (*storev1.RawWindowEdgeRecord, uint64, error) {
	entry, err := s.kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return &storev1.RawWindowEdgeRecord{}, 0, nil
	}
	if err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeStore).Attr("key", key).Msg("read raw window record")
	}

	rec := &storev1.RawWindowEdgeRecord{}
	if err := proto.Unmarshal(entry.Value(), rec); err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeDecode).Attr("key", key).Msg("decode raw window record")
	}
	reconcile(rec, s.clock())
	return rec, entry.Revision(), nil
}

// tenantKeys lists the keys of the tenant's edges.
func (s *Store) tenantKeys(ctx context.Context, tenantID string) ([]string, error) {
	keys, err := s.kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Msg("list raw window records")
	}

	prefix := tenantID + "."
	var own []string
	for _, key := range keys {
		if rest, ok := strings.CutPrefix(key, prefix); ok && isEdgeID(rest) {
			own = append(own, key)
		}
	}
	return own, nil
}

func edgeKey(tenantID, edgeID string) (string, error) {
	if err := tenant.Validate(tenantID); err != nil {
		return "", errs.From(err).Code(ErrCodeBadKey).Attr("tenant", tenantID).Msg("validate tenant")
	}
	if !isEdgeID(edgeID) {
		return "", errs.New().Code(ErrCodeBadKey).Attr("edge", edgeID).Msg("edge identifier is not a uuid")
	}
	return tenantID + "." + edgeID, nil
}

// isEdgeID reports whether id is a hyphenated uuid, the shape the edge schema
// gives an id and the only one that is a valid key segment in every case.
func isEdgeID(id string) bool {
	if len(id) != 36 {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}

// checkFence refuses a process the stored generation and epoch do not name,
// with the code the caller's situation calls for.
func checkFence(rec *storev1.RawWindowEdgeRecord, generation uint64, epoch string, code errs.Code) error {
	if rec.GetProcessEpoch() != "" && rec.GetBootGeneration() == generation && rec.GetProcessEpoch() == epoch {
		return nil
	}
	return errs.New().Code(code).Attr("generation", generation).Attr("current_generation", rec.GetBootGeneration()).
		Msg("process is not the one the record is fenced to")
}

func checkReportable(state *rawwindowv1.RawWindowState) error {
	switch state.GetLifecycle() {
	case rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE:
		return nil
	case rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL:
		if state.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_UNSPECIFIED && state.HasEndedAt() {
			return nil
		}
	}
	return errs.New().Code(ErrCodeBadReport).Attr("window", state.GetRef().GetRawWindow().GetId()).
		Attr("lifecycle", state.GetLifecycle().String()).Msg("a process reports an active window or a terminal one with its reason and end")
}

// applyReport folds one report into its window. The caller has refused a
// revision above the window's.
func applyReport(entry *storev1.RawWindowEntry, report *edgeingestv1.RawWindowReport, now time.Time) {
	if report.GetRevision() < entry.GetReportedRevision() {
		return
	}

	state := entry.GetState()
	reported := report.GetState()
	mergeUsage(state, reported.GetUsage())

	if state.GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL {
		switch reported.GetLifecycle() {
		case rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE:
			state.SetLifecycle(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE)
			if !state.HasActivatedAt() {
				if reported.HasActivatedAt() {
					state.SetActivatedAt(proto.Clone(reported.GetActivatedAt()).(*timestamppb.Timestamp))
				} else {
					state.SetActivatedAt(timestamppb.New(now))
				}
			}
		case rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL:
			state.SetLifecycle(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL)
			state.SetStopReason(reported.GetStopReason())
			state.SetEndedAt(proto.Clone(reported.GetEndedAt()).(*timestamppb.Timestamp))
		}
	}
	entry.SetReportedRevision(max(entry.GetReportedRevision(), report.GetRevision()))
}

// mergeUsage raises each counter to the reported value and never lowers one.
func mergeUsage(state *rawwindowv1.RawWindowState, reported *rawwindowv1.RawWindowUsage) {
	if reported == nil {
		return
	}
	current := state.GetUsage()
	state.SetUsage(rawwindowv1.RawWindowUsage_builder{
		SelectedBytes:   new(max(current.GetSelectedBytes(), reported.GetSelectedBytes())),
		SelectedRecords: new(max(current.GetSelectedRecords(), reported.GetSelectedRecords())),
	}.Build())
}

// reconcile applies the passage of time to a record: a window past its expiry
// is terminal, and history is pruned. It is idempotent and runs on every read.
func reconcile(rec *storev1.RawWindowEdgeRecord, now time.Time) {
	for _, entry := range rec.GetWindows() {
		if entry.GetState().GetLifecycle() == rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL || !expired(entry, now) {
			continue
		}
		state := entry.GetState()
		state.SetLifecycle(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL)
		state.SetStopReason(rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED)
		state.SetEndedAt(proto.Clone(entry.GetConfig().GetExpiresAt()).(*timestamppb.Timestamp))
		entry.SetReportedRevision(entry.GetRevision())
	}
	prune(rec, now)
}

// endProcess ends every window that was not terminal because the process that
// held it is gone, and reports how many it ended.
func endProcess(rec *storev1.RawWindowEdgeRecord, now time.Time) int {
	ended := 0
	for _, entry := range rec.GetWindows() {
		state := entry.GetState()
		if state.GetLifecycle() == rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL {
			continue
		}
		state.SetLifecycle(rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL)
		state.SetStopReason(rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EDGE_RESTART)
		state.SetEndedAt(timestamppb.New(now))
		entry.SetReportedRevision(entry.GetRevision())
		ended++
	}
	return ended
}

// prune drops expired rows older than the retention, then keeps only the newest
// expired rows up to the cap. An unexpired row is never dropped, so a tombstone
// holds its slot until its expiry whatever its lifecycle.
func prune(rec *storev1.RawWindowEdgeRecord, now time.Time) {
	type row struct {
		index int
		ended time.Time
	}
	var kept []row
	drop := map[int]bool{}
	for i, entry := range rec.GetWindows() {
		if !expired(entry, now) {
			continue
		}
		ended := entry.GetState().GetEndedAt().AsTime()
		if ended.Before(now.Add(-terminalRetention)) {
			drop[i] = true
			continue
		}
		kept = append(kept, row{i, ended})
	}
	if len(kept) > maxTerminalRows {
		slices.SortFunc(kept, func(a, b row) int {
			return cmp.Or(b.ended.Compare(a.ended), cmp.Compare(b.index, a.index))
		})
		for _, r := range kept[maxTerminalRows:] {
			drop[r.index] = true
		}
	}
	if len(drop) == 0 {
		return
	}

	var windows []*storev1.RawWindowEntry
	for i, entry := range rec.GetWindows() {
		if !drop[i] {
			windows = append(windows, entry)
		}
	}
	rec.SetWindows(windows)
}

// fit evicts the oldest expired rows until the serialized record is within its
// bound, and fails when only unexpired rows are left to evict.
func fit(rec *storev1.RawWindowEdgeRecord, now time.Time) error {
	for proto.Size(rec) > maxRecordBytes {
		windows := rec.GetWindows()
		oldest := -1
		for i, entry := range windows {
			if expired(entry, now) && (oldest < 0 || entry.GetState().GetEndedAt().AsTime().Before(windows[oldest].GetState().GetEndedAt().AsTime())) {
				oldest = i
			}
		}
		if oldest < 0 {
			return errs.New().Code(ErrCodeRecordFull).Attr("bytes", proto.Size(rec)).Attr("limit", maxRecordBytes).
				Msg("raw window record exceeds its bound with no expired row to evict")
		}
		rec.SetWindows(slices.Delete(slices.Clone(windows), oldest, oldest+1))
	}
	return nil
}

func expired(entry *storev1.RawWindowEntry, now time.Time) bool {
	return !now.Before(entry.GetConfig().GetExpiresAt().AsTime())
}

func liveWindows(rec *storev1.RawWindowEdgeRecord, now time.Time) int {
	live := 0
	for _, entry := range rec.GetWindows() {
		if !expired(entry, now) {
			live++
		}
	}
	return live
}

// reservedBytes sums the full budgets of the windows that are not terminal.
// Expiry has already made every expired window terminal.
func reservedBytes(rec *storev1.RawWindowEdgeRecord) uint64 {
	var reserved uint64
	for _, entry := range rec.GetWindows() {
		if entry.GetState().GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL {
			reserved += entry.GetConfig().GetMaxBytes()
		}
	}
	return reserved
}

func advertises(rec *storev1.RawWindowEdgeRecord, source string) bool {
	return slices.ContainsFunc(rec.GetEnabledSources(), func(s *rawwindowv1.RawWindowSource) bool { return s.GetKey() == source })
}

func entryOf(rec *storev1.RawWindowEdgeRecord, windowID string) *storev1.RawWindowEntry {
	for _, entry := range rec.GetWindows() {
		if entry.GetConfig().GetRef().GetRawWindow().GetId() == windowID {
			return entry
		}
	}
	return nil
}

func recordOf(entry *storev1.RawWindowEntry) *rawwindowv1.RawWindowRecord {
	return rawwindowv1.RawWindowRecord_builder{
		Config: proto.Clone(entry.GetConfig()).(*rawwindowv1.RawWindowConfig),
		State:  proto.Clone(entry.GetState()).(*rawwindowv1.RawWindowState),
	}.Build()
}

func cloneAll[T proto.Message](msgs []T) []T {
	out := make([]T, len(msgs))
	for i, msg := range msgs {
		out[i] = proto.Clone(msg).(T)
	}
	return out
}
