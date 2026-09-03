// Package l2 provides L2 switching behaviors for the netpen runner. Register
// them with [Behaviors]. Callers must supply dependencies through the runner
// so catalog gates are evaluated before transmission.
//
// Active behaviors send bounded sequences with fixed fixture addresses and
// VLANs. Their findings describe attempted operations, without confirming a
// peer's state. Only DTP sends a restore frame; other registered restore
// callbacks perform no recovery. VLAN enumeration passively reads the leg.
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

// craftPool returns an owned copy of the serialized layers before recycling
// the serialization buffer. The result can be retained across calls.
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
