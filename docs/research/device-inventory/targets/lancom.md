---
title: Integration target — LANCOM Systems
date: 2026-09-10
scope: LANCOM Management Cloud (LMC) REST API, LCOS (routers/WLCs), LCOS LX
  (access points), LCOS SX (switches, FASTPATH and LCOS-SX-5.x lines)
status: research from public documentation; nothing verified against a live system unless
  the lab switch (172.16.0.0/24 .4, GS-2326+ on LCOS SX 3.34) is named explicitly
---

# LANCOM Systems

LANCOM sells three unrelated device firmware stacks (LCOS, LCOS LX, LCOS SX)
under one brand, plus a cloud platform (LMC) that manages all three. The MIB
and OpenAPI corpus is already vendored and inventoried in
`docs/research/network-domain-atlas/vendors/lancom.md` and
`spec/openapi/lancom/SOURCES.md`; this document does not repeat that
inventory, only the API/protocol behavior around it. Every claim carries a
source link. Where documentation is contradictory or version-dependent, say
which version the claim holds for.

## What it manages

- **LMC**: a multi-tenant cloud controller (account → project → site →
  device) for LCOS routers/WLCs, LCOS LX APs, and LCOS SX switches. Public
  instance hosted in a German data center; a private/on-prem deployment
  option exists for service providers and specific data-protection
  requirements [1].
- **LCOS**: routers, VPN gateways, WLAN controllers — LANCOM's own firmware,
  config tree mechanically projected onto both the CLI and the SNMP MIB
  (`docs/research/network-domain-atlas/vendors/lancom.md`, "LCOS" section).
- **LCOS LX**: access points, a separate codebase with the same
  Setup/Status split, either standalone or LMC-managed [2].
- **LCOS SX**: switches. Two unrelated firmware lines share the name: GS-13xx/
  GS-23xx/GS-3xxx run Broadcom FASTPATH-derived LCOS SX 3.x/4.x; XS-series and
  GS-45xx run LCOS SX 5.x [3][17]. The lab device (.4, GS-2326+) is on LCOS SX
  3.34, the FASTPATH line.
- Scale: LMC is licensed per device, "just a few or even several thousand
  devices" per account on the public cloud [1]; a private-cloud appliance is
  sized for up to 1,000 or 5,000 devices [4].

## API surface

| Surface | Protocol / transport | Spec available? | Versioning scheme |
| --- | --- | --- | --- |
| LMC REST API | JSON over HTTPS, 13 independent microservices | OpenAPI 3.0.3, vendored at `spec/openapi/lancom/lmc-openapi/*.json` (auth, backstage, config, control, devices, devicetunnel, dsc, fields, jobs, logging, messaging, monitoring, notification) [5] | Each microservice versions independently — `info.version` in the vendored specs ranges from `1.0.0` (monitoring) to `33.3.8` (devices) [5] |
| LCOS CLI | SSH, Telnet, Telnet-over-SSL | No machine-readable spec; the CLI Reference Guide and Menu Reference Guide (per LCOS version) document it in prose [6] | Command set and config-tree paths tied to LCOS firmware version |
| LCOS SNMP | SNMPv2c, SNMPv3 | `LCOS-MIB` + one `LC-UNIFIED-LCOS-<version>-REL-OIDS.mib` per release, vendored at `spec/mib/lancom/lcos/` | **OID assignments are not stable across LCOS releases** — this is why fifteen per-release OID files exist (network-domain-atlas, "Traps" section) |
| LCOS SX CLI (FASTPATH line) | SSH, Telnet | No machine-readable spec; FASTPATH-derived CLI Reference PDFs per SX version [7] | Command set tied to LCOS SX firmware version |
| LCOS SX SNMP | SNMPv2c, SNMPv3 | FASTPATH MIBs + `LCOS-SX-MIB`/`LCOS-SX-GENERAL-MIB`, vendored at `spec/mib/lancom/sx/` | Two full firmware-version MIB trees vendored (5.20, 5.30) |
| LCOS LX CLI/SNMP | SSH/Telnet, SNMPv2c/v3 | `LCOS-LX-MIB` + two per-model files, vendored at `spec/mib/lancom/lx/` | Tied to LCOS LX firmware version |
| RESTCONF/NETCONF/gNMI on any LANCOM device | none | Not implemented on any LCOS/LCOS LX/LCOS SX device; no YANG models published [8] | n/a |

