package port_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// TestDiffCoversEveryConfigField verifies that every exported port.Port field reaches
// port.Diff. port has no Config; Diff takes a Table, whose fields are
// unexported and built only through NewBuilder, so this walks port.Port directly and the
// companion LAG port lets LagParent's toggle-to-"" perturbation still resolve to a real
// port. Name is exempt: it is the key Diff matches ports across tables by, so renaming
// it reads as removing one port and adding another, not as a changed field.
func TestDiffCoversEveryConfigField(t *testing.T) {
	seed := port.Port{
		Name: "1/1/1",
		// Other, not the zero-value Physical default: perturbing to "" must read as
		// a real change, not normalize back to the seed's own value.
		IfIndex:     1,
		Kind:        port.Other,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
		MTU:         1500,
		LagParent:   "lag1",
	}
	companions := []port.Port{
		{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up},
	}
	exemptions := map[string]string{
		".Name": "Name is the key Diff matches ports across tables by; renaming reads as removing one port and adding another",
	}

	simtest.AssertDiffCoversPort(t, seed, companions, exemptions)
}
