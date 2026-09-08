package edgeapi

import (
	"context"
	"log/slog"
	"regexp"
	"slices"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	credentialv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/credential/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
	"go.aledante.io/FlowSeer/src/services/device/internal/registry"
)

// Error codes the edge handler returns.
var (
	// ErrCodeSetupKeyRefused is a setup key that does not enroll: unknown,
	// wrong, or already used to register a different key. One code covers all
	// three, because the caller is unauthenticated and telling them apart
	// would say which.
	ErrCodeSetupKeyRefused = errs.NewCode("edgeapi/setup-key-refused")
	// ErrCodeKeyProof is a proof of possession that does not verify or is not
	// bound to the registration it was presented for.
	ErrCodeKeyProof = errs.NewCode("edgeapi/key-proof")
	// ErrCodeForbidden is a call about a device the calling edge does not host.
	ErrCodeForbidden = errs.NewCode("edgeapi/forbidden")
	// ErrCodePolicy is a request naming a policy the device does not pin, or a
	// registry that cannot resolve one.
	ErrCodePolicy = errs.NewCode("edgeapi/policy")
	// ErrCodeBus is a failure to mint the edge's bus credential.
	ErrCodeBus = errs.NewCode("edgeapi/bus")
	// ErrCodeCredential is a failure to read the credential material a policy
	// resolves to.
	ErrCodeCredential = errs.NewCode("edgeapi/credential")
)

const setupKeyIDLen = 26

// setupKeyStringPattern is the schema's own rule for a setup key string. The
// handler applies it itself: Enroll carries no assertion, so its request is the
// first thing an unauthenticated caller controls.
var setupKeyStringPattern = regexp.MustCompile(`^fse1_[a-z2-7]{26}_[a-z2-7]{52}$`)

// BusMinter hands an edge the NATS identity its leaf node authenticates with.
// The hub implements it; the permission set on the minted user belongs to the
// hub and is never widened here.
type BusMinter interface {
	MintEdgeUser(ctx context.Context, edgeID string) (edgebus.EdgeCredentials, error)
	Tenant() string
}

// CredentialSource resolves a pinned credential version to its material.
type CredentialSource interface {
	Get(key string, version uint64) (*credentialv1.CredentialMaterial, error)
}

// ServiceConfig is the deployment configuration behind the edge-facing
// answers: what an edge is told to trust, and where it dials the bus.
type ServiceConfig struct {
	// Audience is the value an edge puts in every assertion, and the one the
	// verifier checks.
	Audience string
	// TrustAnchors replace the shipped set on every enrollment, so a
	// deployment can rotate its TLS chain without re-provisioning an edge.
	// At least one is required.
	TrustAnchors [][]byte
	// ClusterURLs are the endpoints an edge's leaf node dials, in preference
	// order. At least one is required.
	ClusterURLs []string
	// PulseInterval is how often an open submission stream restates the
	// edge's authority. Zero uses the default.
	PulseInterval time.Duration
}

const (
	defaultStaleAfter   = 3 * time.Minute
	defaultDormantAfter = time.Hour
)

// Service implements the EdgeService handler. Every call but Enroll runs behind
// [Middleware], which puts the verified assertion in the context. Safe for
// concurrent use.
type Service struct {
	store    *edgestore.Store
	registry *registry.Registry
	lanes    LaneRecords
	creds    CredentialSource
	bus      BusMinter
	cfg      ServiceConfig
	clock    func() time.Time
	log      *slog.Logger
}

// NewService constructs the edge handler. A nil clock uses the wall clock and a
// nil logger discards. It returns an error for a configuration that would leave
// an edge trusting anything or unable to reach the bus.
func NewService(store *edgestore.Store, reg *registry.Registry, lanes LaneRecords, creds CredentialSource, bus BusMinter, cfg ServiceConfig, clock func() time.Time, log *slog.Logger) (*Service, error) {
	if cfg.Audience == "" {
		return nil, errs.New().Code(ErrCodeConfig).Msg("edge service names no audience")
	}
	if len(cfg.TrustAnchors) == 0 {
		return nil, errs.New().Code(ErrCodeConfig).Msg("edge service carries no trust anchor")
	}
	if len(cfg.TrustAnchors) > maxTrustAnchors {
		return nil, errs.New().Code(ErrCodeConfig).Attr("count", len(cfg.TrustAnchors)).
			Msg("edge service carries more trust anchors than an edge accepts")
	}
	for i, anchor := range cfg.TrustAnchors {
		if len(anchor) != sha256Len {
			return nil, errs.New().Code(ErrCodeConfig).Attr("index", i).Attr("len", len(anchor)).
				Msg("trust anchor is not a sha-256 digest")
		}
	}
	if len(cfg.ClusterURLs) == 0 {
		return nil, errs.New().Code(ErrCodeConfig).Msg("edge service names no cluster url")
	}
	if clock == nil {
		clock = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: store, registry: reg, lanes: lanes, creds: creds, bus: bus, cfg: cfg, clock: clock, log: log}, nil
}