## Authentication and authorisation

The vendored LMC OpenAPI specs declare three security schemes on every
microservice: `apiKey` (header `Authorization: LMC-API-KEY <key>`), `basic`
(username/password, optionally scoped to account IDs), and `bearer` (a JWT
session token) [5]. This is not OAuth2 client-credentials, despite
`spec/openapi/lancom/SOURCES.md` describing it that way — the spec's own
`TokenAuth` schema marks the username/password fields on `POST /auth` as
deprecated in favor of `POST /userlogin`, and neither endpoint implements an
OAuth2 grant type or token endpoint discovery [5]. The documented path for
programmatic/integration access is a long-lived **API key**, created per-user
in the LMC UI (profile settings → API Keys) and scoped to a project; the key
is shown once and cannot be recovered if lost [9].

- **Session flow**: `POST /userlogin` (username + password) or `POST /auth`
  (API key or existing bearer token) returns a `Token` object (`value`,
  `created`, `expires`); `DELETE /auth` ends the session. `POST /auth` also
  handles session renewal — sending an unexpired token extends it [5].
- **Rights model**: authorization is right-based, not role-based. Each
  operation in the spec lists required rights inline in its description
  (e.g. `DEVICE_CREATE` to claim a device, `PAIRING_TOKEN_CREATE` to mint a
  pairing token, `DETAIL_CONFIG_READ` + optional `PASSWORD_READ` to read a
  device's live configuration) [5]. `GET /rights/accounts/{accountId}`
  returns the caller's actual rights for an account — the way to discover
  what a given API key can do without trial and error.
  `GET /rights/public` lists every right the platform defines.
  Rights are managed per **authority** (`/accounts/{accountId}/authorities`),
  not per API key directly — a key inherits the rights of the account
  membership it was created under [5].
- **Least-privilege read integration**: `DEVICE_READ` (device/site
  inventory), `DETAIL_CONFIG_READ` without `PASSWORD_READ` (config diff
  without secrets), rights under the `monitoring`/`notification` services
  for telemetry and alerts.
- **Write integration**: `DEVICE_CREATE` + `PAIRING_TOKEN_CREATE` (claim and
  pair devices), `DEVICE_UPDATE` (trigger rollout, reset to default),
  `JOB_MODIFY` (bulk claim/import jobs).
- unverified: exact API-key lifetime/rotation policy and whether keys can be
  scoped to a subset of rights at creation time — the fetched getting-started
  guide describes creation but not expiry behavior [9].

## Data model

- **Hierarchy**: account → (optionally) child accounts → site → device.
  `PUT /accounts/{accountId}/child-projects/{projectId}` and
  `GET /accounts/{accountId}/children` expose the account nesting; sites are
  `/accounts/{accountId}/sites/{siteId}`; devices are
  `/accounts/{accountId}/devices/{deviceId}`, optionally filtered to a site
  via `/accounts/{accountId}/sites/{siteId}/devices` [5].
- **Identifiers**: `accountId`, `siteId`, `deviceId` are server-issued UUIDs
  — the stable keys for cross-references. The device **serial number** is
  the stable hardware identifier used at claim time and exposed as a query
  filter (`status.serial`, `status.serialLike`); MAC and IP are also
  queryable (`status.mac`, `status.ip`) but are not primary keys [5].
- **Inventory read**: `GET /accounts/{accountId}/devices` (and the
  `devices-table` variant for grid views) supports filtering by name,
  model, firmware version/build/label, site, custom fields, claiming state,
  heartbeat state, and config status; `GET .../devices/ids` returns just IDs.
  `id` filters cap at 50 UUIDs per request (`QUERY_IDS`); `limit` on table
  endpoints caps at 1500 rows (`QueryLimit`) [5].
- **Configuration model** is split across two axes:
  - **Device-specific config** (`configdevice`, `configbuilder` services):
    per-device profiles, function groups, role functions, port details.
    `PUT /configbuilder/.../preview` renders a config preview from
    submitted base data (async — returns a ticket ID under load);
    `GET /configbuilder/.../running` returns the device's actual current
    config (202 while still downloading, poll again); `POST
    /configbuilder/.../resetToDefault` reverts to defaults [5].
  - **SDN / config-by-intent** (`confignetwork`, `configsubnetgroup`,
    `configapplication`, `configradiusserver`, `configsecurity` services):
    networks, subnet groups, applications (firewall/QoS intent objects),
    RADIUS servers, and security whitelists are defined once and referenced
    by devices/sites — the same object can roll out to many devices. Global
    applications (`globalapplications`) and variables
    (`configvariable`, scoped to account/network/device/subnetgroup) let a
    template parameterize per target [5].
  - Both axes converge on **rollout**: `POST
    /configdevice/accounts/{accountId}/devices/{deviceId}/rollout` and the
    equivalent subnet-group-level rollout push pending config to the
    device(s). A `force` flag (`OPT_FORCE_ROLLOUT`) overrides staleness
    checks; a 424 response with `OutdatedErrorMessageDto` means the rollout
    could not be triggered because a dependency is out of date [5].
- **Config sync semantics**: `GET
  /configdevice/accounts/{accountId}/devices/{deviceId}/state` returns
  `configState`, `updateState` (e.g. `OUTDATED`), `outdatedCause` (e.g.
  `INVALID`), `lastConfigChange`, `communicationState`, and
  `communicationSince` — this is the read-after-write consistency signal:
  poll `state` after a rollout rather than assuming immediate convergence
  [5]. The device inventory endpoint separately exposes a coarser
  `configStatusFilter.category` enum: `CURRENT`, `ERROR`, `IN_PROGRESS`,
  `OUTDATED`, `OUTDATED_INVALID`, `UNKNOWN` [5].
- unverified: typical propagation delay from rollout trigger to device
  applying config — not stated in the spec; depends on the device's poll/
  heartbeat interval to the LMC, which is itself undocumented in the
  vendored material.

## Telemetry and events

- **Monitoring service**: sixteen `GET /api/{accountId}/tables/<name>`
  endpoints (device-info, device-services, wan-interface, lan-interface,
  wlan-interface, wlan-station, wlan-network, wlan-neighbor, client-traffic,
  application-traffic, dhcp-lease, vpn-connection, phone-line, login-event,
  wired-station, stack-unit-component-state) — each is a time-series table
  read, not a snapshot poll [5]. Common query shape: `deviceId` (max 10) or
  `siteId` (max 3) filters, `from`/`to` ISO-8601 window (default window is
  the last hour when `latestBy` is set and `from` is omitted), `aggregationLevel`
  to group by account or site, and `latestPerDevice`/`latestBy` for
  point-in-time reads instead of a full series [5].
- **Alerts**: `notification` service exposes `GET
  /accounts/{accountId}/alerts` (list, with `/count` and `/ids` variants,
  filterable and per-type) and `GET .../alert/{alertId}` — a pull model, no
  push/webhook registration endpoint anywhere in the vendored specs [5].
- **No webhook or streaming push found** in any of the 13 vendored LMC
  OpenAPI documents (`messaging` and `logging` services expose only the
  common `rights` endpoints, nothing message-specific) — searched
  `spec/openapi/lancom/lmc-openapi/{messaging,notification,logging}.json`
  directly. unverified: whether a push mechanism exists outside the public,
  unauthenticated per-service specs (the two unvendored services,
  `geolocation` and `preferences`, return 401 and were not inspected;
  `spec/openapi/lancom/SOURCES.md` records this) [5].
- **Device-side telemetry protocols remain SNMP** — polling the tables
  documented in `docs/research/network-domain-atlas/vendors/lancom.md`
  (DHCP leases, WLAN scan results, firewall session table on LCOS; PoE/fan/
  temperature/SFP tables on LCOS SX) — independent of whether the device is
  LMC-managed [8].

## Rate limits, quotas, pagination

- **No published rate limit** in the fetched LMC API documentation or in
  any vendored OpenAPI spec — the getting-started guide does not mention
  request throttling [9]. Treat this as unverified rather than "no limit";
  the vendor may enforce one without documenting it.
- **Pagination is filter-plus-limit, not cursor-based**: `limit` caps table
  reads at 1500 rows (`QueryLimit`); batch-by-ID requests cap at 50 UUIDs
  (`QUERY_IDS`/`QUERY_IDS_REQUIRED`); monitoring queries cap at 10 device
  IDs or 3 site IDs per request [5]. There is no `nextPageToken`/cursor
  field in any vendored schema — a caller paginates by combining `sort`,
  `limit`, and a filter that excludes already-fetched rows (e.g. name or ID
  range), or by fetching `.../ids` first and then batching `id` filters of
  up to 50.
- **Job creation is serialized per account**: `POST
  /accounts/{accountId}/jobs/claim-devices` (bulk device claim) and
  `.../jobs/import-sites` both state "at each time, there can be at most one
  executing job in the account" and return 400 if exceeded, with a retry-
  later instruction [5] — the closest thing to a documented backoff rule in
  this API.

## Device-side protocols still available

LMC management does not lock out on-device protocols; they run in parallel
[8]:

- **SNMP v2c/v3** — always available; the per-interface `Access-Table` at
  `/Setup/Config/Access-Table` on LCOS controls which protocols (Telnet,
  TFTP, HTTP, SNMP, HTTPS, SSH, SNMPv3) are reachable on which interface
  [10].
- **CLI over SSH/Telnet/Telnet-SSL** — always available, same menu tree the
  MIB is generated from. Core commands: `cd <path>` (navigate the config
  tree; `cd -` returns to the last directory), `dir`/`ls`/`ll` (list),
  `set <path> <value>` (change a parameter), `show <what>` (display state),
  `default [-r] [path]` (reset to default), `do <path> [params]` (invoke an
  action node), `readconfig`/`writeconfig` (display/change settings from the
  CLI directly, distinct from file-based config transfer), `readscript
  [-d][-n][-c][-m]` (dump the current config as a `.lcs` script), `loadconfig`/
  `loadscript`/`loadfirmware` (pull a file via TFTP and apply it),
  `passwd`, `sysinfo`, `ping`, `trace` [10]. `?` at any point in the tree
  lists available commands there. SSH login accepts an inline password:
  `ssh -o "Password=<pw>" user@<ip>` [10].
- **`.lcs` script files** are the CLI's serialization format — a sequence of
  `cd`/`set` commands `readscript` emits and `loadscript` replays; this is
  how LANconfig exports/imports a full device config as text rather than
  binary.
  unverified: exact on-wire grammar edge cases (comments, conditionals) —
  not covered by the fetched KB page.
- **WEBconfig (HTTPS UI)** — browser-based config, same menu tree rendered
  as HTML forms; also reachable via LMC's `devicetunnel` service
  (`POST /accounts/{accountId}/webconfig` creates a tunnel session, `GET
  .../webconfig/{entryToken}` reports tunnel connection status, `DELETE`
  ends it) for devices behind NAT [5]. The same `devicetunnel` service has a
  `terminal` endpoint pair for SSH-over-cloud-tunnel.
  unverified: whether WEBconfig exposes any JSON/REST endpoint of its own
  under a `/config/...` path — not found in the fetched documentation or the
  vendored specs; `spec/openapi/lancom/SOURCES.md` states no REST/JSON API
  exists on-device [8].
- **TFTP** — used for LANconfig device discovery (a "Sysinfo"-only open
  port makes a device discoverable without exposing config) and for
  `loadconfig`/`loadfirmware`/`loadscript` file transfer; documented as
  unencrypted and a security risk if left open on an untrusted interface
  [10][11]. unverified: LANmonitor/LANconfig's proprietary discovery-probe
  port number — search results referenced TFTP (69) and SNMP (161) but did
  not confirm or deny a UDP 1370 LANCOM-specific discovery protocol; no
  vendored evidence either way.
- **TR-069 (CWMP)** — LCOS routers support "certain features" of TR-069 for
  auto-provisioning and remote management since LCOS 9.10, aimed at
  provider environments (ACS URL/credentials configurable via LANconfig or
  WEBconfig) [12]. unverified: which TR-069 data model version and which
  parameters are implemented — the fetched overview page names the feature
  without the parameter list (linked sub-pages were not fetched).
- **LCOS SX (FASTPATH line)**: CLI equivalent is FASTPATH's own command set
  (different verbs from LCOS), plus `config-file export`/`import` via TFTP
  and a web UI; SNMPv3 available [3][13]. unverified: exact FASTPATH CLI
  verbs for config-file transfer beyond what the vendored MIB/README implies
  — the CLI Reference PDF was located but not fetched in full.
- **No RESTCONF/NETCONF/gNMI, no YANG**, on any device class — restated
  from `spec/openapi/lancom/SOURCES.md` and confirmed by search: no
  evidence of a REST API on LCOS SX 5.x (XS/GS-45xx) either, despite that
  line being newer [8][14].

## Provisioning and onboarding

- **Claim by serial + cloud PIN**: `POST
  /accounts/{accountId}/devices` with `DeviceClaimData` (`pin`, `serial`
  required; optional `name`, `siteId`, `location`, `address`) claims a
  single device, requiring `DEVICE_CREATE` [5]. The PIN ships on a
  "Cloud-ready flyer" with the device; the serial is on the device label or
  in LANconfig/WEBconfig [15]. On the device side, no action is needed for
  a cloud-ready device beyond having network reachability — the device
  contacts LMC on its own and completes pairing on next contact [15].
- **Pairing tokens** (account-level, not per-device): `POST
  /accounts/{accountId}/pairings` creates a token with a caller-chosen
  validity up to 31,536,000 seconds (1 year); `GET` lists active tokens;
  `DELETE .../pairings/{token}` revokes one [5]. This is the mechanism
  behind the LMC "Rollout Assistant" (a camera-equipped phone/tablet scans
  serial+PIN) and behind "activation code" pairing done from WEBconfig's
  `Setup Wizards → LANCOM Management Cloud` [15].
- **Bulk onboarding**: `jobs` service, `claim-devices` and `import-sites`
  job types — async, upload-driven (`POST .../{jobId}/uploads/{uploadId}`
  attaches a file, presumably a CSV/serial-PIN list; not documented further
  in the spec's schema comments), one job executing per account at a time
  [5].
- **Firmware management**: `devices` service exposes
  `GET/POST/DELETE .../firmware/preferences` (per-account update policy),
  `GET/POST .../firmware/update` (trigger), and `.../meta/firmware` (catalog)
  [5]. Firmware level is filterable as `RELEASE`, `RELEASE_CANDIDATE`, or
  `ALPHA` (`QueryFirmwareLevel`, default `RELEASE`) [5].
- **Factory reset / re-provisioning**: `POST
  /configbuilder/.../resetToDefault` resets config to default;
  `DELETE /accounts/{accountId}/devices/{deviceId}` (devices service) and a
  parallel `DELETE .../devices/{deviceId}` on the `control` service both
  exist — unverified which one is authoritative or whether both must be
  called; the spec does not cross-reference them [5].
- **WLC auto-config of APs**: not an LMC feature — this is LCOS's own
  WLAN-controller function. A managed AP paired to a WLC (LAN cable + valid
  certificate) picks up an AutoWDS profile and thereafter operates
  autonomously; if it loses its CAPWAP connection to the WLC for a
  configured time, it falls back to client mode and scans for another
  anchor AP [16]. Configured in LANconfig under `WLAN controller > Profiles
  > AutoWDS`, keyed on an `AutoWDS-Rollout-SSID` [16].

## Known quirks and traps

- The vendored `spec/openapi/lancom/SOURCES.md` calls LMC auth "OAuth2" —
  the spec itself shows API-key/basic/bearer, not an OAuth2 grant. Do not
  build an OAuth2 client-credentials flow against this API; use the
  `LMC-API-KEY` header or a session token from `/userlogin` [5][9]. Worth
  correcting that file once FlowSeer starts implementing against this API.
- **LCOS SNMP OIDs shift between firmware releases.** Any decoder must pin
  a specific `LC-UNIFIED-LCOS-<version>-REL-OIDS.mib` rather than assuming
  OID stability across upgrades (network-domain-atlas, "Traps" section) —
  this is the single most disruptive fact for a long-lived SNMP integration
  against LCOS.
- **GS-2310/GS-2326 add a third, parallel object set**
  (`LANCOM-GS2310-FUNCTION-MIB`, `gs2310*`) alongside both FASTPATH and
  LANCOM's own `lcs*` tables on the same device, and some entities (fans,
  PoE, temperature) are reported by two different table families that can
  disagree (network-domain-atlas, "LCOS SX" section) — pick one source
  deliberately.
- **`GET .../configbuilder/.../running` can return 202** while the config
  download from the device is still in flight; a caller must poll rather
  than treat a non-200 as an error [5].
- **Rollout can fail with 424** if a dependency (e.g. a referenced network
  or subnet group) is itself outdated; `OPT_FORCE_ROLLOUT` exists but
  bypasses that safety check [5].
- **Two device-delete endpoints** (`devices` service and `control` service)
  with no documented relationship between them — a write integration should
  test which one this account's rights actually permit rather than assume.
- **LCOS SX line split is easy to get wrong**: GS-13xx/GS-23xx/GS-3xxx stay
  on LCOS SX 3.x/4.x (FASTPATH-derived); XS-series/GS-45xx moved to LCOS SX
  5.x. A decoder or CLI driver keyed on "LCOS SX" alone will not work across
  both without a firmware-version branch [3].

## What FlowSeer needs

- **Credentials to store**: one LMC API key per integrated account (header
  `Authorization: LMC-API-KEY <key>`), scoped to the rights listed under
  Authentication above; rotate manually since the API does not document
  expiry.
- **Inventory endpoints**: `GET /accounts/{accountId}/devices` (or
  `devices-table` for paged grid reads) keyed by `deviceId` (UUID, stable)
  with `status.serial` as the human/hardware-facing identifier; `GET
  /accounts/{accountId}/sites` for the site tier.
- **Config endpoints**: `GET .../configdevice/.../state` for sync status
  polling after any change; `POST .../configdevice/.../rollout` to push;
  `GET .../configbuilder/.../running` for current on-device config
  (poll-until-200).
- **Telemetry endpoints**: `monitoring` service table reads
  (`device-info`, `wan-interface`, `wlan-station`, etc.), windowed by
  `from`/`to`, batched at ≤10 device IDs or ≤3 site IDs per call; `alerts`
  from `notification` for event polling since no webhook exists.
- **Identifiers to key on**: `deviceId` (UUID) as primary key,
  `status.serial` as the durable cross-system identifier (survives a device
  being unclaimed/reclaimed, unlike `deviceId` which the spec does not
  guarantee is preserved across a delete+reclaim).
- **Rate-limit budget**: none documented — build in client-side throttling
  and respect the 1500-row/50-ID/10-device caps as hard request-shape
  limits, not just performance tuning.
- **Minimum API version**: no cross-service version to target since each
  microservice versions independently; pin against the vendored spec
  versions (`spec/openapi/lancom/lmc-openapi/*.json`, `info.version` per
  file) and re-vendor before adopting a newer one, since paths and schemas
  can change per microservice release.
- For LMC-unmanaged or partially-managed fleets (the lab switch case),
  SNMP remains the only cross-cutting protocol — the LMC REST API is
  additive, not a replacement for the SNMP decoder path.

## Sources

1. FAQ — R&S®LANCOM Management Cloud, https://rs-nc.rohde-schwarz.com/en/solutions/faq/lancom-management-cloud — fetched 2026-09-10. Confirms public LMC hosted in a German data center under German data protection law, multi-tenant single-server model, private-cloud option for service providers.
2. `docs/research/network-domain-atlas/vendors/lancom.md` (repo, dated 2026-08-30) — LCOS LX MIB/config-tree structure.
3. WebSearch, "LANCOM LCOS SX 5.x REST API GS-3xxx GS-4xxx switch" — 2026-09-10. GS-13xx/GS-23xx/GS-3xxx stay on LCOS SX 3.x/4.x; XS-series/GS-45xx moved to LCOS SX 5.x.
4. WebSearch synthesis over LANCOM Private Cloud marketing pages (rs-nc.rohde-schwarz.com, lancom.gr) — 2026-09-10. Hardware appliance sizes of 1,000/5,000 devices; not independently re-fetched, treat as lower-confidence than [1].
5. `spec/openapi/lancom/lmc-openapi/*.json` (repo, vendored 2026-08-20 from `https://cloud.lancom.de/cloud-service-<service>/api-docs/index.json`) — read directly for this document on 2026-09-10: `auth.json` (v18.80.2), `devices.json` (v33.3.8), `config.json` (v5.5.220), `control.json` (v3.30.2000), `devicetunnel.json` (v17.4.68), `jobs.json` (v13.26.49), `monitoring.json` (v1.0.0).
6. LCOS CLI Reference / Menu Reference Guides, referenced from knowledgebase.lancom-systems.de — not fetched directly; existence and per-version scope confirmed via search result titles ("LCOS SX 5.00/5.10 CLI Reference").
7. LCOS SX 5.00/5.10 CLI Reference PDFs, `https://www.lancom-systems.de/download/documentation/CLI-Reference/MA_LCOS-SX-5.00-CLI-Reference_EN.pdf` and `https://ftp.lancom.de/Documentation/LCOS-SX/MA_LCOS-SX-5.10-CLI-Reference_EN.pdf` — located via search, not fetched in full; cited only for their existence/version scope.
8. `spec/openapi/lancom/SOURCES.md` (repo) — states no RESTCONF/NETCONF/gNMI/YANG on any LANCOM device; on-device management is WEBconfig, CLI, SNMPv2c/v3, and the proprietary LMC tunnel.
9. LANCOM Management Cloud API — Getting Started Guide, https://knowledgebase.lancom-systems.de/pages/viewpage.action?pageId=194773420 — fetched 2026-09-10. API-key creation in user profile settings, `Authorization: LMC-API-KEY <key>` header format, key not recoverable once created, no rate limit stated.
10. Useful commands for accessing LANCOM routers & access points from the console, https://knowledgebase.lancom-systems.de/pages/viewpage.action?pageId=36453641 — fetched 2026-09-10 (original `/spaces/KBEN/pages/36453641/` link was dead, 404; corrected to the resolving `viewpage.action?pageId=` form). CLI command list, config-tree navigation, SSH inline-password syntax, Access-Table location. Confirms `readconfig`/`writeconfig`/`loadscript` as documented command names.
11. Management protocols, https://www.lancom-systems.de/docs/LCOS/Refmanual/EN/topics/lanconfig_management-protocols.html — fetched 2026-09-10. TFTP-based discovery ("Sysinfo" port), SNMP for LANmonitor, HTTP/HTTPS auto-redirect for WEBconfig, TFTP unencrypted-data warning.
12. TR-069-Unterstützung (LCOS 9.10 Addendum, DE), https://lancom-systems.de/docs/LCOS-Addendum/9.10-RU1/DE/topics/add910_config_cwmp.html — fetched 2026-09-10. Confirms TR-069/CWMP support added in LCOS 9.10 for provider auto-provisioning; parameter-level detail not on this page.
13. Release notes for GS-2326(P)/GS-2352(P)/GS-2310P, located via search (ftp.lancom.de/Documentation/Release-Notes/LCOS%20SX/) — not fetched in full; cited only for config export/import-via-TFTP existing on this model line.
14. WebSearch, "LANCOM LCOS SX 5.x REST API" — 2026-09-10, no REST API evidence found on either SX line.
15. Search synthesis over "Pairing a LANCOM device with the LMC" (knowledgebase.lancom-systems.de/display/KBEN/, 404 on direct fetch, content recovered via search snippet) and `coupling_to_lmc.html` (LCOS reference manual) — 2026-09-10. Serial+PIN, Rollout Assistant (camera scan), activation code, WEBconfig wizard as the four pairing methods; lower confidence than directly-fetched sources since the primary KB page could not be fetched directly.
16. Deploying the AutoWDS base network / Configuring the WLC, https://www.lancom-systems.de/docs/LCOS-Refmanual/9.10-Rel/EN/Referenzhandbuch_7.60_EN/Addendum-900/topics/wlc_autowds_function.html and .../wlc_autowds_tutorial_preconfig_wlc.html — fetched 2026-09-10 (the original `docs/LCOS/Refmanual/EN/topics/...` URLs 404; corrected to the resolving `LCOS-Refmanual/9.10-Rel/.../Addendum-900/` form). Confirms AutoWDS-Rollout-SSID keying and the CAPWAP-loss-to-client-mode fallback verbatim ("A configured AutoWDS AP will automatically function as an unassociated AP after it has failed to establish a CAPWAP connection to a WLC after a predefined time... temporarily switches its operating mode to Client mode and scans each WLAN until it detects a suitable anchor AP").
17. `https://ftp.lancom.de/LANCOM-Releases/` firmware directory listing, fetched 2026-09-10 (directly, not via search) — independent confirmation of the LCOS SX 3.x/4.x vs 5.x split superseding [3]/[14]: `LC-GS-2412` and `LC-GS-3628X` ship firmware `4.30.0439-RU9`; `LC-GS-4530X` (GS-45xx) and `LC-XS-5110F` (XS-series) both ship firmware `5.20.0534-RU12`. GS-45xx does run the 5.x line as claimed.

## Verification notes (2026-09-10)

- **LMC auth mechanism**: independently re-confirmed by grepping `securitySchemes`
  in all 13 vendored specs under `spec/openapi/lancom/lmc-openapi/*.json` — every
  service declares exactly `apiKey` (type `apiKey`, header `Authorization:
  LMC-API-KEY <key>`), `basic` (type `http`/`basic`), and `bearer` (type
  `http`/`bearer`, `bearerFormat: JWT`). No `oauth2`-typed scheme appears in any
  file. `spec/openapi/lancom/SOURCES.md` still states "API calls require an
  OAuth2 token from the `auth` service," which the vendored specs do not
  support — this dossier's characterization (API key/basic/bearer, not OAuth2)
  is correct and `SOURCES.md` is wrong. Per instructions, `SOURCES.md` was not
  edited; this is a repeat/strengthening of the "Known quirks and traps" entry
  above, not a new finding.
- **Two LCOS SX firmware lines**: confirmed directly against LANCOM's firmware
  distribution tree (source 17) rather than only WebSearch synthesis — see the
  updated citation on the "What it manages" claim.
- **`readconfig`/`writeconfig`/`loadscript` command names**: confirmed against
  the LANCOM knowledge base "Useful commands" page (source 10, URL corrected
  below) — same names, same behavior description as in this document.
- **Dead links found and fixed**: source 10's original URL
  (`knowledgebase.lancom-systems.de/spaces/KBEN/pages/36453641/`) and both of
  source 16's original URLs
  (`lancom-systems.de/docs/LCOS/Refmanual/EN/topics/wlc_autowds_*.html`)
  returned HTTP 404; both were replaced with resolving equivalents (verified
  by direct fetch) that carry the same content.
