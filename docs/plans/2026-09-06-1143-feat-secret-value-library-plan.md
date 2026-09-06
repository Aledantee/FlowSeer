---
title: Secret Value Library - Plan
type: feat
date: 2026-09-06
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
---

# Secret Value Library - Plan

> Implemented. Two deviations: the guard scans struct types declared at
> package level rather than every struct type, because a function-local
> fixture in `src/common/service/telemetry_sdk_test.go` deliberately holds a
> raw `Credential` field and cannot be constructed by another package; and
> the gosnmp client literals in the `src/protocol/snmp/bench` module keep
> their raw `Community` string, since that field belongs to the third-party
> struct.

## Goal

Passwords, passphrases, private keys, and community strings stop being
printable. A new `src/common/secret` package carries them in a `Value` that
redacts itself under `fmt`, `encoding/json`, `encoding.TextMarshaler`, and
`slog`; the four protocol libraries take it in place of their raw `string` and
`[]byte` fields; and an AST scan keeps the raw shape from coming back. Stop
condition: if `fmt` did not honor a field's own `Formatter` when printing the
struct that holds it, the whole approach fails — it does, verified against Go
1.27.1 for `%v`, `%+v`, `%#v`, `%s`, `json.Marshal`, `slog`, a map value, and a
wrapped error.

## Decisions

- One `[]byte`-backed `secret.Value`, not a `Text`/`Bytes` pair. Why: the
  consumers need both shapes — SNMP passphrases are strings that
  `expandPassphrase` immediately converts to `[]byte`
  (`src/protocol/snmp/usm_kdf.go:120`), PEM keys are already `[]byte` — and one
  type means one set of redaction methods to test.
- Redaction lives on the value, not on the containing struct. Why:
  `docs/conventions/observability.md:159` — a key-name denylist cannot detect a
  secret stored under an innocent key, and a value survives being copied into a
  map, an error, or someone else's struct.
- `Reveal` returns the underlying slice, not a copy. Why: a copy the package
  does not own cannot be zeroed, so copying multiplies the material `Zero`
  cannot reach. For the same reason `usmContext` holds `secret.Value`, not the
  `string` copies it takes today (`src/protocol/snmp/usm_security.go:43`).
- Marshaling is asymmetric: `MarshalJSON` and `MarshalText` emit the redaction
  literal, and the unmarshalers reject exactly that literal. Why: a symmetric
  round trip would silently turn a redacted dump back into a credential.
- An empty `Value` renders as the empty string rather than the redaction
  literal, preserving the distinction `src/protocol/snmp/usm.go:345` documents:
  an unset passphrase has nothing to hide and saying so is informative.
- The decoded wire community in `message.community` (`src/protocol/snmp/pdu.go:66`)
  stays a `string`; the session's expected value becomes a `secret.Value` and
  `Value.EqualString` performs the check. Why: the decoded field is untrusted
  input on the per-datagram path, and retyping it would reach the ASN.1 encoder
  for no gain in what a caller can print.
- The guard scans exported fields of hand-written Go under `src/`, outside the
  nested `src/edge/netpen` module. Why: derived key material in unexported
  fields (`authKey` in `src/protocol/snmp/usm_security.go:59`) never crosses a
  package boundary, `generated/` cannot be hand-edited, and netpen is a
  separate module that cannot import `src/common` without a module change.
- The carrier rule is promoted to
  `docs/architecture/2026-09-06-secret-material-carrying-direction.md`
  (`status: proposed-direction`). Why: it binds every future options struct and
  the device service's credential provider, outside this plan's units.

## Requirements

1. `secret.Value` redacts under every standard rendering path. Given
   `Opts{User: "u", Pass: secret.NewString("hunter2")}`, each of `%v`, `%+v`,
   `%#v`, `%s`, `%q`, an unknown verb, `json.Marshal`, `slog` with the text and
   JSON handlers, and `fmt.Errorf("%+v", o)` produces output containing
   `[REDACTED]` and not containing `hunter2`.
2. The material is reachable only through the reveal methods. `Reveal()`
   returns `[]byte("hunter2")` and `RevealString()` returns `"hunter2"`;
   `Len()` returns 7 and `Empty()` returns false.
