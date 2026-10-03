---
title: Operator Authorization Phase 2, OIDC, OpenFGA Client, Model, and Deployment - Plan
type: feat
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-30-operator-authorization-direction.md
parent: docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 2, OIDC, OpenFGA Client, Model, and Deployment - Plan

## Goal

The device service can verify an operator's token against a configured OIDC
issuer and turn it into an `authn.Principal`, and can ask a standalone
OpenFGA through a `Checker` built on the OpenFGA API. The OpenFGA model
lives in the tree with tests, and the lab deployment runs Postgres, OpenFGA
with authentication and TLS, and an OIDC issuer. Enforcement stays off
until phase 3.

**Stop condition:** OpenFGA v1.21.0 refuses the model below on write, or
answers one of requirement 2's cases the other way. The record's membership
shape then does not hold on this engine version. U3 finds this in the first
wave, before it amends the record and before any unit builds on the model.

## Decisions

The parent plan's Decisions apply. Sources under `~/go/pkg/mod`: `openfga@`
is `github.com/openfga/openfga@v1.21.0`, `api@` is
`github.com/openfga/api/proto@v0.0.0-20260319214821-f153694bfc20/openfga/v1`
(the version the server pins, `openfga@/go.mod:33`), and `oidc@` is
`github.com/coreos/go-oidc/v3@v3.21.0/oidc`.

- Token verification uses `github.com/coreos/go-oidc/v3` v3.21.0
  (Apache-2.0), which brings `github.com/go-jose/go-jose/v4` v4.1.4
  (Apache-2.0) and `golang.org/x/oauth2` v0.36.0 (BSD-3-Clause). (unconfirmed)
  Why: its verifier parses against an allowlist that never holds `none` or
  HMAC (`oidc@/oidc.go:178-191`, `oidc@/verify.go:313`), matches the issuer
  exactly (`:237`), requires the audience (`:253`), and refuses a token that
  is expired or has no `exp`, with no leeway (`oidc@/oidc.go:610`,
  `oidc@/verify.go:269`). v3.21.0 is the newest version past the 14-day wait
  (`docs/conventions/dependencies.md`), and OSV `querybatch` on 2026-10-03
  returned no advisory for the three versions.
- The verifier is built without network access, discovers an issuer on its
  first token, and hands go-oidc an `http.Client` with a 10 s timeout whose
  transport replays the last answer for a URL for 10 s. Why:
  `oidc.NewProvider` fails when discovery is down (`oidc@/oidc.go:306-308`)
  and edge-facing services share the process. go-oidc refetches the key set
  for every token no cached key verifies, with no rate limit
  (`oidc@/jwks.go:163-176`), and its default client has no timeout
  (`oidc@/oidc.go:106-107`). It reports a failed key fetch as text
  (`oidc@/verify.go:336`), so the verifier tells an outage from a bad
  signature by the transport's last result for the key URL. First-party
  code needs no goroutine: the one per key fetch is go-oidc's
  (`oidc@/jwks.go:209`).
- The service trusts a configured list of issuers and routes a token by its
  exact `iss`. Each issuer names one audience and at most one organization
  claim. A tenant binds one issuer (`TenantConfig`,
  `spec/proto/flowseer/model/identity/v1/tenant.proto:36-57`), and a second
  issuer's users reach it through `partner` or `platform`. Why:
  `TenantConfig.issuer` already varies per tenant, `PlatformAdmin` carries an
  issuer of its own, and the partner path needs the caller's home tenant
  claim, so the verifier resolves every organization a token lists. This
  answers the parent's open question on several issuers. An issuer
  configured with no organization claim yields no `claimed` tenant, as the
  parent's Decisions and the record say.
- An organization claim is a JSON string, an array of strings, or an object
  whose keys are the organizations. Any other shape, or more than 99
  distinct values, refuses the token with `authn/token-invalid`. Each value
  resolves through `tenantstore.Store.LookupByOrg`
  (`src/services/device/internal/tenantstore/store.go:280`) and counts only
  when the binding's `organization_claim_name` is the issuer's. Why:
  Keycloak documents the object form and Zitadel a string (Open questions).
  OpenFGA takes 100 contextual tuples per check
  (`api@/openfga.pb.validate.go:1443`) and one is the platform claim. The
  `org_` index hashes issuer and value without the claim name
  (`store.go:52-55`).
