---
title: Integration target — Juniper Networks (Mist cloud and Junos devices)
date: 2026-09-10
scope: Mist Cloud API (api.mist.com and regional clouds), Junos device-side management (NETCONF, REST, gNMI/JTI, SNMP, CLI, ZTP), Junos Space and Apstra (brief)
status: research from public documentation; nothing verified against a live system unless stated
---

# Juniper Networks (Mist cloud and Junos devices)

Juniper's wireless/switching/SD-WAN line (the former Mist Systems product plus EX/QFX/SRX
switches acquired into the Mist portfolio) is managed two ways that FlowSeer needs to treat
as separate integration surfaces: the Mist cloud (a SaaS controller, REST + websocket API)
for AP/switch/gateway fleets it manages, and the Junos device itself (NETCONF/REST/gNMI/SNMP/CLI)
for anything not cloud-adopted or for direct device polling alongside Mist. A third surface,
Junos Space and Apstra, covers on-prem NMS and data-center fabric automation and is out of scope
for day-one work; it is covered briefly at the end.

No Juniper/Mist OpenAPI spec or YANG bundle is vendored in this repo yet (checked
`spec/openapi/*/SOURCES.md` and `docs/research/network-domain-atlas/vendors/`: neither has a
juniper/mist entry as of this research).

## What it manages

- **Mist cloud**: Juniper/Mist access points (AP-series), EX-series switches under "Wired
  Assurance", SSR/Session Smart routers under "WAN Assurance", and Mist Edge appliances, all
  adopted into a cloud organization. Topology is cloud-managed only — devices phone home to
  api.mist.com or a regional equivalent; there is no on-prem controller for this product line.
- **Junos devices** (EX/QFX/SRX/MX/PTX running Junos OS or Junos OS Evolved): standalone or
  cloud-adopted, device-side management protocols work whether or not the device is also
  Mist-managed, though Mist claims exclusive config ownership once adopted (see Known quirks).
- **Junos Space**: on-prem NMS for larger Junos device fleets (routers/switches/firewalls),
  independent of Mist.
- **Apstra**: intent-based data-center fabric automation (leaf-spine, EVPN-VXLAN), a separate
  product from Mist campus/branch management.

## API surface

| Surface | Protocol / transport | Spec available? | Versioning scheme |
| --- | --- | --- | --- |
| Mist Dashboard (REST) API | HTTPS + JSON, `api.mist.com` (or regional equivalent) | OpenAPI 3.0, community-maintained at github.com/tmunzer/mist_openapi (moved to github.com/mistsys/mist_openapi as the canonical location) [1][2]; "manually maintained... mainly for documentation purposes," not for codegen [2] | Path-versioned (`/api/v1/...`); no published deprecation policy found |
| Mist WebSocket streaming API | `wss://api-ws.mist.com/api-ws/v1/stream` (regional) | Documented in the API reference under `/websocket/`; not part of the OpenAPI bundle [3] | `/api-ws/v1` prefix |
| Junos NETCONF | SSH, TCP/830 by default (RFC 6242; RFC 4742 is the same "NETCONF over SSH" spec but was obsoleted by 6242) [4] | YANG: native `junos-*` modules plus OpenConfig modules, both shipped per-release and browsable at the Juniper YANG GitHub org (not fetched in this pass) | Per Junos release train |
| Junos REST API | HTTPS/HTTP JSON, TCP/3443 (HTTPS) and TCP/3000 (HTTP) in the example config seen (unverified as the factory default — not stated as such on the pages fetched), requires `set system services rest` [5] | Junos XML/RPC schema surfaced through `/rpc` path; no separate OpenAPI doc found | Per Junos release |
| Junos Telemetry Interface (JTI) | gRPC/gNMI; unverified: native port 32767 for gRPC/OpenConfig JTI subscriptions and gNMI target port 50051 — this page renders its body via JavaScript and the port numbers could not be confirmed by fetching it directly in this pass [6] | OpenConfig YANG + Juniper native YANG sensors | Per Junos release; platform support varies (from 20.2R1 for many gRPC/gNMI sensors) [6] |
| SNMP | UDP/161, v1/v2c/v3 | JUNIPER-* enterprise MIBs + standard MIBs; enterprise Utility MIB for custom counters [7] | Per Junos release |
| CLI (SSH) | SSH/Telnet, text or `| display xml` / `| display json` | N/A | Per Junos release |
| Junos Space NBI | REST/SOAP over HTTPS | Vendor guides, no public OpenAPI found | Per Junos Space release |
| Apstra REST API | HTTPS JSON, token session from a login endpoint | Built-in "REST API Explorer" per Apstra instance; `pyapstra` SDK and a Terraform provider exist [8] | Per Apstra release (4.2, 5.0 docs found) |

