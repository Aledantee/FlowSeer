// Package deviceapi serves the operator-facing DeviceService: the typed read,
// the interface-description write, the status an operator polls, and the two
// calls that end a mutation nobody can finish.
//
// Every handler here writes through the journal, which is the only thing that
// assigns a sequence and the only thing that decides whether a write is
// allowed. This package's job is the part the journal cannot do: resolve the
// device against the registry, refuse an intent whose premises no longer hold
// before anything durable happens, and turn a record into the answer the
// schema describes.
package deviceapi

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

// Error codes the device handlers return.
var (
	// ErrCodeUnknownDevice is a call about a device the registry does not
	// list. Nothing can be dispatched for it, so it is refused at the door.
	ErrCodeUnknownDevice = errs.NewCode("deviceapi/unknown-device")
	// ErrCodePolicy is an intent admitted under a policy version the device
	// does not pin, or one the registry cannot resolve.
	ErrCodePolicy = errs.NewCode("deviceapi/policy")
	// ErrCodeFirmwareEpoch is an intent whose expected firmware fingerprint is
	// not the one the device last reported. The caller decided against a
	// device that has since changed underneath it.
	ErrCodeFirmwareEpoch = errs.NewCode("deviceapi/firmware-epoch")
	// ErrCodeLaneHeld is a second intent while a mutation still holds the
	// device's lane.
	ErrCodeLaneHeld = errs.NewCode("deviceapi/lane-held")
	// ErrCodeNoExpectation is a restore for an interface central holds no
	// expected description for: there is nothing to put back.
	ErrCodeNoExpectation = errs.NewCode("deviceapi/no-expectation")
	// ErrCodeRequest is a request the handler cannot act on as asked.
	ErrCodeRequest = errs.NewCode("deviceapi/request")
	// ErrCodeReadFailed is a read the edge answered with an error, or one
	// whose deadline passed before any answer arrived.
	ErrCodeReadFailed = errs.NewCode("deviceapi/read-failed")
	// ErrCodeReadTimeout is a read still open when the caller's deadline
	// passed. The read itself stays open and its answer lands in the record.
	ErrCodeReadTimeout = errs.NewCode("deviceapi/read-timeout")
)

// DeviceResolver answers the per-device facts that live in the registry rather
// than the lane record: which edge is responsible, the access policy the
// device pins, and whether it is listed at all. The registry implements it.
type DeviceResolver interface {
	Device(deviceID string) (*storev1.RegistryDevice, bool)
	Devices(ctx context.Context, edgeID string) ([]string, error)
	Horizon(ctx context.Context, deviceID string) (time.Duration, error)
	EdgeID() string
}

// RecordWatcher wakes a waiting read when the device's record changes. The
// report that closes a read may land on another central replica, so a waiter
// cannot poll its own memory.
type RecordWatcher interface {
	Watch(ctx context.Context, deviceID string) (<-chan struct{}, func(), error)
}

// Config wires the handlers to the journal, the registry, and the record
// watch a waiting read blocks on.
type Config struct {
	// Journal is the lane store every handler writes through.
	Journal *journal.Journal
	// Resolver answers the registry facts a device call needs.
	Resolver DeviceResolver
	// Watcher wakes a waiting read; without it a read polls at ReadPoll.
	Watcher RecordWatcher
	// ReadPoll is how often a waiting read re-reads the record when no watch
	// is wired, and the upper bound on how long it waits between wakeups when
	// one is. Zero uses a default.
	ReadPoll time.Duration
	// Clock is the time source for read deadlines; nil uses the wall clock.
	Clock func() time.Time
	// Logger records what a handler decided not to fail on; nil discards.
	Logger *slog.Logger
}

const defaultReadPoll = 500 * time.Millisecond

// Service implements the DeviceService handler. Safe for concurrent use.
type Service struct {
	cfg      Config
	clock    func() time.Time
	readPoll time.Duration
	log      *slog.Logger
}

