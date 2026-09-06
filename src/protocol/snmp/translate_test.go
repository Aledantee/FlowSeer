package snmp

import (
	"errors"
	"testing"

	"go.aledante.io/FlowSeer/src/common/secret"
)

func TestCompareOID(t *testing.T) {
	cases := []struct {
		a, b OID
		want int
	}{
		{MustOID(1, 3, 6), MustOID(1, 3, 6), 0},
		{MustOID(1, 3, 6), MustOID(1, 3, 7), -1},
		{MustOID(1, 3, 7), MustOID(1, 3, 6), 1},
		{MustOID(1, 3, 6), MustOID(1, 3, 6, 1), -1}, // prefix is less
		{MustOID(1, 3, 6, 1), MustOID(1, 3, 6), 1},
	}
	for _, c := range cases {
		if got := c.a.Compare(c.b); got != c.want {
			t.Errorf("compareOID(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPDUError(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 5, 0)
	t.Run("no error", func(t *testing.T) {
		if pe := pduError(&message{pdu: pdu{errorStatus: NoError}}); pe != nil {
			t.Fatalf("got %v, want nil", pe)
		}
	})
	t.Run("error with index OID", func(t *testing.T) {
		m := &message{pdu: pdu{
			errorStatus: NoSuchName,
			errorIndex:  1,
			varbinds: []VarBind{
				NullVar{Header: Header{OID: oid, Kind: KindNull}},
			},
		}}
		pe := pduError(m)
		if pe == nil || pe.Status != NoSuchName || !pe.OID.Equal(oid) {
			t.Fatalf("pe = %#v", pe)
		}
	})
	t.Run("out-of-range index leaves OID empty", func(t *testing.T) {
		m := &message{pdu: pdu{errorStatus: GenErr, errorIndex: 9}}
		pe := pduError(m)
		if pe == nil || pe.OID.Len() != 0 {
			t.Fatalf("pe = %#v", pe)
		}
	})
}

func TestValidateResponse(t *testing.T) {
	good := &message{version: V2c, community: "public"}
	if err := validateResponse(good, V2c, secret.NewString("public")); err != nil {
		t.Fatalf("matching response should validate: %v", err)
	}
	if err := validateResponse(&message{version: V1, community: "public"}, V2c, secret.NewString("public")); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("version mismatch err = %v", err)
	}
	if err := validateResponse(&message{version: V2c, community: "secret"}, V2c, secret.NewString("public")); !errors.Is(err, ErrCommunityMismatch) {
		t.Fatalf("community mismatch err = %v", err)
	}
	// The community-mismatch error must not leak the community value.
	err := validateResponse(&message{version: V2c, community: "topsecret"}, V2c, secret.NewString("public"))
	if err != nil && contains(err.Error(), "topsecret") {
		t.Fatalf("error text leaks community string: %q", err.Error())
	}
}

func TestNullVarbinds(t *testing.T) {
	oids := []OID{MustOID(1, 3, 6, 1), MustOID(1, 3, 6, 2)}
	vbs := nullVarbinds(oids)
	if len(vbs) != 2 {
		t.Fatalf("got %d varbinds, want 2", len(vbs))
	}
	for i, vb := range vbs {
		if _, ok := vb.(NullVar); !ok {
			t.Errorf("vb %d not NullVar: %#v", i, vb)
		}
		if !vb.GetHeader().OID.Equal(oids[i]) {
			t.Errorf("vb %d OID = %s, want %s", i, vb.GetHeader().OID, oids[i])
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
