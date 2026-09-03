package snmp

// usm_vectors_test.go holds the shared USM conformance fixtures for the
// KDF, auth, and priv layers. It is the v3 crypto oracle that needs no
// peer.
//
// # Provenance (oracle integrity)
//
// All keys below were captured from an INDEPENDENT implementation, never
// re-derived by the code under test (which would be a circular oracle):
//
//   - MD5/SHA-1 Ku and localized keys are the RFC 3414 Appendix A.3.1/A.3.2
//     published vectors (passphrase "maplesyrup", engineID
//     0x000000000000000000000002). NOTE: the SHA-1 localized key matches the
//     RFC's printed value byte-for-byte; the MD5 localized key here
//     (526f5eed9fcc…) is what H(Ku‖engineID‖Ku) actually produces and what
//     both pysnmp and a plain crypto/md5 computation agree on.
//
//   - SHA-224/256/384/512 localized keys and the Blumenthal/Reeder AES and
//     3DES extended keys were generated with pysnmp 7.1.27
//     (pysnmp.proto.secmod.rfc3414.localkey + .eso.priv.{aes192,aes256,des3})
//     and cross-checked against a direct hashlib computation. They are
//     committed as opaque expected-byte fixtures.
//
// Which properties these fixtures prove (unit-provable here):
//   - localized key bytes vs an external tool (KDF correctness).
//   - HMAC truncation lengths and round-trip.
//   - DES/AES IV/salt layout and round-trip.
//
// What is NOT unit-provable and is deferred to integration (live
// Net-SNMP): the on-the-wire MAC scope (which exact octets the MAC covers)
// and the full encryption framing of a real authPriv message. The
// AES-192C/256C variants have no distinct snmpd keyword and no gosnmp
// differential, so these external fixtures are their ONLY oracle below the
// integration tier.

// vecEngineID is the RFC 3414 Appendix A.3 example authoritative engineID.
var vecEngineID = mustHex("000000000000000000000002")

// vecPassphrase is the RFC 3414 Appendix A.3 example passphrase, used for
// both the auth and priv passphrase in these fixtures.
const vecPassphrase = "maplesyrup"

// kdfAuthVector is one localized-auth-key fixture.
type kdfAuthVector struct {
	name string
	// ku is the password-to-key (hash-phase) digest, before localization.
	ku []byte
	// kul is the localized key H(ku ‖ engineID ‖ ku).
	kul []byte
}

// authKDFVectors are the externally-sourced localized auth keys for every
// auth protocol (passphrase vecPassphrase, engineID vecEngineID).
var authKDFVectors = []kdfAuthVector{
	{
		name: "MD5",
		ku:   mustHex("9faf3283884e92834ebc9847d8edd963"),
		kul:  mustHex("526f5eed9fcce26f8964c2930787d82b"),
	},
	{
		name: "SHA1",
		ku:   mustHex("9fb5cc0381497b3793528939ff788d5d79145211"),
		kul:  mustHex("6695febc9288e36282235fc7151f128497b38f3f"),
	},
	{
		name: "SHA224",
		ku:   mustHex("282a5867ee9aac639ad59df9572c7d3ac0fbc13a905b6df07dbbf00b"),
		kul:  mustHex("0bd8827c6e29f8065e08e09237f177e410f69b90e1782be682075674"),
	},
	{
		name: "SHA256",
		ku:   mustHex("ab51014d1e077f6017df2b12bee5f5aa72993177e9bb569c4dff5a4ca0b4afac"),
		kul:  mustHex("8982e0e549e866db361a6b625d84cccc11162d453ee8ce3a6445c2d6776f0f8b"),
	},
	{
		name: "SHA384",
		ku:   mustHex("e06eccdf2c68a06ed034723c9c26e0db3b669e1e2efed49150b55377a2e98f383c86fb836857444654b287c93f51ff64"),
		kul:  mustHex("3b298f16164a11184279d5432bf169e2d2a48307de02b3d3f7e2b4f36eb6f0455a53689a3937eea07319a633d2ccba78"),
	},
	{
		name: "SHA512",
		ku:   mustHex("7e4396de5aadc77be853819b98c9406265b3a9c37cc3176569847a4e4f6fba63dd3a73d04924d31a63f95a601f9385af6be4ed1b37f87d040f7c6ed6f8d38a91"),
		kul:  mustHex("22a5a36cedfcc085807a128d7bc6c2382167ad6c0dbc5fdff856740f3d84c099ad1ea87a8db096714d9788bd544047c9021e4229ce27e4c0a69250adfcffbb0b"),
	},
}

