// Package projector projects central records into relationship tuples for
// operator authorization and reconciles drift periodically.
package projector

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

// EdgeSource provides edge mappings.
type EdgeSource interface {
	All(ctx context.Context) (map[string]string, error)
	TenantForEdge(ctx context.Context, edgeID string) (string, error)
}

// RegistrySource provides device registry queries.
type RegistrySource interface {
	EdgeID() string
	Device(deviceID string) (*storev1.RegistryDevice, bool)
	Devices(ctx context.Context, edgeID string) ([]string, error)
}

// CaptureSource provides capture session records.
type CaptureSource interface {
	EachSession(ctx context.Context, fn func(tenantID string, rec *modelcapturev1.CaptureSessionRecord) error) error
	Session(ctx context.Context, tenantID, sessionID string) (*modelcapturev1.CaptureSessionRecord, uint64, error)
}

// TenantSource provides committed tenant records.
type TenantSource interface {
	List(ctx context.Context) ([]*identityv1.TenantRecord, error)
}

// AccessSource provides the records from which tenant and role access derives.
type AccessSource interface {
	TenantIDs(ctx context.Context) ([]string, error)
	Members(ctx context.Context, tenantID string) ([]*identityv1.Member, error)
	Roles(ctx context.Context, tenantID string) ([]*identityv1.Role, error)
	Partners(ctx context.Context, tenantID string) ([]*identityv1.Partner, error)
	Member(ctx context.Context, tenantID string, operator *identityv1.OperatorRef) (*identityv1.Member, error)
}

// Option configures the additional relationship sources.
type Option func(*Projector)

// WithTenantSource supplies committed tenant records.
func WithTenantSource(source TenantSource) Option {
	return func(p *Projector) { p.tenants = source }
}

// WithAccessSource enables ownership of tenant, role, platform, and resource grants.
func WithAccessSource(source AccessSource) Option {
	return func(p *Projector) { p.access = source }
}

// WithPlatformPrincipals sets the principal IDs enrolled as platform admins.
func WithPlatformPrincipals(ids []string) Option {
	return func(p *Projector) {
		p.platformPrincipals = slices.Clone(ids)
		slices.Sort(p.platformPrincipals)
		p.platformPrincipals = slices.Compact(p.platformPrincipals)
	}
}

// WithClock supplies the clock used to evaluate full-payload grants.
func WithClock(now func() time.Time) Option {
	return func(p *Projector) {
		if now != nil {
			p.now = now
		}
	}
}

// Object identifies a central entity to synchronize into the relationship graph.
type Object struct {
	Type   string
	ID     string
	Tenant string
}

// Projector synchronizes central records into relationship tuples.
type Projector struct {
	relations          authz.Relations
	edges              EdgeSource
	registry           RegistrySource
	captures           CaptureSource
	tenants            TenantSource
	access             AccessSource
	platformPrincipals []string
	now                func() time.Time
	interval           time.Duration
	log                *slog.Logger
	reconciled         func()
	wait               func(ctx context.Context, d time.Duration) error
}

const (
	defaultInterval = 10 * time.Minute
	initialRetry    = 5 * time.Second
	maxSyncAttempts = 3
)

