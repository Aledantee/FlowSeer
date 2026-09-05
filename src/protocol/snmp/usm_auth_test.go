package snmp

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// authVecKey and authVecMsg are the fixed inputs for the HMAC fixtures. The
// expected truncated MACs in authMACVectors were generated independently
// with Python's hmac module (stdlib), not the code under test.
var authVecKey = mustHex("8982e0e549e866db361a6b625d84cccc11162d453ee8ce3a6445c2d6776f0f8b")

var authVecMsg = []byte("FlowSeer USM auth conformance vector")

var authMACVectors = []struct {
	proto AuthProtocol
	trunc int
	mac   []byte
}{
	{AuthMD5, 12, mustHex("5cf419e80e77419443458055")},
	{AuthSHA, 12, mustHex("e5bc806492c1154f7b1ad567")},
	{AuthSHA224, 16, mustHex("9375a081b39577263cacfae99a0914ee")},
	{AuthSHA256, 24, mustHex("f4502a32a68ded157958d44bdcb03e22ff6000837dfffd6a")},
	{AuthSHA384, 32, mustHex("af2207289f959744b355c8c96252a28a8ef7a54716f41c8f1a2a5f8ccab3b506")},
	{AuthSHA512, 48, mustHex("bda2d05fbe24f26ae918befd9559354256544bd1b0e810dff8c53d02770ce42b61c5f143cee7c1efa75d586d25f4d5aa")},
}

// TestAuthMAC_Vectors pins each protocol's truncated MAC against the
// independent fixtures and asserts the truncation length (a SHA-256 MAC is
// exactly 24 octets, not 32).
func TestAuthMAC_Vectors(t *testing.T) {
	for _, v := range authMACVectors {
		t.Run(v.proto.String(), func(t *testing.T) {
			got, err := authMAC(v.proto, authVecKey, authVecMsg)
			if err != nil {
				t.Fatalf("authMAC: %v", err)
			}
			if len(got) != v.trunc {
				t.Fatalf("len = %d, want %d", len(got), v.trunc)
			}
			if !bytes.Equal(got, v.mac) {
				t.Fatalf("mac mismatch:\n got %x\nwant %x", got, v.mac)
			}
		})
	}
}

// TestAuthParamLen pins the wire truncation lengths per protocol.
func TestAuthParamLen(t *testing.T) {
	want := map[AuthProtocol]int{
		AuthMD5: 12, AuthSHA: 12, AuthSHA224: 16,
		AuthSHA256: 24, AuthSHA384: 32, AuthSHA512: 48,
	}
	for proto, n := range want {
		got, err := authParamLen(proto)
		if err != nil {
			t.Fatalf("%s: %v", proto, err)
		}
		if got != n {
			t.Fatalf("%s len = %d, want %d", proto, got, n)
		}
	}
	if _, err := authParamLen(AuthProtocolNone); err == nil {
		t.Fatalf("AuthProtocolNone should error")
	}
}

// TestAuthVerify_AcceptReject confirms verify accepts a correct MAC and
// rejects a single-bit-flipped one (→ usmStatsWrongDigests).
func TestAuthVerify_AcceptReject(t *testing.T) {
	for _, v := range authMACVectors {
		t.Run(v.proto.String(), func(t *testing.T) {
			good, err := authMAC(v.proto, authVecKey, authVecMsg)
			if err != nil {
				t.Fatalf("authMAC: %v", err)
			}
			if err := authVerify(v.proto, authVecKey, authVecMsg, good); err != nil {
				t.Fatalf("verify rejected a correct MAC: %v", err)
			}
			bad := bytes.Clone(good)
			bad[0] ^= 0x01
			if err := authVerify(v.proto, authVecKey, authVecMsg, bad); !errors.Is(err, ErrAuthFailed) {
				t.Fatalf("verify accepted a flipped MAC, err=%v", err)
			}
		})
	}
}

// TestAuthVerify_WrongLength confirms a received auth-param of the wrong
// length is rejected (never silently zero-padded).
func TestAuthVerify_WrongLength(t *testing.T) {
	good, err := authMAC(AuthSHA256, authVecKey, authVecMsg)
	if err != nil {
		t.Fatalf("authMAC: %v", err)
	}
	// Too short and too long both rejected.
	if err := authVerify(AuthSHA256, authVecKey, authVecMsg, good[:23]); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("short param not rejected: %v", err)
	}
	if err := authVerify(AuthSHA256, authVecKey, authVecMsg, append(bytes.Clone(good), 0x00)); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("long param not rejected: %v", err)
	}
}