3. `Zero()` wipes the material for every copy of the value. Given
   `v := secret.NewString("hunter2"); w := v; v.Zero()`, `w.Reveal()` is all
   zero bytes.
4. Comparison is constant time and value equality is not available by
   accident. `secret.NewString("a").Equal(secret.NewString("a"))` and
   `.EqualString("a")` are true, the `"b"` cases are false, and `v == w` does
   not compile.
5. An empty `Value` is distinguishable from a set one without leaking.
   `fmt.Sprint(secret.Value{})` is the empty string, and encoding it to JSON
   yields the two-byte JSON string `""`.
6. Unmarshaling the redaction literal fails. Unmarshaling the JSON string
   `"[REDACTED]"` into a `Value` returns an error naming the literal;
   unmarshaling `"hunter2"` sets the value.
7. `USMConfig` carries `secret.Value` passphrases and redacts through the type.
   `fmt.Sprintf("%+v", USMConfig{Username: "u", AuthPassphrase: secret.NewString("p")})`
   contains `[REDACTED]` and not `p`; `USMConfig` declares no `String`,
   `GoString`, or `Format`, and `usm.go` declares no `renderRedacted`,
   `redactPassphrase`, or `redactedPassphrase`. The `String` methods on
   `SecurityLevel`, `MinSecurity`, `AuthProtocol`, and `PrivProtocol` stay.
8. The SNMP community string is a `secret.Value` on every surface a caller
   holds. `fmt.Sprintf("%+v", cfg)` for a v2c `SessionConfig` and `%+v` on a
   received `Trap` contain `[REDACTED]` and not `public`; a response carrying
   the wrong community still fails with `ErrCommunityMismatch`.
9. Each other protocol library's options struct carries `secret.Value` for its
   password and private key. `fmt.Sprintf("%+v", netconf.Options{Password: secret.NewString("p"), PrivateKeyPEM: secret.NewString("-----BEGIN")})`
   contains neither `p` nor `BEGIN`; the same holds for `gnmi.Options`,
   `restconf.Options`, and their key fields. Public material — `CACertPEM`,
   `ClientCertPEM`, `HostKeySHA256` — stays raw.
10. The guard rejects a reintroduced raw field. Given a source file declaring
    `type O struct { Password string }` under `src/`, the scanner reports one
    finding naming the file, the struct, and the field; given
    `Password secret.Value`, `password string`, or `CACertPEM []byte`, it
    reports none.

## Out of scope

- Secret storage, rotation, leasing, and the device service's credential
  provider. Those stay in
  `docs/plans/2026-09-05-1709-feat-verified-local-device-access-plan.md`.
- `src/edge/netpen`, a separate Go module with its own `findings.Secret`
  carrier (`src/edge/netpen/findings/findings.go:181`). Adopting `secret.Value`
  there means a module requirement and a `replace`, which this change does not
  take on.
- Zeroing derived key material inside the SNMP security path. `authKey` and the
  localized privacy key are still left to the garbage collector.
- Any `errs` API change. `errs.Attr("pass", v)` already renders redacted
  through `Value.LogValue`; the package gains a test and a doc sentence.
- Protobuf or wire representation of secrets, and `generated/`.

## Units

