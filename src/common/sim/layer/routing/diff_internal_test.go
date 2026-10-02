package routing

import (
	"fmt"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestFactTypeIDsUnique(t *testing.T) {
	t.Parallel()

	facts := []trace.Fact{
		Route{},
		Neighbor{},
		vlanFact(0),
		portFact(""),
		macFact{},
		prefixesFact(""),
		addrFact{},
		routeInterfaceFact(""),
		routePreferenceFact(0),
		routeMetricFact(0),
		neighborModeFact(""),
		reachableTimeFact(0),
		resolutionTimeoutFact(0),
		holdDepthFact(0),
		packetDecisionFact(""),
		routeDecisionFact(""),
		neighborDecisionFact(""),
		egressDecisionFact(""),
		interfaceSnapshotFact(""),
		vrfSnapshotFact(""),
	}

	seen := make(map[string]string)
	for _, f := range facts {
		tid := f.TypeID()
		if tid == "" {
			t.Errorf("fact %T has empty TypeID", f)
		}
		if !strings.HasPrefix(tid, "routing.") {
			t.Errorf("fact %T TypeID %q must be prefixed with 'routing.'", f, tid)
		}
		if prev, ok := seen[tid]; ok {
			t.Errorf("duplicate TypeID %q shared by %s and %T", tid, prev, f)
		}
		seen[tid] = fmt.Sprintf("%T", f)
	}
}
