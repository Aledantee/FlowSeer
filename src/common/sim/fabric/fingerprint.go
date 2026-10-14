package fabric

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
)

func escapeFingerprint(s string) string {
	return strings.NewReplacer(
		"%", "%25",
		":", "%3A",
		";", "%3B",
		",", "%2C",
		"=", "%3D",
		"|", "%7C",
		"/", "%2F",
		"[", "%5B",
		"]", "%5D",
		"{", "%7B",
		"}", "%7D",
	).Replace(s)
}

// Fingerprint returns a canonical string over the protocol-relevant state of
// the fabric snapshot.
func (f *Fabric) Fingerprint() string {
	return f.Snapshot().Fingerprint()
}

// Fingerprint returns a canonical string over the protocol-relevant state of
// the snapshot, deliberately excluding timers, arrival queues, and counters
// so periodic protocol wakes do not prevent convergence detection.
func (s Snapshot) Fingerprint() string {
	var b strings.Builder

	// Devices in name order.
	devNames := slices.Sorted(maps.Keys(s.Devices))
	for _, devName := range devNames {
		dev := s.Devices[devName]
		b.WriteString("dev:")
		b.WriteString(escapeFingerprint(devName))
		b.WriteString("|")

		b.WriteString(dev.Fingerprint())
	}

	// Links in canonical endpoint order.
	b.WriteString("links:[")
	links := slices.Clone(s.Links)
	slices.SortFunc(links, func(a, b Link) int {
		keyA := cableEndpointsFor(a.Cable).Canonical()
		keyB := cableEndpointsFor(b.Cable).Canonical()
		return cmp.Compare(keyA, keyB)
	})
	for i, l := range links {
		if i > 0 {
			b.WriteString(";")
		}
		c := canonicalCableOrientation(l.Cable)
		endA, endB := l.A, l.B
		if compareEndpoint(l.Cable.A, c.A) != 0 {
			endA, endB = l.B, l.A
		}
		b.WriteString(cableEndpointsFor(c).Canonical())
		b.WriteString("={")
		b.WriteString("a=")
		b.WriteString(escapeFingerprint(string(endA.Oper)))
		b.WriteString(",")
		b.WriteString(escapeFingerprint(string(endA.Reason)))
		b.WriteString(",")
		b.WriteString(strconv.FormatUint(endA.Speed.SpeedBPS, 10))
		b.WriteString(",")
		b.WriteString(escapeFingerprint(string(endA.Speed.DuplexA)))
		b.WriteString(";b=")
		b.WriteString(escapeFingerprint(string(endB.Oper)))
		b.WriteString(",")
		b.WriteString(escapeFingerprint(string(endB.Reason)))
		b.WriteString(",")
		b.WriteString(strconv.FormatUint(endB.Speed.SpeedBPS, 10))
		b.WriteString(",")
		b.WriteString(escapeFingerprint(string(endB.Speed.DuplexA)))
		b.WriteString(";fault=")
		b.WriteString(escapeFingerprint(string(c.Fault.Kind)))
		b.WriteString(";medium=")
		b.WriteString(escapeFingerprint(string(c.Medium)))
		b.WriteString("}")
	}
	b.WriteString("]")

	return b.String()
}
