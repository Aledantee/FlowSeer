---
title: Integration target — Cisco enterprise and SMB (non-Meraki)
date: 2026-09-10
scope: Catalyst Center (DNA Center) Intent API, IOS-XE device-side programmability, IOS/IOS-XE CLI+SNMP, Cisco Business/Small-Business switches, NX-OS NX-API. Cisco Meraki is covered separately in cisco-meraki.md.
status: research from public documentation; nothing verified against a live system except the lab SG220 (see docs/research/network-domain-atlas/vendors/cisco.md and the lab capture in ../lab/labsw05-cisco-sg220.md)
---

# Cisco enterprise and SMB (non-Meraki)

Cisco's non-Meraki portfolio splits into product families with almost nothing
in common: a controller (Catalyst Center) that talks a Cisco-specific REST
API, IOS-XE devices that speak standard NETCONF/RESTCONF/gNMI, and a separate
SMB switch line (Cisco Business, formerly Small Business) built on Marvell
silicon with a Radlan-derived OS that has no relation to IOS. This document
covers all four as one target because they share a vendor, not an
architecture. The repo's own vendor dossier at
[`docs/research/network-domain-atlas/vendors/cisco.md`](../../network-domain-atlas/vendors/cisco.md)
already covers the vendored MIB/YANG corpus in depth; this document does not
repeat that inventory and instead references it.

## What it manages

- **Catalyst Center** (formerly DNA Center, renamed 2022): an on-prem or
  cloud-hosted controller/appliance that discovers, inventories, and
  provisions Catalyst switches, ISR/ASR routers, and Catalyst 9800 wireless
  controllers through SD-Access fabric abstractions
  [1][2]. It is a single physical or virtual appliance per site (with HA
  clustering), not a per-device agent.
- **IOS-XE devices** (Catalyst 9000 switches, ISR/ASR1000 routers, Catalyst
  9800 WLCs) can be managed directly, without Catalyst Center, over
  NETCONF/RESTCONF/gNMI or classic SSH+SNMP. This is the path that matters
  when Catalyst Center is not deployed — which is the FlowSeer default case,
  per the lab environment.
- **Cisco Business switches** (CBS220/CBS250/CBS350, successors to the
  SG220/SG250/SG350 "Small Business" line) are unmanaged-to-managed access
  switches for SMB deployments, at a different tier: unmanaged (CBS110),
  smart-lite (CBS220), smart-managed (CBS250), fully managed (CBS350) [3].
  Scale is per-switch or per-site (up to ~15 devices) via Cisco Business
  Dashboard's embedded probe [4], not per-organization the way Meraki or
  Catalyst Center are.
- **NX-OS** (Nexus data-center switches) is out of scope for the lab but
  covered briefly since it shares the Cisco NX-API name pattern with IOS-XE's
  RESTCONF/NETCONF stack and is a plausible future target.

## API surface

