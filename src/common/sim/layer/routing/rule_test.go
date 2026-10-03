package routing_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestRoutingRuleConstants(t *testing.T) {
	cases := []struct {
		name string
		got  trace.RuleID
		want trace.RuleID
	}{
		{name: "RuleStatusDown", got: routing.RuleStatusDown, want: "port.status.down"},
		{name: "RuleEgressNoMember", got: routing.RuleEgressNoMember, want: "lag.egress.no_member"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}

	if got, want := routing.RuleStatusPrefix, "port.status."; got != want {
		t.Errorf("RuleStatusPrefix = %q, want %q", got, want)
	}
}
