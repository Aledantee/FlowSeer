package smi_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/smi"
)

// Two vendored IEEE MIBs used to carry a local patch commenting out an
// empty-BITS DEFVAL, because the previous parser aborted the build on
// it. RFC 2578 §7.9 spells that construct out as the way to say no bit
// is set, so the patch was a workaround for a defect rather than a fix
// for the source, and every upstream re-sync had to re-apply it by hand.
//
// The patches are gone. This test is what keeps them gone: it reads the
// two MIBs as they ship and asserts the clause survives the whole way
// into the model, so a regression that made the clause expensive again
// fails here instead of tempting somebody to edit the vendored source.
func TestVendoredEmptyBitsDefaultsResolve(t *testing.T) {
	cases := []struct {
		module string
		node   string
	}{
		{"LLDP-MIB", "lldpPortConfigTLVsTxEnable"},
		{"LLDP-EXT-DOT3-MIB", "lldpXdot3PortConfigTLVsTxEnable"},
	}

	for _, tc := range cases {
		t.Run(tc.module, func(t *testing.T) {
			set, err := smi.Load([]string{tc.module}, smi.Options{
				SearchPaths: corpusSearchPaths(t),
			})
			if err != nil {
				t.Fatalf("loading %s: %v", tc.module, err)
			}

			mod, ok := set.Module(tc.module)
			if !ok {
				t.Fatalf("%s did not resolve into the set", tc.module)
			}

			node, ok := mod.Node(tc.node)
			if !ok {
				t.Fatalf("%s declares no %s", tc.module, tc.node)
			}
			if node.Unresolved {
				t.Fatalf("%s is unresolved", tc.node)
			}
			if node.Default.Kind != smi.DefaultBits {
				t.Fatalf("%s default kind = %v, want %v",
					tc.node, node.Default.Kind, smi.DefaultBits)
			}
			if len(node.Default.Bits) != 0 {
				t.Errorf("%s default names bits %v, want the empty set",
					tc.node, node.Default.Bits)
			}
		})
	}
}