- `Principal.Platform` is true when the token's issuer is
  `platform_admin.issuer` and its `organization_claim_name` claim holds
  `organization` (`service_config.proto:74-100`). `subject` stays unused
  until phase 4 writes the stored relationship.
- `Principal.ID` is the lowercase hex SHA-256 of issuer, one zero byte, and
  subject, and `Principal` gains `Issuer` and `Subject`. Why: OpenFGA
  refuses a user id holding `:` (`openfga@/pkg/tuple/tuple.go:417-438`),
  reads `user:a#b` as a userset and `user:*` as a wildcard (`:515-517`), and
  caps the user at 512 characters (`api@/openfga_service.pb.validate.go:2642`),
  while an issuer URL holds `:` and may be 2048 long. `OrgIndexKey` already
  hashes this way, and phase 3 stamps `OperatorRef` from the two fields.
- A request rule hands the `Checker` an id checked only for emptiness
  (`src/services/device/internal/authz/interceptor.go:160-161`).
  `IsValidObject` (`openfga@/pkg/tuple/tuple.go:417-438`) refuses a second
  `:`, any `#`, a space, and control characters, so such an id cannot change
  the object type. A refused id fails the check with OpenFGA code 2000
  (`openfga@/internal/validation/validation.go:392`), and one holding
  whitespace fails a whole BatchCheck at request validation
  (`api@/openfga_service.pb.validate.go:2646`). The interceptor would report
  either as `Unavailable`. The `Checker` therefore answers false, without a
  call, for a query whose object, user, or contextual tuple is not `type:id`
  by that rule, has the id `*`, or exceeds 256 bytes (object) or 512 (user).
  Why: OpenFGA validates a write by the same rule (`validation.go:38-56`),
  so no stored relationship can name such an id. The byte limit is stricter
  than OpenFGA's character limit for a multi-byte id, which fails closed.
- The `Checker` is an owned `net/http` JSON client in
  `src/services/device/internal/authz/openfga`, with no new dependency.
  (unconfirmed) Why: phase 2 needs four calls, `GET /stores/{store_id}`,
  `GET /stores/{store_id}/authorization-models/{id}`, and `POST` to
  `/stores/{store_id}/check` and `/batch-check`
  (`api@/openfga_service.pb.gw.go:2072-2096`), and a dependency's tree counts
  against it. `github.com/openfga/go-sdk` v0.8.0 retries a 400 with
  `time.Sleep` (`api_executor.go:367`, `api_executor_test.go:2111`) and drops
  its credentials header when given an `http.Client` (`api_client.go:60-69`).
- The model is the API's JSON, `internal/authz/model.json`, embedded by the
  package. The `Checker` is configured with a store id and a model id, sends
  the model id on every call, and refuses to start unless the store exists
  and the model under that id equals the embedded one. Why: OpenFGA has no
  flag that creates a store or writes a model
  (`openfga@/cmd/run/run.go:139-390`), so a deployment writes it once, and
  new code against an old model fails every check, as the phase 1 plan's
  open question describes for `platform#claimed`. An absent store answers
  404 `store_id_not_found` and an absent model 400
  `authorization_model_not_found`
  (`openfga@/pkg/server/commands/get_store.go:44`, `read_authzmodel.go:45`,
  `openfga@/pkg/server/errors/encoded_errors.go:99-120`).
