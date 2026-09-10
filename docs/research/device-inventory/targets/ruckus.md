---
title: Integration target — Ruckus (CommScope) management surfaces
date: 2026-09-10
scope: SmartZone/vSZ REST API (v9_x-v13_x), Ruckus One cloud API, Unleashed
  controller-less mode, ICX FastIron standalone switches
status: research from public documentation; nothing verified against a live system unless stated
---

# Ruckus (CommScope)

Ruckus splits into two acquisitions that share a brand and nothing else: ICX
switching (Foundry → Brocade → Ruckus) and Ruckus wireless (SmartZone,
Ruckus One, Unleashed, ZoneDirector). `docs/research/network-domain-atlas/vendors/ruckus.md`
already covers the SNMP MIB and YANG corpus in detail; this document covers
the four management APIs and does not repeat that MIB/YANG inventory except
where an API interacts with it.

Every claim carries a source link. Where documentation is contradictory or
version-dependent, say which version the claim holds for.

## What it manages

- **SmartZone / vSZ**: a controller cluster (physical SZ100/SZ300/SZ144,
  virtual vSZ-E/vSZ-H) that manages Ruckus APs in zones, and — through a
  second, separately-versioned API — ICX switches [1][7].
- **Ruckus One**: the current cloud-managed offering (renamed from "RUCKUS
  Cloud"), same wireless product line plus switches, managed through a
  tenant/venue hierarchy with a Bearer-JWT REST API [3][4].
- **Unleashed**: a small cluster of APs (one elected master) with no
  separate controller appliance — the master AP serves the web UI and the
  unofficial AJAX API [8][9].
- **ICX FastIron**: standalone managed switches (ICX 7150/7250/7450/
  7550/7650/7850+) run as CLI-first devices, optionally adopted into a
  SmartZone cluster for centralized management [1][7].

## API surface

| Surface | Protocol / transport | Spec available? (OpenAPI/YANG/other, URL) | Versioning scheme |
| --- | --- | --- | --- |
| SmartZone/vSZ public REST API | JSON over HTTPS, session cookie | Swagger 2.0, vendored at `spec/openapi/ruckus/vsz/vsz-7.1.1-v13_1-openapi.json` (688 paths); controller serves its own copy at `/wsg/apiDoc/openapi` [2][1] | Path-versioned (`/wsg/api/public/v9_0` .. `v13_1`); version number tracks controller software release, not a separate API-version negotiation [1] |
| SmartZone switch-management API (ICX) | JSON over HTTPS, `serviceTicket` param | No OpenAPI vendored in this repo; rendered guide only, e.g. `switch-management-public-api-reference-guide-611.html` | Separate path and version line, `/switchm/api/v9_0`..`v11_1` as of SmartZone 6.1.1 — not the same version counter as the wireless API [5] |
| Ruckus One API | JSON over HTTPS, OAuth2 client-credentials + JWT | OpenAPI 3 docs per service, browsable at `docs.ruckus.cloud/api` (e.g. `mspservice-0.3.3`, `switch-0.4.0`); no bulk spec vendored in this repo | Each service versioned independently (`switch-0.4.0`, `mspservice-0.3.3`, …), not one controller-wide version [4][6] |
| Unleashed AJAX/web API | XML/AJAX over HTTPS, community-reverse-engineered | Unofficial Postman collection, `github.com/commscope-ruckus/RUCKUS-Unleashed` (marked not officially supported) [9] | None published; tracks the web UI, which tracks firmware version |
| ICX FastIron RESTCONF | RESTCONF (YANG-driven) over HTTPS, port 443 | YANG models vendored at `spec/yang/ruckus/icx/9.0.00/` (111 modules); rendered guide `docs.ruckuswireless.com/fastiron/fastiron-09010-restconfapi.html` | Tied to FastIron release (09.0.00, 09.0.10, …); no separate RESTCONF version negotiation observed [10][11] |
| ICX FastIron SNMP | SNMP v2c/v3 | 30 `FOUNDRY-*` MIBs vendored at `spec/mib/ruckus/icx/` | MIB set tied to firmware release; not further versioned |
| ICX FastIron CLI | SSH/Telnet | None (CLI reference guides only, e.g. `fastiron-08090-commandref`) | Command set tied to firmware release |

## Authentication and authorisation

### SmartZone / vSZ

- Session-based: `POST /wsg/api/public/{version}/session` with a username
  and password body creates a session; the controller returns a
  `JSESSIONID` cookie that must accompany every subsequent request over
  HTTPS on port 8443 [1]. The vendored 7.1.1 "Essentials" spec does not list
  a `/session` path in its `paths` map or populate `securityDefinitions`
  (checked directly against the JSON) — the session endpoint is documented
  in the rendered guide, not surfaced through the machine-readable spec
  itself. Treat the JSON spec as an operations catalogue, not the full
  auth contract.
- The switch-management API (ICX-via-SmartZone) uses a *different* login
  call, described in its guide as a "Service Ticket Logon API"; every
  subsequent call carries the resulting `serviceTicket` as a request
  parameter rather than a cookie [5]. This is a separate credential from
  the wireless-API session — expect to authenticate twice against the same
  controller if an integration needs both AP and switch data.
- unverified: whether SmartZone enforces per-role scoping on the public API
  beyond the admin account's own RBAC role; the reference guides describe
  RBAC in the admin UI but the fetched pages did not state how it maps onto
  individual API operations.

### Ruckus One

- OAuth2 client-credentials flow. An "Application Token" (client ID +
  client secret + scope) is created under Administration → Account
  Management → Settings [3]. The token endpoint is
  `https://{region.}ruckus.cloud/oauth2/token/{tenantId}`; the request
  carries `grant_type=client_credentials`, `client_id`, `client_secret` and
  returns a JWT [3][4].
- The JWT is sent as `Authorization: Bearer <token>` on every call (RFC
  6750 §2.1) [3].
- A now-retired `/token` endpoint (without the JWT step) was deprecated and
  turned down on 2024-08-31; only the JWT flow works going forward [3].
- MSP/delegated access adds an `x-rks-tenantid` header to act on behalf of
  a managed tenant, and any `{tenantId}` path parameter must then name the
  delegated tenant, not the MSP's own [3].
- unverified: exact scope strings available on an Application Token (the
  fetched auth page did not enumerate them); the switch/Wi-Fi/client
  service catalogue at `docs.ruckus.cloud/api` implies per-service scoping
  but this was not confirmed against a live token.

### Unleashed

- No official API or credential model. The community-documented flow logs
  in against the web UI (username/password), retrieves a CSRF token from
  the response, and includes it on subsequent AJAX POSTs — the same
  session the browser UI uses [8]. There is no service account concept;
  an integration authenticates as an admin user of the Unleashed master AP.
- `aioruckus` (BSD-0, actively maintained, PyPI) implements this flow as a
  Python async client and additionally offers offline parsing of an
  Unleashed/ZoneDirector configuration backup file, letting inventory be
  read without hitting the AJAX API at all [12].

### ICX FastIron

- CLI: local username/password or AAA (RADIUS/TACACS+) over SSH; Telnet is
  also available but sends credentials in clear text.
- RESTCONF: HTTP Basic authentication (username + password) plus TLS;
  the guide states the server returns `401 Authorization Required` for an
  unauthenticated or failed request and separately supports client
  authentication via SSL certificates, but the fetched guide excerpt did
  not spell out a combined mTLS+Basic mode versus either/or [11].
- SNMP: v2c community strings or v3 USM (auth/priv per MIB set already
  documented in `docs/research/network-domain-atlas/vendors/ruckus.md`).

## Data model

- **SmartZone**: `domains → rkszones → aps` is the org hierarchy the
  wireless API exposes; a zone holds WLANs, AP groups and policies (the
  path count under `/rkszones/...` — 232 of 688 total paths in the
  vendored 7.1.1 spec — reflects how much of the surface is zone-scoped
  configuration) [2]. AP identity is the AP's MAC address, used as the path
  parameter on most `/aps/{apMac}/...` operations (checked directly
  against the vendored spec, e.g. `/aps/{apMac}/apPacketCapture`).
- **SmartZone switch management**: switches are inventoried and configured
  through `/switchm/api/{version}/switchconfig` and related paths under a
  separate base (`https://{host}:8443/switchm/api`), with POST-based query
  endpoints returning paginated, sortable results [5]. This confirms the
  brief's premise that ICX switches are reachable *through* SmartZone, but
  it is a distinct API tree from the AP/zone API, not a
  `/wsg/api/public/...` extension — the 7.1.1 "Essentials" spec vendored in
  this repo has no `/switches` or `/switchconfig` paths at all (checked:
  only 4 paths anywhere contain "switch", none of them switch inventory),
  so switch management is either an add-on module or excluded from the
  Essentials edition.
- **Ruckus One**: tenant is the top-level identifier — a 32-character
  string that appears in the URL path (`/api/tenant/{tenantId}/...`) after
  authentication [4][6]. Venues are the site/location grouping under a
  tenant; MSP accounts sit above tenants and delegate access into managed
  tenants via the tenant-delegation-management service [6]. Async
  create/update calls return HTTP 202 with a `requestId`; the caller polls
  `GET /api/tenant/{tenantId}/activity/{requestId}` until it reports
  success, then re-queries the resource list for the created object's ID —
  reads are synchronous, writes are not [4][6].
- **Unleashed**: no persistent controller-side org hierarchy beyond the AP
  cluster itself; inventory is APs, WLANs and clients on that one cluster,
  keyed by AP MAC [8][12].
- **ICX FastIron**: identity is chassis serial number and per-interface
  ifIndex (SNMP) or YANG list keys (RESTCONF); no separate device UUID.
  RESTCONF's writable model is the OpenConfig tree filtered by the ICX
  `-dev`/`-aug` deviation and augmentation modules already vendored at
  `spec/yang/ruckus/icx/9.0.00/` — those modules are the authoritative
  statement of which OpenConfig leaves this platform actually implements,
  per `spec/yang/ruckus/icx/SOURCES.md`.

## Telemetry and events

- **SmartZone**: the `/query/...` paths (36 of 688 in the vendored spec)
  are the report/search interface for historical data; `RUCKUS-CTRL-MIB`
  (already vendored, see the network-domain-atlas dossier) supplies polling
  SNMP tables for AP/client/radio state if the SNMP path is preferred over
  REST polling.
- **Ruckus One**: unverified — no webhook or streaming push mechanism was
  found in the fetched pages; the pattern documented is poll-based
  (activity IDs for write confirmation, presumably periodic GETs for
  status). Searched: "Ruckus One API webhook", "Ruckus One API streaming
  telemetry" (not run this session due to time budget — flagged as gap,
  see Known quirks).