// kdfPrivVector is one localized+extended priv-key fixture.
type kdfPrivVector struct {
	name string
	// blumenthal and reeder are the localized priv keys under the two
	// extension schemes. They are equal when the auth hash is at least as
	// wide as the priv key (no extension needed).
	blumenthal []byte
	reeder     []byte
}

// privExtendVectors are the externally-sourced extended priv keys for the
// auth×priv cells that exercise key extension (passphrase vecPassphrase,
// engineID vecEngineID). The four cells where Blumenthal != Reeder (the
// auth hash is narrower than the priv key) are the ones the external oracle
// is mandatory for; self-consistency alone would pass a wrong-but-stable
// chaining.
var privExtendVectors = []kdfPrivVector{
	{
		name:       "MD5+AES192",
		blumenthal: mustHex("526f5eed9fcce26f8964c2930787d82bfa24a92467426c2f"),
		reeder:     mustHex("526f5eed9fcce26f8964c2930787d82b79eff44a90650ee0"),
	},
	{
		name:       "SHA1+AES192",
		blumenthal: mustHex("6695febc9288e36282235fc7151f128497b38f3f505e07eb"),
		reeder:     mustHex("6695febc9288e36282235fc7151f128497b38f3f9b8b6d78"),
	},
	{
		name:       "MD5+AES256",
		blumenthal: mustHex("526f5eed9fcce26f8964c2930787d82bfa24a92467426c2f4b09192be10dfaec"),
		reeder:     mustHex("526f5eed9fcce26f8964c2930787d82b79eff44a90650ee0a3a40abfac5acc12"),
	},
	{
		name:       "SHA1+AES256",
		blumenthal: mustHex("6695febc9288e36282235fc7151f128497b38f3f505e07eb9af25568fa1f5dbe"),
		reeder:     mustHex("6695febc9288e36282235fc7151f128497b38f3f9b8b6d78936ba6e7d19dfd9c"),
	},
	{
		name:       "SHA224+AES256",
		blumenthal: mustHex("0bd8827c6e29f8065e08e09237f177e410f69b90e1782be682075674e82d9bf0"),
		reeder:     mustHex("0bd8827c6e29f8065e08e09237f177e410f69b90e1782be68207567422c34be4"),
	},
	{
		// Equivalence cell: SHA-256 (32-octet hash) + AES-256 (32) needs no
		// extension, so Blumenthal == Reeder byte-for-byte.
		name:       "SHA256+AES256",
		blumenthal: mustHex("8982e0e549e866db361a6b625d84cccc11162d453ee8ce3a6445c2d6776f0f8b"),
		reeder:     mustHex("8982e0e549e866db361a6b625d84cccc11162d453ee8ce3a6445c2d6776f0f8b"),
	},
	{
		// SHA-224 (28) + AES-192 (24): no extension, schemes identical.
		name:       "SHA224+AES192",
		blumenthal: mustHex("0bd8827c6e29f8065e08e09237f177e410f69b90e1782be6"),
		reeder:     mustHex("0bd8827c6e29f8065e08e09237f177e410f69b90e1782be6"),
	},
}

// priv3DESVectors are the externally-sourced 32-octet 3DES extended keys
// (Reeder extension, the draft 3DES uses).
var priv3DESVectors = []struct {
	name string
	key  []byte
}{
	{name: "MD5+3DES", key: mustHex("526f5eed9fcce26f8964c2930787d82b79eff44a90650ee0a3a40abfac5acc12")},
	{name: "SHA1+3DES", key: mustHex("6695febc9288e36282235fc7151f128497b38f3f9b8b6d78936ba6e7d19dfd9c")},
}