| Surface | Protocol / transport | Spec available? | Versioning scheme |
| --- | --- | --- | --- |
| Catalyst Center Intent API | HTTPS REST, JSON, port 443 | Partial OpenAPI: `github.com/cisco-en-programmability/catalyst-center-api-specs` publishes only the Assurance folder as of this fetch; full catalog is browsable (not machine-downloadable as one file) via the in-product Developer Toolkit and the DevNet docs site [5][6] | Per-endpoint `v1`/`v2` paths coexist under one product release (e.g. `deploy-template` has both `v1` and `v2`); the *product* itself is versioned separately (e.g. "3.1.6", "2.3.7.9") and each product version publishes its own DevNet doc tree [7] |
| IOS-XE NETCONF | NETCONF over SSH, port 830 | YANG models: vendored at `spec/yang/cisco/iosxe/2611/` (IOS-XE 26.1.1, sparse-checked from `YangModels/yang`, `vendor/cisco/xe/2611/`) [8] | Per-release YANG module set; capabilities advertised via `<hello>` and per-platform `capability-*.xml` files in the vendored tree |
| IOS-XE RESTCONF | HTTPS REST, RFC 8040, port 443, base `/restconf` | Same YANG corpus as NETCONF | Same as NETCONF; RESTCONF exposes the same datastore/model set over HTTP verbs |
| IOS-XE gNMI/gNOI | gRPC, port 9339 (or via gRPC tunnel for dial-out) | OpenConfig + Cisco native YANG (same corpus) | gNMI protocol version negotiated at capability exchange; TDL (Telemetry Data Language) governs the binary-encoded dial-out payload schema, separate from YANG |
| IOS/IOS-XE CLI + SNMP | SSH/console CLI; SNMP v1/v2c/v3 | `spec/mib/cisco/smb/` only covers CISCOSB; no `CISCO-*` enterprise MIBs (CDP, VTP, STP, ENTITY-SENSOR, POWER-ETHERNET-EXT, CONFIG-COPY) are vendored in this repo — see the gap section in the vendor dossier [9] | N/A (CLI); SNMP versioned per RFC |
| Cisco Business Dashboard API | HTTPS REST, JSON, `/serverapi` or `/api` (see auth) | API reference published on DevNet, current major version v2 [4][10] | Dashboard-product-versioned; API introduced at Dashboard 2.2.0 [10] |
| Cisco Business switch (CBS220/250/350) device-level | Web UI (HTTPS), SNMP v1/v2c/v3, CLI (console/SSH) on CBS220+ and up | No REST API found on the switch itself; see below | N/A |
| NX-OS NX-API | HTTPS REST (JSON-RPC or JSON/XML "NX-API CLI") | Programmability guide per release, no separate OpenAPI found in this search | Per NX-OS release documentation set (e.g. "9.3(x)") |

## Authentication and authorisation

**Catalyst Center Intent API.** POST `/dna/system/api/v1/auth/token` with
HTTP Basic Authentication (`Base64(username:password)`, RFC 7617); the
response body is `{"Token": "..."}`. The token is a bearer credential sent as
the `X-Auth-Token` header on every subsequent call. Token lifetime is 60
minutes; on expiry, calls return `401 UNAUTHORIZED` and the client must
re-authenticate from scratch — there is no refresh-token flow documented
[11]. Catalyst Center also supports an AES-256 credential-encryption variant
of the same endpoint (`CSRO-AES-256` auth scheme) as an optional hardening
feature, documented on the same page [11]. The DevNet authentication pages do
not mention API scopes tied to the token itself; access control is enforced
by the user account's assigned role (e.g. `NETWORK-ADMIN-ROLE`,
`TELEMETRY-ADMIN-ROLE`, `SUPER-ADMIN-ROLE` are named specifically for event
subscription management) [12]. The DevNet "Authentication and Authorization"
page for an earlier release (2.3.7.5) does not mention rate limiting or
429 responses in the fetched content [13] — see the rate-limit section below
for what could and could not be verified.

**IOS-XE NETCONF/RESTCONF/gNMI.** Authenticated with the device's own
AAA-backed local or RADIUS/TACACS+ credentials over the transport's own
security (SSH for NETCONF, TLS for RESTCONF and gRPC). No separate API-key
concept; whoever can log into the device's VTY/HTTP server can call these
APIs, subject to IOS privilege levels and any configured `netconf-yang`
RBAC.

**SNMP.** SNMPv3 `authPriv` requires both an authentication protocol
(MD5/SHA, SHA-256 from IOS-XE 16.6+) and a privacy protocol (DES/AES,
AES-256 from 16.6+), configured per-user under an SNMPv3 group:
`snmp-server group <name> v3 priv` plus `snmp-server user <user> <group> v3
auth sha <pass> priv aes 256 <pass>` [14]. This matches the lab's working
config pattern recorded in the lab capture (`../lab/labsw05-cisco-sg220.md`).

