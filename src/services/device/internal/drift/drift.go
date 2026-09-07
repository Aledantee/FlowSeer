// Package drift is central's detector for a managed interface that stopped
// matching what central expects of it.
//
// Detection is central's because both halves of the comparison are central's.
// The expectation is what a verified mutation left behind, and only central
// holds it; the management mode that decides what to do about a difference is
// the operator's configuration, not the edge's. An edge that decided for
// itself would be comparing against a value that goes stale the moment an
// operator accepts an observed state, and would then report every later read
// as drift.
//
// One pass per device does two things per managed interface: it judges what
// the last read brought back, and it asks for the next one. The read is an
// ordinary row on the device's lane, dispatched and answered like any other,
// so the poll writes no side channel and needs no second order.
package drift

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

// ErrCodeConfig is a poller that cannot be built from the configuration given.
var ErrCodeConfig = errs.NewCode("drift/config")

// DeviceResolver names the devices the poll covers and the per-device
// configuration it reads: which interfaces are polled, which policy a read is
// admitted under, and who owns the device's configuration.
type DeviceResolver interface {
	Devices(ctx context.Context, edgeID string) ([]string, error)
	Device(deviceID string) (*storev1.RegistryDevice, bool)
	EdgeID() string
}

// Audit records what central found. A detection that records nothing leaves
// the device diverged with nobody aware of it, so this is not optional in any
// deployment an operator relies on.
type Audit interface {
	DriftDetected(ctx context.Context, device *inventoryv1.DeviceGlobalRef, iface, expected, observed string) error
}

// Config wires the poller.
type Config struct {
	// Journal holds the expectations, the observations, and the reads.
	Journal *journal.Journal
	// Resolver names the devices and their managed interfaces.
	Resolver DeviceResolver
	// Audit records each detection.
	Audit Audit
	// Interval is how long between passes over every device. Zero uses a
	// default; the record's write cost is two writes per managed interface per
	// interval, so a deployment with many managed interfaces lengthens it.
	Interval time.Duration
	// ReadDeadline bounds one poll read. A read still open when it passes is
	// swept and recorded failed rather than owed forever. Zero uses the
	// interval, so a read has until the next pass to answer.
	ReadDeadline time.Duration
	// Clock is the time source; nil uses the wall clock.
	Clock func() time.Time
	// Logger records what one pass could not do; nil discards.
	Logger *slog.Logger
}

// DefaultInterval is the poll period the plan sizes the record's write budget
// against.
const DefaultInterval = 5 * time.Minute

// Poller compares each managed interface against its expectation and asks for
// the next read. Safe for concurrent use only in the sense that one Poller
// runs one pass at a time; Run holds it.
type Poller struct {
	cfg      Config
	clock    func() time.Time
	interval time.Duration
	deadline time.Duration
	log      *slog.Logger
}

// New builds the poller. A missing journal, resolver, or audit is refused
// rather than defaulted: a poll with no audit would detect drift and record
// nothing, which is the failure the detector exists to prevent.
func New(cfg Config) (*Poller, error) {
	switch {
	case cfg.Journal == nil:
		return nil, errs.New().Code(ErrCodeConfig).Msg("drift poll needs a journal")
	case cfg.Resolver == nil:
		return nil, errs.New().Code(ErrCodeConfig).Msg("drift poll needs a device resolver")
	case cfg.Audit == nil:
		return nil, errs.New().Code(ErrCodeConfig).Msg("drift poll needs somewhere to record what it finds")
	}

	p := &Poller{cfg: cfg, clock: cfg.Clock, interval: cfg.Interval, deadline: cfg.ReadDeadline, log: cfg.Logger}
	if p.clock == nil {
		p.clock = time.Now
	}
	if p.interval <= 0 {
		p.interval = DefaultInterval
	}
	if p.deadline <= 0 {
		p.deadline = p.interval
	}
	if p.log == nil {
		p.log = slog.New(slog.DiscardHandler)
	}
	return p, nil
}

