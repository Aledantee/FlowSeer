---
title: Integration target — Ubiquiti UniFi
date: 2026-09-10
scope: UniFi Network Application (self-hosted controller, UniFi OS gateways/UDM/UCG, CloudKey), UniFi Site Manager cloud API; UISP/EdgeSwitch/EdgeRouter covered briefly as a separate product line
status: research from public documentation; nothing verified against a live system unless stated
---

# Ubiquiti UniFi

Every claim carries a source link. Where documentation is contradictory or version-dependent,
say which version the claim holds for. The repo already vendors both official OpenAPI specs at
`spec/openapi/ubiquiti/` (see `SOURCES.md` there) and a MIB/API dossier at
`docs/research/network-domain-atlas/vendors/ubiquiti.md`; this document references rather than
repeats what those already cover, and adds what they don't: auth mechanics, adoption, device-side
access, SNMP/webhook configuration, rate limits, and the legacy controller API.

## What it manages

UniFi is Ubiquiti's SDN-style controller product for switches (USW), access points (UAP), and
gateways (UDM/UCG/UXG). One controller ("UniFi Network application") manages one or more **sites**,
each with its own devices, clients, and network config. Two deployment shapes exist:

- **Self-hosted controller**: the Network application running as a Java process (Linux/Windows/
  Docker), talking to devices over the LAN or over site-to-site L3 adoption.
- **UniFi OS console**: purpose-built hardware (Dream Machine / Dream Router, Cloud Gateway Ultra,
  UCG-Max, UDW, CloudKey Gen2+) running UniFi OS, with the Network application as one of several
  apps on top (alongside Protect, Access, Talk, InnerSpace) [1].

CloudKey Gen1 and the classic software controller are standalone: no UniFi OS layer, direct
`/api/...` paths. UniFi OS consoles wrap the same application behind `/proxy/network/...` [2][3].

Ubiquiti also runs a cloud identity/relay layer at `unifi.ui.com` / `api.ui.com` ("Site Manager")
that aggregates multiple consoles under one UI account and can proxy local API calls to consoles
that are not directly reachable [2].

Scale: no official per-controller device/client limits are published in the sources checked; scale
guidance is qualitative in Ubiquiti's own marketing, not in the API docs. unverified: any specific
device-count ceiling.

## API surface

| Surface | Protocol / transport | Spec available? | Versioning scheme |
| --- | --- | --- | --- |
| UniFi Network API (local, official) | HTTPS + JSON, `X-API-Key` | OpenAPI 3.1, vendored at `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json` (73 operations / 44 paths) [4] | Tied to Network application release; spec version = app version (`10.4.57` is the newest published spec as of 2026-09-08, app itself has since shipped past that — see Known quirks) [4][5] |
| UniFi Network API via Connector Proxy (cloud-relayed) | HTTPS + JSON, `X-API-Key`, routed through `api.ui.com` | Same OpenAPI document as above; the console does not need a reachable local IP | Requires console firmware ≥ 5.0.3 for the proxy feature [2] |
| UniFi Site Manager API (cloud) | HTTPS + JSON, `X-API-Key` | OpenAPI 3.0.3, vendored at `spec/openapi/ubiquiti/unifi-site-manager-openapi-v1.0.0.json` (9 operations / 9 paths) [6] | `v1.0.0`, single published version as of 2026-09-08 [6] |
| Classic/legacy controller API | HTTPS + JSON, cookie session | Not published anywhere by Ubiquiti; reverse-engineered by the community (e.g. `ubntwiki.com`, `Art-of-WiFi/UniFi-API-client`) [3][7] | No version contract; endpoints have moved and been removed across major app releases without notice |
| SNMP (device + controller-relayed) | UDP, v1/v2c/v3 | `UBNT-MIB`, `UBNT-UniFi-MIB` vendored at `spec/mib/ubiquiti/` (see atlas doc) | Tied to firmware; controller exposes only a small MIB, mostly Linux host resources |
| EdgeOS REST API (EdgeRouter, separate product) | HTTPS + JSON, session cookie | Not published; reverse-engineered [8] | No contract |
| EdgeSwitch (FASTPATH-derived CLI + web) | SSH/CLI + HTTPS | MIBs vendored at `spec/mib/ubiquiti/edgemax/` (see atlas doc); CLI undocumented in machine-readable form | Firmware-tied |

