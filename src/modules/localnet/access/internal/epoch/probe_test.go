package epoch_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/epoch"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// identitySession answers only the two identity OIDs the probe reads,
// proving the probe needs nothing else — no interface table, no walk — to
// stay route-independent of any capability that might also be unreachable.
type identitySession struct {
	descr    string
	objectID snmp.OID
	err      error
}

func (s *identitySession) Get(_ context.Context, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	if s.err != nil {
		return nil, s.err
	}

	out := make([]snmp.VarBind, 0, len(oids))
	for _, o := range oids {
		switch {
		case o.Compare(snmp.MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)) == 0:
			out = append(out, snmp.OctetStringVar{Header: snmp.Header{OID: o, Kind: snmp.KindOctetString}, Value: []byte(s.descr)})
		case o.Compare(snmp.MustOID(1, 3, 6, 1, 2, 1, 1, 2, 0)) == 0:
			out = append(out, snmp.ObjectIDVar{Header: snmp.Header{OID: o, Kind: snmp.KindObjectID}, Value: s.objectID})
		default:
			out = append(out, snmp.NoSuchObjectVar{Header: snmp.Header{OID: o, Kind: snmp.KindNoSuchObject}})
		}
	}

	return out, nil
}

func (s *identitySession) GetNext(context.Context, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	panic("not used by the identity probe")
}

func (s *identitySession) GetBulk(context.Context, uint8, uint8, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	panic("not used by the identity probe")
}

func (s *identitySession) Set(context.Context, []snmp.VarBind, ...snmp.CallOption) ([]snmp.VarBind, error) {
	panic("not used by the identity probe")
}

func (s *identitySession) Close() error { return nil }

func (s *identitySession) Walk(context.Context, snmp.OID, ...snmp.CallOption) *snmp.Walker {
	panic("not used by the identity probe")
}

func (s *identitySession) BulkWalk(context.Context, snmp.OID, ...snmp.CallOption) *snmp.Walker {
	panic("not used by the identity probe")
}

func (s *identitySession) BulkWalkRaw(context.Context, snmp.OID, ...snmp.CallOption) *snmp.RawWalker {
	panic("not used by the identity probe")
}

func TestProbeCombinesSysDescrAndSysObjectID(t *testing.T) {
	sess := &identitySession{descr: "ICX7150 10.0.10g", objectID: snmp.MustOID(1, 3, 6, 1, 4, 1, 1991, 1, 3, 1)}

	fp, err := epoch.Probe(context.Background(), sess)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if fp == "" {
		t.Fatal("expected a non-empty fingerprint")
	}
}

// TestProbeFingerprintNeverExceedsSchemaBound proves the fingerprint stays
// bounded even when a device's sysDescr is long operator-controlled free
// text, since the schema caps the fingerprint field at 128 characters.
func TestProbeFingerprintNeverExceedsSchemaBound(t *testing.T) {
	const schemaMaxLen = 128

	longDescr := strings.Repeat("Ruckus ICX7150-24P Switch with a very verbose vendor banner string ", 20)
	sess := &identitySession{descr: longDescr, objectID: snmp.MustOID(1, 3, 6, 1, 4, 1, 1991, 1, 3, 1)}

	fp, err := epoch.Probe(context.Background(), sess)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(fp) > schemaMaxLen {
		t.Fatalf("expected fingerprint to stay within the schema's %d-char bound, got %d chars: %q", schemaMaxLen, len(fp), fp)
	}
}

func TestProbeChangesWhenEitherIdentityObjectChanges(t *testing.T) {
	base := &identitySession{descr: "ICX7150 10.0.10g", objectID: snmp.MustOID(1, 3, 6, 1, 4, 1, 1991, 1, 3, 1)}
	changedDescr := &identitySession{descr: "ICX7150 10.0.20a", objectID: base.objectID}
	changedObjectID := &identitySession{descr: base.descr, objectID: snmp.MustOID(1, 3, 6, 1, 4, 1, 1991, 1, 3, 2)}

	fpBase, err := epoch.Probe(context.Background(), base)
	if err != nil {
		t.Fatalf("Probe(base): %v", err)
	}
	fpDescr, err := epoch.Probe(context.Background(), changedDescr)
	if err != nil {
		t.Fatalf("Probe(changedDescr): %v", err)
	}
	fpObjectID, err := epoch.Probe(context.Background(), changedObjectID)
	if err != nil {
		t.Fatalf("Probe(changedObjectID): %v", err)
	}

	if fpBase == fpDescr {
		t.Fatal("expected a sysDescr change to change the fingerprint")
	}
	if fpBase == fpObjectID {
		t.Fatal("expected a sysObjectID change to change the fingerprint")
	}
}

func TestProbeWrapsSessionError(t *testing.T) {
	sess := &identitySession{err: errors.New("timeout")}

	_, err := epoch.Probe(context.Background(), sess)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestProbeRejectsWrongShape(t *testing.T) {
	badShape := &shapeMismatchSession{}
	_, err := epoch.Probe(context.Background(), badShape)
	if err == nil {
		t.Fatal("expected an error for a malformed identity response")
	}
	if code, _ := errs.CodeOf(err); code != epoch.ErrCodeProbe {
		t.Fatalf("expected ErrCodeProbe, got %v", err)
	}
}

// shapeMismatchSession answers both identity OIDs as octet strings, which
// is wrong for sysObjectID, to prove the probe validates variant kinds
// rather than trusting position alone.
type shapeMismatchSession struct{ identitySession }

func (s *shapeMismatchSession) Get(_ context.Context, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	out := make([]snmp.VarBind, 0, len(oids))
	for _, o := range oids {
		out = append(out, snmp.OctetStringVar{Header: snmp.Header{OID: o, Kind: snmp.KindOctetString}, Value: []byte("x")})
	}

	return out, nil
}
