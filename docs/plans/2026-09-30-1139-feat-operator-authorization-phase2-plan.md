---
title: Operator Authorization Phase 2, OIDC, OpenFGA Client, Model, and Deployment - Plan
type: feat
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/conventions/a-combinatorial-table-count-guard-must-assert-a-literal.md
execution: mixed
amends: docs/architecture/2026-09-30-operator-authorization-direction.md
parent: docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 2, OIDC, OpenFGA Client, Model, and Deployment - Plan

> Implemented. 6 units, 2026-10-03T11:58Z to 2026-10-03T13:12Z.

## Goal

The device service can verify an operator's token against a configured OIDC
issuer and turn it into an `authn.Principal`, and can ask a standalone
OpenFGA through a `Checker` that speaks OpenFGA's gRPC API. The OpenFGA
model lives in the tree with tests, and the lab deployment runs Postgres,
OpenFGA with authentication and TLS, and an OIDC issuer. Enforcement stays
off until phase 3.

**Stop condition:** OpenFGA v1.21.0 refuses the model below on write, or
answers one of requirement 2's cases the other way. The record's membership
shape then does not hold on this engine version. U3 finds this in the first
wave, before it amends the record and before any unit builds on the model.

## Decisions

The parent plan's Decisions apply. Sources under `~/go/pkg/mod`: `openfga@`
is `github.com/openfga/openfga@v1.21.0`, `oidc@` is
`github.com/coreos/go-oidc/v3@v3.21.0/oidc`, `grpc@` is
`google.golang.org/grpc@v1.84.0`, and `api@` is
`github.com/openfga/api/proto@v0.0.0-20260723150800-6981fff8d33b/openfga/v1`.
U3's `go get` puts `api@` there. Until then it is the zip the Go proxy
serves for that version.

- Token verification uses `github.com/coreos/go-oidc/v3` v3.21.0
  (Apache-2.0), which brings `github.com/go-jose/go-jose/v4` v4.1.4
  (Apache-2.0) and `golang.org/x/oauth2` v0.36.0 (BSD-3-Clause).
  (decided by the user, 2026-10-03)
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
  Keycloak documents the object form, Zitadel a string, and Dex an array
  (Open questions).
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
  (`openfga@/internal/validation/validation.go:392`,
  `openfga@/pkg/server/commands/errors.go:90-92`), and one holding
  whitespace fails a whole BatchCheck at request validation
  (`api@/openfga_service.pb.validate.go:2646`). The interceptor would report
  either as `Unavailable`. The `Checker` therefore answers false, without a
  call, for a query whose object, user, or contextual tuple is not `type:id`
  by that rule, has the id `*`, or exceeds 256 bytes (object) or 512 (user).
  Why: OpenFGA validates a write by the same rule (`validation.go:38-56`),
  so no stored relationship can name such an id. The byte limit is stricter
  than OpenFGA's character limit for a multi-byte id, which fails closed.
- The model, in OpenFGA DSL for reading. The file the service embeds is
  decided below:

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
  requirement 8 uses it. `manage` follows the parent, and `download` the
  capture record
  (`docs/architecture/2026-09-09-remote-packet-capture-direction.md:187-196`),
  which stamps the requester from the principal.
- The configuration sections are named for their role, `authentication` and
  `authorization`. The schema names no engine and holds the two ids as
  bounded opaque strings, whose format the adapter checks. Why: the record's
  "The service boundary names no engine" keeps engine names and formats out
  of `spec/proto/`. Endpoints and issuers must be `https`, since a key set
  fetched in clear text lets the network choose the signing key. The engine
  endpoint is an `https` URL of a host and a port, as
  `ServiceTelemetry.endpoint` is a URL for an exporter that dials its host
  over gRPC (`service_config.proto:204-211`,
  `src/common/service/telemetry_otlp.go:234`).
- The preshared key is a file read without following a symlink and refused
  when group or world can read it
  (`docs/solutions/architecture-patterns/mounted-credential-reads-need-openat-nofollow-and-atomic-pairing.md`),
  held as `secret.Value`, and sent as `authorization: Bearer` metadata
  (`openfga@/internal/authn/presharedkey/presharedkey.go:38`) through
  per-RPC credentials that require transport security. Why: grpc then
  refuses to build a client that would send the key in clear
  (`grpc@/clientconn.go:494-500`).
- The `Checker` sends no consistency preference. Why: the check cache and
  its controller are off by default
  (`openfga@/pkg/server/config/config.go:43`, `:58`), the lab sets both off,
  and `authz.Query` carries no policy point.
- Each OpenFGA call is one CLIENT span named by its `rpc.method`, such as
  `openfga.v1.OpenFGAService/Check`, and one point on
  `rpc.client.call.duration` with `rpc.system.name` `grpc`. One owned unary
  client interceptor makes both and injects the trace context. Why:
  `docs/conventions/observability.md`, Traces, reuses a protocol's own
  names, the view already records the server side through `rpcconv`
  (`src/services/device/internal/telemetry/telemetry.go:116`), and `otelgrpc`
  would be a second direct module for one span and one histogram. A refused
  token already lands in the RPC histogram's `error.type`.
- Engine behavior is tested against the real server under the build tag
  `authz_integration`. Why: a fake written from this plan shares its
  reading of the API (`AGENTS.md`, Investigation discipline). The images are
  constants in an untagged test file, and a test holds `compose.yaml` to
  them. The verifier vets tagged files and does not run them, so
  Verification names the command.
- The model, the principal id, the issuer list, and the opaque ids outlive
  this plan and constrain phases 3 and 4, so U3 writes them into the
  operator authorization record as a dated amendment.
- The plan stays whole at six units and runs past 300 lines. Why: the
  parent fixes this phase as one unit of its own, and the six units form
  one cluster around `go.mod`, the model, and the integration environment.
  The length is sources and the issuer survey, which is the data for the
  one open decision.
- The `Checker` reaches OpenFGA through `github.com/openfga/api/proto` over
  gRPC, without `github.com/openfga/language`, so the model is not written
  in DSL. This replaces the earlier recommendation, an owned HTTP client with a
  JSON model.
  Why: typed requests, `proto.Equal` for the model, and the transport the
  spike measured. (decided by the user, 2026-10-03)
- That module is pinned at `v0.0.0-20260723150800-6981fff8d33b`
  (Apache-2.0), the newest version past the 14-day wait
  (`docs/conventions/dependencies.md`). Why: the module has no tags. The Go
  proxy dates this commit 2026-07-23 and the newest one, `c0650ce2169b`,
  2026-09-28, which waits until 2026-10-12. OpenFGA v1.21.0 builds against
  `f153694bfc20` of 2026-03-19 (`openfga@/go.mod:33`), which is 126 days
  older and so stale by the convention's 90-day rule on the day it is
  added. Between the two versions the `protobuf:` struct tags, the rules in
  the `.pb.validate.go` files, and the `FullMethodName` constants are
  identical, and the generated code around them differs. The tagged tests
  run this client against the v1.21.0 image. `go.mod` gains this
  line and `github.com/envoyproxy/protoc-gen-validate` v1.3.3 as indirect,
  published 2026-02-18, which grpc v1.84.0 already requires. The package
  also imports `github.com/grpc-ecosystem/grpc-gateway/v2` v2.31.0 and
  `google.golang.org/genproto/googleapis/api`, both in `go.mod` today. OSV
  `querybatch` on 2026-10-03 returned no advisory for those versions. For
  grpc v1.84.0 it returned `GO-2026-6443`, a server panic on requests that
  lack authority and Host headers, which
  `docs/dependencies/statements/go/google.golang.org/grpc.md` records and a
  client does not reach.
