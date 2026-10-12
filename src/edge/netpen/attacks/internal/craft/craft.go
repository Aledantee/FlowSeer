// Package craft holds the serialize path shared by netpen's attack behavior
// packages (attacks/ip6, attacks/l2, attacks/routing, attacks/fh), so the
// serialize options stay identical across every attack family.
package craft

import (
	"github.com/gopacket/gopacket"
)

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