// Run polls every interval until ctx ends. It returns nil on cancellation: a
// stopped poll is how the host shuts down, not a failure.
func (p *Poller) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.Pass(ctx)
		}
	}
}

// Pass runs one sweep over every device. It reports nothing: a device whose
// record cannot be read this pass is logged and left for the next one, because
// one unreachable record must not stop the others being polled.
func (p *Poller) Pass(ctx context.Context) {
	edgeID := p.cfg.Resolver.EdgeID()
	devices, err := p.cfg.Resolver.Devices(ctx, edgeID)
	if err != nil {
		p.log.ErrorContext(ctx, "drift poll could not name the devices to poll", "edge", edgeID, "error", err)
		return
	}
	for _, deviceID := range devices {
		if err := p.pollDevice(ctx, deviceID); err != nil {
			p.log.ErrorContext(ctx, "drift poll skipped a device", "device", deviceID, "error", err)
		}
	}
}

// pollDevice judges what the last reads brought back and asks for the next.
//
// A device whose lane holds a mutation is skipped whole. Central's own change
// is the obvious explanation for an interface not matching what central
// expected before it, and an abandoned mutation means nobody knows what the
// device carries — reporting either as drift would be central detecting
// itself. The block clears when the operator resolves, and the next pass
// judges the device then.
func (p *Poller) pollDevice(ctx context.Context, deviceID string) error {
	entry, ok := p.cfg.Resolver.Device(deviceID)
	if !ok {
		return nil // no longer listed; nothing to poll
	}
	managed := entry.GetManagedInterfaces()
	if len(managed) == 0 {
		return nil
	}

	record, err := p.cfg.Journal.Record(ctx, deviceID)
	if err != nil {
		return err
	}
	if record.HasMutation() {
		return nil
	}

	for _, iface := range managed {
		if err := p.judge(ctx, deviceID, entry, record, iface); err != nil {
			return err
		}
		// A judgement that admitted an intent has taken the lane, so the rest
		// of this device's interfaces wait for the next pass: one difference
		// at a time is what the lane's one-mutation rule already means.
		if admitted, err := p.reread(ctx, deviceID, entry, record, iface); err != nil {
			return err
		} else if admitted {
			return nil
		}
	}
	return nil
}

// judge compares what the interface last showed against what central expects.
// An interface with no expectation is not judged: central has applied nothing
// to it, so it has nothing to be different from, and adopting whatever the
// first read found would be central deciding an expectation nobody asked for.
func (p *Poller) judge(ctx context.Context, deviceID string, entry *storev1.RegistryDevice, record *storev1.DeviceLaneRecord, iface string) error {
	expected, managed := record.GetExpectedDescriptions()[iface]
	if !managed {
		return nil
	}
	observed, seen := record.GetLastObservations()[iface]
	if !seen || observed.GetCompleteness() != accessv1.Completeness_COMPLETENESS_COMPLETE {
		return nil
	}
	if observed.GetDescription() == expected {
		return nil
	}

	// The record goes out before the intent is admitted. An audit record with
	// no intent behind it is a detection an operator can still see and act on;
	// an intent with no record is a held lane with nothing saying why.
	if err := p.cfg.Audit.DriftDetected(ctx, deviceRef(deviceID, record), iface, expected, observed.GetDescription()); err != nil {
		return err
	}

	if record.GetFirmwareFingerprint() == "" {
		// Central admits this intent on its own behalf, so nobody else can
		// supply the epoch it is decided against, and an intent must name one
		// — the same rule ResolveDesynchronization's restore arm states, and
		// for the same reason. Admitting anyway writes an intent that fails
		// its own schema rules and, under AUTHORITATIVE, dispatches it.
		//
		// The detection above still stands: an operator can see it, and the
		// next read's report teaches central the epoch, after which the
		// following pass admits normally.
		p.log.WarnContext(ctx, "drift detected but not acted on",
			"device", deviceID, "interface", iface,
			"reason", "central has not learned this device's firmware epoch, so it cannot admit an intent of its own")
		return nil
	}
	return p.record(ctx, deviceID, entry, record, iface, expected)
}