A UniFi OS console (UDM/UCG-class gateway) serves the Network Integration API at
`https://<console>/proxy/network/integration/v1` — confirmed directly in [2] and [9]'s
reference doc. unverified: a standalone software controller (no UniFi OS layer) serving the
same Integration API at `https://<controller>:8443/integration/v1`. Re-checked against [2],
[3], and [9] directly: all three give port 8443 and `/api/...` for the *classic/legacy* API on
a standalone controller, but none states an Integration API base path for that deployment shape
at all — [9]'s reference doc documents only the UniFi-OS-console and Connector-Proxy base URLs.
The `:8443/integration/v1` path is a plausible extrapolation from the classic-API port
convention, not a confirmed fact; treat it as unverified pending a fetch against an actual
standalone controller. The spec itself is host-relative (`servers: [{url: "/integration"}]` in
the vendored JSON), so the caller supplies whichever prefix matches the deployment.

## Authentication and authorisation

Four distinct credential mechanisms exist; they are not interchangeable and cover different
reachability cases [2][7]:

| Method | Where created | Reaches | Notes |
| --- | --- | --- | --- |
| Network Integration API key | UniFi OS console: Settings → Control Plane → Integrations; or generated via `unifi.ui.com` | Local network path to the console (or via Connector Proxy) | `X-API-Key` header, stateless, no login/logout. Key inherits the creating admin's permission level — there is no separate scope/role model for the key itself [7][9] |
| Site Manager API key | `unifi.ui.com` → account profile → API Keys | `api.ui.com` cloud only | `X-API-Key` header (confirmed directly in the vendored spec's `securitySchemes`, and no `scope`/permission field is defined anywhere in it). unverified: "the API key is currently read-only" — [10]'s Packagist page was re-checked directly in this pass and does not state this or anything about key scopes at all, so the claim traces to search-result synthesis, not a confirmed statement on that page; the vendored OpenAPI spec's operations are all `GET`/`POST` (`POST` only for ISP-metrics *query*, which is a read), consistent with a read-only surface, but Ubiquiti's own docs page could not be fetched (Cloudflare-gated) to confirm explicitly |
| Classic cookie session (standalone) | `POST /api/login` with `{username, password}` | Direct network path to the controller, port 8443 | No CSRF token required on standalone controllers [3][7] |
| Classic cookie session (UniFi OS) | `POST /api/auth/login` (note different path) at the console, then `/proxy/network/api/...` for all data calls | Direct network path to the console, port 443 | Response carries an `X-CSRF-Token` header (also set as a `TOKEN` cookie); every subsequent state-changing `POST`/`PUT`/`DELETE` must echo it back in the `X-Csrf-Token` request header or the UniFi OS nginx layer returns 403 before the request reaches the app [3][7][11] |

2FA: if the admin account has 2FA enabled, the first classic login attempt returns HTTP 499 with
`meta.msg = "api.err.Ubic2faTokenRequired"`; resubmitting the same POST with a `token` field
completes login [7].

Least-privilege read-only integration: an official Network Integration API key created by a
read-only local admin account, used directly against the console (no cloud relay needed for a
LAN-reachable target). Write integration (adopt, port actions, firewall/ACL changes): the same
key mechanism but created by an admin with the relevant write permissions — there is no
per-key scope restriction independent of the creating admin's role [7][9].

## Data model

Hierarchy: UI.com account (cloud identity) → Host/console → Site → Device / Client / Network
(VLAN) / WLAN. A single UniFi OS console can host multiple sites; Site Manager aggregates devices
and sites across every console tied to one UI.com account [6].

Identifiers: device `id` (controller-assigned, changes if the device is removed and re-adopted)
and `mac` (stable hardware identifier, format varies by endpoint — lowercase without separators
in most classic-API payloads, colon-separated in newer Integration API responses) [7][12].
Sites use a UUID `siteId` in the Integration API and a short string ID (often `default`) in the
classic API [4][7].

Inventory read (Integration API, local): `GET /v1/sites`, `GET /v1/sites/{siteId}/devices`,
`GET /v1/sites/{siteId}/devices/{deviceId}`, `GET /v1/sites/{siteId}/clients` — see the atlas doc
for the fuller endpoint map. Pagination on list endpoints uses `offset`/`limit` query parameters,
`limit` capped at 200, default 25 [4] (read directly from the vendored OpenAPI schema for
`GET /v1/sites/{siteId}/devices`).

Inventory read (Site Manager, cloud): `GET /v1/hosts`, `GET /v1/devices`, `GET /v1/sites`.
Pagination uses `pageSize` (string-typed in the spec) plus an opaque `nextToken` cursor, not
offset-based [6]. `GET /v1/devices` also accepts `hostIds[]` (array filter) and `time` (RFC 3339
timestamp, "last processed timestamp of devices") for incremental polling [6].

Configuration model: the Integration API is declarative REST over specific resource types —
`networks`, `wifi/broadcasts` (WLANs), `firewall/policies`, `firewall/zones`, `acl-rules`,
`dns/policies`, `traffic-matching-lists` — each with standard `GET`/`POST`/`PUT`/`DELETE`, plus a
`PATCH` on firewall policies specifically [4][9]. Two resource types expose ordering as a
separate sub-resource: `GET`/`PUT /v1/sites/{siteId}/acl-rules/ordering` and the equivalent under
`firewall/policies/ordering` — rule evaluation order is a mutable property distinct from rule
content [4][9]. The classic API's `/rest/*` endpoints (`networkconf`, `wlanconf`, `firewallrule`,
`portconf` for port profiles) cover the same ground but with no `ordering` sub-resource; order is
implied by position in the returned array and mutated by rewriting it (unverified beyond what
the community reference states) [7].

