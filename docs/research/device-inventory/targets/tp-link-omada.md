---
title: Integration target — TP-Link Omada (and TP-Link business switches)
date: 2026-09-10
scope: Omada Software/Hardware Controller (OC200/OC300, v5.x), Omada Open API v1, Omada Cloud-Based
  Controller and Omada Cloud, standalone JetStream/Omada switches, standalone EAPs, Omada discovery
status: research from public documentation; nothing verified against a live system unless stated
---

# TP-Link Omada (and TP-Link business switches)

Every claim carries a source link. Where documentation is contradictory or version-dependent, say
which version the claim holds for. Nothing for TP-Link/Omada is vendored yet under `spec/openapi/`
or `docs/research/network-domain-atlas/vendors/` (checked 2026-09-10, no matching files); this
document is the first record of the target.

## What it manages

Omada is TP-Link's SDN-style controller product line for its business-grade switches (JetStream,
SafeStream router line, and the Omada-branded switch series), access points (EAP), and gateways.
One controller manages one or more **sites** under an org-like grouping (the controller itself, or
a **Global View**/MSP layer above multiple sites) [1][2]. Deployment shapes:

- **Software Controller**: a Java application the operator runs on their own Linux/Windows/Docker
  host or a NAS package, listening on ports distinct from the hardware controller (8088/8043/8843
  vs. 80/443) [3].
- **Hardware Controller**: purpose-built appliances OC200 and OC300, running the same controller
  software preinstalled [3][4].
- **Omada Cloud-Based Controller**: TP-Link-hosted, no customer-premises hardware; see the Cloud
  section below.
- **Standalone mode**: an individual switch, EAP, or gateway with no controller, managed only
  through its own local web UI (and CLI/SSH where the model has one); see the standalone sections
  below.

Every Omada device also has a **Controller Inform URL** field (System/Management settings on the
device) that can be pointed at a controller manually instead of relying on discovery, which matters
for L3-separated deployments [5].

Scale: no per-controller device or site count ceiling is published in the sources checked.
unverified: any specific device-count or client-count limit for the Software or Hardware
Controller.

## API surface

| Surface | Protocol / transport | Spec available? | Versioning scheme |
| --- | --- | --- | --- |
| Omada Open API | HTTPS + JSON, OAuth2 (client-credentials or authorization-code) | Not published as a downloadable spec file; an "Online API Document" is served by the controller itself once an app is registered, reachable from Settings → Platform Integration → Open API [6][7] | Single published major version, path prefix `/openapi/v1/`; a TP-Link community documentation hub lists Open API as supported on Omada Pro and controllers v5.12+ [2]. unverified: the verbatim "Open API currently only has the V1 version" wording — not found on the forum page [8] previously cited for it |
| Legacy Web API (`/api/v2`) | HTTPS + JSON, session cookie + CSRF token | Undocumented by TP-Link; reverse-engineered by community projects (`ghaberek/omada-api`, `MarkGodwin/tplink-omada-api`, a maintained gist) against controller-shipped PDF changelogs per version [9][10][11] | No stable contract; TP-Link ships a per-release PDF ("Omada SDN Controller API Document") for v2.6.0 through at least v5.9.9, and endpoints have moved across releases without notice [9] |
| JetStream/Omada switch CLI | SSH/Telnet/console, Cisco-IOS-like text | CLI Reference Guide PDFs per model family (e.g. T1700G, T3700G, TL-SG3216) [12] | Tied to switch firmware, not to the controller |
| SNMP (switches, EAPs) | UDP, versions vary by model | MIBs not checked in this pass; not found in `spec/mib/` | Firmware-tied |
| EAP standalone SSH | SSH, vendor-documented command set | "SSH Commands Guide for Omada AP" referenced by support articles, not fetched in this pass [13] | Firmware-tied |

