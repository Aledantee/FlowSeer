package access

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/audit"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/credential"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/epoch"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/evidence"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/freeze"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/lane"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/mutation"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/recovery"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
	"go.aledante.io/FlowSeer/src/protocol/snmp"

	"google.golang.org/protobuf/proto"
)

// Error codes lane.go returns. See each function's doc for when.
var (
	ErrCodeUnknownDevice     = errs.NewCode("access/unknown-device")
	ErrCodeNoPendingWait     = errs.NewCode("access/no-pending-wait")
	ErrCodeDesynchronized    = errs.NewCode("access/desynchronized")
	ErrCodeRecoveryAmbiguous = errs.NewCode("access/recovery-ambiguous")
)

// Refusal codes produced inside internal/mutation and re-exported here.
// They cross the wire to central alongside the codes above, and a code that
// leaves the process is public contract whatever package produces it — so a
// host branching on what an acknowledgement was refused for finds every
// answer in one place rather than reaching into an internal package it
// cannot import.
var (
	// ErrCodeAlreadyTerminal answers an acknowledgement for a mutation
	// that has already ended; its own terminal report is on the way.
	ErrCodeAlreadyTerminal = mutation.ErrCodeAlreadyTerminal
	// ErrCodeOutOfOrder answers a disposition the mutation's phase does
	// not allow, with nothing changed.
	ErrCodeOutOfOrder = mutation.ErrCodeOutOfOrder
)

// Reporter receives everything this lane owes its host to pass on to
// central: each phase a mutation reaches, each observation, and each
// acknowledgement of a message central sent. The lane calls it on the
// goroutine that did the work.
//
// An implementation must return at once and must not do I/O. The lane
// reports while it holds a device's drain lock, so a Reporter that blocked
// on a network send would stall every other operation queued for that
// device behind whatever central's connection is doing — and the whole
// point of reporting is that it is one-way. Hand the message to a queue and
// return; delivering it is the host's problem, and the host is the only
// party that can retry it.
//
// A nil Reporter is a no-op. The lane never fails an operation because a
// report could not be made, which is why these methods return nothing:
// there is no failure for a caller to act on.
type Reporter interface {
	// Reported carries one ExecuteResult: a progress report at admission
	// and on entering recovery, an observation for a read or a verified
	// mutation, an error, or a terminal phase.
	Reported(ctx context.Context, result *integrationv1.ExecuteResult)
	// CheckpointAcked carries the acknowledgement of central's
	// CheckpointRequest.
	CheckpointAcked(ctx context.Context, ack *integrationv1.CheckpointAck)
	// HoldResolvedAcked carries the acknowledgement of central's
	// HoldResolved.
	HoldResolvedAcked(ctx context.Context, ack *integrationv1.HoldResolvedAck)
}

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
	ReadCredentials       credential.ReadCredentialSource
	Reporter              Reporter
	SubmissionCredentials credential.SubmissionCredentialSource
	Audit                 audit.Deliverer
	Telemetry             *telemetry.View
	Clock                 func() time.Time
	// OperationTimeout is a FLOOR on a coalesced read's device call — the
	// actual work its joiners depend on, detached from any single
	// caller's own context so one caller's cancellation cannot fail every
	// other item queued behind it. It does not bound a mutation's
	// Execute/Observe, which runs under the submitting caller's own
	// context. Without its own floor the detached read would run forever
	// against a device that accepts a connection but never answers,
	// parking the device's drain goroutine and the coalescing ticket
	// permanently — but it is a floor, not a ceiling: a caller whose own
	// deadline is longer than OperationTimeout keeps that longer
	// deadline, since a joiner may depend on it, rather than being cut
	// down to this default. Zero is silently raised to a default of 30s.
	OperationTimeout time.Duration
}

// SNMPSession is one open SNMP session and the way to close it. The lane
// closes every session it opens, at the end of the operation that opened
// it.
type SNMPSession struct {
	Session snmp.Session
	Close   func() error
}

// ShellSession is one open shell session and the way to close it.
type ShellSession struct {
	Adapter InterfaceShellAdapter
	Close   func() error
}

