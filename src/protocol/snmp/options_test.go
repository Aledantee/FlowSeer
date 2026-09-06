package snmp

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/secret"
)

// TestOptions_Commutative pins the "order-independent" property: any
// permutation of Options that touches distinct fields produces the same
// SessionConfig. (Options that touch the same field are last-wins, which
// is a separate property.)
func TestOptions_Commutative(t *testing.T) {
	a := ApplyOptions(
		WithCommunity(secret.NewString("public")),
		WithTimeout(2*time.Second),
		WithRetries(3),
		WithMaxOIDs(40),
		WithMinSecurity(MinSecurityAuthPriv),
	)
	b := ApplyOptions(
		WithMinSecurity(MinSecurityAuthPriv),
		WithMaxOIDs(40),
		WithRetries(3),
		WithTimeout(2*time.Second),
		WithCommunity(secret.NewString("public")),
	)

	if !reflect.DeepEqual(a, b) {
		t.Errorf("permutations diverged:\n a=%+v\n b=%+v", a, b)
	}
}

func TestOption_FieldsApplyIndividually(t *testing.T) {
	cfg := ApplyOptions(
		WithCommunity(secret.NewString("private")),
		WithTimeout(7*time.Second),
		WithRetries(5),
		WithMaxOIDs(25),
		WithMinSecurity(MinSecurityNoAuth),
	)
	if !cfg.Community.EqualString("private") {
		t.Errorf("Community = %q", cfg.Community)
	}
	if cfg.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
	if cfg.Retries != 5 {
		t.Errorf("Retries = %d", cfg.Retries)
	}
	if cfg.MaxOIDs != 25 {
		t.Errorf("MaxOIDs = %d", cfg.MaxOIDs)
	}
	if cfg.MinSecurity != MinSecurityNoAuth {
		t.Errorf("MinSecurity = %v", cfg.MinSecurity)
	}
}

// TestCallOption_DoesNotMutateSessionConfig pins the per-call scope: a
// CallOption operating on a CallConfig must not be able to reach into a
// SessionConfig. The proof is structural — the function signatures are
// different — but the test exercises both types side-by-side to make
// that property obvious to readers.
func TestCallOption_DoesNotMutateSessionConfig(t *testing.T) {
	scfg := &SessionConfig{Timeout: time.Second, Retries: 2}
	ccfg := &CallConfig{}

	WithCallTimeout(5 * time.Second)(ccfg)
	WithCallRetries(7)(ccfg)
	WithCallMaxOIDs(15)(ccfg)
	WithRowBuffer(64)(ccfg)

	if scfg.Timeout != time.Second {
		t.Errorf("SessionConfig.Timeout mutated: %v", scfg.Timeout)
	}
	if scfg.Retries != 2 {
		t.Errorf("SessionConfig.Retries mutated: %d", scfg.Retries)
	}

	if !ccfg.TimeoutSet || ccfg.Timeout != 5*time.Second {
		t.Errorf("CallConfig.Timeout = (%v, set=%v)", ccfg.Timeout, ccfg.TimeoutSet)
	}
	if !ccfg.RetriesSet || ccfg.Retries != 7 {
		t.Errorf("CallConfig.Retries = (%v, set=%v)", ccfg.Retries, ccfg.RetriesSet)
	}
	if ccfg.MaxOIDs != 15 {
		t.Errorf("CallConfig.MaxOIDs = %d", ccfg.MaxOIDs)
	}
	if ccfg.RowBuffer != 64 {
		t.Errorf("CallConfig.RowBuffer = %d", ccfg.RowBuffer)
	}
}

// TestEnforceMinSecurity_V2cRejectedAtDefault pins the policy that a
// v1/v2c session (USM == nil) is rejected by the default minimum
// security floor (MinSecurityAuthNoPriv) unless the caller explicitly
// opts into the laxer floor via WithMinSecurity(MinSecurityNoAuth).
//
// Regression: before this fix EnforceMinSecurity returned nil whenever
// USM was nil, silently admitting any v1/v2c configuration regardless
// of the caller's stated floor.
func TestEnforceMinSecurity_V2cRejectedAtDefault(t *testing.T) {
	// Default MinSecurity = MinSecurityAuthNoPriv. A v1/v2c session has
	// no USM, so the effective level is noAuthNoPriv, which falls below
	// the floor.
	cfg := ApplyOptions(WithCommunity(secret.NewString("public")))
	if err := cfg.EnforceMinSecurity(); !errors.Is(err, ErrSecurityPolicy) {
		t.Errorf("default floor: err = %v, want ErrSecurityPolicy", err)
	}

	// Explicit opt-in to MinSecurityNoAuth admits v1/v2c.
	cfg = ApplyOptions(WithCommunity(secret.NewString("public")), WithMinSecurity(MinSecurityNoAuth))
	if err := cfg.EnforceMinSecurity(); err != nil {
		t.Errorf("MinSecurityNoAuth + v2c: err = %v, want nil", err)
	}

	// MinSecurityAuthPriv also rejects nil-USM.
	cfg = ApplyOptions(WithMinSecurity(MinSecurityAuthPriv))
	if err := cfg.EnforceMinSecurity(); !errors.Is(err, ErrSecurityPolicy) {
		t.Errorf("MinSecurityAuthPriv + v2c: err = %v, want ErrSecurityPolicy", err)
	}
}