- The model, in OpenFGA DSL for reading:

  ```
  model
    schema 1.1
  type user
  type platform
    relations
      define claimed: [user]
      define enrolled: [user]
      define admin: claimed and enrolled
  type role
    relations
      define assignee: [user]
  type tenant
    relations
      define platform: [platform]
      define partner: [tenant]
      define claimed: [user]
      define enrolled: [user]
      define admin: [user, role#assignee] or admin from platform
      define active_admin: admin and member
      define member: (claimed and enrolled) or active_admin from partner or admin from platform
      define operator: [user, role#assignee, tenant#active_admin] or admin
      define capturer: [user, role#assignee, tenant#active_admin] or admin
      define viewer: [user, role#assignee, tenant#active_admin] or operator or capturer
      define full_payload: [user, role#assignee]
  type site
  type tag
  type edge
    relations
      define tenant: [tenant]
      define administer: [user, role#assignee] or admin from tenant
      define operate: [user, role#assignee] or operator from tenant
      define capture: [user, role#assignee] or capturer from tenant
      define view: [user, role#assignee] or administer or operate or capture or viewer from tenant
  type device
    relations
      define tenant: [tenant]
      define operate: [user, role#assignee] or operator from tenant
      define view: [user, role#assignee] or operate or viewer from tenant
  type capture_session
    relations
      define tenant: [tenant]
      define edge: [edge]
      define requester: [user]
      define manage: capture from edge
      define download: requester or capture from edge
  ```

  Why: the tenant lines to `member` are the record's. `platform#claimed`
  exists because the interceptor sends it (phase 1 requirement 11), and
  `enrolled` is the stored half phase 4 writes, so a platform admin loses
  reach when the token stops carrying the claim. Grants on an edge or device
  are direct because the parent's requirement 9 needs a caller with
  `capture` on one of two edges, and `site` and `tag` grant nothing yet.
  `tenant#active_admin` is a grantee on tenant roles only, as the parent's
  requirement 8 uses it. `manage` and `download` follow the parent and the
  capture record
  (`docs/architecture/2026-09-09-remote-packet-capture-direction.md:189-193`).
- The configuration sections are named for their role, `authentication` and
  `authorization`. The schema names no engine and holds the two ids as
  bounded opaque strings, whose format the adapter checks. Why: the record's
  "The service boundary names no engine" keeps engine names and formats out
  of `spec/proto/`. Endpoints and issuers must be `https`, since a key set
  fetched in clear text lets the network choose the signing key.
- The preshared key is a file read without following a symlink and refused
  when group or world can read it
  (`docs/solutions/architecture-patterns/mounted-credential-reads-need-openat-nofollow-and-atomic-pairing.md`),
  held as `secret.Value`, and sent as `Authorization: Bearer`
  (`openfga@/internal/authn/presharedkey/presharedkey.go:38`).
- The `Checker` sends no consistency preference. Why: the check cache and
  its controller are off by default
  (`openfga@/pkg/server/config/config.go:43`, `:58`), the lab sets both off,
  and `authz.Query` carries no policy point.
- Each OpenFGA call is one CLIENT span and one point on a duration
  histogram in `telemetry.View` (`docs/conventions/observability.md`,
  Traces). A refused token already lands in the RPC histogram's `error.type`.
- Engine behavior is tested against the real server under the build tag
  `authz_integration`. Why: a fake written from this plan shares its
  reading of the API (`AGENTS.md`, Investigation discipline). The images are
  constants in an untagged test file, and a test holds `compose.yaml` to
  them. The verifier vets tagged files and does not run them, so
  Verification names the command.
- The lab issuer is Keycloak 26.7.4. (unconfirmed) Why: Open questions.
  `start-dev --help` of the pinned image lists `--import-realm`,
  `--https-certificate-file`, `--https-certificate-key-file`, `--hostname`,
  and `organization` among `--features`.
- The model, the principal id, the issuer list, and the opaque ids outlive
  this plan and constrain phases 3 and 4, so U3 writes them into the
  operator authorization record as a dated amendment.
- The plan stays whole at six units and runs past 300 lines. Why: the
  parent fixes this phase as one unit of its own, U2 shares no file with the
  rest and runs beside them in the first wave, and the other five form one
  cluster. The length is sources and the options of three open decisions.

## Requirements

1. A token from the configured issuer with a valid signature, audience,
   and expiry becomes a principal. The same token with a wrong audience is
   refused with `Unauthenticated`. A token signed by the test issuer with
   `aud: flowseer-device` and `sub: u1` yields `Issuer`, `Subject: "u1"`, and
   the 64 hex characters of `PrincipalID`. With `aud: other`, an unknown
   `iss`, `alg: none`, another key's signature, or no `sub` it is
   `Unauthenticated` with `authn/token-invalid`, and past `exp` with
   `authn/token-expired`.
