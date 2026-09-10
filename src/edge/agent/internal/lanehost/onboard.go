package lanehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
)

// ErrCodeOnboard identifies a device listing that could not be obtained or a
// device that could not be onboarded from one.
var ErrCodeOnboard = errs.NewCode("agent/onboard")

// Lister is the one EdgeService call the onboarder makes.
type Lister interface {
	ListDevices(context.Context, *connect.Request[edgev1.ListDevicesRequest]) (*connect.Response[edgev1.ListDevicesResponse], error)
}

// DeviceRegistrar is what a listed device is onboarded into. Satisfied by
// *access.Lane; an interface so the onboarding can be tested without devices.
type DeviceRegistrar interface {
	AddDevice(ctx context.Context, deviceKey string, session access.DeviceSession) error
}

// SNMPFactoryFor builds a device's SNMP session factory from where the
// device answers. [OpenSNMP] is the one production uses.
type SNMPFactoryFor func(Endpoint) func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error)

// ShellFactoryFor is SNMPFactoryFor's counterpart for the shell. [OpenShell]
// is the one production uses.
type ShellFactoryFor func(Endpoint) func(context.Context, *edgev1.DeviceCredential, string) (access.ShellSession, error)

// OnboardConfig declares the onboarder. Construct with keyed fields.
type OnboardConfig struct {
	Client Lister
	Lane   DeviceRegistrar
	// Edge is this edge's own ref, which every observation the lane makes
	// names as the edge that answered. The listing does not carry it: the
	// call is authorized as this edge, so central would only be telling the
	// edge who it already is.
	Edge *edgev1.EdgeGlobalRef
	// OpenSNMP and OpenShell build a device's session factories from where
	// it answers. Nil means [OpenSNMP] and [OpenShell].
	//
	// They are injectable because nothing can read an endpoint back out of
	// a DeviceSession — it is captured inside the closures — so a test that
	// checked only what the endpoint was computed to be would pass for an
	// agent that computed the right address and handed the dialers an empty
	// one. Substituting these puts the assertion on the endpoint the thing
	// that dials was actually built with.
	OpenSNMP  SNMPFactoryFor
	OpenShell ShellFactoryFor
	// PerDeviceTimeout bounds one device's onboarding. Zero means the
	// default.
	//
	// Bounded because onboarding is serial and the dispatch loop runs it
	// before every attempt to open the stream. Adding a device runs the
	// identity probe against it, and a device that is powered off does not
	// refuse — it says nothing, for the SNMP backend's whole retransmit
	// horizon. Unbounded, a handful of dead devices delays every
	// reconnection by minutes, with the heartbeat still succeeding and the
	// listing not having failed: from central the edge looks enrolled,
	// healthy, and permanently unsubscribed.
	PerDeviceTimeout time.Duration
	Logger           *slog.Logger
}

// Onboarder holds the devices this edge has been told to serve, and adds the
// ones it does not hold yet.
//
// Safe for concurrent use; one Sync runs at a time. Serializing them is not
// caution about the map — it is that two Syncs racing could both find a
// device absent and both add it, and a second AddDevice for a registered
// device replaces its whole lane state, orphaning its queue and any drainer
// working through it.
type Onboarder struct {
	cfg OnboardConfig
	log *slog.Logger

	syncing sync.Mutex

	mu sync.Mutex
	// held is the listing entry each onboarded device was built from,
	// keyed by device id. Guarded by mu, which is never held across
	// AddDevice.
	held map[string]*edgev1.ListedDevice
}