Port profiles and WLAN groups in the classic model are shared config objects referenced by
`_id` from device port overrides and site WLAN definitions respectively — not vendored/confirmed
in the Integration API spec's schema in the depth this research covered; treat as an area to
verify against the OpenAPI component schemas directly before relying on it.

No documented dry-run/validation endpoint, commit/rollback, or explicit config-versioning
mechanism was found for either API generation in the sources checked. unverified: read-after-write
consistency and device-apply propagation delay — not stated in any fetched source.

## Telemetry and events

- Polling: `GET /v1/sites/{siteId}/devices/{deviceId}/statistics/latest` (Integration API) [4];
  classic API `stat/device`, `stat/sta` (active clients), `stat/report/{5minutes,hourly,daily}.site`
  for historical rollups, `stat/dpi`/`stat/stadpi` for deep packet inspection [7].
- Push/webhook: **UniFi Alarm Manager**, configured in the Network application UI, triggers on
  device status, WAN, client join/leave, IDS/IPS detections, honeypot hits, firewall blocks, and
  PoE loss; one action type is a custom webhook (`Action → Webhook → Custom Webhook → POST`) to
  an arbitrary URL [13]. No official payload schema or retry policy for this webhook was found in
  the sources checked; unverified.
- Real-time feed: a WebSocket event stream exists on both deployment shapes —
  `wss://<controller>:8443/wss/s/{site}/events` (standalone) and
  `wss://<console>/proxy/network/wss/s/{site}/events` (UniFi OS), authenticated with the session
  cookie [7]. This is undocumented/community-derived, not part of either official OpenAPI spec.
- SNMP traps: sources disagree. A community setup guide describes a "trap server IP" field and
  trap notifications as available [14]; a separate community Q&A states traps are "not currently
  available" pending a future release [15]. Neither is a primary vendor doc (the official
  `help.ui.com` SNMP article returned HTTP 403/Cloudflare-challenge to automated fetch and could
  not be verified directly). Treat trap availability as version-dependent and unverified pending a
  fetch of the vendor page.

## Rate limits, quotas, pagination

