package snmpmap

import (
	"math"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// enumsFromBits maps a bitmap to an open enum, one value per set
// position, numbered as the registry that defines the bitmap numbers it.
// A position the schema does not name is kept as its own value: the agent
// said the bit is set, and only the name is missing. An empty bitmap
// yields nil.
func enumsFromBits[T ~int32](bits snmp.BitSet) []T {
	positions := bits.Positions()
	values := make([]T, 0, len(positions))

	for _, p := range positions {
		if p > math.MaxInt32 {
			continue
		}

		values = append(values, T(p))
	}

	if len(values) == 0 {
		return nil
	}

	return values
}