// NewOnboarder builds the onboarder. A nil logger discards.
func NewOnboarder(cfg OnboardConfig) (*Onboarder, error) {
	if cfg.Client == nil || cfg.Lane == nil {
		return nil, errs.New().Code(ErrCodeOnboard).Msg("onboarding needs an EdgeService client and a lane")
	}
	if cfg.Edge.GetEdge().GetId() == "" {
		return nil, errs.New().Code(ErrCodeOnboard).Msg("onboarding needs this edge's own ref for provenance")
	}
	if cfg.OpenSNMP == nil {
		cfg.OpenSNMP = OpenSNMP
	}
	if cfg.OpenShell == nil {
		cfg.OpenShell = OpenShell
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Onboarder{cfg: cfg, log: log, held: make(map[string]*edgev1.ListedDevice)}, nil
}

// Sync lists the devices central says this edge serves and onboards the ones
// it does not already hold. It is what the agent calls after attaching and
// before each attempt to open the dispatch stream.
//
// A device this edge already holds is left alone rather than re-added, for
// the reason on [Onboarder]. That means a device whose listing has changed —
// a horizon measured since, an address corrected — keeps the values it was
// first onboarded with. The divergence is recorded so the discrepancy is
// findable: without it, an operator who measures a horizon centrally sees
// mutations go on being refused with nothing anywhere connecting the two
// facts.
//
// One device failing to onboard does not stop the others, and it is not
// recorded as held, so the next Sync tries it again. That is the case of a
// device that is simply unreachable at this moment, which is not a reason to
// leave the rest of the edge's fleet unserved.
func (o *Onboarder) Sync(ctx context.Context) error {
	o.syncing.Lock()
	defer o.syncing.Unlock()

	resp, err := o.cfg.Client.ListDevices(ctx, connect.NewRequest(&edgev1.ListDevicesRequest{}))
	if err != nil {
		return errs.From(err).Code(ErrCodeOnboard).Msg("list the devices this edge serves")
	}

	for _, listed := range resp.Msg.GetDevices() {
		o.onboard(ctx, listed)
	}
	return nil
}

// defaultPerDeviceTimeout bounds one device's onboarding when the
// configuration names none. The identity probe is one SNMP exchange, and the
// backend's own horizon for a device that never answers is timeout times
// retries; this sits above a working probe on a slow WAN and well below the
// time a handful of dead devices would otherwise add to every reconnection.
const defaultPerDeviceTimeout = 30 * time.Second

func (o *Onboarder) perDeviceTimeout() time.Duration {
	if o.cfg.PerDeviceTimeout > 0 {
		return o.cfg.PerDeviceTimeout
	}
	return defaultPerDeviceTimeout
}

// errorType classifies a failure for the error.type attribute: the error's
// own code where it has one, and the two context causes by name where it does
// not. Bounded, because it becomes a metric dimension downstream.
func errorType(err error) string {
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

// onboard adds one listed device, or reports how a re-listing of a device
// already held differs from what it was onboarded with.
//
// Its own deadline, so one device cannot hold the rest. A device that runs
// out of time is not held and is onboarded on a later Sync, which is what
// happens to a device that fails outright.
func (o *Onboarder) onboard(ctx context.Context, listed *edgev1.ListedDevice) {
	deviceID := listed.GetDeviceId()

	o.mu.Lock()
	previous, held := o.held[deviceID]
	o.mu.Unlock()
	if held {
		if changed := divergedFields(previous, listed); len(changed) > 0 {
			o.log.WarnContext(ctx, "listed device differs from the one onboarded",
				slog.String("otel.event.name", "flowseer.edge.device.listing_diverged"),
				slog.String("flowseer.device.id", deviceID),
				slog.Any("flowseer.edge.device.diverged", changed))
		}
		return
	}

	session, err := o.deviceSession(listed)
	if err == nil {
		attempt, cancel := context.WithTimeout(ctx, o.perDeviceTimeout())
		err = o.cfg.Lane.AddDevice(attempt, deviceID, session)
		cancel()
	}
	if err != nil {
		// A warning, not an error: this device is not held and the next Sync
		// tries it again, which is a retry rather than an abandoned
		// operation. An edge with one device switched off would otherwise
		// report an error per reconnection for as long as it stays off.
		o.log.WarnContext(ctx, "listed device was not onboarded; it will be tried again",
			slog.String("otel.event.name", "flowseer.edge.device.onboarding_failed"),
			slog.String("flowseer.device.id", deviceID),
			slog.String("error.type", errorType(err)))
		return
	}

	o.mu.Lock()
	o.held[deviceID], _ = proto.Clone(listed).(*edgev1.ListedDevice)
	o.mu.Unlock()

	o.log.InfoContext(ctx, "device onboarded",
		slog.String("otel.event.name", "flowseer.edge.device.onboarded"),
		slog.String("flowseer.device.id", deviceID))
	if !listed.HasDelayedApplyHorizon() {
		// Said at onboarding rather than left for the first mutation to
		// discover. The lane refuses that mutation correctly and its error
		// names the device, but an operator reading it has no way to know
		// the edge has been holding the device in that state since it
		// started.
		o.log.WarnContext(ctx, "device onboarded without a measured delayed-apply horizon",
			slog.String("otel.event.name", "flowseer.edge.device.horizon_unmeasured"),
			slog.String("flowseer.device.id", deviceID))
	}
}

// deviceSession builds what the lane needs to reach one listed device.
func (o *Onboarder) deviceSession(listed *edgev1.ListedDevice) (access.DeviceSession, error) {
	endpoint, err := endpointFor(listed)
	if err != nil {
		return access.DeviceSession{}, err
	}
	binding := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(listed.GetBindingId())}.Build(),
	}.Build()

	return access.DeviceSession{
		OpenSNMP:      o.cfg.OpenSNMP(endpoint),
		OpenShell:     o.cfg.OpenShell(endpoint),
		AccessPolicy:  listed.GetAccessPolicy(),
		BindingID:     listed.GetBindingId(),
		Prov:          access.InterfaceProvenanceInputs{Binding: binding, Edge: o.cfg.Edge},
		DelayedEffect: access.InterfaceDelayedEffect{Horizon: listed.GetDelayedApplyHorizon().AsDuration()},
	}, nil
}

