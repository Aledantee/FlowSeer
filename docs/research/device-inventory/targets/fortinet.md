---
title: Integration target — Fortinet Security Fabric
date: 2026-09-10
scope: FortiGate (FortiOS REST/CLI/SNMP), FortiSwitch, FortiAP, FortiManager,
  FortiAnalyzer, FortiCloud/FortiGate Cloud/FortiLAN Cloud; versions 6.4–8.0
  as documented publicly, fetched 2026-09-10
status: research from public documentation; nothing verified against a live system unless stated
---

# Fortinet Security Fabric as a network-management surface

Every claim carries a source link. Where documentation is contradictory or version-dependent,
say which version the claim holds for. `docs.fortinet.com` is a JavaScript-rendered document
library; WebFetch on those URLs returned only navigation chrome, not article body text, so
claims sourced from a `docs.fortinet.com` page are backed by the indexed snippet text Fortinet
publishes for that exact URL (visible in search results), not a full page fetch. Claims backed
by a page WebFetch actually retrieved in full (readthedocs mirrors, community forum threads,
vendor blog posts) are noted as such.

## What it manages

Fortinet's Security Fabric spans firewalls (FortiGate), switches (FortiSwitch), access points
(FortiAP), and centralized managers (FortiManager, FortiAnalyzer), plus cloud-hosted variants
(FortiGate Cloud, FortiLAN Cloud, FortiCloud IAM/asset management). FortiSwitch and FortiAP are
managed two ways: through a FortiGate via FortiLink (the common deployment, no direct API
surface of their own reachable from outside the fabric) or standalone with their own REST API
and CLI [1][6]. FortiManager centralizes configuration across many FortiGates, organized into
Administrative Domains (ADOMs) and policy packages [7][8]. FortiAnalyzer centralizes logs and
reports, also ADOM-scoped [11][12]. Scale limits (devices per ADOM, per FortiManager instance)
are model-dependent and not published in a single comparable table; unverified: no single scale
ceiling found for either FortiManager or FortiGate REST API concurrent object counts.

## API surface

| Surface | Protocol / transport | Spec available? (OpenAPI/YANG/other, URL) | Versioning scheme |
| --- | --- | --- | --- |
| FortiGate CMDB API | HTTPS REST, `/api/v2/cmdb/<vdom-scoped tree>` | Swagger/OpenAPI viewer inside FNDN (account-gated, fndn.fortinet.net → FortiAPI tab) [3]; no public OpenAPI JSON found outside the portal | Tied to FortiOS release (e.g. 7.4.x, 7.6.x, 8.0.0); CMDB schema changes per release |
| FortiGate Monitor API | HTTPS REST, `/api/v2/monitor/<category>/<endpoint>` | Same FNDN Swagger viewer [3] | Same as CMDB |
| FortiGate Log API | HTTPS REST, `/api/v2/log/<log type>` | Same FNDN Swagger viewer [3] | Same as CMDB |
| FortiGate CLI | SSH / console, `config`/`edit`/`set`/`get`/`show`/`execute` | CLI Reference in the Document Library, per FortiOS version [15] | Per FortiOS release |
| FortiSwitch (FortiLink-managed) | Proxied through FortiGate CMDB (`switch-controller` tree) and `execute switch-controller` CLI on the FortiGate | Same as FortiGate CMDB/CLI [16][17] | Tied to FortiGate/FortiSwitchOS pairing |
| FortiSwitch (standalone) | HTTPS REST, `/api/v2/...` on the switch itself; own CLI | FortiSwitchOS REST API guide, gated behind FNDN [6] | Tied to FortiSwitchOS release (e.g. 6.4.3, 7.0.x) |
| FortiManager | JSON-RPC 2.0-shaped, single `/jsonrpc` endpoint | No OpenAPI; JSON-RPC method reference in FNDN, Swagger viewer added for FNDN API tool [4][7] | Tied to FortiManager release |
| FortiAnalyzer | JSON-RPC 2.0-shaped, single `/jsonrpc` endpoint | No OpenAPI; community-maintained doc project covers 108 operations against 7.4.8/7.6.4/8.0.0 [11] | Tied to FortiAnalyzer release |
| FortiCloud IAM / asset management / FortiGate Cloud | HTTPS REST + OAuth2 password grant via `customerapiauth.fortinet.com` | Per-service API docs behind FortiCloud IAM portal [13][14] | Per cloud service, not FortiOS-aligned |
| SNMP | SNMPv1/v2c/v3 agent on FortiGate | `FORTINET-CORE-MIB`, `FORTINET-FORTIGATE-MIB`, plus standard MIB-II; downloaded from the Fortinet Support Portal per firmware version [18][19] | Tied to FortiOS release |

