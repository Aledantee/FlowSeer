---
title: Integration target — Cisco Meraki Dashboard API
date: 2026-09-10
scope: Cisco Meraki cloud-managed portfolio (MS switches, MR access points, MX
  security appliances, MG cellular gateways, MV cameras, MT sensors) plus the
  Cloud Monitoring / Cloud Management bridge for Catalyst 9000 IOS-XE
  switches. Dashboard API v1 (OpenAPI v3), fetched 2026-09-10.
status: research from public documentation; nothing verified against a live system unless stated
---

# Cisco Meraki Dashboard API

Every claim carries a source link. Where documentation is contradictory or version-dependent,
say which version the claim holds for.

Nothing for Meraki is vendored yet under `spec/openapi/` — no `SOURCES.md`
directory exists for it, and there is no `docs/research/network-domain-atlas/vendors/`
entry either (checked 2026-09-10, both absent).

## What it manages

Meraki is cloud-managed end to end: every device phones home to the Meraki
cloud (dashboard.meraki.com and regional equivalents) over an outbound tunnel,
and almost all configuration and monitoring goes through that cloud, not the
device directly [1]. Product families: MS (switches), MR (access points), MX
(security/SD-WAN appliances), MG (cellular gateways), MV (cameras), MT
(environmental sensors), plus Systems Manager (MDM) and, since 2023, Catalyst
9000 IOS-XE switches bridged in under "Cloud Monitoring" / "Cloud Management"
(see below) [1][11].

Topology is strictly cloud-managed — there is no controller-less or
standalone mode comparable to Aruba Instant or UniFi's local-controller
option. Organization → network → device is the only hierarchy; a network is
the unit that groups devices sharing one configuration and one set of
enabled product types (switch, wireless, appliance, camera, sensor,
cellularGateway) [2]. unverified: hard scale limits (max devices per
network/org) — not stated in the fetched pages.

## API surface

| Surface | Protocol / transport | Spec available? | Versioning scheme |
| --- | --- | --- | --- |
| Dashboard API | REST over HTTPS, JSON | OpenAPI v3 (`spec3.json`) and legacy v2, published at `https://github.com/meraki/openapi` [3]; also fetchable per-organization via `GET /organizations/{organizationId}/openapiSpec` [4] | URL-versioned (`/api/v0` deprecated, `/api/v1` current); early-access endpoints ship unversioned ahead of GA, see below [5] |
| Webhooks (HTTP servers) | Outbound HTTPS POST from Meraki cloud to a receiver you host | JSON payload documented per alert type; no formal schema file found | Alert-type catalog changes independently of the REST API version [6] |
| SNMP | UDP, polled from the Dashboard (cloud) or directly against a device on the LAN | Meraki-specific MIB, not vendored here | v1/v2c/v3, dashboard-side and device-side are configured separately [7] |
| Local device HTTP | HTTPS to the device's own IP, digest auth | No API — a read-only/limited-config status page, not a programmatic surface | N/A [8] |

Base URL for most regions: `https://api.meraki.com/api/v1`. Some regions
(documented as region-specific dashboards, e.g. China) use a different base
URI — the fetched page states this without listing every regional host [1].

## Authentication and authorisation

- **API key.** Created in the dashboard under `Organization > API & Webhooks
  > API keys and access`. The current Dashboard API v1 standard is the
  `Authorization: Bearer {API_KEY}` header — the authorization docs page
  explicitly says to check for this "and not v0's `X-Cisco-Meraki-API-Key`"
  [9]. The v0-era `X-Cisco-Meraki-API-Key` header name is still defined as
  an accepted `apiKey` security scheme in the current OpenAPI v3 spec
  (`spec3.json`) alongside `bearerAuth`, so it likely still works, but the
  docs no longer recommend it [3][9]. Each admin identity can hold up to two
  valid keys at once [9].
- **Scope inheritance, not independent scoping.** A key has exactly the
  permissions of the admin identity that created it, per organization. An
  admin who is a full org admin in Org1 gets full-org-admin API access to
  Org1; the same key against Org2 carries whatever role that identity holds
  there. There is no separate "API scope" narrower than the admin's
  dashboard role [9].
