---
title: Integration target — HPE Aruba Networking
date: 2026-09-10
scope: Aruba Central / GreenLake cloud API, AOS-CX REST/NETCONF/gNMI, AOS-S REST/SNMP, Instant AP REST, AOS-8 Mobility Controller/Conductor REST, ClearPass API
status: research from public documentation; nothing verified against a live system unless stated
---

# HPE Aruba Networking

HPE sells four technically unrelated management planes under the Aruba brand: the
Central cloud (SaaS, covers wired, wireless, and SD-WAN), AOS-CX switches, AOS-S
switches (the ex-ProCurve line), and ArubaOS wireless (Instant AP / AOS-8 Mobility
Controllers / AOS-10 cloud APs), plus ClearPass as an adjacent policy server. A
customer saying "Aruba" always needs a follow-up question about which platform.
This target file covers the cloud control plane and every device-resident
management surface it fronts.

## What it manages

- **Aruba Central**: multi-tenant SaaS that manages AOS-CX and AOS-S switches,
  Instant APs, AOS-10 APs, SD-Branch gateways, and (via ClearPass integration)
  policy. Organized as customer account → group → site → device. Scale is not
  published in the fetched pages; Central is sold as handling tens of thousands
  of devices per customer account, unverified: exact ceiling.
- **AOS-CX**: standalone or stacked (VSX/VSF) campus and data-center switches,
  self-managed via REST/CLI/SNMP or Central-managed.
- **AOS-S**: the legacy ProCurve/ArubaOS-Switch line, self-managed only via the
  device's own REST/CLI/SNMP; Central manages a subset of AOS-S models but the
  device-side REST API described here is the standalone on-box one.
- **ArubaOS wireless**: Instant APs (controller-less, one AP elected virtual
  controller), AOS-8 Mobility Controllers/Conductor (hardware or virtual
  controller cluster), and AOS-10 (cloud-native AP OS, no local controller,
  Central is the only control plane).
- **ClearPass**: RADIUS/TACACS+ policy server and guest/onboarding portal,
  usually paired with any of the above for 802.1X and device profiling.

## API surface

