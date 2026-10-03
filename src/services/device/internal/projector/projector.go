// Package projector projects central records into relationship tuples for
// operator authorization and reconciles drift periodically.
package projector

import (
	"context"
	"log/slog"
	"strings"
	"time"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
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

// Object identifies a central entity to synchronize into the relationship graph.
type Object struct {
	Type   string
	ID     string
	Tenant string
}

// Projector synchronizes central records into relationship tuples.
type Projector struct {
	relations  authz.Relations
	edges      EdgeSource
	registry   RegistrySource
	captures   CaptureSource
	interval   time.Duration
	log        *slog.Logger
	reconciled func()
	wait       func(ctx context.Context, d time.Duration) error
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
) *Projector {
	if interval <= 0 {
		interval = defaultInterval
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Projector{
		relations:  relations,
		edges:      edges,
		registry:   registry,
		captures:   captures,
		interval:   interval,
		log:        logger,
		reconciled: reconciled,
	}
}

// Sync synchronizes relationship tuples for a single object.
func (p *Projector) Sync(ctx context.Context, obj Object) error {
	_, err := p.syncObject(ctx, obj)
	return err
}

func (p *Projector) syncObject(ctx context.Context, obj Object) (bool, error) {
	for attempt := 0; attempt < maxSyncAttempts; attempt++ {
		repaired, err := p.attemptSync(ctx, obj)
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

func (p *Projector) attemptSync(ctx context.Context, obj Object) (bool, error) {
	objectKey := obj.Type + ":" + obj.ID
	storedTuples, err := p.relations.Read(ctx, objectKey)
	if err != nil {
		return false, err
	}

	existingOwned := filterOwnedTuples(obj.Type, storedTuples)

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
			for _, t := range existingOwned {
				if t.Relation == "tenant" && strings.HasPrefix(t.User, "tenant:") {
					tenantID = strings.TrimPrefix(t.User, "tenant:")
					break
				}
			}
		}
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
			principalID := authn.ComputePrincipalID(reqBy.GetIssuer(), reqBy.GetSubject())
			desired = append(desired, authz.Tuple{
				Object:   objectKey,
				Relation: "requester",
				User:     "user:" + principalID,
			})
		}
		return desired, nil

	default:
		return nil, nil
	}
}

// Run executes periodic reconciliation passes until ctx is canceled.
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