// New constructs a relationship projector.
func New(
	relations authz.Relations,
	edges EdgeSource,
	registry RegistrySource,
	captures CaptureSource,
	interval time.Duration,
	logger *slog.Logger,
	reconciled func(),
	opts ...Option,
) *Projector {
	if interval <= 0 {
		interval = defaultInterval
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	p := &Projector{
		relations:  relations,
		edges:      edges,
		registry:   registry,
		captures:   captures,
		interval:   interval,
		log:        logger,
		reconciled: reconciled,
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Sync synchronizes relationship tuples for a single object.
func (p *Projector) Sync(ctx context.Context, obj Object) error {
	_, err := p.syncObject(ctx, obj)
	return err
}

func (p *Projector) syncObject(ctx context.Context, obj Object) (bool, error) {
	return p.syncObjectWithStored(ctx, obj, nil, false)
}

func (p *Projector) syncObjectWithStored(ctx context.Context, obj Object, stored []authz.Tuple, hasStored bool) (bool, error) {
	for attempt := 0; attempt < maxSyncAttempts; attempt++ {
		repaired, err := p.attemptSync(ctx, obj, stored, hasStored)
		hasStored = false
		if err != nil {
			if code, ok := errs.CodeOf(err); ok && code == authz.ErrCodeConflict {
				continue
			}
			return false, err
		}
		return repaired, nil
	}
	return false, errs.New().Code(authz.ErrCodeConflict).Attr("type", obj.Type).Attr("id", obj.ID).Msg("relationship write did not settle")
}

func (p *Projector) attemptSync(ctx context.Context, obj Object, storedTuples []authz.Tuple, hasStored bool) (bool, error) {
	objectKey := obj.Type + ":" + obj.ID
	if !hasStored {
		var err error
		storedTuples, err = p.relations.Read(ctx, objectKey)
		if err != nil {
			return false, err
		}
	}

	existingOwned := p.filterOwnedTuples(obj.Type, storedTuples)

	desired, err := p.desiredTuples(ctx, obj, existingOwned)
	if err != nil {
		return false, err
	}

	writes, deletes := diffTuples(existingOwned, desired)
	if len(writes) == 0 && len(deletes) == 0 {
		return false, nil
	}

	if err := p.relations.Write(ctx, writes, deletes); err != nil {
		return false, err
	}
	return true, nil
}

func (p *Projector) desiredTuples(ctx context.Context, obj Object, existingOwned []authz.Tuple) ([]authz.Tuple, error) {
	if obj.Type == "capture_session" && obj.Tenant == "" {
		for _, tuple := range existingOwned {
			if tuple.Relation == "tenant" && strings.HasPrefix(tuple.User, "tenant:") {
				obj.Tenant = strings.TrimPrefix(tuple.User, "tenant:")
				break
			}
		}
	}
	// Engine object identifiers need not satisfy the stores' record rules.
	if obj.Tenant != "" && tenant.Validate(obj.Tenant) != nil {
		return nil, nil
	}
	switch obj.Type {
	case "tenant":
		if tenant.Validate(obj.ID) != nil {
			return nil, nil
		}
	case "edge", "device", "capture_session", "role":
		if len(obj.ID) != 36 || uuid.Validate(obj.ID) != nil {
			return nil, nil
		}
	case "platform":
		if obj.ID != "flowseer" {
			return nil, nil
		}
	default:
		return nil, nil
	}

	objectKey := obj.Type + ":" + obj.ID
	switch obj.Type {
	case "edge":
		tenantID, err := p.edges.TenantForEdge(ctx, obj.ID)
		if err != nil {
			return nil, err
		}
		if tenantID == "" {
			return nil, nil
		}
		return []authz.Tuple{
			{Object: objectKey, Relation: "tenant", User: "tenant:" + tenantID},
		}, nil

	case "device":
		if p.registry == nil {
			return nil, nil
		}
		_, ok := p.registry.Device(obj.ID)
		if !ok {
			return nil, nil
		}
		edgeID := p.registry.EdgeID()
		if edgeID == "" {
			return nil, nil
		}
		tenantID, err := p.edges.TenantForEdge(ctx, edgeID)
		if err != nil {
			return nil, err
		}
		if tenantID == "" {
			return nil, nil
		}
		return []authz.Tuple{
			{Object: objectKey, Relation: "tenant", User: "tenant:" + tenantID},
		}, nil

	case "capture_session":
		tenantID := obj.Tenant
		if tenantID == "" {
			return nil, nil
		}
		rec, _, err := p.captures.Session(ctx, tenantID, obj.ID)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, nil
		}
		var desired []authz.Tuple
		desired = append(desired, authz.Tuple{
			Object:   objectKey,
			Relation: "tenant",
			User:     "tenant:" + tenantID,
		})
		if edgeID := rec.GetConfig().GetRef().GetEdge().GetEdge().GetId(); edgeID != "" {
			desired = append(desired, authz.Tuple{
				Object:   objectKey,
				Relation: "edge",
				User:     "edge:" + edgeID,
			})
		}
		reqBy := rec.GetConfig().GetAuthorization().GetRequestedBy()
		if reqBy != nil && reqBy.GetIssuer() != "" {
			if p.access != nil {
				member, err := p.access.Member(ctx, tenantID, reqBy)
				if err != nil {
					return nil, err
				}
				if member == nil {
					return desired, nil
				}
			}
			principalID := authn.ComputePrincipalID(reqBy.GetIssuer(), reqBy.GetSubject())
			desired = append(desired, authz.Tuple{
				Object:   objectKey,
				Relation: "requester",
				User:     "user:" + principalID,
			})
		}
		return desired, nil

	case "tenant":
		if p.access == nil {
			return nil, nil
		}
		committed, err := p.committedTenant(ctx, obj.ID)
		if err != nil {
			return nil, err
		}
		members, err := p.access.Members(ctx, obj.ID)
		if err != nil {
			return nil, err
		}
		roles, err := p.access.Roles(ctx, obj.ID)
		if err != nil {
			return nil, err
		}
		partners, err := p.access.Partners(ctx, obj.ID)
		if err != nil {
			return nil, err
		}
		return tenantTuples(obj.ID, committed, members, roles, partners, p.now()), nil

	case "role":
		if p.access == nil {
			return nil, nil
		}
		tenantID := obj.Tenant
		var roles []*identityv1.Role
		if tenantID != "" {
			var err error
			roles, err = p.access.Roles(ctx, tenantID)
			if err != nil {
				return nil, err
			}
		}
		if tenantID == "" || !slices.ContainsFunc(roles, func(role *identityv1.Role) bool {
			return role.GetRef().GetRole().GetId() == obj.ID
		}) {
			tenantID = ""
			ids, err := p.access.TenantIDs(ctx)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				roles, err = p.access.Roles(ctx, id)
				if err != nil {
					return nil, err
				}
				for _, role := range roles {
					if role.GetRef().GetRole().GetId() == obj.ID {
						tenantID = id
						break
					}
				}
				if tenantID != "" {
					break
				}
			}
		}
		if tenantID == "" {
			return nil, nil
		}
		members, err := p.access.Members(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		return roleTuples(obj.ID, roles, members), nil

	case "platform":
		if p.access == nil {
			return nil, nil
		}
		var desired []authz.Tuple
		for _, id := range p.platformPrincipals {
			desired = append(desired, authz.Tuple{Object: objectKey, Relation: "enrolled", User: "user:" + id})
		}
		return desired, nil

	default:
		return nil, nil
	}
}

func (p *Projector) committedTenant(ctx context.Context, tenantID string) (bool, error) {
	if p.tenants == nil {
		return false, nil
	}
	records, err := p.tenants.List(ctx)
	if err != nil {
		return false, err
	}
	for _, rec := range records {
		if rec.GetConfig().GetRef().GetTenant().GetId() == tenantID {
			return true, nil
		}
	}
	return false, nil
}

func relationName(relation identityv1.TenantRelation) string {
	switch relation {
	case identityv1.TenantRelation_TENANT_RELATION_ADMIN:
		return "admin"
	case identityv1.TenantRelation_TENANT_RELATION_OPERATOR:
		return "operator"
	case identityv1.TenantRelation_TENANT_RELATION_CAPTURER:
		return "capturer"
	case identityv1.TenantRelation_TENANT_RELATION_VIEWER:
		return "viewer"
	default:
		return ""
	}
}

func tenantTuples(tenantID string, committed bool, members []*identityv1.Member, roles []*identityv1.Role, partners []*identityv1.Partner, now time.Time) []authz.Tuple {
	object := "tenant:" + tenantID
	var tuples []authz.Tuple
	if committed {
		tuples = append(tuples, authz.Tuple{Object: object, Relation: "platform", User: "platform:flowseer"})
	}
	for _, member := range members {
		operator := member.GetOperator()
		if operator == nil {
			continue
		}
		user := "user:" + authn.ComputePrincipalID(operator.GetIssuer(), operator.GetSubject())
		tuples = append(tuples, authz.Tuple{Object: object, Relation: "enrolled", User: user})
		grant := member.GetFullPayload()
		if grant != nil && grant.GetExpiresAt().AsTime().After(now) {
			tuples = append(tuples, authz.Tuple{Object: object, Relation: "full_payload", User: user})
		}
	}
	for _, role := range roles {
		roleID := role.GetRef().GetRole().GetId()
		for _, relation := range role.GetRelations() {
			if name := relationName(relation); name != "" {
				tuples = append(tuples, authz.Tuple{Object: object, Relation: name, User: "role:" + roleID + "#assignee"})
			}
		}
	}
	for _, partner := range partners {
		providerID := partner.GetTenant().GetTenant().GetId()
		provider := "tenant:" + providerID
		tuples = append(tuples, authz.Tuple{Object: object, Relation: "partner", User: provider})
		for _, relation := range partner.GetRelations() {
			if name := relationName(relation); name != "" && name != "admin" {
				tuples = append(tuples, authz.Tuple{Object: object, Relation: name, User: provider + "#active_admin"})
			}
		}
	}
	return tuples
}

func roleTuples(roleID string, roles []*identityv1.Role, members []*identityv1.Member) []authz.Tuple {
	if !slices.ContainsFunc(roles, func(role *identityv1.Role) bool {
		return role.GetRef().GetRole().GetId() == roleID
	}) {
		return nil
	}
	var tuples []authz.Tuple
	for _, member := range members {
		if slices.ContainsFunc(member.GetRoles(), func(ref *identityv1.RoleGlobalRef) bool {
			return ref.GetRole().GetId() == roleID
		}) {
			operator := member.GetOperator()
			tuples = append(tuples, authz.Tuple{
				Object: "role:" + roleID, Relation: "assignee",
				User: "user:" + authn.ComputePrincipalID(operator.GetIssuer(), operator.GetSubject()),
			})
		}
	}
	return tuples
}

// SyncTenant synchronizes a tenant and every role still named by its records
// or stored tenant tuples.
func (p *Projector) SyncTenant(ctx context.Context, tenantID string) error {
	if p.access == nil {
		return nil
	}
	stored, err := p.relations.Read(ctx, "tenant:"+tenantID)
	if err != nil {
		return err
	}
	if err := tenant.Validate(tenantID); err != nil {
		_, err := p.syncObjectWithStored(ctx, Object{Type: "tenant", ID: tenantID}, stored, true)
		return err
	}
	roleIDs := make(map[string]bool)
	for _, tuple := range stored {
		if id, ok := strings.CutPrefix(tuple.User, "role:"); ok {
			if roleID, valid := strings.CutSuffix(id, "#assignee"); valid {
				roleIDs[roleID] = true
			}
		}
	}
	roles, err := p.access.Roles(ctx, tenantID)
	if err != nil {
		return err
	}
	for _, role := range roles {
		roleIDs[role.GetRef().GetRole().GetId()] = true
	}
	if _, err := p.syncObjectWithStored(ctx, Object{Type: "tenant", ID: tenantID}, stored, true); err != nil {
		return err
	}
	ids := make([]string, 0, len(roleIDs))
	for id := range roleIDs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := p.Sync(ctx, Object{Type: "role", ID: id, Tenant: tenantID}); err != nil {
			return err
		}
	}
	return nil
}

// SyncRequester synchronizes the sessions requested by an operator in a tenant.
func (p *Projector) SyncRequester(ctx context.Context, tenantID string, operator *identityv1.OperatorRef) error {
	if p.access == nil || operator == nil {
		return nil
	}
	principalID := authn.ComputePrincipalID(operator.GetIssuer(), operator.GetSubject())
	var sessionIDs []string
	err := p.captures.EachSession(ctx, func(id string, rec *modelcapturev1.CaptureSessionRecord) error {
		if id != tenantID {
			return nil
		}
		requestedBy := rec.GetConfig().GetAuthorization().GetRequestedBy()
		if requestedBy != nil && authn.ComputePrincipalID(requestedBy.GetIssuer(), requestedBy.GetSubject()) == principalID {
			sessionIDs = append(sessionIDs, rec.GetConfig().GetRef().GetCaptureSession().GetId())
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, id := range sessionIDs {
		if err := p.Sync(ctx, Object{Type: "capture_session", ID: id, Tenant: tenantID}); err != nil {
			return err
		}
	}
	return nil
}

// Run executes periodic reconciliation passes until ctx is canceled. It calls
// the Reconciled callback only after a pass returns no error.
func (p *Projector) Run(ctx context.Context) error {
	nextWait := time.Duration(0)
	retryDelay := initialRetry
	if retryDelay > p.interval {
		retryDelay = p.interval
	}

	for {
		if nextWait > 0 {
			if p.wait != nil {
				if err := p.wait(ctx, nextWait); err != nil {
					return nil
				}
			} else {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(nextWait):
				}
			}
		} else if ctx.Err() != nil {
			return nil
		}

		_, err := p.Reconcile(ctx)
		if err != nil {
			p.log.ErrorContext(ctx, "relationship reconciliation pass failed", slog.String("error.type", telemetry.ErrorType(err)))
			nextWait = retryDelay
			retryDelay *= 2
			if retryDelay > p.interval {
				retryDelay = p.interval
			}
		} else {
			if p.reconciled != nil {
				p.reconciled()
			}
			nextWait = p.interval
			retryDelay = initialRetry
			if retryDelay > p.interval {
				retryDelay = p.interval
			}
		}
	}
}
