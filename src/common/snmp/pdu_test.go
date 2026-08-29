package snmp

import (
	"errors"
	"net"
	"testing"
)

// Covers conformance matrix row: RFC 3416 §4 PDU tags. The PDU identifier
// octets are wire-protocol constants; a reorder would silently mis-frame
// every PDU once it hits the network.
func TestPDUType_NumericValues(t *testing.T) {
	cases := []struct {
		name string
		got  pduType
		want byte
	}{
		{"GetRequest", pduGetRequest, 0xA0},
		{"GetNextRequest", pduGetNextRequest, 0xA1},
		{"GetResponse", pduGetResponse, 0xA2},
		{"SetRequest", pduSetRequest, 0xA3},
		{"v1 Trap", pduV1Trap, 0xA4},
		{"GetBulkRequest", pduGetBulkRequest, 0xA5},
		{"InformRequest", pduInformRequest, 0xA6},
		{"v2c Trap", pduV2Trap, 0xA7},
		{"Report", pduReport, 0xA8},
	}
	for _, c := range cases {
		if byte(c.got) != c.want {
			t.Errorf("pduType %s = 0x%02x, want 0x%02x", c.name, byte(c.got), c.want)
		}
	}
}

func sysDescr() OID { return MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0) }
func ifInOctets() OID {
	return MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10, 1)
}