- `openfga.New` builds one `grpc.ClientConn` as the OTLP exporter does
  (`src/common/service/telemetry_otlp.go:219-234`): the endpoint's host and
  port as target, TLS from the CA file or the system roots, no proxy, no
  service config, and no retry. Why: one way to dial gRPC in the tree, and
  the interceptor already reports a failed check as retryable
  (`src/services/device/internal/authz/authz.go:67`), so the caller retries.
  `grpc.NewClient` does no I/O (`grpc@/clientconn.go:157-160`), so the two
  start calls prove the engine reachable.
- The model is `internal/authz/openfga/model.json`, the protojson form of
  `openfgav1.AuthorizationModel` without an `id`, parsed with
  `protojson.Unmarshal`. The `Checker` is configured with a store id and a
  model id, sends the model id on every call, and refuses to start unless
  the store exists and `proto.Equal` holds between the embedded model and
  the stored one with its `id` cleared. Why this form: the file is also the
  body a deployment posts to write the model. The server's HTTP gateway
  parses with protojson
  (`github.com/grpc-ecosystem/grpc-gateway/v2@v2.30.0/runtime/marshaler_registry.go:20-29`),
  and `WriteAuthorizationModelRequest` names its fields as
  `AuthorizationModel` does (`api@/openfga_service.pb.go:1700`,
  `api@/authzmodel.pb.go:103`), so the service and the deployment read one
  file into one message. The service's parse refuses an unknown field, which
  the gateway drops
  (`google.golang.org/protobuf@v1.36.12/encoding/protojson/decode.go:41-42`).
  A model built in Go needs a FlowSeer program in every deployment to write
  it, and no OpenFGA endpoint reads prototext. The file sits with the
  adapter because the record gives the adapter the model ("The service
  boundary names no engine") and `internal/authz` then imports no engine
  type. Why the start check: OpenFGA has no flag that creates a store or
  writes a model (`openfga@/cmd/run/run.go:139-390`), so a deployment writes
  it once, and new code against an old model fails every check, as the
  phase 1 plan's open question describes for `platform#claimed`. The server
  stores the type definitions a write held under a new id
  (`openfga@/pkg/server/commands/write_authzmodel.go:67-72`,
  `openfga@/pkg/storage/sqlcommon/sqlcommon.go:1155`), so an unchanged model
  reads back equal.
- OpenFGA answers a gRPC call with its own numbers as the status code:
  1010 for a missing key and 1500 for a wrong one
  (`openfga@/internal/authn/authn.go:16-17`), 5002 for an absent store, and
  2001 for an absent model (`openfga@/pkg/server/errors/errors.go:28`,
  `:88-90`, `api@/errors_ignore.pb.go:34-35`, `:97`, `:403`). Its tests read
  them from a client with `status.Code`
  (`openfga@/tests/functional_test.go:369`, `:391`, `:1035`). The `Checker`
  maps 1000 to 1999, the range the server calls authentication
  (`openfga@/pkg/server/errors/encoded_errors.go:15-16`, `:100`), with
  `Unauthenticated` and `PermissionDenied` to `authz/engine-refused`. It
  maps `Unavailable`, and `DeadlineExceeded` from its own timeout, to
  `authz/engine-unreachable`, and every other code to
  `authz/engine-protocol`. Why: grpc reports a failed connection or TLS
  handshake as `Unavailable` to a call that does not wait for ready
  (`grpc@/picker_wrapper.go:171-176`,
  `grpc@/internal/transport/http2_client.go:297`), and phase 3 must tell an
  unreachable engine from a wrong one.
- The lab issuer is Dex v2.45.1, chosen from the survey under Open
  questions. (decided by the user, 2026-10-03) Why: Open questions. Its organization claim is `groups`,
  which a tenant binds by name like any other
  (`TenantConfig.organization_claim_name`).
- The statements for `github.com/coreos/go-oidc/v3` v3.21.0 and
  `github.com/openfga/api/proto` are approved on 2026-10-03, with the facts
  these Decisions give, so U2 and U3 write `approved: 2026-10-03` and run
  through `go get`. (decided by the user, 2026-10-03)

## Requirements

1. A token from the configured issuer with a valid signature, audience,
   and expiry becomes a principal. The same token with a wrong audience is
   refused with `Unauthenticated`. A token signed by the test issuer with
   `aud: flowseer-device` and `sub: u1` yields `Issuer`, `Subject: "u1"`, and
   the 64 hex characters of `PrincipalID`. With `aud: other`, an unknown
   `iss`, `alg: none`, another key's signature, or no `sub` it is
   `Unauthenticated` with `authn/token-invalid`, and past `exp` or without
   one with `authn/token-expired` (`oidc@/verify.go:262-271`).
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
   id the generated `Validate` refuses, which is anything but 26 characters
   of `0-9A-HJKMNP-TV-Z` (`api@/openfga_service.pb.validate.go:4189-4191`),
   or an endpoint that is not an `https` URL of a host and a port, fails
   with `authz/engine-config` before a file is read or a call is made. A
   key file that is absent, a symlink, or readable by group or world fails
   with the credential package's code, such as `credential/not-found`,
   before a call is made.
4. A container-backed benchmark in
   `src/services/device/test/integration/` reports uncached Check and
   BatchCheck latency for the paths the
   [spike](../research/2026-09-30-openfga-authorization-spike.md) measured,
   and a test pins that `ListObjects` stops at the configured cap without
   marking the result partial. With `OPENFGA_LIST_OBJECTS_MAX_RESULTS=10`
   and 15 edges granted, the response holds 10 objects, and
   `ListObjectsResponse` has no other field
   (`api@/openfga_service.pb.go:134-139`).
5. OpenFGA refuses a client without the preshared key on both listeners.
   `ListStores` over gRPC with no `authorization` metadata fails with status
   code 1010 and with a wrong key 1500. On the lab's HTTP listener
   `GET /stores` with no `Authorization` header answers 401
   `bearer_token_missing`, and a wrong key 401 `unauthenticated`
   (`openfga@/pkg/server/errors/encoded_errors_test.go:80-94`).
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
8. A failed call returns an error and never an answer, by the Decisions'
   mapping. Status 1000 to 1999, `Unauthenticated`, or `PermissionDenied`
   yields `authz/engine-refused`. `Unavailable`, or `DeadlineExceeded` from
   the `Checker`'s own timeout, yields `authz/engine-unreachable`. Any other
   status, a batch result missing a correlation id, or one carrying `error`
   yields `authz/engine-protocol`, except 5002 and 2001 at start, which
   requirement 3 names. An ended caller context answers `ctx.Err()` bare
   (`docs/code-style.md:255-256`). A wrong key, status 1500, is
   `authz/engine-refused`, and a server that never answers is
   `authz/engine-unreachable` once the timeout passes.
9. `DeviceServiceConfig` accepts both sections and refuses an `http` issuer
   or endpoint, an endpoint with a path or without a port, a relative key
   path, two issuers with one URL, and a `platform_admin.issuer` no issuer
   names.

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
tokens claim no organization. `AuthorizationEngine` holds a required
`endpoint`, an `https` URL with a host and a port and no path, query, or
user, required `store_id` and `model_id` of 1 to 64 characters, a required
absolute `preshared_key_file`, and `ca_file`. A message rule holds
`platform_admin.issuer` to one of the issuers when both are set. Comments
name no engine and no transport. `Config` gains `Authentication()` and
`Authorization()`, and the package README documents both sections.
Tests: `TestOperatorAuthenticationAndAuthorization`, shaped like
`TestPlatformAdminAndDevTenant` (`config_test.go:234`): one accepted file,
then requirement 9's refusals, an empty audience, and no issuers, each one
property from the accepted file and each `host/config-invalid`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/store/device/v1 src/services/device/internal/host`

### U2. Token verifier and its interceptor

Files: `go.mod`, `go.sum`, `docs/dependencies/statements/go/github.com/coreos/go-oidc/v3.md`, `src/services/device/internal/authn/principal.go`, `src/services/device/internal/authn/principal_test.go`, `src/services/device/internal/authn/verifier.go`, `src/services/device/internal/authn/verifier_test.go`, `src/services/device/internal/authn/interceptor.go`, `src/services/device/internal/authn/interceptor_test.go`
After: U3
Change: this unit follows U3 only because both edit `go.mod` and `go.sum`.
It opens with the statement, by `docs/conventions/dependencies.md` with
criteria `deploy`, and changes `go.mod` only once `approved` holds the date
of a person's ruling (Open questions). The counts `go run ./tools/deps tree`
prints are added after `go get`.
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
verifies after the replay window. An issuer that never answers yields
`Unavailable` once a client timeout the test sets has passed, and the
client `NewVerifier` builds by default has a 10 s timeout. The interceptor
cases are no header,
`bearer` in lower case, and a streaming call.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- go.mod go.sum docs/dependencies/statements/go/github.com/coreos/go-oidc/v3.md src/services/device/internal/authn`

### U3. The model, its evaluation, and the record's amendment

Files: `go.mod`, `go.sum`, `docs/dependencies/statements/go/github.com/openfga/api/proto.md`, `src/services/device/internal/authz/openfga/model.json`, `src/services/device/internal/authz/openfga/model.go`, `src/services/device/internal/authz/openfga/model_test.go`, `src/services/device/test/integration/openfga_images_test.go`, `src/services/device/test/integration/openfga_env_test.go`, `src/services/device/test/integration/openfga_model_test.go`, `docs/architecture/2026-09-30-operator-authorization-direction.md`
After: none
Change: the unit opens with the statement, by
`docs/conventions/dependencies.md` with criteria `deploy` and the dates,
OSV result, and added versions of the Decisions. `go.mod` requires the
module at the Decisions' version only once `approved` holds the date of a
person's ruling (Open questions). The counts `go run ./tools/deps tree`
prints are added after `go get`.
`model.json` is the Decisions' model as protojson (`schema_version` and
`type_definitions`). `model.go` embeds it, and `Model()` returns the parsed
`AuthorizationModel`, a message of its own per call, or the parse error.
`openfga_images_test.go` is untagged and holds
`openfga/openfga:v1.21.0@sha256:2113c664a486b5da8d7a2cdab479e0d4e30639c80fd2c000540f645c1dbc1e55`
and `postgres:17@sha256:d74eeac9a635390a49bc21bd49fccd973de707e2a53a76ac49b552b8712ec46f`.
The other two files carry `//go:build authz_integration`. The environment
makes a certificate for `localhost`, `127.0.0.1`, and the Docker host. It
starts OpenFGA on its memory datastore with `OPENFGA_AUTHN_METHOD=preshared`,
`OPENFGA_GRPC_TLS_ENABLED`, and `OPENFGA_HTTP_ENABLED=false`
(`openfga@/cmd/run/flags.go:32-46`, `:75-78`). It waits until
`grpc.health.v1.Health/Check` answers `SERVING`, which needs no key
(`openfga@/pkg/server/health/health.go:27-29`), then creates a store and
writes `Model()` to it. Its helpers write relationships, check, and list
objects through `openfgav1.OpenFGAServiceClient`
(`api@/openfga_service_grpc.pb.go:46-64`). Its start takes further server
settings, which U5 uses. It skips under `-short` and fails when Docker is
absent. Once the tagged test passes, the record gains
a dated amendment with the model in DSL and the reason for each relation
beyond its tenant shape, the principal id, the issuer list, and the opaque
engine ids in configuration. It says how the record's sentence on resource
permissions without intersection reads beside `platform#admin` and
`tenant#active_admin`, which a resource permission reaches only through
`tenant`.
Tests: `model_test.go` parses the file and refuses a copy with one
misspelled member. It compares every type's relation names, and the user
types each relation takes directly, with a table in the test, so `site` and
`tag` hold none. It walks `protoregistry.GlobalFiles` for `flowseer.api.`
and asserts that each rule's object type and relation exist in the model,
with `tenant` on every type a request rule names, and fails when it walked
no method. Nothing in this unit compares the file with the DSL above. The
table and the server cases are what catch a wrong translation.
`openfga_model_test.go` evaluates on the server. For every
relation a rule may name, each branch of its definition has one allowed
case and one denial a single relationship away. Member needs both `claimed`
and `enrolled`. A partner admin is a member of the customer only with the
home claim, and holds `capture` on its edge only while
`tenant:C#capturer@tenant:M#active_admin` is stored. A platform admin with
its claim is `admin` of every tenant and lacks `full_payload`.
`edge:E1#capture@user:u` grants nothing on `E2`, and `edge#tenant` is false
for another tenant. The site write of requirement 2 is refused.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- go.mod go.sum docs/dependencies/statements/go/github.com/openfga/api/proto.md src/services/device/internal/authz src/services/device/test/integration docs/architecture/2026-09-30-operator-authorization-direction.md`

### U4. The OpenFGA checker

Files: `src/services/device/internal/authz/openfga/checker.go`, `src/services/device/internal/authz/openfga/checker_test.go`, `src/services/device/internal/credential/keyfile.go`, `src/services/device/internal/credential/keyfile_test.go`, `src/services/device/internal/telemetry/telemetry.go`, `src/services/device/internal/telemetry/telemetry_test.go`, `src/services/device/test/integration/openfga_checker_test.go`
After: U3
Change: `credential.ReadKeyFile(path)` opens with `O_NOFOLLOW`, applies
`checkSecure` (`provider.go:192`), trims one trailing newline, refuses an
empty file, and returns a `secret.Value`. `openfga.New(ctx, Options)` takes
the endpoint, both ids, the key and CA file paths, a per-call timeout that
defaults to 5 s, a `*telemetry.View`, a tracer provider, and a propagator.
`ReadKeyFile` fails with the package's codes (`provider.go:22-27`), which
`openfga.New` passes on. `openfga.New` checks the ids through
`ReadAuthorizationModelRequest.Validate`
(`api@/openfga_service.pb.validate.go:4067`) and the endpoint by requirement
9's rule, then reads the files and builds the connection by the Decisions.
It calls `GetStore` and `ReadAuthorizationModel` and compares by
requirement 3. `Check` and `BatchCheck` implement `authz.Checker` by
requirements 7 and 8. The default timeout sits above OpenFGA's 3 s
`requestTimeout` (`openfga@/pkg/server/config/config.go:89`). A batch
numbers its checks as correlation ids, which satisfy `^[\w\d-]{1,36}$`
(`api@/openfga_service.pb.validate.go:3149`), and reads `allowed` from the
result of each (`api@/openfga_service.pb.go:1215`, `:1329-1341`). `Close`
closes the connection. `View` gains `RecordEngineCall` on
`rpcconv.ClientCallDuration` with `rpc.method` and, on failure,
`error.type`.
Tests: in `checker_test.go` a grpc server on a loopback TLS listener
implements `OpenFGAServiceServer` and records requests and their metadata.
Cases cover requirements 3, 7, and 8, each refusal one property from an
accepted exchange. They include the `CheckRequest` for a query with two
contextual tuples, the `authorization` metadata, each status of requirement
8, an untrusted certificate and a server that never answers under a short
timeout as `authz/engine-unreachable`, a caller's canceled context as
`context.Canceled` bare, a plain `http` endpoint refused before a dial, and
the histogram and span on success and failure. A refused object
(`edge:a:b`, `edge:*`, 257 bytes), user (`user:*`, `user:a#b`, 513 bytes),
and contextual tuple each make no call. That server shares the generated types with the
real one, and its status codes come from this plan's reading of OpenFGA, so
`openfga_checker_test.go`, tagged, repeats requirements 3 and 7 on U3's
environment: `openfga.New` accepts the embedded model as the server returns
it and refuses an absent store, an absent model, a model one relation
short, and a wrong key with `authz/engine-refused`. A batch holding one
query twice answers both. It holds requirement 5's gRPC half.
`keyfile_test.go` mirrors `TestProviderRefusesSymlinkedCredential` and
`TestProviderRefusesGroupOrWorldMode`, and adds an absent file, an empty
one, and one trailing newline.
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
Its `openfga.New` is the start check against a model Postgres stored and
returned. `TestOpenFGAListObjectsStopsAtTheCap` starts U3's environment
with `OPENFGA_LIST_OBJECTS_MAX_RESULTS=10` (`openfga@/cmd/run/flags.go:237`)
on the memory datastore and holds requirement 4's second half through its
`ListObjects` helper.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/test/integration`

### U6. The lab deployment

Files: `deploy/lab/compose.yaml`, `deploy/lab/dex/config.yaml`, `deploy/lab/write-lab-secrets.sh`, `deploy/lab/write-openfga-store.sh`, `deploy/lab/.gitignore`, `deploy/lab/central.textproto`, `deploy/lab/README.md`, `src/services/device/README.md`, `src/services/device/test/integration/lab_fixtures_test.go`, `src/services/device/test/integration/lab_issuer_test.go`
After: U1, U2, U3, U4
Change: the issuer parts are written for Dex, which the user has not
confirmed (Open questions). `compose.yaml` runs U3's Postgres image, its
OpenFGA image once as `migrate` and once as `run`, and
`dexidp/dex:v2.45.1@sha256:8499afd690c437f52301efd2b05b2455da5bd2dfc20332cd697dc9937f808462`.
OpenFGA runs with `OPENFGA_AUTHN_METHOD=preshared`, gRPC and HTTP TLS, and
the playground, check query cache, and cache controller off
(`openfga@/cmd/run/flags.go`). Its health check runs the
`grpc_health_probe` the image ships (`openfga@/Dockerfile.goreleaser:10`)
with `-tls`, `-tls-ca-cert`, and `-tls-server-name`
(https://raw.githubusercontent.com/grpc-ecosystem/grpc-health-probe/v0.4.57/README.md).
The HTTP listener stays on for the store script and requirement 5. Compose
publishes every port on `127.0.0.1` only. `dex/config.yaml` follows the
tag's [`config.yaml.dist`][dx1]: `issuer: https://127.0.0.1:8445/dex`,
memory storage, `web.https: 0.0.0.0:8445` inside the container with
`tlsCert` and `tlsKey` and no `web.http`, `oauth2.passwordConnector: local`,
and `enablePasswordDB`. Its `staticClients` are `flowseer-device`, the
audience, with `trustedPeers: [flowseer-lab]` ([dx11],
`storage/storage.go:166`), and `flowseer-lab` with `secretEnv`. Its
`staticPasswords` are one user in groups `acme` and `globex` and one in
`flowseer-platform`, each with `hashFromEnv` ([dx12],
`cmd/dex/config.go:109`) and a fixed `userID`.
`write-lab-secrets.sh` makes an ignored `secrets/` directory with a lab CA,
one server certificate for `localhost` and `127.0.0.1` that both services
use, a random preshared key at mode 0600, the client secret, a random
password and its bcrypt hash per lab user, and the env files, and refuses
to overwrite. The certificate names `localhost` because OpenFGA's HTTP
gateway verifies its own gRPC listener under that name
(`openfga@/cmd/run/run.go:681-735`). `write-openfga-store.sh` creates the
store, posts `internal/authz/openfga/model.json` with `curl` and `jq`
(`api@/openfga_service.pb.gw.go:1639-1643`), and prints the `authorization`
block. `central.textproto` gains both sections: the issuer above with
audience `flowseer-device` and `organization_claim_name: "groups"`, and
endpoint `https://127.0.0.1:8081` with placeholder ids and a key path that
names no file. It gains no `platform_admin`: the required `subject` is
Dex's `sub`, an encoding of user and connector ([dx2],
`server/oauth2.go:306-313`), which the README's run reads from a token and
phase 4 uses. The lab README lists the files and the run: secrets, `up`,
store, a token by password grant with
`scope=openid groups audience:server:client_id:flowseer-device`, and the
calls of requirement 5. It names `lab_issuer_test.go` with the tier's
command. It says that a user leaves a group by an edit of `config.yaml`
and a restart, and that a restart makes new signing keys. The device README's Layout names the verifier, the model, and
`internal/authz/openfga`, and Deployment says both sections are validated
and not yet enforced.
Tests: `TestTheLabAuthorizationPlaceholdersAreRefused` hands the file's
`authorization` section to `openfga.New` and expects `authz/engine-config`,
and `credential/not-found` once both ids are well formed. `TestTheLabOpenFGARequiresAKeyAndTLS` parses
`compose.yaml` with `gopkg.in/yaml.v3` and asserts the settings above, U3's
image constants, a digest on the Dex image, and the loopback bind.
`TestTheLabIssuerMatchesCentral` parses `dex/config.yaml` and holds its
`issuer` to the one in `central.textproto`, with no `web.http`.
`lab_issuer_test.go` carries `//go:build authz_integration`. It starts the
Dex image on `dex/config.yaml` with a test certificate and fetches a token
by password grant for each user, through an `http.Client` that dials the
container's mapped port for `127.0.0.1:8445`. U2's verifier, given the
issuer entry of `central.textproto`, that client, and a platform triple of
group `flowseer-platform` under claim `groups`, yields two tenants for the
first user through a fake resolver and `Platform` for the second, and
refuses a token asked without the audience scope with
`authn/token-invalid`. That test is the second source for U2's handwritten
issuer. No test runs the store script or the gateway's path to gRPC: the
README's run proves both.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- deploy/lab src/services/device/README.md src/services/device/test/integration/lab_fixtures_test.go src/services/device/test/integration/lab_issuer_test.go`

