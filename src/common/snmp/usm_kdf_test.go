package snmp

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// mustHex decodes a hex string fixture or fails the package's test setup.
func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("usm test: bad hex fixture " + s + ": " + err.Error())
	}
	return b
}

// authProtoByName maps a fixture name to its [AuthProtocol].
var authProtoByName = map[string]AuthProtocol{
	"MD5":    AuthMD5,
	"SHA1":   AuthSHA,
	"SHA224": AuthSHA224,
	"SHA256": AuthSHA256,
	"SHA384": AuthSHA384,
	"SHA512": AuthSHA512,
}

// privCell decomposes a fixture name like "MD5+AES256" into its protocols.
var privCell = map[string]struct {
	auth AuthProtocol
	priv PrivProtocol
}{
	"MD5+AES192":    {AuthMD5, PrivAES192},
	"SHA1+AES192":   {AuthSHA, PrivAES192},
	"SHA224+AES192": {AuthSHA224, PrivAES192},
	"MD5+AES256":    {AuthMD5, PrivAES256},
	"SHA1+AES256":   {AuthSHA, PrivAES256},
	"SHA224+AES256": {AuthSHA224, PrivAES256},
	"SHA256+AES256": {AuthSHA256, PrivAES256},
	"MD5+3DES":      {AuthMD5, Priv3DES},
	"SHA1+3DES":     {AuthSHA, Priv3DES},
}

// cVariantFor returns the Cisco (Reeder) priv protocol matching a
// Blumenthal AES protocol.
func cVariantFor(p PrivProtocol) PrivProtocol {
	switch p {
	case PrivAES192:
		return PrivAES192C
	case PrivAES256:
		return PrivAES256C
	default:
		return p
	}
}

// TestPasswordToKey_RFC3414 pins the password-to-key (hash-phase) output
// against the RFC 3414 A.3 published Ku values for MD5 and SHA-1 — the
// genuine external vectors — plus the externally-sourced SHA-2 widths.
func TestPasswordToKey_RFC3414(t *testing.T) {
	for _, v := range authKDFVectors {
		t.Run(v.name, func(t *testing.T) {
			newHash, err := authHashFor(authProtoByName[v.name])
			if err != nil {
				t.Fatalf("authHashFor: %v", err)
			}
			got := expandPassphrase(newHash, []byte(vecPassphrase))
			if !bytes.Equal(got, v.ku) {
				t.Fatalf("Ku mismatch:\n got %x\nwant %x", got, v.ku)
			}
		})
	}
}

// TestLocalizedAuthKey_AllProtocols asserts the localized auth key for every
// protocol against the externally-sourced fixtures and pins the key widths
// (16/20/28/32/48/64).
func TestLocalizedAuthKey_AllProtocols(t *testing.T) {
	wantWidth := map[string]int{"MD5": 16, "SHA1": 20, "SHA224": 28, "SHA256": 32, "SHA384": 48, "SHA512": 64}
	for _, v := range authKDFVectors {
		t.Run(v.name, func(t *testing.T) {
			got, err := localizedAuthKey(authProtoByName[v.name], vecPassphrase, vecEngineID)
			if err != nil {
				t.Fatalf("localizedAuthKey: %v", err)
			}
			if !bytes.Equal(got, v.kul) {
				t.Fatalf("Kul mismatch:\n got %x\nwant %x", got, v.kul)
			}
			if len(got) != wantWidth[v.name] {
				t.Fatalf("width = %d, want %d", len(got), wantWidth[v.name])
			}
		})
	}
}

// TestLocalizedPrivKey_Extension asserts both AES key-extension schemes
// against the external fixtures. For the cells where the schemes differ
// (auth hash narrower than the priv key), the external byte-for-byte check
// is mandatory — self-consistency ("the two differ; first bytes match")
// would pass a wrong-but-stable chaining.
//
// Covers conformance matrix row: usm-aes-keyext (gosnmp #424; Cisco/Extreme).
// AES-192/256 Blumenthal vs Reeder (C-variant) key extension is pinned against
// the independent pysnmp/hashlib fixtures, with the schemes producing distinct
// keys where the auth hash is narrower than the priv key. Unit-level only —
// net-snmp has no createUser keyword that selects the Reeder path, so there is
// no T1 integration cell for it.
func TestLocalizedPrivKey_Extension(t *testing.T) {
	for _, v := range privExtendVectors {
		t.Run(v.name, func(t *testing.T) {
			cell := privCell[v.name]
			blum, err := localizedPrivKey(cell.auth, cell.priv, vecPassphrase, vecEngineID)
			if err != nil {
				t.Fatalf("Blumenthal localizedPrivKey: %v", err)
			}
			if !bytes.Equal(blum, v.blumenthal) {
				t.Fatalf("Blumenthal mismatch:\n got %x\nwant %x", blum, v.blumenthal)
			}
			reeder, err := localizedPrivKey(cell.auth, cVariantFor(cell.priv), vecPassphrase, vecEngineID)
			if err != nil {
				t.Fatalf("Reeder localizedPrivKey: %v", err)
			}
			if !bytes.Equal(reeder, v.reeder) {
				t.Fatalf("Reeder mismatch:\n got %x\nwant %x", reeder, v.reeder)
			}
			// Necessary-but-not-sufficient self-consistency: the first
			// min(width) bytes (the unextended localized key) always match.
			if !bytes.Equal(blum[:16], reeder[:16]) {
				t.Fatalf("unextended prefix should match: blum %x reeder %x", blum[:16], reeder[:16])
			}
		})
	}
}