- **Unleashed**: AJAX endpoints return current WLAN/client/AP state on
  request; no push mechanism documented. SNMP traps are available via
  `RUCKUS-UNLEASHED-EVENT-MIB` (vendored). `aioruckus` itself only reads
  snapshots — it does not add a push capability the controller lacks.
- **ICX FastIron**: SNMP traps via `FOUNDRY-SN-TRAP-MIB` /
  `-SN-ROUTER-TRAP-MIB` / `-SN-NOTIFICATION-MIB` (vendored, broadest and
  only trap source per the network-domain-atlas dossier); RESTCONF has no
  push/subscribe mechanism in the fetched guide excerpt (poll-only).

## Rate limits, quotas, pagination

- SmartZone public API: unverified — a community source states "no rate
  limiting defined" as of October 2024, but this was a WebSearch summary,
  not a fetched primary document, so treat it as unverified: no published
  rate limit found for the SmartZone REST API.
- SmartZone switch-management API: paginated POST-query endpoints
  (configurable page size, sortable) on `/switchconfig`-style paths, no
  numeric limit stated in the fetched guide excerpt [5].
- Ruckus One: no numeric rate limit or pagination page-size default was
  found in the fetched overview or auth pages; the overview lists 30+
  per-service API domains but did not enumerate limits [4]. unverified:
  a WebSearch summary of the API overview states the platform returns
  HTTP 429 when a limit is hit and that "each set of APIs identif[ies]
  the limits for those APIs in the API information included with each
  API section" — i.e. limits are per-endpoint, not global — but this page
  could not be independently fetched this session (`docs.ruckus.cloud`
  serves a JS-rendered SPA shell to a direct fetch) and no numeric value
  was found [16].