**Cisco Business Dashboard API.** Access keys are created by logging into
the Dashboard GUI (Administration > Users) and are not tied to a Dashboard
user's interactive password; the access key is used to sign a JWT (HS256,
RFC 7519), and that signed JWT — not the raw key — is presented as the
bearer token on each request [4][15]. No OAuth2 flow is documented.

**Cisco Business switch web UI / SNMP.** Local admin credentials for the web
UI; SNMP community strings (v1/v2c) or SNMPv3 users configured the same way
as IOS, via the switch's own CLI or web UI SNMP page.

**What a least-privilege read-only integration needs:** for Catalyst Center,
a service account scoped to a read-only role if the deployment defines one
(not verified which built-in roles exist beyond the three named above); for
direct IOS-XE, an AAA account with `netconf-yang` read access and an SNMPv3
`authPriv` read-only user; for Cisco Business switches, an SNMPv3 read-only
user or a Dashboard access key scoped by whatever the Dashboard's own RBAC
exposes (unverified — not reached in this pass).

## Data model

**Catalyst Center.** Devices sit under a site hierarchy (global > area >
building > floor); device identity is a Catalyst Center-internal UUID
returned from the inventory API, distinct from the device's own serial
number or MAC. Configuration is delivered two ways: SD-Access fabric intent
(declarative — assign a device to a fabric role, create a virtual network,
attach an IP pool) [16], and Template Programmer (imperative — CLI-snippet
templates grouped into projects, with Jinja/Velocity variable substitution,
deployed to a device or device group with a `templateId` and a resolved
`versionedTemplateId`) [17]. The write path for both is asynchronous: a
mutating call returns HTTP `202 Accepted` with a `taskId`, and the caller
polls `GET /dna/intent/api/v1/task/{task_id}` for completion; `202` itself is
not proof of success and must be followed up [18]. The Deploy Template
endpoint has both a `v1` and a `v2` form; `v2` adds composite-template
support (`isComposite`, `memberTemplateDeploymentInfo`) and multiple target
selectors (hostname, UUID, IP, pre-provisioned device) plus a `forcePushTemplate`
flag — the two versions are not drop-in compatible request/response shapes
[7].

**IOS-XE NETCONF.** The candidate datastore is optional and must be enabled
explicitly (`netconf-yang feature candidate-datastore`); once enabled, the
running datastore becomes read-only over NETCONF and all writes go through
candidate → commit. Confirmed-commit is available whenever the candidate
datastore is enabled (they are coupled, not independently toggleable) — a
pending confirmed commit is automatically rolled back if the client session
that issued it terminates before confirming [19]. Configuration is otherwise
split by the `Cisco-IOS-XE-native` model (augments `/native`, i.e. a
YANG-modeled mirror of the classic IOS running-config) versus OpenConfig
models, which the vendored capability files show only partial coverage for
per platform — see "the deviation files" note in the vendor dossier [8].

**IOS-XE RESTCONF.** Same datastore/model set as NETCONF, exposed over HTTP
verbs at `/restconf`, port 443. Supports YANG-Patch (RFC 8072) as an
ordered-list-of-edits PATCH body, letting a client submit multiple
create/merge/delete operations against a target datastore in one HTTP
request; documented consistently from IOS-XE Amsterdam 17.1.x through at
least 17.15.x [20].

**IOS-XE gNMI/gNOI.** gNMI serves `Get`/`Set`/`Subscribe` against the same
YANG paths; port 9339 is the secure gNMI/gNOI server port, confirmed via
`show gnxi state detail` in the IOS-XE docs [21]. Streaming telemetry
("Model-Driven Telemetry", MDT) is delivered either dial-in (collector polls
via gNMI `Subscribe`) or dial-out (device pushes to a collector); from IOS-XE
Dublin 17.11.x, dial-out uses a gRPC tunnel service where the router is the
tunnel client and the collector is the tunnel server [22]. gNOI covers
device operations (file transfer, reboot, cert management) as a separate
gRPC service from gNMI, both on the same port.