func TestMessageRoundTrip_GetRequest(t *testing.T) {
	m := &message{
		version:   V2c,
		community: "public",
		pdu: pdu{
			typ:       pduGetRequest,
			requestID: 0x7fffffff, // max request-id
			varbinds: []VarBind{
				NullVar{Header: Header{OID: sysDescr(), Kind: KindNull}},
			},
		},
	}
	enc, err := encodeMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeMessage(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got.version != V2c || got.community != "public" {
		t.Fatalf("envelope mismatch: %+v", got)
	}
	if got.pdu.typ != pduGetRequest || got.pdu.requestID != 0x7fffffff {
		t.Fatalf("pdu mismatch: %+v", got.pdu)
	}
	if len(got.pdu.varbinds) != 1 {
		t.Fatalf("want 1 varbind, got %d", len(got.pdu.varbinds))
	}
	if !got.pdu.varbinds[0].GetHeader().OID.Equal(sysDescr()) {
		t.Fatalf("varbind OID = %s", got.pdu.varbinds[0].GetHeader().OID)
	}
}

func TestMessageRoundTrip_GetResponse_MixedVarBinds(t *testing.T) {
	m := &message{
		version:   V2c,
		community: "public",
		pdu: pdu{
			typ:       pduGetResponse,
			requestID: 42,
			varbinds: []VarBind{
				OctetStringVar{Header: Header{OID: sysDescr(), Kind: KindOctetString}, Value: []byte("Router X")},
				Counter64Var{Header: Header{OID: ifInOctets(), Kind: KindCounter64}, Value: 0xfedcba9876543210},
				NoSuchInstanceVar{Header: Header{OID: MustOID(1, 3, 6, 1, 2, 1, 1, 5, 0), Kind: KindNoSuchInstance}},
			},
		},
	}
	enc, err := encodeMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeMessage(enc)
	if err != nil {
		t.Fatal(err)
	}
	vbs := got.pdu.varbinds
	if len(vbs) != 3 {
		t.Fatalf("want 3 varbinds, got %d", len(vbs))
	}
	if os, ok := vbs[0].(OctetStringVar); !ok || string(os.Value) != "Router X" {
		t.Fatalf("vb0 = %#v", vbs[0])
	}
	if c, ok := vbs[1].(Counter64Var); !ok || c.Value != 0xfedcba9876543210 {
		t.Fatalf("vb1 = %#v", vbs[1])
	}
	// v2c exception decoded as the Kind, not as data.
	if _, ok := vbs[2].(NoSuchInstanceVar); !ok {
		t.Fatalf("vb2 should be NoSuchInstanceVar, got %#v", vbs[2])
	}
}

func TestMessageRoundTrip_EmptyVarBindList(t *testing.T) {
	m := &message{version: V1, community: "c", pdu: pdu{typ: pduGetResponse, requestID: 1}}
	enc, err := encodeMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeMessage(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.pdu.varbinds) != 0 {
		t.Fatalf("want empty varbind list, got %d", len(got.pdu.varbinds))
	}
}

// Covers conformance matrix row: RFC 3416 error-status. A v1 noSuchName
// response carries its error-status and 1-based error-index, which must
// decode into the structured model verbatim for translate.go to surface
// as a *PDUError.
func TestDecode_V1NoSuchName(t *testing.T) {
	m := &message{
		version:   V1,
		community: "public",
		pdu: pdu{
			typ:         pduGetResponse,
			requestID:   7,
			errorStatus: NoSuchName,
			errorIndex:  1,
			varbinds: []VarBind{
				NullVar{Header: Header{OID: sysDescr(), Kind: KindNull}},
			},
		},
	}
	enc, err := encodeMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeMessage(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got.pdu.errorStatus != NoSuchName || got.pdu.errorIndex != 1 {
		t.Fatalf("error-status=%s index=%d", got.pdu.errorStatus, got.pdu.errorIndex)
	}
}

func TestRoundTrip_GetBulkRequest(t *testing.T) {
	m := &message{
		version:   V2c,
		community: "public",
		pdu: pdu{
			typ:            pduGetBulkRequest,
			requestID:      99,
			nonRepeaters:   0,
			maxRepetitions: 50,
			varbinds: []VarBind{
				NullVar{Header: Header{OID: MustOID(1, 3, 6, 1, 2, 1, 2, 2), Kind: KindNull}},
			},
		},
	}
	enc, err := encodeMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeMessage(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got.pdu.typ != pduGetBulkRequest || got.pdu.nonRepeaters != 0 || got.pdu.maxRepetitions != 50 {
		t.Fatalf("getbulk fields: %+v", got.pdu)
	}
}

func TestDecode_V1TrapPDU(t *testing.T) {
	// Build a v1 Trap-PDU by hand (encodePDU does not emit traps).
	body := encodeOID(MustOID(1, 3, 6, 1, 4, 1, 9)) // enterprise (Cisco)
	ip, err := appendIPv4(net.IPv4(10, 1, 2, 3))
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, ip...)
	body = appendInt(body, 6)                     // generic = enterpriseSpecific
	body = appendInt(body, 42)                    // specific
	body = appendUint(body, tagTimeTicks, 123456) // time-stamp
	vbl, err := encodeVarBindList([]VarBind{
		OctetStringVar{Header: Header{OID: sysDescr(), Kind: KindOctetString}, Value: []byte("trap detail")},
	})
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, vbl...)
	pduBytes := appendSequence(byte(pduV1Trap), body)

	var msgBody []byte
	msgBody = appendInt(msgBody, wireVersionV1)
	msgBody = appendOctetString(msgBody, tagOctetString, []byte("public"))
	msgBody = append(msgBody, pduBytes...)
	datagram := appendSequence(tagSequence, msgBody)

	got, err := decodeMessage(datagram)
	if err != nil {
		t.Fatal(err)
	}
	if got.pdu.typ != pduV1Trap || got.pdu.trapV1 == nil {
		t.Fatalf("not a v1 trap: %+v", got.pdu)
	}
	tr := got.pdu.trapV1
	if !tr.enterprise.Equal(MustOID(1, 3, 6, 1, 4, 1, 9)) {
		t.Errorf("enterprise = %s", tr.enterprise)
	}
	if !tr.agentAddr.Equal(net.IPv4(10, 1, 2, 3)) {
		t.Errorf("agent-addr = %s", tr.agentAddr)
	}
	if tr.generic != 6 || tr.specific != 42 || tr.timestamp != 123456 {
		t.Errorf("trap fields generic=%d specific=%d ts=%d", tr.generic, tr.specific, tr.timestamp)
	}
	if len(got.pdu.varbinds) != 1 {
		t.Errorf("want 1 trap varbind, got %d", len(got.pdu.varbinds))
	}
}

func TestDecode_V2TrapPDU(t *testing.T) {
	// v2c trap uses the standard PDU shape, led by sysUpTime.0 and
	// snmpTrapOID.0.
	sysUpTime := MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0)
	snmpTrapOID := MustOID(1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0)
	coldStart := MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 1)
	m := &message{
		version:   V2c,
		community: "public",
		pdu: pdu{
			typ:       pduV2Trap,
			requestID: 1,
			varbinds: []VarBind{
				TimeTicksVar{Header: Header{OID: sysUpTime, Kind: KindTimeTicks}, Value: 555},
				ObjectIDVar{Header: Header{OID: snmpTrapOID, Kind: KindObjectID}, Value: coldStart},
			},
		},
	}
	// encodePDU handles pduV2Trap via the standard path.
	enc, err := encodeMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeMessage(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got.pdu.typ != pduV2Trap || len(got.pdu.varbinds) != 2 {
		t.Fatalf("v2 trap decode: %+v", got.pdu)
	}
	if tt, ok := got.pdu.varbinds[0].(TimeTicksVar); !ok || tt.Value != 555 {
		t.Errorf("sysUpTime vb = %#v", got.pdu.varbinds[0])
	}
	if oidv, ok := got.pdu.varbinds[1].(ObjectIDVar); !ok || !oidv.Value.Equal(coldStart) {
		t.Errorf("snmpTrapOID vb = %#v", got.pdu.varbinds[1])
	}
}