- Unleashed: none published; it is a single AP's web server, not a scaled
  API.
- ICX FastIron RESTCONF/SNMP: no rate limit; both are direct device
  connections bounded only by the device's own CPU and the SSH/HTTPS
  session limit already recorded for this device family in
  `docs/research/network-domain-atlas/vendors/ruckus.md` (lab evidence:
  `../lab/labsw06-ruckus-icx7150.md` — SSH session limit noted, not re-verified
  here).

## Device-side protocols still available

- **Under SmartZone**: an adopted AP is managed exclusively through the
  controller; direct AP CLI/SSH access still exists but the controller
  overwrites AP-local config on reconnect (standard Ruckus AP adoption
  model — not separately re-verified this session).
- **Under SmartZone (ICX)**: an ICX switch bridged into SmartZone still
  answers SSH CLI and SNMP directly — SmartZone push configuration through
  the switch-management API does not lock those out, per the
  network-domain-atlas dossier's note that "an ICX may be reachable through
  two integrations at once." This is exactly the case FlowSeer's Binding
  concept exists for.
- **Standalone ICX**: SSH/Telnet CLI, SNMP v2c/v3, RESTCONF all coexist
  and are independently enabled/disabled; RESTCONF and the HTTPS web UI
  share TCP port 443 [11].
