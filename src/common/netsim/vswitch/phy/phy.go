// Package phy provides the physical layer of the virtual switch: per-port
// Ethernet speed resolution and Power over Ethernet budget allocation.
package phy

import (
	"slices"
)

// Config is the physical-layer configuration of a virtual switch, keyed by
// port name. A nil Ethernet map is the Ethernet capability absent; a nil PoE
// is the power-sourcing capability absent.
type Config struct {
	Ethernet map[string]Ethernet
	PoE      *PoE
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}