2. The model admits the parent's relation table and nothing that grants on
   `site` or `tag`. Model tests cover member via claim and enrollment,
   partner, platform admin, and full payload not inherited. With
   `tenant:T#enrolled@user:u` stored, `tenant:T#member` is true only with
   the contextual `tenant:T#claimed@user:u`. A write of `site:s#viewer@user:u`
   is refused.
3. The `Checker` refuses to start against an OpenFGA whose store or model
   id is not the configured one. Against a server holding store S and model
   M, `openfga.New` with another well-formed store id fails with
   `authz/engine-store-mismatch`, and with another model id, or with M one
   relation short of the embedded model, `authz/engine-model-mismatch`. An
   id that is not 26 characters of `0-9A-HJKMNP-TV-Z`
   (`api@/openfga_service.pb.validate.go:2503`) fails with
   `authz/engine-config` before a file is read or a call is made.
4. A container-backed benchmark in
   `src/services/device/test/integration/` reports uncached Check and
   BatchCheck latency for the paths the
   [spike](../research/2026-09-30-openfga-authorization-spike.md) measured,
   and a test pins that `ListObjects` stops at the configured cap without
   marking the result partial. With `OPENFGA_LIST_OBJECTS_MAX_RESULTS=10`
   and 15 edges granted, the response holds 10 objects and no other member.
5. The lab OpenFGA refuses a client without the preshared key. `GET /stores`
   with no `Authorization` header answers 401 `bearer_token_missing`, and a
   wrong key 401 `unauthenticated`.
6. `Principal.Tenants` holds the tenants the token's organization claim
   resolves to. With the issuer's claim `organization`, a token carrying
   `["acme","globex"]`, and a binding of `acme` to tenant A, it is `[A]`. A
   binding of `acme` under claim name `groups` yields none, and a store
   error yields `Unavailable` with `authn/unavailable`.
7. `Check` and `BatchCheck` answer in query order, send at most 50 checks
   per call (`DefaultMaxChecksPerBatchCheck`,
   `openfga@/pkg/server/config/config.go:76`), and answer false without a
   call for an identifier OpenFGA refuses. A `BatchCheck` of 120 queries
   with query 7 naming `edge:a:b` makes three calls of 50, 50, and 19.
8. A failed call returns an error and never an answer. A 401 yields
   `authz/engine-refused`, a transport failure `authz/engine-unreachable`,
   and any other status but 200, a result missing a correlation id, or one
   carrying `error`, `authz/engine-protocol`.
9. `DeviceServiceConfig` accepts both sections and refuses an `http` issuer
   or endpoint, a relative key path, two issuers with one URL, and a
   `platform_admin.issuer` no issuer names.

## Out of scope

- Mounting either interceptor, constructing the verifier or the `Checker`
  in `internal/host`, and serving `TenantService`: phase 3. Their errors
  separate an unreachable engine or issuer from a wrong one, so phase 3 can
  decide what a start does with each.
- Relationship writes and deletes in the adapter, and the projector: phase 3.
  The tests write relationships through a helper of their own.
- A consistency preference per call, check caching, and running the
  container tier from `verify-change.sh`.
- Trust: the verifier reads tokens from callers nobody trusts, and every
  malformed token is in scope. The `Checker` reads answers from the
  deployment's own OpenFGA over TLS, which is trusted, as are the operators
  and repository authors who write configuration and the model file.

## Units

### U1. Configuration sections