// Heartbeat records that the edge is alive, what version it is running, and
// how long it has been holding data it could not deliver, and returns central's
// clock, which a device booting with a dead battery uses for its assertion
// timestamps until its own clock is synchronized.
//
// A heartbeat that reports nothing buffered clears the stored timestamp. Each
// one describes the edge as it is now, so leaving the last one standing would
// show an operator a backlog that has already drained.
func (s *Service) Heartbeat(ctx context.Context, req *connect.Request[edgev1.HeartbeatRequest]) (*connect.Response[edgev1.HeartbeatResponse], error) {
	edgeID, err := EdgeIDFromContext(ctx)
	if err != nil {
		return nil, unauthenticated(err)
	}

	now := s.clock()
	if _, err := s.store.Mutate(ctx, edgeID, func(current *storev1.StoredEdge) (*storev1.StoredEdge, error) {
		if current == nil {
			return nil, notFound(edgeID)
		}
		state := current.GetRecord().GetState()
		state.SetLastSeenAt(timestamppb.New(now))
		state.SetAgentVersion(req.Msg.GetAgentVersion())
		if buffering := req.Msg.GetBufferingSince(); buffering != nil {
			state.SetBufferingSince(buffering)
		} else {
			state.ClearBufferingSince()
		}
		return current, nil
	}); err != nil {
		return nil, connectErr(err)
	}

	return connect.NewResponse(edgev1.HeartbeatResponse_builder{ServerTime: timestamppb.New(now)}.Build()), nil
}

// AttachBus hands the calling edge the NATS identity its embedded leaf node
// authenticates with, the subjects it publishes on, and the endpoints to dial.
// The user is minted by the hub with the hub's own permission set; this handler
// chooses nothing about what the edge may reach.
func (s *Service) AttachBus(ctx context.Context, _ *connect.Request[edgev1.AttachBusRequest]) (*connect.Response[edgev1.AttachBusResponse], error) {
	edgeID, err := EdgeIDFromContext(ctx)
	if err != nil {
		return nil, unauthenticated(err)
	}

	creds, err := s.bus.MintEdgeUser(ctx, edgeID)
	if err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodeBus).Attr("edge", edgeID).Msg("mint edge bus user"))
	}
	credsFile, err := creds.CredsFile()
	if err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodeBus).Attr("edge", edgeID).Msg("render edge bus credential"))
	}

	return connect.NewResponse(edgev1.AttachBusResponse_builder{
		AccountJwt:     []byte(creds.AccountJWT),
		UserCredential: credsFile,
		Subjects:       edgebus.EdgePublishSubjects(s.bus.Tenant(), edgeID),
		ClusterUrls:    s.cfg.ClusterURLs,
	}.Build()), nil
}

// ListDevices names the devices the calling edge serves and what it needs to
// reach each one: the management address and ports, the binding, the pinned
// access policy, and the measured delayed-apply horizon.
//
// It is a listing of its own rather than a rider on the credential calls
// because the two kinds of fact fail in opposite directions. A host key that
// changed fails closed — the pin refuses and the caller gets an error — while
// an address that changed fails open, against a different device that answers
// normally. A mutation acquires separately for its command and for the
// observation that verifies it, so an address riding those calls could send
// the command to one device and read the verification from another, with the
// lane reporting the mutation verified about a box it never touched.
//
// A device the registry lists without a measured horizon is listed all the
// same, with the field unset. The edge serves it and refuses a mutation on
// it, which is what central does with the same fact; omitting it from the
// listing would instead leave the edge unable to tell a device it must not
// mutate from one central has never heard of, and would stop its reads for a
// property only mutations depend on.
func (s *Service) ListDevices(ctx context.Context, _ *connect.Request[edgev1.ListDevicesRequest]) (*connect.Response[edgev1.ListDevicesResponse], error) {
	edgeID, err := EdgeIDFromContext(ctx)
	if err != nil {
		return nil, unauthenticated(err)
	}

	// The registry refuses an edge it does not describe rather than
	// answering with an empty list, and that refusal is passed on: an edge
	// told it hosts nothing would onboard nothing and wait forever, which
	// is what a deployment whose registry has not caught up with its second
	// edge looks like.
	ids, err := s.registry.Devices(ctx, edgeID)
	if err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodeForbidden).Attr("edge", edgeID).
			Msg("list the devices this edge hosts"))
	}
	slices.Sort(ids)

	listed := make([]*edgev1.ListedDevice, 0, len(ids))
	for _, id := range ids {
		device, ok := s.registry.Device(id)
		if !ok {
			continue
		}
		entry := edgev1.ListedDevice_builder{
			DeviceId:     proto.String(id),
			BindingId:    proto.String(device.GetBinding().GetBinding().GetId()),
			Ip:           device.GetIp(),
			AccessPolicy: device.GetConfig().GetAccessPolicy(),
		}
		if port := device.GetSnmpPort(); port != 0 {
			entry.SnmpPort = proto.Uint32(port)
		}
		if port := device.GetSshPort(); port != 0 {
			entry.SshPort = proto.Uint32(port)
		}
		if horizon := device.GetDelayedApplyHorizon(); horizon != nil {
			entry.DelayedApplyHorizon = horizon
		}
		listed = append(listed, entry.Build())
	}

	return connect.NewResponse(edgev1.ListDevicesResponse_builder{Devices: listed}.Build()), nil
}

