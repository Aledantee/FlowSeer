package rawsocket

// counterPair is a snapshot of the two cumulative counters a BPF descriptor
// keeps, the shape of unix.BpfStat. The kernel declares both as u_int
// (golang.org/x/sys v0.48.0 unix/ztypes_freebsd_arm64.go, BpfStat), so they
// wrap at 2^32.
type counterPair struct {
	recv, drop uint32
}

// deltaSince reports what the descriptor counted between prev and c. The
// kernel's recv counts every packet that reached the filter, drops included,
// so received excludes the dropped. Subtraction is modular, which keeps the
// delta true across a wrap of either counter.
func (c counterPair) deltaSince(prev counterPair) (received, dropped uint64) {
	recvDelta := c.recv - prev.recv
	dropDelta := c.drop - prev.drop
	return uint64(recvDelta - dropDelta), uint64(dropDelta)
}