Files: `spec/proto/flowseer/store/device/v1/service_config.proto`, `spec/proto/flowseer/store/device/v1/README.md`, `generated/go/proto/flowseer/store/device/v1/service_config.pb.go`, `src/services/device/internal/host/config.go`, `src/services/device/internal/host/config_test.go`
After: none
Change: `DeviceServiceConfig` gains `OperatorAuthentication authentication = 11`
and `AuthorizationEngine authorization = 12`, where unset leaves the service
as it is today. `OperatorAuthentication` holds `repeated OidcIssuer issuers`
(1 to 8, unique by `issuer`) and an absolute `ca_file`, where unset means
the system roots. `OidcIssuer` holds a required `https` `issuer`, a required
`audience`, and `organization_claim_name`, where unset means the issuer's
tokens claim no organization. `AuthorizationEngine` holds a required `https`
`endpoint`, required `store_id` and `model_id` of 1 to 64 characters, a
required absolute `preshared_key_file`, and `ca_file`. A message rule holds
`platform_admin.issuer` to one of the issuers when both are set. Comments
name no engine. `Config` gains `Authentication()` and `Authorization()`, and
the package README documents both sections.
Tests: `TestOperatorAuthenticationAndAuthorization`, shaped like
`TestPlatformAdminAndDevTenant` (`config_test.go:234`): one accepted file,
then requirement 9's refusals, an empty audience, and no issuers, each one
property from the accepted file and each `host/config-invalid`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/store/device/v1 src/services/device/internal/host`

### U2. Token verifier and its interceptor

Files: `go.mod`, `go.sum`, `docs/dependencies/statements/go/github.com/coreos/go-oidc/v3.md`, `src/services/device/internal/authn/principal.go`, `src/services/device/internal/authn/principal_test.go`, `src/services/device/internal/authn/verifier.go`, `src/services/device/internal/authn/verifier_test.go`, `src/services/device/internal/authn/interceptor.go`, `src/services/device/internal/authn/interceptor_test.go`
After: none
Change: the statement follows `docs/conventions/dependencies.md` with
criteria `deploy` and `approved` set by the person who rules on it.
`NewVerifier(Options)` takes the issuers, the platform triple, a resolver
with `LookupByOrg`'s signature, an `http.Client`, and a clock, and does no
I/O. `Verify(ctx, token)` reads `iss` from the unverified payload, picks the
issuer, discovers it once, verifies through
`oidc.Config{ClientID: audience, Now: clock}`, and builds the principal by
the Decisions, with `Tenants` sorted and distinct. A subject that is empty
or holds a zero byte is refused. A discovery, key, or resolver failure is
`Unavailable` with `authn/unavailable` and retryable, and an ended context
answers `ctx.Err()` bare (`docs/code-style.md:255-256`). `Interceptor`
implements `connect.Interceptor`: it reads `Authorization: Bearer`, scheme
in any case, on unary and streaming handlers, refuses through
`connecterr.WrapAs` with "authentication required", and puts the principal
in the context.
Tests: an `httptest` issuer serves discovery and a key set. Tokens are built
with `crypto/rsa`, `crypto/ecdsa`, and `encoding/base64` alone, which keeps
go-jose indirect and makes the token bytes a second source. One case per
refusal in requirement 1, each one property from the accepted token.
Requirement 6 with a fake resolver, the three claim shapes, a number as the
claim, 100 values, and the platform flag set and unset by one field.
`PrincipalID` differs for (`https://a/b`, `c`) and (`https://a/`, `bc`).
Twenty tokens with unknown `kid` values inside 10 s cause one key fetch. A
key endpoint answering 500 yields `Unavailable` for an unknown `kid`, where
a healthy one yields `Unauthenticated`. Discovery that fails and recovers
verifies after the replay window. The interceptor cases are no header,
`bearer` in lower case, and a streaming call.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- go.mod go.sum docs/dependencies/statements/go/github.com/coreos/go-oidc/v3.md src/services/device/internal/authn`

### U3. The model, its evaluation, and the record's amendment

Files: `src/services/device/internal/authz/model.json`, `src/services/device/internal/authz/model.go`, `src/services/device/internal/authz/model_test.go`, `src/services/device/test/integration/openfga_images_test.go`, `src/services/device/test/integration/openfga_env_test.go`, `src/services/device/test/integration/openfga_model_test.go`, `docs/architecture/2026-09-30-operator-authorization-direction.md`
After: none
Change: `model.json` is the Decisions' model as a `WriteAuthorizationModel`
body (`schema_version` and `type_definitions`), and `model.go` embeds it.
`openfga_images_test.go` is untagged and holds
`openfga/openfga:v1.21.0@sha256:2113c664a486b5da8d7a2cdab479e0d4e30639c80fd2c000540f645c1dbc1e55`
and `postgres:17@sha256:d74eeac9a635390a49bc21bd49fccd973de707e2a53a76ac49b552b8712ec46f`.
The other two files carry `//go:build authz_integration`. The environment
makes a certificate, starts OpenFGA on its memory datastore with
`OPENFGA_AUTHN_METHOD=preshared` and HTTP TLS, waits on `/healthz`, creates a
store with the embedded model, and offers plain HTTP helpers to write
relationships and check. It skips under `-short` and fails when Docker is
absent. Once the tagged test passes, the record gains a dated amendment
with the model in DSL and the reason for each relation beyond its tenant
shape, the principal id, the issuer list, and the opaque engine ids in
configuration. It says how the record's sentence on resource permissions
without intersection reads beside `platform#admin` and
`tenant#active_admin`, which a resource permission reaches only through
`tenant`.
Tests: `model_test.go` compares every type's relation names, and the user
types each relation takes directly, with a table in the test, so `site` and
`tag` hold none. It walks `protoregistry.GlobalFiles` for `flowseer.api.`
and asserts that each rule's object type and relation exist in the model,
with `tenant` on every type a request rule names, and fails when it walked
no method. `openfga_model_test.go` evaluates on the server. For every
relation a rule may name, each branch of its definition has one allowed
case and one denial a single relationship away. Member needs both `claimed`
and `enrolled`. A partner admin is a member of the customer only with the
home claim, and holds `capture` on its edge only while
`tenant:C#capturer@tenant:M#active_admin` is stored. A platform admin with
its claim is `admin` of every tenant and lacks `full_payload`.
`edge:E1#capture@user:u` grants nothing on `E2`, and `edge#tenant` is false
for another tenant. The site write of requirement 2 is refused.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/authz src/services/device/test/integration docs/architecture/2026-09-30-operator-authorization-direction.md`

### U4. The OpenFGA checker

Files: `src/services/device/internal/authz/openfga/checker.go`, `src/services/device/internal/authz/openfga/checker_test.go`, `src/services/device/internal/credential/keyfile.go`, `src/services/device/internal/credential/keyfile_test.go`, `src/services/device/internal/telemetry/telemetry.go`, `src/services/device/internal/telemetry/telemetry_test.go`, `src/services/device/test/integration/openfga_checker_test.go`
After: U3
Change: `credential.ReadKeyFile(path)` opens with `O_NOFOLLOW`, applies
`checkSecure` (`provider.go:192`), trims one trailing newline, refuses an
empty file, and returns a `secret.Value`. `openfga.New(ctx, Options)` takes
the endpoint, both ids, the key and CA file paths, a `*telemetry.View`, a
tracer provider, and a propagator. It checks the options by requirement 3,
then makes the two `GET`s and compares the stored model with the embedded
one after dropping `id` and every member that is null, empty, or an empty
collection, since the gateway emits unset fields
(`github.com/grpc-ecosystem/grpc-gateway/v2@v2.30.0/runtime/marshaler_registry.go:23`).
`Check` and `BatchCheck` implement `authz.Checker` by requirements 7 and 8,
with a per-call timeout above OpenFGA's 3 s `requestTimeout`
(`config.go:89`). `View` gains `flowseer.device.authz.request.duration` in
seconds with `flowseer.device.authz.operation` and, on failure, `error.type`.
Tests: in `checker_test.go` an `httptest` TLS server stands in for OpenFGA
and records requests. Cases cover requirements 3, 7, and 8, each refusal one
property from an accepted exchange. They include the request JSON for a
query with two contextual tuples, an untrusted certificate, the histogram
and span on success and failure, and a refused object, user (`user:*`,
`user:a#b`), and contextual tuple each making no call. That server is
written from this plan's reading of the API, so `openfga_checker_test.go`,
tagged, repeats requirements 3 and 7 on U3's environment: `openfga.New`
accepts the embedded model as the server returns it and refuses an absent
store, an absent model, a model one relation short, and a wrong key. It
holds requirement 5. `keyfile_test.go` mirrors
`TestProviderRefusesSymlinkedCredential` and
`TestProviderRefusesGroupOrWorldMode`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/authz/openfga src/services/device/internal/credential src/services/device/internal/telemetry src/services/device/test/integration`

### U5. The benchmark and the list cap

Files: `src/services/device/test/integration/openfga_bench_test.go`, `src/services/device/test/integration/README.md`
After: U3, U4
Change: the file carries `//go:build authz_integration`. It runs
`openfga migrate` and `run` on Postgres in one container network, from U3's
image constants, and checks through `openfga.New`. The README gives the
tier's command, the fixture, and what the output means.
Tests: `BenchmarkOpenFGA` loads 20 tenants, each with 2,000 edges, 4,000
sessions, and 200 users on one of 10 roles, then reports p50 and p99
through `b.ReportMetric` for a platform admin and a tenant capturer on
`edge#capture`, a caller with no grant, a platform admin on
`tenant#full_payload`, a BatchCheck of 50 sessions, and the spike's six
membership cases. Its site and Tag paths have no counterpart in this model.
`TestOpenFGAListObjectsStopsAtTheCap` holds requirement 4's second half on
the memory datastore.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/test/integration`

### U6. The lab deployment

Files: `deploy/lab/compose.yaml`, `deploy/lab/keycloak/realm.json`, `deploy/lab/write-lab-secrets.sh`, `deploy/lab/write-openfga-store.sh`, `deploy/lab/.gitignore`, `deploy/lab/central.textproto`, `deploy/lab/README.md`, `src/services/device/README.md`, `src/services/device/test/integration/lab_fixtures_test.go`
After: U1, U2, U3, U4
Change: `compose.yaml` runs U3's Postgres image, its OpenFGA image once as
`migrate` and once as `run`, and
`quay.io/keycloak/keycloak:26.7.4@sha256:82a77884f3af238beab1e7afd63b5f530e1b5c0590bd7aa60b40a40463e29b2c`.
OpenFGA runs with `OPENFGA_AUTHN_METHOD=preshared`, HTTP and gRPC TLS, and
the playground, check query cache, and cache controller off
(`openfga@/cmd/run/flags.go`). Keycloak runs
`start-dev --import-realm --features=organization` with
`--https-certificate-file`, `--https-certificate-key-file`, and
`--hostname=https://127.0.0.1:8445`. Every port binds `127.0.0.1`.
`write-lab-secrets.sh` makes an ignored `secrets/` directory with a lab CA,
one server certificate both services use, a random preshared key at mode
0600, and the env files, and refuses to overwrite. `write-openfga-store.sh`
creates the store, posts `model.json` with `curl` and `jq`, and prints the
`authorization` block. `realm.json` is made once in the pinned image and
written by `kc.sh export`: realm `flowseer-lab`, organizations `acme` and
`flowseer-platform`, a public client with an audience mapper for
`flowseer-device` and the `organization` scope, and no user.
`central.textproto` gains both sections with placeholder ids. The lab
README lists the files and the run: secrets, `up`, store, two users through
`kcadm.sh`, a token, and the `curl` of requirement 5. The device README's
Layout names the verifier, the model, and `internal/authz/openfga`, and
Deployment says both sections are validated and not yet enforced.
Tests: `TestTheLabAuthorizationPlaceholdersAreRefused` hands the file's ids
to `openfga.New` and expects `authz/engine-config`, and another code once
both are well formed. `TestTheLabOpenFGARequiresAKeyAndTLS` parses
`compose.yaml` with `gopkg.in/yaml.v3` and asserts the settings above, U3's
image constants, a digest on the Keycloak image, and the loopback bind. No
test starts Keycloak: the README's run proves the realm.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- deploy/lab src/services/device/README.md src/services/device/test/integration/lab_fixtures_test.go`

