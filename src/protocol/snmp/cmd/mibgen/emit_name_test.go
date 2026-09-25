package main

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// TestRenderModuleRejectsGoNameCollision proves the name pre-pass is wired
// into generation: two SMI names in one module that map to the same Go
// identifier (an initialism spelled two ways) fail renderModule with both
// SMI names in the error, and no output is produced.
func TestRenderModuleRejectsGoNameCollision(t *testing.T) {
	mod := &smi.Module{
		Name: "COLLIDE-MIB",
		Nodes: []*smi.Node{
			{Name: "fooId", Module: "COLLIDE-MIB", Kind: smi.NodeScalar, OID: smi.NewOID(1, 3, 6, 1, 4, 1, 1)},
			{Name: "fooID", Module: "COLLIDE-MIB", Kind: smi.NodeScalar, OID: smi.NewOID(1, 3, 6, 1, 4, 1, 2)},
		},
	}
	set := &smi.ModuleSet{}

	out, _, err := renderModule(mod, set, Module{Name: "COLLIDE-MIB", Package: "collidemib"}, nil, "example.test/gen")
	if err == nil {
		t.Fatal("renderModule over two names mapping to FooID returned nil error")
	}
	for _, name := range []string{"fooId", "fooID"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name SMI name %q", err, name)
		}
	}
	if len(out) != 0 {
		t.Errorf("renderModule returned %d bytes of output on failure, want none", len(out))
	}
}

// TestEmit_InitialismNames pins goname's initialism table on the generated
// bindings: lldpRemChassisId renders as LLDPRemChassisID, and the
// lldpPortConfigTable descriptor singleton stays lldpPortConfigTableT rather
// than lower-casing only the first rune of LLDP.
func TestEmit_InitialismNames(t *testing.T) {
	src := renderConfigured(t, "LLDP-MIB")
	wantFragments(t, src,
		"var LLDPRemChassisID =",
		"func (lldpPortConfigTableT) Descriptor() snmp.TableDescriptor",
	)
	rejectFragments(t, src, "lLDPPortConfigTableT")
}
