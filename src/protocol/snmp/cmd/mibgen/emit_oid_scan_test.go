package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// TestValidateEmittedOID checks the pre-pass guard directly: a well-formed OID
// passes, and each SMIv2 root or length rule snmp.NewOID enforces — but
// smi.OID.String does not — is rejected. These are the malformed trees the
// pre-pass must fail generation on rather than emitting a MustOID that panics
// at the generated package's init.
func TestValidateEmittedOID(t *testing.T) {
	cases := []struct {
		name    string
		oid     string
		wantErr bool
	}{
		{"empty is the zero OID", "", false},
		{"well formed", "1.3.6.1.4.1", false},
		{"first arc above 2", "3.1", true},
		{"second arc past 39 under arc 1", "1.40.1", true},
		{"129 sub-identifiers", longOID(129), true},
		{"128 sub-identifiers", longOID(128), false},
		{"non-numeric sub-identifier", "1.3.six", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateEmittedOID(c.oid)
			if c.wantErr && err == nil {
				t.Fatalf("validateEmittedOID(%q) = nil, want an error", c.oid)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("validateEmittedOID(%q) = %v, want nil", c.oid, err)
			}
		})
	}
}

// longOID returns "1.3" padded with 1s to n sub-identifiers.
func longOID(n int) string {
	s := "1"
	for i := 1; i < n; i++ {
		s += ".1"
	}

	return s
}

// TestRenderModuleRejectsInvalidOID proves the pre-pass is wired into
// generation: a resolved module carrying a node whose first arc is 3 fails
// renderModule with the node named, and no output is produced.
func TestRenderModuleRejectsInvalidOID(t *testing.T) {
	mod := &smi.Module{
		Name: "BAD-OID-MIB",
		Nodes: []*smi.Node{
			{Name: "badRoot", Module: "BAD-OID-MIB", Kind: smi.NodeNode, OID: smi.NewOID(3, 1)},
		},
	}
	set := &smi.ModuleSet{}

	out, _, err := renderModule(mod, set, Module{Name: "BAD-OID-MIB", Package: "badoidmib"}, nil, "example.test/gen")
	if err == nil {
		t.Fatal("renderModule over a first-arc-3 OID returned nil error")
	}
	if !strings.Contains(err.Error(), "badRoot") || !strings.Contains(err.Error(), "first sub-identifier") {
		t.Errorf("error %q is not the OID pre-pass rejection naming the node", err)
	}
	if len(out) != 0 {
		t.Errorf("renderModule returned %d bytes of output on failure, want none", len(out))
	}
}

// TestGeneratedMustOIDLiteralsAreValid is the clause-2 proof over the
// artifacts, not the generator: it reads every committed golden mib.go,
// extracts each snmp.MustOID(...) literal, and feeds the sub-identifiers to
// snmp.NewOID. A drift that let an invalid OID reach a generated file fails
// here on every go test, which the generator alone does not guarantee.
func TestGeneratedMustOIDLiteralsAreValid(t *testing.T) {
	root := filepath.Join("testdata", "golden")
	seen := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Base(path) != "mib.go" {
			return nil
		}
		seen += scanMustOIDLiterals(t, path)

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if seen == 0 {
		t.Fatalf("no MustOID literals found under %s; the scan matched nothing", root)
	}
}

// scanMustOIDLiterals parses one generated file, finds every snmp.MustOID call
// with integer-literal arguments, and asserts snmp.NewOID accepts them.
// Returns the number of calls checked.
func scanMustOIDLiterals(t *testing.T, path string) int {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	count := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "MustOID" {
			return true
		}

		subs := make([]uint32, 0, len(call.Args))
		for _, arg := range call.Args {
			lit, ok := arg.(*ast.BasicLit)
			if !ok || lit.Kind != token.INT {
				// A non-literal argument is not a generated OID
				// literal; skip this call rather than guess.
				return true
			}
			v, err := strconv.ParseUint(lit.Value, 0, 32)
			if err != nil {
				t.Errorf("%s: MustOID argument %q does not parse as uint32: %v", path, lit.Value, err)
				return true
			}
			subs = append(subs, uint32(v))
		}

		if _, err := snmp.NewOID(subs...); err != nil {
			t.Errorf("%s: generated MustOID(%v) is not a valid OID: %v", path, subs, err)
		}
		count++

		return true
	})

	return count
}