unverified as a directly-fetched vendor-stated number. Correction: `developer.ui.com` (both APIs'
official doc host) does not return HTTP 403 — re-checked directly in this pass, it returns HTTP
200 but is a JS-rendered SPA, so WebFetch/curl retrieve only the page's nav shell ("Getting
Started · Site Manager · UniFi API") with no body text; `help.ui.com` is the one that is actually
Cloudflare-403-gated. A community developer reference states: Site Manager API enforces 10,000
requests/minute at GA, returning HTTP 429 with a `Retry-After` header on overflow; the same source
states the local Network Integration API and Connector Proxy are also "official" without quoting a
separate number for them [7]. A search-engine snippet of `developer.ui.com`'s own "Getting
Started · Site Manager" page (not independently fetched in full, same SPA limitation as above)
corroborates this: Early Access was 100 req/min, and the current v1 stable release is 10,000
req/min with HTTP 429 — consistent with [7], but still not a directly-fetched primary confirmation.
The classic/legacy API has no documented quota in any source checked; expect only session-expiry
(`401`/`api.err.LoginRequired`) as a control [7].

Pagination: Integration API list endpoints use `offset`/`limit` (`limit` max 200, default 25),
read from the vendored OpenAPI schema [4]. Site Manager list endpoints use `pageSize` (string) +
`nextToken` cursor [6]. Classic API list endpoints use `_limit`/`_start`/`_sort` query params,
per community reference, not part of any official spec [7].

## Device-side protocols still available

- **SSH**: enabled on adopted and pre-adoption UniFi devices. Factory-default credentials are
  `ubnt`/`ubnt` [16]; once adopted, the controller pushes its own configured SSH credentials
  (System → Application Configuration → Device SSH Authentication) [16]. The device-side shell
  exposes an `info` command that prints current IP, firmware version, MAC, and inform-URL/
  adoption status [16], and a separate `mca-cli` (management-agent CLI) entry point — real and
  attested across multiple community sources, but [16] does not mention it; re-cited to [21],
  which lists `mca-cli` only via a reader comment with no functional detail, so treat exactly
  what `mca-cli` exposes beyond `set-inform` as unverified. The MIB/API dossier
  (`docs/research/network-domain-atlas/vendors/ubiquiti.md`) notes UniFi's SNMP surface is
  "trivial" and the API is the real interface — SSH remains the fallback for adoption recovery and
  low-level diagnostics, not routine monitoring.
- **SNMP**: controller-side SNMP (site-wide, not per-device) is configured in the Network
  application's system settings; a community walkthrough describes enabling it with a community
  string, port (default 161), and, for v3, a username/password pair — the article states UniFi
  "simplifies" v3 configuration and does not expose the full set of standard v3 knobs (no per-user
  engine ID, limited auth/priv algorithm choice) [14][17]. Not vendor-confirmed (help.ui.com
  blocked automated fetch); treat specifics as community-sourced.
- **NETCONF/RESTCONF/gNMI**: none. Not offered on any UniFi product line in any source checked.

## Provisioning and onboarding

- **L2 (same broadcast domain) adoption**: unmanaged devices self-announce and appear as "Pending
  Adoption" in the controller UI; no SSH interaction needed [18][19].
- **L3 (cross-subnet / remote controller) adoption**: SSH to the device with default `ubnt`/`ubnt`
  credentials and run `set-inform http://<controller-ip>:8080/inform`; if that does not take
  (common after factory reset), unverified: issue the same `set-inform` from inside `mca-cli`
  rather than the plain shell — the fallback behavior itself is not stated by [16] or [18]; only
  the existence of an `mca-cli` entry point is attested (see note above, [21]).
- **DHCP option 43 / DNS discovery**: devices query DHCP option 43 (vendor-specific, encoded
  per-DHCP-server — one community example for pfSense: `01:04:0A:00:00:0A`) or resolve the
  hostname `unifi` via DNS to find a controller automatically; a community source notes pointing
  directly at the controller IP is more reliable in practice than relying on the `unifi` DNS name
  [16].
- **`/v1/pending-devices`** (Integration API): the adoption queue exposed as a first-class REST
  resource — a device that exists but is not yet managed is a distinct, queryable state [4] (see
  atlas doc for the design note on this).