| Surface | Protocol / transport | Spec available? (OpenAPI/YANG/other, URL) | Versioning scheme |
| --- | --- | --- | --- |
| Aruba Central (Classic) | REST over HTTPS, OAuth2 bearer | Swagger/OpenAPI browsable per-endpoint at [developer.arubanetworks.com/central](https://developer.arubanetworks.com/central/docs/api-reference-guide) [1]; not vendored in this repo | Path-versioned per microservice (e.g. `/monitoring/v1/...`), no single API version number [1] |
| Aruba Central (New Central / "Central NG") | REST over HTTPS, same OAuth2 model | Docs at [developer.arubanetworks.com/new-central](https://developer.arubanetworks.com/new-central/docs/getting-started-with-rest-apis) [7], Postman collection published [8]; no GraphQL found | Same path-versioning pattern; GA but Classic Central still runs in parallel [7][8] |
| Central Streaming API | WebSocket (WSS), subscribe/publish | Documented, no machine-readable spec [4] | One `/streaming/api` path, topic selected by header |
| Central Webhooks | HTTP POST callback, HMAC-signed | Documented, no OpenAPI file found [9] | N/A |
| AOS-CX REST | REST over HTTPS, session cookie + CSRF token | Swagger UI generated and served **per switch** at `https://<switch-ip>/api-docs` or via Web UI > Settings; HPE ships no standalone OpenAPI file [10]. Repo vendors AOS-CX **OpenConfig YANG** (not the REST API) at `spec/yang/aruba/cx/aoscx-yang/10.17/` | `/rest/v1` (deprecated at 10.09, deactivated/removed at 10.12), then `/rest/v10.04`, `/rest/v10.08` … `/rest/v10.16`+ (no v10.05–v10.07 exist — the numbering jumps from v10.04 to v10.08), `/rest/latest` [10][11][23] |
| AOS-CX gNMI / OpenConfig | gNMI over gRPC | 19 OpenConfig modules vendored at `spec/yang/aruba/cx/aoscx-yang/10.17/openconfig/v5_0_0/` [12] | Tied to firmware release directory (`10.17/`) |
| AOS-CX NETCONF | unverified: not found in fetched pages | none found | — |
| AOS-S REST | REST over HTTPS, session cookie | PDF guides per firmware version, e.g. 16.09/16.11 [13]; community docs at [arubaos-switch-rest-guide.readthedocs.io](https://arubaos-switch-rest-guide.readthedocs.io/) [14] | `/rest/v1` fixed; no v7 in current docs, see Known quirks |
| AOS-S SNMP | SNMP v1/v2c/v3 | none vendored under `spec/mib/aruba` (that tree is AOS-CX and wireless only) [see repo check below] | — |
| Instant AP REST | REST over HTTPS, disabled by default | Per-release PDF guides, e.g. 8.9–8.12 [15] | Tied to Instant firmware release (8.x) |
| AOS-8 Mobility Controller/Conductor REST | REST over HTTPS, port 4343 | Developer Hub pages [16][17] plus per-release PDF [18] | `/v1/api`, `/v1/configuration/object` |
| AOS-10 AP | no on-device API; Central/New Central is the only control plane | same as Central rows above | — |
| ClearPass API | REST over HTTPS, OAuth2 (password or client_credentials grants) | Developer Hub, versioned per CPPM release (`/cppm/v6.11.0/docs/...`) [19] | Path includes CPPM version |

## Authentication and authorisation

**Aruba Central (Classic).** OAuth2 authorization-code flow: log in against
`/oauth2/authorize/central/api/login` to get a session/CSRF pair, exchange
those for an authorization code at `/oauth2/authorize/central/api`, then
exchange the code for an access token at `/oauth2/token` [1][2]. Access
tokens expire after 7200 s (2 hours); the authorization code itself must be
redeemed within 300 s [1][2]. Refresh tokens are valid 15 days and are
revoked if unused that long; the documented pattern is to refresh well before
expiry rather than re-issue [3]. Generating a *new* token for the same
`client_id` is capped at one per 30 minutes, which is why the guide pushes
refresh over re-login [2][3]. Every request goes to a **region-specific**
gateway host (the account's dashboard under Menu > API Gateway > REST API
names it; example default `apigw-prod2.central.arubanetworks.com` for one
US cluster) — using the wrong region's gateway fails outright [1].

**New Central.** Same bearer-token model; base URLs are per-region hosts
like `us1.api.central.arubanetworks.com` through `us6`, `de1`–`de3`,
`in1`, `jp1`, `au1` [7]. The onboarding docs don't describe a materially
different auth flow from Classic Central in the fetched pages.

**AOS-CX.** Cookie-based session login; POST to the login endpoint with
`x-use-csrf-token: true`, the response sets a session cookie and returns an
`X-Csrf-Token` header that must be echoed on every subsequent write request
(v10.09+) [10]. Up to 6 concurrent HTTPS sessions per user, 48 per switch;
sessions idle out after 20 minutes and are hard-capped at 8 hours [10]. Write
access needs the switch's REST interface set to `read-write` mode
(`https-server rest access-mode read-write`); default is read-only prior to
firmware 10.04, and read-write on the default VRF from 10.04 on [10].

**AOS-S.** Cookie session from `POST /rest/v1/login-sessions`, `sessionId`
returned in the body and reused as a header on later calls [14]. From AOS-S
16.10.0009 a factory-default switch requires manager credentials to be
configured before the REST API accepts any call other than the credential-set
call itself, otherwise it returns 401 [WebSearch summary of official docs,
unverified against a fetched page — see Known quirks]. AOS-S 16.08+ supports
local, RADIUS, and TACACS+ auth for the REST interface [same source].

**Instant AP.** REST is disabled by default and must be turned on from the
Instant CLI before any call succeeds; only the elected master AP in a cluster
answers REST calls [15].

**AOS-8 Mobility Controller/Conductor.** `GET https://<host>:4343/v1/api/login`
with `username`/`password` as form parameters returns a `UIDARUBA` token that
must be passed as a query parameter on every following call [16][17].
Recommended practice is to reuse one token rather than log in per request.
Session timeout defaults to 900 s (15 minutes), configurable via the
web-server profile; the controller allows at most 64 concurrent sessions
total across CLI, WebUI, and API [17]. Logout via
`GET/POST /v1/api/logout` frees the slot [17].

**ClearPass.** OAuth2 with either `password` or `client_credentials` grant
(6.6+); token lifetime is configured per API client, and long-lived tokens
are a deliberate per-client choice rather than a platform default [19].

**Least privilege.** A read-only inventory integration needs: a Central
operator role scoped to "Network Admin — read only" or the closest
custom-role equivalent (role granularity not enumerated in the fetched
pages, unverified: exact role names), device-local REST/API credentials on
AOS-CX/AOS-S/Instant/AOS-8 only if bypassing Central, and a ClearPass API
client with the `Read` scope. A write/config integration needs the
Central operator role with configuration write, `read-write` REST mode on
AOS-CX, and the corresponding AOS-8/Instant credentials that already have
config privilege on the device (REST doesn't add a separate authorization
layer on top of the device's own role).

## Data model

**Aruba Central hierarchy.** Customer account → group (UI-group or
template-group) → site → device. Devices carry a serial number and MAC as
stable identifiers; Central additionally issues internal device IDs used in
API paths, unverified: whether the API-facing ID is the serial or an opaque
UUID for every endpoint family — the fetched pages show serial-keyed device
paths for monitoring endpoints but don't confirm this for every module.

**UI groups vs template groups.** A **UI group** exposes device configuration
through Central's own workflows/forms; the platform own the schema and there
is no raw-CLI config surface. A **template group** instead holds a CLI script
with `{{variable}}` placeholders; Central pushes rendered CLI to each member
device, and administrators manage the variable bindings by exporting/editing
a JSON or CSV file per device and re-uploading it [20]. A switch or AP can be
moved between the two group types but not run in both simultaneously [20].
This means "config as code" against Central is either UI-group JSON payloads
(schema-validated, declarative) or template-group CLI text (imperative,
untyped) — no single unified config object model spans both group types
based on the fetched documentation.

**AOS-CX inventory read.** REST resources are the OVSDB-backed configuration
database exposed under `/rest/v10.xx/system/...`; `depth` (1–4, default 1)
controls how many levels of nested references are expanded inline, and
`attributes` (comma-separated) narrows the response to named fields, e.g.
`GET /rest/v10.04/system/vlans?depth=2&attributes=id,name,type` [11]. REST
v10.04's `depth=1` is documented as equivalent to REST v1's `depth=0`, i.e.
the depth semantics shifted by one between the v1 and v10.0x API families
[11][23].

**AOS-CX config versioning / persistence.** There is no separate "commit"
step distinct from the write itself: a `PUT`/`PATCH` against `running-config`
takes effect immediately. Persistence across reboot is a separate operation:
`PUT /rest/v10.04/fullconfigs/{to}?from=<source-URI>` copies one full
configuration (`running-config`, `startup-config`, or a named checkpoint)
into another; copying `running-config` into `startup-config` is the REST
equivalent of `write memory` [21]. Checkpoints are just named configs under
the same `fullconfigs` resource, so config versioning is "however many named
checkpoints you choose to keep," not a platform-managed history.

**AOS-8 config write path.** Changes go through
`POST /v1/configuration/object/<object-name>` with `config_path` and
`UIDARUBA` as query parameters; nothing is applied to the running system
until a subsequent `POST /v1/configuration/object/write_memory` (or the
running-config equivalent) is called once per `config_path` that has pending
edits [17][18]. This is an explicit two-phase stage-then-commit model, unlike
AOS-CX where the running config write is immediate and only persistence is
the separate step.

**Read-after-write / propagation.** Not documented in the fetched pages for
either Central or the on-device APIs; unverified: consistency window between
a Central config push and the pushed state showing up in a subsequent GET,
and between a device-side REST write and it being reflected in SNMP/gNMI on
the same box.

## Telemetry and events

- **Central monitoring API**: per-device status, client, and interface
  polling endpoints (path-versioned per microservice, see API surface row);
  no polling interval numbers found in the fetched pages beyond the
  streaming API's own cadence, below.
- **Central Streaming API**: WebSocket (`wss://<base-url>/streaming/api`)
  subscribe/publish. Client sends `UserName` (the Central account email) and
  `Authorization` (a "wss-key") headers plus a `Topic` header naming one
  topic per connection [4]. Documented topics: **Audit** (device
  connectivity, config status, firmware status), **AppRF** (client session
  flow data), and **Monitoring** (state/stats messages, delivered roughly
  every 5 minutes) [4]. Gated behind licensing: the Streaming API tab only
  appears if at least one device in the account carries an Advanced license
  — Foundation-only accounts don't get it [4].
- **Central Webhooks**: HTTP POST to up to 3 configured endpoint URLs per
  webhook, covering user/connectivity/AP/switch/gateway alert categories;
  payload is JSON, authenticity verified via HMAC signature, transport is
  TLS 1.3 [9]. Exact retry count/backoff not found in the fetched page;
  unverified: retry semantics.
- **SNMP traps**: device-resident, independent of Central — AOS-CX and
  wireless controllers both support standard SNMP trap delivery per their
  vendored MIB sets (see "Device-side protocols" below); not re-verified
  here since it's already documented in the vendored corpus.

## Rate limits, quotas, pagination

| Limit | Value | Source |
| --- | --- | --- |
| Central API calls per second | 7 calls/sec per account, HTTP 429 above that | [5] |
| Central API calls per day (base) | 5,000/day for all accounts | [5] |
| Central dynamic daily limit | +30 calls/device/day on Foundation license, +90 calls/device/day on Advanced license, additive on top of the 5,000 base | [5] |
| Central rate-limit headers | `X-RateLimit-Limit-day`, `X-RateLimit-Remaining-day`, `X-RateLimit-Limit-second`, `X-RateLimit-Remaining-second`; daily counters reset at GMT midnight | [5] |
| Central new-token issuance | 1 new access token per `client_id` per 30 minutes; refresh instead | [2][3] |
| Central webhook endpoints | up to 3 URLs per configured webhook | [9] |
| AOS-CX concurrent sessions | 6 per user, 48 per switch | [10] |
| AOS-CX session idle/hard timeout | 20 min idle, 8 hours hard cap | [10] |
| AOS-8 concurrent sessions | 64 total across CLI/WebUI/API | [17] |
| AOS-8 session timeout | 900 s (15 min) default, configurable | [17] |

Pagination: Central's monitoring/inventory endpoints (switches, APs,
gateways, clients, alerts, events, MSP groups) use `offset`/`limit` query
parameters, not a cursor — confirmed by example calls in the Monitoring
API docs [24]. Defaults and maximums are set per endpoint rather than one
API-wide constant: some endpoints default to and cap at `limit=20`, others
allow up to `limit=50` (HTTP 400 `LIMIT_REQUEST_EXCEEDED` above that), and
audit-event endpoints default to 100; `offset` defaults to 0 everywhere
[24]. unverified: the exact default/maximum `limit` for any specific
endpoint FlowSeer would call — read it off that endpoint's page at
build time. AOS-CX's `depth`/`attributes` query parameters shape response
size but are not a cursor/offset pagination mechanism.

## Device-side protocols still available

AOS-CX and AOS-S both keep their on-box management planes reachable whether
or not Central manages them; Central does not lock out local REST/SSH/SNMP.

- **AOS-CX**: REST v1 (removed as of firmware 10.12; use v10.0x from 10.09 on) through v10.16+ per firmware [10][11][23], gNMI with
  OpenConfig (19 modules vendored, see below), SNMP via the 35
  `ARUBAWIRED-*` MIBs already in `spec/mib/aruba/cx/` (documented in
  `docs/research/network-domain-atlas/vendors/aruba.md`, not re-verified
  here). NETCONF: unverified, not found in any fetched page — AOS-CX's own
  docs favor REST and gNMI/OpenConfig; there is no NETCONF mention in the
  Developer Hub introduction page [10].
- **AOS-S**: REST v1 on-box [14], SNMP (versions not confirmed in fetched
  pages; MIB set is not vendored under `spec/mib/aruba` — that tree covers
  AOS-CX wired and ArubaOS wireless only, not ProCurve/AOS-S).
- **ArubaOS wireless**: SNMP via the `WLSX-*`/`AI-AP-MIB` set already
  vendored (25 modules, `spec/mib/aruba/wireless/`), plus each platform's
  own REST as covered above.
- **AOS-10 APs**: no local management surface once converted; Central/New
  Central is the sole control and telemetry path (see "What it manages").

## Provisioning and onboarding

Not covered in depth by the fetched pages. What's confirmed: AOS-10 APs use
zero-touch provisioning, connecting to Central automatically and pulling
their running configuration from the cloud without CLI setup [6]. Instant
APs elect a virtual controller among themselves and only that controller
answers REST calls [15]. Migration paths exist both ways between locally
managed Instant and Central-managed AOS-10 [6]; unverified: exact claim
mechanism (serial-based claim code vs. subscription-key activation) for
Central onboarding of switches and APs — not found in the fetched pages.

## Known quirks and traps

- **AOS-S REST version numbering doesn't match the brief's "v7" label.**
  Every fetched AOS-S guide and the community docs use `/rest/v1/...` as the
  URL prefix, versioned instead by the *firmware* release (16.03 through
  16.11 in the fetched titles) [13][14]. If "v7" refers to an older
  ArubaOS-Switch firmware branch (7.x, pre-16.x renumbering), that
  generation's REST docs were not found in this search; treat any "REST API
  v7" reference as needing a firmware-branch cross-check before assuming it
  maps to the same `/rest/v1` surface.
- **Factory-default AOS-S switches only accept the credential-setup call.**
  From 16.10.0009, an uninitialized switch returns 401 with an explicit
  "configure the manager credentials" message for every other REST call —
  an integration probing a fresh switch needs to handle this as a distinct
  state, not a generic auth failure [WebSearch summary, source page not
  independently refetched — treat as unverified pending a direct fetch].
- **Central token/region mismatch fails silently as an auth error.** The
  gateway host is account-region-specific; using the wrong region's base URL
  produces normal-looking auth failures rather than a clear "wrong region"
  error [1].
- **AOS-CX depth semantics shifted between API families.** `v10.04`
  `depth=1` corresponds to `v1` `depth=0` — porting query strings between
  the two REST generations silently changes how much nested data comes
  back [11].
- **Two incompatible wireless MIB/API worlds coexist.** Confirmed already in
  `docs/research/network-domain-atlas/vendors/aruba.md`: `AI-AP-MIB`
  (Instant) and the `WLSX-*` set (AOS-8 controllers) model the same
  entities with different keys, and a deployment is one or the other, never
  both — the discriminator is `sysObjectID`. AOS-10 adds a third world with
  no local SNMP/REST at all.
- **AOS-CX has no single downloadable OpenAPI spec.** The Swagger
  definition is generated per-switch from its running firmware; HPE
  publishes only PDF/HTML prose guides centrally [10][22]. Any FlowSeer
  OpenAPI vendoring for AOS-CX would need to be pulled from a live switch,
  not a vendor download — consistent with the note already in
  `spec/yang/aruba/cx/SOURCES.md`.
- **New Central and Classic Central run in parallel with overlapping but
  not identical APIs.** The fetched "About" page confirms GA status and
  says they coexist, but gives no API-compatibility matrix or sunset date
  for Classic [8]; treat any integration decision as needing a recheck
  closer to build time.

## What FlowSeer needs

- **Credentials to store**: per-Central-account OAuth2 `client_id` +
  `client_secret` (or the username/password used for the initial
  authorization-code exchange) plus the long-lived refresh token, keyed by
  account and region (gateway host is not derivable from the account ID
  alone — it must be read from the dashboard or a discovery call). For
  device-direct integrations: AOS-CX/AOS-S local admin credentials with
  `read-write` REST mode where write access is needed, AOS-8 admin
  credentials for the `/v1/api/login` flow, and a ClearPass API client
  (`client_credentials` grant preferred for a service integration, per
  scope needed).
- **Inventory endpoints**: Central's device/monitoring REST resources
  (path-versioned per microservice, exact paths need a deeper fetch of the
  API Reference Guide beyond what was pulled here) for cloud-managed
  devices; `GET /rest/v10.xx/system/...` with `depth`/`attributes` tuning
  for AOS-CX devices reached directly; `GET /rest/v1/system` equivalents for
  AOS-S.
- **Config endpoints**: Central UI-group JSON payloads or template-group
  variable files depending on group type [20]; AOS-CX `PUT`/`PATCH` against
  the OVSDB-backed REST tree plus the `fullconfigs` copy endpoint for
  persistence [21]; AOS-8 `POST /v1/configuration/object/...` followed by
  `write_memory` per `config_path` [17][18].
- **Telemetry**: Central Streaming API (WSS, Advanced-license gated) for
  near-real-time state/audit/AppRF data, Central Webhooks for alert push,
  SNMP against the already-vendored `ARUBAWIRED-*` and `WLSX-*`/`AI-AP-MIB`
  sets for anything reached without going through Central.
- **Identifiers to key on**: device serial number and MAC as the stable
  cross-plane identifiers; Central's internal device ID is opaque and
  should be treated as a cache key, not a durable identity, pending
  confirmation it's derived deterministically from the serial.
- **Rate-limit budget**: design Central polling against 7 req/s and the
  5,000/day-plus-per-device-license floor; batch device-count-scaled work
  rather than one call per device per poll cycle to stay inside the daily
  budget on Foundation-only accounts.
- **Minimum API version to target**: Central Classic REST (current, path
  versioned) with a migration watch on New Central; AOS-CX REST v10.04 as
  the floor for `depth`/`attributes` support as documented here, with
  awareness that firmware-specific Swagger definitions mean the actual
  field set must be read live off the target switch generation.

## Sources

1. [OAuth APIs for Access Token — Aruba Developer Hub](https://developer.arubanetworks.com/central/docs/api-oauth-access-token), fetched 2026-09-10. Authorization-code flow steps, endpoints, region base URLs, updated July 2, 2026 per the page.
2. [Access Token Management — Aruba Developer Hub](https://developer.arubanetworks.com/central/docs/access-token-management), fetched 2026-09-10. Token/refresh-token lifetimes, new-token rate limit.
3. Same as [2], additional detail on refresh-token 15-day expiry and removal-on-disuse.
4. [API Streaming — Aruba Central Online Help](https://help.central.arubanetworks.com/latest/documentation/online_help/content/api/api-streaming-public-cloud.htm), fetched 2026-09-10 (via search summary; page content not independently refetched — `help.central.arubanetworks.com` TLS-handshake-timed-out on direct fetch during verification, both via WebFetch and a raw `curl`, so treat the WSS connection details, topics, and licensing gate as unverified against the primary source pending a reachable fetch). WSS connection details, topics, licensing gate.
5. [Usage and Rate Limits — Aruba Developer Hub](https://developer.arubanetworks.com/central/docs/usage-and-rate-limits), fetched 2026-09-10. Per-second/per-day limits, dynamic calculation, rate-limit headers.
6. [Migrating locally managed Instant APs to AOS 10 / Migrating to AOS 10 — HPE Aruba Networking techdocs](https://arubanetworking.hpe.com/techdocs/aos/aos10/migrate/), search-summary level, fetched 2026-09-10. AOS-10 zero-touch provisioning and migration paths; not independently WebFetched.
7. [Getting Started with REST APIs — New Central, Aruba Developer Hub](https://developer.arubanetworks.com/new-central/docs/getting-started-with-rest-apis), fetched 2026-09-10. Bearer-token auth, region base-URL list, REST-only confirmation.
8. [About — New Central, Aruba Developer Hub](https://developer.arubanetworks.com/new-central/docs/about), fetched 2026-09-10. GA status, coexistence with Classic Central, no GraphQL mentioned.
9. [Webhooks — Aruba Central Online Help](https://help.central.arubanetworks.com/latest/documentation/online_help/content/api/api_webhook.htm) (unreachable during verification — `help.central.arubanetworks.com` TLS-handshake-timed-out on both WebFetch and a raw `curl`; treat as a dead link pending a retry) and [Getting Started with Webhooks — Aruba Developer Hub](https://developer.arubanetworks.com/central/docs/webhooks-getting-started), fetched 2026-09-10, re-verified 2026-09-10 (WebFetch confirms 3-URL limit, HMAC, TLS 1.3; exact retry count/backoff not stated on this page). Alert categories, 3-URL limit, HMAC/TLS 1.3.
10. [Introduction — AOS-CX, Aruba Developer Hub](https://developer.arubanetworks.com/aoscx/docs/introduction), fetched 2026-09-10. Login/CSRF flow, session limits/timeouts, read-write mode requirement, versioning paths.
11. Search summary of AOS-CX 10.07–10.16 REST API Guide PDFs (arubanetworking.hpe.com/techdocs/AOS-CX/.../PDF/rest_v10-0x.pdf), fetched 2026-09-10 via WebSearch, not individually WebFetched. `depth`/`attributes` parameter behavior and v1-vs-v10.04 depth offset.
12. `spec/yang/aruba/cx/SOURCES.md` in this repo. OpenConfig module set and vendoring notes (already in repo, not re-fetched).
13. Search summary of "HPE Aruba Networking REST API for AOS-S Switch 16.11" and "Aruba REST API Guide for ArubaOS-Switch 16.09" PDFs, fetched 2026-09-10 via WebSearch. Version-numbering and auth-support history (401 on unconfigured switch, RADIUS/TACACS+ support since 16.08).
14. [Getting started with ArubaOS-Switch RESTful API](https://arubaos-switch-rest-guide.readthedocs.io/), fetched 2026-09-10. `/rest/v1/login-sessions`, `/rest/v1/system` endpoint examples, cookie-based session auth.
15. Search summary of Aruba Instant 8.9–8.12 REST API Guide PDFs, fetched 2026-09-10 via WebSearch. REST disabled by default, master-AP-only in cluster mode.
16. [Getting Started with AOS 8 API — Aruba Developer Hub](https://developer.arubanetworks.com/aos8/docs/getting-started-aos8-restapi), fetched 2026-09-10 (search summary).
17. [Authentication — AOS 8, Aruba Developer Hub](https://developer.arubanetworks.com/aos8/docs/login), fetched 2026-09-10. Login endpoint, UIDARUBA token, session timeout/limit, port 4343.
18. Search summary of "ArubaOS 8.9.0.x API Guide" PDF and [POST — AOS 8, Aruba Developer Hub](https://developer.arubanetworks.com/aos8/docs/post), fetched 2026-09-10. `write_memory` per-`config_path` semantics.
19. [API Authorization – OAuth2 — ClearPass, Aruba Developer Hub](https://developer.arubanetworks.com/cppm/docs/api-authorization-oauth2), fetched 2026-09-10 (search summary). Grant types and per-client token lifetime.
20. Search summary of "Moving a Switch from a UI Group to a Template Group", "Device Groups and Configuration Modes", and "Managing Template Variables" (help.central.arubanetworks.com / arubanetworking.hpe.com techdocs), fetched 2026-09-10. UI-group vs template-group model, variable file export/import.
21. [Copy a configuration to/from running-config or startup-config — AOS-CX, Aruba Developer Hub](https://developer.arubanetworks.com/aoscx/v10.04/reference/put_fullconfigs-to), fetched 2026-09-10. `PUT /fullconfigs/{to}?from=...` endpoint and constraints.
22. `spec/yang/aruba/cx/SOURCES.md` in this repo, already-recorded note that HPE publishes no standalone OpenAPI file for AOS-CX REST.
23. [Tips and Tricks with AOS-CX REST — Aruba Developer Hub](https://developer.arubanetworks.com/aoscx/v10.04/docs/tips-and-tricks-with-aos-cx-rest), fetched 2026-09-10 (WebFetch). Confirms `depth` default 1 in v10.04 vs. 0 in v1. Added during verification, together with a WebSearch confirming (via HPE techdocs page titles/snippets — `arubanetworking.hpe.com` techdocs return HTTP 403 to direct fetch) that REST v1 was deprecated at firmware 10.09 and deactivated at 10.12, and that the v10.0x line has no v10.05–v10.07 releases (jumps v10.04 → v10.08).
24. [Monitoring Customers — Aruba Developer Hub](https://developer.arubanetworks.com/central/docs/monitoring-customers), fetched 2026-09-10 (WebFetch), plus a WebSearch over HPE's API Reference Guide PDF and third-party client docs (centralcli, Nexla). Confirms `offset`/`limit` query-parameter pagination (not cursor-based) and that defaults/maximums vary per endpoint (20, 50, or 100 seen). Added during verification.
