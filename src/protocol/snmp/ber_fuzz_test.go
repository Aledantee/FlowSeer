package snmp

import (
	"testing"
)

// FuzzDecodeTLV is the repo's first fuzz target. It walks an arbitrary
// byte stream as a chain of BER TLVs, descending into constructed tags
// up to the depth ceiling. The contract under test is the invariant
// that any input yields either a clean decode or a typed
// error — never a panic, an out-of-bounds slice, or unbounded recursion.
//
// The seed corpus includes a deeply-nested SEQUENCE so the fuzzer starts
// from the recursion-ceiling boundary rather than discovering it by
// chance.
func FuzzDecodeTLV(f *testing.F) {
	f.Add([]byte{0x02, 0x01, 0x05})                         // INTEGER 5
	f.Add([]byte{0x30, 0x03, 0x02, 0x01, 0x05})             // SEQUENCE { INTEGER 5 }
	f.Add([]byte{0x06, 0x05, 0x2b, 0x06, 0x01, 0x02, 0x01}) // OID
	f.Add([]byte{0x41, 0x81, 0x04, 0x00, 0x0f, 0x42, 0x40}) // Counter32 long-form
	f.Add([]byte{0x80})                                     // indefinite length
	f.Add([]byte{0x04, 0x0a, 0x01})                         // length past buffer
	// Covers conformance matrix row: enc-neg-length (gosnmp #552). A
	// long-form length octet 0x88 announces 8 length octets, which would
	// sign-extend to a negative int64 if accumulated unguarded; parseLength
	// caps the long-form octet count, so this yields a typed error with no
	// negative-index panic.
	f.Add([]byte{0x04, 0x88, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	// Deeply nested SEQUENCE (10 levels) — past the depth ceiling.
	nested := []byte{0x02, 0x01, 0x00}
	for i := 0; i < 10; i++ {
		wrapped := append([]byte{0x30}, appendLength(nil, len(nested))...)
		wrapped = append(wrapped, nested...)
		nested = wrapped
	}
	f.Add(nested)

	f.Fuzz(func(_ *testing.T, data []byte) {
		walkTLVs(data, 0)
	})
}

// walkTLVs recursively decodes data as a sequence of TLVs, descending into
// constructed tags. It returns nothing: the test contract is the absence
// of a panic. Errors are expected and ignored; the depth guard is honored
// so a malicious input cannot recurse without bound.
func walkTLVs(data []byte, depth int) {
	if depth > maxDecodeDepth {
		return
	}
	for len(data) > 0 {
		tag, content, consumed, err := parseTLV(data)
		if err != nil {
			return
		}
		// Constructed tags (class/constructed bit 0x20) may nest.
		if tag&0x20 != 0 {
			walkTLVs(content, depth+1)
		} else {
			// Exercise the value decoders too; all must be panic-free.
			switch tag {
			case tagInteger:
				_, _ = decodeSignedInt(content)
			case tagCounter32, tagGauge32, tagTimeTicks, tagCounter64, tagUinteger32:
				_, _ = decodeUnsigned(content)
			case tagOID:
				_, _ = decodeOID(content)
			case tagIPAddress:
				_, _ = decodeIPv4(content)
			case tagOpaque:
				_, _, _, _ = opaqueReal(content)
			}
		}
		data = data[consumed:]
	}
}
