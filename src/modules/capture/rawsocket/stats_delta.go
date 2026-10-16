package rawsocket

// counterPair is a snapshot of the two cumulative counters a BPF descriptor
// keeps, the shape of unix.BpfStat. The kernel declares both as u_int
// (golang.org/x/sys v0.48.0 unix/ztypes_freebsd_arm64.go, BpfStat), so they
// wrap at 2^32.
type counterPair struct {
	recv, drop uint32
}

// deltaSince returns deltas and the baseline for the next sample. FreeBSD
// releng/14.3 sys/net/bpf.c:1709-1710 reads recv before drop without a lock.
// A packet can increment recv after that fetch (lines 2326/2400/2474) and
// then drop before its fetch (lines 2595/2646). Defer drops beyond recv's
// delta so received cannot underflow and the next sample counts them once.
// Each counter's subtraction is modular to preserve independent uint32 wraps.
func (c counterPair) deltaSince(prev counterPair) (received, dropped uint64, next counterPair) {
	recvDelta := c.recv - prev.recv
	dropDelta := c.drop - prev.drop
	reportedDrop := min(dropDelta, recvDelta)
	return uint64(recvDelta - reportedDrop), uint64(reportedDrop), counterPair{
		recv: c.recv,
		drop: prev.drop + reportedDrop,
	}
}
