package assembly

import "go.aledante.io/FlowSeer/src/common/sim/fabric"

// Derive builds the target topology, retaining the layers each switch admits
// from its namesake in cur. An unset Start takes cur's current clock. Every
// built switch hears its links before its output and next wake are scheduled.
func Derive(cur *fabric.Fabric, target ConstructionSpec) (*fabric.Fabric, error) {
	if cur != nil && target.Start.IsZero() {
		target.Start = cur.Clock()
	}
	return build(cur, target)
}
