package snmp

import (
	"crypto/hmac"
	"crypto/subtle"
	"hash"

	"go.aledante.io/ae"
)

// usm_auth.go is the USM authentication (HMAC) codec. It composes
// crypto/hmac over the auth protocol's hash and crypto/subtle for the
// constant-time verify — no MAC primitive is implemented
// here. The msgAuthenticationParameters field on the wire carries the MAC
// *truncated* to a protocol-specific length (RFC 3414 §6.3.1, RFC 7860
// §4.2.2):
//
//	MD5  → 12   SHA-1  → 12   SHA-224 → 16
//	SHA-256 → 24 SHA-384 → 32  SHA-512 → 48

// authParamLen returns the wire length (octets) of the truncated
// msgAuthenticationParameters for the protocol. It errors for
// AuthProtocolNone (a noAuth message carries no auth params) and for an
// unknown protocol.
func authParamLen(proto AuthProtocol) (int, error) {
	switch proto {
	case AuthMD5, AuthSHA:
		return 12, nil
	case AuthSHA224:
		return 16, nil
	case AuthSHA256:
		return 24, nil
	case AuthSHA384:
		return 32, nil
	case AuthSHA512:
		return 48, nil
	case AuthProtocolNone:
		return 0, ae.New().Attr("proto", proto.String()).Msg("no auth parameters for AuthProtocolNone")
	default:
		return 0, ae.Wrapf("auth protocol %s", ErrUSMProtocolUnsupported, proto)
	}
}

// hmacTrunc runs body against a fresh HMAC keyed for proto, then returns the
// digest truncated to the protocol's wire length ([authParamLen]). It is the
// single home of the hash-setup + truncation convention shared by [authMAC]
// and [authMACOverZeroed]. The returned slice is freshly allocated by Sum and
// capped to its length, so a caller may neither alias nor grow it into the
// rest of the digest.
func hmacTrunc(proto AuthProtocol, key []byte, body func(mac hash.Hash)) ([]byte, error) {
	newHash, err := authHashFor(proto)
	if err != nil {
		return nil, err
	}
	trunc, err := authParamLen(proto)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(newHash, key)
	body(mac)
	return mac.Sum(nil)[:trunc:trunc], nil
}

// authMAC computes the truncated HMAC over wholeMsg under the localized
// auth key, for the given protocol. wholeMsg is the entire serialized v3
// message with the msgAuthenticationParameters field already zero-filled to
// exactly the truncated length (the caller overwrites the zeros with the
// returned MAC in place). The returned slice has length [authParamLen].
func authMAC(proto AuthProtocol, key, wholeMsg []byte) ([]byte, error) {
	return hmacTrunc(proto, key, func(mac hash.Hash) { mac.Write(wholeMsg) })
}

// authParamMaxLen is the largest truncated msgAuthenticationParameters
// length across all supported protocols (SHA-512 → 48). It bounds the
// stack-resident zero window in authMACOverZeroed.
const authParamMaxLen = 48

// authMACOverZeroed computes the same truncated HMAC as [authMAC] over the
// "auth-zeroed whole message" — wholeMsg with its msgAuthenticationParameters
// region [authStart:authEnd) treated as all-zeros (RFC 3414 §6.3.2) — but
// WITHOUT materializing that zeroed copy. It streams three segments into the
// HMAC: the bytes before the field, exactly (authEnd-authStart) zero bytes
// (the SAME length the field occupies on the wire, so the hashed message is
// byte-for-byte the canonical auth-zeroed datagram and the MAC is identical to
// the copy-based result), then the bytes after the field. wholeMsg may still
// carry the sender's MAC in that window; those bytes are skipped, never
// hashed. This removes the full-datagram copy on every inbound authenticated
// datagram.
func authMACOverZeroed(proto AuthProtocol, key, wholeMsg []byte, authStart, authEnd int) ([]byte, error) {
	window := authEnd - authStart
	// Defense-in-depth: callers gate this behind a wrong-length param check,
	// but never hash a bogus/oversized window rather than risk a panic.
	if authStart < 0 || window < 0 || authEnd > len(wholeMsg) || window > authParamMaxLen {
		return nil, ae.New().Attr("start", authStart).Attr("end", authEnd).
			Cause(ErrAuthFailed).Msg("auth parameter window out of range")
	}
	return hmacTrunc(proto, key, func(mac hash.Hash) {
		mac.Write(wholeMsg[:authStart])
		var zeros [authParamMaxLen]byte
		mac.Write(zeros[:window])
		mac.Write(wholeMsg[authEnd:])
	})
}

// verifyAuthMAC is the shared verify contract behind [authVerify] and
// [authVerifyOverZeroed]: it rejects a received parameter whose length is not
// the protocol's truncated length BEFORE any hashing (feeding
// usmStatsWrongDigests, never silently zero-padded), then computes the
// expected MAC via compute and compares it to received in constant time
// (never branch on the secret, never early-return on the first
// mismatching octet). Single-sourcing it keeps the length gate and the
// constant-time compare — the security-critical invariants — from drifting
// between the two MAC sources.
func verifyAuthMAC(proto AuthProtocol, received []byte, compute func() ([]byte, error)) error {
	trunc, err := authParamLen(proto)
	if err != nil {
		return err
	}
	if len(received) != trunc {
		return ae.New().Attr("len", len(received)).Attr("want", trunc).
			Cause(ErrAuthFailed).Msg("auth parameter wrong length")
	}
	want, err := compute()
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(want, received) != 1 {
		return ErrAuthFailed
	}
	return nil
}

// authVerifyOverZeroed is the streaming counterpart of [authVerify]: it
// recomputes the MAC via [authMACOverZeroed] (no full-datagram copy) and
// compares it to received under the shared [verifyAuthMAC] contract.
// authStart/authEnd bound the msgAuthenticationParameters content region
// within wholeMsg.
func authVerifyOverZeroed(proto AuthProtocol, key, wholeMsg []byte, authStart, authEnd int, received []byte) error {
	return verifyAuthMAC(proto, received, func() ([]byte, error) {
		return authMACOverZeroed(proto, key, wholeMsg, authStart, authEnd)
	})
}

// authVerify recomputes the truncated HMAC over wholeMsg (which must carry
// the received msgAuthenticationParameters zero-filled to the truncated
// length, exactly as [authMAC] expects) and compares it to received under the
// shared [verifyAuthMAC] contract. It is the copy-based reference verify; the
// production read path uses [authVerifyOverZeroed].
func authVerify(proto AuthProtocol, key, wholeMsg, received []byte) error {
	return verifyAuthMAC(proto, received, func() ([]byte, error) {
		return authMAC(proto, key, wholeMsg)
	})
}

// ErrAuthFailed is the leaf sentinel for an HMAC verification failure; it
// maps to usmStatsWrongDigests. It carries no key material.
var ErrAuthFailed = ae.Msg("USM authentication failed")
