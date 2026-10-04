package filter_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
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

	changes := filter.Diff(a, b)
	var keys []string
	for _, c := range changes {
		if c.Subject.Kind == "rule" {
			keys = append(keys, c.Subject.Key)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("got %d rule changes, want 2: %+v", len(keys), changes)
	}
	if keys[0] == keys[1] {
		t.Fatalf("rule subject keys collide: %q", keys[0])
	}
}
