// Package l2 provides L2 switching behaviors for the netpen runner.
// [Behaviors] lists them for the shipped catalog. The runner supplies the
// legs, the target, the emitter, and the teardown handle and applies the
// catalog gates. Each behavior reads the VLANs, domain, and victim it acts
// on from the target and its own MAC from the attack leg. A value that only
// has to be well-formed, such as a flood frame's stamped MAC or an MVRP
// VLAN id, stays a constant in the behavior.
//
// Active behaviors send bounded sequences. Their findings describe
// attempted operations, without confirming a peer's state. DTP is the only
// behavior whose restore step sends a frame. The other armed restore steps
// perform no recovery. VLAN enumeration passively reads the leg.
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
// Legs consume but do not retain the sent buffer: afpacket's
// WritePacketData copies into the TX ring, and the test harness copies
// what it records. The slice therefore returns to the pool right after Send.
var packetPool = sync.Pool{
	New: func() any {
		b := make([]byte, 1600)
		return &b
	},
}
