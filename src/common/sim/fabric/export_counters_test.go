package fabric

// ExportPortCounters exposes a port's counters, which Snapshot reports only for switches.
func ExportPortCounters(f *Fabric, node, port string) Counters {
	c := f.counters[Endpoint{Node: node, Port: port}]
	if c == nil {
		return Counters{}
	}
	return c.Clone()
}