**Cisco Business switches.** No org/site hierarchy — each switch is a
standalone management target, discovered by Dashboard via CDP/LLDP/mDNS when
directly connected, or via an embedded/software probe that performs
discovery on behalf of the Dashboard for a device group (up to 15 devices
per embedded probe) [23]. Identity is MAC address plus IP; there is no
separate cloud-assigned ID.

**Read-after-write / propagation.** Not verified for Catalyst Center beyond
the documented task-polling requirement; for IOS-XE NETCONF a `commit` is
synchronous (the RPC does not return until the datastore switch is
complete), so no propagation-delay concern at the device level. Whether
Catalyst Center's SD-Access intent APIs push to devices synchronously with
their task completion, or asynchronously after task completion is reported,
was not confirmed in this pass — treat it as unverified and assume there is
a device-push delay beyond `taskId` completion until proven otherwise.

## Telemetry and events

**Catalyst Center Event Webhooks.** REST/webhook subscriptions are
configured against a destination created under System > Settings > External
Services > Destinations > Webhook, then bound to event types via the Event
Management API (`GET`/create/update Rest-Webhook-Subscriptions endpoints)
[24][12]. Subscription-management calls require one of
`NETWORK-ADMIN-ROLE`, `TELEMETRY-ADMIN-ROLE`, or `SUPER-ADMIN-ROLE` [12].
Retry/backoff behavior for webhook delivery and the payload schema were not
reached in this pass — unverified.

**Catalyst Center polling.** Assurance and inventory data are readable via
the Intent API's own GET endpoints (device list, device detail, client
health, etc.); granularity and refresh interval were not verified in this
pass.

**IOS-XE.** gNMI `Subscribe` (dial-in) and dial-out MDT (above) are the
streaming-telemetry path; SNMP traps and syslog remain available in
parallel — the controller/API layer does not lock these out.

**Cisco Business Dashboard.** Exposes a Notification API (per the DevNet
sidebar: `notification-api`) — not fetched in this pass; presence confirmed
by URL only, contents unverified.

## Rate limits, quotas, pagination

- Catalyst Center: a Cisco Community post (not an official doc) states the
  intent/site API rate limit has been 100 calls/minute since 2.2.2.5;
  unverified against official documentation, treat as **unverified**. The
  official Platform User Guide states each API's rate limit is visible
  per-endpoint in the product's own API catalog (Platform > Developer
  Toolkit > APIs), meaning the limit is not fixed platform-wide but set
  per-API and only discoverable from a live instance [25]. No `X-RateLimit-*`
  response header documentation was found for Catalyst Center in this pass.
- IOS-XE NETCONF/RESTCONF/gNMI: no published rate limit; constrained in
  practice by the device's control-plane CPU and the number of concurrent
  NETCONF/RESTCONF sessions the platform allows (not verified — varies by
  platform).
- Cisco Business Dashboard API: no rate-limit figure found in this pass.
- Pagination: not verified for any of the above APIs in this pass.

## Device-side protocols still available

Catalyst Center does not lock a device out of direct management: a device
managed by Catalyst Center can still be reached over SSH/CLI or SNMP in
parallel, though Catalyst Center may flag out-of-band changes as
configuration drift. IOS-XE devices expose NETCONF, RESTCONF, gNMI/gNOI,
classic SSH CLI, and SNMP simultaneously, all independently enabled/disabled
in the running-config.

## Provisioning and onboarding

Not the focus of this research pass (FlowSeer targets direct device
management over the direct-to-device protocols, not Catalyst Center
onboarding flows). Catalyst Center supports PnP-based zero-touch onboarding
and site-based provisioning workflows per the Device Provisioning and Device
Onboarding DevNet doc sections [26][27] — contents not fetched.

## Known quirks and traps

- **Catalyst Center OpenAPI coverage is partial.** The
  `cisco-en-programmability/catalyst-center-api-specs` GitHub repo — the
  closest thing to a downloadable OpenAPI spec — currently publishes only
  the Assurance API folder, not the full Intent API surface [6]. A client
  wanting a complete spec has to pull it from the Developer Toolkit inside a
  running Catalyst Center instance, which means the spec is
  version-and-instance-specific rather than a stable public artifact.