// DeviceSession is how the lane reaches one device. It is a set of
// factories, not a live connection: every operation opens its own session
// from credential material acquired for that operation and closes it when
// the operation ends.
//
// That is the whole point of the shape. A standing session outlives the
// credential it was opened with, so a credential central revoked would go
// on working for as long as the connection stayed up, and revocation would
// mean nothing until something happened to drop it. Opening per operation
// means the authority is checked by the act of acquiring, every time.
//
// [Lane.AddDevice] copies this struct once. The closures must be safe for
// the concurrent use one device's lane makes of them.
type DeviceSession struct {
	// OpenSNMP opens an SNMP session from material acquired for this
	// operation. Required: the identity probe at onboarding and every read
	// go through it.
	OpenSNMP func(ctx context.Context, cred *edgev1.DeviceCredential) (SNMPSession, error)
	// OpenShell opens a shell session. Used for the fallback read route
	// and for a mutation's own command, and may be nil for a device with
	// no shell adapter — a mutation on such a device fails rather than
	// silently doing nothing.
	OpenShell func(ctx context.Context, cred *edgev1.DeviceCredential, hostKeySHA256 string) (ShellSession, error)

	// AccessPolicy is the handle the onboarding identity probe acquires
	// its credential under. A read carries its own on TypedRead, since
	// central decides per read which policy admitted it; onboarding has no
	// TypedRead to carry one, so it comes from here.
	AccessPolicy *policyv1.AccessPolicyHandle

	Prov InterfaceProvenanceInputs
	// BindingID names the integration binding this device is reachable
	// through, passed to the credential sources as
	// OpenDeviceSubmissionRequest's and AcquireReadCredentialRequest's
	// binding_id — required and UUID-constrained against a real
	// EdgeService.
	BindingID string

	// ReadOverride and SubmitOverride, when set, replace the device call
	// itself — interfaces.Read, or the shell's SetPortName. They do not
	// replace acquiring a credential, and they never bypass it: a test
	// using one still proves the acquisition happened, which is the
	// property most worth not being able to switch off. Production wiring
	// leaves both nil.
	ReadOverride   func(ctx context.Context, interfaceName string) (*accessv1.InterfaceObservation, error)
	SubmitOverride func(ctx context.Context, intent *accessv1.InterfaceDescriptionChange) error
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

	stateMu     sync.Mutex
	fingerprint string
	hold        recovery.Hold
	// current is the machine central's acknowledgements address: set when a
	// mutation is admitted, cleared when it finishes. A read never sets it —
	// central acknowledges nothing about a read — so a mutation's ack is
	// never delivered to a read that happens to be running.
	current *mutation.Machine
}