- **Lifetime.** Keys are permanent until revoked — no built-in expiry.
  Admins can revoke and regenerate their own key [9].
- **Per-user, multi-org.** One key belongs to one admin identity and works
  across every organization that identity administers, at whatever role each
  org grants [9].
- **OAuth 2.0.** Supported as an alternative to API keys, with app-scoped
  access configured at the organization level, permissions configurable per
  application, and 60-minute access tokens with auto-refresh [9]. unverified:
  exact grant type(s) (authorization code vs client credentials) — not shown
  in the fetched excerpt.
- **Least-privilege read-only integration:** create a dedicated dashboard
  admin with read-only organization access and mint its API key, rather than
  reusing a full-admin key — the key can only be as narrow as the admin
  account backing it [9].

## Data model

- **Hierarchy:** organization → network → device. A device belongs to
  exactly one network at a time; unclaimed/unassigned devices sit in the
  organization's inventory without being in any network [1][10].
- **Identifiers:** device serial number (Meraki's stable, human-visible
  identifier, printed on the unit and used to claim it) is the primary key
  for device-level API calls; networks and organizations have Meraki-assigned
  string IDs (e.g. `N_...`, org ID as a numeric string) [10]. MAC address is
  exposed as device metadata but serial is what claim/inventory endpoints key
  on [10].
- **Inventory read:** `GET /organizations/{organizationId}/inventoryDevices`
  and `GET /organizations/{organizationId}/devices` list claimed hardware;
  paginated per the pagination scheme below [2]. unverified: full field list
  per device — not fetched in this pass.
- **Configuration model:** two layers. Per-network configuration is direct
  and immediate. Configuration templates let many networks bind to one
  shared base configuration (`bindNetwork` attaches a network to a template);
  a bound network inherits template settings and can override certain
  fields locally, which is the closest Meraki gets to a profile/declarative
  layer [12][13]. There is no dry-run/validate-only mode surfaced in the
  fetched pages; unverified: whether any endpoints support a preview-only
  write.
- **Bulk/atomic writes — action batches.** `POST
  /organizations/{organizationId}/actionBatches` groups multiple write
  actions (create/update/destroy) into one batch executed atomically, all or
  nothing. Synchronous batches cap at 20 actions and block until done;
  asynchronous batches cap at 100 actions and return immediately while the
  batch runs in the background. At most 5 concurrent batches per
  organization regardless of mode. A batch can be submitted unconfirmed
  (`confirmed: false`) for preview — it is stored and auto-deleted after one
  week if never confirmed; once `confirmed: true` it executes and can no
  longer be deleted [14].
- **Read-after-write / propagation:** not stated in the fetched pages for
  general config pushes; webhooks and dashboard reporting note their own
  propagation delay (see Telemetry below). unverified: general
  config-push-to-device propagation latency.

## Telemetry and events

- **Polling:** per-network and per-device monitoring endpoints exist for
  status, connected clients, and interface/uplink statistics (e.g. network
  Bluetooth clients, appliance client security events were the paginated
  examples surfaced by the docs) [15]. unverified: full catalog of
  monitoring endpoints and their native polling granularity — not
  enumerated in the fetched pages.
- **Push — webhooks (HTTP servers).** Configured network-wide (Network-wide
  → Alerts) or via the alert-configuration API; any configured HTTP server
  can be a destination for any alert type. Payload is JSON with common
  fields `alertId`, `alertType`, `alertData` (alert-type-specific object),
  `networkId`, `networkName`, `organizationId`, `occurredAt`, `sentAt`.
  HTTPS with a valid (non-self-signed) certificate is required on the
  receiver. An optional `sharedSecret` field lets the receiver validate the
  sender. Alerts typically arrive within 90 seconds of the triggering event,
  though some alert types have configurable thresholds adding up to 10
  minutes of delay — hence `occurredAt` and `sentAt` can diverge [6].