## Authentication and authorisation

**Mist cloud** supports three methods, with Basic Auth (username/password on every call)
scheduled for full deprecation across "admin logins and scripts" by September 2026 [9]:

- **API tokens** — two kinds:
  - *Organization tokens*: bound to the org, not a user; multiple team members' automation can
    share one; access level is configurable per token; suited to service-style integrations [10].
  - *User tokens*: inherit the creating user's own privileges and follow that user across every
    org/MSP they can access; suited to personal scripts [10].
  - The full token value is shown only once, at creation ("The only time you will see the entire,
    untruncated key is upon creation") [10]. No expiry or rotation policy is documented on the
    token-creation page; treat tokens as non-expiring until proven otherwise (unverified: rotation
    cadence).
- **HTTP login** — username/password against `/api/v1/login`, matching the portal dashboard login,
  with optional two-factor; this endpoint is rate-limited after three failed attempts [11].
- **OAuth2** — requires linking the Mist account to an external OAuth provider; the fetched page
  only rendered navigation chrome, so scope names and the exact flow (authorization code vs.
  client credentials) are unverified: could not confirm from `oauth-authentication` or
  `oauth2-0` pages in this pass [12][13].

For FlowSeer's read-only inventory/telemetry integration, an org-level API token scoped to
read-only access is the minimum; a write integration (config push, device claim, template
assignment) needs an org token with write access to inventory and configuration objects.

**Junos NETCONF/REST/CLI** authenticate with local Junos user accounts (username/password or
SSH public key) carrying a Junos login class that grants the needed permission bits (e.g.
`configure`, `view`, `maintenance`). SNMPv3 uses its own USM users (auth + privacy passwords)
independent of CLI accounts [7].

## Data model

**Mist org hierarchy**: Organization → Site → Device, with API tokens scoped at org (or user,
which spans orgs/MSPs) level [1][10]. Identifiers: `org_id`, `site_id`, and per-device `id`
(Mist-internal UUID) plus the device's own MAC address, which is the stable hardware identifier
carried through claim, adoption, and inventory listings.

- **Inventory read**: `GET /api/v1/orgs/:org_id/inventory` lists claimed devices (paginated,
  see Rate limits below); each entry carries MAC, serial, model, site assignment, claim/adoption
  state.
- **Device claim**: devices join an org by claim code (the code printed on/under the unit,
  scanned via QR or entered in the portal/API) for one-off units, or by an order-linked
  activation code for bulk/ZTP-style adoption — the activation code ties to a purchase order so
  a booting unit auto-adopts into the org without a manual claim step [14]. unverified: `Adopt`
  as a distinct action from `Claim` for a device already visible to the org (e.g. via a connected
  switch) — neither page cited at [14] mentions "Adopt"; the Mist OpenAPI spec has only a
  `/sites/:site_id/devices/:device_id/readopt` endpoint, which is adjacent but not a confirmed
  match for this framing.
- **Configuration model**: hierarchical and profile-based, not per-device imperative CLI.
  Config layers are org-level switch templates → site-level switch configuration → per-device
  overrides, with dynamic "port profiles" that auto-apply based on what's detected on a port
  (colorless ports) [15][16]. Linking a site to an org template is a PUT request to
  `/api/v1/sites/:site_id/setting` setting `networktemplate_id` in the body, not a change to the
  site object itself [16]. "Site settings" hold site-wide values
  (NTP, RADIUS, VLANs, etc.) separate from switch templates. unverified: dry-run/validation
  endpoint semantics and exact PATCH partial-update behavior — not confirmed from a fetched
  API reference page in this pass.
- **Device profiles**: reusable AP/switch configuration bundles assignable to a site or device,
  distinct from per-org switch templates; both are managed through their own API resources
  (unverified: exact endpoint paths — only conceptual docs were fetched, not the OpenAPI spec
  itself).

**Junos config model**: candidate + active datastore. Locking is not automatic at the protocol
level — the client must explicitly send `<lock/>` before editing and `<unlock/>` after commit to
get exclusive behavior (equivalent to CLI `configure exclusive`); Juniper's own docs describe
this as something "the application must emit," recommended but not the default, with unlocked
edits as the (not-recommended) alternative [17]. Client libraries commonly default to locking,
though: the Ansible `junipernetworks.junos` collection locks/unlocks by default and exposes a
private-candidate mode where the client skips those RPCs and edits without locking the shared
candidate [17]. Edit modes on `<edit-config>`: `merge` (default — merges new config into
existing), `replace` (replaces the targeted subtree; from Junos 21.1R1 this uses a "load update"
internally rather than "load override") [18], and `none`. `<commit/>` applies the candidate;
`<commit><confirmed/></commit>` applies it provisionally and auto-rolls-back to the prior
committed config if no follow-up confirming commit arrives within a window that defaults to 600
seconds (10 minutes) [19]. `<commit-check>`-equivalent (`<validate>`) verifies candidate syntax
without applying it [4].

## Telemetry and events

- **Mist webhooks**: two topic families — infrastructure (Alert, Audit, Client Join, Client
  Sessions, Device Events, Device Updowns, Guest Authorization, Juniper Mist Edge Events) and
  site-scoped location (Location Coordinates, Occupancy Alerts, RSSI Zone, SDK Client Scan Data,
  Virtual Beacon Entry/Exit, Zone Entry/Exit) [20]. Payloads are JSON with a header describing
  the webhook config and a body carrying `site_id`/`org_id` plus topic-specific fields [20][21].
  Payload signing: when a webhook is created with a `secret`, Mist adds two headers to each
  delivery — `X-Mist-Signature-v2` (HMAC-SHA256 of the body) and `X-Mist-Signature` (HMAC-SHA1
  of the body), both keyed with that secret [28]. unverified: retry count and backoff schedule —
  not present in the two webhook pages fetched, the OpenAPI webhook schemas, or the "API Sample
  Webhooks" section referenced but not fetched in this pass.
- **Mist websocket streaming**: `wss://api-ws.mist.com/api-ws/v1/stream` (region-specific host),
  authenticated, bidirectional; used for near-real-time device/client stats (e.g. hourly-interval
  TX/RX counters) pushed to external dashboards without polling [3][22].
- **Marvis**: Mist's AI assistant, reachable via a conversational interface and a structured
  "Marvis Query Language" for troubleshooting queries (e.g. "why did client X fail to
  connect") [23]. unverified: whether Marvis exposes a distinct machine-callable API endpoint
  separate from the portal UI, versus being UI/chat-only — the fetched pages describe the
  interaction model, not an API contract.
- **Junos JTI**: gRPC/gNMI streaming of OpenConfig (`/interfaces/interface/state/counters`) and
  native (`/junos/system/linecard/interface/`) sensor paths [6]. unverified: native gRPC/OpenConfig
  JTI subscriptions on port 32767 and gNMI examples commonly on port 50051 — [6] renders its body
  via JavaScript and neither port number could be confirmed against the fetched page content in
  this pass; treat both as needing a lab or CLI-reference check before relying on them. Supported
  from roughly Junos 20.2R1 onward, platform
  list includes MX, EX (2300/3400/4300/4600), QFX (5100/5110/5120/5200), SRX54/56/5800, PTX [6].
  Docs warn against partial/wildcard sensor paths (e.g. `/components/component/`) causing device
  load; always subscribe to the fully-qualified path [6].
- **SNMP traps/informs**: standard SNMPv3 notification delivery; JUNIPER-* enterprise MIBs plus
  a Utility MIB for user-defined counters not otherwise exposed [7].

## Rate limits, quotas, pagination

- Mist API: **5,000 calls per hour per token**, resetting on the hour boundary; contact
  `support@mist.com` for a higher limit or an alternative API for bulk use cases [11][24].
  The `/api/v1/login` endpoint has its own protection: rate-limited after three failed login
  attempts [11]. unverified: whether the 5,000/hour figure differs between org and user tokens,
  and whether standard `X-RateLimit-*` response headers are returned — not confirmed in the
  pages fetched.
- Pagination: `X-Page-Limit`, `X-Page-Page`, `X-Page-Total` response headers; default page size
  100, maximum 1000; request with `?limit=N&page=P` query parameters, e.g.
  `GET /api/v1/orgs/:org_id/inventory?limit=2&page=47` [25].

## Device-side protocols still available

Mist adoption does not disable Junos-native management: NETCONF, the Junos REST API, gNMI/JTI,
SNMP, and CLI/SSH remain reachable on an adopted EX switch, though Mist's own switch templates
become the primary config-of-record and out-of-band CLI changes can be overwritten or flagged as
drift by Mist's Wired Assurance (unverified: exact drift-detection/overwrite behavior — not
confirmed from a fetched source in this pass; flagged as a risk to validate against a lab
device). For pure Junos routers/firewalls with no Mist involvement, all of NETCONF/REST/gNMI/
SNMP/CLI are independent and simultaneously usable, each requiring its own `set system services
<protocol>` enablement.

## Provisioning and onboarding

- **Mist claim**: single-unit claim code (label/QR on the device) entered via portal or API;
  bulk/ZTP-style onboarding uses an order-linked activation code so a newly racked unit
  auto-joins the org on first boot without a manual claim step [14].
- **Junos ZTP**: DHCP-driven. Option 150 supplies the **FTP** server IP address (not TFTP — the
  cited page's exact wording is "Option 150—FTP server IP address"), with ZTP preferring it over
  option 66, then the `ftp-ip` parameter inside option 43, for the transfer address [26]. Option
  43 (vendor-specific) carries named parameters `image-file-name`, `configuration-file-name`,
  `transfer-type`, and `ftp-ip` (unverified: numeric sub-option assignments — the cited page names
  the parameters but does not give sub-option numbers) [26]. For the config/script location,
  option 67 (bootfile-name) is primary and option 43 secondary, *except* when the transfer type is
  HTTP, in which case option 43 takes precedence [26]. After the device pulls and applies the
  image/config over the chosen transfer method, it reboots into the provisioned state.
- **Firmware management**: Mist manages AP/switch firmware centrally per-org/per-site through
  the portal/API (specific endpoint not confirmed in this pass — unverified). Junos devices
  independently support `request system software add` and related CLI/NETCONF RPCs for image
  installs, outside of Mist's scope on non-adopted devices.

## Known quirks and traps

- Basic Authentication (username/password on every Mist API call) is being fully retired by
  September 2026 — any integration built now must use API tokens or OAuth2, not Basic Auth [9].
- Mist's API base URL is region-specific (up to 12 documented endpoints across Global, EMEA, and
  APAC clouds: `api.mist.com`, `api.gc1.mist.com`, `api.ac2.mist.com`, `api.gc2.mist.com`,
  `api.gc4.mist.com`, `api.eu.mist.com`, `api.gc3.mist.com`, `api.ac6.mist.com`,
  `api.gc6.mist.com`, `api.ac5.mist.com`, `api.gc5.mist.com`, `api.gc7.mist.com`) [27]. An
  integration must resolve the correct regional host per org (visible in the portal's own URL,
  substituting `manage.` for `api.`) rather than hardcoding `api.mist.com` — a customer on the
  EU cloud simply will not appear if queried against the global host.
- The community OpenAPI spec ("Mist APIs documentation in OpenAPI 3.0... manually maintained... 
  mainly for documentation purposes") explicitly disclaims fitness for codegen or automated
  testing tooling [2] — treat it as a reference, not a source for generated clients, and expect
  drift from the live API.
- `<default-operation>replace</default-operation>` on NETCONF changed its internal mechanism in
  21.1R1 (load update instead of load override) [18] — behavior on older releases may differ for
  edge cases around implicit deletes; pin expectations to the target device's release train.
- Junos REST API's default connection ceiling is 64 simultaneous connections
  (`set system services rest control connection-limit`, configurable 1–1024) [5] — a polling
  integration hitting many devices per process needs to budget concurrent connections per
  device, not just globally.

## What FlowSeer needs

- **Credentials to store**: per-org Mist API token (org-scoped, least-privilege read-only for
  inventory/telemetry; a separate write-scoped token if FlowSeer pushes config), plus the
  resolved regional API base URL and websocket URL per org. For direct Junos access: a
  service-account SSH keypair or username/password per device (or per-site if templated) for
  NETCONF, and SNMPv3 USM credentials if SNMP polling is also used.
- **Endpoints to call**:
  - Inventory: `GET /api/v1/orgs/:org_id/inventory` (paginated).
  - Site/device topology: org and site list endpoints (paths not confirmed from a fetched
    reference page in this pass; use the OpenAPI bundle at [1]/[2] to enumerate exactly).
  - Config: site/template endpoints for switch templates and site settings; `networktemplate_id`
    PATCH to link a site to a template [16].
  - Telemetry: websocket stream at `wss://api-ws.<region>.mist.com/api-ws/v1/stream` [3] for
    near-real-time stats; webhooks for event-driven infra/location topics [20].
- **Identifiers to key on**: Mist `org_id` + `site_id` + device MAC (stable across claim/adopt/
  reassignment) as the primary key; Mist's internal device `id` as a secondary, org-scoped key.
  For direct Junos, the chassis serial number from `show chassis hardware` (or its NETCONF/XML
  equivalent) is the hardware-stable identifier.
- **Rate-limit budget**: plan around 5,000 calls/hour/token [11][24]; a fleet inventory sync
  should page at the 1000-per-page maximum to minimize call count, and any polling loop needs
  headroom under 5,000/hour shared across inventory, config-read, and webhook-driven follow-up
  calls.
- **Minimum API version**: Mist API has no published version negotiation beyond the `/api/v1/`
  path prefix — target `v1` and track the community OpenAPI spec for drift. For Junos, NETCONF
  RFC 6241 base capabilities (candidate, confirmed-commit, validate) are present on any
  currently supported Junos release; JTI gRPC/gNMI sensor coverage should be validated per
  target platform against the 20.2R1+ feature list [6] if telemetry is in scope.

## Sources

1. mistsys/mist_openapi (canonical OpenAPI 3.0 spec, successor to tmunzer/mist_openapi) —
   https://github.com/mistsys/mist_openapi — fetched via search 2026-09-10; confirms "documentation
   only" status and the org migration from tmunzer to mistsys/Mist-Automation-Programmability.
2. tmunzer/mist_openapi — https://github.com/tmunzer/mist_openapi — fetched via search
   2026-09-10; states the spec is "manually maintained... mainly for documentation purposes."
3. WebSocket API Overview / Endpoint — https://api-class.mist.com/websocket/basics/endpoint/ and
   https://www.juniper.net/documentation/us/en/software/mist/automation-integration/topics/concept/websocket-api-overview.html
   — fetched via search 2026-09-10; gives `wss://api-ws.mist.com/api-ws/v1/stream`.
4. Establish an SSH Connection for a NETCONF Session —
   https://www.juniper.net/documentation/us/en/software/junos/netconf/topics/topic-map/netconf-ssh-connection.html
   — fetched via search 2026-09-10; NETCONF over SSH on port 830. The page itself does not cite
   an RFC; RFC 4742 ("NETCONF over SSH") is obsolete and was superseded by RFC 6242, confirmed
   directly against https://www.rfc-editor.org/rfc/rfc4742 (2026-09-10) — cite 6242 for this
   mechanism.
5. Example: Configuring the REST API —
   https://www.juniper.net/documentation/us/en/software/junos/rest-api/topics/example/rest-api-configuring-example.html
   — fetched via search 2026-09-10; shows an example using ports 3000 (HTTP)/3443 (HTTPS) and a
   connection-limit of 100, but does not state these are factory defaults. The connection-limit
   default (64, range 1–1024) is confirmed instead against the CLI reference page
   https://www.juniper.net/documentation/us/en/software/junos/cli-reference/topics/ref/statement/connection-limit-edit-system-services-rest.html
   (fetched 2026-09-10). The 3000/3443 default ports are not independently confirmed in this pass
   (unverified).
6. Guidelines for gRPC and gNMI Sensors (Junos Telemetry Interface) —
   https://www.juniper.net/documentation/us/en/software/junos/open-config/interfaces-telemetry/topics/concept/junos-telemetry-interface-grpc-sensors.html
   — fetched directly 2026-09-10; sensor path guidance and platform support list confirmed. Port
   32767/50051 could not be re-confirmed on re-fetch 2026-09-10: this page renders its body via
   JavaScript and the fetched content had no port numbers in it — marked unverified in this pass.
7. Understand SNMP Implementation in Junos OS / Utility MIB pages —
   https://www.juniper.net/documentation/us/en/software/junos/network-mgmt/topics/topic-map/understand-snmp-implementation-in-junos-os.html
   and https://www.juniper.net/documentation/en_US/junos/topics/task/operational/security-snmp-best-practices-utility-mib-using.html
   — fetched via search 2026-09-10; SNMPv1/v2c/v3, USM/VACM, Utility MIB.
8. REST API Explorer (Apstra 5.0) —
   https://www.juniper.net/documentation/us/en/software/apstra5.0/apstra-user-guide/topics/concept/rest-api-explorer.html
   — fetched via search 2026-09-10; token-session auth, browsable endpoint explorer.
9. Create API Tokens (Mist) —
   https://www.juniper.net/documentation/us/en/software/mist/automation-integration/topics/task/create-token-for-rest-api.html
   — fetched directly 2026-09-10; Basic Auth deprecation by September 2026.
10. Same as [9] — org vs. user token distinction, one-time full-token display.
11. RESTful API Overview (Mist) —
    https://www.juniper.net/documentation/us/en/software/mist/automation-integration/topics/concept/restful-api-overview.html
    — fetched directly 2026-09-10; 5,000 calls/hour, login endpoint rate-limit after three
    failures.
12. OAuth Authentication (Mist API Reference) —
    https://www.juniper.net/documentation/us/en/software/mist/api/http/guides/overview/oauth-authentication
    — fetched directly 2026-09-10; page returned only navigation chrome, no body content
    (JS-rendered SPA), so OAuth2 flow/scope details are unverified from this source.
13. OAuth2.0 (Mist API Reference) —
    https://www.juniper.net/documentation/us/en/software/mist/api/http/guides/authentication/oauth2-0
    — found via search 2026-09-10, not fetched (same SPA rendering issue expected).
14. Claiming APs / Claim a Juniper Access Point pages —
    https://www.mist.com/documentation/claiming-aps/ and
    https://www.juniper.net/documentation/us/en/software/mist/mist-wireless/topics/topic-map/claim-an-ap.html
    — fetched via search 2026-09-10; claim code vs. activation code, Adopt vs. Claim.
15. Overview of Template-Based Switch Configuration —
    https://www.juniper.net/documentation/us/en/software/mist/mist-wired/topics/concept/wired-config-overview.html
    — fetched via search 2026-09-10; org template → site → device inheritance, dynamic port
    profiles.
16. API with Wired Assurance — https://www.mist.com/documentation/api-with-wired-assurance/ —
    fetched via search 2026-09-10; `networktemplate_id` linking a site to a template.
17. Locking and Unlocking the Candidate Configuration Using NETCONF, and the private-mode
    discussion in ansible-collections/junipernetworks.junos issue #169 —
    https://www.juniper.net/documentation/en_US/junos13.2/topics/task/configuration/netconf-configuration-locking-unlocking.html
    and https://github.com/ansible-collections/junipernetworks.junos/issues/169 — fetched via
    search 2026-09-10; exclusive lock default, private-candidate client-side option.
18. Change Individual Configuration Elements Using NETCONF (and related edit-config pages) —
    https://www.juniper.net/documentation/us/en/software/junos/netconf/topics/task/netconf-configuration-elements-changing.html
    — fetched via search 2026-09-10; merge/replace/none default-operation, 21.1R1 replace
    mechanism change.
19. Commit the Candidate Configuration Only After Confirmation Using NETCONF —
    https://www.juniper.net/documentation/us/en/software/junos/netconf/topics/task/netconf-configuration-committing-with-confirmation.html
    — fetched via search 2026-09-10; confirmed-commit default 600-second window.
20. Webhook Messages —
    https://www.juniper.net/documentation/us/en/software/mist/automation-integration/topics/topic-map/webhook-messages.html
    — fetched directly 2026-09-10; full topic list (infrastructure + location), JSON envelope.
21. Setting up Webhooks in Mist — https://www.mist.com/documentation/webhooks/ — found via
    search 2026-09-10, not directly fetched; corroborates topic selection and payload identifiers
    (site_id/org_id).
22. Stream Device Data with a WebSocket (Use Case) —
    https://www.juniper.net/documentation/us/en/software/mist/automation-integration/topics/example/usecase-stream-device-data-with-websocket.html
    — found via search 2026-09-10; hourly stats streaming use case.
23. Marvis Conversations and Queries Overview / Marvis Query Language Overview —
    https://www.juniper.net/documentation/us/en/software/mist/mist-aiops/topics/concept/marvis-interactions.html
    and https://www.juniper.net/documentation/us/en/software/mist/mist-aiops/topics/concept/marvis-query-language-overview.html
    — found via search 2026-09-10; conversational interface and structured query language,
    no confirmed separate machine API.
24. API Rate Limiting (Mist) — https://www.mist.com/documentation/api-rate-limiting/ — fetched
    directly 2026-09-10; corroborates 5,000/hour and the support contact for higher limits.
25. RESTful API Pagination Example — found via search 2026-09-10, corroborated against
    https://www.juniper.net/documentation/us/en/software/mist/automation-integration/topics/concept/rest-api-pagination.html;
    `X-Page-Limit`/`X-Page-Page`/`X-Page-Total` headers, default 100, max 1000.
26. Zero Touch Provisioning DHCP Options for Junos OS Evolved —
    https://www.juniper.net/documentation/us/en/software/junos/junos-install-upgrade-evo/topics/concept/evo-ztp-dhcp-options.html
    — re-fetched directly 2026-09-10; corrected against this pass: option 150 is an **FTP**
    server IP address, not TFTP ("Option 150—FTP server IP address"), preferred over option 66,
    then the `ftp-ip` parameter in option 43; option 43 carries named parameters
    `image-file-name`, `configuration-file-name`, `transfer-type`, `ftp-ip` (no numeric
    sub-options given on this page); option 67 is primary and option 43 secondary for
    config/script location *except* when transfer type is HTTP, where option 43 takes
    precedence. Page title confirms this is Junos OS Evolved-specific; a page for base Junos OS
    ZTP DHCP options was not found in this pass (searched
    .../junos-install-upgrade/topics/concept/ztp-dhcp-options-overview.html, 404) — treat the
    Evolved semantics as unverified for base Junos OS.
27. API Endpoints and Global Regions —
    https://www.juniper.net/documentation/us/en/software/mist/automation-integration/topics/topic-map/api-endpoint-url-global-regions.html
    — fetched directly 2026-09-10; full list of 12 regional API hosts and how to derive one
    from the portal URL.
28. mistsys/mist_openapi, `mist.openapi.json` (webhook and securitySchemes sections) —
    https://raw.githubusercontent.com/mistsys/mist_openapi/master/mist.openapi.json — fetched
    directly 2026-09-10; `components.securitySchemes` defines only `apiToken` (Authorization:
    Token header) and `csrfToken` (session-based), no OAuth2 scheme — consistent with the OAuth2
    pages at [12][13] not rendering usable content; each `webhooks.*` entry's request carries
    `X-Mist-Signature-v2` and `X-Mist-Signature` headers, and the webhook-config schema's
    `secret` field description states "when `secret` is provided, two HTTP headers will be
    added: X-Mist-Signature-v2: HMAC_SHA256(secret, body), X-Mist-Signature: HMAC_SHA1(secret,
    body)". No retry-count or backoff field found in the spec.
