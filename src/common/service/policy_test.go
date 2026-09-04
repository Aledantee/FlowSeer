package service

import (
	"testing"
	"time"
)

func TestPolicyDefaults(t *testing.T) {
	policy, err := normalizePolicy(Policy{})
	if err != nil {
		t.Fatalf("normalizePolicy() error = %v", err)
	}

	if policy.normal.action != Stop {
		t.Errorf("normal action = %v, want Stop", policy.normal.action)
	}
	for name, outcome := range map[string]normalizedOutcomePolicy{
		"error": policy.failure,
		"panic": policy.panic,
	} {
		if outcome.action != Restart {
			t.Errorf("%s action = %v, want Restart", name, outcome.action)
		}
		if outcome.budget != (RestartBudget{Max: 3, Window: time.Minute}) {
			t.Errorf("%s budget = %+v, want three per minute", name, outcome.budget)
		}
		if outcome.backoff != (Backoff{Initial: time.Second, Maximum: 30 * time.Second, ResetAfter: time.Minute}) {
			t.Errorf("%s backoff = %+v, want one-second initial, 30-second maximum, one-minute reset", name, outcome.backoff)
		}
	}
}

func TestSupervisorDefaults(t *testing.T) {
	branch, err := normalizeSupervisor(OneForOne, RestartBudget{})
	if err != nil {
		t.Fatalf("normalizeSupervisor() error = %v", err)
	}
	if branch.intensity != (RestartBudget{Max: 5, Window: 5 * time.Minute}) {
		t.Errorf("branch intensity = %+v, want five per five minutes", branch.intensity)
	}
	root, err := normalizeRootSupervisor(OneForOne, RestartBudget{})
	if err != nil {
		t.Fatalf("normalizeRootSupervisor() error = %v", err)
	}
	if root.intensity != (RestartBudget{Max: 3, Window: 5 * time.Minute}) {
		t.Errorf("root intensity = %+v, want three per five minutes", root.intensity)
	}
}

func TestPolicyRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		policy Policy
	}{
		{name: "action", policy: Policy{Normal: OutcomePolicy{Action: Action(99)}}},
		{name: "negative budget", policy: Policy{Error: OutcomePolicy{Budget: RestartBudget{Max: -1}}}},
		{name: "budget window", policy: Policy{Error: OutcomePolicy{Budget: RestartBudget{Max: 1}}}},
		{name: "backoff maximum", policy: Policy{Error: OutcomePolicy{Backoff: Backoff{Initial: time.Second, Maximum: time.Millisecond}}}},
		{name: "strategy", policy: Policy{}},
	}

	for _, tt := range tests[:len(tests)-1] {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizePolicy(tt.policy); err == nil {
				t.Fatal("normalizePolicy() succeeded, want error")
			}
		})
	}
	if _, err := normalizeSupervisor(Strategy(99), RestartBudget{}); err == nil {
		t.Fatal("normalizeSupervisor() succeeded with unknown strategy")
	}
}

func TestRollingBudgetUsesSlidingWindow(t *testing.T) {
	budget := rollingBudget{limit: RestartBudget{Max: 2, Window: time.Minute}}
	now := time.Unix(100, 0)
	if !budget.consume(now) || !budget.consume(now.Add(time.Second)) {
		t.Fatal("first two budget uses were rejected")
	}
	if budget.consume(now.Add(2 * time.Second)) {
		t.Fatal("third budget use was accepted inside window")
	}
	if !budget.consume(now.Add(time.Minute + time.Nanosecond)) {
		t.Fatal("expired budget use was not pruned")
	}
}
