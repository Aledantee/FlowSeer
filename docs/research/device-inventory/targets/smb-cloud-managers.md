---
title: Integration target — SMB cloud network managers
date: 2026-09-10
scope: Netgear Insight, Extreme Networks ExtremeCloud IQ, D-Link Nuclias Cloud/Connect, Zyxel Nebula, HPE Networking Instant On. Cisco Business Dashboard is covered in cisco-catalyst-iosxe.md and is not repeated here.
status: research from public documentation; nothing verified against a live system
---

# SMB cloud network managers

Five vendors sell the same product shape to the same buyer: a switch line
priced for a single-site business, managed from a multi-tenant cloud console
rather than an on-box GUI or a self-hosted controller. The shape is
consistent — organization, site, device, claim-by-serial — but API
availability is not. Two vendors (Extreme, Zyxel) publish a documented REST
API with auth and rate limits. Two (Netgear, D-Link) advertise a partner API
whose actual reference material sits behind a login this pass could not get
past. One (HPE Instant On) has no API at all, by its own staff's statement.
This document covers each vendor as its own top-level section rather than
folding them into one comparison table, because the differences are the
finding.

## Netgear Insight

### What it manages

Netgear's managed and smart-managed switch lines (M4250, M4300, GS7xx,
GS3xx) plus Insight-managed access points and routers, through the Insight
Cloud Portal and mobile app. Netgear splits the same physical switch line
into two management modes: Insight-managed (cloud) and standalone
(local web UI / CLI) — the mode is set at first boot and changes the
management surface available afterward [1].

### API surface

| Surface | Protocol / transport | Spec available? | Versioning scheme |
| --- | --- | --- | --- |
| Insight Cloud REST API for Partners | HTTPS REST, JSON | Portal at `api-web.insight.netgear.com/insightappcom/insight-ui/index.html`, titled "Cloud REST API for Partners" [2] | unverified: no version string surfaced outside login |
| Switch local SNMP | SNMPv1/v2c/v3 | `spec/mib/netgear/` in this repo (FASTPATH-derived, see atlas) [3] | N/A |
| Switch local CLI (FASTPATH) | SSH/console, Cisco-IOS-like syntax | Per-model CLI reference PDFs, e.g. M4250 [4] | Per firmware release |

The partner API's documentation page rendered as a bare Swagger-UI shell with
the title "Cloud REST API for Partners" and no endpoint list visible without
authenticating [2]. NETGEAR community threads confirm the API exists for
partners doing automation and language-SDK integration (Java, JavaScript,
Go, C#, Ruby, PHP are named) [5], and a separate community thread has a user
asking for a PoE port power-cycle endpoint, implying the documented surface
does not (or did not, at time of posting) cover per-port PoE actions [6].
Nothing in this pass confirms current endpoint coverage, auth flow, or
whether "Insight Pro" gates access beyond the free Insight tier — the portal
required credentials this pass did not have.

### Authentication and authorization

unverified: no auth flow could be read without an account. The portal name
("Cloud REST API for **Partners**") suggests scoped partner-program
enrollment rather than open self-service signup, consistent with Zyxel's and
D-Link's partner-gated APIs below [2].

### Data model

unverified: the API's org/site/device hierarchy and identifiers were not
readable from the public portal. The Insight mobile/web UI itself is
organized as Business → Location → Network → Device, per the general Insight
product description [1], but this is the UI hierarchy, not confirmed as the
API's resource hierarchy.

### Telemetry and events

unverified: no telemetry endpoint documentation reached from public search.

### Rate limits, quotas, pagination

None found. Searched NETGEAR developer/community sources; no published
number.

### Device-side protocols still available

SNMP and the FASTPATH CLI remain reachable on Insight-managed switches
according to the CLI manuals, which describe console/SSH access
independent of Insight enrollment [4]. This repo's own vendor dossier
(`docs/research/network-domain-atlas/vendors/netgear.md`) already
establishes that Netgear's switching MIBs (`NETGEAR-SWITCHING-MIB`,
`NETGEAR-SMART-SWITCHING-MIB`) are FASTPATH derivatives sharing ~96 table
names with Ubiquiti EdgeSwitch and the raw FASTPATH corpus, and states
plainly: "Netgear Insight (the cloud platform) has no published API spec
and is not represented here. For Insight-managed switches, SNMP on the
device remains the only machine interface documented in this corpus." That
line is this repo's prior conclusion and this pass did not overturn it —
it only found that a partner API portal exists, not that its contents are
public.

