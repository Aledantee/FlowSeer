package epoch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// ErrCodeProbe identifies a failed or incomplete identity probe: the
// device answered but not with the shape the probe needs to build a
// fingerprint.
var ErrCodeProbe = errs.NewCode("epoch/probe")

// oidSysDescr and oidSysObjectID are the two SNMPv2-MIB identity objects
// the probe reads. Both are read in one request so a device that changes
// either without changing the other still yields a different fingerprint.
var (
	oidSysDescr    = snmp.MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	oidSysObjectID = snmp.MustOID(1, 3, 6, 1, 2, 1, 1, 2, 0)
)

// Probe reads a device's firmware fingerprint over SNMP, independent of any
// capability's own route evidence — the direction record's decision 7
// requires the probe to work even when every capability's evidence is
// stale, so it does not go through [interfaces.SelectRoute] or any other
// capability-scoped path. The fingerprint is opaque and stable only insofar
// as sysDescr and sysObjectID are: a firmware upgrade that changes either
// yields a new fingerprint. It is the hex SHA-256 of the two values, not
// their raw concatenation, since sysDescr is operator-controlled free text
// that can exceed the schema's fingerprint length limit.
func Probe(ctx context.Context, sess snmp.Session) (fingerprint string, err error) {
	vbs, err := sess.Get(ctx, []snmp.OID{oidSysDescr, oidSysObjectID})
	if err != nil {
		return "", errs.Wrap(err, "probe firmware identity")
	}

	if len(vbs) != 2 {
		return "", errs.New().Code(ErrCodeProbe).
			Msgf("identity probe expected 2 values, got %d", len(vbs))
	}

	descr, ok := octetString(vbs[0])
	if !ok {
		return "", errs.New().Code(ErrCodeProbe).Msg("sysDescr was not returned as an octet string")
	}

	objectID, ok := objectIdentifier(vbs[1])
	if !ok {
		return "", errs.New().Code(ErrCodeProbe).Msg("sysObjectID was not returned as an object identifier")
	}

	var b strings.Builder
	b.WriteString(descr)
	b.WriteByte('|')
	b.WriteString(objectID)

	sum := sha256.Sum256([]byte(b.String()))

	return hex.EncodeToString(sum[:]), nil
}

func octetString(vb snmp.VarBind) (string, bool) {
	v, ok := vb.(snmp.OctetStringVar)
	if !ok {
		return "", false
	}

	return string(v.Value), true
}

func objectIdentifier(vb snmp.VarBind) (string, bool) {
	v, ok := vb.(snmp.ObjectIDVar)
	if !ok {
		return "", false
	}

	return v.Value.String(), true
}
