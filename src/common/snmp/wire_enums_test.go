package snmp

import (
	"net"
	"testing"
)

// TestPDUErrorStatus_NumericValues pins each PDUErrorStatus constant to
// its RFC 3416 §3 numeric code AND its canonical SMI name. A reorder of
// the iota block in errors.go fails this test rather than silently
// shifting wire-protocol semantics for the Backend translation layer.
//
// Covers conformance matrix row: RFC 3416 §3 / PDUErrorStatus.
func TestPDUErrorStatus_NumericValues(t *testing.T) {
	cases := []struct {
		c    PDUErrorStatus
		n    int
		name string
	}{
		{NoError, 0, "noError"},
		{TooBig, 1, "tooBig"},
		{NoSuchName, 2, "noSuchName"},
		{BadValue, 3, "badValue"},
		{ReadOnly, 4, "readOnly"},
		{GenErr, 5, "genErr"},
		{NoAccess, 6, "noAccess"},
		{WrongType, 7, "wrongType"},
		{WrongLength, 8, "wrongLength"},
		{WrongEncoding, 9, "wrongEncoding"},
		{WrongValue, 10, "wrongValue"},
		{NoCreation, 11, "noCreation"},
		{InconsistentValue, 12, "inconsistentValue"},
		{ResourceUnavailable, 13, "resourceUnavailable"},
		{CommitFailed, 14, "commitFailed"},
		{UndoFailed, 15, "undoFailed"},
		{AuthorizationError, 16, "authorizationError"},
		{NotWritable, 17, "notWritable"},
		{InconsistentName, 18, "inconsistentName"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if int(c.c) != c.n {
				t.Errorf("int(%s) = %d, want %d (RFC 3416 §3)", c.name, int(c.c), c.n)
			}
			if got := c.c.String(); got != c.name {
				t.Errorf("(%d).String() = %q, want %q", c.n, got, c.name)
			}
		})
	}

	// Fallback path for unknown values surfaces a numeric form, not a
	// named status. Pins both the upper and lower out-of-range cases.
	if got := PDUErrorStatus(19).String(); got != "PDUErrorStatus(19)" {
		t.Errorf("PDUErrorStatus(19).String() = %q, want %q", got, "PDUErrorStatus(19)")
	}
	if got := PDUErrorStatus(-1).String(); got != "PDUErrorStatus(-1)" {
		t.Errorf("PDUErrorStatus(-1).String() = %q, want %q", got, "PDUErrorStatus(-1)")
	}
}

// TestRowStatus_NumericValues pins each RowStatus constant to its
// RFC 2579 §2 numeric code AND its canonical name. A reorder of the
// const block in tc.go fails this test.
//
// Covers conformance matrix row: RFC 2579 §2 / RowStatus.
func TestRowStatus_NumericValues(t *testing.T) {
	cases := []struct {
		c    RowStatus
		n    int32
		name string
	}{
		{RowStatusActive, 1, "active"},
		{RowStatusNotInService, 2, "notInService"},
		{RowStatusNotReady, 3, "notReady"},
		{RowStatusCreateAndGo, 4, "createAndGo"},
		{RowStatusCreateAndWait, 5, "createAndWait"},
		{RowStatusDestroy, 6, "destroy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if int32(c.c) != c.n {
				t.Errorf("int32(%s) = %d, want %d (RFC 2579 §2)", c.name, int32(c.c), c.n)
			}
			if got := c.c.String(); got != c.name {
				t.Errorf("(%d).String() = %q, want %q", c.n, got, c.name)
			}
		})
	}

	// Out-of-range values fall through to the numeric form.
	if got := RowStatus(0).String(); got != "RowStatus(0)" {
		t.Errorf("RowStatus(0).String() = %q, want %q", got, "RowStatus(0)")
	}
	if got := RowStatus(7).String(); got != "RowStatus(7)" {
		t.Errorf("RowStatus(7).String() = %q, want %q", got, "RowStatus(7)")
	}
}