Waves: U1 U3 | U2 U4 | U5 U6

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
- [ ] The statements for go-oidc and the OpenFGA API module carry a
      person's `approved` date.
- [ ] Both READMEs under `src/services/device/`, the store package README,
      `deploy/lab/README.md`, and the amended record updated in the same
      change.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for U2 filled.
- [ ] No plan labels in code.

## Open questions

One decision waits for the user: which issuer the lab runs. U6 is written
for the recommendation and changes with the answer.

The survey judges eleven issuers at their newest stable release on
2026-10-03 against what this plan needs from a lab issuer: one JWT access
token that lists several organizations of a user, configuration from
files, HTTPS and an `aud` the deployment chooses, the vendor rule as
the record applies it
(`docs/architecture/2026-09-30-operator-authorization-direction.md:201-202`),
the weight on a lab host, maturity, and a user token a script can fetch
for the README's run. Sizes are the compressed linux/amd64 image. Releases
are the stable ones since 2025-10-03. Nothing was started for the survey:
every cell is what the cited page or source file says. All eleven are
open source and self-hostable, so the vendor rule excludes none.

| Issuer | Several organizations in one access token | From files | TLS and audience | Licence and publisher | Image and resources | Maturity | Token from a script | Fit |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Dex v2.45.1 | Yes, as groups. A static user lists `groups` [dx1], a field v2.45.0 added [dx8]. The access token is a JWT built like the ID token, with `groups` as an array [dx2]. | One YAML holds issuer, clients, users, and groups, with no step after start [dx1]. Keys are generated [dx3]. | `web.https`, `tlsCert`, `tlsKey`, and `issuer` [dx1]. `aud` names a registered client, added by scope `audience:server:client_id:` when that client trusts the caller [dx4]. | Apache-2.0 [dx5], CNCF sandbox [dx6] | 48 MB [dx7]. No companion. | Since 2015. 2 releases, newest 2026-03-03 [dx8]. Not OpenID certified [dx9]. | Password grant through `oauth2.passwordConnector` [dx10] | Yes |
| Keycloak 26.8.0 | Yes. Scope `organization:*` puts every organization of the user in claim `organization`, an array of aliases or an object keyed by alias [kc1] [kc2]. JWT by default [kc3]. | `--import-realm` loads realm, clients, users, and keys [kc4]. Organizations and members import by the source, which the guide leaves out [kc5]. | PEM files by flag [kc6]. `--hostname` pins the issuer [kc7]. An audience mapper adds any `aud` [kc8]. | Apache-2.0 [kc13], CNCF incubating [kc9] | 265 MB [kc10]. No companion. 750 MB memory at least, 2 GB recommended [kc11]. | Since 2014. 26 releases, newest 2026-10-01 [kc12]. | Password grant [kc8] | Yes |
| Rauthy 0.36.2 | Yes, as groups. The JWT access token carries `groups` as an array under scope `groups` [ra1]. New clients sign with EdDSA, and RS256 can be chosen [ra2]. | A bootstrap directory declares users, groups, and clients on an empty database [ra3]. The API audience needs a call after start [ra4]. | `[tls] cert_path` and `key_path` [ra5]. `pub_url` fixes the issuer under `/auth/v1/` [ra6]. `aud` is the client id plus `default_aud` [ra4]. | Apache-2.0 [ra7], one maintainer [ra8] | 36 MB [ra9]. No companion. 57 MB memory by its README [ra10]. | Since 2023, before 1.0. 17 releases, newest 2026-08-08 [ra11]. One published audit [ra10]. | Password grant [ra4] | Yes, with one call after start |
| Authentik 2026.8.3 | Yes, as groups. The `profile` mapping emits `groups` as an array [ak1]. JWT by default [ak2]. | Blueprint YAML declares users, groups, providers, and key pairs [ak3]. | HTTPS on 9443 with a certificate chosen on the brand [ak4]. The issuer follows each request's host [ak2]. `aud` is the client id [ak5]. | MIT, with an enterprise directory under its own licence in the same image [ak6]. Authentik Security Inc. | 382 MB [ak7], plus PostgreSQL and a worker. 2 cores and 2 GB [ak8]. | Since 2019. 33 releases, newest 2026-09-17 [ak9]. Three published audits [ak10]. | Password grant with an app password [ak11] | Partial: `aud` is the client id, and no setting pins the issuer |
| Authelia v4.39.28 | Yes, as groups. Opaque by default. A client opts into JWT access tokens [al1], and a claims policy copies `groups` into them as an array [al2]. | `configuration.yml` and a YAML user file hold clients, users, groups, and keys [al2] [al3]. | `server.tls` [al4]. The issuer follows the request URL [al5]. A client's `audience` list allows any string [al1]. | Apache-2.0, community project [al6] | 28 MB [al7]. No companion. | Since 2016. 17 releases, newest 2026-09-17 [al8]. OpenID certified [al9]. | None. The password grant is left out on purpose, and device code needs a browser [al10]. | Partial: no user token without a browser |
| Casdoor v4.14.0 | Yes, as groups. A user has one organization and several groups [cd1]. The default JWT access token carries `groups` as an array [cd2]. | `init_data.json` declares organizations, applications, users, groups, and keys [cd3]. | No documented way to serve HTTPS itself. The docs put Nginx in front [cd4]. `origin` pins the issuer [cd5]. `aud` is the client id on the password grant [cd6]. | Apache-2.0 [cd7], Casbin Inc. [cd8] | 54 MB [cd9]. SQLite or a database server. 100 MB memory at least [cd10]. | Since 2021. 618 releases, newest 2026-10-03 [cd11]. | Password grant [cd12] | Partial: a TLS proxy in front |
| Zitadel v4.19.4 | Only nested. A user has one organization, in a string claim [zt1]. The organizations a user holds roles in sit under role keys in the roles claim [zt2]. A flat claim needs an Action [zt3]. Opaque by default [zt4]. | Init steps declare the instance, one organization, and two users, and have no key for projects, applications, or further users [zt5]. | `--tlsMode enabled` with a key and a certificate [zt6]. `ExternalDomain` pins the issuer [zt7]. `aud` holds client ids and the project id [zt1]. | AGPL-3.0-only [zt8], Zitadel, Inc. | 53 MB [zt9] and a login image, plus PostgreSQL [zt10]. 512 MB memory [zt11]. | Since 2020. 68 releases, newest 2026-10-01 [zt12]. | No password grant [zt13] | Partial: an Action and API provisioning |
| Logto v1.44.0 | Only by script. `organizations` is an ID token claim, and an organization token names one organization [lg1]. A custom JWT script can add all of them, in the open-source edition too [lg2]. | Seeding is a CLI step that takes signing keys [lg3]. Application, API resource, users, organizations, and the script need the Management API [lg4]. | `HTTPS_CERT_PATH` and `HTTPS_KEY_PATH`, and `ENDPOINT` pins the issuer [lg5]. `aud` is the resource indicator, an absolute URI [lg6]. | MPL-2.0 [lg7], Silverhand Inc. [lg8] | 327 MB [lg9], plus PostgreSQL. 2 vCPU and 8 GiB recommended [lg10]. | Since 2022. 14 releases, newest 2026-09-30 [lg11]. | No password grant [lg12]. A personal access token can be exchanged [lg13]. | Partial: a claim script and API provisioning |
| Ory Hydra with Kratos 26.2.0 | Only with own code. Opaque by default, JWT by `strategies.access_token` [or1]. The consent app or a token hook supplies claims [or2], and the stock UI sends none [or3]. | YAML for both servers. Clients and identities are imported by CLI after start [or4] [or5]. | `serve.tls` [or6], `urls.self.issuer` [or1], and an `audience` list per client [or7]. | Apache-2.0 [or8], Ory Corp. The password grant and organizations need the enterprise licence [or9]. | 20, 29, and 90 MB for Hydra, Kratos, and the UI [or10] [or11] [or12], plus a database [or13]. | Hydra since 2016. 2 releases, newest 2026-03-20 [or14]. | None in the open-source build [or9] | Partial: an own consent app or hook |
| Kanidm 1.11.2 | No. The JWT access token has a fixed claim set without groups [kn1]. Groups and claim maps reach the ID token and userinfo [kn2]. | Migrations declare persons, groups, and clients, and no passwords or secrets [kn3]. | TLS is mandatory [kn4]. Each client has its own issuer [kn5]. `aud` is the client name [kn6]. | MPL-2.0 [kn7], volunteer project [kn8] | 60 MB [kn9]. No companion. | Since 2019. 22 releases, newest 2026-09-11 [kn10]. | None: code flow and token exchange only [kn6] | No |
| Pocket ID v2.17.0 | No. `groups` and custom claims reach the ID token and userinfo, and the JWT access token holds neither [pk1]. | Environment variables only. Users, groups, and clients need the UI or the API [pk2]. | `TLS_CERT_FILE` and `TLS_KEY_FILE` [pk3]. `APP_URL` pins the issuer [pk4]. `aud` is a registered API given as `resource`, an absolute URI [pk5]. | BSD-2-Clause [pk6], sponsored maintainers | 35 MB [pk7]. No companion. | Since 2024. 30 releases, newest 2026-10-01 [pk8]. OpenID certified [pk9]. | None: no password grant [pk10] | No |

