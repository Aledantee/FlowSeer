package authz

import (
	"context"
	"sync"

	authzv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/authz/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
)

const maxChecksPerBatch = 50

type obligationTracker struct {
	mu             sync.Mutex
	checker        Checker
	principal      authn.Principal
	admittedTenant string
	mode           authzv1.RuleMode
	discharged     bool
	requireDenied  bool
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
func Require(ctx context.Context, relation, objectType, id string) error {
	tracker := trackerFromContext(ctx)
	if tracker == nil || tracker.admittedTenant == "" {
		return internalError(errs.New().Code(ErrCodeObligationViolation).
			Msg("require called on invalid context or without admitted tenant"))
	}

	tracker.mu.Lock()
	tracker.discharged = true
	tracker.mu.Unlock()

	tuples := contextualTuples(tracker.principal)

	if objectType == "tenant" {
		if id != tracker.admittedTenant {
			tracker.mu.Lock()
			tracker.requireDenied = true
			tracker.mu.Unlock()
			return permissionDenied(errs.New().Code(ErrCodeDenied).
				Msg("tenant id does not match admitted tenant"))
		}

		q := Query{
			Object:           "tenant:" + id,
			Relation:         relation,
			User:             "user:" + tracker.principal.ID,
			ContextualTuples: tuples,
		}
		allowed, err := tracker.checker.Check(ctx, q)
		if err != nil {
			return unavailable(errs.New().Code(ErrCodeUnavailable).Cause(err).
				Msg("authorization is unavailable"))
		}
		if !allowed {
			tracker.mu.Lock()
			tracker.requireDenied = true
			tracker.mu.Unlock()
			return permissionDenied(errs.New().Code(ErrCodeDenied).
				Msg("permission denied"))
		}
		return nil
	}

	q1 := Query{
		Object:           objectType + ":" + id,
		Relation:         relation,
		User:             "user:" + tracker.principal.ID,
		ContextualTuples: tuples,
	}
	q2 := Query{
		Object:           objectType + ":" + id,
		Relation:         "tenant",
		User:             "tenant:" + tracker.admittedTenant,
		ContextualTuples: tuples,
	}
	answers, err := tracker.checker.BatchCheck(ctx, []Query{q1, q2})
	if err != nil {
		return unavailable(errs.New().Code(ErrCodeUnavailable).Cause(err).
			Msg("authorization is unavailable"))
	}
	if len(answers) < 2 || !answers[0] || !answers[1] {
		tracker.mu.Lock()
		tracker.requireDenied = true
		tracker.mu.Unlock()
		return permissionDenied(errs.New().Code(ErrCodeDenied).
			Msg("permission denied"))
	}
	return nil
}

// Filter evaluates relation and tenant constraints across candidate object identifiers,
// batching checks in groups of at most 50, and returns the permitted identifiers.
func Filter(ctx context.Context, relation, objectType string, ids []string) ([]string, error) {
	tracker := trackerFromContext(ctx)
	if tracker == nil || tracker.admittedTenant == "" {
		return nil, internalError(errs.New().Code(ErrCodeObligationViolation).
			Msg("filter called on invalid context or without admitted tenant"))
	}

	tracker.mu.Lock()
	tracker.discharged = true
	tracker.mu.Unlock()

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

	tuples := contextualTuples(tracker.principal)

	if objectType == "tenant" {
		var allowed []string
		for _, id := range unique {
			if id != tracker.admittedTenant {
				continue
			}
			q := Query{
				Object:           "tenant:" + id,
				Relation:         relation,
				User:             "user:" + tracker.principal.ID,
				ContextualTuples: tuples,
			}
			ok, err := tracker.checker.Check(ctx, q)
			if err != nil {
				return nil, unavailable(errs.New().Code(ErrCodeUnavailable).Cause(err).
					Msg("authorization is unavailable"))
			}
			if ok {
				allowed = append(allowed, id)
			}
		}
		return allowed, nil
	}

	var queries []Query
	for _, id := range unique {
		queries = append(queries,
			Query{
				Object:           objectType + ":" + id,
				Relation:         relation,
				User:             "user:" + tracker.principal.ID,
				ContextualTuples: tuples,
			},
			Query{
				Object:           objectType + ":" + id,
				Relation:         "tenant",
				User:             "tenant:" + tracker.admittedTenant,
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
		answers, err := tracker.checker.BatchCheck(ctx, batch)
		if err != nil {
			return nil, unavailable(errs.New().Code(ErrCodeUnavailable).Cause(err).
				Msg("authorization is unavailable"))
		}
		if len(answers) != len(batch) {
			return nil, unavailable(errs.New().Code(ErrCodeUnavailable).
				Msg("checker returned unexpected answer count"))
		}
		allAnswers = append(allAnswers, answers...)
	}

	var allowed []string
	for i, id := range unique {
		relAllowed := allAnswers[2*i]
		tenantAllowed := allAnswers[2*i+1]
		if relAllowed && tenantAllowed {
			allowed = append(allowed, id)
		}
	}

	return allowed, nil
}