- **Unleashed**: SNMP (`RUCKUS-UNLEASHED-*` MIBs) runs alongside the web
  UI/AJAX interface; no CLI on the AP hardware itself in this mode
  (unverified against a live Unleashed AP — not in the lab inventory
  per the sweep in `../README.md`).

## Provisioning and onboarding

- **SmartZone AP adoption**: standard Ruckus zero-touch adoption (AP
  discovers controller via DHCP option, DNS, or static config, then joins
  a zone) — not independently re-verified against a fetched source this
  session; treat as background knowledge, unverified: exact discovery
  precedence order.
- **SmartZone ICX bridging**: ICX switches join a SmartZone cluster
  through what the docs call ICX-SZ management, exposed via the
  switch-management API's inventory/group endpoints [5]; the exact
  join/claim handshake was not fetched this session (unverified).
- **Ruckus One**: devices are claimed into a venue under a tenant. The
  Wi-Fi Services AP API exposes an "Add APs" operation, `POST
  /venues/{venueId}/aps`, taking a list of new APs with `serialNumber`,
  `venueId`, `apGroupId`, `name`, `description`, `position`, and `tags`;
  a duplicate serial is rejected with a "Serial Already Registered" error
  — this is serial-based claim into a venue, not QR code or bulk import.
  unverified: this is a WebSearch summary of `docs.ruckus.cloud/api`
  (a JS-rendered SPA that returns only nav chrome to a direct fetch), not
  independently fetched this session; treat the exact field names as
  second-hand [15].
- **ICX FastIron firmware**: manifest-file upgrades are the vendor-
  recommended path. `copy tftp system-manifest <server-ip> <manifest-file>
  [primary|secondary|all-images-primary|all-images-secondary]` pulls a
  manifest containing "all boot, firmware, and application images as well
  as signature files," and the switch selects the images matching its own
  device family automatically [13]. SCP transfer of manifest files is not
  supported; only TFTP. PoE firmware upgrades are separate and manual [13].
  Config backup ahead of an upgrade is `copy tftp` of the `startup-config`
  file to a TFTP server, recommended specifically so a downgrade has a
  known-good config to restore [13].

## Known quirks and traps

- The vendored 7.1.1 SmartZone OpenAPI spec is titled "Essentials" but the
  network-domain-atlas dossier already notes it includes `/domains`
  endpoints (the vSZ-H/high-scale differentiator) despite that — and this
  research adds the opposite gap: it has **no** switch-management paths at
  all, meaning ICX-via-SmartZone requires fetching the separate
  switch-management spec from a live controller; it is not in this
  captured document [2][5].
- SmartZone's wireless API and switch-management API are versioned on
  independent counters (`v13_1` vs `v11_1` as of the versions checked) and
  require separate login calls with separate credential types (cookie vs.
  `serviceTicket`) even against the same controller [1][5].
- Ruckus One's original `/token` OAuth endpoint is dead (turned down
  2024-08-31); any integration guide or sample code predating that date
  needs updating to the JWT flow before it will work [3].