Recommended: Dex v2.45.1. It is the one candidate that meets every need
from a single file with nothing to do after start, it is 48 MB with no
companion, and it is small enough for `lab_issuer_test.go` to start, which
gives U2's verifier a real issuer as a second source. What it costs: an
organization is a group name on a static user, with no organization object
to manage, that field exists only since v2.45.0, the project made two
releases in twelve months, and it is not OpenID certified. A lab bound to
loopback bears those. The alternatives:

- Keycloak 26.7.4, the newest patch release older than 14 days: the lab
  then mirrors an identity provider that holds organizations as objects,
  managed through `kcadm.sh`. It costs a 265 MB JVM image with 750 MB of
  memory, a realm file exported from a running server, and an organization
  import the guide does not describe. U6 returns to its shape in `0fdd9689`
  and loses the issuer test.
- Rauthy 0.36.2: as light as Dex, with more releases and a published
  audit. It has one maintainer, is before 1.0, and needs one API call
  after start for the audience.
- Any partial fit: the Fit column names what the lab would have to add.

One approval also waits on a person. `docs/conventions/dependencies.md`
has a person approve a statement before the manifest changes, so U3 and U2
each open with their statement and stop before `go get` while `approved`
is empty. The Decisions hold each statement's facts: versions, dates, OSV
results, and what `go.mod` gains. A ruling on both, recorded here as a
Decision with its date, is the date the units write, and they then run
through.

