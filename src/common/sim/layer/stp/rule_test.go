package stp_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestRuleConstants(t *testing.T) {
	if got, want := stp.RuleStatusDown, trace.RuleID("port.status.down"); got != want {
		t.Errorf("RuleStatusDown = %q, want %q", got, want)
	}
}
