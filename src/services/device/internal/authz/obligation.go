package authz

import (
	"context"
	"sync"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
)

const maxChecksPerBatch = 50

type obligationTracker struct {
	mu             sync.Mutex // guards discharged, checkFailed, inFlight
	checker        Checker
	principal      authn.Principal
	admittedTenant string
	discharged     bool
	checkFailed    bool
	inFlight       int
}

func newObligationTracker(checker Checker, principal authn.Principal, admittedTenant string) *obligationTracker {
	return &obligationTracker{
		checker:        checker,
		principal:      principal,
		admittedTenant: admittedTenant,
	}
}

func (t *obligationTracker) flags() (discharged, checkFailed bool, inFlight int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.discharged, t.checkFailed, t.inFlight
}

func (t *obligationTracker) startCheck() {
	t.mu.Lock()
	t.inFlight++
	t.mu.Unlock()
}

func (t *obligationTracker) finishCheck(failed bool) {
	t.mu.Lock()
	t.inFlight--
	if failed {
		t.checkFailed = true
	}
	t.mu.Unlock()
}

func (t *obligationTracker) discharge() {
	t.mu.Lock()
	t.discharged = true
	t.mu.Unlock()
}

func (t *obligationTracker) recordCheckFailed() {
	t.mu.Lock()
	t.checkFailed = true
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
// PermissionDenied error. If the checker fails while ctx.Err() is non-nil, Require
// returns ctx.Err(). Otherwise checker errors return Unavailable. If the handler
// returns a response after Require returned any error, the interceptor answers
// Internal with an obligation violation.
func Require(ctx context.Context, relation, objectType, id string) (err error) {
	tracker := trackerFromContext(ctx)
	if tracker != nil {
		tracker.startCheck()
		tracker.discharge()
		defer func() {
			tracker.finishCheck(err != nil)
		}()
	}
	if tracker == nil || tracker.admittedTenant == "" {
		return internalError(errs.New().Code(ErrCodeObligationViolation).
			Msg("require called on invalid context or without admitted tenant"))
	}

	allowed, err := checkObjects(ctx, tracker.checker, tracker.principal, tracker.admittedTenant, objectType, relation, []string{id})
	if err != nil {
		return err
	}
	if len(allowed) == 0 {
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
// returns an Internal error. If the checker fails while ctx.Err() is non-nil, Filter
// returns ctx.Err(). Otherwise checker errors return Unavailable. If the handler
// returns a response after Filter returned any error, the interceptor answers
// Internal with an obligation violation.
func Filter(ctx context.Context, relation, objectType string, ids []string) (_ []string, err error) {
	tracker := trackerFromContext(ctx)
	if tracker != nil {
		tracker.startCheck()
		tracker.discharge()
		defer func() {
			tracker.finishCheck(err != nil)
		}()
	}
	if tracker == nil || tracker.admittedTenant == "" {
		return nil, internalError(errs.New().Code(ErrCodeObligationViolation).
			Msg("filter called on invalid context or without admitted tenant"))
	}

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

// Abandon discharges an authorization obligation when a handler encounters an
// error before it can evaluate its relationship checks.
//
// If ctx was not prepared by the interceptor or lacks an admitted tenant, or if
// err is nil, Abandon returns an Internal error. Otherwise, Abandon marks the
// obligation discharged and records a failed check, so that any response
// returned after it is dropped, then returns err.
func Abandon(ctx context.Context, err error) error {
	tracker := trackerFromContext(ctx)
	if tracker != nil {
		tracker.discharge()
		tracker.recordCheckFailed()
	}
	if tracker == nil || tracker.admittedTenant == "" || err == nil {
		return internalError(errs.New().Code(ErrCodeObligationViolation).
			Msg("abandon called on invalid context, without admitted tenant, or with nil error"))
	}
	return err
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
				return nil, checkerError(ctx, err, "authorization is unavailable")
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
			return nil, checkerError(ctx, err, "authorization is unavailable")
		}
		if len(answers) != len(batch) {
			return nil, checkerError(ctx, nil, "checker returned unexpected answer count")
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