### Provisioning and onboarding

unverified via API. The Insight product manual describes onboarding through
the mobile app or portal by scanning/entering a serial number or MAC, the
same shape as every other vendor here [1].

### Known quirks and traps

- The switch has two operating modes (Insight-managed vs. standalone) set at
  first boot; a device already claimed by Insight is not the same
  addressable target as the same model running standalone CLI/SNMP [1].
- The FASTPATH CLI/MIB overlap with Ubiquiti EdgeSwitch (documented in this
  repo's atlas) means a FASTPATH decoder written for one covers most of the
  other, but Netgear's own MIB corpus is thin (4 modules) relative to the
  full FASTPATH feature set — missing OIDs should be looked up in the raw
  FASTPATH or EdgeSwitch modules.

### What FlowSeer needs

- SNMP (v2c/v3) against the switch directly is the only confirmed machine
  interface for Netgear's SMB switch line today; treat it as the baseline
  integration path.
- Partner-API access requires enrollment FlowSeer does not currently have;
  revisit only after establishing a NETGEAR partner relationship, since the
  public docs give no endpoint detail to plan against.
- If Insight-managed mode is ever targeted, confirm first whether the local
  SNMP/CLI surface stays reachable while a switch is Insight-enrolled, or
  whether Insight enrollment locks it out — this pass found no answer either
  way.

---

## Extreme Networks ExtremeCloud IQ

### What it manages

Switches (Universal/Switch Engine, formerly EXOS), APs, and routers across
customer organizations, through the ExtremeCloud IQ (XIQ) SaaS console.
Underlying device operating systems split three ways: EXOS/Switch Engine
(Broadcom-based switches), VOSS/Fabric Engine (the former Avaya/Nortel line),
and the AP line. XIQ is the northbound cloud console; each device OS also
keeps its own local API.

### API surface

| Surface | Protocol / transport | Spec available? | Versioning scheme |
| --- | --- | --- | --- |
| XIQ REST API | HTTPS REST, JSON | Developer portal at `developer.extremecloudiq.com`, OpenAPI reference at `extremecloudiq.com/api-docs/api-reference.html` [7][8] | unverified: no version header found in this pass; docs are dated by XIQ release, not API version |
| EXOS/Switch Engine JSON-RPC | HTTPS POST to `/jsonrpc` on-box | Vendored example client and docs under `extremenetworks/EXOS_Apps` on GitHub, plus `documentation.extremenetworks.com/exos/api/ClientApplications/JSONRPC/` [9] | Per EXOS release |
| Switch Engine RESTCONF | HTTPS + YANG, on-box | "ExtremeXOS and Switch Engine RESTCONF API Developer Guide" [10] | Per release, guide numbered e.g. `restconf_31.6` |
| VOSS/Fabric Engine RESTCONF | HTTPS + YANG, on-box | "Fabric Engine and VOSS RESTCONF API Guide", versions 8.8 through 9.2 seen [11] | Per release |
| VOSS/Fabric Engine NETCONF | SSH, on-box | Same VOSS User Guide family documents NETCONF alongside RESTCONF [11] | Per release |
| Device SNMP | SNMPv1/v2c/v3 | Not separately vendored under `spec/mib/`; not checked in this pass | N/A |

### Authentication and authorization

- Username/password exchanged at `POST /login` returns a bearer access token
  encoded as a JWT [7].
- Token validity is 1 day (86400 seconds); the client must request a new
  token before expiry or explicitly revoke it via `POST /logout` [7].
- Tokens are sent as `Authorization: Bearer <token>`; every endpoint except
  `/login` requires a valid token [7].
- The public developer-portal authentication page documents only the
  username/password login flow; no separate long-lived API-key mechanism was
  found in this pass, though a related "Add a Third-Party API Token" guide
  exists in the XIQ user guide for a different purpose (letting XIQ call
  *other* systems) — not to be confused with XIQ's own client auth [12].

### Data model

unverified in detail from public docs reached this pass — the REST API
overview page names GET/POST/PUT/PATCH/DELETE semantics and JSON payloads
but the endpoint catalogue itself sits behind the OpenAPI reference UI,
which this pass could not render past its landing shell [8]. Pagination is
documented: list responses carry `page`, `count`, `total_pages`, and
`total_count` fields [8]. Device onboarding into an organization is by
serial number — entered individually or via CSV import — with a documented
gotcha that spreadsheet tools can silently strip leading zeros from serial
numbers before import [13].

### Telemetry and events

unverified: no telemetry/webhook endpoint documentation reached in this
pass. ZTP+-enabled devices push serial number, firmware version, MAC
address, OS, and port information to the cloud automatically at enrollment
time, which is provisioning telemetry rather than ongoing monitoring [14].

### Rate limits, quotas, pagination

- Default quota: **7,500 API requests per hour per customer**, shared across
  all users in that organization, enforced with a sliding-window algorithm
  [15].
- Response headers: `RateLimit-Limit` (e.g. `7500;w=3600`),
  `RateLimit-Remaining`, `RateLimit-Reset` (seconds until reset) [15].
- Exceeding the quota returns HTTP 429 [15].
- Customers needing a higher quota must contact Extreme support to request
  an increase [15].
- Pagination fields are `page`/`count`/`total_pages`/`total_count`, per the
  REST API overview [8]; no default/max page size number was found in this
  pass.

### Device-side protocols still available

This is the strongest device-side story of the five vendors in this
document, because Extreme's device OSes are the same code used in its
non-cloud-managed enterprise gear:

- **EXOS/Switch Engine**: JSON-RPC over HTTPS at `/jsonrpc` on the switch
  itself, with three methods — `cli` (run any CLI command and get back
  structured data instead of screen-scraped text), `runscript` (upload and
  execute a script without a separate transfer step), and `python` (invoke
  the switch's published Python API remotely) [9]. Separately, Switch Engine
  also exposes RESTCONF [10].
- **VOSS/Fabric Engine**: both NETCONF (SSH, port 830 by convention) and
  RESTCONF (HTTPS, YANG-modeled, JSON payloads) are documented northbound
  interfaces "described using the datastore concepts defined in NETCONF"
  [11].
- SNMP is not confirmed disabled on cloud-managed devices in either family;
  not separately checked in this pass.

### Provisioning and onboarding

Serial-number-based claim, either typed in comma-separated or via CSV
upload [13]. ZTP+ is the zero-touch path: a ZTP+-capable device contacts XIQ
Site Engine over HTTPS on first boot and reports serial number, firmware
version, MAC, OS, and port count/speed, letting it appear in the console
with minimal manual entry [14].

### Known quirks and traps

- XIQ documentation is split across two product lines with overlapping
  names — "ExtremeCloud IQ" (SaaS) and "ExtremeCloud IQ Controller /
  ExtremeCloud IQ Site Engine" (on-prem management server, formerly Extreme
  Management Center) — and search results interleave both; confirm which
  product a given doc page describes before trusting version numbers.
- CSV bulk-import of serial numbers through spreadsheet tools risks losing
  leading zeros [13]; this is a documented vendor warning, not a guess.
- The rate-limit quota is per-customer, not per-token or per-integration —
  a single organization running multiple concurrent integrations shares one
  7,500/hour budget [15].

### What FlowSeer needs

- Store a username/password (or eventual API-key mechanism if one is
  confirmed) to mint a 1-day JWT; plan token refresh at well under 24 hours.
- Budget calls against a shared 7,500/hour organization quota and read the
  `RateLimit-*` response headers rather than assuming a fixed per-second
  rate.
- For EXOS/Switch Engine devices, the on-box JSON-RPC `cli` method is a
  fallback path that returns structured data for any CLI command without
  waiting on cloud API coverage — useful for filling gaps the XIQ REST API
  does not expose.
- For VOSS/Fabric Engine devices, NETCONF/RESTCONF with YANG models is the
  standards-shaped path and likely the lowest-effort integration of any
  vendor in this document, if FlowSeer already has NETCONF/RESTCONF tooling
  for other targets.
- Key inventory on serial number, matching the vendor's own onboarding unit.

---

## D-Link Nuclias Cloud / Nuclias Connect

### What it manages

D-Link splits its SMB cloud story into two distinct products with the same
brand: **Nuclias Cloud**, a hosted SaaS console, and **Nuclias Connect**, a
self-hosted (Windows/Linux-Docker) on-premises manager that can run fully
offline, "without uploading data to the cloud" [16]. Nuclias Connect scales
to 1,000 access points under one deployment and is free to use [16]. Managed
device lines include DGS switches (DGS-1210/1250/1510/1520 smart-managed,
DGS-3000/3130/3630 enterprise) and DAP access points.

### API surface

| Surface | Protocol / transport | Spec available? | Versioning scheme |
| --- | --- | --- | --- |
| Nuclias Cloud API | unverified | Documentation center at `resources.dlink.com/nuclias-cloud/` renders only a language-picker shell with no visible developer/API section in this pass [17] | unverified |
| Nuclias Connect local API | unverified | No public developer documentation found | unverified |
| DGS switch SNMP | SNMPv1/v2c/v3 | Vendored in `spec/mib/dlink/`; this repo's atlas documents 143 modules, `DLINKSW-*` for the enterprise line and separate `DGS-1210`-family MIBs for the smart-managed line [18] | N/A |
| DGS switch CLI | SSH/console | Per-model CLI reference manuals published by D-Link support (not fetched in this pass) | Per firmware |

No developer/API page was reachable under either the Nuclias Cloud or
Nuclias Connect documentation centers in this pass — both resolved to a bare
navigation shell client-side rendered, with no visible API/developer link in
the fetched markup [17]. This does not prove no API exists; it means none
was found published. Searches for "Nuclias Cloud API," "Nuclias open API,"
and "Nuclias Connect REST API" returned only user manuals and marketing
pages, not developer references, in contrast to Extreme and Zyxel where a
developer portal was easy to find by name.

### Authentication and authorization

unverified: no API auth documentation found.

### Data model

unverified: no API documentation found. The general Nuclias Cloud portal
manual describes a Site → Device hierarchy at the UI level [16], but this is
not confirmed as an API resource model.

### Telemetry and events

None found.

### Rate limits, quotas, pagination

None found.

### Device-side protocols still available

This repo's own atlas is the authoritative source here and confirms D-Link's
switch line has **no NETCONF, no RESTCONF, no published REST API, and no
YANG** at the device level — SNMP is the only machine plane [18]. The atlas
also flags a specific hazard for the smart-managed DGS-1210 family: it
"redefines standard table names privately" — for example keying
`dot1qVlanTable` on VLAN *name* rather than index — which breaks a
name-based OID matcher that otherwise works across vendors [18].

### Provisioning and onboarding

unverified for Nuclias Cloud via API. Nuclias Connect's on-prem controller
performs local discovery/adoption of devices on the same network [16]; no
API-driven provisioning path was found.

### Known quirks and traps

- Two products share the "Nuclias" brand with materially different
  architectures (hosted SaaS vs. self-hosted, cloud-required vs.
  cloud-optional); confirm which one a customer is running before assuming
  API availability, since this pass found no API for either.
- The DGS-1210 line's private redefinition of standard MIB table names
  (documented in this repo's atlas) is a switch-level hazard independent of
  the cloud-manager question above [18].

### What FlowSeer needs

- Treat D-Link SMB switches as SNMP-only for the foreseeable integration
  path; there is no confirmed cloud or local REST API to build against.
- If a customer runs Nuclias Connect specifically (self-hosted), device
  discovery happens through the controller rather than the switch directly;
  FlowSeer would need to either integrate with that controller (no
  published API found) or bypass it and talk SNMP straight to the switches.
- Before committing engineering time to a Nuclias integration, a direct
  vendor/partner contact is the only path found in this pass to get API
  access — public search surfaced no developer program page for either
  product, unlike Extreme's and Zyxel's public developer portals.

---

## Zyxel Nebula

### What it manages

Zyxel's SMB switch, AP, and gateway line under the Nebula Control Center
(NCC) cloud console. Nebula OpenAPI launched in September 2021 as "the
interface for software to directly interact with the Nebula cloud platform
and Nebula-managed devices" [19].

### API surface

| Surface | Protocol / transport | Spec available? | Versioning scheme |
| --- | --- | --- | --- |
| Nebula OpenAPI | HTTPS REST, JSON | Public OpenAPI spec and docs at `zyxelnetworks.github.io/NebulaOpenAPI/`, source repo `github.com/ZyxelNetworks/NebulaOpenAPI` [20][21] | Repo tagged at release `0.1.36` at time of this fetch [21] |
| Switch CLI/SNMP (GS1900, XGS series) | SSH/console, SNMPv1/v2c/v3 | Per-model user guides, e.g. GS1900-48 [22] | Per firmware |

### Authentication and authorization

- API key authentication, not OAuth2 — "API key scope is per-Admin, and it
  shares the same permissions" as the admin account that generated it
  [20][21].
- Keys are sent in the `X-ZyxelNebula-API-Key` header; a failed check
  returns HTTP 401 [20].
- unverified: the claim that keys are generated and regenerated in the NCC
  console under "API Key Management" inside "My Devices and Services" since
  Nebula 18.20, and that a new key revokes the old one — source [23] as
  fetched in this pass contains no text matching this (it is a support-forum
  thread about the Public API program, not a key-management walkthrough).
- Nebula OpenAPI access is gated behind a paid tier: joining Zyxel's Nebula
  Public API program requires "you already have Nebula devices registered
  with Professional pack" [23], meaning the organization's devices must be
  licensed under Nebula's paid Professional Pack tier. The stronger claim
  that this applies to *all* OpenAPI use, not just the partner program, is
  unverified — source [23] documents the partner-program prerequisite, not
  a blanket statement. A separate Public API partner program exists for
  broader access, joined by contacting Zyxel sales/support directly rather
  than self-service signup [24].

### Data model

Resources are addressed as `{orgId}` → `{siteId}` → `{devId}`, a three-level
organization/site/device hierarchy matching the NCC console structure
[20][21]. Endpoint-level field and pagination detail sits in the OpenAPI
spec itself, which this pass did not fully enumerate — the fetched pages
described the resource hierarchy and auth model but not a full endpoint
catalogue.

### Telemetry and events

None found in this pass beyond the general device/site read endpoints
implied by the `{devId}` resource path.

### Rate limits, quotas, pagination

None found — neither the GitHub repo nor the docs site surfaced a numeric
rate limit in this pass, unlike Extreme's explicit 7,500/hour figure.
Responses do carry an `X-ZyxelNebula-API-RequestId` header for debugging/
support correlation [20].

### Device-side protocols still available

Zyxel's GS1900 and XGS smart-managed switches keep standard remote
management independent of Nebula enrollment: "remote management using PING,
telnet, SNMP, HTTP and TFTP services," with SNMPv1/v2c/v3 available once
TCP/IP is configured [25]. This is device-family documentation (the GS1900
manual), not Nebula-specific confirmation that a Nebula-enrolled unit keeps
these open — treat the two as separate claims.

### Provisioning and onboarding

unverified in detail from the pages reached; device IDs (`{devId}`) are
first-class API resources implying serial/MAC-based registration similar to
the other vendors, but the exact onboarding call sequence was not read in
this pass.

### Known quirks and traps

- API access requires both a Pro Pack license on the org and, for the wider
  partner program, a direct relationship with Zyxel — this is a harder gate
  than Extreme's self-service developer portal [23][24].
- All requests must set `Content-Type: application/json`; the docs note a
  JSON response is always returned even on non-200 status, which matters
  for error-handling code that might otherwise assume a non-JSON body on
  failure [20].

### What FlowSeer needs

- Confirm Pro Pack licensing status per customer org before assuming Nebula
  API access is available — this is a per-org gate, not a global one.
- Store one API key per admin account (not per-integration); rotating it
  invalidates the previous key immediately, so a shared integration account
  is safer than a personal admin's key.
- Key inventory on `{orgId}/{siteId}/{devId}`, matching Zyxel's own
  resource path shape.
- For customers without Pro Pack, or where API access isn't granted, SNMP
  against the switch directly remains available per the device manuals,
  independent of Nebula.

---

## Cisco Business Dashboard

Already covered in
[`cisco-catalyst-iosxe.md`](cisco-catalyst-iosxe.md), which documents the
Dashboard API (access-key auth, REST/JSON, DevNet-published reference, no
rate-limit figure found), the CBS device tiers, and the direct-managed vs.
embedded-probe device modes. Not repeated here per the brief.

---

## HPE Networking Instant On

### What it manages

HPE's small-business switch and AP line (formerly branded Aruba Instant
On), managed through the Instant On mobile app and web portal
(`portal.instant-on.hpe.com`). Positioned explicitly below Aruba Central in
HPE's networking portfolio.

### API surface

| Surface | Protocol / transport | Spec available? | Versioning scheme |
| --- | --- | --- | --- |
| Instant On cloud API | none | No public or partner API exists | N/A |
| Device local management | Web UI only, per HPE's own product design | N/A | N/A |

An HPE employee stated directly in the Instant On community forum: "Instant
On does not support any APIs" [26]. When a user separately asked about API
access for multi-tenant ISP use, another HPE employee's guidance was: "If
API's are a requirement then I would highly recommend looking at Aruba
Central" [26] — i.e., HPE's own answer to an API requirement is to point the
customer at a different, higher product tier rather than at Instant On.
This closes the question cleanly: Instant On is cloud-only with no
programmatic interface, by vendor statement, not merely undocumented.

### Authentication and authorization

N/A — no API exists to authenticate against.

### Data model

N/A for API purposes. The Instant On web/app UI groups devices under a
"site," per the general user guide [27], but this has no API-facing
counterpart.

### Telemetry and events

N/A.

### Rate limits, quotas, pagination

N/A.

### Device-side protocols still available

unverified in this pass whether Instant On switches expose SNMP or a local
CLI independent of cloud enrollment; the HPE Instant On User Guide covers
only the web-application management surface in the pages found [27], and
this pass did not locate an Instant On-specific CLI or SNMP reference. Given
HPE's explicit "no APIs" position for the cloud side, and that Instant On is
positioned as a cloud-only, app-managed product distinct from HPE's
CLI-driven ArubaOS-Switch / Comware lines (covered separately in this repo's
`docs/research/network-domain-atlas/vendors/hpe.md`), it should not be
assumed that Instant On switches share ArubaOS-Switch's SNMP/CLI surface
without separate verification against actual Instant On hardware.

### Provisioning and onboarding

Devices join via the mobile app by local network discovery/Bluetooth setup,
per the standard Instant On onboarding flow described in the user guide
[27]; not API-driven.

### Known quirks and traps

- Do not confuse this product with "Aruba Instant" (the AP-only, on-prem
  clustered product with its own documented REST API for Instant APs [28])
  or with Aruba Central (HPE's API-bearing cloud platform, one tier up).
  Search results interleave all three under similar names; the "no API"
  finding is specific to Instant On, not to HPE/Aruba wireless products in
  general.

### What FlowSeer needs

- No integration path exists today. If a customer's switches are
  Instant-On-managed, FlowSeer cannot reach them through any cloud API;
  local SNMP/CLI availability is unverified and would need direct hardware
  access to confirm.
- If Instant On integration becomes a requirement, the realistic options are
  (a) confirm and use device-local SNMP/CLI if it survives cloud
  enrollment, unverified here, or (b) treat it as out of scope and steer
  affected customers toward Aruba Central, per HPE's own guidance to other
  API-seeking customers [26].

---

## Sources

1. NETGEAR Insight Basic and Premium Mobile App and Cloud Portal user manual — `https://www.downloads.netgear.com/files/GDC/Insight/Insight_UM_EN.pdf` — referenced via search result summary 2026-09-10; Insight product overview, onboarding, and mode description.
2. `https://api-web.insight.netgear.com/insightappcom/insight-ui/index.html` — fetched 2026-09-10; page titled "Cloud REST API for Partners," rendered as an unauthenticated Swagger-UI shell with no endpoint content visible.
3. `docs/research/network-domain-atlas/vendors/netgear.md` (this repo) — read 2026-09-10; Netgear MIB corpus, FASTPATH lineage, and the line "NETGEAR Insight (the cloud platform) has no published API spec and is not represented here."
4. `https://www.downloads.netgear.com/files/GDC/M4250/M4250_CLI_Manual_EN.pdf` — search-result summary 2026-09-10; M4250 CLI reference, console/SSH access.
5. `https://community.netgear.com/t5/NETGEAR-Insight-Network/Insight-Network-REST-API/td-p/2341226` — search-result summary 2026-09-10 (direct fetch returned HTTP 403); community reference to Insight Cloud REST APIs for partner automation across Java/JavaScript/Go/C#/Ruby/PHP.
6. `https://community.netgear.com/t5/NETGEAR-Insight-Network/NETGEAR-PROVIDE-ROUTER-API-FOR-APPLICATION-DEVELOPER/m-p/2391314` — search-result summary 2026-09-10; user request for PoE port control via API.
7. `https://developer.extremecloudiq.com/pages/fc16c5/` — fetched 2026-09-10; XIQ REST API authentication: `POST /login`, JWT bearer token, 1-day (86400s) expiry, `POST /logout` revocation.
8. `https://developer.extremecloudiq.com/pages/ac5337/` — fetched 2026-09-10; REST API overview: HTTPS-only, HTTP verb semantics, JSON payloads, pagination fields (`page`, `count`, `total_pages`, `total_count`).
9. `https://documentation.extremenetworks.com/exos/api/ClientApplications/JSONRPC/` and `https://github.com/extremenetworks/EXOS_Apps/blob/master/JSONRPC/README.md` — search-result summary 2026-09-10; EXOS JSON-RPC `/jsonrpc` endpoint, `cli`/`runscript`/`python` methods.
10. `https://documentation.extremenetworks.com/restconf_31.6/GUID-E6C98F14-2CE0-4103-B1B7-F7052ECBE364.shtml` — search-result title 2026-09-10; ExtremeXOS and Switch Engine RESTCONF API Developer Guide.
11. `https://documentation.extremenetworks.com/Fabric%20Engine%20and%20VOSS%20v9.1%20RESTCONF%20API%20Guide/GUID-43CC5BCA-D515-428D-8379-B903B86348B7.shtml` and `https://documentation.extremenetworks.com/VOSS/SW/842/VOSSUserGuide/GUID-D9BC4B48-5001-408C-B577-386EA9741601.shtml` — search-result summary 2026-09-10; VOSS/Fabric Engine RESTCONF and NETCONF, YANG-modeled, HTTP/HTTPS+JSON.
12. `https://documentation.extremenetworks.com/XIQ/23r6/ug/GUID-B8504C23-3F35-4109-9FAC-B45A5EFEA28B.shtml` — search-result title 2026-09-10; "Add a Third-Party API Token," a distinct feature from XIQ's own client login.
13. `https://documentation.extremenetworks.com/XIQ/23r6/ug/GUID-65BABBBC-CA16-44C9-A51B-0131D05ED6F7.shtml` — search-result summary 2026-09-10; device onboarding by serial number, CSV import, leading-zero Excel warning.
14. `https://documentation.extremenetworks.com/XIQC/10.06/DG/GUID-9FC25BF1-9B2F-40AA-A576-9675A605F762.shtml` — search-result summary 2026-09-10; ZTP+ device-to-cloud handshake fields.
15. `https://developer.extremecloudiq.com/pages/859426/` — fetched 2026-09-10; REST API Rate Limiting: 7,500 requests/hour/customer, sliding window, `RateLimit-*` headers, HTTP 429, support-ticket path to raise the quota.
16. `https://www.dlink.com/en/for-business/nuclias/nuclias-connect` and `https://dlinkmarketing.s3.us-west-1.amazonaws.com/5366487536/Nuclias+Connect+SolutionsGuide_2023.pdf` — search-result summary 2026-09-10; Nuclias Connect self-hosted deployment, offline capability, 1,000-AP scale, free licensing.
17. `https://resources.dlink.com/nuclias-cloud/` — fetched 2026-09-10; documentation-center landing page, no developer/API section visible in the fetched markup.
18. `docs/research/network-domain-atlas/vendors/dlink.md` (this repo) — read 2026-09-10; D-Link MIB corpus (143 modules), "No NETCONF, no RESTCONF, no published REST API, no YANG," and the DGS-1210 private table-name redefinition hazard.
19. `https://community.zyxel.com/en/discussion/12536/zyxel-nebula-public-api-program` — search-result summary 2026-09-10; Nebula OpenAPI introduced September 2021.
20. `https://zyxelnetworks.github.io/NebulaOpenAPI/` — fetched 2026-09-10; API key auth via `X-ZyxelNebula-API-Key`, per-admin key scope, `{orgId}/{siteId}/{devId}` resource path, JSON content-type requirement, `X-ZyxelNebula-API-RequestId` response header.
21. `https://github.com/ZyxelNetworks/NebulaOpenAPI` — fetched 2026-09-10; repo hosting the OpenAPI spec and docs, release tag `0.1.36` at fetch time.
22. `https://download.zyxel.com/GS1900-48/user_guide/GS1900-48_V2.80_Ed1.pdf` — search-result summary 2026-09-10; GS1900-48 user guide reference for remote-management protocols.
23. `https://community.zyxel.com/en/discussion/15533/nebula-public-api` — search-result summary 2026-09-10; "Nebula OpenAPI integration is exclusively available in the Pro Pack," API key management moved into NCC UI as of Nebula 18.20.
24. `https://community.zyxel.com/en/discussion/9766/are-there-any-public-http-restful-apis-for-the-nebula-portal` — search-result summary 2026-09-10; Public API partner program joined by contacting Zyxel sales/support.
25. ZyXEL GS1900-8 user manual, SNMP section — `https://www.manualslib.com/manual/1354175/Zyxel-Communications-Gs1900-8.html?page=199` — search-result summary 2026-09-10; PING/telnet/SNMP/HTTP/TFTP remote management, SNMPv1/v2c/v3.
26. `https://community.instant-on.hpe.com/communities/community-home/digestviewer/viewthread?MID=113` — fetched 2026-09-10; HPE staff statement "Instant On does not support any APIs" and the recommendation to use Aruba Central instead.
27. `https://arubanetworking.hpe.com/techdocs/instanton/3.3.0/HPE-Networking-Instant-On-3.3.0-User-Guide-Web-Application-Version.pdf` — search-result title 2026-09-10; Instant On 3.3.0 web-application user guide.
28. `https://arubanetworking.hpe.com/techdocs/Aruba-Instant-8.x-Books/812/Aruba-Instant-8.12.0.0-REST-API-Guide.pdf` — search-result title 2026-09-10; Aruba Instant (a distinct product from Instant On) REST API guide, cited only to disambiguate naming.
