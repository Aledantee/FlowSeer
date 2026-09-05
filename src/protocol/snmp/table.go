package snmp

import (
	"context"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// TableDescriptor identifies one MIB table without reference to its row
// or walker types, so a consumer can hold tables from many generated
// packages in one slice, probe them, and declare them as dependencies.
// Generated bindings populate it; callers treat it as a value.
type TableDescriptor struct {
	// Root is the table's OID (the node above the entry).
	Root OID
	// Indicator is the change indicator a Watcher would poll for the
	// table, or the zero value when the MIB declares none; see
	// [TableDescriptor.HasIndicator].
	Indicator ChangeIndicator
	// KeyType is the Go type name of the row key in the generated
	// package, for diagnostics and dependency reporting.
	KeyType string
}

// HasIndicator reports whether the table has a change indicator.
func (d TableDescriptor) HasIndicator() bool { return !d.Indicator.isZero() }

// Present issues one GetNext at the table root and reports whether the
// agent answered with an instance under it. This is an instance probe,
// not a support probe: an agent that implements the table but currently
// holds no rows reads as absent, as does one that answers outside the
// root or with an end-of-MIB marker. A transport failure or a reply
// without a VarBind is returned as an error.
func (d TableDescriptor) Present(ctx context.Context, sess Session) (bool, error) {
	vbs, err := sess.GetNext(ctx, []OID{d.Root})
	if err != nil {
		return false, err
	}
	if len(vbs) == 0 || vbs[0] == nil {
		return false, errs.New().Attr("root", d.Root).Msg("table presence probe received no varbind")
	}
	if IsException(vbs[0]) {
		return false, nil
	}
	oid := vbs[0].GetHeader().OID
	return oid.Len() > d.Root.Len() && oid.HasPrefix(d.Root), nil
}
