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
	ErrCodeHorizonUnmeasured = errs.NewCode("access/horizon-unmeasured")
	ErrCodeNoAccessPolicy    = errs.NewCode("access/no-access-policy")
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
	// ErrCodeFirmwareEpoch reports that the device's firmware changed:
	// either the intent named an epoch the device is no longer running, or
	// it changed under an operation already in flight.
	ErrCodeFirmwareEpoch = mutation.ErrCodeFirmwareEpoch
)

// Reporter receives everything this lane owes its host to pass on to
// central: each phase a mutation reaches, each observation, and each
// acknowledgement of a message central sent. The lane calls it on the
// goroutine that did the work.
//
// Every method names the device. None of the three messages does — a
// sequence is unique per device and the execution envelope leaves
// addressing to the transport — while ReportRequest.device_id is required,
// so a Reporter without this parameter is handed a checkpoint
// acknowledgement it cannot address. The lane knows the device at every
// call site; the host would have to reconstruct it, and cannot.
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
	Reported(ctx context.Context, deviceKey string, result *integrationv1.ExecuteResult)
	// CheckpointAcked carries the acknowledgement of central's
	// CheckpointRequest.
	CheckpointAcked(ctx context.Context, deviceKey string, ack *integrationv1.CheckpointAck)
	// HoldResolvedAcked carries the acknowledgement of central's
	// HoldResolved.
	HoldResolvedAcked(ctx context.Context, deviceKey string, ack *integrationv1.HoldResolvedAck)
	// Onboarded carries the firmware fingerprint the identity probe learned
	// when this device was added, and with it the fact that the device was
	// added at all.
	//
	// It is the one thing this lane reports that central did not ask for.
	// Everything else answers a dispatch; this answers a start. Central has
	// no other way to learn that an edge came back and re-onboarded a device
	// — which is what tells it to re-send an open mutation — and no other
	// way to learn the epoch before the first read, which every mutation
	// intent has to name.
	Onboarded(ctx context.Context, deviceKey, fingerprint string)
}

// Config carries every dependency [Lane] needs. Construct with keyed
// fields and do not mutate afterward; [NewLane] copies it once and every
// [Lane] method thereafter reads its own copy, so a Config is safe to
// share for concurrent reads but not to write to concurrently with
// NewLane. QueueCapacity below 1 is silently raised to 1 rather than
// rejected.
type Config struct {
	QueueCapacity  int
	EvidencePolicy evidence.Policy
	RecoveryMinGap time.Duration
	// RecoveryPollInterval spaces one mutation's recovery polls. Zero is
	// raised to a default of 30s.
	RecoveryPollInterval time.Duration
	// Wait blocks for d or until ctx ends, reporting whether the full
	// interval elapsed. It exists so a test can drive the recovery loop
	// without spending the horizon in real time; production leaves it nil
	// and gets a timer.
	Wait                  func(ctx context.Context, d time.Duration) bool
	Fenced                recovery.Fenced
	ReadCredentials       credential.ReadCredentialSource
	Reporter              Reporter
	SubmissionCredentials credential.SubmissionCredentialSource
	Audit                 audit.Deliverer
	// Telemetry is this lane's instrumentation scope, built with
	// [NewTelemetry]. Nil is tolerated — every method guards it, and a
	// facade with no exporter is a legitimate way to use this module — and
	// it means the lane's spans, metrics and events do not exist. Nothing
	// reports that, because this module has no logger to report it with:
	// the View is its output. A host with providers passes one, and the
	// agent host always does.
	Telemetry *telemetry.View
	Clock     func() time.Time
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

	// DelayedEffect is how long a mutation on this device may take to
	// become visible to a fresh read, measured on this device's own
	// fixture. Required, and per device rather than per lane: one edge
	// serves devices whose horizons differ by orders of magnitude, and a
	// lane-wide value abandons the slow device at the fast device's
	// horizon — reporting a mutation as never applied while it is still
	// landing. Zero means unmeasured: the device is served, and
	// [Lane.Submit] refuses a mutation on it with [ErrCodeHorizonUnmeasured]
	// while a read, which has no effect to become visible, runs as usual.
	DelayedEffect InterfaceDelayedEffect

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
	// current is the mutation central's acknowledgements address: set when
	// one is admitted, cleared when it finishes. A read never sets it —
	// central acknowledges nothing about a read — so a mutation's ack is
	// never delivered to a read that happens to be running.
	current *openMutation
}