The brief's two path shapes both exist but serve different generations of the API: `/openapi/v1/
{omadacId}/sites/...` is the current Open API, while `/{omadacId}/api/v2/...` is the legacy,
undocumented Web API that the controller's own front end still uses [2][10]. TP-Link's own community
documentation hub distinguishes them as "Web API" (legacy, changes across controller versions) and
"Open API" (OAuth2, the stable third-party integration surface) [2]; the additional detail that the
legacy surface is cookie-based and drives the controller's own UI comes from the login-flow evidence
in source 10, not from TP-Link's own terminology.

## Authentication and authorisation

**Open API (`/openapi/v1/...`)**

- Two modes, selected when registering the app under Settings → Platform Integration → Open API:
  **Client Credentials mode** ("suitable for system-to-system integration where no user interaction
  is required") and **Authorization Code mode** ("suitable when a third-party platform or user needs
  to log in through the Controller with user identity verification") [7][8].
- Client Credentials mode: register an app, choose a role and site privileges for the client, and
  receive a Client ID and Client Secret (secret shown once) [6][8]. Token acquisition is
  `POST /openapi/authorize/token?grant_type=client_credentials` [1].
- Authorization Code mode additionally requires a redirect URL at registration and issues both an
  access token and a refresh token; the client renews via a documented refresh API rather than a
  full re-login [7][8].
- Every request needs the controller's own identifier, called **`omadacId`** (also labeled "Omada
  ID" or "MSP ID / Customer ID" in the UI), found on the same Platform Integration page and required
  in the URL path `/openapi/v1/{omadacId}/...` [1][6].
- Authorization header format: `Authorization: Bearer AccessToken={accessToken}` — a community
  reply from TP-Link staff corrects a common mistake of omitting the `Bearer` prefix or the
  `AccessToken=` literal, both of which are required together [14].
- Token lifetime and refresh-token lifetime are not stated in any source fetched in this pass, only
  that the access token "has a limited lifespan" and expiry produces error code `-44112` with the
  message "The access token has expired. Please re-initiate the refreshToken process" [8][14].
  unverified: exact access-token and refresh-token TTLs in seconds.
- **Rate limit**: "Each Omada Pro controller and each Omada organization has an API call budget of
  10 requests per second," confirmed by TP-Link support staff as fixed and not raisable on request;
  most individual endpoints additionally carry an unspecified concurrency limit, and TP-Link
  recommends applying global-scope configuration changes before site-scope ones for that reason
  [15]. unverified: the concurrency limit's actual value, and whether the 10 req/s budget is a
  token-bucket or fixed window.
- Cloud/paid-tier gating: the legacy README for a Home Assistant integration states cloud-mode
  Open API access requires the Standard cloud tier or higher — "Free cloud accounts won't work" —
  and that Omada Cloud Essentials specifically lacks Open API support [16]. A separate community KB
  reply states "Essential version doesn't support OpenAPI" [6]. Both describe the same restriction
  from different angles; neither is a primary TP-Link API reference document, so treat the specific
  tier name as best-effort rather than confirmed against the current pricing page.

**Legacy Web API (`/api/v2`)**

- Login: `POST /{omadacId}/api/v2/login` with a JSON body of `username`/`password`, returning a
  session token and the `omadacId` in the response body [10].
- The returned token is echoed back on subsequent requests as a `Csrf-Token` header; the session
  itself is carried in a cookie whose name changed between controller generations — `TPEAP_SESSIONID`
  before v5.11, `TPOMADA_SESSIONID` from v5.11 onward, per a Python client's documented login flow
  [11]. This cookie-name split was not independently cross-checked against a second source in this
  pass. unverified: whether the split is exactly at v5.11.0 or a later patch within the v5.11 line.
- A legacy session and CSRF token are not accepted by Open API endpoints and vice versa; the two
  authentication stacks are fully separate on the same controller [11].

**What a least-privilege read-only integration needs**: Client Credentials mode, a role/site
privilege scoped to the sites being read, no redirect URL. **What a write integration needs**:
either mode with a role that has write privilege on the target sites; TP-Link's own guidance to
apply global config before site config suggests write ordering matters for consistency, not just
for the concurrency limit [15].

## Data model

- Hierarchy: controller (identified by `omadacId`) → sites (`siteId`) → devices, matching the
  `/openapi/v1/{omadacId}/sites/{siteId}/...` and `/{omadacId}/api/v2/sites/{siteId}/...` path
  shapes seen in both API generations [1][10]. The legacy API also exposes a site list at
  `GET /{omadacId}/api/v2/sites?currentPage=1&currentPageSize=1000`, showing pagination by
  `currentPage`/`currentPageSize` query parameters rather than cursors [10].
- Site creation via Open API returns `errorCode 0` and a `siteId` on success, confirming `siteId` is
  server-assigned at creation time rather than caller-supplied [1].
- Device identifiers: the MarkGodwin/tplink-omada-api Open API client keys devices by MAC address —
  its `OmadaDevice` model reads `self._data["mac"]` and builds each device's resource path as
  `{eaps|gateways|switches}/{mac}` [7] — consistent with MAC address appearing directly in
  hotspot-client-auth paths (`.../clients/{clientMac}/auth`) [1]. This is corroborating evidence from
  a community client library, not a primary TP-Link reference. unverified: whether a separate
  numeric/controller-assigned device ID also exists as an alternate key in TP-Link's own Open API
  reference (the interactive "Online API Document" itself was not reachable in this pass).
- Config model, dry-run/validation, commit/rollback semantics, and PATCH behavior: not documented in
  any source reachable without an active Open API session in this pass (the interactive Swagger-like
  "Online API Document" is served by the controller itself and was not reachable without a
  registered app on a live controller). unverified: everything about partial-update semantics and
  config versioning.

## Telemetry and events

Not found in the sources checked in this pass. The legacy Web API is known (from the controller's
own front end using it) to expose polling endpoints for clients, device status, and traffic
counters, but no endpoint list or schema was fetched. unverified: any webhook, streaming, or
syslog/SNMP-trap push mechanism from the controller itself; searches in this pass surfaced no such
feature. Devices independently support SNMP traps and syslog at the device level (see below), which
is a different channel from a controller push API.

## Rate limits, quotas, pagination

- Open API: 10 requests/second per controller (Pro) and per organization, fixed, not raisable on
  request, per unnamed TP-Link forum staff [15].
- Legacy Web API list endpoints use `currentPage`/`currentPageSize` query parameters (example seen:
  page size 1000) with no documented server-side cap on page size [10]. unverified: any enforced
  maximum page size or whether extremely large `currentPageSize` values are rejected.
- No backoff guidance (e.g., recommended retry-after behavior, 429 vs custom error code on
  throttling) was found for the Open API in this pass. unverified: the exact HTTP status or error
  code returned when the 10 req/s budget is exceeded.

## Device-side protocols still available

- **JetStream/Omada switches (standalone)**: CLI over SSH, Telnet, and console (where the model has
  a console port), Cisco-IOS-style modes (User EXEC, Privileged EXEC, Global Configuration,
  Interface Configuration, VLAN Configuration) with `show running-config` and `copy running-config
  startup-config` documented in per-model CLI Reference Guides [12][17]. Default credentials are
  `admin`/`admin` for both web and CLI [17]. SSH supports password or public/private key
  authentication (512–3072-bit keys via PuTTY Key Generator in the documented flow) [17]. TFTP is
  the documented mechanism for config backup/restore and firmware transfer on this switch family,
  consistent with the IOS-like CLI style, though a TFTP command example was not independently
  fetched in this pass. unverified: exact TFTP command syntax.
- **SMB models with no CLI**: the "Easy Smart" switch line (e.g., TL-SG105E/TL-SG108E and related
  unmanaged-adjacent "smart" switches) has no CLI and no SNMP; management is either the proprietary
  Easy Smart Configuration Utility (a Windows tool speaking a closed discovery protocol) or, on
  later hardware revisions (TL-SG105E V2.0+, TL-SG108E V2.0+, TL-SG108PE, TL-SG1016DE, TL-SG1024DE),
  a web management page [18]. Community sources describe the only access into V1.0 hardware as
  scraping this closed protocol or the web UI, since neither SNMP nor CLI exists [18]. Separately,
  within the JetStream naming, a switch SKU suffixed `WEB` (versus the same SKU without the suffix)
  is reported by a forum reply as the web-only variant lacking CLI and SNMP entirely [18]. This
  `WEB`-suffix claim comes from a single community forum reply, not a TP-Link spec sheet.
- **SNMP** on managed JetStream/Omada switches: TP-Link publishes a configuration guide for
  "managing the switch via SNMP" [19], implying v1/v2c/v3-class support on the managed line, but the
  exact version list and default community strings were not confirmed against a primary source in
  this pass. unverified: which SNMP versions (v1/v2c/v3) each managed model supports and whether v3
  with authPriv is available across the whole line or only newer models.
- **LLDP**: supported on the JetStream L2 Managed line, disabled by default, configurable via web UI
  (L2 Features → LLDP Config) or CLI (`lldp` command family: hold-multiplier, timer, transmit
  settings); also supports LLDP-MED [20][21].
- The controller does not appear to lock out these device-side protocols once a device is adopted;
  standalone-mode access (web UI, SSH) remains a documented, TP-Link-supported path even for
  controller-managed EAPs, per the standalone SSH note below. unverified: whether an adopted
  switch's local web UI login is disabled or changed by adoption (only EAP behavior was checked).

## Provisioning and onboarding

- **Discovery**: two topologies. Layer 2 (controller and devices on the same subnet/broadcast
  domain) and Layer 3 (separated networks, needing an explicit mechanism to reach the controller)
  [5].
  - **Discovery Utility**: a PC-based tool on the same L2 network; the operator selects a pending
    device and clicks "Manage," entering the controller's address, username, and password. Adoption
    status transitions Pending → Adopting → Configuring → Setting Succeed [5].
  - **DHCP option 138**: the DHCP server hands out the controller's IP via option 138 (or option 43
    on some setups) to devices on a different subnet than the controller [5]. unverified: that the
    DHCP client must renew its lease (e.g., replug) before it takes effect — TP-Link's own discovery
    document does not state this, and the only other source for it (a manualowl.com manual mirror)
    returned HTTP 403 on every fetch attempt in this pass; see source 22.
  - **Controller Inform URL**: manual, per-device fallback — set directly on the device's own web UI
    (System/Management/System Tools depending on device class) [5].
  - **Omada discovery/management UDP and TCP ports**: discovery on UDP 29810 in every controller
    generation checked; device management/adoption/upgrade on TCP 29811/29812/29813 — the
    v5.0.15+-scoped support document and its v4.x/v3.x predecessor both label all three ports TCP,
    not UDP, so the port-protocol conflict noted in an earlier draft of this dossier does not hold
    up: both TP-Link documents agree these three ports are TCP [3][23]. The two documents differ
    only in per-port *purpose* labeling across controller generations — the v5.0.15+ document
    groups 29811/29812 together as "device management" for v4-generation device firmware and 29813
    as v4-gen "firmware upgrade," while the older document splits them as 29811=device management,
    29812=device adoption, 29813=device upgrade — and in the newer ports added since: TCP 29814 for
    v5-generation device management, TCP 29815 added in v5.9 for device info/packet-capture/DPI
    statistics transfer, TCP 29816 added in v5.9 for remote terminal (SSH-passthrough-style)
    sessions from the controller, TCP 29817 added in v6.0 for device monitoring data. UDP 27001 is
    used for discovery by the Omada mobile app on the same network; the v4.x/v3.x document also
    lists UDP 27001 as a controller-initialization check on v3.2.4 and earlier only, and TCP
    27002/27017 as older controller-information/database ports, both superseded by TCP 27217
    (MongoDB, v3.x and above, and the Software Controller's local MongoDB port in the current
    generation) [3][23].
  - The brief also names DNS name resolution of the hostname `omada` as a discovery mechanism; no
    source fetched in this pass documents this explicitly. unverified: whether Omada devices
    attempt to resolve `omada` (or `omada.<searchdomain>`) as a controller-discovery fallback,
    analogous to Ubiquiti's `unifi` DNS convention.
  - **Zero-Touch Provisioning**: available from Controller v5.15.24 onward, using per-device "device
    keys" (imported individually or via a template of up to 1,500 devices) to pre-authorize adoption
    before the device is physically deployed, working with Omada Cloud for remote pre-configuration
    [5].
- **Standalone mode**: switches, EAPs, and gateways ship usable outside any controller; each keeps
  its own local web UI (and CLI/SSH where the model has one) for direct management [24][25].
  Adoption into a controller and standalone operation are not mutually exclusive states the device
  enforces at the protocol level — an EAP's standalone SSH toggle exists under Management → SSH on
  its own web page regardless of controller history [26].

## Known quirks and traps

- The Open API and the legacy `/api/v2` API are two separate auth stacks on the same controller;
  code written against one silently fails against the other rather than erroring cleanly on the
  wrong auth type — this is the single most common cause of confusion in the community threads
  surveyed [10][11].
- The legacy API's session cookie name changed across a controller version boundary
  (`TPEAP_SESSIONID` → `TPOMADA_SESSIONID` around v5.11) with no deprecation window documented; a
  hardcoded cookie name breaks silently across an upgrade [11]. Single-source claim, not
  cross-checked.
- Omada Open API is gated behind cloud tier ("Essentials" free tier reportedly lacks Open API
  access) even when the controller itself is self-hosted hardware, according to two independent
  community threads, though this specific gating detail was not confirmed against a current
  official pricing/feature-comparison page in this pass [6][16].
- The 10 req/s Open API rate limit is per controller/organization, not per app or per credential, so
  multiple integrations against the same controller share one budget — TP-Link support confirmed
  there is no path to raise it [15].
- TP-Link's own documentation set is fragmented across at least three properties consulted in this
  pass — `community.tp-link.com`, `support.omadanetworks.com`, and `www.tp-link.com/.../
  configuration-guides/` and `/faq/` — with overlapping and sometimes inconsistently worded copies
  of the same document (seen directly in the per-port purpose-labeling differences above, though a
  2026-09-10 verification pass found the two port documents agree on UDP/TCP protocol). Treat any
  single fetched copy as potentially stale relative to another mirror of the same page.
- Within the switch line, CLI/SNMP availability is a per-model, sometimes per-hardware-revision
  property (Easy Smart V1.0 vs V2.0+; the `WEB`-suffixed JetStream SKUs), not a property of the
  product family name alone — model number and hardware version must both be checked before
  assuming CLI or SNMP reachability [18].

## What FlowSeer needs

- **Credentials to store**: for Open API access, `omadacId`, Client ID, Client Secret (and, for
  Authorization Code mode, the issued refresh token); rotate the secret is shown once at creation,
  so it must be captured and stored at registration time, not re-derivable later [6][7].
- **Endpoints to call**:
  - Token: `POST /openapi/authorize/token?grant_type=client_credentials` (or the authorization-code
    equivalent) [1].
  - Inventory read: `GET /openapi/v1/{omadacId}/sites` and per-site device/client listings under
    `/openapi/v1/{omadacId}/sites/{siteId}/...`, following the same path shape as the confirmed
    hotspot-client and site-creation endpoints [1].
  - Fall back to the legacy `/{omadacId}/api/v2/...` surface only where the Open API does not yet
    cover a needed capability (e.g., historical community use for client and device detail before
    the Open API existed), accepting that surface's lack of any stability contract [9][10].
- **Identifiers to key on**: `omadacId` at the controller level, `siteId` at the site level; device-
  level identifier is MAC address per a community Open API client's resource-path convention
  (`{type}/{mac}`) [7], not independently confirmed against TP-Link's own primary reference (see
  Data model) — verify against a live controller's Open API device-list response before building an
  inventory key.
- **Rate-limit budget**: design for a shared 10 requests/second ceiling per controller/organization,
  across all FlowSeer components hitting the same controller — this argues for a single rate-limited
  client per controller rather than one per poller [15].
- **Minimum API version to target**: Open API v1 (the only version that exists) for any new
  integration work; treat the legacy Web API as a documented-quirks fallback only, given its
  undocumented, per-release-changing nature [8][9].
- **Device-side fallback**: for switch and EAP models confirmed to carry the full JetStream CLI, an
  SSH-based path (`show running-config`, TFTP backup) is available independent of the controller and
  independent of Open API rate limits, which matters for bulk config backup where the controller
  API path is unverified for that use case [12][17].

## Sources

1. [How to Create Site in Omada Controller via Open API — Omada Network Support](https://support.omadanetworks.com/us/document/109315/) — fetched 2026-09-10. Open API create-site request/response, `omadacId`/Interface Access Address concept, auth mode descriptions, `AccessToken=` header prefix.
2. [Omada SDN Controller API Document — TP-Link Community](https://community.tp-link.com/en/business/forum/topic/590430) — fetched 2026-09-10 (verification pass). Lists per-version legacy Web API PDFs and the live Open API online reference; uses TP-Link's own terms "Web API" (changes across controller versions) vs. "Open API"; states Open API is supported on all Omada Pro versions and controllers v5.12+ (Cloud-Based Controllers excepted as of December 2023).
3. [Which ports do Omada SDN Controller and Omada Discovery Utility use? (above Controller 5.0.15) — Omada Network Support](https://support.omadanetworks.com/us/document/13090/) — fetched 2026-09-10. Web-management ports (80/443 hardware, 8088/8043/8843 software) and device discovery/management/upgrade port table for v5.0.15+.
4. [Download for Omada Software Controller V5 — TP-Link](https://www.tp-link.com/us/support/download/omada-software-controller/) — search result only; OC200/OC300 hardware controller confirmation.
5. [How to Discover Omada Devices Using the Software Controller and Hardware Controller — Omada Network Support](https://support.omadanetworks.com/ae/document/13223/) — fetched 2026-09-10. L2/L3 discovery framing, Discovery Utility flow, DHCP option 138, Inform URL, ZTP (v5.15.24+, device keys, 1,500-device template).
6. [How to Configure OpenAPI via Omada Controller — TP-Link Business Community KB](https://community.tp-link.com/en/business/kb/detail/412930) — fetched 2026-09-10. App registration steps (Settings → Platform Integration → Open API), mode selection, "Essential version doesn't support OpenAPI" community reply.
7. GitHub — [MarkGodwin/tplink-omada-api](https://github.com/MarkGodwin/tplink-omada-api) — fetched 2026-09-10, including `src/tplink_omada_client/devices.py` (verification pass, 2026-09-10). OAuth2 client-credentials with automatic token refresh, Fusion Gateway using local credentials instead, `omadacId` path pattern, supported controller list (OC300, OC200, software, cloud, Fusion Gateway), cloud Essentials-tier gating claim. `devices.py` confirms the `OmadaDevice` model keys on `self._data["mac"]` and builds each device's resource path as `{eaps|gateways|switches}/{mac}` — no `deviceId` field found anywhere in the client source.
8. [Omada Open API — TP-Link Business Community forum topic 840728](https://community.tp-link.com/en/business/forum/topic/840728) — fetched 2026-09-10. `-44112` expired-access-token error text, `Bearer AccessToken=` header format correction. A verification pass on 2026-09-10 re-fetched this page and did not find "Open API currently only has the V1 version" or any Web-API-vs-Open-API distinction on it; those claims now cite source 2 instead and the V1-only wording is marked unverified in the text above.
9. GitHub — [ghaberek/omada-api](https://github.com/ghaberek/omada-api) — fetched 2026-09-10 (README). Links to per-release legacy Web API PDFs from V2.6.0 through V5.9.9, confirming no single stable legacy spec.
10. [Example API Calls Using Powershell and Bash/curl for Omada Controller — gist by mbentley](https://gist.github.com/mbentley/03c198077c81d52cb029b825e9a6dc18) — fetched 2026-09-10. Legacy login endpoint and body, `Csrf-Token` header, site/device/user list endpoints with `currentPage`/`currentPageSize` pagination, "last validated on 5.12.7."
11. mcp-omada project — cited via [Omada legacy API search summary](https://glama.ai/mcp/servers/thalisantunes/mcp-omada) (not independently fetched; claim taken from aggregated search-result text) — search result date 2026-09-10. `TPEAP_SESSIONID` vs `TPOMADA_SESSIONID` cookie-name split around controller v5.11, legacy-vs-Open-API session incompatibility. Single-source, not cross-checked against a second document.
12. [Typical CLI Configuration Examples for TP-Link JetStream Switch — Omada Network Support](https://support.omadanetworks.com/baltic/document/13117/) — search result only, not deep-fetched; corroborates CLI mode structure alongside source 17.
13. [Typical SSH commands for troubleshooting Omada APs with Controller v6 and earlier versions — Omada Network Support](https://support.omadanetworks.com/us/document/13236/) — search result only, not deep-fetched; referenced for existence of an EAP SSH command guide.
14. Same as source 8 (forum topic 840728) — error code and header-format detail.
15. [Omada Open API Limit — TP-Link Business Community forum topic 785332](https://community.tp-link.com/en/business/forum/topic/785332) — fetched 2026-09-10. "10 requests per second" per controller/organization, no increase available, concurrency-limit and global-before-site-config guidance from TP-Link staff.
16. GitHub — [bullitt186/ha-omada-open-api](https://github.com/bullitt186/ha-omada-open-api) — fetched 2026-09-10. OAuth2 client-credentials with automatic refresh for cloud and local controllers, Fusion Gateway local-credential exception, supported hardware list (OC300, OC200 firmware 6.2.10.18 confirmed, software controller, Fusion Gateway), "Free cloud accounts won't work" / Standard-tier-or-higher claim for cloud mode.
17. [Accessing the Switch — TP-Link Configuration Guides](https://www.tp-link.com/us/configuration-guides/accessing_the_switch/?configurationId=18231) — fetched 2026-09-10. Console/Telnet/SSH access for T1500G/T1600G/T1700G/T2600G, default `admin`/`admin` credentials, SSH password vs. key auth (512–3072-bit keys via PuTTY Key Generator).
18. Aggregated from search results for "Easy Smart switch web-only no CLI SNMP" (GitHub psmode/essstat, TP-Link Business Community forum topic 75407, LibreNMS community thread) — search performed 2026-09-10, not independently deep-fetched. No-SNMP/no-CLI claim for Easy Smart line, V1.0-vs-V2.0+ hardware-revision management-path split, single-reply claim about `WEB`-suffixed JetStream SKUs lacking CLI/SNMP.
19. [Key Points of Managing the Switch via SNMP — TP-Link Configuration Guides](https://www.tp-link.com/us/configuration-guides/key_points_of_managing_the_switch_via_snmp/?configurationId=20745) — search result only, not deep-fetched; cited for existence of an SNMP management guide on the managed switch line.
20. [Configuring LLDP — TP-Link Configuration Guides](https://www.tp-link.com/us/configuration-guides/configuring_lldp/?configurationId=18043) — search result only, not deep-fetched; corroborates LLDP/LLDP-MED support and default-disabled state.
21. [TL-SG3210/TL-SG3216/TL-SG3424/TL-SG3424P JetStream L2 Managed Switch CLI Reference Guide — TP-Link](https://static.tp-link.com/res/down/doc/TL-SG3216(UN)_V2.0_CLI.pdf) — search result only, not deep-fetched; cited for `lldp` CLI command family (hold-multiplier, timer, transmit).
22. DEAD LINK, flagged in a 2026-09-10 verification pass: manualowl.com mirror of the Omada Software Controller manual (previously `https://www.manualowl.com/m/TP-Link/Omada-Software-Controller/Manual/531550?page=40`), cited for the DHCP-lease-renewal requirement for option 138. The domain returned HTTP 403 on every fetch attempt in this pass (including with a browser user agent), so the URL is removed here; no replacement source was found, and the claim is marked unverified in the text above.
23. Same as source 3 (support.omadanetworks.com/us/document/13090/), cross-checked against [Which ports do Omada Controller and Omada Discovery Utility use? (v4.x/v3.x) — Omada Network Support](https://support.omadanetworks.com/us/document/13087/) (search result only, not deep-fetched) for the older-controller port range; the two documents disagree on UDP/TCP labeling for ports 29811–29813, noted as an open discrepancy in Known quirks.
24. [How to Setup Omada EAP in Standalone Mode — Omada Network Support](https://support.omadanetworks.com/us/document/112424/) — search result only, not deep-fetched; cited for standalone-mode existence and per-device web management.
25. [Methods for Managing the Omada EAPs Network — TP-Link Configuration Guides](https://www.tp-link.com/us/configuration-guides/methods_for_managing_the_omada_eaps_network/?configurationId=21103) — search result only, not deep-fetched; cited for Omada-app/web-browser management in standalone mode.
26. Aggregated from search results for "Omada EAP standalone mode SSH access HTTP API" (Business Community forum topic 827828, and the two sources above) — search performed 2026-09-10, not independently deep-fetched. Management → SSH toggle on a standalone EAP's own web page; portal-auth POST target `http://<ap-ip>/portal/auth`.