- Firmware management: not covered by the sources checked in this pass beyond the classic API's
  `cmd/devmgr` `upgrade` action [7]; no OTA/staged-rollout API surface was found documented.

## Known quirks and traps

- **API spec version lags the shipped application version.** `spec/openapi/ubiquiti/SOURCES.md`
  already notes this: `10.4.57` is the newest *published spec* as of the last vendoring pass, while
  the Network *application* had already shipped past it. The catalog mirror confirms `10.4.57` is
  still the newest published spec as of 2026-09-08 [5] — the spec release cadence trails the app
  release cadence, and an integration should not assume spec version == running app version.
- **UniFi OS vs Network Application are two independently versioned layers**, a distinction that
  postdates the pre-2021 product naming. Post-rebrand, a console runs "UniFi OS 3.x" as the base
  layer and "UniFi Network 7.x/8.x/..." as an app on top — [1] confirms the independent-versioning
  framing and the 2021 rebrand directly (e.g. "UniFi OS 3.0" alongside "Network 7.3"), but does not
  mention any 3.1.6 compatibility detail. unverified: a compatibility constraint tying UDM/UDR/
  Express app-version support to UniFi OS 3.1.6 specifically — that detail comes only from [20],
  which is unfetched search-result synthesis; re-cited to [20] alone, not [1]. Track both numbers,
  not one.
- **Standalone vs UniFi OS path and auth differ silently.** Same classic API, but standalone uses
  `/api/login` + no CSRF, UniFi OS uses `/api/auth/login` + mandatory `X-CSRF-Token`; an integration
  written against one silently 403s against the other if it skips the CSRF header [3][7][11].
- **The classic API has no contract.** It is not published by Ubiquiti anywhere; every field,
  endpoint, and `cmd` payload in the tables above comes from community reverse-engineering
  (`ubntwiki.com`, `Art-of-WiFi/UniFi-API-client`, `uchkunr/unifi-best-practices`) and can change
  between app releases without changelog notice [3][7].
- **Site Manager API key is read-only** — unverified: [10] was re-checked directly in this pass
  and its Packagist page does not actually state this, so the origin is weaker than a single
  community source; treat as an educated guess consistent with the spec's all-`GET`/read-`POST`
  surface, not a confirmed fact. Not confirmed against a primary Ubiquiti page in this pass
  (Cloudflare blocked automated fetch of the official docs).
  Do not assume write access from Site Manager without checking current vendor docs directly.
- **SNMP traps: conflicting community claims** on availability (see Telemetry and events above).
- **help.ui.com and developer.ui.com are hard to fetch programmatically.** developer.ui.com is a
  JS-rendered SPA (already noted in `spec/openapi/ubiquiti/SOURCES.md`); help.ui.com is behind a
  Cloudflare interactive challenge that blocked every automated fetch attempt in this research
  pass. The `opastorello/unifi-api-docs` GitHub mirror (CI-updated daily, per its own README) is
  the practical way to pull current OpenAPI JSON and a human-readable endpoint index without a
  browser.

## What FlowSeer needs

- **Credentials to store per integration target**: one Network Integration API key per console
  (local reachability) plus, optionally, one Site Manager API key per UI.com account (cloud
  reachability / multi-console rollup / Connector Proxy fallback for consoles behind CGNAT) [2][7].
- **Inventory endpoints**: `GET /v1/sites` then `GET /v1/sites/{siteId}/devices` and
  `GET /v1/sites/{siteId}/clients` against the local Integration API for per-console detail;
  `GET /v1/hosts` and `GET /v1/devices` against Site Manager for a cross-console rollup keyed by
  `hostId` [4][6].
- **Config endpoints**: `networks`, `wifi/broadcasts`, `firewall/policies` (+`/ordering`),
  `firewall/zones`, `acl-rules` (+`/ordering`), `dns/policies`, `traffic-matching-lists` under
  `/v1/sites/{siteId}/...` — all in the vendored OpenAPI spec, no legacy-API fallback should be
  needed for these [4].