### U1. The secret package
Files: `src/common/secret/{doc.go,value.go,value_test.go,README.md}`,
`src/common/README.md`
After: none
Change: `secret.Value` wraps an unexported `[]byte`. `New([]byte)` takes
ownership of the slice, `NewString(string)` copies. `Reveal`, `RevealString`,
`Len`, `Empty`, `Equal`, and `EqualString` (the last two via
`subtle.ConstantTimeCompare`) read it; `Zero` wipes it. `String`, `GoString`,
`Format`, `MarshalJSON`, `UnmarshalJSON`, `MarshalText`, `UnmarshalText`, and
`LogValue` render `[REDACTED]` for a set value and the empty string for an
unset one. `Format` mirrors `src/protocol/snmp/usm.go:305`: `%v`, `%+v`, `%s`,
`%q`, `%#v`, and a Go-style placeholder for an unknown verb, so no verb reaches
the reflect walker. The package doc states the contract `Reveal` relies on —
the returned slice is read-only to the caller — and that `Zero` reaches every
copy of the value. Add the `secret` row to the `src/common/README.md` table.
Tests: `value_test.go` — a table over every verb and encoder for set, unset,
and struct-embedded values asserting the material is absent (R1, R5); reveal
and length round trips (R2); `Zero` through a copy (R3); the comparison truth
table plus a comment recording that `==` does not compile (R4); unmarshal of
the literal and of a real value (R6); an `errs.Attr` case proving a `Value`
attached to an error renders redacted through `Error.LogValue`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/secret src/common/README.md`

### U2. SNMP USM passphrases
Files: `src/protocol/snmp/{usm.go,usm_kdf.go,usm_security.go}`, the `usm*_test.go`,
`session_v3_test.go`, `reactor_v3_test.go`, `trap_test.go`, `trap_listen_test.go`,
`instrument_test.go`, `options_test.go`, `conformance_corpus_test.go`,
`v3_bench_test.go`, `v3_fuzz_test.go`, `bench/macro_test.go`,
`test/integration/{t1_usm_matrix_test.go,testenv/containerlab.go}`
After: U1
Change: `USMConfig.AuthPassphrase` and `PrivPassphrase` become `secret.Value`,
and so do the `usmContext.authPass` and `privPass` copies at
`src/protocol/snmp/usm_security.go:43`. `localizedAuthKey` and
`localizedPrivKey` take `secret.Value` and pass `Reveal()` straight to
`expandPassphrase`, dropping the `[]byte(passphrase)` conversion. `Validate`
keeps its field-name-only messages, with its six `== ""` comparisons
(`usm.go:256`) becoming `Empty()`. `USMConfig.String`, `GoString`, `Format`,
`renderRedacted`, `redactPassphrase`, and `redactedPassphrase` are deleted; the
enum `String` methods in the same file stay. The `USMConfig` doc comment says
the passphrase fields are `secret.Value` and points at the package instead of
listing redaction methods.
Tests: replace the rendering tests that asserted the hand-written format with
one asserting `%+v`, `%#v`, and `slog` output of a populated `USMConfig` carry
`[REDACTED]` and no passphrase (R7); the USM key-derivation vectors are
unchanged in value and change only in construction, which is what proves the
migration did not alter derivation.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp`

### U3. SNMP community strings
Files: `src/protocol/snmp/{options.go,backend.go,trap.go,trap_listen.go,translate.go,reactor.go,session_engine.go,doc.go}`
and the v1/v2c tests that construct or assert a community
After: U2
Change: `SessionConfig.Community` and `Trap.Community` become `secret.Value`
and `WithCommunity` takes one. The session's and reactor's expected values
(`reactor.go:180`, `reactor.go:217`, `session_engine.go:28`) follow;
`validateResponse` and the reactor's per-datagram check compare with
`EqualString` against the decoded `message.community`, which stays a `string`.
`trap_listen.go:242` builds the trap's community with `secret.New` from the
decoded value. `ErrCommunityMismatch` keeps carrying no community value.
Tests: a rendering test for a v2c `SessionConfig` and a received `Trap` (R8);
the existing mismatch, trap, and reactor tests adjusted for construction, with
the mismatch case proving behavior is unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp`

### U4. NETCONF options
Files: `src/protocol/netconf/{options.go,session.go}`,
`src/protocol/netconf/test/integration/{targets_test.go,targets_parse_test.go,t1_main_test.go,t4_lab_test.go}`,
`src/protocol/yang/test/integration/testenv/testenv.go`
After: U1
Change: `Options.Password` and `Options.PrivateKeyPEM` become `secret.Value`.
`session.go` reveals at the `ssh.Password` (`session.go:98`) and
`ssh.ParsePrivateKey` call sites and nowhere else, and its `!= ""` guard
becomes `Empty()`. `HostKeySHA256` stays a `string`: a fingerprint is public.
The integration target struct that loads lab credentials carries `secret.Value`
too, so a failing table test cannot print them; `targets_parse_test.go:38`
compares with `Equal`. `src/protocol/yang/test/integration/testenv/testenv.go:35`
builds `netconf.Options` for the netopeer2 container and is behind the
`yang_integration_t1` tag, which the untagged build never compiles.
Tests: a rendering test on `Options` (R9); the existing dial and host-key tests
adjusted for construction only.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/netconf src/protocol/yang`

### U5. gNMI and RESTCONF options
Files: `src/protocol/gnmi/{options.go,session.go,session_test.go}`,
`src/protocol/gnmi/test/integration/{t4_main_test.go,t4_lab_test.go}`,
`src/protocol/restconf/{options.go,session.go,session_test.go}`,
`src/protocol/restconf/test/integration/{t4_main_test.go,t4_lab_test.go}`
After: U1
Change: `Password` and `ClientKeyPEM` become `secret.Value` in both structs;
`CACertPEM` and `ClientCertPEM` stay raw. Both reveal only where the value
enters the transport — the gNMI metadata pair (`gnmi/session.go:195`),
`req.SetBasicAuth` (`restconf/session.go:80`), and `tls.X509KeyPair` in each
TLS builder. The lab-target structs in the tagged integration files carry
`secret.Value` for the same reason as U4.
Tests: a rendering test per options struct (R9); TLS and auth tests adjusted
for construction only.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/gnmi src/protocol/restconf`