// record admits central's response to the difference, which the device's
// management mode chooses. Under AUTHORITATIVE the intent is dispatched like
// any other and puts the expectation back; under OPERATOR_MANAGED it is held
// DESYNCHRONIZED, which takes the lane so nothing else is admitted behind it
// and waits for the operator to accept, restore, or replace it.
func (p *Poller) record(ctx context.Context, deviceID string, entry *storev1.RegistryDevice, record *storev1.DeviceLaneRecord, iface, expected string) error {
	intent := reconciliationIntent(record, entry, iface, expected)
	edge := edgeRef(p.cfg.Resolver.EdgeID())

	var err error
	if entry.GetConfig().GetManagementMode() == inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE {
		_, err = p.cfg.Journal.Admit(ctx, deviceID, intent, edge)
	} else {
		_, err = p.cfg.Journal.AdmitBlocked(ctx, deviceID, intent, edge,
			accessv1.BlockReason_BLOCK_REASON_DESYNCHRONIZED)
	}
	return err
}

// reread opens the next poll read, and reports whether the lane is now held —
// which it is when judge just admitted an intent, and a read must not be
// opened behind one, since the poll's next judgement would compare a fresh
// observation against an expectation central is already acting on.
func (p *Poller) reread(ctx context.Context, deviceID string, entry *storev1.RegistryDevice, record *storev1.DeviceLaneRecord, iface string) (bool, error) {
	current, err := p.cfg.Journal.Record(ctx, deviceID)
	if err != nil {
		return false, err
	}
	if current.HasMutation() {
		return true, nil
	}
	if open, ok := current.GetOpenReads()[iface]; ok && !open.HasOutcome() {
		return false, nil // last pass's read has not answered yet
	}

	read := &accessv1.TypedRead{}
	read.SetAccessPolicy(entry.GetConfig().GetAccessPolicy())
	intent := &accessv1.InterfaceReadIntent{}
	intent.SetInterfaceName(iface)
	read.SetInterface(intent)

	_, err = p.cfg.Journal.OpenRead(ctx, deviceID, deviceRef(deviceID, record), iface, read,
		uuid.NewString(), p.clock().Add(p.deadline))
	return false, err
}

// reconciliationIntent is what central admits on its own behalf to put an
// interface's expected description back. It carries the fingerprint central
// last learned, because nobody else is asserting what epoch this is decided
// against.
func reconciliationIntent(record *storev1.DeviceLaneRecord, entry *storev1.RegistryDevice, iface, expected string) *accessv1.MutationIntent {
	system := &accessv1.SystemActor{}
	system.SetReason(accessv1.SystemReason_SYSTEM_REASON_RECONCILIATION)
	actor := &accessv1.Actor{}
	actor.SetSystem(system)

	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName(iface)
	change.SetDescription(expected)

	intent := &accessv1.MutationIntent{}
	intent.SetDevice(record.GetDevice())
	intent.SetIdempotencyKey(uuid.NewString())
	intent.SetActor(actor)
	intent.SetAccessPolicy(entry.GetConfig().GetAccessPolicy())
	intent.SetExpectedFirmwareFingerprint(record.GetFirmwareFingerprint())
	intent.SetInterfaceDescription(change)
	return intent
}

// deviceRef names the device a read is opened for. The record supplies it once
// there is one; the first poll of a device central has never written to has no
// record to take it from, and the ref is required on the one this read
// creates.
func deviceRef(deviceID string, record *storev1.DeviceLaneRecord) *inventoryv1.DeviceGlobalRef {
	if ref := record.GetDevice(); ref.GetDevice().GetId() != "" {
		return ref
	}

	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(deviceID)
	ref := &inventoryv1.DeviceGlobalRef{}
	ref.SetDevice(local)
	return ref
}

func edgeRef(edgeID string) *edgev1.EdgeGlobalRef {
	local := &edgev1.EdgeLocalRef{}
	local.SetId(edgeID)
	ref := &edgev1.EdgeGlobalRef{}
	ref.SetEdge(local)
	return ref
}
