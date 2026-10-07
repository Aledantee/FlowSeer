// Package search provides bounded differential search over finite domains of
// network simulation scenarios and deterministic counterexample minimization.
package search

import (
	"cmp"
	"math"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

// Tuple is the canonical, deterministic identity of a domain candidate.
type Tuple string

// String returns the tuple's canonical representation.
func (t Tuple) String() string { return string(t) }

// Candidate pairs an ordered injection and fault scenario with its domain identity.
// Action indices identify declarations and survive reduction.
type Candidate struct {
	Tuple   Tuple
	Actions []fabric.Action
}

// Clone returns a copy with independent action payloads.
func (c Candidate) Clone() Candidate {
	cp := c
	if c.Actions != nil {
		cp.Actions = make([]fabric.Action, len(c.Actions))
		for i, action := range c.Actions {
			cp.Actions[i] = action.Clone()
		}
	}
	return cp
}

// Validate rejects non-injection/non-fault actions and invalid scenario actions,
// including mixed numbering and duplicate timestamp/index pairs.
func (c Candidate) Validate() error {
	for i, action := range c.Actions {
		if action.Kind != fabric.ActionInject && action.Kind != fabric.ActionFault {
			return errs.Msgf("candidate action %d must be injection or fault", i)
		}
	}
	return c.ToScenario(1).Validate()
}

// ToScenario copies the candidate for execution. Unnumbered declarations receive
// indices once, with injections preceding faults at tied times. Numbered actions
// retain their indices and follow the fabric's timestamp/index order.
func (c Candidate) ToScenario(budget int) fabric.Scenario {
	actions := c.Clone().Actions
	numbered := false
	for _, action := range actions {
		if action.Index != 0 {
			numbered = true
		}
	}
	if !numbered {
		slices.SortStableFunc(actions, func(a, b fabric.Action) int {
			if a.Kind == b.Kind {
				return 0
			}
			if a.Kind == fabric.ActionInject {
				return -1
			}
			return 1
		})
		for i := range actions {
			actions[i].Index = i + 1
		}
	}
	for i := range actions {
		actions[i].At = actions[i].At.Round(0).UTC()
		if actions[i].Inject != nil && !actions[i].Inject.At.IsZero() {
			actions[i].Inject.At = actions[i].Inject.At.Round(0).UTC()
		}
	}
	slices.SortStableFunc(actions, func(a, b fabric.Action) int {
		if r := a.At.Compare(b.At); r != 0 {
			return r
		}
		return cmp.Compare(a.Index, b.Index)
	})
	return fabric.Scenario{Name: "search-candidate", Actions: actions, Budget: budget}
}

// Limits bounds evaluations and retained differences. It is immutable during Search.
type Limits struct {
	// MaxCandidates bounds tested candidates. Zero enumerates the entire domain.
	MaxCandidates int
	// MaxDifferences is the retained replay limit. Zero counts only, -1 is unlimited.
	MaxDifferences int
	// Budget is the positive per-candidate simulation step budget.
	Budget int
}

// Validate rejects nonpositive budgets, negative candidate limits, and difference
// limits below -1 before any domain enumeration.
func (l Limits) Validate() error {
	if l.Budget <= 0 {
		return errs.Msg("search budget must be positive")
	}
	if l.MaxCandidates < 0 {
		return errs.Msg("search candidate limit must be nonnegative")
	}
	if l.MaxDifferences < -1 {
		return errs.Msg("search difference limit must be at least -1")
	}
	return nil
}

// Domain enumerates a finite deterministic set. Size is its exact candidate count.
// Enumerate stops when yield returns false, with order independent of input ordering.
type Domain interface {
	Size() int
	Enumerate(yield func(Candidate) bool)
}

func productSize(dimensions ...int) (int, error) {
	if slices.Contains(dimensions, 0) {
		return 0, nil
	}
	size := 1
	for _, dimension := range dimensions {
		if dimension > math.MaxInt/size {
			return 0, errs.Msg("domain cross product exceeds int")
		}
		size *= dimension
	}
	return size, nil
}