func TestDecodeMessage_RejectsV3BeforeBody(t *testing.T) {
	// A message whose version octet is v3 (3) must be rejected via
	// errUnsupportedVersion regardless of the rest of the body.
	var body []byte
	body = appendInt(body, wireVersionV3)
	body = appendOctetString(body, tagOctetString, []byte("ignored"))
	// Deliberately append garbage where the PDU would be; decode must not
	// reach it.
	body = append(body, 0xff, 0xff, 0xff)
	datagram := appendSequence(tagSequence, body)

	_, err := decodeMessage(datagram)
	if !errors.Is(err, errUnsupportedVersion) {
		t.Fatalf("err = %v, want Is errUnsupportedVersion", err)
	}
}

// Exception varbinds are receive-only in requests, but the codec encodes
// them symmetrically so GetResponse fixtures round-trip; the transmit
// policy lives in Session.Set. Here we confirm each exception
// variant round-trips through encode→decode preserving its Kind.
func TestEncodeDecode_ExceptionVarBinds(t *testing.T) {
	oid := sysDescr()
	for _, want := range []VarBind{
		NoSuchObjectVar{Header: Header{OID: oid, Kind: KindNoSuchObject}},
		NoSuchInstanceVar{Header: Header{OID: oid, Kind: KindNoSuchInstance}},
		EndOfMibViewVar{Header: Header{OID: oid, Kind: KindEndOfMibView}},
	} {
		enc, err := encodeVarBind(want)
		if err != nil {
			t.Fatalf("%T: encode: %v", want, err)
		}
		vbs, err := decodeVarBindList(appendSequence(tagSequence, enc), 1)
		if err != nil || len(vbs) != 1 {
			t.Fatalf("%T: decode: %v (n=%d)", want, err, len(vbs))
		}
		if vbs[0].GetHeader().Kind != want.GetHeader().Kind {
			t.Errorf("%T: kind = %s, want %s", want, vbs[0].GetHeader().Kind, want.GetHeader().Kind)
		}
	}
}

func TestDecodeValue_AllScalars_RoundTrip(t *testing.T) {
	oid := sysDescr()
	cases := []VarBind{
		Integer32Var{Header: Header{OID: oid, Kind: KindInteger32}, Value: -2147483648},
		Uinteger32Var{Header: Header{OID: oid, Kind: KindUinteger32}, Value: 4294967295},
		Counter32Var{Header: Header{OID: oid, Kind: KindCounter32}, Value: 4000000000},
		Gauge32Var{Header: Header{OID: oid, Kind: KindGauge32}, Value: 100},
		TimeTicksVar{Header: Header{OID: oid, Kind: KindTimeTicks}, Value: 8888},
		IPAddressVar{Header: Header{OID: oid, Kind: KindIPAddress}, Value: net.IPv4(8, 8, 8, 8).To4()},
		OpaqueFloatVar{Header: Header{OID: oid, Kind: KindOpaqueFloat}, Value: 1.5},
		OpaqueDoubleVar{Header: Header{OID: oid, Kind: KindOpaqueDouble}, Value: -9.25},
	}
	for _, want := range cases {
		enc, err := encodeVarBind(want)
		if err != nil {
			t.Fatalf("%T: encode: %v", want, err)
		}
		// Decode the varbind back via the list decoder (wrap in a SEQ OF).
		list := appendSequence(tagSequence, enc)
		vbs, err := decodeVarBindList(list, 1)
		if err != nil {
			t.Fatalf("%T: decode: %v", want, err)
		}
		if len(vbs) != 1 {
			t.Fatalf("%T: want 1 vb, got %d", want, len(vbs))
		}
		if vbs[0].GetHeader().Kind != want.GetHeader().Kind {
			t.Errorf("%T: kind = %s, want %s", want, vbs[0].GetHeader().Kind, want.GetHeader().Kind)
		}
	}
}

