package lag_test

import (
	"strconv"
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
)

func TestDiffMemberSubjectKeysAreInjectiveAcrossLAGs(t *testing.T) {
	t.Parallel()

	pairs := []struct {
		lag    string
		member string
	}{
		{lag: "a/b", member: "c"},
		{lag: "a", member: "b/c"},
		{lag: "left|lag", member: "shared/member"},
		{lag: "right|lag", member: "shared/member"},
	}
	a := lag.Config{LAGs: make(map[string]lag.LAG, len(pairs))}
	b := lag.Config{LAGs: make(map[string]lag.LAG, len(pairs))}
	for _, pair := range pairs {
		a.LAGs[pair.lag] = lag.LAG{Members: map[string]lag.Member{pair.member: {Priority: 1}}}
		b.LAGs[pair.lag] = lag.LAG{Members: map[string]lag.Member{pair.member: {Priority: 2}}}
	}

	changes := lag.Diff(a, b)
	keys := make(map[string]struct{}, len(changes))
	for _, change := range changes {
		if change.Field != "priority" {
			continue
		}
		if _, duplicate := keys[change.Subject.Key]; duplicate {
			t.Errorf("duplicate member subject key %q", change.Subject.Key)
		}
		keys[change.Subject.Key] = struct{}{}
	}
	if len(keys) != len(pairs) {
		t.Fatalf("member subject keys = %v, want %d unique keys", keys, len(pairs))
	}
	for _, pair := range pairs {
		want := strconv.Quote(pair.lag) + "/" + strconv.Quote(pair.member)
		if _, ok := keys[want]; !ok {
			t.Errorf("member subject keys = %v, want %q", keys, want)
		}
	}
}