// TestVersion_NumericValues pins each Version constant to its numeric
// value AND canonical name. Note these are library-internal values, NOT
// the SNMPv1/v2c/v3 wire-version bytes (which are 0/1/3 respectively per
// RFC 3416); the Backend translation layer maps between the two.
//
// Covers conformance matrix row: RFC 3416 §3 / Version.
func TestVersion_NumericValues(t *testing.T) {
	cases := []struct {
		c    Version
		n    int
		name string
	}{
		{VersionUnset, 0, "unset"},
		{V1, 1, "v1"},
		{V2c, 2, "v2c"},
		{V3, 3, "v3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if int(c.c) != c.n {
				t.Errorf("int(%s) = %d, want %d", c.name, int(c.c), c.n)
			}
			if got := c.c.String(); got != c.name {
				t.Errorf("(%d).String() = %q, want %q", c.n, got, c.name)
			}
		})
	}

	// Fallback for unknown numeric form.
	if got := Version(99).String(); got != "Version(99)" {
		t.Errorf("Version(99).String() = %q, want %q", got, "Version(99)")
	}
}

// TestKind_OneToOneWithVarBind pins the one-to-one correspondence
// between every non-Unknown Kind value and a concrete VarBind variant.
// A new Kind constant added without a matching variant — or vice versa
// — fails this test or the exhaustive switch in varbind_test.go.
//
// Covers conformance matrix row: RFC 3416 §4.2 / exception variants on
// wire, and the structural pin that a Kind reorder paired with
// a Header.Kind mismatch is caught at test time.
func TestKind_OneToOneWithVarBind(t *testing.T) {
	oid := MustOID(1, 3, 6)
	mk := func(k Kind) Header { return Header{OID: oid, Kind: k} }

	// One constructor per Kind — fills GetHeader().Kind from the variant's
	// embedded Header. Order matches kind.go declaration order so a Kind
	// reorder will show up as a test failure on the misaligned row.
	cases := []struct {
		k  Kind
		vb VarBind
	}{
		{KindInteger32, Integer32Var{Header: mk(KindInteger32)}},
		{KindUinteger32, Uinteger32Var{Header: mk(KindUinteger32)}},
		{KindOctetString, OctetStringVar{Header: mk(KindOctetString)}},
		{KindObjectID, ObjectIDVar{Header: mk(KindObjectID)}},
		{KindBitString, BitStringVar{Header: mk(KindBitString)}},
		{KindCounter32, Counter32Var{Header: mk(KindCounter32)}},
		{KindGauge32, Gauge32Var{Header: mk(KindGauge32)}},
		{KindTimeTicks, TimeTicksVar{Header: mk(KindTimeTicks)}},
		{KindCounter64, Counter64Var{Header: mk(KindCounter64)}},
		{KindIPAddress, IPAddressVar{Header: mk(KindIPAddress), Value: net.IPv4(0, 0, 0, 0)}},
		{KindNsapAddress, NsapAddressVar{Header: mk(KindNsapAddress)}},
		{KindOpaque, OpaqueVar{Header: mk(KindOpaque)}},
		{KindOpaqueFloat, OpaqueFloatVar{Header: mk(KindOpaqueFloat)}},
		{KindOpaqueDouble, OpaqueDoubleVar{Header: mk(KindOpaqueDouble)}},
		{KindNull, NullVar{Header: mk(KindNull)}},
		{KindNoSuchObject, NoSuchObjectVar{Header: mk(KindNoSuchObject)}},
		{KindNoSuchInstance, NoSuchInstanceVar{Header: mk(KindNoSuchInstance)}},
		{KindEndOfMibView, EndOfMibViewVar{Header: mk(KindEndOfMibView)}},
	}
	for _, c := range cases {
		t.Run(c.k.String(), func(t *testing.T) {
			if got := c.vb.GetHeader().Kind; got != c.k {
				t.Errorf("%T.GetHeader().Kind = %v, want %v", c.vb, got, c.k)
			}
		})
	}

	// The count check pins that the Kind enum and the variant set move
	// together: 18 non-Unknown Kinds, 18 cases. KindUnknown is the zero
	// value reserved for uninitialised headers and has no variant.
	const wantCases = 18
	if len(cases) != wantCases {
		t.Errorf("len(cases) = %d, want %d -- Kind/variant counts diverged",
			len(cases), wantCases)
	}
}
