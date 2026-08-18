package snmp

import (
	"crypto/md5"  //nolint:gosec // MD5 is mandated by RFC 3414 USM key derivation; not used for collision-resistant security.
	"crypto/sha1" //nolint:gosec // SHA-1 is mandated by RFC 3414 USM key derivation.
	"crypto/sha256"
	"crypto/sha512"
	"hash"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// usm_kdf.go derives the localized USM auth/priv keys from a passphrase and
// the authoritative engineID (RFC 3414 §2.6, §A.2; RFC 7860; the
// Blumenthal and Reeder AES key-extension drafts). It is pure composition
// of the stdlib hashes: no cryptographic primitive is implemented
// here — only the password-to-key expansion, the H(Ku‖engineID‖Ku)
// localization, and the two AES/3DES key-extension schemes, all of which
// are protocol serialization that *call* crypto/md5, crypto/sha1,
// crypto/sha256, and crypto/sha512.
//
// The conformance oracle for this file is usm_vectors_test.go, whose
// SHA-2 and AES-extension fixtures are captured from an independent
// implementation (pysnmp), cross-checked against plain crypto/* and the
// RFC 3414 Appendix A.3 published MD5/SHA-1 vectors — never re-derived from
// this code (which would be a circular oracle).

// kdfExpandBytes is the total number of passphrase-derived octets fed to
// the hash in the password-to-key step (RFC 3414 §A.2.1): 2^20 = 1,048,576
// bytes, processed in 64-octet blocks.
const kdfExpandBytes = 1 << 20

// authHashFor maps an [AuthProtocol] to its hash constructor. It
// returns an error for [AuthProtocolNone] (no key to derive) and for
// an unknown protocol; callers only invoke it once a non-None auth protocol
// is established by USMConfig.Validate.
func authHashFor(proto AuthProtocol) (func() hash.Hash, error) {
	switch proto {
	case AuthMD5:
		return md5.New, nil
	case AuthSHA:
		return sha1.New, nil
	case AuthSHA224:
		return sha256.New224, nil
	case AuthSHA256:
		return sha256.New, nil
	case AuthSHA384:
		return sha512.New384, nil
	case AuthSHA512:
		return sha512.New, nil
	case AuthProtocolNone:
		return nil, errs.New().Attr("proto", proto.String()).Msg("no key derivation for AuthProtocolNone")
	default:
		return nil, errs.Wrapf(ErrUSMProtocolUnsupported, "auth protocol %s", proto)
	}
}

// privKeyLen reports the number of localized-key octets a privacy protocol
// consumes: DES/AES-128 use 16, 3DES/AES-256(+C) use 32, AES-192(+C) use
// 24. The value drives whether the localized key must be extended past the
// auth hash width.
func privKeyLen(proto PrivProtocol) (int, error) {
	switch proto {
	case PrivDES, PrivAES:
		return 16, nil
	case PrivAES192, PrivAES192C:
		return 24, nil
	case Priv3DES, PrivAES256, PrivAES256C:
		return 32, nil
	case PrivProtocolNone:
		return 0, errs.New().Attr("proto", proto.String()).Msg("no key derivation for PrivProtocolNone")
	default:
		return 0, errs.Wrapf(ErrUSMProtocolUnsupported, "priv protocol %s", proto)
	}
}

// expandPassphrase implements the RFC 3414 §A.2 password-to-key expansion:
// hash exactly [kdfExpandBytes] octets formed by cycling through the
// passphrase byte-wise (passphrase[i % len] — not string
// concatenation), and return the digest Ku. The passphrase must be
// non-empty (USMConfig.Validate guarantees this whenever a protocol is
// selected); a 1-byte passphrase expands correctly.
func expandPassphrase(newHash func() hash.Hash, passphrase []byte) []byte {
	h := newHash()
	var block [64]byte
	plen := len(passphrase)
	idx := 0
	for written := 0; written < kdfExpandBytes; written += len(block) {
		for i := range block {
			block[i] = passphrase[idx%plen]
			idx++
		}
		h.Write(block[:])
	}
	return h.Sum(nil)
}

// localize folds the engineID into a key: Kul = H(key ‖ engineID ‖ key)
// (RFC 3414 §2.6). The same primitive is reused by both AES key-extension
// schemes.
func localize(newHash func() hash.Hash, key, engineID []byte) []byte {
	h := newHash()
	h.Write(key)
	h.Write(engineID)
	h.Write(key)
	return h.Sum(nil)
}

// localizedAuthKey returns the localized authentication key Kul for the
// given protocol, passphrase, and authoritative engineID. engineID must be
// known (non-empty) — discovery populates it before this is called.
func localizedAuthKey(proto AuthProtocol, passphrase string, engineID []byte) ([]byte, error) {
	newHash, err := authHashFor(proto)
	if err != nil {
		return nil, err
	}
	if len(engineID) == 0 {
		return nil, errs.Msg("key derivation requires a known engineID")
	}
	ku := expandPassphrase(newHash, []byte(passphrase))
	return localize(newHash, ku, engineID), nil
}

// localizedPrivKey returns the localized privacy key for privProto, derived
// from privPassphrase under the auth protocol's hash (USM derives the priv
// key with the auth hash) and extended to the protocol's key length via the
// scheme privProto selects:
//
//   - DES, AES-128: the localized key truncated to the key length.
//   - AES-192, AES-256: Blumenthal extension (extend the localized key with
//     H of the accumulated key; no engineID, no re-expansion).
//   - 3DES, AES-192C, AES-256C: Reeder extension (re-run the full
//     password-to-key + localization each round, chaining the growing key
//     as the next passphrase).
//
// The split only changes the bytes when the auth hash is narrower than the
// priv key (e.g. MD5/SHA-1 + AES-192/256, SHA-224 + AES-256); with a wide
// enough hash the two schemes are byte-identical.
func localizedPrivKey(authProto AuthProtocol, privProto PrivProtocol, privPassphrase string, engineID []byte) ([]byte, error) {
	newHash, err := authHashFor(authProto)
	if err != nil {
		return nil, err
	}
	need, err := privKeyLen(privProto)
	if err != nil {
		return nil, err
	}
	if len(engineID) == 0 {
		return nil, errs.Msg("key derivation requires a known engineID")
	}

	ku := expandPassphrase(newHash, []byte(privPassphrase))
	kul := localize(newHash, ku, engineID)

	switch privProto {
	case PrivDES, PrivAES:
		// The hash is always at least 16 octets, so a 16-octet key is a
		// straight truncation with no extension.
		if len(kul) < need {
			return nil, errs.New().Attr("have", len(kul)).Attr("need", need).
				Msg("localized key shorter than priv key length")
		}
		return kul[:need], nil
	case PrivAES192, PrivAES256:
		return extendBlumenthal(newHash, kul, need), nil
	case Priv3DES, PrivAES192C, PrivAES256C:
		return extendReeder(newHash, kul, engineID, need), nil
	default:
		return nil, errs.Wrapf(ErrUSMProtocolUnsupported, "priv protocol %s", privProto)
	}
}

// extendBlumenthal grows kul to at least need octets by repeatedly
// appending H(accumulated key), then truncates to need (Blumenthal AES-USM
// draft §3.1.2.1). No engineID and no password re-expansion are involved.
func extendBlumenthal(newHash func() hash.Hash, kul []byte, need int) []byte {
	out := make([]byte, len(kul))
	copy(out, kul)
	for len(out) < need {
		h := newHash()
		h.Write(out)
		out = h.Sum(out)
	}
	return out[:need]
}

// extendReeder grows kul to at least need octets the Reeder/Cisco way:
// each round re-runs the full password-to-key expansion over the current
// accumulated key, localizes the result against engineID, and appends it
// (draft-reeder-snmpv3-usm-3desede). It truncates to need.
func extendReeder(newHash func() hash.Hash, kul, engineID []byte, need int) []byte {
	out := make([]byte, len(kul))
	copy(out, kul)
	for len(out) < need {
		ku := expandPassphrase(newHash, out)
		out = append(out, localize(newHash, ku, engineID)...)
	}
	return out[:need]
}