- **Telemetry**: poll `.../statistics/latest` per device; for push, plan around Alarm Manager
  webhooks once their payload shape is confirmed against a live console (not found documented) —
  do not build a parser against assumed fields.
- **Identifiers to key on**: `mac` for cross-API device identity (stable); `id`/`deviceId` is
  controller-local and not stable across re-adoption [7][12].
- **Rate-limit budget**: plan for 10,000 req/min as a working assumption for the cloud API
  (community-sourced, not vendor-confirmed — re-verify against `help.ui.com` once fetchable) [7];
  no local-API number found anywhere, so throttle conservatively and honor `429`/`Retry-After`.
  See feedback_check_provider_quota-style discipline: verify before relying on either number in
  production.
- **Minimum API version to target**: Network Integration API `10.4.57` (currently vendored, still
  latest published as of 2026-09-08) [4][5]; Site Manager API `1.0.0` (only published version) [6].
- **EdgeSwitch/EdgeRouter**: out of scope for the UniFi Network API entirely — a separate product
  line with no official REST API (EdgeOS) and a Broadcom-FASTPATH-derived CLI/MIB set (EdgeSwitch),
  already covered in `docs/research/network-domain-atlas/vendors/ubiquiti.md` and
  `spec/mib/ubiquiti/edgemax/`. If FlowSeer needs to manage these, budget for reverse-engineered
  session-cookie REST (EdgeOS, port 443, `PHPSESSID`, 15-minute session refreshed by a heartbeat
  call) [8] and SSH/FASTPATH CLI (EdgeSwitch) rather than any of the UniFi APIs above.

## Sources

1. https://evanmccann.net/blog/2023/4/catching-up-with-ubiquiti — fetched 2026-09-10; UniFi OS vs
   Network Application history, the 2021 rebrand, independent OS/app version numbering.
2. https://raw.githubusercontent.com/uchkunr/unifi-best-practices/master/README.md — fetched
   2026-09-10 (community developer reference, `master` branch, badge shows `unifi-api v10.1.68` at
   time of writing though body text covers the current API set); Connector Proxy, API key
   generation locations, base URL table, rate-limit claim.
3. https://ubntwiki.com/products/software/unifi-controller/api — fetched 2026-09-10 (community
   wiki); classic API login endpoints, standalone vs UniFi OS path/prefix differences.
4. `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json` — inspected directly in-repo
   2026-09-10; OpenAPI 3.1 document, `servers: [{url: "/integration"}]`, pagination parameters on
   `GET /v1/sites/{siteId}/devices` (`offset` default 0, `limit` default 25 max 200), full path
   list.
5. https://raw.githubusercontent.com/opastorello/unifi-api-docs/main/catalog.json — fetched
   2026-09-10; confirms `network` app latest published spec is still `v10.4.57` as of source
   `lastmod` `2026-09-08T11:51:55.543Z`; also lists `site-manager` latest `v1.0.0`, plus
   `protect`, `innerspace`, `carrier-fabric`, `mobility` app catalogs (out of scope here).
6. `spec/openapi/ubiquiti/unifi-site-manager-openapi-v1.0.0.json` and
   https://raw.githubusercontent.com/opastorello/unifi-api-docs/main/site-manager/v1.0.0/reference.md
   — inspected/fetched 2026-09-10; `X-API-Key` security scheme, `pageSize`/`nextToken` pagination,
   `hostIds[]`/`time` filters on `GET /v1/devices`, server `https://api.ui.com`.
7. https://raw.githubusercontent.com/uchkunr/unifi-best-practices/master/README.md — same source
   as [2], additionally used for: classic API endpoint tables (`stat/device`, `stat/sta`,
   `rest/*`, `cmd/*` managers), CSRF/MFA behavior, classic-API pagination params (`_limit`/
   `_start`/`_sort`), rate-limit and error-handling section, WebSocket event feed URLs and auth.
8. https://ubntwiki.com/products/software/edgeos/api — fetched 2026-09-10 (community wiki); EdgeOS
   REST API base path, `PHPSESSID` session auth, 15-minute session with heartbeat refresh, endpoint
   list (`get.json`, `getcfg.json`, `data.json`, `batch.json`, `config/save.json`).
