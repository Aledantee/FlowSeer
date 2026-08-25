// Package l2 holds netpen's L2 switching attack behaviors: dtp, doubletag,
// vlanenum, vlanhop, voicevlan, stproot, camflood, vtp, mvrp, portsteal, and
// the EtherChannel (LACP/PAgP) superset. Each behavior is a thin
// [runner.Behavior] over the protocol toolkit — leg sends, decoder reads,
// findings emission — with durability duties sourced from the catalog, not
// per-command code.
//
// Flood-class behaviors (camflood, stproot, mvrp bursts) use the shared
// pool-craft path ([craftPool]) for pre-serialized buffers; the
// default craft path uses SerializeLayers+ComputeChecksums+FixLengths.
package l2

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

// craftPool serializes the given layers into a fresh buffer from the pool
// and returns the bytes. The caller must copy the result before returning
// the buffer. This is the flood-class craft path: pre-serialized
// buffers through sync.Pool so burst sends are allocation-free.
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

// packetPool is a sync.Pool of []byte stamp buffers for the true
// zero-allocation flood shape: serialize a frame template once, copy it
// into a pooled slice per packet, mutate the stamped field(s), and send.
// Legs consume but do not retain the sent buffer (afpacket's
// WritePacketData copies into the TX ring; the test harness copies on
// record), so the slice returns to the pool immediately after Send.
var packetPool = sync.Pool{
	New: func() any {
		b := make([]byte, 1600)
		return &b
	},
}

// craftDefault serializes layers with the default craft path:
// SerializeLayers + ComputeChecksums + FixLengths. Used by non-flood
// behaviors.
func craftDefault(layers ...gopacket.SerializableLayer) ([]byte, error) {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, layers...); err != nil {
		return nil, err
	}
	return append([]byte(nil), buf.Bytes()...), nil
}
