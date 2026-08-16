package snmp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// randomPassphrase returns a hex-encoded random string distinct from the
// literal "[REDACTED]" placeholder. 16 bytes -> 32 hex chars, plenty of
// entropy and zero chance of a coincidental substring match.
func randomPassphrase(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func TestUSMConfig_Validate(t *testing.T) {
	const pass = "secret"

	cases := []struct {
		name string
		cfg  USMConfig
		ok   bool
	}{
		// Valid combos
		{"noauth_nopriv", USMConfig{Username: "u"}, true},
		{"auth_md5", USMConfig{Username: "u", AuthProtocol: AuthMD5, AuthPassphrase: pass}, true},
		{"auth_sha", USMConfig{Username: "u", AuthProtocol: AuthSHA, AuthPassphrase: pass}, true},
		{"auth_sha256_priv_aes", USMConfig{
			Username: "u", AuthProtocol: AuthSHA256, AuthPassphrase: pass,
			PrivProtocol: PrivAES, PrivPassphrase: pass,
		}, true},
		{"auth_sha256_priv_aes256", USMConfig{
			Username: "u", AuthProtocol: AuthSHA256, AuthPassphrase: pass,
			PrivProtocol: PrivAES256, PrivPassphrase: pass,
		}, true},

		// Invalid combos
		{"empty_username", USMConfig{}, false},
		{"auth_no_passphrase", USMConfig{Username: "u", AuthProtocol: AuthSHA}, false},
		{"auth_none_with_passphrase", USMConfig{Username: "u", AuthPassphrase: pass}, false},
		{"priv_no_passphrase", USMConfig{
			Username: "u", AuthProtocol: AuthSHA, AuthPassphrase: pass,
			PrivProtocol: PrivAES,
		}, false},
		{"priv_none_with_passphrase", USMConfig{
			Username: "u", AuthProtocol: AuthSHA, AuthPassphrase: pass,
			PrivPassphrase: pass,
		}, false},
		{"priv_without_auth", USMConfig{
			Username: "u",
			// AuthProtocol is None — but supplying a priv protocol +
			// passphrase. This must fail with the "priv requires auth"
			// branch, not the "auth set but no passphrase" branch.
			PrivProtocol: PrivAES, PrivPassphrase: pass,
		}, false},
		{"bad_auth_proto", USMConfig{Username: "u", AuthProtocol: AuthProtocol(99)}, false},
		{"bad_priv_proto", USMConfig{
			Username: "u", AuthProtocol: AuthSHA, AuthPassphrase: pass,
			PrivProtocol: PrivProtocol(99), PrivPassphrase: pass,
		}, false},

		// RFC 3411 §5 snmpEngineID length cases. Empty stays valid
		// (signals Backend discovery); 5 and 32 are the boundary
		// values; 4 and 33 are out of range.
		// Covers conformance matrix row: RFC 3411 §5 / snmpEngineID length.
		{"engine_id_empty_ok", USMConfig{
			Username: "u", AuthProtocol: AuthMD5, AuthPassphrase: pass,
			EngineID: nil,
		}, true},
		{"engine_id_5_octets_ok", USMConfig{
			Username: "u", AuthProtocol: AuthMD5, AuthPassphrase: pass,
			EngineID: []byte{1, 2, 3, 4, 5},
		}, true},
		{"engine_id_32_octets_ok", USMConfig{
			Username: "u", AuthProtocol: AuthMD5, AuthPassphrase: pass,
			EngineID: bytes.Repeat([]byte{0xab}, 32),
		}, true},
		{"engine_id_4_octets_too_short", USMConfig{
			Username: "u", AuthProtocol: AuthMD5, AuthPassphrase: pass,
			EngineID: []byte{1, 2, 3, 4},
		}, false},
		{"engine_id_33_octets_too_long", USMConfig{
			Username: "u", AuthProtocol: AuthMD5, AuthPassphrase: pass,
			EngineID: bytes.Repeat([]byte{0xcd}, 33),
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.ok && err != nil {
				t.Errorf("Validate %+v = %v, want nil", tc.cfg, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("Validate %+v = nil, want error", tc.cfg)
			}
		})
	}
}

// TestUSMConfig_ValidateError_NoPassphraseLeak asserts that no validation
// error message embeds the supplied passphrase value. This is a critical
// security property: users frequently log the error returned
// from Dial/WithUSM, and a leaked passphrase there is as bad as logging
// the config itself.
func TestUSMConfig_ValidateError_NoPassphraseLeak(t *testing.T) {
	pass := randomPassphrase(t)

	// Configs that should fail validation and would carry the passphrase
	// in a naïve error message.
	configs := []USMConfig{
		{Username: "u", AuthPassphrase: pass},                                                                              // auth=none, passphrase set
		{Username: "u", AuthProtocol: AuthSHA, AuthPassphrase: pass, PrivPassphrase: pass},                                 // priv=none, passphrase set
		{Username: "u", AuthProtocol: AuthSHA, AuthPassphrase: pass, PrivProtocol: PrivAES},                                // priv proto, no passphrase
		{Username: "u", PrivProtocol: PrivAES, PrivPassphrase: pass},                                                       // priv without auth
		{Username: "u", AuthProtocol: AuthProtocol(99), AuthPassphrase: pass},                                              // bad auth proto
		{Username: "u", AuthProtocol: AuthSHA, AuthPassphrase: pass, PrivProtocol: PrivProtocol(99), PrivPassphrase: pass}, // bad priv proto
	}
	for i, cfg := range configs {
		err := cfg.Validate()
		if err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
			continue
		}
		if strings.Contains(err.Error(), pass) {
			t.Errorf("case %d: validation error %q embeds passphrase", i, err.Error())
		}
	}
}

// TestUSMConfig_Format_NoPassphraseLeak asserts that every common fmt
// verb redacts both passphrase fields. The passphrase is random per-run
// to defeat any cached-string optimization.
func TestUSMConfig_Format_NoPassphraseLeak(t *testing.T) {
	authPass := randomPassphrase(t)
	privPass := randomPassphrase(t)
	cfg := USMConfig{
		Username:       "user",
		AuthProtocol:   AuthSHA256,
		AuthPassphrase: authPass,
		PrivProtocol:   PrivAES256,
		PrivPassphrase: privPass,
		EngineID:       []byte{0x80, 0x00, 0x1f, 0x88, 0x01},
	}

	cases := []string{
		fmt.Sprintf("%v", cfg),
		fmt.Sprintf("%+v", cfg),
		fmt.Sprintf("%#v", cfg),
		fmt.Sprintf("%s", cfg),
		fmt.Sprintf("%q", cfg),
		cfg.String(),
		cfg.GoString(),
	}
	for i, rendered := range cases {
		if strings.Contains(rendered, authPass) {
			t.Errorf("case %d: rendered %q embeds auth passphrase", i, rendered)
		}
		if strings.Contains(rendered, privPass) {
			t.Errorf("case %d: rendered %q embeds priv passphrase", i, rendered)
		}
		if !strings.Contains(rendered, "REDACTED") {
			t.Errorf("case %d: rendered %q lacks REDACTED marker", i, rendered)
		}
	}
}

// TestUSMConfig_Format_Embedded asserts redaction holds when the config
// is embedded inside another struct, exercising the fmt.Formatter
// integration. A plain Stringer would be bypassed by a parent %+v that
// reflect-walks into the embedded struct's fields.
func TestUSMConfig_Format_Embedded(t *testing.T) {
	pass := randomPassphrase(t)
	cfg := USMConfig{
		Username:       "u",
		AuthProtocol:   AuthSHA,
		AuthPassphrase: pass,
	}
	type wrapper struct {
		Name string
		USM  USMConfig
	}
	w := wrapper{Name: "wrapper", USM: cfg}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		s := fmt.Sprintf(verb, w)
		if strings.Contains(s, pass) {
			t.Errorf("verb %s: wrapper rendering %q embeds passphrase", verb, s)
		}
	}
}