// TestAuthMACOverZeroed_MatchesCopy is the byte-identity gate for the
// streaming HMAC: authMACOverZeroed must produce the exact same MAC as the
// copy-based authMAC over a message whose auth-param window is zero-filled,
// for every protocol and every field placement (start, middle, end). The
// copy-based reference is itself pinned to the independent Python vectors by
// TestAuthMAC_Vectors, so equality here transitively pins the streaming MAC
// to those vectors. The window on the wire still carries the sender's MAC;
// streaming must skip it, hashing zeros of the same length instead.
func TestAuthMACOverZeroed_MatchesCopy(t *testing.T) {
	prefixes := []int{0, 1, 7, 64} // bytes before the auth-param window
	for _, v := range authMACVectors {
		for _, pre := range prefixes {
			t.Run(fmt.Sprintf("%s/prefix=%d", v.proto, pre), func(t *testing.T) {
				head := bytes.Repeat([]byte{0xAB}, pre)
				tail := []byte("trailing scopedPDU bytes after the auth window")
				window := bytes.Repeat([]byte{0x5A}, v.trunc) // sender's MAC on the wire
				whole := append(append(bytes.Clone(head), window...), tail...)
				authStart, authEnd := len(head), len(head)+v.trunc

				// Reference: copy, zero the window in place, copy-based MAC.
				zeroed := bytes.Clone(whole)
				for i := authStart; i < authEnd; i++ {
					zeroed[i] = 0
				}
				want, err := authMAC(v.proto, authVecKey, zeroed)
				if err != nil {
					t.Fatalf("authMAC: %v", err)
				}

				got, err := authMACOverZeroed(v.proto, authVecKey, whole, authStart, authEnd)
				if err != nil {
					t.Fatalf("authMACOverZeroed: %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("streaming MAC != copy MAC:\n got %x\nwant %x", got, want)
				}
			})
		}
	}
}

// TestAuthMACOverZeroed_RejectsBadWindow locks the defense-in-depth guard: an
// out-of-range msgAuthenticationParameters window returns a typed
// ErrAuthFailed, never a slice panic. Production gates this via the
// length-check in verifyAuthMAC, but a future direct caller might not — and a
// panic on a forged datagram would be a remote DoS.
func TestAuthMACOverZeroed_RejectsBadWindow(t *testing.T) {
	whole := make([]byte, 100)
	cases := []struct {
		name       string
		start, end int
	}{
		{"window exceeds max (isolated)", 0, authParamMaxLen + 1}, // end < len, only window>max trips
		{"end past message", 8, 200},
		{"negative start", -1, 4},
		{"start after end", 40, 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := authMACOverZeroed(AuthSHA, authVecKey, whole, c.start, c.end); !errors.Is(err, ErrAuthFailed) {
				t.Fatalf("got %v, want ErrAuthFailed (no panic)", err)
			}
		})
	}
}

// TestAuthVerifyOverZeroed_AcceptReject confirms the streaming verify accepts
// a correct MAC, rejects a single-bit flip in constant time
// (→ usmStatsWrongDigests), and rejects a wrong-length param before hashing.
func TestAuthVerifyOverZeroed_AcceptReject(t *testing.T) {
	for _, v := range authMACVectors {
		t.Run(v.proto.String(), func(t *testing.T) {
			head := []byte("v3 header bytes")
			tail := []byte("scopedPDU")
			window := make([]byte, v.trunc)
			whole := append(append(bytes.Clone(head), window...), tail...)
			authStart, authEnd := len(head), len(head)+v.trunc

			good, err := authMACOverZeroed(v.proto, authVecKey, whole, authStart, authEnd)
			if err != nil {
				t.Fatalf("authMACOverZeroed: %v", err)
			}
			// Place the MAC on the wire (as a real sender would).
			copy(whole[authStart:authEnd], good)

			if err := authVerifyOverZeroed(v.proto, authVecKey, whole, authStart, authEnd, good); err != nil {
				t.Fatalf("verify rejected a correct MAC: %v", err)
			}
			bad := bytes.Clone(good)
			bad[0] ^= 0x01
			if err := authVerifyOverZeroed(v.proto, authVecKey, whole, authStart, authEnd, bad); !errors.Is(err, ErrAuthFailed) {
				t.Fatalf("verify accepted a flipped MAC, err=%v", err)
			}
			if err := authVerifyOverZeroed(v.proto, authVecKey, whole, authStart, authEnd, good[:len(good)-1]); !errors.Is(err, ErrAuthFailed) {
				t.Fatalf("verify accepted a short param, err=%v", err)
			}
		})
	}
}

// TestAuthVerify_ZeroFillPlacement confirms compute and verify agree on the
// field placement: a MAC computed with the field zero-filled at the
// truncated length verifies against the same zero-filled message. This is
// the round-trip the security processor relies on.
func TestAuthVerify_ZeroFillPlacement(t *testing.T) {
	for _, v := range authMACVectors {
		t.Run(v.proto.String(), func(t *testing.T) {
			// Simulate the wire layout: a message body with a zero-filled
			// auth-param window of the truncated length.
			msg := bytes.Clone(authVecMsg)
			window := make([]byte, v.trunc) // the zero-filled field
			full := append(bytes.Clone(msg), window...)
			mac, err := authMAC(v.proto, authVecKey, full)
			if err != nil {
				t.Fatalf("authMAC: %v", err)
			}
			// Overwrite the window in place, as the security
			// processor does.
			copy(full[len(msg):], mac)
			// Re-zero the window and verify the recomputed MAC matches.
			recv := bytes.Clone(mac)
			copy(full[len(msg):], window)
			if err := authVerify(v.proto, authVecKey, full, recv); err != nil {
				t.Fatalf("verify failed after in-place overwrite: %v", err)
			}
		})
	}
}
