package lanehost

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"

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

// OnboardConfig declares the onboarder. Construct with keyed fields.
type OnboardConfig struct {
	Client Lister
	Lane   DeviceRegistrar
	// Edge is this edge's own ref, which every observation the lane makes
	// names as the edge that answered. The listing does not carry it: the
	// call is authorized as this edge, so central would only be telling the
	// edge who it already is.
	Edge   *edgev1.EdgeGlobalRef
	Logger *slog.Logger
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

// Devices names the devices this edge holds.
func (o *Onboarder) Devices() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	ids := make([]string, 0, len(o.held))
	for id := range o.held {
		ids = append(ids, id)
	}
	return ids
}

// onboard adds one listed device, or reports how a re-listing of a device
// already held differs from what it was onboarded with.
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

	session, err := deviceSession(listed, o.cfg.Edge)
	if err != nil {
		o.log.ErrorContext(ctx, "listed device cannot be onboarded",
			slog.String("otel.event.name", "flowseer.edge.device.onboarding_failed"),
			slog.String("flowseer.device.id", deviceID),
			slog.Any("error", err))
		return
	}
	if err := o.cfg.Lane.AddDevice(ctx, deviceID, session); err != nil {
		o.log.ErrorContext(ctx, "listed device cannot be onboarded",
			slog.String("otel.event.name", "flowseer.edge.device.onboarding_failed"),
			slog.String("flowseer.device.id", deviceID),
			slog.Any("error", err))
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
func deviceSession(listed *edgev1.ListedDevice, edge *edgev1.EdgeGlobalRef) (access.DeviceSession, error) {
	endpoint, err := endpointFor(listed)
	if err != nil {
		return access.DeviceSession{}, err
	}
	binding := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(listed.GetBindingId())}.Build(),
	}.Build()

	return access.DeviceSession{
		OpenSNMP:      OpenSNMP(endpoint),
		OpenShell:     OpenShell(endpoint),
		AccessPolicy:  listed.GetAccessPolicy(),
		BindingID:     listed.GetBindingId(),
		Prov:          access.InterfaceProvenanceInputs{Binding: binding, Edge: edge},
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

	previousAddress, _ := addressOf(previous.GetIp())
	currentAddress, _ := addressOf(current.GetIp())
	compare("address", previousAddress, currentAddress)
	compare("snmp_port", previous.GetSnmpPort(), current.GetSnmpPort())
	compare("ssh_port", previous.GetSshPort(), current.GetSshPort())
	compare("binding", previous.GetBindingId(), current.GetBindingId())
	compare("access_policy", handleOf(previous.GetAccessPolicy()), handleOf(current.GetAccessPolicy()))
	compare("delayed_apply_horizon", previous.GetDelayedApplyHorizon().AsDuration(), current.GetDelayedApplyHorizon().AsDuration())
	return changed
}

func handleOf(handle interface {
	GetKey() string
	GetVersion() uint64
},
) string {
	return fmt.Sprintf("%s/%d", handle.GetKey(), handle.GetVersion())
}
