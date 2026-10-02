package authz

import (
	"context"
	"sync"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
)

const maxChecksPerBatch = 50

type obligationTracker struct {
	mu             sync.Mutex // guards discharged and requireDenied
	checker        Checker
	principal      authn.Principal
	admittedTenant string
	discharged     bool
	requireDenied  bool
}

func (t *obligationTracker) flags() (discharged, requireDenied bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.discharged, t.requireDenied
}

func (t *obligationTracker) discharge() {
	t.mu.Lock()
	t.discharged = true
	t.mu.Unlock()
}

func (t *obligationTracker) recordRequireDenied() {
	t.mu.Lock()
	t.requireDenied = true
	t.mu.Unlock()
}

type obligationKey struct{}

func withTracker(ctx context.Context, t *obligationTracker) context.Context {
	return context.WithValue(ctx, obligationKey{}, t)
}

func trackerFromContext(ctx context.Context) *obligationTracker {
	t, _ := ctx.Value(obligationKey{}).(*obligationTracker)
	return t
}

func contextualTuples(p authn.Principal) []Tuple {
	tuples := make([]Tuple, 0, len(p.Tenants)+1)
	for _, t := range p.Tenants {
		tuples = append(tuples, Tuple{
			Object:   "tenant:" + t,
			Relation: "claimed",
			User:     "user:" + p.ID,
		})
	}
	if p.Platform {
		tuples = append(tuples, Tuple{
			Object:   "platform:flowseer",
			Relation: "claimed",
			User:     "user:" + p.ID,
		})
	}
	return tuples
}

// Require evaluates relation and tenant constraints for an object, discharging
// the deferred authorization obligation on the context.
//
// If ctx was not prepared by the interceptor or lacks an admitted tenant, Require
// returns an Internal error. If authorization is denied, Require returns a
// PermissionDenied error, and the interceptor answers Internal if the handler
// returns a response. If the checker fails or returns an unexpected answer
// count, Require returns an Unavailable error.
func Require(ctx context.Context, relation, objectType, id string) error {
	tracker := trackerFromContext(ctx)
	if tracker == nil || tracker.admittedTenant == "" {
		return internalError(errs.New().Code(ErrCodeObligationViolation).
			Msg("require called on invalid context or without admitted tenant"))
	}

	tracker.discharge()

	allowed, err := checkObjects(ctx, tracker.checker, tracker.principal, tracker.admittedTenant, objectType, relation, []string{id})
	if err != nil {
		return err
	}
	if len(allowed) == 0 {
		tracker.recordRequireDenied()
		return permissionDenied(errs.New().Code(ErrCodeDenied).
			Msg("permission denied"))
	}
	return nil
}

// Filter deduplicates candidate object identifiers, evaluates relation and tenant
// constraints across them, batching checks in groups of at most 50, and returns
// the permitted unique identifiers in first-seen input order.
//
// If ctx was not prepared by the interceptor or lacks an admitted tenant, Filter
// returns an Internal error. If the checker fails or returns an unexpected answer
// count, Filter returns an Unavailable error.
func Filter(ctx context.Context, relation, objectType string, ids []string) ([]string, error) {
	tracker := trackerFromContext(ctx)
	if tracker == nil || tracker.admittedTenant == "" {
		return nil, internalError(errs.New().Code(ErrCodeObligationViolation).
			Msg("filter called on invalid context or without admitted tenant"))
	}

	tracker.discharge()

	if len(ids) == 0 {
		return []string{}, nil
	}

	var unique []string
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}

	return checkObjects(ctx, tracker.checker, tracker.principal, tracker.admittedTenant, objectType, relation, unique)
}

func checkObjects(ctx context.Context, checker Checker, p authn.Principal, admittedTenant, objectType, relation string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}

	tuples := contextualTuples(p)

	if objectType == "tenant" {
		var allowed []string
		for _, id := range ids {
			if id != admittedTenant {
				continue
			}
			q := Query{
				Object:           "tenant:" + id,
				Relation:         relation,
				User:             "user:" + p.ID,
				ContextualTuples: tuples,
			}
			ok, err := checker.Check(ctx, q)
			if err != nil {
				return nil, unavailable(errs.New().Code(ErrCodeUnavailable).Cause(err).
					Retryable().Msg("authorization is unavailable"))
			}
			if ok {
				allowed = append(allowed, id)
			}
		}
		return allowed, nil
	}

	var queries []Query
	for _, id := range ids {
		queries = append(queries,
			Query{
				Object:           objectType + ":" + id,
				Relation:         relation,
				User:             "user:" + p.ID,
				ContextualTuples: tuples,
			},
			Query{
				Object:           objectType + ":" + id,
				Relation:         "tenant",
				User:             "tenant:" + admittedTenant,
				ContextualTuples: tuples,
			},
		)
	}

	var allAnswers []bool
	for start := 0; start < len(queries); start += maxChecksPerBatch {
		end := start + maxChecksPerBatch
		if end > len(queries) {
			end = len(queries)
		}
		batch := queries[start:end]
		answers, err := checker.BatchCheck(ctx, batch)
		if err != nil {
			return nil, unavailable(errs.New().Code(ErrCodeUnavailable).Cause(err).
				Retryable().Msg("authorization is unavailable"))
		}
		if len(answers) != len(batch) {
			return nil, unavailable(errs.New().Code(ErrCodeUnavailable).
				Retryable().Msg("checker returned unexpected answer count"))
		}
		allAnswers = append(allAnswers, answers...)
	}

	var allowed []string
	for i, id := range ids {
		relAllowed := allAnswers[2*i]
		tenantAllowed := allAnswers[2*i+1]
		if relAllowed && tenantAllowed {
			allowed = append(allowed, id)
		}
	}

	return allowed, nil
}
