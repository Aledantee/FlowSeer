// Package craft holds the packet-crafting fixtures shared by netpen's attack
// behavior packages (attacks/ip6, attacks/l2, attacks/routing, attacks/fh).
// It owns the default serialize path and the fixed fixture source MAC those
// packages build frames with, so the value and the serialize options stay
// identical across every attack family.
package craft

import (
	"net"

	"github.com/gopacket/gopacket"
)

// FixtureSrcMAC is the fixed source MAC the attack behaviors and their fixture
// generators build frames with. Behaviors do not derive it from the attack
// leg. Callers must not mutate it concurrently with a behavior run.
var FixtureSrcMAC = net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

// Default serializes layers with the default craft path: SerializeLayers with
// ComputeChecksums and FixLengths enabled. It returns an owned copy of the
// serialized bytes that the caller may retain. Used by non-flood behaviors;
// flood behaviors use their package-local pooled craft path instead.
func Default(serializable ...gopacket.SerializableLayer) ([]byte, error) {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, serializable...); err != nil {
		return nil, err
	}
	return append([]byte(nil), buf.Bytes()...), nil
}
