package snmp

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des" //nolint:gosec // DES/3DES are mandated by RFC 3414/3826 USM privacy; legacy interop only.
	crand "crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"sync/atomic"

	"go.aledante.io/as"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// usm_priv.go is the USM privacy (encryption) codec. It composes
// crypto/des, crypto/aes, and crypto/cipher block modes with the RFC
// 3414 §8 (DES-CBC), draft-reeder (3DES-EDE-CBC), and RFC 3826 (AES-CFB128)
// IV/salt construction — no cipher or mode is implemented here.
//
// IV/salt layout:
//
//	DES-CBC   key=Kul[0:8]  preIV=Kul[8:16]  salt(8)=boots‖localInt  IV=preIV XOR salt
//	3DES-CBC  key=Kul[0:24] preIV=Kul[24:32] salt(8)=boots‖localInt  IV=preIV XOR salt
//	AES-CFB   key=Kul[0:keylen]               salt(8)=localCounter    IV=boots‖time‖salt
//
// The per-context salt counter MUST be atomic, not merely monotonic: the
// reactor allows many concurrent in-flight authPriv encrypts per session,
// and two racing encrypts sharing a salt would reuse an IV under one key —
// a confidentiality break.

// ErrPrivDecrypt is the leaf sentinel for any privacy-layer decrypt
// failure (bad ciphertext length, truncated salt). It maps to
// usmStatsDecryptionErrors. It carries no key material.
var ErrPrivDecrypt = errs.Msg("USM decryption failed")

// privContext holds the localized priv key and the atomic salt counter for
// one session or engine entry. The counter is seeded from crypto/rand so a
// fresh context does not start every key's salt sequence at a predictable
// value.
type privContext struct {
	proto  PrivProtocol
	key    []byte
	salt   atomic.Uint64
	logCtx context.Context
}

// newPrivContext builds a priv context for proto with the supplied
// localized+extended key, validating the key length and seeding the salt
// counter from crypto/rand. For 3DES it logs (does not reject) a degenerate
// key whose sub-keys are not all distinct — Net-SNMP and the Reeder draft
// accept it, so a hard reject would be a unilateral interop divergence.
func newPrivContext(ctx context.Context, proto PrivProtocol, key []byte) (*privContext, error) {
	need, err := privKeyLen(proto)
	if err != nil {
		return nil, err
	}
	if len(key) != need {
		return nil, errs.New().Attr("have", len(key)).Attr("need", need).Attr("proto", proto.String()).
			Msg("priv key length mismatch")
	}
	pc := &privContext{proto: proto, key: key, logCtx: context.WithoutCancel(ctx)}
	var seed [8]byte
	if _, err := crand.Read(seed[:]); err == nil {
		pc.salt.Store(binary.BigEndian.Uint64(seed[:]))
	}
	if proto == Priv3DES && weakTripleDESKey(key) {
		as.Logger(pc.logCtx).WarnContext(pc.logCtx,
			"snmp: 3DES localized key has non-distinct sub-keys (accepted for interop)")
	}
	return pc, nil
}

// weakTripleDESKey reports whether any two of the three 8-octet DES
// sub-keys in a 3DES key are equal (a degenerate key that reduces 3DES's
// effective strength).
func weakTripleDESKey(key []byte) bool {
	if len(key) < 24 {
		return false
	}
	k1, k2, k3 := key[0:8], key[8:16], key[16:24]
	return subtle.ConstantTimeCompare(k1, k2) == 1 ||
		subtle.ConstantTimeCompare(k2, k3) == 1 ||
		subtle.ConstantTimeCompare(k1, k3) == 1
}

// nextSalt8 returns the next 8-octet msgPrivacyParameters for an outbound
// message, advancing the atomic counter. For DES/3DES the low 32 bits are
// the localInt and boots occupies the high 4 octets; for AES the whole
// 64-bit counter is the salt.
func (pc *privContext) nextSalt8(boots uint32) []byte {
	n := pc.salt.Add(1)
	salt := make([]byte, 8)
	switch pc.proto {
	case PrivDES, Priv3DES:
		binary.BigEndian.PutUint32(salt[0:4], boots)
		binary.BigEndian.PutUint32(salt[4:8], uint32(n))
	default: // AES family
		binary.BigEndian.PutUint64(salt, n)
	}
	return salt
}

// encrypt encrypts plaintext (the serialized scoped PDU) under the context,
// allocating a fresh atomic salt. It returns the ciphertext and the
// msgPrivacyParameters to place on the wire.
func (pc *privContext) encrypt(boots, engineTime uint32, plaintext []byte) (ciphertext, privParams []byte, err error) {
	salt := pc.nextSalt8(boots)
	switch pc.proto {
	case PrivDES, Priv3DES:
		ct, err := cbcEncrypt(pc.proto, pc.key, salt, plaintext)
		return ct, salt, err
	case PrivAES, PrivAES192, PrivAES256, PrivAES192C, PrivAES256C:
		ct, err := aesCFB(pc.key, boots, engineTime, salt, plaintext, false)
		return ct, salt, err
	default:
		return nil, nil, errs.Wrapf(ErrUSMProtocolUnsupported, "priv protocol %s", pc.proto)
	}
}