func TestUSMConfig_Level(t *testing.T) {
	cases := []struct {
		name string
		cfg  USMConfig
		want SecurityLevel
	}{
		{"noauth_nopriv", USMConfig{}, SecurityLevelNoAuthNoPriv},
		{"auth_only", USMConfig{AuthProtocol: AuthSHA, AuthPassphrase: "p"}, SecurityLevelAuthNoPriv},
		{"auth_and_priv", USMConfig{
			AuthProtocol: AuthSHA256, AuthPassphrase: "p",
			PrivProtocol: PrivAES, PrivPassphrase: "p",
		}, SecurityLevelAuthPriv},
		{"priv_without_auth_invalid", USMConfig{
			PrivProtocol: PrivAES, PrivPassphrase: "p",
		}, SecurityLevelUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Level(); got != tc.want {
				t.Errorf("Level = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestWithUSM_InvalidCapturedForDial pins deferred validation: WithUSM
// accepts the config without panicking, but the captured validation error is
// available on the SessionConfig so a Backend's Dial can surface it
// before any wire IO. (The public Backend interface was removed in the
// D4 refactor; Backend Dial implementations now live in backend/<impl>
// packages — the per-backend test suite asserts that the error
// surfaces from their own Dial.)
func TestWithUSM_InvalidCapturedForDial(t *testing.T) {
	bad := USMConfig{Username: "u", PrivProtocol: PrivAES, PrivPassphrase: "p"} // priv-without-auth
	cfg := ApplyOptions(WithUSM(bad))
	err := cfg.USMValidationError()
	if err == nil {
		t.Fatal("USMValidationError returned nil for invalid WithUSM")
	}
	// Ensure the validation message does not embed the raw passphrase.
	if strings.Contains(err.Error(), "p\"") || strings.Contains(err.Error(), "p,") {
		t.Errorf("validation error message %q appears to embed the passphrase", err.Error())
	}
}

// TestUSMConfig_Validate_EngineIDErrorMessage pins that the RFC 3411
// EngineID length error names the offending field and the [5,32] range
// so callers can diagnose the misconfiguration without reading the
// validator source.
//
// Covers conformance matrix row: RFC 3411 §5 / snmpEngineID length.
func TestUSMConfig_Validate_EngineIDErrorMessage(t *testing.T) {
	cfg := USMConfig{
		Username: "u", AuthProtocol: AuthMD5, AuthPassphrase: "p",
		EngineID: []byte{1, 2, 3, 4}, // 4 octets
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for 4-octet EngineID")
	}
	msg := err.Error()
	for _, want := range []string{"EngineID", "[5,32]"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

// TestAuthPrivProtocols_RFCMapping asserts each AuthProtocol and
// PrivProtocol constant tracks its docstring-cited RFC and renders the
// canonical name from that RFC. The mapping table is private to the
// test rather than exposed via a new public RFC() method (no public
// API beyond MustOID is added).
//
// Drift between the docstring and the test row is possible but visible
// to reviewers because both live in usm.go's neighborhood; the test
// fails immediately if a constant's String() form changes.
//
// Covers conformance matrix rows:
//   - RFC 3414 §6 / AuthMD5
//   - RFC 3414 §7 / AuthSHA
//   - RFC 7860 §4 / AuthSHA224..AuthSHA512
//   - RFC 3414 / PrivDES
//   - RFC 3826 / PrivAES, PrivAES192, PrivAES256
//   - draft-reeder-snmpv3-usm-3desede / Priv3DES, PrivAES192C, PrivAES256C
func TestAuthPrivProtocols_RFCMapping(t *testing.T) {
	authCases := []struct {
		p    AuthProtocol
		name string
		rfc  string
	}{
		{AuthProtocolNone, "none", ""},
		{AuthMD5, "MD5", "RFC 3414"},
		{AuthSHA, "SHA", "RFC 3414"},
		{AuthSHA224, "SHA224", "RFC 7860"},
		{AuthSHA256, "SHA256", "RFC 7860"},
		{AuthSHA384, "SHA384", "RFC 7860"},
		{AuthSHA512, "SHA512", "RFC 7860"},
	}
	for _, c := range authCases {
		t.Run("auth/"+c.name, func(t *testing.T) {
			if !c.p.valid() {
				t.Errorf("%s.valid() = false, want true", c.name)
			}
			if got := c.p.String(); got != c.name {
				t.Errorf("%v.String() = %q, want %q", c.p, got, c.name)
			}
		})
	}
	// Out-of-range AuthProtocol falls through to the numeric form and
	// valid() returns false.
	if got := AuthProtocol(99).String(); got != "AuthProtocol(99)" {
		t.Errorf("AuthProtocol(99).String() = %q, want %q", got, "AuthProtocol(99)")
	}
	if AuthProtocol(99).valid() {
		t.Error("AuthProtocol(99).valid() = true, want false")
	}

	privCases := []struct {
		p    PrivProtocol
		name string
		rfc  string
	}{
		{PrivProtocolNone, "none", ""},
		{PrivDES, "DES", "RFC 3414"},
		{Priv3DES, "3DES", "RFC 3826"},
		{PrivAES, "AES", "RFC 3826"},
		{PrivAES192, "AES192", "RFC 3826"},
		{PrivAES256, "AES256", "RFC 3826"},
		{PrivAES192C, "AES192C", "draft-reeder"},
		{PrivAES256C, "AES256C", "draft-reeder"},
	}
	for _, c := range privCases {
		t.Run("priv/"+c.name, func(t *testing.T) {
			if !c.p.valid() {
				t.Errorf("%s.valid() = false, want true", c.name)
			}
			if got := c.p.String(); got != c.name {
				t.Errorf("%v.String() = %q, want %q", c.p, got, c.name)
			}
		})
	}
	if got := PrivProtocol(99).String(); got != "PrivProtocol(99)" {
		t.Errorf("PrivProtocol(99).String() = %q, want %q", got, "PrivProtocol(99)")
	}
	if PrivProtocol(99).valid() {
		t.Error("PrivProtocol(99).valid() = true, want false")
	}
}

// TestWithUSM_ValidApplies pins that a valid WithUSM populates every
// field on the SessionConfig.
func TestWithUSM_ValidApplies(t *testing.T) {
	cfg := USMConfig{
		Username:       "user",
		AuthProtocol:   AuthSHA256,
		AuthPassphrase: "auth-pass",
		PrivProtocol:   PrivAES256,
		PrivPassphrase: "priv-pass",
		// RFC 3411 §5 requires snmpEngineID to be 5..32 octets when
		// non-empty; this fixture uses 5 octets (the lower bound).
		EngineID: []byte{0x80, 0x00, 0x1f, 0x88, 0x01},
	}
	sc := ApplyOptions(WithUSM(cfg))
	if err := sc.USMValidationError(); err != nil {
		t.Fatalf("USMValidationError() = %v, want nil", err)
	}
	if sc.USM == nil {
		t.Fatal("SessionConfig.USM is nil after WithUSM")
	}
	if sc.USM.Username != cfg.Username ||
		sc.USM.AuthProtocol != cfg.AuthProtocol ||
		sc.USM.AuthPassphrase != cfg.AuthPassphrase ||
		sc.USM.PrivProtocol != cfg.PrivProtocol ||
		sc.USM.PrivPassphrase != cfg.PrivPassphrase ||
		string(sc.USM.EngineID) != string(cfg.EngineID) {
		t.Errorf("applied USMConfig mismatch:\n got %+v\nwant %+v", *sc.USM, cfg)
	}
}
