package snmp

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
)

// Independent fixtures generated with pysnmp 7.1.27 (rfc3414/3826 priv +
// eso aes192/aes256/des3). Plaintext is 21 octets (not a block multiple, so
// CBC padding is exercised); boots/time are fixed.
const (
	privVecBoots = 0x01020304
	privVecTime  = 0x0a0b0c0d
)

var privVecPlain = []byte("scopedPDU-plaintext!!")

// TestPriv_DESVector pins DES-CBC salt and ciphertext, and round-trips.
func TestPriv_DESVector(t *testing.T) {
	key := mustHex("6695febc9288e36282235fc7151f1284")
	salt := mustHex("0102030411223344") // boots(01020304) || localInt(11223344)
	wantCT := mustHex("713a9ff223f469641861f5a281d3e5db83064afc1f73ae52")

	ct, err := cbcEncrypt(PrivDES, key, salt, privVecPlain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !bytes.Equal(ct, wantCT) {
		t.Fatalf("DES ct mismatch:\n got %x\nwant %x", ct, wantCT)
	}
	pt, err := cbcDecrypt(PrivDES, key, salt, ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.HasPrefix(pt, privVecPlain) {
		t.Fatalf("round-trip lost plaintext: got %x", pt)
	}
}

// TestPriv_3DESVector pins 3DES-CBC and round-trips; the key has distinct
// sub-keys (no degenerate warning expected).
func TestPriv_3DESVector(t *testing.T) {
	key := mustHex("6695febc9288e36282235fc7151f128497b38f3f9b8b6d78936ba6e7d19dfd9c")
	salt := mustHex("0102030455667788")
	wantCT := mustHex("9e49f1f831d35f4d777baabbe5fb8779e6e88e6ea92abca7")

	ct, err := cbcEncrypt(Priv3DES, key, salt, privVecPlain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !bytes.Equal(ct, wantCT) {
		t.Fatalf("3DES ct mismatch:\n got %x\nwant %x", ct, wantCT)
	}
	pt, err := cbcDecrypt(Priv3DES, key, salt, ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.HasPrefix(pt, privVecPlain) {
		t.Fatalf("round-trip lost plaintext")
	}
}

// TestPriv_AESVectors pins AES-128/192/256 CFB salt+IV (via ciphertext) and
// round-trips.
func TestPriv_AESVectors(t *testing.T) {
	cases := []struct {
		name string
		key  []byte
		salt []byte
		ct   []byte
	}{
		{"AES128", mustHex("6695febc9288e36282235fc7151f1284"), mustHex("1122334455667788"), mustHex("27c78739e010b204f7f69d77c9fb650c1cc078976a")},
		{"AES192", mustHex("6695febc9288e36282235fc7151f128497b38f3f505e07eb"), mustHex("99aabbccddeeff00"), mustHex("df48219bb861816953f489edfbfbdf1852933a3cfe")},
		{"AES256", mustHex("6695febc9288e36282235fc7151f128497b38f3f505e07eb9af25568fa1f5dbe"), mustHex("0102030405060708"), mustHex("2e46a8ac4d90d5551fd8c455bae201a87d92d1ac2d")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ct, err := aesCFB(c.key, privVecBoots, privVecTime, c.salt, privVecPlain, false)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			if !bytes.Equal(ct, c.ct) {
				t.Fatalf("ct mismatch:\n got %x\nwant %x", ct, c.ct)
			}
			pt, err := aesCFB(c.key, privVecBoots, privVecTime, c.salt, ct, true)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if !bytes.Equal(pt, privVecPlain) {
				t.Fatalf("round-trip mismatch:\n got %x\nwant %x", pt, privVecPlain)
			}
		})
	}
}

// TestPriv_AESCVectors ties the Cisco/Reeder C-variant KEY DERIVATION to an
// external ciphertext oracle. Round-trip self-consistency alone would pass a
// key-selection bug (Blumenthal key used for a *C protocol); deriving the key
// via localizedPrivKey(..., PrivAES192C/256C, ...) and asserting the
// resulting ciphertext against an independent value (cryptography AES-CFB128
// over the Reeder localized key) catches it. The C-variants have no snmpd
// keyword and no gosnmp differential, so this is their only oracle below the
// integration tier.
func TestPriv_AESCVectors(t *testing.T) {
	cases := []struct {
		name string
		auth AuthProtocol
		priv PrivProtocol
		salt []byte
		ct   []byte
	}{
		{"SHA1+AES192C", AuthSHA, PrivAES192C, mustHex("99aabbccddeeff00"), mustHex("69ecba3a73ca7cb32f718471d21007ba7ac79364dd")},
		{"SHA1+AES256C", AuthSHA, PrivAES256C, mustHex("0102030405060708"), mustHex("b9ee37b4edb13e72fbce9f0531efcb69e6167556ca")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key, err := localizedPrivKey(c.auth, c.priv, vecPassphrase, vecEngineID)
			if err != nil {
				t.Fatalf("localizedPrivKey: %v", err)
			}
			ct, err := aesCFB(key, privVecBoots, privVecTime, c.salt, privVecPlain, false)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			if !bytes.Equal(ct, c.ct) {
				t.Fatalf("C-variant ciphertext mismatch (wrong key derivation?):\n got %x\nwant %x", ct, c.ct)
			}
		})
	}
}

// TestPriv_ContextSaltUnique asserts the atomic counter never repeats a salt
// under concurrent encrypts on one context (the IV-reuse guard).
func TestPriv_ContextSaltUnique(t *testing.T) {
	key := mustHex("6695febc9288e36282235fc7151f128497b38f3f505e07eb9af25568fa1f5dbe")
	pc, err := newPrivContext(context.Background(), PrivAES256, key)
	if err != nil {
		t.Fatalf("newPrivContext: %v", err)
	}
	const n = 256
	salts := make([][]byte, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, pp, err := pc.encrypt(7, 7, privVecPlain)
			if err != nil {
				t.Errorf("encrypt: %v", err)
				return
			}
			salts[i] = pp
		}(i)
	}
	wg.Wait()
	seen := make(map[string]bool, n)
	for _, s := range salts {
		if seen[string(s)] {
			t.Fatalf("duplicate salt: %x", s)
		}
		seen[string(s)] = true
	}
}

// TestPriv_WeakTripleDESWarns confirms a degenerate 3DES key is accepted
// (round-trips), not rejected.
func TestPriv_WeakTripleDESWarns(t *testing.T) {
	// K1==K2==K3 then an 8-byte pre-IV.
	block := mustHex("0101010101010101")
	key := bytes.Join([][]byte{block, block, block, mustHex("0203040506070809")}, nil)
	if !weakTripleDESKey(key) {
		t.Fatalf("expected weak key detection")
	}
	pc, err := newPrivContext(context.Background(), Priv3DES, key)
	if err != nil {
		t.Fatalf("degenerate 3DES key should be accepted, got: %v", err)
	}
	ct, pp, err := pc.encrypt(1, 1, privVecPlain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	pt, err := pc.decrypt(1, 1, pp, ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.HasPrefix(pt, privVecPlain) {
		t.Fatalf("round-trip lost plaintext")
	}
}

// TestPriv_AESCInteropDirection confirms an AES-256C (Reeder) ciphertext
// decrypted under an AES-256 (Blumenthal) key yields wrong plaintext (which
// surfaces upstream as a BER-parse failure), never a panic.
func TestPriv_AESCInteropDirection(t *testing.T) {
	blum, err := localizedPrivKey(AuthMD5, PrivAES256, vecPassphrase, vecEngineID)
	if err != nil {
		t.Fatalf("blum key: %v", err)
	}
	reeder, err := localizedPrivKey(AuthMD5, PrivAES256C, vecPassphrase, vecEngineID)
	if err != nil {
		t.Fatalf("reeder key: %v", err)
	}
	salt := mustHex("0102030405060708")
	ct, err := aesCFB(reeder, 1, 1, salt, privVecPlain, false)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	pt, err := aesCFB(blum, 1, 1, salt, ct, true) // wrong key
	if err != nil {
		t.Fatalf("decrypt errored (should produce garbage, not error): %v", err)
	}
	if bytes.Equal(pt, privVecPlain) {
		t.Fatalf("wrong key should not recover plaintext")
	}
}

// TestPriv_DecryptEdges confirms malformed inputs are typed errors, never
// panics.
func TestPriv_DecryptEdges(t *testing.T) {
	key := mustHex("6695febc9288e36282235fc7151f1284")
	salt := mustHex("0102030411223344")
	// CBC ciphertext not a block multiple.
	if _, err := cbcDecrypt(PrivDES, key, salt, mustHex("00112233")); !errors.Is(err, ErrPrivDecrypt) {
		t.Fatalf("non-block-multiple not rejected: %v", err)
	}
	// Truncated salt.
	if _, err := cbcDecrypt(PrivDES, key, mustHex("0102"), mustHex("0011223344556677")); !errors.Is(err, ErrPrivDecrypt) {
		t.Fatalf("short salt not rejected: %v", err)
	}
	if _, err := aesCFB(key, 1, 1, mustHex("0102"), privVecPlain, true); !errors.Is(err, ErrPrivDecrypt) {
		t.Fatalf("short AES salt not rejected: %v", err)
	}
}
