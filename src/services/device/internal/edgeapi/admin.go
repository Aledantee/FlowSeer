package edgeapi

import (
	"context"
	"encoding/base64"
	"time"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
)

// Error codes the admin handler returns.
var (
	// ErrCodeConfig is a deployment configuration an admin service cannot be
	// built from.
	ErrCodeConfig = errs.NewCode("edgeapi/config")
	// ErrCodeRequest is a request that names no edge or carries an expiry
	// already past.
	ErrCodeRequest = errs.NewCode("edgeapi/request")
	// ErrCodeNotFound is an operation on an edge that has no record.
	ErrCodeNotFound = errs.NewCode("edgeapi/not-found")
	// ErrCodeLifecycle is an operation the edge's lifecycle does not allow.
	ErrCodeLifecycle = errs.NewCode("edgeapi/lifecycle")
	// ErrCodeSetupKey is an operation on a setup key that is not outstanding.
	ErrCodeSetupKey = errs.NewCode("edgeapi/setup-key")
	// ErrCodePageToken is a listing token that did not come from this service.
	ErrCodePageToken = errs.NewCode("edgeapi/page-token")
	// ErrCodeRandom is a failure to draw a setup key from the system's
	// cryptographic source. Nothing is written and no key is shown.
	ErrCodeRandom = errs.NewCode("edgeapi/random")
)

const (
	// defaultSetupKeyTTL is how long an unused setup key is accepted when the
	// operator names no expiry. A key ships inside a device and may sit in a
	// warehouse for months.
	defaultSetupKeyTTL = 180 * 24 * time.Hour
	// defaultPageSize and maxPageSize bound one ListEdges page; the schema
	// fixes the ceiling.
	defaultPageSize = 50
	maxPageSize     = 500
	// sha256Len and maxTrustAnchors are the schema's shape for the pin set an
	// edge is provisioned with.
	sha256Len       = 32
	maxTrustAnchors = 8
)

// Provisioning is the deployment half of what ships with an edge: where
// central is and which TLS keys the edge accepts on the way there. The setup
// key is minted per issue and is not part of it.
type Provisioning struct {
	// CentralURL is the base URL of this deployment's edge service.
	CentralURL string
	// TrustAnchors are the SHA-256 digests of the subject public key info the
	// edge pins, 32 bytes each. At least one is required: an edge that pins
	// nothing accepts any chain, and corporate TLS interception would then
	// read every assertion.
	TrustAnchors [][]byte
}

// AdminService implements the EdgeAdminService handler: the operator's side of
// an edge's life, from creation through provisioning to retirement. It never
// authenticates an edge, so it is served behind the operator authorization,
// not the assertion middleware. Safe for concurrent use.
type AdminService struct {
	store        *edgestore.Store
	provisioning Provisioning
	contact      Contact
	clock        func() time.Time
}

// NewAdminService constructs the admin handler over the edge store, the
// provisioning the deployment ships, and the contact derivation its reads
// report. A nil clock uses the wall clock. It returns an error when the
// provisioning would produce an edge that pins nothing or cannot find central.
func NewAdminService(store *edgestore.Store, provisioning Provisioning, contact Contact, clock func() time.Time) (*AdminService, error) {
	if provisioning.CentralURL == "" {
		return nil, errs.New().Code(ErrCodeConfig).Msg("provisioning names no central url")
	}
	if len(provisioning.TrustAnchors) == 0 {
		return nil, errs.New().Code(ErrCodeConfig).Msg("provisioning carries no trust anchor")
	}
	if len(provisioning.TrustAnchors) > maxTrustAnchors {
		return nil, errs.New().Code(ErrCodeConfig).Attr("count", len(provisioning.TrustAnchors)).
			Msg("provisioning carries more trust anchors than an edge accepts")
	}
	for i, anchor := range provisioning.TrustAnchors {
		if len(anchor) != sha256Len {
			return nil, errs.New().Code(ErrCodeConfig).Attr("index", i).Attr("len", len(anchor)).
				Msg("trust anchor is not a sha-256 digest")
		}
	}
	if clock == nil {
		clock = time.Now
	}
	return &AdminService{store: store, provisioning: provisioning, contact: contact, clock: clock}, nil
}