// TestLocalizedPrivKey_Equivalence proves the escape hatch: with a hash at
// least as wide as the priv key (SHA-256 + AES-256), Blumenthal and Reeder
// are byte-identical (no extension occurs).
func TestLocalizedPrivKey_Equivalence(t *testing.T) {
	blum, err := localizedPrivKey(AuthSHA256, PrivAES256, vecPassphrase, vecEngineID)
	if err != nil {
		t.Fatalf("Blumenthal: %v", err)
	}
	reeder, err := localizedPrivKey(AuthSHA256, PrivAES256C, vecPassphrase, vecEngineID)
	if err != nil {
		t.Fatalf("Reeder: %v", err)
	}
	if !bytes.Equal(blum, reeder) {
		t.Fatalf("SHA-256+AES-256 should be identical:\n blum   %x\n reeder %x", blum, reeder)
	}
}

// TestLocalizedPrivKey_3DES asserts the 32-octet 3DES extended key (Reeder
// extension) against the external fixtures.
func TestLocalizedPrivKey_3DES(t *testing.T) {
	for _, v := range priv3DESVectors {
		t.Run(v.name, func(t *testing.T) {
			cell := privCell[v.name]
			got, err := localizedPrivKey(cell.auth, cell.priv, vecPassphrase, vecEngineID)
			if err != nil {
				t.Fatalf("localizedPrivKey: %v", err)
			}
			if !bytes.Equal(got, v.key) {
				t.Fatalf("3DES key mismatch:\n got %x\nwant %x", got, v.key)
			}
			if len(got) != 32 {
				t.Fatalf("3DES key width = %d, want 32", len(got))
			}
		})
	}
}

// TestLocalizedPrivKey_DESTruncation checks DES/AES-128 take a straight
// 16-octet truncation of the localized key (no extension).
func TestLocalizedPrivKey_DESTruncation(t *testing.T) {
	for _, proto := range []PrivProtocol{PrivDES, PrivAES} {
		t.Run(proto.String(), func(t *testing.T) {
			got, err := localizedPrivKey(AuthSHA, proto, vecPassphrase, vecEngineID)
			if err != nil {
				t.Fatalf("localizedPrivKey: %v", err)
			}
			if len(got) != 16 {
				t.Fatalf("key width = %d, want 16", len(got))
			}
			// SHA-1 localized priv key is the SHA-1 auth Kul (same passphrase),
			// truncated to 16 octets.
			want := mustHex("6695febc9288e36282235fc7151f1284")
			if !bytes.Equal(got, want) {
				t.Fatalf("DES/AES128 key mismatch:\n got %x\nwant %x", got, want)
			}
		})
	}
}

// TestExpandPassphrase_CyclicBoundary guards the passphrase[i % len]
// indexing: a passphrase whose length does not divide the 1,048,576-byte
// expansion must still expand deterministically (a concatenation bug would
// diverge here). We assert determinism plus a known independent value for a
// short non-dividing passphrase.
func TestExpandPassphrase_CyclicBoundary(t *testing.T) {
	// 7 does not divide 64 or 1<<20, exercising the wrap in every block.
	pass := []byte("abcdefg")
	newHash, _ := authHashFor(AuthMD5)
	a := expandPassphrase(newHash, pass)
	b := expandPassphrase(newHash, pass)
	if !bytes.Equal(a, b) {
		t.Fatalf("expansion not deterministic")
	}
	// Independently generated with pysnmp localkey.hash_passphrase("abcdefg", md5).
	want := mustHex("8ac4139bbed4d1dc00227906c86e1681")
	if !bytes.Equal(a, want) {
		t.Fatalf("Ku for non-dividing passphrase:\n got %x\nwant %x", a, want)
	}
}

// TestExpandPassphrase_OneByte confirms a 1-octet passphrase expands (the
// Validate layer rejects empty, but a single octet is legal).
func TestExpandPassphrase_OneByte(t *testing.T) {
	newHash, _ := authHashFor(AuthMD5)
	got := expandPassphrase(newHash, []byte("x"))
	if len(got) != 16 {
		t.Fatalf("Ku width = %d, want 16", len(got))
	}
	// Independently generated with pysnmp localkey.hash_passphrase("x", md5).
	want := mustHex("b561f87202d04959e37588ee05cf5b10")
	if !bytes.Equal(got, want) {
		t.Fatalf("Ku for 1-byte passphrase:\n got %x\nwant %x", got, want)
	}
}