Waves: U1 U2 U3 | U4 | U5 U6

## Verification

```bash
go tool -modfile=tools/buf/go.mod buf lint
go tool -modfile=tools/buf/go.mod buf generate
go test -race ./src/services/device/... ./test/conformance/...
go vet -tags=authz_integration ./src/services/device/test/integration/
go test -race -tags=authz_integration ./src/services/device/test/integration/
go test -tags=authz_integration -run '^$' -bench . -benchtime=1000x ./src/services/device/test/integration/
docker compose -f deploy/lab/compose.yaml config --quiet
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/store/device/v1 go.mod go.sum docs/dependencies docs/architecture/2026-09-30-operator-authorization-direction.md src/services/device deploy/lab
```

The tagged runs need Docker and run the package whole, since a `-run`
pattern that matches nothing passes
(`docs/solutions/conventions/a-gate-selected-by-name-stops-running-silently.md`).
The run in `deploy/lab/README.md` is done once by hand through requirement
5's `curl`.

## Definition of done

- [ ] Verifier green for every changed path, and both tagged runs pass.
- [ ] The statement for go-oidc carries a person's `approved` date.
- [ ] Both READMEs under `src/services/device/`, the store package README,
      `deploy/lab/README.md`, and the amended record updated in the same
      change.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for U2 filled.
