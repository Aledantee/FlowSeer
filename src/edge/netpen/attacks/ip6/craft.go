// craft.go holds the flood-class craft path for ip6 behaviors.
// Flood-class behaviors (dhcpstarve, raflood, mld bursts) use the
// shared pool-craft path for pre-serialized buffers; the
// default craft path uses SerializeLayers+ComputeChecksums+FixLengths.

package ip6

import (
	"sync"

	"github.com/gopacket/gopacket"
)

// poolBuf is a sync.Pool of reusable serialize buffers for flood-class
// craft. The pool keeps per-packet allocation off the GC's back
// during burst sends — the syscall bounds throughput, but buffer
// allocation is what the GC pays.
var poolBuf = sync.Pool{
	New: func() any {
		return gopacket.NewSerializeBuffer()
	},
}

// getBuf acquires a serialize buffer from the pool.
func getBuf() gopacket.SerializeBuffer {
	return poolBuf.Get().(gopacket.SerializeBuffer)
}

// putBuf returns a serialize buffer to the pool, clearing it first so
// the next acquirer starts fresh.
func putBuf(b gopacket.SerializeBuffer) {
	_ = b.Clear()
	poolBuf.Put(b)
}

// craftPool serializes the given layers into a fresh buffer from the
// pool and returns the bytes. The caller must copy the result before
// returning the buffer. This is the flood-class craft path:
// pre-serialized buffers through sync.Pool so burst sends are
// allocation-free.
func craftPool(layers ...gopacket.SerializableLayer) ([]byte, error) {
	buf := getBuf()
	defer putBuf(buf)

	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, layers...); err != nil {
		return nil, err
	}
	return append([]byte(nil), buf.Bytes()...), nil
}