// openMutation is one admitted mutation and the caller still waiting on it.
// It exists because a mutation in recovery outlives the process call that
// admitted it: the drain loop returns, and the poll timer, or central's
// acknowledgement arriving on its own goroutine, is what eventually ends
// it. Whichever of them gets there first answers the caller.
type openMutation struct {
	machine *mutation.Machine
	sub     *submission

	// answered guards the one send on sub.result. Three parties can reach
	// it — the drain loop on the ordinary path, the poll timer, and the
	// acknowledgement path — and the channel holds exactly one buffered
	// send, so a second would either block forever or, worse, be dropped.
	answered sync.Once
	// stopPoll ends this mutation's recovery poll, and is nil until one
	// starts. Written and read under deviceState.stateMu, like polled: it
	// is assigned on the goroutine that entered recovery and read by
	// HandleTerminalAck and Close on theirs. Without that lock an
	// acknowledgement landing in the window between the poll starting and
	// the assignment becoming visible cancels nothing — bounded damage,
	// since the poll notices the terminal machine on its next tick, but a
	// real race the detector finds eventually as unexplained CI flakiness.
	stopPoll context.CancelFunc
	// polled says recovery has taken this mutation over, so the process
	// call that admitted it has returned and will answer nobody. It decides
	// which party ends the mutation when central's acknowledgement arrives:
	// while false, process is still running and blocked on Done, and it
	// answers its own caller once its operation telemetry is closed;
	// answering from the acknowledgement's goroutine instead would hand the
	// caller a result before the span and duration metric for it existed.
	// Written and read under deviceState.stateMu, the same lock that guards
	// current.
	polled bool
}