// TestDecodeMessage_FootgunWire feeds a hand-assembled v2c GetResponse
// datagram with non-canonical BER — a 5-byte leading-zero Counter32
// and a forced long-form-length OCTET STRING — through the full
// decodeMessage pipeline. The round-trip tests only exercise canonical
// encodings; this restores the end-to-end wire-to-VarBind coverage the
// deleted t5 raw-byte tier provided for these foot-gun shapes.
func TestDecodeMessage_FootgunWire(t *testing.T) {
	flen := func(n int) []byte {
		if n < 0x80 {
			return []byte{byte(n)}
		}
		var b []byte
		for n > 0 {
			b = append([]byte{byte(n)}, b...)
			n >>= 8
		}
		return append([]byte{byte(0x80 | len(b))}, b...)
	}
	ftlv := func(tag byte, c []byte) []byte { return append(append([]byte{tag}, flen(len(c))...), c...) }
	cat := func(parts ...[]byte) []byte {
		var o []byte
		for _, p := range parts {
			o = append(o, p...)
		}
		return o
	}

	// ifHCInOctets.1 (1.3.6.1.2.1.31.1.1.1.6.1); Counter32 0xFFFFFFFF
	// encoded as 5 octets with a leading zero.
	oid1 := []byte{0x2b, 0x06, 0x01, 0x02, 0x01, 0x1f, 0x01, 0x01, 0x01, 0x06, 0x01}
	c32 := []byte{0x41, 0x05, 0x00, 0xff, 0xff, 0xff, 0xff}
	vb1 := ftlv(0x30, cat(ftlv(0x06, oid1), c32))

	// sysDescr.0 (1.3.6.1.2.1.1.1.0); OCTET STRING "hello" framed with a
	// forced long-form length (0x81 0x05) rather than the minimal 0x05.
	oid2 := []byte{0x2b, 0x06, 0x01, 0x02, 0x01, 0x01, 0x01, 0x00}
	osv := append([]byte{0x04, 0x81, 0x05}, []byte("hello")...)
	vb2 := ftlv(0x30, cat(ftlv(0x06, oid2), osv))

	vbl := ftlv(0x30, cat(vb1, vb2))
	pduBytes := ftlv(0xA2, cat(
		ftlv(0x02, []byte{0x01}), // request-id 1
		ftlv(0x02, []byte{0x00}), // error-status 0
		ftlv(0x02, []byte{0x00}), // error-index 0
		vbl,
	))
	datagram := ftlv(0x30, cat(
		ftlv(0x02, []byte{0x01}),     // version v2c
		ftlv(0x04, []byte("public")), // community
		pduBytes,
	))

	m, err := decodeMessage(datagram)
	if err != nil {
		t.Fatalf("decodeMessage: %v", err)
	}
	vbs := m.pdu.varbinds
	if len(vbs) != 2 {
		t.Fatalf("want 2 varbinds, got %d", len(vbs))
	}
	if c, ok := vbs[0].(Counter32Var); !ok || c.Value != 0xffffffff {
		t.Fatalf("vb0 (5-byte Counter32) = %#v, want Counter32Var 4294967295", vbs[0])
	}
	if os, ok := vbs[1].(OctetStringVar); !ok || string(os.Value) != "hello" {
		t.Fatalf("vb1 (long-form OctetString) = %#v, want OctetStringVar \"hello\"", vbs[1])
	}
}