// endpointFor is where a listed device answers. An unset port stays zero
// here, so the default is applied by [Endpoint] at the moment a session is
// opened rather than being baked in twice.
func endpointFor(listed *edgev1.ListedDevice) (Endpoint, error) {
	address, err := addressOf(listed.GetIp())
	if err != nil {
		return Endpoint{}, err
	}
	return Endpoint{
		Address:  address,
		SNMPPort: int(listed.GetSnmpPort()),
		SSHPort:  int(listed.GetSshPort()),
	}, nil
}

// addressOf renders the listed management address. The octets are the wire's
// own, so a length that is not 4 or 16 is a message that passed validation
// nowhere; it is refused rather than dialed, since the alternative is a
// session opened against whatever a truncated address happens to mean.
func addressOf(ip *addrv1.IpAddress) (string, error) {
	var octets []byte
	switch {
	case ip.HasV4():
		octets = ip.GetV4().GetOctets()
	case ip.HasV6():
		octets = ip.GetV6().GetOctets()
	default:
		return "", errs.New().Code(ErrCodeOnboard).Msg("listed device carries no management address")
	}
	addr, ok := netip.AddrFromSlice(octets)
	if !ok {
		return "", errs.New().Code(ErrCodeOnboard).Attr("len", len(octets)).
			Msg("listed device's management address is not 4 or 16 octets")
	}
	return addr.String(), nil
}

// divergedFields names the fields on which a re-listing differs from what a
// device was onboarded with, each as "field: was → is". It names them rather
// than reporting that something changed: an operator told only that a listing
// differs is left to compare two things by hand, and the answer is usually
// one number.
func divergedFields(previous, current *edgev1.ListedDevice) []string {
	var changed []string
	compare := func(name string, was, is any) {
		if fmt.Sprint(was) != fmt.Sprint(is) {
			changed = append(changed, fmt.Sprintf("%s: %v → %v", name, was, is))
		}
	}

	compare("address", renderAddress(previous.GetIp()), renderAddress(current.GetIp()))
	compare("snmp_port", previous.GetSnmpPort(), current.GetSnmpPort())
	compare("ssh_port", previous.GetSshPort(), current.GetSshPort())
	compare("binding", previous.GetBindingId(), current.GetBindingId())
	compare("access_policy", handleOf(previous.GetAccessPolicy()), handleOf(current.GetAccessPolicy()))
	compare("delayed_apply_horizon", previous.GetDelayedApplyHorizon().AsDuration(), current.GetDelayedApplyHorizon().AsDuration())
	return changed
}

// renderAddress is addressOf for a divergence record, which has to say
// something about an address it cannot render. The empty string is what it
// must not say: an operator reading "address: 172.16.0.6 → " would take the
// device's address to have been removed rather than replaced with one this
// edge cannot use.
func renderAddress(ip *addrv1.IpAddress) string {
	address, err := addressOf(ip)
	if err != nil {
		return fmt.Sprintf("<unusable: %s>", errorType(err))
	}
	return address
}

func handleOf(handle interface {
	GetKey() string
	GetVersion() uint64
},
) string {
	return fmt.Sprintf("%s/%d", handle.GetKey(), handle.GetVersion())
}