// decrypt reverses [privContext.encrypt] using the received
// msgPrivacyParameters (the salt). For DES/3DES the returned plaintext
// still carries CBC padding; the caller recovers the scoped PDU by BER
// length and ignores trailing octets.
func (pc *privContext) decrypt(boots, engineTime uint32, privParams, ciphertext []byte) ([]byte, error) {
	switch pc.proto {
	case PrivDES, Priv3DES:
		return cbcDecrypt(pc.proto, pc.key, privParams, ciphertext)
	case PrivAES, PrivAES192, PrivAES256, PrivAES192C, PrivAES256C:
		if len(privParams) != 8 {
			return nil, errs.New().Attr("len", len(privParams)).Cause(ErrPrivDecrypt).Msg("AES salt wrong length")
		}
		return aesCFB(pc.key, boots, engineTime, privParams, ciphertext, true)
	default:
		return nil, errs.Wrapf(ErrUSMProtocolUnsupported, "priv protocol %s", pc.proto)
	}
}

// cbcBlock builds the DES or 3DES block cipher and returns it with the
// 8-octet pre-IV slice from the key.
func cbcBlock(proto PrivProtocol, key []byte) (cipher.Block, []byte, error) {
	switch proto {
	case PrivDES:
		if len(key) < 16 {
			return nil, nil, errs.New().Cause(ErrPrivDecrypt).Msg("DES key too short")
		}
		blk, err := des.NewCipher(key[0:8])
		if err != nil {
			return nil, nil, errs.Wrap(err, "DES cipher")
		}
		return blk, key[8:16], nil
	case Priv3DES:
		if len(key) < 32 {
			return nil, nil, errs.New().Cause(ErrPrivDecrypt).Msg("3DES key too short")
		}
		blk, err := des.NewTripleDESCipher(key[0:24])
		if err != nil {
			return nil, nil, errs.Wrap(err, "3DES cipher")
		}
		return blk, key[24:32], nil
	default:
		return nil, nil, errs.Wrapf(ErrUSMProtocolUnsupported, "priv protocol %s", proto)
	}
}

// cbcEncrypt encrypts plaintext with DES/3DES-CBC: IV = preIV XOR salt,
// plaintext zero-padded to the 8-octet block size (RFC 3414 §8.1.1.2).
func cbcEncrypt(proto PrivProtocol, key, salt, plaintext []byte) ([]byte, error) {
	blk, preIV, err := cbcBlock(proto, key)
	if err != nil {
		return nil, err
	}
	if len(salt) != 8 {
		return nil, errs.New().Attr("len", len(salt)).Cause(ErrPrivDecrypt).Msg("CBC salt wrong length")
	}
	iv := xorBytes(preIV, salt)
	bs := blk.BlockSize()
	padded := plaintext
	if rem := len(plaintext) % bs; rem != 0 {
		padded = make([]byte, len(plaintext)+bs-rem)
		copy(padded, plaintext)
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(blk, iv).CryptBlocks(out, padded)
	return out, nil
}

// cbcDecrypt reverses [cbcEncrypt]. A ciphertext that is not a whole number
// of blocks, or a wrong-length salt, is a typed error, never a panic.
func cbcDecrypt(proto PrivProtocol, key, salt, ciphertext []byte) ([]byte, error) {
	blk, preIV, err := cbcBlock(proto, key)
	if err != nil {
		return nil, err
	}
	if len(salt) != 8 {
		return nil, errs.New().Attr("len", len(salt)).Cause(ErrPrivDecrypt).Msg("CBC salt wrong length")
	}
	bs := blk.BlockSize()
	if len(ciphertext) == 0 || len(ciphertext)%bs != 0 {
		return nil, errs.New().Attr("len", len(ciphertext)).Cause(ErrPrivDecrypt).Msg("CBC ciphertext not a block multiple")
	}
	iv := xorBytes(preIV, salt)
	out := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(blk, iv).CryptBlocks(out, ciphertext)
	return out, nil
}

// aesCFB runs AES-CFB128 with IV = boots(4B BE) ‖ time(4B BE) ‖ salt(8B).
// CFB feedback differs between directions, so decrypt must use the
// decrypter stream (not the encrypter); the decrypt flag selects it.
func aesCFB(key []byte, boots, engineTime uint32, salt, in []byte, decrypt bool) ([]byte, error) {
	if len(salt) != 8 {
		return nil, errs.New().Attr("len", len(salt)).Cause(ErrPrivDecrypt).Msg("AES salt wrong length")
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, errs.Wrap(err, "AES cipher")
	}
	iv := make([]byte, 16)
	binary.BigEndian.PutUint32(iv[0:4], boots)
	binary.BigEndian.PutUint32(iv[4:8], engineTime)
	copy(iv[8:16], salt)
	out := make([]byte, len(in))
	var stream cipher.Stream
	if decrypt {
		stream = cipher.NewCFBDecrypter(blk, iv) //nolint:staticcheck // CFB is mandated by RFC 3826 AES-USM.
	} else {
		stream = cipher.NewCFBEncrypter(blk, iv) //nolint:staticcheck // CFB is mandated by RFC 3826 AES-USM.
	}
	stream.XORKeyStream(out, in)
	return out, nil
}

// xorBytes returns the byte-wise XOR of two equal-length slices (the DES IV
// construction). The inputs are equal-length by construction (8 octets).
func xorBytes(a, b []byte) []byte {
	out := make([]byte, len(a))
	subtle.XORBytes(out, a, b)
	return out
}