- **Retry semantics:** Meraki does not document a fixed retry count/backoff
  schedule in the fetched page; it states that a receiver is auto-disabled
  after delivery has failed for more than 100 attempts within 24 hours, and
  that transient failures alone do not trigger disablement. Re-enabling
  requires deleting and recreating the webhook, including a successful test
  delivery [6]. unverified: the per-attempt retry interval.
- **SNMP:** dashboard-side polling of the SNMP proxy the Dashboard itself
  exposes (all traffic to the cloud, no LAN access needed) versus per-device
  local SNMP (traffic stays on the LAN, device answers directly) are
  configured separately — dashboard SNMP under Organization-wide settings,
  local/device SNMP under Network-wide > General > SNMP. Both support
  v1/v2c or v3; Cisco recommends v3 with AES128 privacy [7].
- **Syslog:** devices can also stream events to a syslog server, configured
  alongside SNMP under network-wide reporting settings [16]. unverified:
  syslog message format/RFC compliance — not fetched.

## Rate limits, quotas, pagination

| Scope | Limit | Source |
| --- | --- | --- |
| Per organization (steady state) | 10 requests/second, shared across every application using that org's key(s) | [17] |
| Per organization (burst) | +10 extra requests in the first second — up to 30 requests across any 2-second window | [17] |
| Per source IP | 100 requests/second, shared across every client from that IP | [17] |
| Over limit | HTTP 429, body `{"errors": ["API rate limit exceeded for organization"]}`, with a `Retry-After` header stating the wait in seconds | [17] |

Meraki's own guidance: read `Retry-After` and back off rather than
guessing; requests bursting past 30/2s or 100/s-per-IP get throttled
regardless of client count [17].