- The Unleashed API is explicitly unsupported and undocumented by the
  vendor; the CommScope-published GitHub repo is a Postman collection, not
  a spec, and carries no compatibility guarantee across firmware versions
  [9]. Treat any integration against it as fragile by design.
- unverified: whether FastIron 10.x adds NETCONF alongside RESTCONF. The
  09.0.10 RESTCONF guide fetched for [11] never mentions NETCONF, and a
  WebSearch for the FastIron 10.0.10 Management Configuration Guide
  (`support.ruckuswireless.com/documents/4467-...`) only reached an
  unrelated vendor portal redirect, not the guide text — the 10.x NETCONF
  question was not settled this session; treat FastIron as RESTCONF-only
  until a fetched 10.x guide says otherwise.
- FastIron's RESTCONF surface and the standalone SNMP MIB surface overlap
  in places (interfaces, VLANs, LLDP) but are not a 1:1 mirror — the
  network-domain-atlas dossier's advice to read the ICX `-dev` YANG modules
  before writing a RESTCONF collector, "the difference between a working
  RESTCONF client and a pile of 404s," applies unchanged here.
- Ruckus publishes no static download of the SmartZone OpenAPI spec
  anywhere; `spec/openapi/ruckus/vsz/SOURCES.md` already records that the
  vendored copy is a community capture of a live controller's
  `/wsg/apiDoc/openapi` response, not an official artifact. The refresh
  path is pulling from an owned controller, not the vendor's docs site.

## What FlowSeer needs

- **Credentials to store per surface**: SmartZone wireless — admin
  username/password (to mint sessions); SmartZone switch-management —
  same or a separate admin account (separate `serviceTicket` login);
  Ruckus One — OAuth2 client ID + client secret per tenant, plus the
  tenant ID itself as a required path parameter on every call; Unleashed
  — an admin username/password with no scoping available; ICX standalone
  — SSH credentials (or AAA-backed) and, if RESTCONF is used, its own
  Basic-auth credentials (may be the same account).
- **Inventory endpoints**: SmartZone `/aps/...` (keyed by AP MAC) and
  `/switchm/api/{version}/switchconfig` (ICX); Ruckus One's per-service
  inventory endpoints under `/api/tenant/{tenantId}/...` (service name
  varies — `switch-0.4.0` for switches); Unleashed AJAX AP/WLAN/client
  calls or, preferably, `aioruckus`'s offline backup-file parser to avoid
  depending on the undocumented API at all; ICX standalone via SNMP
  (`FOUNDRY-SN-*`, broadest coverage) or RESTCONF (better-typed, narrower
  coverage per the `-dev` modules).
- **Identifiers to key on**: AP MAC (SmartZone, Unleashed), switch serial
  number (ICX, both standalone and SmartZone-managed), Ruckus One tenant
  ID + venue ID + device serial.
- **Rate-limit budget**: no confirmed numeric ceiling on any surface;
  budget conservatively and watch for 429s until a live-controller test
  establishes real numbers — this is a gap worth closing with a lab device
  before committing to a polling interval.
- **Minimum API version to target**: SmartZone `v13_1` (matches the
  vendored 7.1.1 spec and is the newest generation checked); Ruckus One —
  no controller-wide version, track each service's own semver
  independently; ICX RESTCONF — FastIron 09.0.00+ (matches the vendored
  YANG set; earlier firmware has an older, unvendored RESTCONF guide only).

## Sources

