package snmp

import (
	"fmt"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
)

// SecurityLevel is the effective SNMPv3 USM security level implied by a
// [USMConfig]'s authentication and privacy protocol choices. The values
// correspond to RFC 3414 securityLevel.
type SecurityLevel int

const (
	// SecurityLevelUnknown is the zero value; the level has not been
	// determined yet (or the config is invalid).
	SecurityLevelUnknown SecurityLevel = iota
	// SecurityLevelNoAuthNoPriv selects no authentication and no privacy.
	SecurityLevelNoAuthNoPriv
	// SecurityLevelAuthNoPriv selects authentication without privacy.
	SecurityLevelAuthNoPriv
	// SecurityLevelAuthPriv selects authentication with privacy.
	SecurityLevelAuthPriv
)

// String returns a short identifier for the security level.
func (l SecurityLevel) String() string {
	switch l {
	case SecurityLevelUnknown:
		return "unknown"
	case SecurityLevelNoAuthNoPriv:
		return "noAuthNoPriv"
	case SecurityLevelAuthNoPriv:
		return "authNoPriv"
	case SecurityLevelAuthPriv:
		return "authPriv"
	}
	return fmt.Sprintf("SecurityLevel(%d)", int(l))
}

// MinSecurity is the floor that a Session's effective [SecurityLevel] must
// meet or exceed. It is configured at Dial time via [WithMinSecurity]; the
// default is [MinSecurityAuthNoPriv]. [NewSession] is rejected with
// [ErrSecurityPolicy] when the active USM configuration falls below the
// floor.
type MinSecurity int

const (
	// MinSecurityUnset is the zero value; [NewSession] substitutes the
	// default [MinSecurityAuthNoPriv] when it sees this value.
	MinSecurityUnset MinSecurity = iota
	// MinSecurityNoAuth accepts noAuthNoPriv. This is an explicit opt-in
	// for test setups; production callers should never select it.
	MinSecurityNoAuth
	// MinSecurityAuthNoPriv requires authentication; privacy is optional.
	// This is the default.
	MinSecurityAuthNoPriv
	// MinSecurityAuthPriv requires both authentication and privacy.
	MinSecurityAuthPriv
)

// String returns a short identifier for the minimum-security floor.
func (m MinSecurity) String() string {
	switch m {
	case MinSecurityUnset:
		return "unset"
	case MinSecurityNoAuth:
		return "noAuth"
	case MinSecurityAuthNoPriv:
		return "authNoPriv"
	case MinSecurityAuthPriv:
		return "authPriv"
	}
	return fmt.Sprintf("MinSecurity(%d)", int(m))
}

// AuthProtocol identifies an SNMPv3 USM authentication protocol.
// [AuthProtocolNone] disables authentication.
type AuthProtocol int

const (
	// AuthProtocolNone disables authentication.
	AuthProtocolNone AuthProtocol = iota
	// AuthMD5 selects HMAC-MD5-96 (RFC 3414).
	AuthMD5
	// AuthSHA selects HMAC-SHA1-96 (RFC 3414).
	AuthSHA
	// AuthSHA224 selects HMAC-SHA-224 (RFC 7860).
	AuthSHA224
	// AuthSHA256 selects HMAC-SHA-256 (RFC 7860).
	AuthSHA256
	// AuthSHA384 selects HMAC-SHA-384 (RFC 7860).
	AuthSHA384
	// AuthSHA512 selects HMAC-SHA-512 (RFC 7860).
	AuthSHA512
)

// String returns the canonical name of the authentication protocol.
func (a AuthProtocol) String() string {
	switch a {
	case AuthProtocolNone:
		return "none"
	case AuthMD5:
		return "MD5"
	case AuthSHA:
		return "SHA"
	case AuthSHA224:
		return "SHA224"
	case AuthSHA256:
		return "SHA256"
	case AuthSHA384:
		return "SHA384"
	case AuthSHA512:
		return "SHA512"
	}
	return fmt.Sprintf("AuthProtocol(%d)", int(a))
}

// valid reports whether the AuthProtocol value is a defined constant.
func (a AuthProtocol) valid() bool {
	return a >= AuthProtocolNone && a <= AuthSHA512
}

// ErrUSMProtocolUnsupported is returned by [NewSession] when the
// requested [AuthProtocol] or [PrivProtocol] is defined in the
// library's enum but not implemented.
//
// Callers match this with [errors.Is] to detect "this implementation
// cannot realize the configured USM choice" without coupling to a
// specific error message. The wrapped message names the offending
// protocol; the sentinel is what callers test against.
//
// Example — NewSession rejects [Priv3DES] before any wire IO:
//
//	_, err := snmp.NewSession(ctx, target, snmp.V3, snmp.WithUSM(snmp.USMConfig{
//	    PrivProtocol: snmp.Priv3DES, ...,
//	}))
//	if !errors.Is(err, snmp.ErrUSMProtocolUnsupported) {
//	    t.Fatalf("got %v, want wrapped ErrUSMProtocolUnsupported", err)
//	}
//
// The rejection is wrapped with this sentinel via [errs.Wrap]; [errors.Is]
// matches through the full wrap chain (errs' multi-error Unwrap is walked
// recursively by Go's errors package), so do not rely on err.Error()
// string-matching to detect the condition.
var ErrUSMProtocolUnsupported = errs.Msg("USM protocol not implemented")

// PrivProtocol identifies an SNMPv3 USM privacy (encryption) protocol.
// [PrivProtocolNone] disables privacy.
type PrivProtocol int