// New builds the handler. It returns an error rather than defaulting a missing
// journal or resolver, because a device service without either would answer
// every call with an internal error at the first request instead of failing to
// start.
func New(cfg Config) (*Service, error) {
	if cfg.Journal == nil {
		return nil, errs.New().Code(ErrCodeRequest).Msg("device service needs a journal")
	}
	if cfg.Resolver == nil {
		return nil, errs.New().Code(ErrCodeRequest).Msg("device service needs a device resolver")
	}
	s := &Service{cfg: cfg, clock: cfg.Clock, readPoll: cfg.ReadPoll, log: cfg.Logger}
	if s.clock == nil {
		s.clock = time.Now
	}
	if s.readPoll <= 0 {
		s.readPoll = defaultReadPoll
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	return s, nil
}

// device resolves a device ref to its id and registry entry, refusing one the
// registry does not list.
func (s *Service) device(ref *inventoryv1.DeviceGlobalRef) (string, *storev1.RegistryDevice, error) {
	id := ref.GetDevice().GetId()
	entry, ok := s.cfg.Resolver.Device(id)
	if !ok {
		return "", nil, errs.New().Code(ErrCodeUnknownDevice).Attr("device", id).
			Msg("registry lists no such device")
	}
	return id, entry, nil
}

// pinnedPolicy refuses a handle that is not the version the device pins, so an
// operation never runs under a policy the device has moved off.
func pinnedPolicy(entry *storev1.RegistryDevice, handle *policyv1.AccessPolicyHandle, deviceID string) error {
	pinned := entry.GetConfig().GetAccessPolicy()
	if handle.GetKey() != pinned.GetKey() || handle.GetVersion() != pinned.GetVersion() {
		return errs.New().Code(ErrCodePolicy).Attr("device", deviceID).
			Attr("pinned", pinned.GetKey()).Attr("pinned_version", pinned.GetVersion()).
			Msg("intent names a policy version the device does not pin")
	}
	return nil
}

// currentEpoch refuses an intent whose expected fingerprint is not the one the
// device last reported. A record with no fingerprint has had no probe reported
// yet, and an unknown epoch is not a stale one: refusing there would make the
// first write on a fresh device impossible.
func currentEpoch(record *storev1.DeviceLaneRecord, expected, deviceID string) error {
	known := record.GetFirmwareFingerprint()
	if known == "" || known == expected {
		return nil
	}
	return errs.New().Code(ErrCodeFirmwareEpoch).Attr("device", deviceID).
		Msg("device reports a firmware epoch other than the one the intent expects")
}

// reconciliationIntent is the intent central admits on its own behalf to put
// an interface's expected description back. It carries the record's own
// fingerprint rather than a caller's: nobody is asserting what the device runs,
// central is acting on what it last saw.
func reconciliationIntent(device *inventoryv1.DeviceGlobalRef, entry *storev1.RegistryDevice, record *storev1.DeviceLaneRecord, iface, description string) *accessv1.MutationIntent {
	system := &accessv1.SystemActor{}
	system.SetReason(accessv1.SystemReason_SYSTEM_REASON_RECONCILIATION)
	actor := &accessv1.Actor{}
	actor.SetSystem(system)

	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName(iface)
	change.SetDescription(description)

	intent := &accessv1.MutationIntent{}
	intent.SetDevice(device)
	intent.SetIdempotencyKey(uuid.NewString())
	intent.SetActor(actor)
	intent.SetAccessPolicy(entry.GetConfig().GetAccessPolicy())
	intent.SetExpectedFirmwareFingerprint(record.GetFirmwareFingerprint())
	intent.SetInterfaceDescription(change)
	return intent
}

// changedInterface names the interface a mutation's change addresses, or "" if
// the state carries no intent — which is what a closed sequence leaves behind.
func changedInterface(m *accessv1.MutationState) string {
	return m.GetIntent().GetInterfaceDescription().GetInterfaceName()
}

// recordedKey reports whether the record already admitted this idempotency
// key, which makes the call a resubmission rather than a second intent.
func recordedKey(record *storev1.DeviceLaneRecord, key string) bool {
	for _, entry := range record.GetIdempotency() {
		if entry.GetIdempotencyKey() == key {
			return true
		}
	}
	return false
}

// edgeRef names the edge responsible for every device this registry lists.
func edgeRef(edgeID string) *edgev1.EdgeGlobalRef {
	local := &edgev1.EdgeLocalRef{}
	local.SetId(edgeID)
	ref := &edgev1.EdgeGlobalRef{}
	ref.SetEdge(local)
	return ref
}