1. https://docs.ruckuswireless.com/smartzone/7.1.0/vszh-public-api-reference-guide-710.html — fetch attempted 2026-09-10, blocked by tool content-size limit; session/version facts corroborated via [2] (vendored spec metadata) and WebSearch summary of this and sibling SZ100/SZ144 guides (session-cookie login via `POST /{version}/session`, base URL `https://{host}:8443/wsg/api/public`, `/wsg/apiDoc/openapi` doc endpoint).
2. `spec/openapi/ruckus/vsz/vsz-7.1.1-v13_1-openapi.json` — inspected directly in-repo, 2026-09-10 (688 paths, 1068 operations, `info.version` = `v13_1`, `basePath` = `/wsg/api/public/v13_1`, no `/session` path and empty `securityDefinitions` in this capture).
3. https://docs.ruckus.cloud/api/docs/api-authentication-and-jwt — fetched 2026-09-10 (OAuth2 client-credentials flow, token endpoint pattern, JWT Bearer usage, `/token` deprecation date, `x-rks-tenantid` delegation header).
4. https://docs.ruckus.cloud/api/ — fetched 2026-09-10 (regional base URLs `api.ruckus.cloud` / `api.eu.ruckus.cloud` / `api.asia.ruckus.cloud`, async `requestId`/activity-poll pattern, 30+ service domain list).
5. https://docs.ruckuswireless.com/smartzone/6.1.1/switch-management-public-api-reference-guide-611.html — fetched 2026-09-10 (`serviceTicket` auth, base path `/switchm/api`, `v11_1` covered by this doc with a `v9_0`-`v11_1` compatibility matrix, paginated `/switchconfig` query endpoints).
6. WebSearch results for "Ruckus One API venue tenant model MSP hierarchy documentation" — 2026-09-10 (tenant ID in URL path, MSP entitlement/delegation model, venue-scoped endpoints such as floor-plan calibration); underlying pages (`docs.ruckus.cloud/api/mspservice-0.3.3`, `.../tenant`) not independently fetched — treat tenant/venue specifics here as second-hand.
7. `docs/research/network-domain-atlas/vendors/ruckus.md` — read in-repo 2026-09-10 (ICX/wireless product taxonomy, SNMP/YANG/OpenAPI/protobuf corpus inventory).
8. WebSearch results for "Ruckus Unleashed API JSON XML community" — 2026-09-10 (login-then-CSRF-token flow, XML/AJAX payload, unofficial/unpublished status); primary community-forum pages returned 404 or title-only on direct WebFetch this session.
9. https://github.com/commscope-ruckus/RUCKUS-Unleashed — fetched 2026-09-10 (Postman collection for XML/AJAX AP monitoring, explicitly "not officially supported by RUCKUS").
10. `spec/yang/ruckus/icx/SOURCES.md` and `spec/yang/ruckus/icx/9.0.00/` — read in-repo 2026-09-10 (111 YANG modules, FastIron 09.0.00 RESTCONF model set, ICX `-dev`/`-aug` deviation/augmentation files).
11. https://docs.ruckuswireless.com/fastiron/fastiron-09010-restconfapi.html — fetched 2026-09-10 (RESTCONF base path `https://{host}/restconf/data/...`, POST/GET/PATCH/PUT/DELETE methods, config domains covered such as AAA/ACL/DNS/LAG/LLDP/OSPF/PoE/STP/VLAN; page did not state FastIron version explicitly or confirm/deny NETCONF support).
12. https://github.com/ms264556/aioruckus (via WebSearch summary, page itself not fetched) — 2026-09-10 (Python async client for Unleashed/ZoneDirector AJAX Web Service, BSD-0 license, offline backup-file parsing mode, ZoneDirector 9.10+ compatibility).
13. https://docs.ruckuswireless.com/fastiron/08.0.70/fastiron-08070-upgradeguide/GUID-70D129FE-BA03-4044-99D3-072E361B20BE.html — fetched 2026-09-10 (`copy tftp system-manifest` syntax, manifest file contents, primary/secondary partition semantics, TFTP-only transfer, separate PoE firmware path).
14. WebSearch results for "FastIron write memory configuration save reload semantics" — 2026-09-10 (write memory → `startup-config.txt`, stacking sync-on-write behavior, reload-without-save falls back to `stacking.boot`); underlying `docs.ruckuswireless.com` pages not independently fetched.
15. WebSearch results for `docs.ruckus.cloud` AP API "Add APs" endpoint — 2026-09-10 (`POST /venues/{venueId}/aps`, `serialNumber`/`venueId`/`apGroupId` fields, "Serial Already Registered" conflict error); `docs.ruckus.cloud/api/wifi-17.3.3.312/ap` returned 404 on direct WebFetch this session, so this endpoint shape is second-hand.
16. WebSearch results for Ruckus One API rate limiting — 2026-09-10 (429 on limit exceeded, limits documented per API section rather than globally); `docs.ruckus.cloud/api/overview.html` and `.../api/original/overview.html` both returned 404 on direct WebFetch this session (SPA shell only), so no numeric limit was confirmed against a primary source.