- **Token lifetime is short and non-refreshable.** 60 minutes with no
  refresh token means any long-running integration must re-authenticate on
  a timer or on `401`, not just on failure of a specific call [11].
- **`202 Accepted` is not success.** Every mutating Catalyst Center call
  needs the follow-up task poll; treating `202` as done will silently miss
  failed template deployments or fabric provisioning steps [18].
- **v1/v2 coexist per endpoint, not per product.** `deploy-template` has a
  `v2` with a different request body while other endpoints remain `v1`-only
  in the same product release; there is no single "API version" number to
  pin against [7].
- **Candidate datastore and confirmed-commit are coupled, not independent,
  on IOS-XE NETCONF** — enabling one enables the other; you cannot have
  confirmed-commit without giving up direct-write access to `running` [19].
- **CISCOSB vs. IOS-XE are unrelated management planes.** The Cisco Business
  (CBS/SG) line runs a Marvell/Radlan-derived OS with the `rl` MIB prefix,
  not IOS, and has no NETCONF/RESTCONF/YANG at all — confirmed by the
  absence of any Cisco Business entries in `spec/yang/cisco/` and by the
  product-tier comparison table (below) [3]. Do not assume any Catalyst
  Center or IOS-XE technique transfers to CBS/SG hardware.
- **The 220-series CLI story changed with the CBS rebrand.** The current
  CBS220 (the CBS-branded successor to SG220, current since ~2021)
  documents CLI access over console and SSH, with the web UI presented as
  the primary method and CLI flagged as needing "advanced user skills"
  [28]. The lab's device is the earlier SG220 (pre-CBS rebrand); per
  the lab capture (`../lab/labsw05-cisco-sg220.md`)
  it is managed only via web UI and SNMP with no working CLI observed —
  this is a lab-verified fact about that specific unit's firmware/branding
  generation, not a general claim about the 220 tier. Confirm CLI
  availability against the specific unit's firmware before assuming either
  behavior for a new SG220/CBS220 in the field.
- **No `CISCO-*` enterprise MIBs are vendored in this repo.** CDP, VTP, STP
  extensions, entity sensor, and config-copy MIBs are all absent from
  `spec/mib/cisco/` — only the CISCOSB corpus is present. This means CDP
  neighbor discovery, VTP VLAN reads, and SNMP-based config-copy jobs
  cannot be built against this repo's vendored MIBs today for IOS/IOS-XE
  devices; see the "gap" section of the vendor dossier for the full list
  [9]. `CISCOSB-CDP` in `spec/mib/cisco/smb/CISCOSBCDP.mib` covers CDP for
  the Business/Small-Business line only, not IOS/IOS-XE.

## What FlowSeer needs

- **Credentials to store per target type:** Catalyst Center — username and
  password for the Basic-Auth token exchange (or an AES-256 pre-shared key
  if that mode is enabled), refreshed hourly. IOS-XE direct — AAA
  credentials for NETCONF/RESTCONF/gNMI plus a separate SNMPv3
  authPriv user/auth-pass/priv-pass triple. Cisco Business — an SNMPv3
  authPriv triple for device-level polling, and/or a Dashboard access key
  if a Dashboard instance is in scope.
- **Endpoints for inventory:** Catalyst Center device inventory GET
  endpoints (not enumerated in this pass); for direct IOS-XE, NETCONF/
  RESTCONF reads against `Cisco-IOS-XE-native`,
  `Cisco-IOS-XE-interfaces-oper`, `Cisco-IOS-XE-platform-oper`, and
  `Cisco-IOS-XE-environment-oper` (all vendored under
  `spec/yang/cisco/iosxe/2611/`); for SMB switches, SNMP walks against
  `spec/mib/cisco/smb/` tables (`rlSysNameTable`, `rlMngInfListTable`, the
  interface/PoE/EEE tables catalogued in the vendor dossier).
