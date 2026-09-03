package service

import (
	"fmt"
	"time"
)

const (
	defaultOutcomeRestarts  = 3
	defaultOutcomeWindow    = time.Minute
	defaultBackoffInitial   = time.Second
	defaultBackoffMaximum   = 30 * time.Second
	defaultHealthyReset     = time.Minute
	defaultBranchRestarts   = 5
	defaultRootRestarts     = 3
	defaultSupervisorWindow = 5 * time.Minute
)

// Action chooses what the owning supervisor does after a module attempt ends.
// The zero value selects the outcome default: Stop for a normal return and
// Restart for an error or panic.
type Action uint8

const (
	actionDefault Action = iota
	// Stop leaves the module inactive until its owning supervisor is rebuilt.
	Stop
	// Restart applies the owning supervisor's restart strategy.
	Restart
	// Escalate fails the owning supervisor immediately.
	Escalate
)

// Strategy selects which active siblings are reconstructed after a Restart.
type Strategy uint8

const (
	// OneForOne reconstructs only the module that ended.
	OneForOne Strategy = iota
	// OneForAll reconstructs every active child.
	OneForAll
	// RestForOne reconstructs the module that ended and active children after it.
	RestForOne
)

// RestartBudget limits restart decisions within a rolling window. Its zero
// value selects the runtime default for the field where it is used.
type RestartBudget struct {
	Max    int
	Window time.Duration
}

// Backoff configures full-jitter exponential restart delay. Its zero value
// selects the KTD6 defaults.
type Backoff struct {
	Initial    time.Duration
	Maximum    time.Duration
	ResetAfter time.Duration
}

// OutcomePolicy configures one attempt outcome. Each outcome owns independent
// budget and backoff state.
type OutcomePolicy struct {
	Action  Action
	Budget  RestartBudget
	Backoff Backoff
}

// Policy configures normal returns, returned errors, and recovered panics.
// Its zero value stops normal returns and restarts errors and panics.
type Policy struct {
	Normal OutcomePolicy
	Error  OutcomePolicy
	Panic  OutcomePolicy
}

type normalizedOutcomePolicy struct {
	action  Action
	budget  RestartBudget
	backoff Backoff
}

type normalizedPolicy struct {
	normal  normalizedOutcomePolicy
	failure normalizedOutcomePolicy
	panic   normalizedOutcomePolicy
}

type normalizedSupervisor struct {
	strategy  Strategy
	intensity RestartBudget
}

func normalizePolicy(policy Policy) (normalizedPolicy, error) {
	normal, err := normalizeOutcomePolicy("normal", policy.Normal, Stop)
	if err != nil {
		return normalizedPolicy{}, err
	}
	failure, err := normalizeOutcomePolicy("error", policy.Error, Restart)
	if err != nil {
		return normalizedPolicy{}, err
	}
	panicked, err := normalizeOutcomePolicy("panic", policy.Panic, Restart)
	if err != nil {
		return normalizedPolicy{}, err
	}
	return normalizedPolicy{normal: normal, failure: failure, panic: panicked}, nil
}

func normalizeOutcomePolicy(name string, policy OutcomePolicy, defaultAction Action) (normalizedOutcomePolicy, error) {
	action := policy.Action
	if action == actionDefault {
		action = defaultAction
	}
	if action < Stop || action > Escalate {
		return normalizedOutcomePolicy{}, fmt.Errorf("service %s outcome has unknown action %d", name, action)
	}

	budget, err := normalizeBudget(name+" outcome", policy.Budget, defaultOutcomeRestarts, defaultOutcomeWindow)
	if err != nil {
		return normalizedOutcomePolicy{}, err
	}
	backoff := policy.Backoff
	if backoff == (Backoff{}) {
		backoff = Backoff{Initial: defaultBackoffInitial, Maximum: defaultBackoffMaximum, ResetAfter: defaultHealthyReset}
	}
	if backoff.Initial <= 0 || backoff.Maximum < backoff.Initial || backoff.ResetAfter <= 0 {
		return normalizedOutcomePolicy{}, fmt.Errorf("service %s outcome has invalid backoff", name)
	}
	return normalizedOutcomePolicy{action: action, budget: budget, backoff: backoff}, nil
}

func normalizeSupervisor(strategy Strategy, intensity RestartBudget) (normalizedSupervisor, error) {
	if strategy > RestForOne {
		return normalizedSupervisor{}, fmt.Errorf("service supervisor has unknown strategy %d", strategy)
	}
	budget, err := normalizeBudget("supervisor", intensity, defaultBranchRestarts, defaultSupervisorWindow)
	if err != nil {
		return normalizedSupervisor{}, err
	}
	return normalizedSupervisor{strategy: strategy, intensity: budget}, nil
}

func normalizeRootSupervisor(strategy Strategy, intensity RestartBudget) (normalizedSupervisor, error) {
	if strategy > RestForOne {
		return normalizedSupervisor{}, fmt.Errorf("service root supervisor has unknown strategy %d", strategy)
	}
	budget, err := normalizeBudget("root supervisor", intensity, defaultRootRestarts, defaultSupervisorWindow)
	if err != nil {
		return normalizedSupervisor{}, err
	}
	return normalizedSupervisor{strategy: strategy, intensity: budget}, nil
}

func normalizeBudget(name string, budget RestartBudget, defaultMax int, defaultWindow time.Duration) (RestartBudget, error) {
	if budget == (RestartBudget{}) {
		return RestartBudget{Max: defaultMax, Window: defaultWindow}, nil
	}
	if budget.Max <= 0 || budget.Window <= 0 {
		return RestartBudget{}, fmt.Errorf("service %s restart budget must have a positive max and window", name)
	}
	return budget, nil
}

type rollingBudget struct {
	limit RestartBudget
	uses  []time.Time
}

func (b *rollingBudget) consume(now time.Time) bool {
	cutoff := now.Add(-b.limit.Window)
	first := 0
	for first < len(b.uses) && !b.uses[first].After(cutoff) {
		first++
	}
	b.uses = append(b.uses[:0], b.uses[first:]...)
	if len(b.uses) >= b.limit.Max {
		return false
	}
	b.uses = append(b.uses, now)
	return true
}

func (b *rollingBudget) reset() {
	b.uses = b.uses[:0]
}
