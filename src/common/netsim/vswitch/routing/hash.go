package routing

import (
	"encoding/binary"
	"hash/fnv"
	"net/netip"
)

// flowHash returns the FNV-1a hash of one packet's layer-3 flow: the source address bytes,
// the destination address bytes, and for IPv6 the flow label in big-endian order.
//
// Nothing else enters, because that is what a router nobody configured hashes. Cisco CEF's
// default per-destination load sharing takes the address pair alone, and Linux's default
// multipath hash policy is layer 3 with the IPv6 flow label, which RFC 6437 put there for
// exactly this use. Leaving the transport ports out keeps every fragment of one datagram on
// one next hop with no fragment rule to write, and leaves out the protocol octet, which for
// IPv6 names the first extension header rather than the transport protocol.
func flowHash(src, dst netip.Addr, flowLabel uint32) uint32 {
	h := fnv.New32a()
	h.Write(src.AsSlice())
	h.Write(dst.AsSlice())
	if dst.Is6() {
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], flowLabel)
		h.Write(buf[:])
	}
	return h.Sum32()
}

// hashThreshold returns the index into a candidate set of n members whose region holds hash.
// The 32-bit hash space is cut into n contiguous regions of equal width in the set's
// canonical order, and the last region absorbs the remainder (RFC 2992).
//
// The reduction is not hash modulo n: RFC 2992 measures modulo-n as the most disruptive of
// the algorithms, moving (N-1)/N of the flows when a next hop comes or goes, against
// between 1/4 and 1/2 for hash-threshold. Under hash-threshold a flow only ever slides into
// the region below it, so "which flows move when this path fails" has a bounded answer.
func hashThreshold(hash uint32, n int) int {
	width := uint64(1<<32) / uint64(n)
	region := int(uint64(hash) / width)
	if region >= n {
		region = n - 1
	}
	return region
}

// selection is one route lookup's answer: the equal-cost candidates in canonical order, the
// layer-3 fields the flow hash was taken over, and the candidate that hash chose.
type selection struct {
	candidates []routeEntry
	src        netip.Addr
	dst        netip.Addr
	flowLabel  uint32
	hash       uint32
	chosen     int
}

// selectRoute reduces the candidate run a table lookup returned to the single route this
// flow takes, or returns nil when the lookup matched nothing. It reads only the packet, so
// two calls for one flow answer alike and neither leaves a trace behind.
func selectRoute(candidates []routeEntry, src, dst netip.Addr, flowLabel uint32) *selection {
	if len(candidates) == 0 {
		return nil
	}
	hash := flowHash(src, dst, flowLabel)
	return &selection{
		candidates: candidates,
		src:        src,
		dst:        dst,
		flowLabel:  flowLabel,
		hash:       hash,
		chosen:     hashThreshold(hash, len(candidates)),
	}
}

// route returns the chosen candidate.
func (s *selection) route() *routeEntry {
	return &s.candidates[s.chosen]
}