- **Endpoints for config:** Catalyst Center Template Programmer
  (`template-programmer/project`, `template/deploy` v1 or v2) for
  imperative pushes, SD-Access APIs for fabric-declarative changes; direct
  IOS-XE NETCONF `edit-config` against candidate + confirmed-commit where
  available, or RESTCONF PATCH/YANG-Patch; SMB switches have no config API
  beyond CLI/web — SNMP `rlCopyTable` handles config backup/restore jobs
  only, per the vendor dossier.
- **Endpoints for telemetry:** Catalyst Center event webhooks for
  push-model alerts; gNMI `Subscribe` (dial-in or dial-out) for IOS-XE
  streaming stats; SNMP polling/traps as the universal fallback across all
  four families.
- **Identifiers to key on:** Catalyst Center's internal device UUID (not
  stable across a device being removed and re-added, unverified); IOS-XE
  and SMB switches — MAC address as the only vendor-stable identifier found
  in this pass; serial number where SNMP `entPhysicalSerialNum` or
  equivalent is available (not vendored for IOS-XE in this repo's MIB set —
  see the gap section).
- **Rate-limit budget:** unresolved for Catalyst Center in this pass — do
  not assume a numeric budget without checking the live instance's own API
  catalog per endpoint, per the official guidance that the limit is
  per-API and instance-visible only [25].
- **Minimum API version to target:** for Catalyst Center, target whatever
  DevNet doc tree matches the deployed product release (this pass consulted
  3.1.6 and 2.3.7.x docs, which are not interchangeable); for IOS-XE, the
  vendored YANG set is 26.1.1 — older devices will advertise a smaller
  module set via their own `<hello>` capabilities, so capability discovery
  at connect time is required rather than assuming the full 2611 corpus is
  present.

## NX-OS NX-API (brief)

Out of current scope but noted for completeness. NX-API exposes CLI-style
management over HTTPS using JSON-RPC ("NX-API CLI" — send a CLI command
string, get back structured JSON/XML) as well as a more RESTful subset, and
is documented per NX-OS release inside each platform's own Programmability
Guide (e.g. "Nexus 9000 Series NX-OS Programmability Guide, Release 9.3(x)")
rather than one central spec [29]. A "NX-API Developer Sandbox" runs on the
switch itself and translates between CLI commands and their JSON/XML
payload equivalents interactively, which is useful for discovering the
JSON-RPC method/params shape for a given CLI command without external
documentation [30]. Nexus switches also support NETCONF and RESTCONF
alongside NX-API [29], but neither was checked against the vendored YANG
corpus (`spec/yang/cisco/iosxe/` is IOS-XE only; there is no
`spec/yang/cisco/nxos/` in this repo).

## Sources