No vendored OpenAPI/YANG spec for any Fortinet product exists yet under `spec/openapi/` in this
repository (checked `spec/openapi/*/SOURCES.md`: only ubiquiti, hp, mikrotik, lancom, and
ruckus/vsz are present) and nothing under `docs/research/network-domain-atlas/vendors/` either.

## Authentication and authorisation

**FortiGate REST API.** Two mechanisms: session-cookie login (username/password against
`/logincheck`, CSRF token echoed back and required on subsequent state-changing calls) and
bearer-token auth via a dedicated **REST API Admin** account [2][5]. A REST API Admin is created
under System > Administrators, is bound to an admin profile that scopes read/write per feature
area, and can be restricted to specific trusted source IP ranges (PKI/certificate binding is also
supported per the admin guide's REST API administrator page, though WebFetch could not confirm
exact field names) [5]. The API token is shown once at creation time and must be stored by the
caller [2]. Config backup specifically needs a profile with full `super_admin` access, per a
Fortinet-staff-authored community Technical Tip (author badge confirmed "Staff", so this is
vendor guidance despite being published on the community forum rather than the admin guide)
[10]. unverified: that Tip does not itself discuss a `super_admin_readonly` profile or state it
is insufficient — no source found says so explicitly.

**FortiManager / FortiAnalyzer JSON-RPC.** Session-based: `exec` a login call to
`/sys/login/user` with username/password, get back a `session` string, and pass it in the
`session` field of every subsequent JSON-RPC call body until logout or idle timeout [7][11][12].

**FortiCloud IAM (covers FortiGate Cloud, FortiLAN Cloud, and other Fortinet cloud services).**
OAuth2 password grant against `https://customerapiauth.fortinet.com/api/v1/oauth/token/`. An
"IAM API user" is created in the FortiCloud portal, and its credentials (API user ID, encrypted
password, and a `client_id` naming the target service, e.g. `iam`, `assetmanagement`,
`FortiManager`, `FortiAnalyzer`) are downloaded once as a password-protected file [13][14]. The
token response returns `access_token`, `token_type: Bearer`, `expires_in` (shown as 3660 seconds
in the example response on the vendor's own accessing-fortiapis page, fetched in full — unverified
as a hard constant across all services since only one worked example was seen), `refresh_token`,
and `scope` [13]. Later calls to the actual service API (e.g. FortiGate Cloud's
own REST endpoints) carry `Authorization: Bearer <access_token>` [13][14].

A read-only inventory integration needs a REST API Admin (FortiGate) or FortiCloud IAM API user
scoped to view-only, plus, for FortiManager/FortiAnalyzer, a session login with a read-only ADOM
role. A write integration additionally needs `super_admin`-equivalent scope for config push,
device-DB writes and `installdevice` calls, and workspace lock/unlock rights on the target ADOM.

## Data model

**FortiGate.** VDOMs are the tenancy/scoping unit inside one physical or virtual appliance. Every
CMDB and monitor call accepts a `vdom` query parameter: `vdom=root` for one VDOM, a
comma-separated list for several, or `vdom=*` for all VDOMs the admin can see; omitting it
defaults to the management VDOM, and requesting a VDOM the admin profile can't reach returns a
permission error [9]. Serial number is the stable per-device identifier surfaced across FortiGate,
FortiSwitch, and FortiAP; FortiCloud registration and FortiDeploy both key off it [20][21].

Configuration is declarative CLI-tree style: CMDB objects map 1:1 onto `config`/`edit` blocks in
the CLI, addressed as REST paths like `/api/v2/cmdb/firewall/policy`. The `action` query parameter
changes GET semantics on a CMDB path — `action=default` returns default values for a new object,
`action=schema` returns the object's meta/schema instead of instances [9]. The `filter` parameter
filters returned CMDB objects by one or more conditions; `start` and `count` page GET results
(e.g. `start=1&count=10`) [9]. FortiOS 7.4.1 added transaction support to the CMDB API: changes
made under an `X-TRANSACTION-ID` header accumulate uncommitted, can be inspected with a
`transaction-show` action, and are applied atomically with `action=transaction-commit` against
the transaction id, or discarded — WebFetch could not retrieve the full parameter grammar from
the vendor page, so treat the header name and action names as unverified: sourced from a search
snippet quoting the doc, not a full fetch [22].

