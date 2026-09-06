package access

import (
	"context"
	"sync"
	"time"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/audit"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/credential"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/drift"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/epoch"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/evidence"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/freeze"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/lane"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/mutation"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/recovery"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// Error codes lane.go returns. See each function's doc for when.
var (
	ErrCodeUnknownDevice     = errs.NewCode("access/unknown-device")
	ErrCodeNoPendingWait     = errs.NewCode("access/no-pending-wait")
	ErrCodeDesynchronized    = errs.NewCode("access/desynchronized")
	ErrCodeRecoveryAmbiguous = errs.NewCode("access/recovery-ambiguous")
)

// Config carries every dependency [Lane] needs. Construct with keyed
// fields and do not mutate afterward; [NewLane] copies it once and every
// [Lane] method thereafter reads its own copy, so a Config is safe to
// share for concurrent reads but not to write to concurrently with
// NewLane. QueueCapacity below 1 is silently raised to 1 rather than
// rejected.
type Config struct {
	QueueCapacity         int
	EvidencePolicy        evidence.Policy
	DelayedEffect         interfaces.DelayedEffect
	RecoveryMinGap        time.Duration
	Fenced                recovery.Fenced
	ManagementMode        inventoryv1.DeviceManagementMode
	ReadCredentials       credential.ReadCredentialSource
	SubmissionCredentials credential.SubmissionCredentialSource
	Audit                 audit.Deliverer
	Telemetry             *telemetry.View
	Clock                 func() time.Time
}

// DeviceSession is what one device needs to answer capability calls: an
// SNMP session and, optionally, a shell adapter for the fallback route.
// Sess must not be nil; Shell may be, when the device has no shell adapter
// configured. [Lane.AddDevice] copies it once; the fields themselves
// (Sess, Shell, the override closures) must be safe for the concurrent use
// this device's lane may make of them afterward.
type DeviceSession struct {
	Sess  snmp.Session
	Shell InterfaceShellAdapter
	Prov  InterfaceProvenanceInputs
	// BindingID names the integration binding this device is reachable
	// through, passed to SubmissionCredentialSource.Open as
	// OpenDeviceSubmissionRequest's binding_id — required and
	// UUID-constrained against a real EdgeService.
	BindingID string

	// ReadOverride and SubmitOverride, when set, replace the ordinary
	// interfaces.Read/ShellAdapter.SetPortName path entirely. Production
	// wiring leaves both nil; a test that wants to drive the lane without
	// a full SNMP or SSH fake sets one or both directly.
	ReadOverride   func(ctx context.Context, interfaceName string) (*accessv1.InterfaceObservation, error)
	SubmitOverride func(ctx context.Context, intent *accessv1.InterfaceDescriptionChange) error
	// FingerprintOverride, when set, is used as the device's starting
	// firmware fingerprint instead of running [epoch.Probe] against Sess —
	// which must otherwise be non-nil.
	FingerprintOverride string
}

type submission struct {
	// ctx is this submission's own caller's context, used to process this
	// item regardless of which goroutine's Submit call becomes the
	// device's active drainer. Using the drainer's own context instead
	// would let one caller's cancellation spuriously fail every other
	// item queued behind it on the same device.
	ctx     context.Context
	request *integrationv1.ExecuteRequest
	result  chan submissionOutcome
}

type submissionOutcome struct {
	result *integrationv1.ExecuteResult
	err    error
}

type deviceState struct {
	key      string
	session  DeviceSession
	queue    *lane.Queue
	draining sync.Mutex

	waitMu       sync.Mutex
	waitingSeq   uint64
	checkpointCh chan *integrationv1.CheckpointRequest
	termAckCh    chan *integrationv1.TerminalResultAck

	stateMu     sync.Mutex
	fingerprint string
	lastIntent  *accessv1.InterfaceDescriptionChange
	hold        recovery.Hold
}

// Lane is the edge-resident runtime that admits every read, probe,
// mutation, verification, and recovery step for every device this host
// serves, one ordered lane per device, per the direction record's decision
// 3. Construct with [NewLane]; the zero value is not usable. Safe for
// concurrent use: Submit, AddDevice, HandleCheckpoint, HandleTerminalAck,
// Freeze, Unfreeze, EvaluateDrift, ResolveHold, and Close may all be called
// from multiple goroutines. AddDevice is not safe to race against a Submit
// for the same not-yet-added deviceKey — the caller must ensure a device is
// added before any Submit names it, which every production call path does
// (onboarding runs once, before the device's lane can receive work).
type Lane struct {
	cfg       Config
	evid      *evidence.Store
	freeze    *freeze.Gate
	coalescer lane.Coalescer

	mu      sync.Mutex
	devices map[string]*deviceState
	closed  bool
}

// ErrCodeClosed identifies a Submit call rejected because [Lane.Close] has
// already run.
var ErrCodeClosed = errs.NewCode("access/lane-closed")

// ShutdownReport summarizes a [Lane.Close] call. It is currently empty:
// Close does not implement the plan's requirement 18 in full. It does stop
// new admission, and each already-admitted Submit call independently
// returns on its own passed-in context regardless of how long the
// background drainer spends on other items (drain runs on its own
// goroutine; see [Lane.drain]'s doc comment). What it does not do: wait for
// in-flight operations up to a configured deadline, or report an operation
// whose audit delivery did not complete as undelivered rather than simply
// never returning to its own caller. A host that needs those guarantees
// must build them from Submit's per-call context today; ShutdownReport is
// reserved for that accounting once it exists.
type ShutdownReport struct{}

// Close stops the Lane from admitting new work. Already-admitted items
// continue to their terminal result on whichever goroutine is draining
// them; Close does not wait for them and enforces no deadline on them —
// see [ShutdownReport]'s doc for exactly what requirement 18 this does and
// does not satisfy.
func (l *Lane) Close(_ context.Context) (ShutdownReport, error) {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return ShutdownReport{}, nil
}

// NewLane constructs a Lane governed by cfg. No device is known until
// [Lane.AddDevice] registers one.
func NewLane(cfg Config) *Lane {
	if cfg.SubmissionCredentials == nil {
		cfg.SubmissionCredentials = noopSubmissionSource{}
	}
	if cfg.Clock == nil {
		// Unlike a nil *telemetry.View (every View method guards a nil
		// receiver) or a nil Deliverer/Telemetry, a nil Clock panics on
		// its first call — Go cannot invoke a nil function value the way
		// it can no-op a nil-receiver method — so this default is load
		// bearing, not cosmetic.
		cfg.Clock = time.Now
	}
	return &Lane{
		cfg:     cfg,
		evid:    evidence.NewStore(cfg.EvidencePolicy),
		freeze:  freeze.New(cfg.Telemetry),
		devices: make(map[string]*deviceState),
	}
}

// noopSubmissionSource is Config's default SubmissionCredentialSource when
// a host has not wired a real one: it grants immediately, stays
// AUTHORIZED, and never ends, which is correct for a facade with no live
// central to revoke authority from.
type noopSubmissionSource struct{}

func (noopSubmissionSource) Open(context.Context, string, string, uint64) (credential.SubmissionHandle, error) {
	return noopSubmissionHandle{}, nil
}

type noopSubmissionHandle struct{}

func (noopSubmissionHandle) Grant() *edgev1.SubmissionGrant { return &edgev1.SubmissionGrant{} }
func (noopSubmissionHandle) Authority() edgev1.SubmissionAuthority {
	return edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED
}
func (noopSubmissionHandle) Err() error   { return nil }
func (noopSubmissionHandle) Close() error { return nil }

// AddDevice registers a device this Lane serves, keyed by deviceKey (an
// edge-local identifier; this package does not interpret it). It runs the
// route-independent identity probe once to learn the device's starting
// firmware fingerprint, per the onboarding sequence this module's README
// documents.
func (l *Lane) AddDevice(ctx context.Context, deviceKey string, session DeviceSession) error {
	fingerprint := session.FingerprintOverride
	if fingerprint == "" {
		probed, err := epoch.Probe(ctx, session.Sess)
		if err != nil {
			return errs.Wrap(err, "identity probe")
		}
		fingerprint = probed
	}

	l.cfg.Telemetry.DiscoveryCompleted(ctx, fingerprint)
	if l.cfg.Audit != nil {
		event := audit.BuildDiscoveryCompleted(l.cfg.Clock, audit.Common{Device: audit.Device{DeviceID: deviceKey}}, fingerprint)
		if err := l.cfg.Audit.Emit(ctx, event); err != nil {
			return errs.Wrap(err, "deliver discovery completed event")
		}
	}

	capacity := l.cfg.QueueCapacity
	if capacity < 1 {
		capacity = 1
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.devices[deviceKey] = &deviceState{
		key:         deviceKey,
		session:     session,
		queue:       lane.NewQueue(capacity),
		fingerprint: fingerprint,
	}
	return nil
}

// device looks up deviceKey, rejecting the call once [Lane.Close] has run.
// closed is read under the same lock as the map lookup so a concurrent
// Close cannot race this check by the Go memory model, not merely by
// observed timing.
func (l *Lane) device(deviceKey string) (*deviceState, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, errs.New().Code(ErrCodeClosed).Msg("lane is closed")
	}
	return l.deviceLocked(deviceKey)
}

// deviceRegardlessOfClosed looks up deviceKey without rejecting a closed
// Lane: [Lane.HandleCheckpoint] and [Lane.HandleTerminalAck] deliver
// central's messages to a mutation this Lane already admitted before
// Close ran, and Close's own doc states such an item "continues to its
// terminal result" — that promise requires central to still be able to
// unblock it after Close, since Close stops new admissions, not delivery
// to already-admitted work.
func (l *Lane) deviceRegardlessOfClosed(deviceKey string) (*deviceState, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.deviceLocked(deviceKey)
}

// deviceLocked looks up deviceKey. The caller must hold l.mu.
func (l *Lane) deviceLocked(deviceKey string) (*deviceState, error) {
	ds, ok := l.devices[deviceKey]
	if !ok {
		return nil, errs.New().Code(ErrCodeUnknownDevice).Attr("device", deviceKey).
			Msgf("device %s was never added to this lane", deviceKey)
	}
	return ds, nil
}

// SubmitOptions is one admission request. An immutable value once passed
// to [Lane.Submit]; safe to build fresh per call from any goroutine.
type SubmitOptions struct {
	// DeviceKey names the device this operation targets, matching a prior
	// [Lane.AddDevice] call. The execution envelope carries no device or
	// edge ref of its own (the transport already addresses one), so the
	// caller supplies it here.
	DeviceKey string
	Request   *integrationv1.ExecuteRequest
	Priority  lane.Priority
}

// Submit admits opts.Request into its device's lane and blocks until that
// operation reaches a terminal result: an observation or an error. It never
// silently drops an admitted item — every admission reaches a terminal
// result or an explicit cancellation, even if this call's own context ends
// first (the drain loop that already started keeps running for whichever
// goroutine is driving it).
func (l *Lane) Submit(ctx context.Context, opts SubmitOptions) (*integrationv1.ExecuteResult, error) {
	if opts.Request.GetSequence() == 0 && opts.Request.GetMutation() != nil {
		return nil, errs.New().Msg("mutation request must carry a sequence")
	}

	// internal/lane.Queue rejects PriorityUnspecified outright (a missing
	// choice must never silently become the lowest priority); at this
	// facade a caller leaving SubmitOptions.Priority at its zero value in a
	// keyed struct literal is the ordinary mistake, not a deliberate
	// choice, so it is coerced to PriorityNormal rather than rejected here.
	if opts.Priority == lane.PriorityUnspecified {
		opts.Priority = lane.PriorityNormal
	}

	ds, err := l.device(opts.DeviceKey)
	if err != nil {
		return nil, err
	}

	if ds.hold.Active() && opts.Request.GetMutation() != nil {
		return nil, errs.New().Code(ErrCodeDesynchronized).
			Msg("this device's lane is on hold; call ResolveHold before admitting another mutation")
	}

	// Poll coalescing: a TypedRead matching one already queued or in flight
	// for this device and interface joins that ticket instead of admitting
	// a second lane entry. A mutation never coalesces — two intents are
	// distinguished by idempotency key even when they target the same
	// field.
	if read := opts.Request.GetRead(); read != nil {
		key := lane.CoalesceKey{Device: opts.DeviceKey, OperationKind: "interface_read", Target: read.GetInterface().GetInterfaceName()}
		ticket, isNew := l.coalescer.Start(key)
		if !isNew {
			result, err := ticket.Wait(ctx)
			if err != nil {
				return nil, err
			}
			execResult, _ := result.(*integrationv1.ExecuteResult)
			return execResult, nil
		}
		return l.submitAndCoalesce(ctx, ds, opts, key)
	}

	sub := &submission{ctx: ctx, request: opts.Request, result: make(chan submissionOutcome, 1)}
	if _, err := ds.queue.Submit(opts.Priority, sub); err != nil {
		return nil, err
	}

	l.drain(ds)

	select {
	case outcome := <-sub.result:
		return outcome.result, outcome.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// submitAndCoalesce is Submit's path for the first caller of a coalescing
// key: it admits the item as usual, then delivers the result to every
// coalesced waiter through the Coalescer ticket once processing completes.
func (l *Lane) submitAndCoalesce(ctx context.Context, ds *deviceState, opts SubmitOptions, key lane.CoalesceKey) (*integrationv1.ExecuteResult, error) {
	// The actual work is detached from this caller's own cancellation: a
	// joiner with a longer deadline is depending on this read completing,
	// so the owner giving up early must not kill it, and the owner's
	// cancellation must never be handed to every joiner as if it were the
	// read's own outcome (that is exactly the bug 542b303f fixed for the
	// plain queue path but left open here).
	workCtx := context.WithoutCancel(ctx)
	sub := &submission{ctx: workCtx, request: opts.Request, result: make(chan submissionOutcome, 1)}
	if _, err := ds.queue.Submit(opts.Priority, sub); err != nil {
		l.coalescer.Finish(key, nil, err)
		return nil, err
	}

	l.drain(ds)

	// One goroutine, independent of any single caller's context, owns
	// finishing the ticket from the work's actual outcome — never from a
	// caller's own timeout — so every other joiner still waiting on
	// ticket.Wait sees the real result.
	done := make(chan submissionOutcome, 1)
	go func() {
		outcome := <-sub.result
		l.coalescer.Finish(key, outcome.result, outcome.err)
		done <- outcome
	}()

	select {
	case outcome := <-done:
		return outcome.result, outcome.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// drain becomes the device's sole active worker if none is already running,
// processing every currently- and newly-admitted item in Position order
// until the queue empties. If another call is already draining, this call
// returns immediately — the active drainer will reach the item this call
// just admitted.
// drain runs on its own goroutine so Submit's own select only ever waits on
// its own context and result channel — the drainer never borrows a caller's
// deadline for work that caller did not admit. It stops trying to become
// the active worker once TryLock fails (some other goroutine already is
// one), and, on its own exit, re-checks the queue length after unlocking:
// Queue and draining are independent locks, so an item admitted in the gap
// between this goroutine's last empty Next() and its Unlock would otherwise
// sit forever with no drainer — the recheck-and-retry loop closes that
// window instead of relying on the racing Submit's own drain call to win a
// TryLock it may lose only because this goroutine has not unlocked yet.
func (l *Lane) drain(ds *deviceState) {
	go func() {
		for {
			if !ds.draining.TryLock() {
				return
			}
			for {
				item, ok := ds.queue.Next()
				if !ok {
					break
				}
				sub := item.Payload.(*submission)
				// Each item is processed under its own submitter's
				// context, never the context of whichever goroutine
				// happened to become the drainer: this device may drain
				// several callers' items in one loop, and one caller's
				// cancellation or deadline must never spuriously fail
				// another caller's still-live item queued behind it.
				result, err := l.process(sub.ctx, ds, sub.request)
				sub.result <- submissionOutcome{result: result, err: err}
			}
			ds.draining.Unlock()
			if ds.queue.Len() == 0 {
				return
			}
			// Something was admitted in the gap between the last empty
			// Next() and the Unlock above; loop back and try to become
			// the drainer again rather than leaving it stranded.
		}
	}()
}

// HandleCheckpoint delivers central's CheckpointRequest to the device's
// currently in-flight mutation, if one is waiting for exactly this
// sequence. A future host's message loop calls this from the execution
// envelope's CheckpointRequest, per decision 4's barrier.
func (l *Lane) HandleCheckpoint(deviceKey string, req *integrationv1.CheckpointRequest) error {
	ds, err := l.deviceRegardlessOfClosed(deviceKey)
	if err != nil {
		return err
	}

	ds.waitMu.Lock()
	defer ds.waitMu.Unlock()
	ch := ds.checkpointCh
	match := ds.waitingSeq == req.GetSequence()
	if ch == nil || !match {
		return errs.New().Code(ErrCodeNoPendingWait).
			Msgf("no mutation on device %s is waiting for a checkpoint at sequence %d", deviceKey, req.GetSequence())
	}
	// Send and clear under the same lock the match check ran under, so an
	// at-least-once redelivery of the same CheckpointRequest after the
	// first one already won sees ch == nil here rather than resending into
	// a channel nothing is left to drain (the capacity-1 buffer accepts
	// exactly one send with no receiver; a second would block forever).
	select {
	case ch <- req:
	default:
		return errs.New().Code(ErrCodeNoPendingWait).
			Msgf("checkpoint for device %s sequence %d was already delivered", deviceKey, req.GetSequence())
	}
	ds.checkpointCh = nil
	return nil
}

// HandleTerminalAck delivers central's TerminalResultAck, which frees the
// device's lane for the next sequence per decision 4's barrier.
func (l *Lane) HandleTerminalAck(deviceKey string, ack *integrationv1.TerminalResultAck) error {
	ds, err := l.deviceRegardlessOfClosed(deviceKey)
	if err != nil {
		return err
	}

	ds.waitMu.Lock()
	defer ds.waitMu.Unlock()
	ch := ds.termAckCh
	match := ds.waitingSeq == ack.GetSequence()
	if ch == nil || !match {
		return errs.New().Code(ErrCodeNoPendingWait).
			Msgf("no mutation on device %s is waiting for a terminal ack at sequence %d", deviceKey, ack.GetSequence())
	}
	// See HandleCheckpoint's comment: send and clear under one lock so an
	// at-least-once redelivery cannot block forever on an orphaned channel.
	select {
	case ch <- ack:
	default:
		return errs.New().Code(ErrCodeNoPendingWait).
			Msgf("terminal ack for device %s sequence %d was already delivered", deviceKey, ack.GetSequence())
	}
	ds.termAckCh = nil
	return nil
}

func (ds *deviceState) awaitCheckpoint(ctx context.Context, seq uint64) (*integrationv1.CheckpointRequest, error) {
	ch := make(chan *integrationv1.CheckpointRequest, 1)
	ds.waitMu.Lock()
	ds.waitingSeq = seq
	ds.checkpointCh = ch
	ds.waitMu.Unlock()
	defer func() {
		ds.waitMu.Lock()
		ds.checkpointCh = nil
		ds.waitMu.Unlock()
	}()

	select {
	case req := <-ch:
		return req, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (ds *deviceState) awaitTerminalAck(ctx context.Context, seq uint64) (*integrationv1.TerminalResultAck, error) {
	ch := make(chan *integrationv1.TerminalResultAck, 1)
	ds.waitMu.Lock()
	ds.waitingSeq = seq
	ds.termAckCh = ch
	ds.waitMu.Unlock()
	defer func() {
		ds.waitMu.Lock()
		ds.termAckCh = nil
		ds.waitMu.Unlock()
	}()

	select {
	case ack := <-ch:
		return ack, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// process runs one admitted request through the mutation state machine's
// ordinary path: plan, checkpoint (mutation only), execute, observe,
// compare, and — for a verified mutation — acknowledge and release. An
// ambiguous or failed step (an execute error, a non-VERIFIED disposition, a
// conflicting read) is reported as this call's error rather than being
// automatically retried: automatic recovery and drift resolution are this
// module's internal/recovery and internal/drift packages, exercised
// directly by their own tests; wiring their retry loop into this
// synchronous drain requires a real clock-driven poll only a host with a
// live transport can run (see README's onboarding section), so this
// package's own tests drive recovery and drift explicitly rather than
// through Submit.
func (l *Lane) process(ctx context.Context, ds *deviceState, req *integrationv1.ExecuteRequest) (*integrationv1.ExecuteResult, error) {
	ds.stateMu.Lock()
	fingerprint := ds.fingerprint
	ds.stateMu.Unlock()

	deps := l.machineDeps(ds, fingerprint, req)
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		return nil, err
	}

	if !m.IsRead() {
		checkpointReq, err := ds.awaitCheckpoint(ctx, req.GetSequence())
		if err != nil {
			return nil, errs.Wrap(err, "await checkpoint")
		}
		if _, err := m.Checkpoint(ctx, checkpointReq); err != nil {
			return nil, err
		}
		if err := m.Execute(ctx); err != nil {
			ds.hold.Engage()
			return nil, errs.Wrap(err, "execute")
		}
	}

	obs, err := m.Observe(ctx)
	if err != nil {
		if !m.IsRead() {
			ds.hold.Engage()
		}
		return nil, errs.Wrap(err, "observe")
	}
	l.recordEvidence(ds, fingerprint, req, obs)

	disposition, err := m.Compare(ctx, nil)
	if err != nil {
		if !m.IsRead() {
			ds.hold.Engage()
		}
		return nil, err
	}

	if m.IsRead() {
		return m.Result(), nil
	}

	if disposition != accessv1.Disposition_DISPOSITION_VERIFIED {
		// The mutation may have reached the device but was never verified:
		// decision 5's ambiguity-stays-indeterminate rule means the lane
		// must not admit another mutation on top of an unresolved change.
		// The caller drives internal/recovery explicitly (see this
		// function's doc comment); until it resolves this device's hold
		// (or an operator/reconciliation call does), Submit refuses every
		// further mutation for it.
		ds.hold.Engage()
		return nil, errs.New().Code(ErrCodeRecoveryAmbiguous).
			Msg("mutation was not verified; drive internal/recovery explicitly for this sequence")
	}

	if err := m.MarkVerified(ctx); err != nil {
		return nil, err
	}

	termAck, err := ds.awaitTerminalAck(ctx, req.GetSequence())
	if err != nil {
		return nil, errs.Wrap(err, "await terminal acknowledgement")
	}
	if err := m.Acknowledge(ctx, termAck); err != nil {
		return nil, err
	}

	ds.stateMu.Lock()
	ds.lastIntent = req.GetMutation().GetInterfaceDescription()
	ds.stateMu.Unlock()

	return m.Result(), nil
}

// interfaceName returns the device-local interface name req's mutation or
// read arm targets, whichever is set.
func interfaceName(req *integrationv1.ExecuteRequest) string {
	if mutationIntent := req.GetMutation(); mutationIntent != nil {
		return mutationIntent.GetInterfaceDescription().GetInterfaceName()
	}
	return req.GetRead().GetInterface().GetInterfaceName()
}

// machineDeps builds this request's Read/Submit closures bound to the one
// interface it targets and ds's session, since [mutation.Deps.Read] and
// [mutation.Deps.Submit] carry no target of their own — every other field
// this module's route-selection and submission machinery needs is already
// in prov and sess.
func (l *Lane) machineDeps(ds *deviceState, fingerprint string, req *integrationv1.ExecuteRequest) mutation.Deps {
	sess := ds.session
	name := interfaceName(req)

	return mutation.Deps{
		CurrentFingerprint: fingerprint,
		DeviceID:           ds.key,
		BindingID:          sess.BindingID,
		Read: func(ctx context.Context) (*accessv1.InterfaceObservation, error) {
			if sess.ReadOverride != nil {
				return sess.ReadOverride(ctx, name)
			}
			return interfaces.Read(ctx, sess.Sess, sess.Shell, name, sess.Prov, nil, interfaces.Freshness{}, l.cfg.Clock())
		},
		Submit: func(ctx context.Context, intent *accessv1.InterfaceDescriptionChange) error {
			if sess.SubmitOverride != nil {
				return sess.SubmitOverride(ctx, intent)
			}
			if sess.Shell == nil {
				return errs.New().Msg("device has no shell adapter configured; a mutation cannot be submitted")
			}
			return sess.Shell.SetPortName(ctx, intent.GetInterfaceName(), intent.GetDescription())
		},
		Submission: l.cfg.SubmissionCredentials,
		Freeze:     l.freeze,
		Audit:      l.cfg.Audit,
		Telemetry:  l.cfg.Telemetry,
		Clock:      l.cfg.Clock,
	}
}

// recordEvidence records which route answered req's operation, when the
// observation is complete enough to say so, per decision 1's live
// per-operation evidence. A record failure (no configured lifetime for this
// operation kind) is not this call's problem to report — evidence is a
// caching optimization for a later admission, never a gate this one needs
// to pass — so the error is discarded.
func (l *Lane) recordEvidence(ds *deviceState, fingerprint string, req *integrationv1.ExecuteRequest, obs *accessv1.InterfaceObservation) {
	if obs.GetCompleteness() != accessv1.Completeness_COMPLETENESS_COMPLETE {
		return
	}
	kind := evidence.KindInterfaceRead
	if req.GetMutation() != nil {
		kind = evidence.KindInterfaceDescriptionChange
	}
	route := obs.GetProvenance().GetProtocol()
	_ = l.evid.Record(ds.key, fingerprint, kind, route, obs.GetCompleteness(), l.cfg.Clock())
}

// Freeze pauses side effects across every device this Lane serves, per
// decision 8's control-plane freeze.
func (l *Lane) Freeze(ctx context.Context) { l.freeze.Freeze(ctx) }

// Unfreeze resumes side effects.
func (l *Lane) Unfreeze(ctx context.Context) { l.freeze.Unfreeze(ctx) }

// EvaluateDrift runs drift detection for deviceKey against a fresh
// observation, per decision 6. See [drift.Evaluate].
func (l *Lane) EvaluateDrift(ctx context.Context, deviceKey string, observed *accessv1.InterfaceObservation, inFlight bool) (drift.Outcome, error) {
	ds, err := l.device(deviceKey)
	if err != nil {
		return drift.Outcome{}, err
	}

	// A hold already names an unresolved mutation whose effect on this
	// device is unknown; lastIntent may not yet reflect it (it is only
	// updated after a successful terminal acknowledgement), so treating a
	// difference from lastIntent as drift here would report the lane's own
	// unresolved change as an out-of-band one. The hold's own resolution
	// path — not a second, competing drift block — is what un-sticks this
	// device.
	if ds.hold.Active() {
		return drift.Outcome{}, nil
	}

	ds.stateMu.Lock()
	lastIntent := ds.lastIntent
	ds.stateMu.Unlock()

	outcome := drift.Evaluate(observed, lastIntent, inFlight, l.cfg.ManagementMode)
	if outcome.Drifted {
		l.cfg.Telemetry.DriftDetected(ctx)
		if outcome.Blocked {
			ds.hold.Engage()
		}
	}
	return outcome, nil
}

// ResolveHold clears deviceKey's recovery or drift hold, admitting
// mutations again.
func (l *Lane) ResolveHold(deviceKey string) error {
	ds, err := l.device(deviceKey)
	if err != nil {
		return err
	}
	ds.hold.Resolve()
	return nil
}