- [ ] No plan labels in code.

## Open questions

Three decisions wait for the user. The units are written for the first
option of each.

- How tokens are verified, which is also a dependency admission.
  - go-oidc v3.21.0 (recommended): three new modules. The library owns the
    order of the checks. FlowSeer owns the replay transport and tells an
    outage from a bad signature indirectly.
  - go-jose v4.1.4 alone: one new module. FlowSeer owns discovery, the key
    cache, and the claim checks, including refusing a token without `exp`,
    which go-jose accepts
    (`github.com/go-jose/go-jose/v4@v4.1.4/jwt/validation.go:116`).
  - `github.com/zitadel/oidc/v3`: seven to eleven new modules at v3.27.0,
    and its access token verifier checks no audience
    (`pkg/op/verifier_access_token.go:47-55`).
  - Standard library only: no module, and FlowSeer owns JOSE parsing.
- How the `Checker` reaches OpenFGA and how the model is stored.
  - Owned HTTP client and a JSON model (recommended): no new module, about
    250 lines to own, and a model that is hard to read beside the record's
    DSL.
  - `github.com/openfga/api/proto` over gRPC: one new direct module with
    `github.com/envoyproxy/protoc-gen-validate` behind it. Typed requests,
    `proto.Equal` for the model, and the transport the spike measured.
  - That plus `github.com/openfga/language/pkg/go`: the model is written in
    DSL, for a second direct module and an ANTLR parser in the service.