Config backup is a monitor-API GET, not a CMDB action:
`GET /api/v2/monitor/system/config/backup/?scope=global&access_token=<token>` streams the raw CLI
configuration file [10]. The CLI equivalent is `execute backup config <destination> <comment>`
(e.g. `execute backup config management-station <comment>` to push to FortiManager, or a
TFTP/USB/flash destination) [23]. Full-configuration CLI dump for inspection or diffing is
`show full-configuration` (undocumented in the pages fetched here beyond its name; treat as
common knowledge from FortiOS CLI conventions, not independently sourced this session).

**FortiManager.** Two configuration layers per managed device: the **device DB** (FortiManager's
own copy of the device's config, editable offline) and **live config** on the device itself;
changes must be installed (pushed) to reconcile them [7][8]. Workspace mode, when enabled on an
ADOM, requires locking the ADOM, a device, or a policy package before any change, then committing
and unlocking; unverified: neither cited page [8] states in the fetched content that the lock
must be taken and released within the same login session — the `pyfmg` client [7] only implies
this by releasing any held lock on logout, not by stating a hard same-session rule. Rolling a
device's config out uses an `exec` call against `securityconsole/install/device` (commonly called
`installdevice` in tooling and forum threads) [7][8]. ADOMs are the multi-tenancy/grouping unit,
analogous to VDOMs but at the manager level.

**FortiAnalyzer.** ADOM-scoped log search and reporting: paths like
`/logview/adom/{adom}/logsearch` and `/report/adom/{adom}/run`, `root` being the default/common
ADOM [12]. Reports run by referencing a report layout ID and starting a run task, polled for
completion [11].

Read-after-write consistency: unverified: no vendor statement found on propagation delay between
a FortiManager device-DB write and the live device reflecting it beyond "install after commit";
same for FortiLink-managed switch-controller object writes on FortiGate reaching the physical
FortiSwitch.

## Telemetry and events

FortiGate's monitor API exposes point-in-time state: interface counters, session tables, VPN
tunnel status, HA status, and (relevant to this project) switch-controller managed-switch health
under `/api/v2/monitor/switch-controller/managed-switch` when FortiLink is active [16]. No
webhook push mechanism was found in the pages reachable this session; push telemetry is via
syslog, SNMP traps, and NetFlow/sFlow configured on the device, not a REST subscription — the
searches run this session did not surface a FortiGate webhook API, so treat "no webhook support"
as based on absence of evidence rather than a vendor statement ruling it out.

FortiAnalyzer is the log/event sink of record for the fabric (syslog and the proprietary
FortiGate-to-FortiAnalyzer OFTP-like log forwarding protocol); its JSON-RPC API is for querying
already-ingested logs and running reports, not for real-time event push to a third party [11][12].

## Rate limits, quotas, pagination

Fortinet does not appear to publish a formal numeric rate limit for the FortiGate REST API in the
administration guide pages searched this session. A community support-forum thread (fetched in
full) states FortiGate returns HTTP 429 "Too Many Requests" when exceeded, and a community member
(forum rank "Explorer", no Fortinet staff badge) cited "typical default caps" of roughly 100
GET/monitor calls per second
and roughly 30 configuration-write calls per second, calling the limits fixed and non-configurable
[24]. Treat these two numbers as unverified: they come from a community forum reply, not an
official Fortinet document. The documented workarounds from that thread: batch reads instead of
per-object polling loops, reuse one session or API key instead of opening many, and stagger
multiple independent pollers so they don't collide [24].

CMDB GET pagination uses `start` and `count` query parameters; unverified: the `fortigate-api`
readthedocs page [9] confirms only the `filter` parameter by direct fetch — `vdom`, `action`,
`start`, and `count` came from a search snippet quoting FortiOS doc text, not a full-page fetch.
FortiCloud's OAuth `access_token` expiry is shown as 3660 seconds in the
vendor's own example response [13]; refresh via a
`grant_type=refresh_token` call to the same token endpoint [13]. FortiManager/FortiAnalyzer
session idle timeout is configurable server-side; no default value confirmed this session.

## Device-side protocols still available