// CreateEdge creates a pending edge and issues its first setup key. The key
// string is in the response and nowhere else; central stores only its digest.
func (s *AdminService) CreateEdge(ctx context.Context, req *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeRandom).Msg("draw edge identifier")
	}
	edgeID := id.String()

	// The key is drawn once, outside the compare-and-set loop: a retry that
	// re-drew it would store a digest of a key the operator was never shown.
	key, err := s.mintSetupKey(req.Msg.GetSetupKeyExpiresAt())
	if err != nil {
		return nil, connectErr(err)
	}

	ref := edgeRef(edgeID)
	stored, err := s.store.Mutate(ctx, edgeID, func(current *storev1.StoredEdge) (*storev1.StoredEdge, error) {
		if current != nil {
			return nil, errs.New().Code(ErrCodeLifecycle).Attr("edge", edgeID).Msg("edge identifier already in use")
		}
		lifecycle := edgev1.EdgeLifecycle_EDGE_LIFECYCLE_PENDING
		return storev1.StoredEdge_builder{
			Record: edgev1.EdgeRecord_builder{
				Config: edgev1.EdgeConfig_builder{
					Ref:         ref,
					Name:        optionalString(req.Msg.GetName()),
					Description: optionalString(req.Msg.GetDescription()),
				}.Build(),
				State: edgev1.EdgeState_builder{
					Ref:       ref,
					Lifecycle: &lifecycle,
					SetupKey:  key.record,
				}.Build(),
			}.Build(),
			SetupKeyHash: key.hash,
		}.Build(), nil
	})
	if err != nil {
		return nil, connectErr(err)
	}
	if err := s.store.IndexSetupKey(ctx, key.record.GetId(), edgeID); err != nil {
		return nil, connectErr(err)
	}

	return connect.NewResponse(edgev1.CreateEdgeResponse_builder{
		Edge:         s.reported(stored),
		Provisioning: s.provisioningFor(key.str),
	}.Build()), nil
}

// IssueSetupKey issues a fresh setup key to a pending or retired edge,
// replacing any unused one and returning a retired edge to pending. The
// replaced key stops being accepted with the same write. An enrolled edge is
// refused: it holds a registered key pair, and replacing that is Rekey.
func (s *AdminService) IssueSetupKey(ctx context.Context, req *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
	edgeID, err := edgeIDOf(req.Msg.GetEdge())
	if err != nil {
		return nil, connectErr(err)
	}
	key, err := s.mintSetupKey(req.Msg.GetExpiresAt())
	if err != nil {
		return nil, connectErr(err)
	}

	var replaced string
	stored, err := s.store.Mutate(ctx, edgeID, func(current *storev1.StoredEdge) (*storev1.StoredEdge, error) {
		if current == nil {
			return nil, notFound(edgeID)
		}
		state := current.GetRecord().GetState()
		if state.GetLifecycle() == edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED {
			return nil, errs.New().Code(ErrCodeLifecycle).Attr("edge", edgeID).
				Msg("an enrolled edge takes no setup key; retire it first")
		}
		replaced = outstandingKeyID(state)
		state.SetLifecycle(edgev1.EdgeLifecycle_EDGE_LIFECYCLE_PENDING)
		state.SetSetupKey(key.record)
		current.SetSetupKeyHash(key.hash)
		return current, nil
	})
	if err != nil {
		return nil, connectErr(err)
	}
	if err := s.reindexSetupKey(ctx, edgeID, key.record.GetId(), replaced); err != nil {
		return nil, connectErr(err)
	}

	return connect.NewResponse(edgev1.IssueSetupKeyResponse_builder{
		Edge:         s.reported(stored),
		Provisioning: s.provisioningFor(key.str),
	}.Build()), nil
}