1. https://developer.cisco.com/docs/catalyst-center/ — fetched 2026-09-10; Catalyst Center 3.1.6 introduction, Intent API description, DevNet/OpenAPI availability statement.
2. https://developer.cisco.com/docs/catalyst-center/software-defined-access-sda/ — search-result summary 2026-09-10; SD-Access API scope (fabric, edge/border/control-plane roles, virtual networks).
3. https://www.cisco.com/c/en/us/support/docs/smb/switches/Cisco-Business-Switching/kmgmt-3081-Cisco-Business-Switch-Comparison.pdf — fetched and read as PDF 2026-09-10; tier comparison table (CBS110 unmanaged, CBS220 smart lite, CBS250 smart managed, CBS350 managed) including the Cisco Business Dashboard "Direct management" vs "Embedded probe" row.
4. https://developer.cisco.com/docs/business-dashboard/ — search-result summary 2026-09-10; Business Dashboard API overview, REST/JSON design, access-key auth.
5. https://developer.cisco.com/docs/catalyst-center/ — same as [1]; OpenAPI/sample-code/sandbox availability statement.
6. https://github.com/cisco-en-programmability/catalyst-center-api-specs — search-result summary 2026-09-10; repo currently publishes only the Assurance API folder.
7. https://developer.cisco.com/docs/catalyst-center/deploy-template-v2/ — fetched 2026-09-10; v2 deploy-template fields (isComposite, memberTemplateDeploymentInfo, versionedTemplateId, forcePush) and confirmation that v1/v2 paths coexist per product release "3.1.6".
8. spec/yang/cisco/iosxe/SOURCES.md (repo-local) — read 2026-09-10; vendored IOS-XE 26.1.1 YANG set, sparse-checked from YangModels/yang `vendor/cisco/xe/2611/`.
9. docs/research/network-domain-atlas/vendors/cisco.md (repo-local) — read 2026-09-10; CISCOSB and IOS-XE corpus shape, and the enumerated gap in enterprise CISCO-* MIB vendoring (CDP, VTP, STP, ENTITY-SENSOR, CONFIG-COPY, AIRESPACE).
10. https://developer.cisco.com/docs/business-dashboard/versions/ and /release-notes/ — search-result summary 2026-09-10; current API major version v2, introduced at Dashboard 2.2.0.
11. https://developer.cisco.com/docs/catalyst-center/authentication/ — fetched 2026-09-10; `/dna/system/api/v1/auth/token` endpoint, Basic and AES-256 credential formats, `X-Auth-Token` header, 60-minute token lifetime, 401-on-expiry behavior.
12. Search results for "Cisco Catalyst Center Event Webhook subscription API documentation" (https://developer.cisco.com/docs/catalyst-center/event-management/, https://developer.cisco.com/docs/dna-center/get-restwebhook-event-subscriptions/) — summarized 2026-09-10; webhook destination configuration path, subscription endpoints, required roles (NETWORK-ADMIN-ROLE, TELEMETRY-ADMIN-ROLE, SUPER-ADMIN-ROLE).
13. https://developer.cisco.com/docs/dna-center/2-3-7-5/authentication-and-authorization/ — fetched 2026-09-10; confirmed no rate-limit or RBAC-scope content in this page's fetched content.
14. Search results for "Cisco IOS SNMPv3 authPriv configuration" (networklessons.com, cisco.com SNMPv3 Community MIB Support guide) — summarized 2026-09-10; authPriv requires auth (MD5/SHA, SHA-256 from 16.6+) and priv (DES/AES, AES-256 from 16.6+), `snmp-server group ... v3 priv` / `snmp-server user ... v3 auth sha ... priv aes 256 ...` syntax.
15. https://developer.cisco.com/docs/business-dashboard/how-to-authenticate/ — referenced via search summary 2026-09-10; access keys created under Administration > Users, not fetched directly.
16. Search results for "Cisco Catalyst Center SDA fabric API sites virtual networks provision" — summarized 2026-09-10; fabric/site/virtual-network/IP-pool/role-assignment operations list.
17. Search results for "Cisco Catalyst Center Template Programmer API projects templates deploy" (https://developer.cisco.com/docs/catalyst-center/deploy-template/) — summarized 2026-09-10; project/template/deploy endpoint paths and variable-substitution model.
18. Search results for "Cisco Catalyst Center Intent API task API asynchronous polling taskId execution status" — summarized 2026-09-10; 202 Accepted + taskId pattern, `/dna/intent/api/v1/task/{task_id}` polling, explicit warning that 202 is not proof of success.
19. Search results for "IOS-XE NETCONF candidate datastore confirmed-commit support" (Cisco Programmability Configuration Guides, IOS-XE 17.1.x through 26.x) — summarized 2026-09-10; `netconf-yang feature candidate-datastore` command, confirmed-commit coupling, rollback-on-session-termination behavior.
20. Search results for "IOS-XE RESTCONF YANG-Patch RFC 8072 support" (Cisco Programmability Configuration Guides, 17.1.x–17.15.x) — summarized 2026-09-10; YANG-Patch PATCH-method support description.
21. Search results for "Cisco IOS-XE gNMI gNOI port 9339 dial-out model-driven telemetry gRPC TDL" (Cisco IOS XE 17.11–17.18 Programmability Configuration Guides, "gNMI Dial-Out Using the gRPC Tunnel Service") — summarized 2026-09-10; port 9339 confirmed via `show gnxi state detail`, gRPC tunnel dial-out model from 17.11.1.
22. Same as [21] — dial-out tunnel-client/tunnel-server roles.
23. Search results for "Cisco Business Dashboard 'Direct management' vs 'Embedded probe'" (Cisco Business Dashboard and Probe Quick Start / Admin Guides) — summarized 2026-09-10; direct-managed vs. probe-managed device modes, 15-device embedded-probe capacity.
24. Same as [12].
25. Search results for "Cisco Catalyst Center Intent API rate limit X-RateLimit" (Cisco Catalyst Center Platform User Guide, releases 2.3.7.x and 3.1.x) — summarized 2026-09-10; per-API rate limit visible in the product's own API catalog under Platform > Developer Toolkit > APIs; a Cisco Community post's "100 calls/minute since 2.2.2.5" claim is explicitly unverified against official docs.
26. https://developer.cisco.com/docs/catalyst-center/device-provisioning/ — referenced via search summary 2026-09-10, not fetched.
27. https://developer.cisco.com/docs/catalyst-center/device-onboarding/ — referenced via search summary 2026-09-10, not fetched.
28. https://www.cisco.com/c/en/us/td/docs/switches/lan/csbss/CBS220/Adminstration-Guide/cbs-220-admin-guide/get-to-know-your-switch.html — fetched 2026-09-10; CBS220 console/SSH CLI availability alongside web UI, CLI described as requiring advanced user skills.
29. Search results for "Cisco NX-API NX-OS REST CLI JSON-RPC documentation" (Nexus 9000 NX-OS Programmability Guide 9.3(x) and 7.x, Nexus 7000 Programmability Guide) — summarized 2026-09-10; NX-API CLI JSON-RPC transport, NETCONF/RESTCONF availability alongside NX-API on Nexus platforms.
30. Same as [29] — NX-API Developer Sandbox description (on-switch CLI↔JSON/XML translator).
31. https://www.cisco.com/c/en/us/td/docs/cloud-systems-management/network-automation-and-management/catalyst-center/catalyst-center-va/esxi/2-3-7/deployment-guide/b_cisco_catalyst_center_237x_on_esxi_deployment_guide.html — fetched 2026-09-10; Catalyst Center 2.3.7.x on ESXi Deployment Guide VM resource requirements: 32 vCPUs with 64-GHz reservation, 256 GB DRAM with 256-GB reservation, 3-TB SSD, VMware vSphere (ESXi/vCenter) 7.0.x or later, Intel Xeon Scalable (Cascade Lake or newer) or AMD EPYC Gen2 at 2.1 GHz or better; the guide warns that changing the VM's resource allocation/reservation may cause operational failure.
32. Search results for "Cisco 220 series smart switch vs 250 350 series CLI comparison" (Cisco 220/250/350 series support pages, B&H comparison page) — summarized 2026-09-10; tier naming (smart lite / smart managed / managed) cross-checked against source [3]'s table.

### Catalyst Center virtual appliance requirements

Per the Catalyst Center 2.3.7.x on ESXi Deployment Guide (source [31],
directly fetched): 32 vCPUs with a 64-GHz CPU reservation, 256 GB DRAM with a
256-GB reservation, and a 3-TB SSD, on VMware vSphere (ESXi/vCenter) 7.0.x or
later, with an Intel Xeon Scalable (Cascade Lake or newer) or AMD EPYC Gen2
processor at 2.1 GHz or better. This is markedly larger than a typical
lab/eval VM footprint — plan capacity accordingly. The guide warns that
changing the VM's resource allocation or reservation after deployment may
cause operational failure; figures are release-specific (2.3.7.x) and should
be re-checked against the deployed release's own deployment guide, since
Catalyst Center's footprint has changed across releases.
