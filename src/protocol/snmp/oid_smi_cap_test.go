package snmp

import (
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// The MIB parser writes the same OID length cap as a literal of its own
// rather than importing it, because package smi imports nothing from
// this package: the dependency has to be able to run the other way so a
// model-driven decode path here can read a parsed MIB. This test is the
// other half of that deliberate duplication, and it lives on this side
// because maxOIDComponents is unexported and reading it from smi would
// need either the forbidden import or a public API this work does not
// own.
//
// If the runtime's cap ever changes, change [smi.MaxOIDLength] with it:
// an OID the parser resolves has to be one this package will carry.
func TestSMIOIDLengthCapMatchesRuntimeCap(t *testing.T) {
	if smi.MaxOIDLength != maxOIDComponents {
		t.Errorf("smi.MaxOIDLength = %d but maxOIDComponents = %d; "+
			"the parser's duplicated literal has drifted from the cap it copies",
			smi.MaxOIDLength, maxOIDComponents)
	}
}
