package host

// LaneReporterForTest exposes the adapter from what the lane reports to what
// central is owed. It is the host's own wiring rather than an exported API:
// the lane is given one of these and nothing else ever holds it.
func LaneReporterForTest(out Outbound) laneReporter { return laneReporter{out: out} } //nolint:revive // the test needs the concrete adapter, and it is this package's own type