Unverified, for the implementer to settle:

- go-oidc takes its algorithm list from the issuer's ID token metadata
  (`oidc@/oidc.go:174`). An issuer that signs access tokens with another
  algorithm needs the list set. Dex signs both tokens with one key and
  advertises its algorithm ([dx10], `server/handlers.go:118`, `:133`), and
  `lab_issuer_test.go` holds it.
- Dex was read and not run. The password grant with its cross-client
  audience and the groups of a static user are source ([dx10],
  `server/handlers.go:1146-1246`, and `server/server.go:582` of the same
  tag). `lab_issuer_test.go` is their first run.
- The OpenFGA image v1.21.0 dates from 2026-09-20 by the Go proxy. The
  dependency convention's wait covers modules. Applied to the image it
  ends 2026-10-04.
- The dependency admission record, still proposed, wants a statement for
  each input pinned by digest
  (`docs/architecture/2026-10-01-dependency-admission-direction.md:159-165`).
  `docs/dependencies/statements/` holds none for an image, so this plan
  writes none for its three.

For phase 3: whether a start fails when the engine or an issuer is
unreachable, and whether a suspended tenant's organization still yields
`claimed` (`TenantLifecycle`, `tenant.proto:26-33`).

From the review, for the plan's owner:

- A key endpoint that answers 200 with a body that is not a key set yields
  `authn/token-invalid`, not retryable. The verifier classifies an outage by
  the transport's last result for the key URL, as the Decisions word it, and
  that result is a 200
  (`src/services/device/internal/authn/verifier.go`, `HasOutage`). U2's
  Change calls a key failure `authn/unavailable`. Classifying on go-oidc's
  fetch error alone (`oidc@/jwks.go:178`, `:327`) would cover the case and
  departs from the Decision's wording.