SNMP remains fully available in parallel with the REST/JSON-RPC APIs on FortiGate, FortiSwitch,
and FortiAP; it is not disabled by enrolling a device into FortiManager or FortiLink management.
unverified: partial SNMPv3 support (RFC 3411 architecture, RFC 3414 User-based Security Model) was
claimed for FortiOS in this dossier, but source [18] (`help.fortinet.com`) is a dead link — the
hostname no longer resolves — and no replacement source was found this session confirming the RFC
numbers; polling via the vendor MIBs `FORTINET-CORE-MIB` and `FORTINET-FORTIGATE-MIB`
plus standard MIB-II, all registered under Fortinet's IANA enterprise number 12356 [19]. MIB files
ship per firmware version from the Fortinet Support Portal, not from a public MIB repository
[19]. SSH CLI access is always available alongside the REST API and is the only interface for a
few operations (e.g. `execute backup config` to non-HTTP destinations, `show full-configuration`).
syslog and NetFlow/sFlow export are standard FortiGate features, independent of API/management
mode.

## Provisioning and onboarding

**FortiLink (FortiSwitch/FortiAP auto-discovery).** Plugging a FortiSwitch into one of the
FortiGate's designated FortiLink-capable ports establishes the FortiLink connection with no
configuration needed on the switch side, and minimal configuration on the FortiGate [17]. Once
joined, the switch appears as a `switch-controller managed-switch` CMDB object, addressable and
inspectable through the FortiGate's own REST API and CLI (`execute switch-controller ...`) rather
than any API of its own [16][17].

**FortiDeploy / ZTP.** A FortiGate (or FortiAP) authenticates to FortiCloud using its own serial
number at first boot/internet contact, and FortiCloud hands back the location of the assigned
FortiManager (or the pre-staged configuration template) based on which account that serial was
registered to [20]. Low-touch provisioning (LTP) has the device establish a secure management
tunnel to FortiManager and authenticate with its serial number or a pre-shared key during that
"Auto-Link" process [20]. Registering a device against an account is done by adding its serial (or
a FortiCare/FortiCloud "cloud key") to the FortiCloud or FortiDeploy portal before the device ever
calls home [20][21].

Factory reset and firmware management are CLI/GUI operations (`execute factory-reset`-class
commands, firmware upload via GUI or CLI `execute restore image`); unverified: no REST API
endpoint for firmware push was found in the sources reachable this session, distinct from
FortiManager's own firmware management workflow, which does have `exec` calls for firmware
upgrade but were not directly confirmed here.

## Known quirks and traps

- `docs.fortinet.com` renders its article bodies client-side; naive HTTP fetches (and likely any
  scraper without a JS-capable fetcher) get only navigation chrome. Any automated docs-sync
  tooling FlowSeer builds against Fortinet's document library needs a JS-rendering fetch path or
  should prefer FNDN's Swagger export instead.
- The FNDN developer portal (Swagger/OpenAPI viewer, FortiSwitch REST API guide, JSON-RPC method
  references) requires its own account and is not openly crawlable [3][6]; a full machine-readable
  spec cannot be vendored into this repo without that account.
- The config-backup monitor endpoint needs full `super_admin`, per Fortinet-staff-authored
  guidance [10]. unverified: whether a narrower read-only profile such as `super_admin_readonly`
  is specifically insufficient — no source found states this. If it is, the "least privilege for
  inventory reads" story on FortiGate would not extend cleanly to config backup.
- Rate limit numbers found for the FortiGate REST API come from a single community forum reply,
  not an official document; do not hardcode 100 req/s or 30 writes/s as guaranteed limits without
  independent confirmation from a FortiOS release note or the FNDN reference.
- FortiSwitch and FortiAP have two entirely different API surfaces depending on management mode
  (FortiLink-managed vs standalone), and a device can only be in one mode at a time; an inventory
  integration needs to detect which mode a given switch/AP is in before deciding which surface to
  poll.
- unverified: FortiManager's workspace-mode locking is commonly assumed per-session (the `pyfmg`
  client [7] releases any held lock on logout, consistent with this), but no fetched source states
  outright that the lock must be released using the same login session that acquired it. If true,
  a poorly designed integration that opens a new session per call could deadlock itself out of its
  own lock.

## What FlowSeer needs

- Credentials to store per FortiGate: a REST API Admin token (or username/password if session-auth
  is chosen) scoped to at least `super_admin_readonly` for inventory/monitor reads, and
  `super_admin` if config backup via `/api/v2/monitor/system/config/backup` is required [10].
- Credentials to store per FortiManager/FortiAnalyzer: a service-account username/password for
  JSON-RPC session login, with read-only ADOM role for inventory/log queries and a write role plus
  workspace-lock rights only if FlowSeer will push config or run `installdevice` [7][8].
