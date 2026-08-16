package snmp_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

// TestNewColumn_ForeignPackage exercises the bug NewColumn exists to
// prevent: a third-party (generated, test, application) package must be
// able to construct a Column[T] without reaching into unexported fields.
// If this stops compiling, the exported-constructor invariant has
// regressed.
func TestNewColumn_ForeignPackage(t *testing.T) {
	oid, err := snmp.ParseOID("1.3.6.1.2.1.2.2.1.2")
	if err != nil {
		t.Fatalf("ParseOID: %v", err)
	}
	dec := func(vb snmp.VarBind) (string, error) {
		os, ok := vb.(snmp.OctetStringVar)
		if !ok {
			return "", snmp.ErrTypeMismatch
		}
		return string(os.Value), nil
	}
	c := snmp.NewColumn[string](oid, snmp.KindOctetString, dec)
	if !c.OID().Equal(oid) {
		t.Errorf("OID() mismatch: %q vs %q", c.OID(), oid)
	}
	if c.Kind() != snmp.KindOctetString {
		t.Errorf("Kind() = %v, want KindOctetString", c.Kind())
	}
	// AnyColumn assertion: variadic Walk signatures depend on this.
	var _ snmp.AnyColumn = c
}