// RevokeSetupKey withdraws an edge's outstanding setup key. A key that was
// already consumed is refused, because an enrollment is undone by RetireEdge
// and not by revoking the key it used.
func (s *AdminService) RevokeSetupKey(ctx context.Context, req *connect.Request[edgev1.RevokeSetupKeyRequest]) (*connect.Response[edgev1.RevokeSetupKeyResponse], error) {
	edgeID, err := edgeIDOf(req.Msg.GetEdge())
	if err != nil {
		return nil, connectErr(err)
	}

	var revoked string
	stored, err := s.store.Mutate(ctx, edgeID, func(current *storev1.StoredEdge) (*storev1.StoredEdge, error) {
		if current == nil {
			return nil, notFound(edgeID)
		}
		key := current.GetRecord().GetState().GetSetupKey()
		if key.GetStatus() != edgev1.SetupKeyStatus_SETUP_KEY_STATUS_ISSUED {
			return nil, errs.New().Code(ErrCodeSetupKey).Attr("edge", edgeID).Attr("status", key.GetStatus().String()).
				Msg("edge has no outstanding setup key")
		}
		revoked = key.GetId()
		key.SetStatus(edgev1.SetupKeyStatus_SETUP_KEY_STATUS_REVOKED)
		current.ClearSetupKeyHash()
		return current, nil
	})
	if err != nil {
		return nil, connectErr(err)
	}
	if err := s.store.UnindexSetupKey(ctx, revoked); err != nil {
		return nil, connectErr(err)
	}

	return connect.NewResponse(edgev1.RevokeSetupKeyResponse_builder{Edge: s.reported(stored)}.Build()), nil
}

// RetireEdge ends an edge's standing: its assertions are refused from the next
// call on, and an outstanding setup key is withdrawn with the same write, so
// retirement leaves no secret in the world that still enrolls. Retiring an
// already retired edge changes nothing and succeeds.
//
// Retirement does not resolve device work the edge was carrying. A mutation
// the edge still holds stays open on its lane record until an operator abandons
// it through DeviceService, which is the call that ends work an edge will never
// come back for.
func (s *AdminService) RetireEdge(ctx context.Context, req *connect.Request[edgev1.RetireEdgeRequest]) (*connect.Response[edgev1.RetireEdgeResponse], error) {
	edgeID, err := edgeIDOf(req.Msg.GetEdge())
	if err != nil {
		return nil, connectErr(err)
	}

	var revoked string
	stored, err := s.store.Mutate(ctx, edgeID, func(current *storev1.StoredEdge) (*storev1.StoredEdge, error) {
		if current == nil {
			return nil, notFound(edgeID)
		}
		state := current.GetRecord().GetState()
		if state.GetLifecycle() == edgev1.EdgeLifecycle_EDGE_LIFECYCLE_RETIRED {
			return nil, edgestore.ErrSkip
		}
		revoked = outstandingKeyID(state)
		state.SetLifecycle(edgev1.EdgeLifecycle_EDGE_LIFECYCLE_RETIRED)
		if key := state.GetSetupKey(); key.GetStatus() == edgev1.SetupKeyStatus_SETUP_KEY_STATUS_ISSUED {
			key.SetStatus(edgev1.SetupKeyStatus_SETUP_KEY_STATUS_REVOKED)
		}
		current.ClearSetupKeyHash()
		return current, nil
	})
	if err != nil {
		return nil, connectErr(err)
	}

	if revoked != "" {
		if err := s.store.UnindexSetupKey(ctx, revoked); err != nil {
			return nil, connectErr(err)
		}
	}

	return connect.NewResponse(edgev1.RetireEdgeResponse_builder{Edge: s.reported(stored)}.Build()), nil
}

// GetEdge returns one edge's record.
func (s *AdminService) GetEdge(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
	edgeID, err := edgeIDOf(req.Msg.GetEdge())
	if err != nil {
		return nil, connectErr(err)
	}
	stored, _, err := s.store.Get(ctx, edgeID)
	if err != nil {
		return nil, connectErr(err)
	}
	if stored == nil {
		return nil, connectErr(notFound(edgeID))
	}
	return connect.NewResponse(edgev1.GetEdgeResponse_builder{Edge: s.reported(stored)}.Build()), nil
}

// ListEdges returns one page of edges in ascending identifier order. The token
// carries the last identifier of the page before it, so a page is unaffected by
// edges created or retired while the caller reads.
func (s *AdminService) ListEdges(ctx context.Context, req *connect.Request[edgev1.ListEdgesRequest]) (*connect.Response[edgev1.ListEdgesResponse], error) {
	size := int(req.Msg.GetPageSize())
	if size <= 0 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	after, err := decodePageToken(req.Msg.GetPageToken())
	if err != nil {
		return nil, connectErr(err)
	}

	keys, err := s.store.Keys(ctx)
	if err != nil {
		return nil, connectErr(err)
	}
	for len(keys) > 0 && keys[0] <= after {
		keys = keys[1:]
	}
	more := len(keys) > size
	if more {
		keys = keys[:size]
	}

	edges := make([]*edgev1.EdgeRecord, 0, len(keys))
	for _, key := range keys {
		stored, _, err := s.store.Get(ctx, key)
		if err != nil {
			return nil, connectErr(err)
		}
		if stored == nil {
			continue // listed and then gone; the page is a snapshot, not a lock
		}
		edges = append(edges, s.reported(stored))
	}

	resp := edgev1.ListEdgesResponse_builder{Edges: edges}
	if more && len(keys) > 0 {
		resp.NextPageToken = proto.String(encodePageToken(keys[len(keys)-1]))
	}
	return connect.NewResponse(resp.Build()), nil
}