- Credentials to store for FortiCloud-hosted services (FortiGate Cloud, FortiLAN Cloud): an IAM
  API user's downloaded credential bundle (API user ID, password, `client_id`) for the OAuth
  password grant against `customerapiauth.fortinet.com` [13][14].
- Endpoints to call for inventory: FortiGate CMDB tree under `/api/v2/cmdb/system/*` and
  `/api/v2/cmdb/switch-controller/managed-switch` for FortiLink-managed switches; FortiManager
  device DB for centrally managed fleets; FortiCloud/FortiDeploy portal APIs for serial-based
  claim status.
- Endpoints to call for config: FortiGate CMDB PUT/POST with `vdom` and, for multi-step changes,
  the `X-TRANSACTION-ID` transaction flow; FortiManager device-DB edit plus `installdevice` push.
- Endpoints to call for telemetry: FortiGate monitor API for polled state (interfaces, sessions,
  managed-switch health); SNMPv3 against `FORTINET-FORTIGATE-MIB`/`FORTINET-CORE-MIB` as the
  vendor-independent fallback; syslog/NetFlow/sFlow for event and flow data.
- Identifier to key on: device serial number, stable across FortiGate, FortiSwitch, FortiAP,
  FortiCloud, and FortiDeploy/ZTP records [20][21].
- Rate-limit budget: treat FortiGate REST calls as capped in the tens of requests per second for
  writes and roughly 100/s for monitor reads pending official confirmation; poll via batched
  table reads, not per-object loops, and reuse a single session/token per FortiGate [24].
- Minimum API version to target: FortiOS 7.4.1+ if the transaction/batch CMDB flow is required;
  otherwise no version floor found in sources reached this session beyond "REST API generally
  available across the FortiOS 6.x/7.x/8.0 line" implied by the per-version doc pages found for
  6.4, 7.2, 7.4, 7.6, and 8.0.0 [2][5][9][22].

## Sources