// AcquireReadCredential delivers a read credential for one binding, under the
// access policy version the device pins. There is no standing lease: the
// response carries an expiry and the edge acquires a fresh credential for the
// next read.
func (s *Service) AcquireReadCredential(ctx context.Context, req *connect.Request[edgev1.AcquireReadCredentialRequest]) (*connect.Response[edgev1.AcquireReadCredentialResponse], error) {
	edgeID, err := EdgeIDFromContext(ctx)
	if err != nil {
		return nil, unauthenticated(err)
	}
	policy, err := s.resolveAccess(ctx, edgeID, req.Msg.GetDeviceId(), req.Msg.GetBindingId())
	if err != nil {
		return nil, connectErr(err)
	}
	if err := matchesPinnedPolicy(req.Msg.GetAccessPolicy(), policy.GetHandle(), req.Msg.GetDeviceId()); err != nil {
		return nil, connectErr(err)
	}

	handle := policy.GetReadCredential()
	material, err := s.creds.Get(handle.GetKey(), handle.GetVersion())
	if err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodeCredential).Attr("device", req.Msg.GetDeviceId()).
			Attr("credential_key", handle.GetKey()).Msg("read credential material"))
	}

	resp := edgev1.AcquireReadCredentialResponse_builder{
		Credential: edgev1.DeviceCredential_builder{
			Credential:    handle,
			TypedMaterial: material,
		}.Build(),
		HostTrust: policy.GetHostTrust(),
		ExpiresAt: timestamppb.New(s.clock().Add(s.registry.ReadCredentialTTL(policy))),
	}
	// The pin travels with a shell login and only with one: an SNMP credential
	// has no SSH host key to check, and the schema refuses a response that
	// carries a meaningless pin.
	if material.HasShell() {
		resp.SshHostKeySha256 = proto.String(policy.GetSshHostKeySha256())
	}
	return connect.NewResponse(resp.Build()), nil
}

// resolveAccess checks that the calling edge hosts the device and that the
// binding is the one the registry lists for it, then resolves the access policy
// version the device pins.
func (s *Service) resolveAccess(ctx context.Context, edgeID, deviceID, bindingID string) (*storev1.RegistryPolicy, error) {
	hosts, err := s.registry.Hosts(ctx, edgeID, deviceID)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodePolicy).Attr("edge", edgeID).Attr("device", deviceID).
			Msg("resolve edge-device binding")
	}
	if !hosts {
		return nil, errs.New().Code(ErrCodeForbidden).Attr("edge", edgeID).Attr("device", deviceID).
			Msg("edge does not host this device")
	}
	device, ok := s.registry.Device(deviceID)
	if !ok {
		return nil, errs.New().Code(ErrCodeForbidden).Attr("edge", edgeID).Attr("device", deviceID).
			Msg("edge does not host this device")
	}
	if got := device.GetBinding().GetBinding().GetId(); got != bindingID {
		return nil, errs.New().Code(ErrCodeForbidden).Attr("device", deviceID).Attr("binding", bindingID).
			Msg("binding does not reach this device")
	}

	pinned := device.GetConfig().GetAccessPolicy()
	policy, ok := s.registry.Policy(pinned.GetKey(), pinned.GetVersion())
	if !ok {
		return nil, errs.New().Code(ErrCodePolicy).Attr("device", deviceID).Attr("policy", pinned.GetKey()).
			Msg("registry lists no such access policy version")
	}
	return policy, nil
}

// matchesPinnedPolicy refuses a request admitted under a policy version other
// than the one the device pins, so an operation never runs under a version it
// was not admitted under.
func matchesPinnedPolicy(requested, pinned *policyv1.AccessPolicyHandle, deviceID string) error {
	if requested.GetKey() == pinned.GetKey() && requested.GetVersion() == pinned.GetVersion() {
		return nil
	}
	return errs.New().Code(ErrCodePolicy).Attr("device", deviceID).
		Attr("requested_version", requested.GetVersion()).Attr("pinned_version", pinned.GetVersion()).
		Msg("request does not name the access policy the device pins")
}
