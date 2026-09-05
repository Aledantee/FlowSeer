package snmp

import (
	"net/netip"
	"testing"
)

func suffix(arcs ...uint32) OID { return OID{subs: arcs} }

func TestDecodeIndex_SingleInteger(t *testing.T) {
	parts, ok := DecodeIndex(suffix(7), []IndexShape{{Kind: IndexInteger}})
	if !ok || len(parts) != 1 || parts[0].Integer != 7 {
		t.Fatalf("DecodeIndex = %+v, %v", parts, ok)
	}
}

func TestDecodeIndex_FixedThreeArcComposite(t *testing.T) {
	shapes := []IndexShape{{Kind: IndexInteger}, {Kind: IndexInteger}, {Kind: IndexInteger}}
	parts, ok := DecodeIndex(suffix(12000, 3, 1), shapes)
	if !ok || len(parts) != 3 {
		t.Fatalf("DecodeIndex = %+v, %v", parts, ok)
	}
	for i, want := range []uint32{12000, 3, 1} {
		if parts[i].Integer != want {
			t.Errorf("part %d = %d, want %d", i, parts[i].Integer, want)
		}
	}
}

func TestDecodeIndex_LengthPrefixedOctets(t *testing.T) {
	parts, ok := DecodeIndex(suffix(3, 0x61, 0x62, 0x63), []IndexShape{{Kind: IndexLengthPrefixedOctets}})
	if !ok || len(parts) != 1 || string(parts[0].Octets) != "abc" {
		t.Fatalf("DecodeIndex = %+v, %v", parts, ok)
	}
}

func TestDecodeIndex_LengthExceedsRemainingArcs(t *testing.T) {
	shapes := []IndexShape{{Kind: IndexInteger}, {Kind: IndexLengthPrefixedOctets}}
	parts, ok := DecodeIndex(suffix(5, 4, 0x61, 0x62), shapes)
	if ok {
		t.Fatal("expected a short suffix to report ok=false")
	}
	if len(parts) != 2 || parts[0].Integer != 5 {
		t.Fatalf("parts = %+v", parts)
	}
	if parts[1].Octets != nil || parts[1].Integer != 0 {
		t.Fatalf("failed part should be zero, got %+v", parts[1])
	}
}

func TestDecodeIndex_FixedOctetsRejectsWideArc(t *testing.T) {
	_, ok := DecodeIndex(suffix(1, 256, 3), []IndexShape{{Kind: IndexFixedOctets, Length: 3}})
	if ok {
		t.Fatal("an arc above 255 is not an octet")
	}
	parts, ok := DecodeIndex(suffix(1, 2, 3), []IndexShape{{Kind: IndexFixedOctets, Length: 3}})
	if !ok || string(parts[0].Octets) != "\x01\x02\x03" {
		t.Fatalf("DecodeIndex = %+v, %v", parts, ok)
	}
}

func TestDecodeIndex_ImpliedOctetsConsumeRest(t *testing.T) {
	shapes := []IndexShape{{Kind: IndexInteger}, {Kind: IndexImpliedOctets}}
	parts, ok := DecodeIndex(suffix(9, 0x68, 0x69), shapes)
	if !ok || len(parts) != 2 || parts[0].Integer != 9 || string(parts[1].Octets) != "hi" {
		t.Fatalf("DecodeIndex = %+v, %v", parts, ok)
	}
}

func TestDecodeIndex_LengthPrefixedOID(t *testing.T) {
	parts, ok := DecodeIndex(suffix(3, 1, 3, 6, 42), []IndexShape{{Kind: IndexLengthPrefixedOID}, {Kind: IndexInteger}})
	if !ok || len(parts) != 2 {
		t.Fatalf("DecodeIndex = %+v, %v", parts, ok)
	}
	if got := parts[0].OID.String(); got != "1.3.6" {
		t.Errorf("OID part = %q, want 1.3.6", got)
	}
	if parts[1].Integer != 42 {
		t.Errorf("trailing integer = %d", parts[1].Integer)
	}
}

func TestDecodeIndex_ImpliedAndFixedOID(t *testing.T) {
	parts, ok := DecodeIndex(suffix(0, 0, 7, 8), []IndexShape{{Kind: IndexFixedOID, Length: 2}, {Kind: IndexImpliedOID}})
	if !ok || parts[0].OID.String() != "0.0" || parts[1].OID.String() != "7.8" {
		t.Fatalf("DecodeIndex = %+v, %v", parts, ok)
	}
}

func TestDecodeIndex_IPv4(t *testing.T) {
	parts, ok := DecodeIndex(suffix(10, 0, 0, 1), []IndexShape{{Kind: IndexIPv4}})
	if !ok || parts[0].Addr != netip.MustParseAddr("10.0.0.1") {
		t.Fatalf("DecodeIndex = %+v, %v", parts, ok)
	}
	parts, ok = DecodeIndex(suffix(10, 0, 0), []IndexShape{{Kind: IndexIPv4}})
	if ok || parts[0].Addr.IsValid() {
		t.Fatalf("three arcs are not an IPv4 address: %+v, %v", parts, ok)
	}
	if _, ok = DecodeIndex(suffix(10, 0, 0, 999), []IndexShape{{Kind: IndexIPv4}}); ok {
		t.Fatal("an arc above 255 is not an IPv4 octet")
	}
}

func TestDecodeIndex_TrailingArcs(t *testing.T) {
	parts, ok := DecodeIndex(suffix(1, 2), []IndexShape{{Kind: IndexInteger}})
	if ok {
		t.Fatal("trailing arcs must report ok=false")
	}
	if len(parts) != 1 || parts[0].Integer != 1 {
		t.Fatalf("parts = %+v", parts)
	}
}

func TestDecodeIndex_ShortSuffix(t *testing.T) {
	parts, ok := DecodeIndex(suffix(1), []IndexShape{{Kind: IndexInteger}, {Kind: IndexInteger}})
	if ok || len(parts) != 2 || parts[0].Integer != 1 || parts[1].Integer != 0 {
		t.Fatalf("DecodeIndex = %+v, %v", parts, ok)
	}
}

func TestDecodeIndex_EmptyShapesAndSuffix(t *testing.T) {
	if parts, ok := DecodeIndex(OID{}, nil); !ok || len(parts) != 0 {
		t.Fatalf("DecodeIndex = %+v, %v", parts, ok)
	}
}

func FuzzDecodeIndex(f *testing.F) {
	f.Add([]byte{3, 1, 2, 3}, []byte{byte(IndexLengthPrefixedOctets)})
	f.Add([]byte{10, 0, 0, 1, 5}, []byte{byte(IndexIPv4), byte(IndexInteger)})
	f.Add([]byte{}, []byte{byte(IndexImpliedOID)})
	f.Add([]byte{1}, []byte{200})
	f.Fuzz(func(t *testing.T, arcs, kinds []byte) {
		subs := make([]uint32, len(arcs))
		for i, a := range arcs {
			// Spread arcs across the uint32 range so range checks are exercised.
			subs[i] = uint32(a) * uint32(a) * uint32(a) * 4
		}
		shapes := make([]IndexShape, len(kinds))
		for i, k := range kinds {
			shapes[i] = IndexShape{Kind: IndexKind(k % 9), Length: int(k >> 4)}
		}
		parts, _ := DecodeIndex(OID{subs: subs}, shapes)
		if len(parts) != len(shapes) {
			t.Fatalf("len(parts) = %d, want %d", len(parts), len(shapes))
		}
	})
}