### U6. The declaration guard
Files: `src/common/internal/secretguard/{scan_test.go,no_raw_secret_fields_test.go}`,
`src/common/README.md`
After: U2, U3, U4, U5
Change: an AST scan over `filepath.Join(repoRoot(t), "src")`, modeled on
`src/common/internal/netpenguard/no_heavy_deps_test.go` including its
`repoRoot` helper and its skip rules for dot-directories and `testdata`, plus a
skip for `src/edge/netpen`. It parses each file, walks `*ast.StructType`
declarations, and reports an exported field whose name matches password,
passphrase, secret, private key, or credential and whose type is `string`,
`[]byte`, or a pointer to either. The package has no non-test file, as
netpenguard has none, so the package doc comment sits on `scan_test.go`. The
scanner is a pure function taking a root, so its table test drives it over
fixture sources in a temp directory rather than over the repository. Add the
`internal/secretguard` row to the `src/common/README.md` table.
Tests: `scan_test.go` covers a raw field, a `secret.Value` field, an unexported
field, a public-material field, a pointer field, and a field of a nested struct
type (R10); `no_raw_secret_fields_test.go` asserts the tree is clean.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/internal/secretguard src/common/README.md`

### U7. Documentation
Files: `src/common/errs/{doc.go,README.md}`, `docs/code-style.md`,
`docs/conventions/observability.md`
After: U1
Change: the prose rules point at the type that now enforces them. `errs` gains
one sentence: secret material reaches an attribute only as a `secret.Value`,
and length and protocol name remain the better attributes.
`docs/code-style.md:224` and the observability logging section gain the same
pointer.
Tests: none; prose only.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/errs docs/code-style.md docs/conventions/observability.md`

## Verification

- `.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>` per
  unit, and `go test -race ./...` from the repository root once U6 lands.
- The verifier builds untagged targets only, so compile the tagged integration
  files this change edits explicitly:
  `go vet -tags yang_integration_t1 ./src/protocol/{netconf,yang}/...`,
  `go vet -tags yang_integration_t4 ./src/protocol/{netconf,gnmi,restconf}/...`,
  and `go vet -tags snmp_integration_t1 ./src/protocol/snmp/...`.
- `grep -rn '\.Reveal' src --include='*.go'` reads as a short, reviewable list:
  every point where material leaves the type.
- Lab check, optional and after the protocol units: one SNMPv3 poll, one v2c
  poll, and one NETCONF session against the switches in `ops/switch-restore/`
  prove the reveal points sit in the right place, since a misplaced reveal
  fails authentication rather than a test.

## Definition of done

- [ ] Verifier green for every changed path; `go test -race ./...` green; the
      three tagged `go vet` runs above green.
- [ ] `src/common/secret/README.md` written and both `src/common/README.md`
      table rows added.
- [ ] `USMConfig` declares no redaction method.
- [ ] The guard test fails when a raw exported secret field is reintroduced,
      demonstrated once by hand before the change lands.
- [ ] This plan's `status` set to `implemented` with an outcome note under the
      title.
- [ ] A person has accepted or rejected
      `docs/architecture/2026-09-06-secret-material-carrying-direction.md`.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- The direction record is `proposed-direction`. It binds nothing until a person
  accepts it; the units may land regardless, since each stands on its own.