// endPoll stops the recovery poll if one is running. Safe to call more than
// once and on a mutation that never entered recovery. The caller must not
// hold ds.stateMu; endPoll takes it to read stopPoll.
func (o *openMutation) endPoll(ds *deviceState) {
	ds.stateMu.Lock()
	stop := o.stopPoll
	ds.stateMu.Unlock()
	if stop != nil {
		stop()
	}
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
// new admission and cancel every device's recovery poll, and each already-admitted Submit call independently
// returns on its own passed-in context regardless of how long the
// background drainer spends on other items (drain runs on its own
// goroutine; see [Lane.drain]'s doc comment). What it does not do: wait for
// in-flight operations up to a configured deadline, or report an operation
// whose audit delivery did not complete as undelivered rather than simply
// never returning to its own caller. A host that needs those guarantees
// must build them from Submit's per-call context today; ShutdownReport is
// reserved for that accounting once it exists.
type ShutdownReport struct{}

// Close stops the Lane from admitting new work and cancels every device's
// recovery poll. Already-admitted items continue to their terminal result
// on whichever goroutine is draining them; Close does not wait for them and
// enforces no deadline on them — see [ShutdownReport]'s doc for exactly
// what requirement 18 this does and does not satisfy.
//
// Canceling the polls is not optional tidying. A poll runs under a context
// detached from any caller's, for up to the horizon, and holds a reference
// to the device's drain lock; a Lane closed without canceling them leaves
// goroutines contending for the lock of a lane nobody is using, and a test
// that closes and rebuilds a Lane inherits the previous one's polls.
//
// A poll already holding the lock finishes the step it is running — it
// cannot be interrupted mid-device-call — and starts no further one: the
// check after acquiring the lock reads the closed flag, so a poll parked in
// Lock at the moment of Close releases without running anything. Each poll
// answers its own caller on the way out.
func (l *Lane) Close(_ context.Context) (ShutdownReport, error) {
	l.mu.Lock()
	l.closed = true
	devices := make([]*deviceState, 0, len(l.devices))
	for _, ds := range l.devices {
		devices = append(devices, ds)
	}
	l.mu.Unlock()

	for _, ds := range devices {
		ds.stateMu.Lock()
		open := ds.current
		ds.stateMu.Unlock()
		if open != nil {
			open.endPoll(ds)
		}
	}
	return ShutdownReport{}, nil
}

// defaultOperationTimeout bounds an admitted item's device call when
// Config.OperationTimeout is unset.
const defaultOperationTimeout = 30 * time.Second

// defaultRecoveryPollInterval spaces recovery polls when
// Config.RecoveryPollInterval is unset.
const defaultRecoveryPollInterval = 30 * time.Second

// waitFor is Config.Wait's default: a real timer.
func waitFor(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

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
	if cfg.RecoveryPollInterval <= 0 {
		cfg.RecoveryPollInterval = defaultRecoveryPollInterval
	}
	if cfg.Wait == nil {
		cfg.Wait = waitFor
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
	// Reported before the device is registered, and deliberately not gated on
	// the registration succeeding. The report says the probe reached this
	// device and found this epoch, which is true whether or not the steps
	// below complete; an AddDevice that fails after here is retried by its
	// caller and reports again, and the queue supersedes per device so the
	// second one replaces the first rather than accumulating.
	if l.cfg.Reporter != nil {
		l.cfg.Reporter.Onboarded(ctx, deviceKey, fingerprint)
	}
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

// reprobeEpoch runs the identity probe against a session opened for it and
// reports the device's firmware fingerprint now.
//
// Probe output against probe output, never probe output against a
// host-supplied provenance field: the two come from unrelated sources this
// module does not reconcile, and comparing them blocked every mutation the
// first time it was tried.
func (l *Lane) reprobeEpoch(ctx context.Context, ds *deviceState, session DeviceSession) (string, error) {
	return l.probeIdentity(ctx, ds.key, session)
}

// epochChanged refreshes the device's fingerprint, drops every piece of
// evidence learned under the old one, and records the change.
//
// The state write comes before the audit attempt, like the recovery hold's:
// a firmware change is a fact about the device, and an audit outage must not
// leave the lane working under an epoch it has already established is wrong.
// The returned error is the record's delivery, not the refresh.
func (l *Lane) epochChanged(ctx context.Context, ds *deviceState, previous, next string) error {
	ds.stateMu.Lock()
	ds.fingerprint = next
	ds.stateMu.Unlock()

	// Evidence says "this route answered this kind for this device under
	// this epoch". The epoch is part of that claim, so a change makes every
	// entry for the device unusable however long its lifetime has left.
	l.evid.InvalidateFingerprint(ds.key, next)

	l.cfg.Telemetry.FirmwareEpochChanged(ctx)
	event := audit.BuildFirmwareEpochChanged(l.cfg.Clock, audit.Common{Device: audit.Device{DeviceID: ds.key}}, previous, next)
	if err := l.cfg.Audit.Emit(ctx, event); err != nil {
		return errs.Wrap(err, "deliver firmware epoch changed event")
	}
	return nil
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

	// A resumed mutation's horizon runs from when central first admitted
	// it, not from now. Without that time there is no correct horizon to
	// give it, and the plausible fallback — the clock — is the specific bug
	// resume exists to avoid: an edge that crash-loops restarts the horizon
	// on every run and the mutation never abandons. Refused rather than
	// guessed, because guessing fails silently and only in the field.
	if opts.Request.GetResume() && opts.Request.GetAdmittedAt() == nil {
		return nil, errs.New().Msg("a resumed mutation must carry the admission time its horizon runs from")
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

	// A mutation on a device whose horizon was never measured is refused
	// here, before it takes a queue slot. The horizon is what bounds how
	// long the effect of a mutation may stay unknown, so without it there
	// is no moment at which recovery may correctly abandon — and every
	// substitute is a number nobody measured. It refuses the mutation and
	// not the device: a read has no effect to become visible, so the same
	// fact says nothing about reading this device, and central's registry
	// draws the line in the same place.
	if opts.Request.GetMutation() != nil && ds.session.DelayedEffect.Horizon <= 0 {
		return nil, errs.New().Code(ErrCodeHorizonUnmeasured).Attr("device", opts.DeviceKey).
			Msg("this device's delayed-apply horizon has never been measured; a mutation on it cannot be bounded")
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
				l.report(ctx, opts.DeviceKey, joined)
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
//
// The release rule, which belongs to ds.draining rather than to this
// function: any acquirer of that lock must, on release, drain the queue and
// re-check its length. It is not enough for this loop to do it, because a
// submitter whose TryLock loses exits at once and never retries — so an
// item admitted while some other acquirer held the lock would have no
// drainer at all until its caller gave up. The recovery poll timer is the
// second acquirer and meets the rule by calling drain after unlocking.
//
// State the rule per acquirer and unconditionally, never as "whoever holds
// it last". Two acquirers each draining is harmless: the loser of the
// TryLock exits immediately. Two acquirers each assuming the other will is
// the lost wakeup, and it only shows up under a timing nobody reproduces on
// purpose. A rule that requires each acquirer to know about the others is
// also the rule that breaks when a third one is added.
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
				//
				// process answers the submission itself rather than
				// returning an outcome to send here, because a mutation
				// that enters recovery has no outcome yet: process
				// returns, this loop moves on, and the poll timer or
				// central's acknowledgement answers the caller later.
				l.process(sub.ctx, ds, sub)
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
	open := ds.current
	ds.stateMu.Unlock()
	if open == nil || open.machine.Sequence() != ack.GetSequence() {
		return errs.New().Code(ErrCodeNoPendingWait).
			Msgf("no mutation on device %s is open at sequence %d", deviceKey, ack.GetSequence())
	}

	if err := open.machine.Acknowledge(ctx, ack); err != nil {
		return err
	}

	// An abandonment leaves the device's lane held: the mutation's effect
	// was never established, and only an explicit resolution admits another
	// one. Engaged after Acknowledge returns, since Acknowledge is what
	// decides whether the abandonment was accepted at all.
	if ack.GetDisposition() == accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED {
		ds.hold.Engage()
	}

	// A mutation in recovery has no process call left waiting on it: the
	// drain loop returned when it entered recovery, and this
	// acknowledgement is what ends it. Answer the caller here, and stop the
	// poll — it has nothing left to decide. answer is once-only, so doing
	// this on the ordinary path too, where process is still running and
	// will answer, costs nothing and needs no test of which path this is.
	// Only a mutation recovery has taken over is ended here. Otherwise the
	// process call that admitted it is parked on Done, which this
	// acknowledgement just closed, and it will answer its own caller.
	ds.stateMu.Lock()
	polled := open.polled
	ds.stateMu.Unlock()
	if polled && open.machine.IsTerminal() {
		open.endPoll(ds)
		l.endMutation(ctx, ds, open, open.machine.Result(nil), nil)
	}
	return nil
}

// clearCurrent releases ds.current, but only if it is still open: a
// mutation that already finished and was replaced by the next one must not
// have its successor cleared out from under it by a late caller.
func (ds *deviceState) clearCurrent(open *openMutation) {
	ds.stateMu.Lock()
	defer ds.stateMu.Unlock()
	if ds.current == open {
		ds.current = nil
	}
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

// process runs one admitted request through the mutation state machine:
// plan, checkpoint (mutation only), execute, observe, compare, and — for a
// verified mutation — the wait for central's acknowledgement and release.
//
// It answers sub itself rather than returning an outcome to the drain loop,
// because not every path has one: a mutation whose effect could not be
// established enters recovery, and this call returns having answered
// nothing, leaving the poll timer or central's acknowledgement to answer
// the caller later.
//
// The two defers are ordered deliberately. Go runs them last-registered
// first, so the telemetry defer below runs before the answering one: a
// caller holding this operation's result can rely on the span and the
// duration metric for it already existing. Answering first would let a test
// — or a host — observe a finished operation whose own signals had not been
// recorded yet, which is a race that reproduces about one run in ten and
// reads as a flaky exporter.
func (l *Lane) process(ctx context.Context, ds *deviceState, sub *submission) {
	req := sub.request
	operationClass := "interface_read"
	if req.GetMutation() != nil {
		operationClass = "interface_description"
	}

	var (
		result *integrationv1.ExecuteResult
		err    error
		open   *openMutation
		// owed is whether this call still owes its caller an answer. It
		// goes false exactly when recovery takes the mutation over.
		owed = true
	)

	start := l.cfg.Clock()
	ctx, endSpan := l.cfg.Telemetry.StartOperation(ctx, operationClass)

	defer func() {
		if !owed {
			return
		}
		if open != nil {
			l.endMutation(ctx, ds, open, result, err)
			return
		}
		sub.result <- submissionOutcome{result: result, err: err}
	}()
	defer func() {
		endSpan(&err, classifyError)
		l.cfg.Telemetry.RecordOperationDuration(ctx, operationClass, l.cfg.Clock().Sub(start).Seconds(), classifyErrorOrEmpty(err))
	}()

	// Re-checked here, not only at admission. Submit's own check happens
	// when the item is queued; the hold can be engaged by the item ahead of
	// this one in the same queue. Without this, two mutations admitted
	// before the first failed would both run — the second over a device
	// whose state the first left unknown, which is the one thing a hold
	// exists to prevent.
	if req.GetMutation() != nil && ds.hold.Active() {
		err = errs.New().Code(ErrCodeDesynchronized).
			Msg("this device's lane is on hold; call ResolveHold before admitting another mutation")
		return
	}

	ds.stateMu.Lock()
	fingerprint := ds.fingerprint
	ds.stateMu.Unlock()

	deps := l.machineDeps(ds, fingerprint, req)
	m, admitErr := mutation.Admitted(req, deps)
	if admitErr != nil {
		err = admitErr
		return
	}

	if m.IsRead() {
		result, err = l.processRead(ctx, ds, m, req, fingerprint)
		return
	}

	open = &openMutation{machine: m, sub: sub}
	ds.stateMu.Lock()
	ds.current = open
	ds.stateMu.Unlock()

	step := l.processMutation(ctx, ds, open, req, fingerprint)
	result, err, owed = step.result, step.err, step.owed
}

// processRead runs a read to its observation. A read never enters recovery
// and never waits on central, so it always has an outcome to return.
func (l *Lane) processRead(ctx context.Context, ds *deviceState, m *mutation.Machine, req *integrationv1.ExecuteRequest, fingerprint string) (*integrationv1.ExecuteResult, error) {
	obs, err := m.Observe(ctx)
	if err != nil {
		err = errs.Wrap(err, "observe")
		l.report(ctx, ds.key, m.Result(err))
		return nil, err
	}
	// A read is discarded across an epoch boundary for the same reason a
	// mutation's observation is: the device that answered is not the device
	// the read was planned against, so there is no honest fingerprint to
	// label the provenance with. A read has nothing to recover, so it fails
	// and its caller re-reads under the new epoch.
	if probed, probeErr := l.reprobeEpoch(ctx, ds, ds.session); probeErr == nil && probed != fingerprint {
		if err := l.epochChanged(ctx, ds, fingerprint, probed); err != nil {
			l.report(ctx, ds.key, m.Result(err))
			return nil, err
		}
		err := errs.New().Code(mutation.ErrCodeFirmwareEpoch).
			Attr("expected_fingerprint", fingerprint).
			Attr("current_fingerprint", probed).
			Msg("device firmware changed while the read was in flight; the observation is discarded")
		l.report(ctx, ds.key, m.Result(err))
		return nil, err
	}
	l.recordEvidence(ds, fingerprint, req, obs)

	if _, err := m.Compare(ctx, nil); err != nil {
		l.report(ctx, ds.key, m.Result(err))
		return nil, err
	}

	result := m.Result(nil)
	l.report(ctx, ds.key, result)
	return result, nil
}

// stepOutcome is what one phase path decided, and who owes the caller an
// answer for it. owed false means recovery has taken the mutation over and
// its poll, or central's acknowledgement, will answer instead. There is no
// third possibility, and there must not be: a path that neither answered
// nor handed off leaves Submit blocked with nothing left running that could
// unblock it.
type stepOutcome struct {
	result *integrationv1.ExecuteResult
	err    error
	owed   bool
}

// processMutation runs a mutation's phases.
func (l *Lane) processMutation(ctx context.Context, ds *deviceState, open *openMutation, req *integrationv1.ExecuteRequest, fingerprint string) stepOutcome {
	m := open.machine

	if req.GetResume() {
		// Central is re-dispatching a mutation whose checkpoint it already
		// holds confirmed. There is nothing to checkpoint again and nothing
		// to execute — the command may already have gone out on the run
		// that died — so this goes straight into recovery, where the only
		// question left is what the device actually holds.
		//
		// No baseline: the pre-mutation read belonged to a process that is
		// gone, and reading the device now would capture whatever state the
		// mutation may already have produced, which is the opposite of a
		// baseline. Recovery therefore cannot corroborate, so a resumed
		// mutation verifies or abandons; with no fence wired it cannot
		// retry, which the plan accepts.
		if err := m.Resume(ctx); err != nil {
			return stepOutcome{err: errs.Wrap(err, "resume"), owed: true}
		}
		l.report(ctx, ds.key, m.Progress())
		return l.enterRecovery(ctx, ds, open, req.GetAdmittedAt().AsTime(), nil,
			errs.New().Code(ErrCodeRecoveryAmbiguous).Msg("resumed after an edge restart; the mutation's effect is unknown"))
	}

	// The pre-mutation read, taken before the command goes out. It is the
	// baseline recovery corroborates against: two later observations that
	// both match it say the device still holds what it held before, so the
	// command did not land, and that is what authorizes a retry. A failed
	// baseline is not fatal — a device that cannot be read may still accept
	// the command, and refusing to try would make an unreadable device
	// unmanageable — but recovery then has nothing to corroborate against
	// and can only verify or abandon.
	baseline, baselineErr := m.Peek(ctx)
	if baselineErr != nil {
		// Swallowed on purpose, and safe only because Peek can fail for
		// device reasons alone: it reads through deps and checks no phase,
		// so an error here means the device would not answer, never that
		// the lane asked at the wrong moment. That is a property of Peek's
		// body two files away. Add a guard to Peek and this swallow starts
		// hiding a lane bug again — which is exactly how the baseline came
		// to be missing on every mutation while every test passed, since
		// routing it through Observe made it fail for a phase reason that
		// looks identical from here.
		//
		// A device that cannot be read may still accept the command, and
		// refusing to try would make an unreadable device unmanageable. But
		// recovery then has nothing to corroborate against and the mutation
		// can only verify or abandon, so the degradation goes on the span:
		// it is invisible in every other signal.
		l.cfg.Telemetry.NoteBaselineUnavailable(ctx, classifyError(baselineErr))
		baseline = nil
	}

	// Probed alongside the baseline, before anything is sent. An intent
	// central built against one firmware must not be applied to another:
	// the command's meaning is the firmware's, not central's.
	if probed, probeErr := l.reprobeEpoch(ctx, ds, ds.session); probeErr == nil && probed != fingerprint {
		return l.epochBlocked(ctx, ds, open, fingerprint, probed)
	}

	l.report(ctx, ds.key, m.Progress())

	checkpointReq, err := ds.awaitCheckpoint(ctx, m, req.GetSequence())
	if err != nil {
		return l.afterStep(ctx, ds, open, errs.Wrap(err, "await checkpoint"), baseline)
	}
	ack, err := m.Checkpoint(ctx, checkpointReq)
	if err != nil {
		return l.afterStep(ctx, ds, open, err, baseline)
	}
	l.reportCheckpoint(ctx, ds.key, ack)

	submittedAt := l.cfg.Clock()
	if err := m.Execute(ctx); err != nil {
		return l.afterStep(ctx, ds, open, errs.Wrap(err, "execute"), baseline)
	}

	obs, err := m.Observe(ctx)
	if err != nil {
		return l.enterRecovery(ctx, ds, open, submittedAt, baseline, errs.Wrap(err, "observe"))
	}

	// Probed again before the observation is trusted for anything. An
	// observation taken across an epoch boundary has no honest fingerprint
	// to label its provenance with — the device that answered is not the
	// device the read was planned against — so it is discarded rather than
	// recorded under either epoch.
	if probed, probeErr := l.reprobeEpoch(ctx, ds, ds.session); probeErr == nil && probed != fingerprint {
		if err := l.epochChanged(ctx, ds, fingerprint, probed); err != nil {
			return l.enterRecovery(ctx, ds, open, submittedAt, baseline, err)
		}
		return l.enterRecovery(ctx, ds, open, submittedAt, baseline,
			errs.New().Code(mutation.ErrCodeFirmwareEpoch).
				Msg("device firmware changed while the mutation was in flight; the observation is discarded"))
	}
	l.recordEvidence(ds, fingerprint, req, obs)

	disposition, err := m.Compare(ctx, nil)
	if err != nil {
		return l.afterStep(ctx, ds, open, err, baseline)
	}

	if disposition != accessv1.Disposition_DISPOSITION_VERIFIED {
		// The command went out and the device does not show it. Whether it
		// landed is unknown, which is recovery's whole subject.
		return l.enterRecovery(ctx, ds, open, submittedAt, baseline,
			errs.New().Code(ErrCodeRecoveryAmbiguous).Msg("mutation was not verified by its observation"))
	}

	if err := m.MarkVerified(ctx); err != nil {
		return l.afterStep(ctx, ds, open, err, baseline)
	}

	// Reported before the wait, not after it: central decides what to
	// acknowledge from this report, so a report withheld until the
	// acknowledgement arrived would wait on something it has to cause.
	l.report(ctx, ds.key, m.Result(nil))

	select {
	case <-m.Done():
	case <-ctx.Done():
		return stepOutcome{err: ctx.Err(), owed: true}
	}
	return stepOutcome{result: m.Result(nil), owed: true}
}

// epochBlocked handles a firmware change found before the command went
// out. Nothing was sent, so this is not ambiguity: the mutation is refused
// with submitted false, which is what tells central it may dispose it
// REJECTED, and the machine waits on Done for that acknowledgement rather
// than the lane deciding on central's behalf.
func (l *Lane) epochBlocked(ctx context.Context, ds *deviceState, open *openMutation, previous, next string) stepOutcome {
	m := open.machine
	if err := l.epochChanged(ctx, ds, previous, next); err != nil {
		return stepOutcome{err: err, owed: true}
	}
	if err := m.BlockFirmwareEpoch(ctx); err != nil {
		return stepOutcome{err: err, owed: true}
	}

	err := errs.New().Code(mutation.ErrCodeFirmwareEpoch).
		Attr("expected_fingerprint", previous).
		Attr("current_fingerprint", next).
		Msg("device firmware changed before the command was sent; the command is not sent")
	l.report(ctx, ds.key, m.Result(err))

	// The command provably never left, so there is nothing to recover and
	// nothing to hold. Central is told, and decides.
	select {
	case <-m.Done():
		return stepOutcome{result: m.Result(nil), owed: true}
	case <-ctx.Done():
		return stepOutcome{err: err, owed: true}
	}
}

// afterStep decides what a failed step means, since central's
// acknowledgement is applied on its own goroutine and can end a mutation
// while the step it was running is still parked.
//
// A terminal machine means the acknowledgement already ended this mutation:
// the step error is the wake-up, not a failure, so its own terminal result
// is the answer and no hold is engaged — central has already said what
// happened.
//
// Canceled but not terminal means an acknowledgement was accepted and its
// own audit delivery failed part-way through. The decision stands and
// central will re-send it, so this waits for the re-send under a context
// detached from the submitter's, whose deadline has nothing to do with how
// long central takes. Bounded, because a central that never re-sends must
// not park this device's drainer forever.
//
// An error after the command went out enters recovery rather than failing.
// The lane cannot say the mutation did not happen, and an error reported
// with submitted true is a claim central would have to guess about.
func (l *Lane) afterStep(ctx context.Context, ds *deviceState, open *openMutation, err error, baseline *accessv1.InterfaceObservation) stepOutcome {
	m := open.machine

	if !m.IsTerminal() && m.Canceled() {
		waitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), l.cfg.OperationTimeout)
		defer cancel()
		select {
		case <-m.Done():
		case <-waitCtx.Done():
		}
	}

	if m.IsTerminal() {
		return stepOutcome{result: m.Result(nil), owed: true}
	}

	if m.Submitted() {
		return l.enterRecovery(ctx, ds, open, l.cfg.Clock(), baseline, err)
	}

	// Provably nothing was sent. Central may dispose this REJECTED, which
	// is what submitted false on the report tells it.
	ds.hold.Engage()
	return stepOutcome{err: err, owed: true}
}

// enterRecovery moves a mutation whose effect is unknown into RECOVERING,
// engages the device's hold, and starts the poll that will end it. It
// answers nobody: the caller stays blocked in Submit until the poll or
// central's acknowledgement decides, which is what the false return says.
func (l *Lane) enterRecovery(ctx context.Context, ds *deviceState, open *openMutation, since time.Time, baseline *accessv1.InterfaceObservation, cause error) stepOutcome {
	m := open.machine
	ds.hold.Engage()

	if err := m.EnterRecovering(ctx); err != nil {
		// Recovery could not be entered, so no poll will run and nothing
		// else will answer this caller. Fail it here rather than leaving
		// it blocked on a loop that does not exist.
		return stepOutcome{err: errs.Wrap(err, "enter recovery"), owed: true}
	}

	l.report(ctx, ds.key, m.Progress())

	ds.stateMu.Lock()
	open.polled = true
	ds.stateMu.Unlock()

	l.startRecoveryPoll(ctx, ds, open, since, baseline)

	// cause is returned with owed false: process records it on this
	// operation's span and duration metric — why the mutation left the
	// synchronous path, which is the question an operator asks first — and
	// answers nobody, because the poll now owns that. It is deliberately
	// not in the report to central, which carries the phase and the
	// progress arm; central needs to know the mutation is recovering, and
	// the local reason a step failed is not something it can act on.
	return stepOutcome{err: cause}
}

// endMutation reports a mutation's outcome and answers its caller, exactly
// once however many parties reach it. The drain loop, the recovery poll and
// central's acknowledgement can all be the one that ends a mutation, and
// sub.result holds exactly one buffered send — a second would block that
// goroutine forever.
func (l *Lane) endMutation(ctx context.Context, ds *deviceState, open *openMutation, result *integrationv1.ExecuteResult, err error) {
	open.answered.Do(func() {
		// A hold engaged because a mutation's effect was unknown is
		// answered by learning what the effect was. Once this mutation
		// verified — whether the ordinary path or a recovery poll
		// established it — the ambiguity the hold exists for is gone, and
		// leaving it engaged would mean every recovery that succeeds still
		// needs an operator to unblock the device. An abandonment is the
		// case that keeps its hold, and it engages one of its own.
		//
		// Safe against a verified mutation that central then disposes
		// INDETERMINATE_ABANDONED, which Acknowledge accepts at every open
		// phase: by the time this runs, Acknowledge has already moved the
		// phase to ABANDONED and set the disposition, so Verified reads
		// false and the hold HandleTerminalAck just engaged survives. That
		// depends on Acknowledge finishing its terminal walk before
		// returning — if it ever marks and defers the phase change, this
		// check starts clearing a hold central asked for.
		if open.machine.Verified() {
			ds.hold.Resolve()
		}
		if err != nil {
			l.report(ctx, ds.key, open.machine.Result(err))
		} else if result != nil {
			l.report(ctx, ds.key, result)
		}
		open.sub.result <- submissionOutcome{result: result, err: err}
		ds.clearCurrent(open)
	})
}

// report hands one result to the host's Reporter, if it wired one.
func (l *Lane) report(ctx context.Context, deviceKey string, result *integrationv1.ExecuteResult) {
	if l.cfg.Reporter == nil {
		return
	}
	l.cfg.Reporter.Reported(ctx, deviceKey, result)
}

func (l *Lane) reportCheckpoint(ctx context.Context, deviceKey string, ack *integrationv1.CheckpointAck) {
	if l.cfg.Reporter == nil {
		return
	}
	l.cfg.Reporter.CheckpointAcked(ctx, deviceKey, ack)
}

// startRecoveryPoll runs one mutation's recovery on its own goroutine until
// the mutation ends or the poll's own budget runs out.
//
// The context is detached from the submitter's. A caller's deadline says
// how long it will wait for an answer, not how long the device's effect
// stays unknown, and a mutation whose submitter gave up still holds this
// device's lane until something resolves it. It is bounded all the same, by
// this device's own horizon plus one interval: the horizon is when Attempt
// abandons, and the extra interval leaves room for the poll that does the
// abandoning.
//
// Every exit answers the caller. That is the property to keep: a poll that
// returned without answering would leave Submit blocked with nothing left
// running that could ever unblock it, and nothing anywhere reporting that
// it had happened.
func (l *Lane) startRecoveryPoll(ctx context.Context, ds *deviceState, open *openMutation, since time.Time, baseline *accessv1.InterfaceObservation) {
	effect := ds.session.DelayedEffect
	budget := effect.Horizon + l.cfg.RecoveryPollInterval
	pollCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	ds.stateMu.Lock()
	open.stopPoll = cancel
	ds.stateMu.Unlock()

	runner := recovery.New(open.machine, l.cfg.Fenced, effect, l.cfg.RecoveryMinGap, l.cfg.Clock, &ds.hold)

	go func() {
		defer cancel()
		for {
			if !l.cfg.Wait(pollCtx, l.cfg.RecoveryPollInterval) {
				l.endRecoveryPoll(pollCtx, ds, open)
				return
			}
			if done := l.pollOnce(pollCtx, ds, open, runner, since, baseline); done {
				return
			}
		}
	}()
}

// pollOnce runs one recovery attempt under the device's drain lock and
// reports whether the poll is finished.
//
// The lock is taken with a blocking Lock rather than a TryLock: this poll
// is the device's work for the moment it runs, not an optional extra, and a
// TryLock that lost would silently skip a tick. It is held only for the
// attempt itself, so reads admitted meanwhile are served between polls
// rather than waiting out the horizon.
func (l *Lane) pollOnce(ctx context.Context, ds *deviceState, open *openMutation, runner *recovery.Runner, since time.Time, baseline *accessv1.InterfaceObservation) bool {
	ds.draining.Lock()

	// Checked after acquiring, before any step. A poll parked in Lock when
	// Close canceled it must release without running: by the time it wins
	// the lock the lane may be shutting down, and the check it made before
	// blocking says nothing about now.
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed || ctx.Err() != nil || open.machine.IsTerminal() {
		ds.draining.Unlock()
		l.drain(ds)
		l.endRecoveryPoll(ctx, ds, open)
		return true
	}

	if open.machine.Verified() {
		// Verified and waiting on central's acknowledgement. Attempt would
		// call Observe, which refuses from VERIFIED — a permanent refusal
		// this loop cannot tell from a transient step failure, so it would
		// go on reading the device every interval for the rest of the
		// budget and treating each refusal as retryable. Nothing here is
		// retryable: the answer is known and the acknowledgement is the
		// only thing outstanding.
		ds.draining.Unlock()
		l.drain(ds)
		return false
	}

	outcome, _, err := runner.Attempt(ctx, since, baseline)
	if err == nil && outcome == recovery.OutcomeRetry {
		// The retry runs under the same lock: it is the same device's
		// single ordered piece of work, and releasing between the decision
		// and the command would let a read interleave with a resubmission.
		err = l.retryUnderLock(ctx, open)
	}

	ds.draining.Unlock()
	// The release rule, stated on drain: an acquirer drains on release, and
	// never on the assumption that another acquirer will. A submitter whose
	// TryLock lost while this poll held the lock exited at once and will not
	// come back for its own item.
	l.drain(ds)

	if err != nil {
		// A failed step — a fence that errored, an abandonment whose audit
		// delivery failed — keeps the poll and retries the same step next
		// tick. It is not this mutation's outcome.
		return false
	}

	switch outcome {
	case recovery.OutcomeVerified:
		// Attempt has already marked it VERIFIED. Report, and wait for
		// central's acknowledgement to end it; the poll keeps running so
		// its budget stays the terminator if that never comes.
		l.report(ctx, ds.key, open.machine.Result(nil))
		return false
	case recovery.OutcomeAbandoned:
		l.endRecoveryPoll(ctx, ds, open)
		return true
	default:
		return false
	}
}

// retryUnderLock resends the command for a mutation recovery authorized a
// retry for. The caller holds ds.draining.
func (l *Lane) retryUnderLock(ctx context.Context, open *openMutation) error {
	m := open.machine
	if err := m.Retry(ctx); err != nil {
		return err
	}
	if err := m.Execute(ctx); err != nil {
		return errs.Wrap(err, "retry execute")
	}
	return nil
}

// endRecoveryPoll answers the caller with whatever the mutation ended as,
// and releases it. It is the poll's single exit, so no path out of the loop
// can forget to answer.
//
// A mutation that is not terminal here ran out of budget: the horizon plus
// one interval passed with no verification, no abandonment, and no
// acknowledgement. The caller is told so rather than left blocked — an
// answer nobody likes is still an answer, and the alternative is a Submit
// that never returns and a device whose hold nothing explains.
func (l *Lane) endRecoveryPoll(ctx context.Context, ds *deviceState, open *openMutation) {
	m := open.machine
	if m.IsTerminal() {
		l.endMutation(ctx, ds, open, m.Result(nil), nil)
		return
	}

	// VERIFIED is a resting place, not an unfinished one. A poll that
	// established the mutation applied and then ran out of budget waiting
	// for central's acknowledgement knows exactly what happened to the
	// device, and answering that caller "the effect could not be
	// established" would be the lane contradicting its own evidence — which
	// it has already reported to central. The two facts are different and
	// the caller needs this one: the effect is known, the acknowledgement
	// is not.
	if m.Phase() == accessv1.OperationPhase_OPERATION_PHASE_VERIFIED {
		l.endMutation(ctx, ds, open, m.Result(nil), nil)
		return
	}

	l.endMutation(ctx, ds, open, nil, errs.New().Code(ErrCodeRecoveryAmbiguous).
		Msg("recovery ended without establishing the mutation's effect; the device's lane is held"))
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

// readPolicyFor is the access-policy handle req's observation is taken under.
//
// It comes from the request's own operation arm, never from the device
// session. Central decides per operation which policy admitted it: a mutation
// is observed under the policy version its intent was admitted under, and a
// read under the one its TypedRead carries. DeviceSession.AccessPolicy is the
// onboarding probe's and would label an observation with the wrong version
// whenever a policy has moved since the device was onboarded.
//
// Exhaustive over the arms, and a handle it cannot source fails here rather
// than traveling. Every call between this point and central's own request
// validation accepts a nil handle, so central refuses it as a schema
// violation three layers from the code that had nothing to send — which is
// how the mutation arm stayed broken through every test on both sides. The
// next arm added hits this in its own package instead.
func readPolicyFor(req *integrationv1.ExecuteRequest) (*policyv1.AccessPolicyHandle, error) {
	var handle *policyv1.AccessPolicyHandle
	switch which := req.WhichOperation(); which {
	case integrationv1.ExecuteRequest_Mutation_case:
		handle = req.GetMutation().GetAccessPolicy()
	case integrationv1.ExecuteRequest_Read_case:
		handle = req.GetRead().GetAccessPolicy()
	default:
		return nil, errs.New().Code(ErrCodeNoAccessPolicy).Attr("operation", int(which)).
			Msg("this operation carries no arm an access policy handle can be read from")
	}
	if handle.GetKey() == "" {
		// Required on both arms, so an empty one means central sent a
		// request its own schema forbids. Refused here for the same reason
		// as an unknown arm: the alternative is a remote validation error
		// about a field this edge never filled in.
		return nil, errs.New().Code(ErrCodeNoAccessPolicy).Attr("operation", int(req.WhichOperation())).
			Msg("this operation's access policy handle is absent")
	}
	return handle, nil
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
			handle, err := readPolicyFor(req)
			if err != nil {
				return nil, err
			}
			// Acquired before the override is consulted, so a test that
			// replaces the device call cannot also skip the acquisition.
			cred, hostKey, err := l.acquireRead(ctx, ds.key, sess, handle)
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
// per-operation evidence.
//
// The error is discarded, and Record has exactly one failure: this
// operation kind has no configured evidence lifetime. That is a
// configuration fact, identical on every call for that kind, so reporting
// it would fail every operation of that kind over a cache nobody has to
// consult — evidence is an optimization for a later admission, never a gate
// this operation needs to pass.
//
// Revisit this when Consult gains a caller. A silently failed Record then
// becomes a reader that silently finds nothing and re-probes forever, which
// is the same shape as the baseline that never ran: a degraded path
// indistinguishable from the healthy one from outside.
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
		l.cfg.Reporter.HoldResolvedAcked(ctx, deviceKey, ack)
	}
	return nil
}
