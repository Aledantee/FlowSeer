package fabric

// Derive builds the target topology, retaining the layers each switch admits
// from its namesake in cur. An unset Start takes cur's current clock. Every
// built device hears its links before its output and next wake are scheduled.
func Derive(cur *Fabric, target ConstructionSpec) (*Fabric, error) {
	if cur != nil && target.Start.IsZero() {
		target.Start = cur.clock
	}
	return build(cur, target)
}