1. https://support.auvik.com/hc/en-us/articles/360056175532-How-do-I-monitor-a-FortiSwitch-in-FortiLink-mode — search-snippet only (WebFetch returned HTTP 403); practitioner note on FortiLink switch visibility via FortiGate REST API. Fetched 2026-09-10.
2. https://docs.fortinet.com/document/fortigate/7.6.5/administration-guide/399023/rest-api-administrator — REST API Admin token creation, shown once. Snippet-sourced (full fetch returned nav chrome only). Fetched 2026-09-10.
3. https://docs.fortinet.com/document/fortigate/7.6.2/administration-guide/940602/using-apis and https://docs.fortinet.com/document/fortimanager/6.2.0/new-features/876929/swagger-support-for-fndn-api-tool — FNDN Swagger/OpenAPI viewer location, account-gated. Snippet-sourced. Fetched 2026-09-10.
4. https://docs.fortinet.com/document/fortimanager/6.2.0/new-features/876929/swagger-support-for-fndn-api-tool — same as [3], FortiManager-specific Swagger note.
5. https://docs.fortinet.com/document/fortigate/7.6.5/administration-guide/399023/rest-api-administrator — admin profile scoping, trusted hosts for REST API Admin. Snippet-sourced. Fetched 2026-09-10.
6. https://docs.fortinet.com/document/fortiswitch/6.4.3/fortiswitch-rest-api and https://fndn.fortinet.net/index.php?/documents/file/267-fortiswitchos-rest-api-guide — standalone FortiSwitch REST API location; full guide is FNDN-gated. Fetched 2026-09-10 (WebFetch on docs.fortinet.com page returned nav chrome only).
7. https://github.com/p4r4n0y1ng/pyfmg and community threads it cites — FortiManager JSON-RPC session/workspace behavior, `installdevice`/`securityconsole/install/device`. Snippet-sourced. Fetched 2026-09-10.
8. https://community.fortinet.com/fortimanager-27/technical-tip-how-to-lock-unlock-an-adom-and-commit-changes-in-fortimanager-using-the-json-api-208549 and https://registry.terraform.io/providers/fortinetdev/fortimanager/latest/docs/guides/fmg_wksnormal — workspace mode lock/commit/unlock sequencing, same-session lock requirement. Snippet-sourced. Fetched 2026-09-10.
9. Search snippets over https://fortigate-api.readthedocs.io and Fortinet doc titles — `vdom`, `filter`, `action`, `start`/`count` CMDB query parameters. fortigate-api readthedocs page fetched in full 2026-09-10 (confirmed CMDB/Monitor/Log API split for FortiOS 6.4.14); parameter semantics confirmed via search snippet quoting FortiOS doc text.
10. https://community.fortinet.com/t5/FortiGate/Technical-Tip-Get-backup-config-file-on-FortiGate-using-RestAPI/ta-p/202286 — `/api/v2/monitor/system/config/backup/` endpoint and `super_admin` requirement; author (Demir21) carries the "Staff" badge, i.e. Fortinet-authored guidance published on the community site. Fetched in full 2026-09-10 (confirmed by direct fetch, not just snippet).
11. https://how-to-fortianalyzer-api.readthedocs.io/en/latest/getting-started/index.html — FortiAnalyzer JSON-RPC base URL, `/sys/login/user`, session field, versions 7.4.0+. Fetched in full 2026-09-10.
12. https://github.com/rstierli/fortianalyzer-api-postman and https://networkengineerhub.com/knowledge-base/fortianalyzer-json-rpc-api — ADOM-scoped log/report paths. Snippet-sourced. Fetched 2026-09-10.
13. https://docs.fortinet.com/document/forticloud/latest/identity-access-management-iam/19322/accessing-fortiapis — FortiCloud IAM OAuth token endpoint, request/response fields, refresh flow. Fetched in full 2026-09-10.
14. https://docs.fortinet.com/document/forticloud/26.1.0/identity-access-management-iam/282341/adding-an-api-user — IAM API user creation, credential bundle download. Snippet-sourced. Fetched 2026-09-10.
15. https://docs.fortinet.com/document/fortigate/8.0.0/cli-reference/229031989/execute-backup — CLI reference existence for `execute backup`. Snippet-sourced. Fetched 2026-09-10.
16. https://docs.fortinet.com/document/fortigate/6.2.1/cli-reference/174620/switch-controller-managed-switch — `switch-controller managed-switch` CMDB/CLI object. Snippet-sourced. Fetched 2026-09-10.
17. https://docs.fortinet.com/document/fortiswitch/7.0.8/devices-managed-by-fortios/173260/configuring-fortilink and https://docs.fortinet.com/document/fortigate/7.6.4/cli-reference/324047635/execute-switch-controller — FortiLink auto-discovery, no switch-side config needed, `execute switch-controller` CLI. Snippet-sourced. Fetched 2026-09-10.
18. https://help.fortinet.com/fmgr/50hlp/56/5-6-1/FMG-FAZ/2400_System_Settings/2400_Advanced/0220_SNMP%20MIBs.htm — DEAD LINK: `help.fortinet.com` no longer resolves (DNS lookup fails as of 2026-09-10). Originally cited for SNMPv3 RFC support (3411, partial 3414); claim is now unverified and no working replacement was found this session.
19. https://mibs.observium.org/mib/FORTINET-FORTIGATE-MIB/ and https://mibs.observium.org/mib/FORTINET-CORE-MIB/ — MIB names, Fortinet IANA enterprise number 12356, per-firmware MIB download from Support Portal. Snippet-sourced. Fetched 2026-09-10.
20. https://community.fortinet.com/fortigate-3/technical-tip-zero-touch-provisioning-of-fortigate-using-fortideploy-99548 and https://www.historiantech.com/zeroish-touch-provisioning-with-fortimanager-explained/ — FortiDeploy/LTP serial-number-based auto-link to FortiManager. Snippet-sourced. Fetched 2026-09-10.
21. https://docs.fortinet.com/document/fortigate/6.4.0/administration-guide/316039/zero-touch-provisioning-with-fortideploy — serial/cloud-key registration in FortiCloud portal before device claim. Snippet-sourced. Fetched 2026-09-10.
22. https://docs.fortinet.com/document/fortigate/7.4.0/new-features/174532/view-batch-transaction-commands-through-the-rest-api-7-4-1 — FortiOS 7.4.1 CMDB transaction support, `X-TRANSACTION-ID`, transaction-show/commit actions. Snippet-sourced (full fetch returned nav chrome only); parameter names unverified against a full page read.
23. https://community.fortinet.com/t5/FortiGate/Technical-Tip-Get-backup-config-file-on-FortiGate-using-RestAPI/ta-p/202286 and general CLI reference titles — `execute backup config` CLI syntax. Snippet-sourced. Fetched 2026-09-10.
24. https://community.fortinet.com/support-forum-92/429-status-code-with-message-too-many-request-208615 — HTTP 429 behavior and community-cited (not official) rate-limit figures of ~100 monitor reads/s and ~30 config writes/s, throttling guidance. Fetched in full 2026-09-10.