// TestEnforceMinSecurity_Matrix asserts the full 4×3 grid of MinSecurity
// floors × effective SecurityLevels per RFC 3414 §3.1's ordering
// (noAuthNoPriv < authNoPriv < authPriv). Each cell builds a real
// SessionConfig, calls EnforceMinSecurity, and confirms accept/reject.
//
// Effective levels are reached as follows:
//
//   - noAuthNoPriv: USM == nil (v1/v2c equivalent).
//   - authNoPriv:   USM with AuthMD5 + PrivProtocolNone.
//   - authPriv:     USM with AuthMD5 + PrivAES (both passphrases).
//
// MinSecurityUnset and MinSecurityAuthNoPriv produce identical accept/
// reject decisions (Unset substitutes the default floor); both rows are
// pinned so a regression in the default-substitution path is caught.
//
// Covers conformance matrix row: RFC 3414 §3.1 / securityLevel ordering.
func TestEnforceMinSecurity_Matrix(t *testing.T) {
	const pass = "secret"
	noUSM := func() Option { return func(*SessionConfig) {} }
	authNoPriv := func() Option {
		return WithUSM(USMConfig{Username: "u", AuthProtocol: AuthMD5, AuthPassphrase: secret.NewString(pass)})
	}
	authPriv := func() Option {
		return WithUSM(USMConfig{
			Username: "u", AuthProtocol: AuthMD5, AuthPassphrase: secret.NewString(pass),
			PrivProtocol: PrivAES, PrivPassphrase: secret.NewString(pass),
		})
	}

	type cell struct {
		level     string
		levelOpt  Option
		reject    bool
		rejectMsg []string // substrings expected in the error message on reject
	}

	rows := []struct {
		minName string
		minOpt  Option
		cells   []cell
	}{
		{
			"MinSecurityUnset",
			WithMinSecurity(MinSecurityUnset),
			[]cell{
				{"noAuthNoPriv", noUSM(), true, []string{"noAuthNoPriv", "authNoPriv"}},
				{"authNoPriv", authNoPriv(), false, nil},
				{"authPriv", authPriv(), false, nil},
			},
		},
		{
			"MinSecurityNoAuth",
			WithMinSecurity(MinSecurityNoAuth),
			[]cell{
				{"noAuthNoPriv", noUSM(), false, nil},
				{"authNoPriv", authNoPriv(), false, nil},
				{"authPriv", authPriv(), false, nil},
			},
		},
		{
			"MinSecurityAuthNoPriv",
			WithMinSecurity(MinSecurityAuthNoPriv),
			[]cell{
				{"noAuthNoPriv", noUSM(), true, []string{"noAuthNoPriv", "authNoPriv"}},
				{"authNoPriv", authNoPriv(), false, nil},
				{"authPriv", authPriv(), false, nil},
			},
		},
		{
			"MinSecurityAuthPriv",
			WithMinSecurity(MinSecurityAuthPriv),
			[]cell{
				{"noAuthNoPriv", noUSM(), true, []string{"noAuthNoPriv", "authPriv"}},
				{"authNoPriv", authNoPriv(), true, []string{"authNoPriv", "authPriv"}},
				{"authPriv", authPriv(), false, nil},
			},
		},
	}
	for _, row := range rows {
		for _, c := range row.cells {
			name := row.minName + "/" + c.level
			t.Run(name, func(t *testing.T) {
				cfg := ApplyOptions(row.minOpt, c.levelOpt)
				err := cfg.EnforceMinSecurity()
				if c.reject {
					if !errors.Is(err, ErrSecurityPolicy) {
						t.Fatalf("%s: err = %v, want ErrSecurityPolicy", name, err)
					}
					for _, want := range c.rejectMsg {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("%s: error %q does not mention %q",
								name, err.Error(), want)
						}
					}
					return
				}
				if err != nil {
					t.Errorf("%s: err = %v, want nil", name, err)
				}
			})
		}
	}
}

// TestEnforceMinSecurity_PrivacyWithoutAuth pins the documented invalid
// USM combination (priv without auth) — Level() returns
// SecurityLevelUnknown and EnforceMinSecurity surfaces an error wrapping
// ErrSecurityPolicy whose message names "USM level is invalid".
//
// Covers conformance matrix row: RFC 3414 §3.1 / privacy without auth.
func TestEnforceMinSecurity_PrivacyWithoutAuth(t *testing.T) {
	// WithUSM captures a validation error for priv-without-auth; we
	// bypass that to directly set the USM field with the invalid combo,
	// exercising the EnforceMinSecurity path that handles
	// SecurityLevelUnknown rather than the WithUSM validation path.
	cfg := &SessionConfig{
		Version:     V3,
		MinSecurity: MinSecurityAuthNoPriv,
		USM: &USMConfig{
			Username:       "u",
			AuthProtocol:   AuthProtocolNone,
			PrivProtocol:   PrivAES,
			PrivPassphrase: secret.NewString("p"),
		},
	}
	err := cfg.EnforceMinSecurity()
	if !errors.Is(err, ErrSecurityPolicy) {
		t.Fatalf("err = %v, want ErrSecurityPolicy", err)
	}
	if !strings.Contains(err.Error(), "USM level is invalid") {
		t.Errorf("error %q does not mention 'USM level is invalid'", err.Error())
	}
}

func TestVersion_String(t *testing.T) {
	cases := []struct {
		v    Version
		want string
	}{
		{VersionUnset, "unset"},
		{V1, "v1"},
		{V2c, "v2c"},
		{V3, "v3"},
	}
	for _, tc := range cases {
		if got := tc.v.String(); got != tc.want {
			t.Errorf("Version(%d).String() = %q, want %q", int(tc.v), got, tc.want)
		}
	}
}
