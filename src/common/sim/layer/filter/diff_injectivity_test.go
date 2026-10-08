package filter_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestDiffRuleSubjectKeysAreInjectiveAcrossSets(t *testing.T) {
	t.Parallel()

	a := filter.Config{
		Sets: map[string]filter.RuleSet{
			"a/b": {Rules: []filter.Rule{{Name: "c", Action: filter.Accept}}},
			"a":   {Rules: []filter.Rule{{Name: "b/c", Action: filter.Accept}}},
		},
	}
	b := filter.Config{
		Sets: map[string]filter.RuleSet{
			"a/b": {Rules: []filter.Rule{{Name: "c", Action: filter.Drop}}},
			"a":   {Rules: []filter.Rule{{Name: "b/c", Action: filter.Drop}}},
		},
	}

	a.Sets["numeric"] = filter.RuleSet{Rules: []filter.Rule{{Name: "1", Action: filter.Accept}, {Action: filter.Drop}}}
	b.Sets["numeric"] = filter.RuleSet{Rules: []filter.Rule{{Name: "1", Action: filter.Drop}, {Action: filter.Drop}}}

	changes := filter.Diff(a, b)
	var keys []string
	for _, c := range changes {
		if c.Subject.Kind == "rule" {
			keys = append(keys, c.Subject.Key)
		}
	}
	if len(keys) != 3 {
		t.Fatalf("got %d rule changes, want 3: %+v", len(keys), changes)
	}
	if keys[0] == keys[1] {
		t.Fatalf("rule subject keys collide: %q", keys[0])
	}
}

func TestDiffPositionalRuleKeysOnSetAdditionAndRemoval(t *testing.T) {
	cfg := filter.Config{Sets: map[string]filter.RuleSet{"s": {Default: filter.Drop, Rules: []filter.Rule{
		{Name: "1", Action: filter.Accept},
		{Action: filter.Drop},
		{Name: "duplicate", Action: filter.Accept},
		{Name: "duplicate", Action: filter.Drop},
	}}}}
	want := []string{trace.CompositeKey("s", "1"), trace.CompositeKey("s", "", "1"), trace.CompositeKey("s", "", "2"), trace.CompositeKey("s", "", "3")}
	changed := cfg.Clone()
	for idx, rule := range changed.Sets["s"].Rules {
		if rule.Action == filter.Accept {
			changed.Sets["s"].Rules[idx].Action = filter.Drop
		} else {
			changed.Sets["s"].Rules[idx].Action = filter.Accept
		}
	}
	for _, tt := range []struct {
		name string
		a, b filter.Config
	}{
		{name: "added", b: cfg},
		{name: "removed", a: cfg},
		{name: "changed", a: cfg, b: changed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			keys := make(map[string]bool)
			for _, change := range filter.Diff(tt.a, tt.b) {
				if change.Subject.Kind == "rule" {
					keys[change.Subject.Key] = true
				}
			}
			if len(keys) != len(want) {
				t.Errorf("keys = %v, want four distinct keys", keys)
			}
			for _, key := range want {
				if !keys[key] {
					t.Errorf("keys = %v, missing %q", keys, key)
				}
			}
		})
	}
}