- Which issuer the lab runs.
  - Keycloak 26.7.4 (recommended): Apache-2.0. Its `organization` scope puts
    every organization of a user in access tokens
    (https://raw.githubusercontent.com/keycloak/keycloak/main/docs/documentation/server_admin/topics/organizations/mapping-organization-claims.adoc),
    and `--import-realm` loads the realm from a file
    (https://www.keycloak.org/server/importExport). It is a JVM image.
  - Zitadel: AGPL-3.0
    (https://raw.githubusercontent.com/zitadel/zitadel/main/LICENSE). A
    user's organization is one string
    (https://zitadel.com/docs/apis/openidoauth/claims), so one token cannot
    list two tenants. How it is set up from files is unverified.
  - Dex: Apache-2.0. A static user has an email, hash, name, and id
    (https://dexidp.io/docs/connectors/local/) and no organization claim, so
    the claimed path needs an upstream connector.

Unverified, for the implementer to settle:

- The shape of Keycloak's `organization` claim with the organization id
  left off. The verifier reads all three shapes either way.
- go-oidc takes its algorithm list from the issuer's ID token metadata
  (`oidc@/oidc.go:174`). An issuer that signs access tokens with another
  algorithm needs the list set.
- OpenFGA v1.21.0 was published 2026-09-20, so its 14-day wait ends
  2026-10-04.

For phase 3: whether a start fails when the engine or an issuer is
unreachable, and whether a suspended tenant's organization still yields
`claimed` (`TenantLifecycle`, `tenant.proto:26-33`).
