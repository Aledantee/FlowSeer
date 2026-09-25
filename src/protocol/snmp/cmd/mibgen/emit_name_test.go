package main

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// TestRenderModuleRejectsGoNameCollision proves the name pre-pass is wired
// into generation: two scalar names in one module that emit the same Go
// accessor (an initialism spelled two ways) fail renderModule with both SMI
// names in the error, and no output is produced.
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
		t.Fatal("renderModule over two names emitting FooIDGet returned nil error")
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

func TestCheckGoNameUniquenessRejectsDerivedIdentifierCollisions(t *testing.T) {
	tests := []struct {
		name string
		mod  *smi.Module
		want []string
	}{
		{
			name: "scalar accessor and column",
			mod: func() *smi.Module {
				table := &smi.Node{Name: "things", Kind: smi.NodeTable}
				row := &smi.Node{Name: "thingEntry", Kind: smi.NodeRow}
				column := &smi.Node{Name: "fooGet", Kind: smi.NodeColumn, Access: smi.AccessReadOnly}
				return &smi.Module{
					Name:   "COLLIDE-MIB",
					Nodes:  []*smi.Node{{Name: "foo", Kind: smi.NodeScalar}, table, row, column},
					Tables: []*smi.Table{{Node: table, Row: row, Columns: []*smi.Node{column}}},
				}
			}(),
			want: []string{"foo", "fooGet", "FooGet"},
		},
		{
			name: "enum members",
			mod: &smi.Module{
				Name: "COLLIDE-MIB",
				Types: []*smi.Type{{
					Name: "mode", Base: smi.BaseInteger,
					Members: []smi.Member{{Name: "fooId"}, {Name: "fooID"}},
				}},
			},
			want: []string{"fooId", "fooID", "ModeFooID"},
		},
		{
			name: "BITS members",
			mod: &smi.Module{
				Name: "COLLIDE-MIB",
				Types: []*smi.Type{{
					Name: "flags", Base: smi.BaseBits,
					Members: []smi.Member{{Name: "fooId"}, {Name: "fooID"}},
				}},
			},
			want: []string{"fooId", "fooID", "FlagsFooID"},
		},
		{
			name: "BITS type prefixes",
			mod: &smi.Module{
				Name: "COLLIDE-MIB",
				Types: []*smi.Type{
					{Name: "fooId", Base: smi.BaseBits, Members: []smi.Member{{Name: "x"}}},
					{Name: "fooID", Base: smi.BaseBits, Members: []smi.Member{{Name: "x"}}},
				},
			},
			want: []string{"fooId.x", "fooID.x", "FooIDX"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := newEmitCtx(tt.mod, &smi.ModuleSet{}, Module{Name: tt.mod.Name}, nil, "")
			err := checkGoNameUniqueness(ec)
			if err == nil {
				t.Fatal("checkGoNameUniqueness returned nil error")
			}
			for _, fragment := range tt.want {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("error %q does not contain %q", err, fragment)
				}
			}
		})
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