// reported is one stored edge as the admin service returns it, with the
// contact its last heartbeat implies rather than the one the record was last
// written with.
func (s *AdminService) reported(stored *storev1.StoredEdge) *edgev1.EdgeRecord {
	record := stored.GetRecord()
	s.contact.Apply(record, s.clock())
	return record
}

// outstandingKeyID is the identifier of the key an edge can still enroll with,
// or an empty string when it has none. A consumed or already withdrawn key
// leaves no index entry to drop.
func outstandingKeyID(state *edgev1.EdgeState) string {
	key := state.GetSetupKey()
	if key.GetStatus() != edgev1.SetupKeyStatus_SETUP_KEY_STATUS_ISSUED {
		return ""
	}
	return key.GetId()
}

// reindexSetupKey points the issued key's identifier at the edge before
// dropping the replaced one's entry, so no window leaves both unusable. The
// replaced entry is safe to outlive its key: the enrollment that reaches an
// edge through it still fails the digest comparison.
func (s *AdminService) reindexSetupKey(ctx context.Context, edgeID, issued, replaced string) error {
	if err := s.store.IndexSetupKey(ctx, issued, edgeID); err != nil {
		return err
	}
	if replaced == "" || replaced == issued {
		return nil
	}
	return s.store.UnindexSetupKey(ctx, replaced)
}

// mintedSetupKey is one freshly drawn key in the three shapes it is needed in:
// the string the operator is shown once, the public record central stores, and
// the digest an enrollment is compared against.
type mintedSetupKey struct {
	str    string
	record *edgev1.SetupKey
	hash   []byte
}

func (s *AdminService) mintSetupKey(requested *timestamppb.Timestamp) (mintedSetupKey, error) {
	now := s.clock()
	expiresAt := now.Add(defaultSetupKeyTTL)
	if requested != nil {
		expiresAt = requested.AsTime()
		if !expiresAt.After(now) {
			return mintedSetupKey{}, errs.New().Code(ErrCodeRequest).Attr("expires_at", expiresAt).
				Msg("setup key expiry is not in the future")
		}
	}

	str, id, err := generateSetupKey()
	if err != nil {
		return mintedSetupKey{}, err
	}

	status := edgev1.SetupKeyStatus_SETUP_KEY_STATUS_ISSUED
	return mintedSetupKey{
		str: str,
		record: edgev1.SetupKey_builder{
			Id:        proto.String(id),
			Status:    &status,
			IssuedAt:  timestamppb.New(now),
			ExpiresAt: timestamppb.New(expiresAt),
		}.Build(),
		hash: hashSetupKey(str),
	}, nil
}

func (s *AdminService) provisioningFor(key string) *edgev1.EdgeProvisioning {
	return edgev1.EdgeProvisioning_builder{
		CentralUrl:   proto.String(s.provisioning.CentralURL),
		SetupKey:     proto.String(key),
		TrustAnchors: s.provisioning.TrustAnchors,
	}.Build()
}

func edgeRef(edgeID string) *edgev1.EdgeGlobalRef {
	return edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
	}.Build()
}

func edgeIDOf(ref *edgev1.EdgeGlobalRef) (string, error) {
	id := ref.GetEdge().GetId()
	if id == "" {
		return "", errs.New().Code(ErrCodeRequest).Msg("request names no edge")
	}
	return id, nil
}

func notFound(edgeID string) error {
	return errs.New().Code(ErrCodeNotFound).Attr("edge", edgeID).Msg("no such edge")
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func encodePageToken(lastID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(lastID))
}

func decodePageToken(token string) (string, error) {
	if token == "" {
		return "", nil
	}
	id, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", errs.From(err).Code(ErrCodePageToken).Msg("decode page token")
	}
	return string(id), nil
}
