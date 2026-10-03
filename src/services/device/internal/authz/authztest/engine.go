// Package authztest provides an in-memory authorization engine for testing.
package authztest

import (
	"context"
	"slices"
	"strings"
	"sync"

	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
)

var _ authz.Engine = (*Engine)(nil)

// grant matches any object of objectType for user and relation.
type grant struct {
	user       string
	relation   string
	objectType string
}

// Engine is an in-memory test implementation of authz.Engine.
// It is safe for concurrent use.
type Engine struct {
	mu      sync.RWMutex
	stored  []authz.Tuple
	grants  []grant
	queries []authz.Query
	failErr error
}

// New constructs an empty test engine.
func New() *Engine {
	return &Engine{}
}

// Fail configures the engine to return err on subsequent operations until
// cleared with Fail(nil).
func (e *Engine) Fail(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failErr = err
}

// Grant adds a wildcard permission for user on relation across all objects of
// objectType.
func (e *Engine) Grant(user, relation, objectType string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.grants = append(e.grants, grant{
		user:       user,
		relation:   relation,
		objectType: objectType,
	})
}

// Queries returns a copy of all recorded queries in execution order.
func (e *Engine) Queries() []authz.Query {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return slices.Clone(e.queries)
}

// Reset clears all stored tuples, grants, recorded queries, and errors.
func (e *Engine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stored = nil
	e.grants = nil
	e.queries = nil
	e.failErr = nil
}

// Write applies writes and deletes to stored tuples. Duplicate writes and
// deletes of missing tuples are ignored.
func (e *Engine) Write(ctx context.Context, writes, deletes []authz.Tuple) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failErr != nil {
		return e.failErr
	}

	for _, del := range deletes {
		e.stored = slices.DeleteFunc(e.stored, func(t authz.Tuple) bool {
			return t == del
		})
	}

	for _, w := range writes {
		if !slices.Contains(e.stored, w) {
			e.stored = append(e.stored, w)
		}
	}
	return nil
}

// Read returns all stored tuples matching object.
func (e *Engine) Read(ctx context.Context, object string) ([]authz.Tuple, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.failErr != nil {
		return nil, e.failErr
	}

	var results []authz.Tuple
	for _, t := range e.stored {
		if t.Object == object {
			results = append(results, t)
		}
	}
	if results == nil {
		results = []authz.Tuple{}
	}
	return results, nil
}

// Scan invokes fn for each stored tuple until fn returns an error or all
// tuples have been visited.
func (e *Engine) Scan(ctx context.Context, fn func(authz.Tuple) error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	e.mu.RLock()
	tuples := slices.Clone(e.stored)
	err := e.failErr
	e.mu.RUnlock()
	if err != nil {
		return err
	}

	for _, t := range tuples {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err := fn(t); err != nil {
			return err
		}
	}
	return nil
}

// Check evaluates an authorization query against contextual tuples, stored
// tuples, and active grants.
func (e *Engine) Check(ctx context.Context, q authz.Query) (bool, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	e.mu.Lock()
	e.queries = append(e.queries, q)
	err := e.failErr
	e.mu.Unlock()
	if err != nil {
		return false, err
	}

	return e.eval(q), nil
}

// BatchCheck evaluates multiple queries in order.
func (e *Engine) BatchCheck(ctx context.Context, queries []authz.Query) ([]bool, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	e.mu.Lock()
	e.queries = append(e.queries, queries...)
	err := e.failErr
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}

	answers := make([]bool, len(queries))
	for i, q := range queries {
		answers[i] = e.eval(q)
	}
	return answers, nil
}

func (e *Engine) eval(q authz.Query) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, t := range q.ContextualTuples {
		if t.Object == q.Object && t.Relation == q.Relation && t.User == q.User {
			return true
		}
	}

	for _, t := range e.stored {
		if t.Object == q.Object && t.Relation == q.Relation && t.User == q.User {
			return true
		}
	}

	objType, _, hasSep := strings.Cut(q.Object, ":")
	if hasSep {
		for _, g := range e.grants {
			if g.user == q.User && g.relation == q.Relation && g.objectType == objType {
				return true
			}
		}
	}
	return false
}