9. `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json` plus
   https://raw.githubusercontent.com/opastorello/unifi-api-docs/main/network/v10.4.57/reference.md
   — fetched/inspected 2026-09-10; confirms `/ordering` sub-resources on `acl-rules` and
   `firewall/policies`, `PATCH` on firewall policies, `/v1/pending-devices` endpoint.
10. Community search-result synthesis citing `art-of-wifi/unifi-api-client` documentation on
    Packagist (https://packagist.org/packages/art-of-wifi/unifi-api-client) — surfaced via search,
    not independently fetched and confirmed in this pass; flagged as unverified in the text above.
11. https://raw.githubusercontent.com/uchkunr/unifi-best-practices/master/README.md — CSRF token
    extraction/replay example (`X-CSRF-Token` from login response header, echoed on state-changing
    calls).
12. `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json` device/client schema fields
    (`mac`, `id`) — inspected in-repo 2026-09-10.
13. https://github.com/PHeonix25/unifi_alerts/blob/main/docs/ALARM_MANAGER_SETUP.md — fetched
    2026-09-10 (community guide, Home-Assistant-focused); Alarm Manager trigger categories and
    webhook action configuration steps.
14. https://www.unihosted.com/blog/unifi-snmp-setup-and-monitoring — fetched 2026-09-10 (managed
    hosting provider's blog, not primary vendor doc); SNMP v2c/v3 mention, community string/port/
    trap-server fields, states traps are supported.
15. Search-result synthesis of a community Q&A thread at community.ui.com (JS-rendered, not
    independently fetched — `community.ui.com` pages returned only a client-side loading shell to
    every WebFetch attempt in this pass); claims traps are "not currently available." Recorded
    because it directly conflicts with [14]; neither is vendor-primary-confirmed.
16. https://lazyadmin.nl/home-network/unifi-set-inform/ — fetched 2026-09-10 (community how-to);
    `set-inform` syntax, default `ubnt`/`ubnt` SSH credentials, `info` command, DHCP option 43
    example, DNS name `unifi` fallback discovery. Re-checked directly in this pass: this page does
    *not* mention `mca-cli` at all (corrected from an earlier version of this dossier that
    attributed it here); see [21] for that.
17. `help.ui.com` SNMP Monitoring article (https://help.ui.com/hc/en-us/articles/33502980942615) —
    attempted fetch 2026-09-10, blocked by a Cloudflare interactive challenge (both `WebFetch` and
    direct `curl` with a browser user agent). Not used as a source; SNMP claims above are sourced
    to [14] and the atlas/MIB corpus instead and marked accordingly.
18. Search-result synthesis on L2 vs L3 adoption terminology (LazyAdmin and related community
    how-to pages, same family as [16]); L2 self-discovery vs L3 SSH-based adoption distinction.
19. Same family of sources as [16]/[18]; "Pending Adoption" UI state description.
20. Search-result synthesis referencing UniFi Network Application release notes on
    community.ui.com (JS-rendered, not independently fetched); UniFi OS 3.1.6 compatibility
    claim for UDM/UDR/Express app versioning — recorded as reported, not independently confirmed.
21. https://lazyadmin.nl/home-network/unifi-ssh-commands/ — fetched 2026-09-10 (community
    how-to); confirms `info` displays device information. `mca-cli` appears only in a reader
    comment on this page ("'mca-cli' are... missing from the list") with no functional
    description; existence of `mca-cli` as an SSH entry point is additionally attested by
    community.ui.com thread titles surfaced in search ("What is mca-cli (SSH)") but those pages
    were not independently fetched (JS-rendered).

Already vendored in-repo, referenced rather than repeated: `spec/openapi/ubiquiti/SOURCES.md`
(spec provenance and refresh procedure), `docs/research/network-domain-atlas/vendors/ubiquiti.md`
(SNMP MIB inventory for all three Ubiquiti product lines, full endpoint path listing for the
Network Integration API, EdgeSwitch FASTPATH MIB mapping).