**Pagination:** RFC 5988 (Web Linking) via the `Link` response header on
paginated GET endpoints, carrying up to four comma-separated relations
(`first`, `prev`, `next`, `last`), each a full request URL with the
appropriate `perPage`/`startingAfter`/`endingBefore` values filled in.
Clients page with `perPage`, `startingAfter`, and `endingBefore` query
parameters; the cursor value (`startingAfter`/`endingBefore`) is either a
timestamp or an integer ID depending on the endpoint [18]. Default and
maximum `perPage` are set per endpoint, not globally: `GET
/organizations/{organizationId}/devices` documents "Acceptable range is 3 -
5000. Default is 1000," while `GET
/organizations/{organizationId}/inventoryDevices` documents "Acceptable
range is 3 - 1000. Default is 1000" — same default, different ceiling [23][24].

## Device-side protocols still available

- **Local status page.** Every Meraki device serves an HTTPS status page
  from its own IP, protected by digest authentication (MD5-based digest
  between the browser and the device). It shows live status and a small set
  of local overrides; it is a human UI, not an API [8].
- **SNMP.** Available two ways: (a) dashboard/cloud SNMP, where the Meraki
  cloud is the SNMP agent an NMS polls, so no LAN reachability to the device
  is required; (b) local/device SNMP, where each device answers SNMP
  directly on the LAN. Both configured independently, v1/v2c/v3 supported on
  each path [7].
- **SSH.** None. Meraki devices do not expose an SSH CLI; there is no
  documented CLI-based management path for MS/MR/MX/MG/MV/MT hardware [1][8].
- **NETCONF/RESTCONF/gNMI.** Not present on native Meraki devices. NETCONF
  does appear, but only as the transport inside the encrypted tunnel a
  bridged Catalyst 9000 switch uses to talk to the Meraki cloud under Cloud
  Monitoring/Cloud Management — it is not exposed to the operator [19].

## Provisioning and onboarding

- **Claim by serial number.** Devices are claimed into the organization's
  inventory (or directly into a network) by entering their 12-digit serial
  number — printed on the device and also on a scannable barcode — one per
  line in the dashboard, or via the API (`claimIntoOrganizationInventory`,
  `claimNetworkDevices`) [10][20].
- **Claim by order number.** An order number claims every device and
  license on that order in one step, but only while none of the order's
  devices have already been claimed individually; once partially claimed,
  the remaining devices must go in by serial number [20].
- **Inventory vs network.** A claimed-but-unassigned device sits in
  organization inventory; assigning it to a network is a separate step
  (`claimNetworkDevices` or the dashboard's "Add devices" flow) [10][20].
  There is no separate "adoption" handshake beyond claim + network
  assignment — once claimed and network-assigned, a device that can reach
  the internet establishes its cloud tunnel and pulls configuration
  automatically (Meraki's stated cloud-managed, zero-touch model) [1].
- **Factory reset:** unverified — not covered in the fetched pages.
- **Firmware:** managed per-network from the dashboard (firmware upgrade
  scheduling under network-wide settings); unverified: whether firmware
  version is independently settable per device versus network-wide only —
  not fetched in this pass.

## Cloud Monitoring / Cloud Management for Catalyst (IOS-XE bridge)

Two distinct, sequential features bridge Catalyst 9000 IOS-XE switches
(9200/9200L/9200CX, 9300/9300L/9300X, 9500) into the Meraki dashboard
without replacing the switch's native IOS-XE control plane:

- **Cloud Monitoring for Catalyst** (the original feature): a Meraki-provided
  onboarding application configures the switch and opens an encrypted
  tunnel using NETCONF as the in-tunnel transport to stream status,
  configuration, and troubleshooting data to the dashboard. Onboarded
  switches show in the dashboard tagged "Monitor Only" and look and behave
  much like native MS switches for viewing purposes, but the dashboard has
  read-only access — configuration and management stay wherever they were
  before (IOS-XE CLI, DNA Center, etc.) [11][19].
  - Requires an active DNA Essentials license for baseline monitoring; DNA
    Advantage is required for client-level analytics [11].
  - **End of service: March 31, 2026** (extended from an original January
    31, 2026 date) — this is before today's fetch date (2026-09-10), so
    Cloud Monitoring for Catalyst is now decommissioned. Switches never
    migrated off it show as offline in the dashboard and require manual
    removal of the onboarding-application configuration; device
    configuration itself is untouched, only cloud visibility is lost [21].
- **Cloud Management with IOS-XE / "cloud management with device
  configuration"** is the replacement: a native architecture (no separate
  onboarding application) that additionally allows configuration changes
  from the dashboard, not just monitoring, requiring IOS-XE 17.15.3+ and a
  re-onboarding step for switches migrating off the legacy Cloud Monitoring
  path [21].

For FlowSeer, treat "Cloud Monitoring for Catalyst" as a dead feature as of
this research date and target Cloud Management with IOS-XE (device
configuration mode) if Catalyst-via-Meraki integration is ever wanted;
unverified: whether Cloud Management with IOS-XE exposes the same Dashboard
API surface (devices/networks endpoints) as native MS hardware, or a
separate endpoint family — not fetched in this pass.

## Known quirks and traps

- Rate limits are per-organization, not per-key: every application sharing
  one org's credentials shares the same 10 req/s budget, so a multi-tenant
  integration must track budget per organization, not per API key [17].
- API key scope is inherited from the admin account, not independently
  configurable — the only way to get a narrower key is to create a narrower
  admin account first [9].
- Confirmed action batches cannot be deleted or rolled back; unconfirmed
  ones silently expire after one week if never confirmed [14].
- Webhook delivery has no documented fixed retry schedule, only an
  aggregate 100-failures/24h auto-disable threshold, and re-enabling after
  disable requires deleting and recreating the webhook (not just retrying)
  [6].
- Cloud Monitoring for Catalyst — the older, monitoring-only Catalyst
  bridge — reached end of service March 31, 2026; any integration research
  or prior notes referencing it need to be re-pointed at Cloud Management
  with IOS-XE [21].
- Order-number claiming breaks as soon as any device on that order has been
  claimed individually, forcing a fallback to per-serial claiming with no
  batch alternative documented [20].

## What FlowSeer needs

- **Credentials to store:** one Dashboard API key per integration admin
  account (per-org effective scope follows the admin's role in each org);
  consider a dedicated read-only admin per organization to bound blast
  radius, since key scope cannot be narrowed independently [9].
- **Inventory endpoints:** `GET /organizations/{organizationId}/devices` and
  `GET /organizations/{organizationId}/inventoryDevices` for claimed
  hardware, plus `GET /organizations/{organizationId}/networks` for the
  network layer; page with `perPage`/`startingAfter` and follow the `Link`
  header rather than assuming endpoint-specific defaults [2][18].
- **Config endpoints:** per-network config calls for direct changes; action
  batches (`POST /organizations/{organizationId}/actionBatches`) for
  multi-device atomic writes, respecting the 20-action sync / 100-action
  async / 5-concurrent-batch ceilings [14]; config templates plus
  `bindNetwork` if FlowSeer models a template layer [12][13].
- **Telemetry:** webhook receiver (HTTPS, valid cert, optional shared
  secret) registered as an HTTP server and attached to the alert types
  FlowSeer cares about, as the primary push path; SNMP (v3 preferred) as a
  polling fallback for either dashboard-proxied or direct-to-device queries
  [6][7].
- **Identifiers to key on:** device serial number as the durable device key;
  network ID and organization ID for the hierarchy; do not key on MAC alone
  since serial is what claim/inventory operations use [10][20].
- **Rate-limit budget:** design around 10 req/s steady-state per
  organization (30 req/2s burst ceiling), read `Retry-After` on 429, and
  track budget per org since it is shared across every client using that
  org's credentials [17].
- **Minimum API version:** target Dashboard API v1 (current, OpenAPI v3
  spec) — v0 is deprecated. Do not opt an organization into the Early
  Access program for a production integration; enrolling exposes every
  endpoint in that org's spec to unannounced breaking changes [1][5][22].
- **Catalyst bridge:** if Catalyst 9000 switches are in scope, plan for
  Cloud Management with IOS-XE (device configuration mode, IOS-XE 17.15.3+),
  not the decommissioned Cloud Monitoring feature [21].

## Sources

1. https://developer.cisco.com/meraki/api-v1/ — fetched 2026-09-10. Base URL, resource hierarchy (org/network/device), CONFIGURE/MONITOR/LIVE TOOL service framing, cloud-managed model.
2. https://developer.cisco.com/meraki/api-v1/ (org/network/device resource description) — fetched 2026-09-10. Hierarchy and inventory-listing endpoints referenced.
3. https://github.com/meraki/openapi — fetched 2026-09-10. Repo hosting `spec3.json` (OpenAPI v3, current) and `spec2.json` (legacy); README points to the Developer Hub as the primary docs source.
4. https://developer.cisco.com/meraki/api-v1/get-organization-openapi-spec/ — found via search 2026-09-10 (not deep-fetched); per-organization OpenAPI spec retrieval endpoint.
5. https://blogs.cisco.com/developer/openapiv3merakiapi01 — fetched 2026-09-10. OpenAPI v3 adoption announcement (June 26, 2023). The companion community.meraki.com thread originally cited here now redirects (301) to community.cisco.com and returns HTTP 403 to automated fetches; dropped as a dead link.
6. https://developer.cisco.com/meraki/webhooks/ — fetched 2026-09-10. Webhook payload fields, shared-secret validation, HTTPS/cert requirement, delivery timing, auto-disable threshold.
7. https://documentation.meraki.com/General_Administration/Monitoring_and_Reporting/SNMP_Overview_and_Configuration — found via search 2026-09-10. Dashboard vs local SNMP, versions supported, AES128 v3 recommendation.
8. https://documentation.meraki.com/General_Administration/Tools_and_Troubleshooting/Using_the_Cisco_Meraki_Device_Local_Status_Page — found via search 2026-09-10. Local status page, digest auth, no SSH/CLI surface.
9. https://developer.cisco.com/meraki/api-v1/authorization/ — fetched 2026-09-10. API key creation location, two-key limit, role-inherited scope, permanence, OAuth2 support and token lifetime.
10. https://documentation.meraki.com/General_Administration/Inventory_and_Devices/Using_the_Organization_Inventory and https://developer.cisco.com/meraki/api-v1/claim-into-organization-inventory/ — found via search 2026-09-10. Serial as claim key, inventory vs network assignment.
11. https://documentation.meraki.com/Cloud_Monitoring_for_Catalyst/Onboarding/Cloud_Monitoring_for_Catalyst_Overview_and_FAQ — fetched 2026-09-10. Onboarding application, NETCONF-in-tunnel transport, "Monitor Only" tag, DNA Essentials/Advantage licensing, superseded-by-cloud-management statement.
12. https://developer.cisco.com/meraki/api-v1/bind-network/ — found via search 2026-09-10 (not deep-fetched). Bind-network-to-template endpoint.
13. https://developer.cisco.com/meraki/api-v1/get-organization-config-templates/ and related config-template endpoints — found via search 2026-09-10. Config templates as shared base configuration across networks.
14. https://developer.cisco.com/meraki/api-v1/action-batches-overview/ — fetched 2026-09-10. Atomicity, sync (20 actions) vs async (100 actions) caps, 5-concurrent-batch limit, confirmed/unconfirmed semantics, one-week auto-delete for unconfirmed batches.
15. https://developer.cisco.com/meraki/api-v1/pagination/ — fetched 2026-09-10 (endpoint examples). Bluetooth clients / appliance client security events cited as paginated monitoring endpoints.
16. https://documentation.meraki.com/General_Administration/Monitoring_and_Reporting/Meraki_Device_Reporting_-_Syslog,_SNMP,_and_API — found via search 2026-09-10 (not deep-fetched). Syslog as a third reporting path alongside SNMP and the API.
17. https://developer.cisco.com/meraki/api-v1/rate-limit/ — fetched 2026-09-10. Per-org 10 req/s + 10 burst, per-IP 100 req/s, 429 body and `Retry-After` header.
18. https://developer.cisco.com/meraki/api-v1/pagination/ — fetched 2026-09-10. RFC 5988 `Link` header, `perPage`/`startingAfter`/`endingBefore` parameters.
19. https://documentation.meraki.com/Cloud_Monitoring_for_Catalyst/Onboarding/Cloud_Monitoring_for_Catalyst_Overview_and_FAQ — fetched 2026-09-10. NETCONF as the in-tunnel protocol.
20. https://documentation.meraki.com/Platform_Management/Dashboard_Administration/Operate_and_Maintain/Inventory_and_Devices/Cannot_Add_Devices_Using_Cisco_Meraki_Order_Number and https://documentation.meraki.com/General_Administration/Inventory_and_Devices/Using_the_Organization_Inventory — found via search 2026-09-10. Serial-number and order-number claim flows, order-number partial-claim failure mode.
21. https://documentation.meraki.com/Switching/Cloud_Monitoring_for_Catalyst/End-of-Service_for_cloud_monitoring_and_onboarding_application and https://documentation.meraki.com/Switching/Cloud_Management_with_IOS_XE/Install_and_Get_Started/Upgrading_Cloud-Monitored_Switches_to_Cloud_Management_with_Device_Configuration — fetched 2026-09-10. March 31, 2026 end-of-service date (extended from January 31, 2026), post-EOS behavior, IOS-XE 17.15.3+ requirement for the replacement. Replaces a community.meraki.com thread that now redirects (301) to community.cisco.com and returns HTTP 403 to automated fetches.
22. https://developer.cisco.com/meraki/meraki-developer-early-access-program/ — fetched 2026-09-10 (direct fetch of `beta-early-access-programs/` and `config-templates-overview/` pages returned HTTP 404). Early access program mechanics: org opt-in exposes the whole org's OpenAPI spec to unannounced breaking changes, not worth joining for occasional beta feature access. The companion community.meraki.com thread originally cited here now redirects (301) to community.cisco.com and returns HTTP 403 to automated fetches; dropped as a dead link, no working replacement found.
23. https://developer.cisco.com/meraki/api-v1/get-organization-devices/ — fetched 2026-09-10. `perPage` default (1000) and range (3-5000).
24. https://developer.cisco.com/meraki/api-v1/get-organization-inventory-devices/ — fetched 2026-09-10. `perPage` default (1000) and range (3-1000).