// Lane is the edge-resident runtime that admits every read, probe,
// mutation, verification, and recovery step for every device this host
// serves, one ordered lane per device, per the direction record's decision
// 3. Construct with [NewLane]; the zero value is not usable. Safe for
// concurrent use: Submit, AddDevice, HandleCheckpoint, HandleTerminalAck,
// Freeze, Unfreeze, ResolveHold, and Close may all be called
// from multiple goroutines. AddDevice is not safe to race against a Submit
// for the same not-yet-added deviceKey — the caller must ensure a device is
// added before any Submit names it, which every production call path does
// (onboarding runs once, before the device's lane can receive work). A
// second AddDevice for an already-registered deviceKey replaces its
// *deviceState wholesale, orphaning that device's own queue and any
// in-flight drainer goroutine still working through it: onboarding for one
// device must run exactly once, never as a way to reset or reconfigure an
// already-added one.
type Lane struct {
	cfg       Config
	evid      *evidence.Store
	freeze    *freeze.Gate
	coalescer lane.Coalescer

	mu      sync.Mutex
	devices map[string]*deviceState
	closed  bool

	// freezeMu guards frozenDelivered and serializes the emission of
	// LaneFrozen records, so two concurrent Freeze calls cannot both decide
	// the same device still owes one. It is never held across Gate.Freeze's
	// drain wait, which can block behind a device write.
	freezeMu        sync.Mutex
	frozenDelivered map[string]bool
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

// defaultOperationTimeout bounds an admitted item's device call when
// Config.OperationTimeout is unset.
const defaultOperationTimeout = 30 * time.Second

// NewLane constructs a Lane governed by cfg. No device is known until
// [Lane.AddDevice] registers one.
func NewLane(cfg Config) *Lane {
	if cfg.SubmissionCredentials == nil {
		cfg.SubmissionCredentials = noopSubmissionSource{}
	}
	if cfg.ReadCredentials == nil {
		// Same reasoning as the submission default: a facade with no live
		// central to acquire from must still be able to read, and a nil
		// interface here would panic on the first read rather than saying
		// so. The no-op source returns an empty credential, which a host's
		// own OpenSNMP is free to interpret as "use whatever this device
		// was configured with".
		cfg.ReadCredentials = noopReadSource{}
	}
	if cfg.Clock == nil {
		// Unlike a nil *telemetry.View (every View method guards a nil
		// receiver) or a nil Deliverer/Telemetry, a nil Clock panics on
		// its first call — Go cannot invoke a nil function value the way
		// it can no-op a nil-receiver method — so this default is load
		// bearing, not cosmetic.
		cfg.Clock = time.Now
	}
	if cfg.Audit == nil {
		// Unlike *telemetry.View, audit.Deliverer is a plain interface
		// with no nil-receiver guard of its own: a nil Audit reaches
		// internal/mutation's Emit calls unguarded and panics on the
		// first admitted mutation. AddDevice tolerates a nil Audit
		// locally, which made a host reasonably conclude Audit was
		// optional everywhere; default it here so it is optional
		// everywhere, not just at that one call site.
		cfg.Audit = noopDeliverer{}
	}
	if cfg.OperationTimeout <= 0 {
		cfg.OperationTimeout = defaultOperationTimeout
	}
	return &Lane{
		cfg:             cfg,
		evid:            evidence.NewStore(cfg.EvidencePolicy),
		freeze:          freeze.New(cfg.Telemetry),
		devices:         make(map[string]*deviceState),
		frozenDelivered: make(map[string]bool),
	}
}

// noopDeliverer is Config's default audit.Deliverer when a host has not
// wired a real one: every Emit succeeds immediately without recording
// anything, so a host that has no durable audit sink yet does not need to
// name one to use this module at all.
type noopDeliverer struct{}

func (noopDeliverer) Emit(context.Context, *eventv1.DeviceOperationEvent) error { return nil }

// noopReadSource is Config's default ReadCredentialSource when a host has
// not wired a real one: it returns an empty credential immediately, which a
// host's own OpenSNMP is free to read as "use whatever this device was
// configured with".
type noopReadSource struct{}

func (noopReadSource) AcquireReadCredential(context.Context, string, string, *policyv1.AccessPolicyHandle) (*edgev1.AcquireReadCredentialResponse, error) {
	return &edgev1.AcquireReadCredentialResponse{}, nil
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
	// The onboarding probe acquires its own credential, under the handle
	// this device was registered with, and opens a session that lives only
	// as long as the probe. There is no cached session to reuse afterwards:
	// the next operation acquires again.
	fingerprint, err := l.probeIdentity(ctx, deviceKey, session)
	if err != nil {
		return err
	}

	l.cfg.Telemetry.DiscoveryCompleted(ctx, fingerprint)
	if l.cfg.Audit != nil {
		event := audit.BuildDiscoveryCompleted(l.cfg.Clock, audit.Common{Device: audit.Device{DeviceID: deviceKey}}, fingerprint)
		if err := l.cfg.Audit.Emit(ctx, event); err != nil {
			return errs.Wrap(err, "deliver discovery completed event")
		}
	}

	// A device added while the lane is fenced is fenced from its first
	// moment, and the audit stream has to say so — otherwise the only
	// devices a reader can see under the fence are the ones that happened
	// to be registered when it was called. Emitted before the device is
	// registered, so a failed delivery leaves no device behind claiming a
	// record that was never written; the caller retries AddDevice.
	if !l.freeze.AllowSideEffect() {
		l.freezeMu.Lock()
		event := audit.BuildLaneFrozen(l.cfg.Clock, audit.Common{Device: audit.Device{DeviceID: deviceKey}})
		emitErr := l.cfg.Audit.Emit(ctx, event)
		if emitErr == nil {
			l.frozenDelivered[deviceKey] = true
		}
		l.freezeMu.Unlock()
		if emitErr != nil {
			return errs.Wrap(emitErr, "deliver lane frozen event")
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

// probeIdentity acquires a read credential for deviceKey under the
// device's own access-policy handle, opens an SNMP session from it, runs
// the route-independent identity probe, and closes the session.
func (l *Lane) probeIdentity(ctx context.Context, deviceKey string, session DeviceSession) (string, error) {
	cred, _, err := l.acquireRead(ctx, deviceKey, session, session.AccessPolicy)
	if err != nil {
		return "", err
	}
	if session.OpenSNMP == nil {
		return "", errs.New().Msg("device has no SNMP session factory; the identity probe cannot run")
	}
	opened, err := session.OpenSNMP(ctx, cred)
	if err != nil {
		return "", errs.Wrap(err, "open snmp session for the identity probe")
	}
	defer closeSession(opened.Close)

	fingerprint, err := epoch.Probe(ctx, opened.Session)
	if err != nil {
		return "", errs.Wrap(err, "identity probe")
	}
	return fingerprint, nil
}

// acquireRead gets a fresh read credential for one operation. The handle is
// the caller's: a read passes the one TypedRead.access_policy carries,
// since central decides per read which policy admitted it, and onboarding
// passes the device's own.
func (l *Lane) acquireRead(ctx context.Context, deviceKey string, session DeviceSession, handle *policyv1.AccessPolicyHandle) (*edgev1.DeviceCredential, string, error) {
	response, err := l.cfg.ReadCredentials.AcquireReadCredential(ctx, deviceKey, session.BindingID, handle)
	if err != nil {
		return nil, "", errs.Wrap(err, "acquire read credential")
	}
	return response.GetCredential(), response.GetSshHostKeySha256(), nil
}

// closeSession runs a session's closer and discards its error. A device
// that answered the operation and then failed to hang up cleanly has not
// invalidated the answer, and reporting the close instead would replace a
// real result with a housekeeping failure.
func closeSession(closer func() error) {
	if closer != nil {
		_ = closer()
	}
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
			shared, err := ticket.Wait(ctx)
			if err != nil {
				return nil, err
			}
			execResult, _ := shared.(*integrationv1.ExecuteResult)
			// Every joiner gets its own copy carrying its own sequence, and
			// reports it. Central admitted each of these reads separately and
			// is owed an answer for each; that the edge served them with one
			// device call is the edge's business. Cloned rather than
			// relabelled in place, since the owner and every other joiner hold
			// the same message and a shared sequence field would be the last
			// writer's.
			if execResult != nil {
				joined, _ := proto.Clone(execResult).(*integrationv1.ExecuteResult)
				joined.SetSequence(opts.Request.GetSequence())
				l.report(ctx, joined)
				return joined, nil
			}
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
	// plain queue path but left open here). Detached is not unbounded,
	// though: every TypedRead routes through this path whether or not it
	// ever gets a joiner, so with no bound of its own a device that
	// accepts a connection but never answers would park this device's
	// drain goroutine, and this coalescing key, forever — Config's
	// OperationTimeout is a FLOOR on the detached work's lifetime, not a
	// ceiling: taking the caller's own deadline unconditionally would hand
	// a short-deadline owner's timeout to a longer-deadline joiner as if
	// it were the read's own outcome — exactly the bug this detachment
	// exists to prevent, one call site removed. A caller with a longer
	// deadline than OperationTimeout keeps it, since a joiner may be
	// depending on that longer budget; a caller with none, or a shorter
	// one, still gets at least OperationTimeout, since an unbounded or
	// very short caller must not be the reason a joiner's own longer wait
	// gets cut short either.
	timeout := l.cfg.OperationTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if until := time.Until(deadline); until > timeout {
			timeout = until
		}
	}
	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	sub := &submission{ctx: workCtx, request: opts.Request, result: make(chan submissionOutcome, 1)}
	if _, err := ds.queue.Submit(opts.Priority, sub); err != nil {
		cancel()
		l.coalescer.Finish(key, nil, err)
		return nil, err
	}

	l.drain(ds)

	// One goroutine, independent of any single caller's context, owns
	// finishing the ticket from the work's actual outcome — never from a
	// caller's own timeout — so every other joiner still waiting on
	// ticket.Wait sees the real result. It cancels workCtx once the work
	// is done, releasing the timer promptly rather than waiting out the
	// full OperationTimeout on every ordinary completion.
	done := make(chan submissionOutcome, 1)
	go func() {
		defer cancel()
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

// drain spawns a goroutine that becomes the device's sole active worker if
// none is already running, processing every currently- and
// newly-admitted item in Position order until the queue empties; if
// another call's goroutine is already draining, this one's goroutine loses
// the TryLock and exits immediately. Nothing guarantees that the item this
// call just admitted is reached by the drainer already running when this
// call was made — a racing exit can leave the queue non-empty right after
// this call's own goroutine gives up — which is why the loop below
// re-checks the queue length after unlocking and retries TryLock itself
// rather than trusting some other call's goroutine to pick the item up.
// Running on its own goroutine, independent of any admitting Submit call,
// is what lets Submit's own select wait only on its own context and result
// channel: the drainer never borrows one caller's deadline for another
// caller's still-queued work, and one caller's own item finishing does not
// wait on the drainer's subsequent work on a different caller's item.
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

// HandleTerminalAck applies central's TerminalResultAck to the mutation it
// addresses and returns what central should do about it. It is answered
// synchronously, on central's own goroutine: the acknowledgement is decided
// against the mutation's phase as it stands right now, and either takes
// effect before this call returns or is refused with nothing changed.
//
// It is not handed to the waiting mutation to apply later. A stored
// acknowledgement has to be re-validated whenever the phase moves and
// re-armed on every path out of a rest point, and central would be told
// "accepted" by a call that could not yet know whether it was.
//
// A refusal is central's to act on: no-pending-wait means this device holds
// no mutation at that sequence, already-terminal means the mutation ended
// and its own report is on the way, and out-of-order means the disposition
// does not fit the phase. An error from the acknowledgement's own audit
// delivery is returned too, with the decision already marked, so central
// re-sends and the identical acknowledgement completes the walk.
func (l *Lane) HandleTerminalAck(ctx context.Context, deviceKey string, ack *integrationv1.TerminalResultAck) error {
	ds, err := l.deviceRegardlessOfClosed(deviceKey)
	if err != nil {
		return err
	}

	// The sequence comparison is safe outside the machine's own lock: a
	// Machine's sequence is fixed at admission and never changes, so only
	// the identity of ds.current can race here, and that is read under
	// stateMu.
	ds.stateMu.Lock()
	m := ds.current
	ds.stateMu.Unlock()
	if m == nil || m.Sequence() != ack.GetSequence() {
		return errs.New().Code(ErrCodeNoPendingWait).
			Msgf("no mutation on device %s is open at sequence %d", deviceKey, ack.GetSequence())
	}

	if err := m.Acknowledge(ctx, ack); err != nil {
		return err
	}

	// An abandonment leaves the device's lane held: the mutation's effect
	// was never established, and only an explicit resolution admits another
	// one. Engaged after Acknowledge returns, since Acknowledge is what
	// decides whether the abandonment was accepted at all.
	if ack.GetDisposition() == accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED {
		ds.hold.Engage()
	}
	return nil
}

// errMutationEnded reports that a wait ended because central's
// acknowledgement turned the mutation terminal rather than because the step
// completed. It is never returned to a caller: process reads the machine
// and reports the terminal phase instead.
var errMutationEnded = errors.New("mutation ended before this step completed")

// awaitCheckpoint blocks until central's CheckpointRequest for seq arrives,
// the mutation ends, or ctx does. It selects on the machine's Done because
// a REJECTED acknowledgement is accepted at ADMITTED — which is exactly
// where this wait sits — and a mutation released here must never go on to
// execute.
func (ds *deviceState) awaitCheckpoint(ctx context.Context, m *mutation.Machine, seq uint64) (*integrationv1.CheckpointRequest, error) {
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
	case <-m.Done():
		return nil, errMutationEnded
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// process runs one admitted request through the mutation state machine's
// ordinary path: plan, checkpoint (mutation only), execute, observe,
// compare, and — for a verified mutation — acknowledge and release. An
// ambiguous or failed step (an execute error, a non-VERIFIED disposition, a
// conflicting read) is reported as this call's error rather than being
// automatically retried: automatic recovery is this module's
// internal/recovery package, exercised directly by its own tests; wiring
// its retry loop into this drain requires a real clock-driven poll only a
// host with a live transport can run (see README's onboarding section), so
// this package's own tests drive recovery explicitly rather than through
// Submit.
func (l *Lane) process(ctx context.Context, ds *deviceState, req *integrationv1.ExecuteRequest) (result *integrationv1.ExecuteResult, err error) {
	operationClass := "interface_read"
	if req.GetMutation() != nil {
		operationClass = "interface_description"
	}

	start := l.cfg.Clock()
	ctx, endSpan := l.cfg.Telemetry.StartOperation(ctx, operationClass)
	defer func() {
		endSpan(&err, classifyError)
		l.cfg.Telemetry.RecordOperationDuration(ctx, operationClass, l.cfg.Clock().Sub(start).Seconds(), classifyErrorOrEmpty(err))
	}()

	ds.stateMu.Lock()
	fingerprint := ds.fingerprint
	ds.stateMu.Unlock()

	deps := l.machineDeps(ds, fingerprint, req)
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		return nil, err
	}

	if !m.IsRead() {
		// Published before the first rest point, so an acknowledgement that
		// arrives while this mutation is parked at the very first wait finds
		// it. Cleared on the way out, whichever way it goes.
		ds.stateMu.Lock()
		ds.current = m
		ds.stateMu.Unlock()
		defer func() {
			ds.stateMu.Lock()
			ds.current = nil
			ds.stateMu.Unlock()
		}()

		l.report(ctx, m.Progress())

		checkpointReq, err := ds.awaitCheckpoint(ctx, m, req.GetSequence())
		if err != nil {
			return l.afterStep(ctx, ds, m, errs.Wrap(err, "await checkpoint"))
		}
		ack, err := m.Checkpoint(ctx, checkpointReq)
		if err != nil {
			return l.afterStep(ctx, ds, m, err)
		}
		l.reportCheckpoint(ctx, ack)

		if err := m.Execute(ctx); err != nil {
			return l.afterStep(ctx, ds, m, errs.Wrap(err, "execute"))
		}
	}

	obs, err := m.Observe(ctx)
	if err != nil {
		return l.afterStep(ctx, ds, m, errs.Wrap(err, "observe"))
	}
	l.recordEvidence(ds, fingerprint, req, obs)

	disposition, err := m.Compare(ctx, nil)
	if err != nil {
		return l.afterStep(ctx, ds, m, err)
	}

	if m.IsRead() {
		result := m.Result(nil)
		l.report(ctx, result)
		return result, nil
	}

	if disposition != accessv1.Disposition_DISPOSITION_VERIFIED {
		// The mutation may have reached the device but was never verified:
		// decision 5's ambiguity-stays-indeterminate rule means the lane
		// must not admit another mutation on top of an unresolved change.
		// Until an explicit resolution clears this device's hold, Submit
		// refuses every further mutation for it.
		return l.afterStep(ctx, ds, m, errs.New().Code(ErrCodeRecoveryAmbiguous).
			Msg("mutation was not verified; its effect on the device is unresolved"))
	}

	if err := m.MarkVerified(ctx); err != nil {
		return l.afterStep(ctx, ds, m, err)
	}

	// Reported before the wait, not after it: central decides what to
	// acknowledge from this report, so a report withheld until the
	// acknowledgement arrived would wait on something it has to cause.
	l.report(ctx, m.Result(nil))

	select {
	case <-m.Done():
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	result = m.Result(nil)
	l.report(ctx, result)
	return result, nil
}

// afterStep decides what a failed step means, since central's
// acknowledgement is applied on its own goroutine and can end a mutation
// while the step it was running is still parked.
//
// A terminal machine means the acknowledgement already ended this mutation:
// the step error is the wake-up, not a failure, so the terminal report goes
// out and no hold is engaged — central has already said what happened.
//
// Canceled but not terminal means an acknowledgement was accepted and its
// own audit delivery failed part-way through. The decision stands and
// central will re-send it, so this waits for the re-send under a context
// detached from the submitter's, whose deadline has nothing to do with how
// long central takes. Bounded, because a central that never re-sends must
// not park this device's drainer forever.
//
// Otherwise the error is what it says, and a mutation whose effect is now
// unknown holds the lane.
func (l *Lane) afterStep(ctx context.Context, ds *deviceState, m *mutation.Machine, err error) (*integrationv1.ExecuteResult, error) {
	if !m.IsRead() && !m.IsTerminal() && m.Canceled() {
		waitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), l.cfg.OperationTimeout)
		defer cancel()
		select {
		case <-m.Done():
		case <-waitCtx.Done():
		}
	}

	if m.IsTerminal() {
		result := m.Result(nil)
		l.report(ctx, result)
		return result, nil
	}

	if !m.IsRead() {
		ds.hold.Engage()
	}
	l.report(ctx, m.Result(err))
	return nil, err
}

// report hands one result to the host's Reporter, if it wired one.
func (l *Lane) report(ctx context.Context, result *integrationv1.ExecuteResult) {
	if l.cfg.Reporter == nil {
		return
	}
	l.cfg.Reporter.Reported(ctx, result)
}

func (l *Lane) reportCheckpoint(ctx context.Context, ack *integrationv1.CheckpointAck) {
	if l.cfg.Reporter == nil {
		return
	}
	l.cfg.Reporter.CheckpointAcked(ctx, ack)
}

// classifyError reduces err to the bounded, low-cardinality error.type
// string the observability convention requires: a mutation package error
// code when there is one, else "context.deadline_exceeded" or
// "context.canceled" for the two ctx errors, else "unknown" — never the raw
// error text, which is unbounded and may embed device-specific values.
func classifyError(err error) string {
	if code, ok := errs.CodeOf(err); ok {
		return string(code)
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "context.deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "context.canceled"
	default:
		return "unknown"
	}
}

// classifyErrorOrEmpty is [classifyError], but returns "" for a nil err —
// [telemetry.View.RecordOperationDuration] requires an empty error.type on
// a successful operation to keep its attribute set to at most two members.
func classifyErrorOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return classifyError(err)
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
			// Acquired before the override is consulted, so a test that
			// replaces the device call cannot also skip the acquisition.
			cred, hostKey, err := l.acquireRead(ctx, ds.key, sess, req.GetRead().GetAccessPolicy())
			if err != nil {
				return nil, err
			}
			if sess.ReadOverride != nil {
				return sess.ReadOverride(ctx, name)
			}
			if sess.OpenSNMP == nil {
				return nil, errs.New().Msg("device has no SNMP session factory; a read cannot be served")
			}
			opened, err := sess.OpenSNMP(ctx, cred)
			if err != nil {
				return nil, errs.Wrap(err, "open snmp session")
			}
			defer closeSession(opened.Close)

			// The shell is opened only for the fallback route, and only when
			// the device has one. interfaces.Read decides whether it needs
			// it; opening it unconditionally would mean an SSH login on every
			// SNMP read.
			var shell InterfaceShellAdapter
			if sess.OpenShell != nil {
				openedShell, err := sess.OpenShell(ctx, cred, hostKey)
				if err != nil {
					return nil, errs.Wrap(err, "open shell session")
				}
				defer closeSession(openedShell.Close)
				shell = openedShell.Adapter
			}

			// The fingerprint the lane probed wins over whatever a host put
			// in Prov: an observation's provenance must name the epoch this
			// lane actually observed under, not one a caller supplied.
			prov := sess.Prov
			prov.FirmwareFingerprint = fingerprint
			return interfaces.Read(ctx, opened.Session, shell, name, prov, nil, interfaces.Freshness{}, l.cfg.Clock())
		},
		Submit: func(ctx context.Context, grant *edgev1.SubmissionGrant, intent *accessv1.InterfaceDescriptionChange) error {
			if sess.SubmitOverride != nil {
				return sess.SubmitOverride(ctx, intent)
			}
			if sess.OpenShell == nil {
				return errs.New().Msg("device has no shell session factory; a mutation cannot be submitted")
			}
			// The command is sent over a session opened from the grant's own
			// material, pinned to the host key the grant names. The grant is
			// one-use and this is the use.
			opened, err := sess.OpenShell(ctx, grant.GetCredential(), grant.GetSshHostKeySha256())
			if err != nil {
				return errs.Wrap(err, "open shell session for the command")
			}
			defer closeSession(opened.Close)
			return opened.Adapter.SetPortName(ctx, intent.GetInterfaceName(), intent.GetDescription())
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
// decision 8's control-plane freeze, and records one LaneFrozen audit
// event per registered device. It does not return until every side effect
// already in flight has finished — or ctx ends first, in which case it
// carries ctx's error while leaving new side effects stopped regardless.
// See [freeze.Gate.Freeze]'s doc for why the wait is cancellable: a control
// plane fencing an edge precisely because it has gone unresponsive must not
// be able to hang on that same edge's own in-flight write.
//
// The gate is frozen before any record is emitted, and stays frozen
// whatever the emissions do. A fence is called when something is already
// wrong, quite often the audit path itself, and an audit outage must not be
// able to leave the devices unfenced — the same ordering the recovery hold
// uses, for the same reason.
//
// The returned error joins the gate's own and every failed delivery. A
// caller that retries gets only the records still missing: which devices
// were recorded is remembered, so a deliverer that failed for one device
// out of three does not produce three more records on the next attempt. The
// set is cleared by [Lane.Unfreeze], so the next fence records afresh.
func (l *Lane) Freeze(ctx context.Context) error {
	gateErr := l.freeze.Freeze(ctx)
	return errors.Join(gateErr, l.recordFrozen(ctx))
}

// recordFrozen emits the LaneFrozen record for every registered device that
// does not already have one, outside l.mu, since a Deliverer is a host's
// code and may do anything.
func (l *Lane) recordFrozen(ctx context.Context) error {
	l.freezeMu.Lock()
	defer l.freezeMu.Unlock()

	l.mu.Lock()
	keys := make([]string, 0, len(l.devices))
	for key := range l.devices {
		keys = append(keys, key)
	}
	l.mu.Unlock()
	// Sorted so a partial failure is reproducible: which devices got their
	// record before a deliverer failed should not depend on map iteration
	// order, or a retry would resume from somewhere different each time.
	slices.Sort(keys)

	var failures []error
	for _, key := range keys {
		if l.frozenDelivered[key] {
			continue
		}
		event := audit.BuildLaneFrozen(l.cfg.Clock, audit.Common{Device: audit.Device{DeviceID: key}})
		if err := l.cfg.Audit.Emit(ctx, event); err != nil {
			failures = append(failures, errs.Wrap(err, "deliver lane frozen event"))
			continue
		}
		l.frozenDelivered[key] = true
	}
	return errors.Join(failures...)
}

// Unfreeze resumes side effects and forgets which devices were recorded
// frozen, so the next Freeze records every device again rather than
// treating an old fence's records as covering a new one.
func (l *Lane) Unfreeze(ctx context.Context) {
	l.freezeMu.Lock()
	clear(l.frozenDelivered)
	l.freezeMu.Unlock()

	l.freeze.Unfreeze(ctx)
}

// ResolveHold clears deviceKey's recovery hold, admitting mutations again,
// and reports the acknowledgement central's HoldResolved is waiting for.
// The acknowledgement is reported only after the hold is actually cleared:
// central takes it as proof that this edge will accept the next mutation,
// and one sent ahead of the clear would be a promise about a lane still
// refusing work.
func (l *Lane) ResolveHold(ctx context.Context, deviceKey string, resolved *integrationv1.HoldResolved) error {
	ds, err := l.device(deviceKey)
	if err != nil {
		return err
	}
	ds.hold.Resolve()

	if l.cfg.Reporter != nil {
		ack := &integrationv1.HoldResolvedAck{}
		ack.SetSequence(resolved.GetSequence())
		l.cfg.Reporter.HoldResolvedAcked(ctx, ack)
	}
	return nil
}