- The Decisions say first-party code needs no goroutine. Discovery now runs
  on one goroutine per flight through `spawn.Go`, detached from the caller as
  go-oidc detaches its key fetch (`oidc@/jwks.go:74`), so a caller's
  cancellation cannot fail another caller. The sentence in the Decisions is
  stale.
- With `platform_admin.organization_claim_name` unset in `authn.Options`, the
  verifier falls back to the issuer's organization claim name. The schema
  requires the field (`service_config.proto:189-193`), so phase 3's wiring
  decides whether the fallback is ever reached.

[dx1]: https://raw.githubusercontent.com/dexidp/dex/v2.45.1/config.yaml.dist
[dx2]: https://raw.githubusercontent.com/dexidp/dex/v2.45.1/server/oauth2.go
[dx3]: https://raw.githubusercontent.com/dexidp/website/main/content/docs/configuration/tokens.md
[dx4]: https://raw.githubusercontent.com/dexidp/website/main/content/docs/configuration/custom-scopes-claims-clients.md
[dx5]: https://raw.githubusercontent.com/dexidp/dex/v2.45.1/LICENSE
[dx6]: https://www.cncf.io/projects/dex/
[dx7]: https://hub.docker.com/v2/repositories/dexidp/dex/tags/v2.45.1
[dx8]: https://api.github.com/repos/dexidp/dex/releases?per_page=100
[dx9]: https://raw.githubusercontent.com/dexidp/website/main/content/docs/development/oidc-certification.md
[dx10]: https://raw.githubusercontent.com/dexidp/dex/v2.45.1/server/handlers.go
[dx11]: https://raw.githubusercontent.com/dexidp/dex/v2.45.1/storage/storage.go
[dx12]: https://raw.githubusercontent.com/dexidp/dex/v2.45.1/cmd/dex/config.go
[kc1]: https://raw.githubusercontent.com/keycloak/keycloak/26.8.0/docs/documentation/server_admin/topics/organizations/mapping-organization-claims.adoc
[kc2]: https://raw.githubusercontent.com/keycloak/keycloak/26.8.0/tests/base/src/test/java/org/keycloak/tests/organization/mapper/OrganizationOIDCProtocolMapperTest.java
[kc3]: https://www.keycloak.org/securing-apps/oidc-layers
[kc4]: https://www.keycloak.org/server/importExport
[kc5]: https://raw.githubusercontent.com/keycloak/keycloak/26.8.0/model/storage-private/src/main/java/org/keycloak/storage/datastore/DefaultExportImportManager.java
[kc6]: https://www.keycloak.org/server/enabletls
[kc7]: https://www.keycloak.org/server/hostname
[kc8]: https://www.keycloak.org/docs/latest/server_admin/index.html
[kc9]: https://www.cncf.io/projects/keycloak/
[kc10]: https://quay.io/api/v1/repository/keycloak/keycloak/manifest/sha256:d79bc4bf1c54e802735ef91926b5c003de1fbb50b1a93382611972277219c9ad
[kc11]: https://www.keycloak.org/server/containers
[kc12]: https://api.github.com/repos/keycloak/keycloak/releases/latest
[kc13]: https://raw.githubusercontent.com/keycloak/keycloak/26.8.0/LICENSE.txt
[ra1]: https://raw.githubusercontent.com/sebadob/rauthy/v0.36.2/src/service/src/token_set.rs
[ra2]: https://raw.githubusercontent.com/sebadob/rauthy/v0.36.2/book/src/work/jwks.md
[ra3]: https://raw.githubusercontent.com/sebadob/rauthy/v0.36.2/book/src/config/bootstrap.md
[ra4]: https://raw.githubusercontent.com/sebadob/rauthy/v0.36.2/book/src/work/resource_indicators.md
[ra5]: https://raw.githubusercontent.com/sebadob/rauthy/v0.36.2/book/src/config/tls.md
[ra6]: https://raw.githubusercontent.com/sebadob/rauthy/v0.36.2/src/data/src/rauthy_config.rs
[ra7]: https://raw.githubusercontent.com/sebadob/rauthy/v0.36.2/LICENSE
[ra8]: https://raw.githubusercontent.com/sebadob/rauthy/v0.36.2/Cargo.toml
[ra9]: https://ghcr.io/v2/sebadob/rauthy/manifests/0.36.2
[ra10]: https://raw.githubusercontent.com/sebadob/rauthy/v0.36.2/README.md
[ra11]: https://api.github.com/repos/sebadob/rauthy/releases?per_page=100
[ak1]: https://raw.githubusercontent.com/goauthentik/authentik/version/2026.8.3/blueprints/system/providers-oauth2.yaml
[ak2]: https://raw.githubusercontent.com/goauthentik/authentik/version/2026.8.3/authentik/providers/oauth2/models.py
[ak3]: https://raw.githubusercontent.com/goauthentik/authentik/version/2026.8.3/website/docs/customize/blueprints/index.mdx
[ak4]: https://raw.githubusercontent.com/goauthentik/authentik/version/2026.8.3/website/docs/customize/branding/index.mdx
[ak5]: https://raw.githubusercontent.com/goauthentik/authentik/version/2026.8.3/website/docs/add-secure-apps/providers/oauth2/token_exchange.mdx
[ak6]: https://raw.githubusercontent.com/goauthentik/authentik/version/2026.8.3/LICENSE
[ak7]: https://hub.docker.com/v2/repositories/authentik/server/tags/2026.8.3
[ak8]: https://raw.githubusercontent.com/goauthentik/authentik/version/2026.8.3/website/docs/install-config/install/docker-compose.mdx
[ak9]: https://api.github.com/repos/goauthentik/authentik/releases/latest
[ak10]: https://docs.goauthentik.io/security/audits-and-certs/
[ak11]: https://raw.githubusercontent.com/goauthentik/authentik/version/2026.8.3/website/docs/add-secure-apps/providers/oauth2/machine_to_machine.mdx
[al1]: https://raw.githubusercontent.com/authelia/authelia/v4.39.28/docs/content/configuration/identity-providers/openid-connect/clients.md
[al2]: https://raw.githubusercontent.com/authelia/authelia/v4.39.28/docs/content/configuration/identity-providers/openid-connect/provider.md
[al3]: https://raw.githubusercontent.com/authelia/authelia/v4.39.28/docs/content/configuration/first-factor/file.md
[al4]: https://raw.githubusercontent.com/authelia/authelia/v4.39.28/docs/content/configuration/miscellaneous/server.md
[al5]: https://raw.githubusercontent.com/authelia/authelia/v4.39.28/docs/content/integration/openid-connect/frequently-asked-questions.md
[al6]: https://raw.githubusercontent.com/authelia/authelia/v4.39.28/README.md
[al7]: https://hub.docker.com/v2/repositories/authelia/authelia/tags/4.39.28
[al8]: https://api.github.com/repos/authelia/authelia/releases?per_page=100
[al9]: https://raw.githubusercontent.com/authelia/authelia/v4.39.28/docs/content/blog/we-are-now-openid-certified/index.md
[al10]: https://raw.githubusercontent.com/authelia/authelia/v4.39.28/docs/content/integration/openid-connect/introduction.md
[cd1]: https://casdoor.ai/docs/organization/organization-tree/
[cd2]: https://casdoor.ai/docs/token/overview/
[cd3]: https://casdoor.ai/docs/deployment/data-initialization/
[cd4]: https://casdoor.ai/docs/deployment/nginx/
[cd5]: https://casdoor.ai/docs/basic/configuration/
[cd6]: https://raw.githubusercontent.com/casdoor/casdoor/v4.14.0/object/token_oauth.go
[cd7]: https://raw.githubusercontent.com/casdoor/casdoor/master/LICENSE
[cd8]: https://www.casdoor.com/pricing
[cd9]: https://hub.docker.com/v2/repositories/casbin/casdoor/tags/4.14.0
[cd10]: https://casdoor.ai/docs/basic/try-with-docker/
[cd11]: https://api.github.com/repos/casdoor/casdoor/releases/latest
[cd12]: https://casdoor.ai/docs/how-to-connect/oauth/
[zt1]: https://raw.githubusercontent.com/zitadel/zitadel/v4.19.4/apps/docs/content/apis/openidoauth/claims.mdx
[zt2]: https://raw.githubusercontent.com/zitadel/zitadel/v4.19.4/internal/api/oidc/client.go
[zt3]: https://raw.githubusercontent.com/zitadel/zitadel/v4.19.4/apps/docs/content/apis/actions/complement-token.mdx
[zt4]: https://raw.githubusercontent.com/zitadel/zitadel/v4.19.4/apps/docs/content/concepts/knowledge/opaque-tokens.mdx
[zt5]: https://raw.githubusercontent.com/zitadel/zitadel/v4.19.4/cmd/setup/steps.yaml
[zt6]: https://raw.githubusercontent.com/zitadel/zitadel/v4.19.4/apps/docs/content/self-hosting/manage/tls_modes.mdx
[zt7]: https://raw.githubusercontent.com/zitadel/zitadel/v4.19.4/apps/docs/content/self-hosting/manage/custom-domain.mdx
[zt8]: https://raw.githubusercontent.com/zitadel/zitadel/v4.19.4/LICENSING.md
[zt9]: https://ghcr.io/v2/zitadel/zitadel/manifests/v4.19.4
[zt10]: https://raw.githubusercontent.com/zitadel/zitadel/v4.19.4/apps/docs/content/self-hosting/manage/requirements.mdx
[zt11]: https://raw.githubusercontent.com/zitadel/zitadel/v4.19.4/apps/docs/content/self-hosting/manage/production.mdx
[zt12]: https://api.github.com/repos/zitadel/zitadel/releases/latest
[zt13]: https://raw.githubusercontent.com/zitadel/zitadel/v4.19.4/apps/docs/content/apis/openidoauth/grant-types.mdx
[lg1]: https://docs.logto.io/authorization/organization-permissions
[lg2]: https://docs.logto.io/developers/custom-token-claims/create-script
[lg3]: https://raw.githubusercontent.com/logto-io/logto/v1.44.0/packages/cli/src/commands/database/seed/oidc-config.ts
[lg4]: https://docs.logto.io/organizations/organization-management
[lg5]: https://docs.logto.io/concepts/core-service/configuration
[lg6]: https://docs.logto.io/authorization/global-api-resources
[lg7]: https://raw.githubusercontent.com/logto-io/logto/master/LICENSE
[lg8]: https://logto.io/terms/of-service
[lg9]: https://hub.docker.com/v2/repositories/svhd/logto/tags/1.44.0
[lg10]: https://docs.logto.io/logto-oss/get-started-with-oss
[lg11]: https://api.github.com/repos/logto-io/logto/releases?per_page=100&page=1
[lg12]: https://blog.logto.io/oauth-2-1
[lg13]: https://docs.logto.io/user-management/personal-access-token
[or1]: https://raw.githubusercontent.com/ory/hydra/v26.2.0/spec/config.json
[or2]: https://raw.githubusercontent.com/ory/docs/master/docs/oauth2-oidc/jwt-access-token.mdx
[or3]: https://raw.githubusercontent.com/ory/kratos-selfservice-ui-node/v26.2.0/src/routes/consent.ts
[or4]: https://raw.githubusercontent.com/ory/hydra/v26.2.0/cmd/cmd_import_client.go
[or5]: https://raw.githubusercontent.com/ory/kratos/v26.2.0/cmd/identities/import.go
[or6]: https://raw.githubusercontent.com/ory/docs/master/docs/hydra/self-hosted/ssl-https-tls.mdx
[or7]: https://raw.githubusercontent.com/ory/docs/master/docs/hydra/guides/audiences.mdx
[or8]: https://raw.githubusercontent.com/ory/hydra/v26.2.0/LICENSE
[or9]: https://raw.githubusercontent.com/ory/docs/master/docs/self-hosted/oel/index.mdx
[or10]: https://hub.docker.com/v2/repositories/oryd/hydra/tags/v26.2.0
[or11]: https://hub.docker.com/v2/repositories/oryd/kratos/tags/v26.2.0
[or12]: https://hub.docker.com/v2/repositories/oryd/kratos-selfservice-ui-node/tags/v26.2.0
[or13]: https://raw.githubusercontent.com/ory/docs/master/docs/hydra/self-hosted/dependencies-environment.md
[or14]: https://api.github.com/repos/ory/hydra/releases?per_page=100
[kn1]: https://raw.githubusercontent.com/kanidm/kanidm/v1.11.2/proto/src/oauth2.rs
[kn2]: https://raw.githubusercontent.com/kanidm/kanidm/v1.11.2/book/src/integrations/oauth2/custom_claims.md
[kn3]: https://raw.githubusercontent.com/kanidm/kanidm/v1.11.2/book/src/entry_management.md
[kn4]: https://raw.githubusercontent.com/kanidm/kanidm/v1.11.2/examples/server.toml
[kn5]: https://raw.githubusercontent.com/kanidm/kanidm/v1.11.2/book/src/integrations/oauth2.md
[kn6]: https://raw.githubusercontent.com/kanidm/kanidm/v1.11.2/server/lib/src/idm/oauth2.rs
[kn7]: https://raw.githubusercontent.com/kanidm/kanidm/v1.11.2/LICENSE.md
[kn8]: https://raw.githubusercontent.com/kanidm/kanidm/v1.11.2/SECURITY.md
[kn9]: https://hub.docker.com/v2/repositories/kanidm/server/tags/1.11.2
[kn10]: https://api.github.com/repos/kanidm/kanidm/releases?per_page=100
[pk1]: https://raw.githubusercontent.com/pocket-id/pocket-id/v2.17.0/backend/internal/oidc/claims_service.go
[pk2]: https://pocket-id.org/docs/setup/user-management
[pk3]: https://pocket-id.org/docs/configuration/environment-variables
[pk4]: https://raw.githubusercontent.com/pocket-id/pocket-id/v2.17.0/backend/internal/oidc/provider.go
[pk5]: https://pocket-id.org/docs/guides/apis
[pk6]: https://raw.githubusercontent.com/pocket-id/pocket-id/v2.17.0/LICENSE
[pk7]: https://ghcr.io/v2/pocket-id/pocket-id/manifests/v2.17.0
[pk8]: https://api.github.com/repos/pocket-id/pocket-id/releases?per_page=100
[pk9]: https://raw.githubusercontent.com/pocket-id/pocket-id/v2.17.0/README.md
[pk10]: https://raw.githubusercontent.com/pocket-id/pocket-id/v2.17.0/backend/internal/controller/well_known_controller.go