const (
	// PrivProtocolNone disables privacy.
	PrivProtocolNone PrivProtocol = iota
	// PrivDES selects CBC-DES (RFC 3414).
	PrivDES
	// Priv3DES selects 3DES-EDE (RFC 3826 / draft-reeder-snmpv3-usm-3desede).
	Priv3DES
	// PrivAES selects CFB128-AES-128 (RFC 3826).
	PrivAES
	// PrivAES192 selects CFB128-AES-192 (RFC 3826).
	PrivAES192
	// PrivAES256 selects CFB128-AES-256 (RFC 3826).
	PrivAES256
	// PrivAES192C selects the Cisco-variant AES-192 needed for
	// compatibility with some Cisco gear (draft-reeder-snmpv3-usm-3desede
	// in spirit).
	PrivAES192C
	// PrivAES256C selects the Cisco-variant AES-256, see [PrivAES192C].
	PrivAES256C
)

// String returns the canonical name of the privacy protocol.
func (p PrivProtocol) String() string {
	switch p {
	case PrivProtocolNone:
		return "none"
	case PrivDES:
		return "DES"
	case Priv3DES:
		return "3DES"
	case PrivAES:
		return "AES"
	case PrivAES192:
		return "AES192"
	case PrivAES256:
		return "AES256"
	case PrivAES192C:
		return "AES192C"
	case PrivAES256C:
		return "AES256C"
	}
	return fmt.Sprintf("PrivProtocol(%d)", int(p))
}

// valid reports whether the PrivProtocol value is a defined constant.
func (p PrivProtocol) valid() bool {
	return p >= PrivProtocolNone && p <= PrivAES256C
}

// USMConfig describes the SNMPv3 User-Based Security Model parameters for
// a [Session]. The passphrases are [secret.Value]s, so any rendering of the
// struct is redacted; validation messages name the field only.
//
// Validation is performed by [USMConfig.Validate]; [WithUSM] applies it
// at option-construction time and surfaces the result via [NewSession].
type USMConfig struct {
	// Username is the SNMPv3 securityName.
	Username string
	// AuthProtocol selects the authentication algorithm. Use
	// [AuthProtocolNone] for noAuth.
	AuthProtocol AuthProtocol
	// AuthPassphrase is the authentication secret.
	AuthPassphrase secret.Value
	// PrivProtocol selects the privacy algorithm. Use [PrivProtocolNone]
	// for noPriv.
	PrivProtocol PrivProtocol
	// PrivPassphrase is the privacy secret.
	PrivPassphrase secret.Value
	// EngineID is the authoritative engine ID; if empty the Backend
	// performs RFC 3414 engine discovery at session establishment.
	EngineID []byte
}

// Level returns the [SecurityLevel] implied by the protocol choices.
// A config that fails [USMConfig.Validate] may still produce a non-Unknown
// level — callers that care about validity should invoke Validate first.
func (c USMConfig) Level() SecurityLevel {
	switch {
	case c.AuthProtocol != AuthProtocolNone && c.PrivProtocol != PrivProtocolNone:
		return SecurityLevelAuthPriv
	case c.AuthProtocol != AuthProtocolNone:
		return SecurityLevelAuthNoPriv
	case c.PrivProtocol == PrivProtocolNone:
		return SecurityLevelNoAuthNoPriv
	}
	// Privacy without auth is not a valid level; surface as Unknown so
	// the validity check is forced through Validate().
	return SecurityLevelUnknown
}

// Validate reports whether the config is internally consistent per RFC
// 3414. The returned error never embeds a passphrase value; the messages
// reference the field name only.
func (c USMConfig) Validate() error {
	if c.Username == "" {
		return errs.Msg("USMConfig.Username is empty")
	}
	if !c.AuthProtocol.valid() {
		return errs.New().Attr("value", int(c.AuthProtocol)).Msg("USMConfig.AuthProtocol is not a known protocol")
	}
	if !c.PrivProtocol.valid() {
		return errs.New().Attr("value", int(c.PrivProtocol)).Msg("USMConfig.PrivProtocol is not a known protocol")
	}
	if c.AuthProtocol == AuthProtocolNone && !c.AuthPassphrase.Empty() {
		return errs.Msg("USMConfig.AuthPassphrase set but AuthProtocol is none")
	}
	if c.AuthProtocol != AuthProtocolNone && c.AuthPassphrase.Empty() {
		return errs.Msg("USMConfig.AuthPassphrase is empty but AuthProtocol requires it")
	}
	if c.PrivProtocol == PrivProtocolNone && !c.PrivPassphrase.Empty() {
		return errs.Msg("USMConfig.PrivPassphrase set but PrivProtocol is none")
	}
	if c.PrivProtocol != PrivProtocolNone && c.PrivPassphrase.Empty() {
		return errs.Msg("USMConfig.PrivPassphrase is empty but PrivProtocol requires it")
	}
	if c.PrivProtocol != PrivProtocolNone && c.AuthProtocol == AuthProtocolNone {
		return errs.Msg("USMConfig privacy requires authentication (RFC 3414)")
	}
	// RFC 3411 §5: snmpEngineID is 5..32 octets when present. An empty
	// EngineID stays valid — it signals Backend discovery at session
	// establishment time, which is the typical client-side configuration.
	if n := len(c.EngineID); n != 0 && (n < 5 || n > 32) {
		return errs.New().Attr("length", n).Msg("USMConfig.EngineID length outside RFC 3411 range [5,32]")
	}
	return nil
}
