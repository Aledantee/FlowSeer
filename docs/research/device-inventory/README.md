---
title: Device inventory research corpus
date: 2026-09-10
scope: 5 live lab captures (172.16.0.0/24 switches) plus 12 online research
  dossiers on integration targets, synthesized into one requirements picture
  for FlowSeer's device layer
status: research; nothing here changes a schema, a Go type, or a service
  contract — see "What changes and what doesn't" in docs/doc-style.md's
  spirit: read the dossiers before building an adapter, don't cite this file
  in code
---

# Device inventory research corpus

Seventeen dossiers: five live captures against lab switches at
`172.16.0.0/24`, twelve desk-research profiles of vendor integration targets.
Together they answer the question FlowSeer's device layer needs answered
before it writes a protocol adapter — what credential types, config models,
identifiers, telemetry modes, and discovery signals actually exist across the
vendors FlowSeer is likely to onboard. This document is the index and the
synthesis; the per-device and per-target files carry the evidence.

## How this was built

A subnet sweep of `172.16.0.0/24` found seven live hosts: `.1` (the lab
gateway, off-limits, port-scanned only, never touched), `.2`–`.6` (five
switches), and `.21` (a Kali host reachable only over SSH). Nothing else on
the subnet answered.

Each of the five switches got one read-only capture by one agent: `.2`
MikroTik CSS326 on SwOS, `.3` Huawei S220 on YunShan OS, `.4` LANCOM
GS-2326+ on LCOS SX, `.5` Cisco SG220, `.6` Ruckus (CommScope) ICX7150.
No `snmpset`, no CLI config command, no HTTP write, and no reboot was issued
against any of them; the only near-write action anywhere in the corpus was
fetching the MikroTik's `/backup.swb`, which the device produces read-only.
Credentials used for the captures live outside this repository; every raw
capture under each dossier's `_raw/` directory is scrubbed of passwords and
community strings before being committed.

The twelve target dossiers cover vendor product lines FlowSeer is likely to
integrate with, built from public documentation, vendored specs already in
this repo (`spec/openapi/`, `spec/mib/`, `spec/yang/`), and this repo's own
`docs/research/network-domain-atlas/` vendor dossiers where they exist.

Every dossier in this corpus was independently re-verified after its first
draft. The online dossiers were re-verified by fetching every cited source
again and re-checking numeric and authentication claims against what the
source actually says (several corrections are recorded inline — dead links
replaced, a mis-cited claim re-attributed, an "OAuth2" label corrected to
"API key" for LANCOM's LMC). The lab dossiers were re-verified by re-running
at least eight of their claims directly against the device.

New dossiers in this shape start from the two templates:
[`TEMPLATE-lab-device.md`](TEMPLATE-lab-device.md) for a live capture,
[`TEMPLATE-target.md`](TEMPLATE-target.md) for a desk-research target
profile.

## Lab devices

| IP | Hostname | Model / firmware | Surfaces that work (auth) | Surfaces present but blocked (why) | Biggest quirk | Dossier |
| --- | --- | --- | --- | --- | --- | --- |
| .2 | LABSW02 | MikroTik CSS326-24G-2S+, SwOS v2.18 | HTTP UI (Digest, `admin`); SNMP v1/v2c (community `tegi`) | SSH/Telnet/HTTPS: no listener at all; SNMPv3: no engine, times out rather than rejects | `/backup.swb` returns the admin password in cleartext (hex-encoded) inside the config backup | [labsw02-mikrotik-css326.md](lab/labsw02-mikrotik-css326.md) |
| .3 | LABSW03 | Huawei eKitEngine S220-24P4X, YunShan OS 1.25.0.1 | SSH CLI (password `admin`, paramiko only) | SNMP: fully configured (SHA2-256/AES128 v3 user, `Active`) but `undo snmp-agent protocol source-status all-interface` disables the listener on every interface; HTTP/HTTPS/RESTCONF: configured but bound to a down `Vlanif1` | macOS system OpenSSH resets the connection at the password step; paramiko works first try with identical credentials — a client-library choice, not a credential problem | [labsw03-huawei-s220.md](lab/labsw03-huawei-s220.md) |
| .4 | LABSW04 | LANCOM GS-2326+, LCOS SX 3.34.0326SU9 | SSH/HTTPS (password, `admin`); SNMP v1/v2c `public` (sysDescr only) and v3 `authPriv` SHA/AES (`tegi`) | v3 at any level below `authPriv` and v2c `tegi`: `authorizationError`, not a timeout — the `tegi` v3 group only grants `authPriv` | No `show running-config`-equivalent anywhere; config export is TFTP-only (`config-file export`), and the CLI pager cannot be disabled | [labsw04-lancom-gs2326.md](lab/labsw04-lancom-gs2326.md) |
| .5 | LABSW05 | Cisco SG220-26P, firmware 1.3.0.62 | HTTPS UI (query-string GET login); SNMP v1/v2c/v3 `tegi` authPriv SHA/DES only | No SSH/Telnet/CLI at all (Smart-tier, not Managed); SNMPv3 authPriv SHA/AES: configured user has no AES key, times out | TLS caps at 1.0 with a self-signed, expired (2013–2014) cert; device clock is stuck at 2013-05-02, so every HTTP/TLS timestamp is unusable | [labsw05-cisco-sg220.md](lab/labsw05-cisco-sg220.md) |
| .6 | LABSW06 | Ruckus (CommScope) ICX7150-24-POE, IronWare 10.0.10g | SSH CLI (password `admin`, no enable password set); SNMP v1/v2c `public` | HTTPS `/api/*`: live backend, throws bare 500s on unauthenticated GET instead of a clean 401/403; SNMPv3: no user exists at all today (`show snmp user` empty) | The SNMPv3 `tegi` user from earlier documentation no longer exists — it "lived in running-config only" on a device that has since reloaded; any fleet policy that assumes authPriv-only SNMPv3 needs an out-of-band provisioning step for a freshly reset unit | [labsw06-ruckus-icx7150.md](lab/labsw06-ruckus-icx7150.md) |

## Integration targets

| Product | Management topology | API transport | Auth mechanism | Spec available (vendored?) | Rate limit | Push/webhook | Dossier |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Cisco Catalyst Center / IOS-XE / CBS | On-prem/cloud controller, or direct-to-device | REST (Catalyst Center); NETCONF/RESTCONF/gNMI (IOS-XE); SNMP+CLI (CBS) | Basic→bearer token (Catalyst Center); AAA (IOS-XE); SNMPv3 or local (CBS) | Partial OpenAPI only; YANG vendored (`spec/yang/cisco/iosxe/`) | Unpublished (per-API, instance-visible only) | Event webhooks (Catalyst Center) | [cisco-catalyst-iosxe.md](targets/cisco-catalyst-iosxe.md) |
| Cisco Meraki | Cloud only (org → network → device) | REST/JSON | API key (bearer) or OAuth2 | OpenAPI v3, not vendored | 10 req/s + 10 burst per org | Webhooks (HTTPS POST, HMAC-optional) | [cisco-meraki.md](targets/cisco-meraki.md) |
| Fortinet (FortiGate/Manager/Analyzer/Cloud) | Standalone, FortiManager-centralized, or FortiCloud | REST (FortiGate CMDB/Monitor/Log); JSON-RPC (Manager/Analyzer); OAuth2 REST (FortiCloud) | REST API Admin token or session cookie; JSON-RPC session; OAuth2 password grant (FortiCloud) | FNDN-gated Swagger, not vendored | ~100/s reads, ~30/s writes (community-sourced, unverified) | None found (syslog/SNMP trap/NetFlow only) | [fortinet.md](targets/fortinet.md) |
| HPE Aruba (Central / AOS-CX / AOS-S / Instant / AOS-8 / ClearPass) | Cloud (Central) fronting four unrelated device planes | REST (Central, AOS-CX, AOS-S, AOS-8); gNMI+OpenConfig (AOS-CX) | OAuth2 (Central, ClearPass); cookie+CSRF (AOS-CX); cookie (AOS-S); token-in-query (AOS-8) | Central: browsable only; AOS-CX: OpenConfig YANG vendored, no OpenAPI | 7 req/s + 5,000/day base (Central) | Streaming WSS + Webhooks (Central) | [hpe-aruba.md](targets/hpe-aruba.md) |
| Huawei (CloudEngine/eKitEngine/AR, iMaster NCE-Campus, Qiankun) | Standalone, or iMaster NCE-Campus / Qiankun cloud | SSH CLI; NETCONF/RESTCONF; gRPC telemetry; REST (NCE NBI) | AAA/local (device); bearer token from `/controller/v2/tokens` (NCE) | 240 MIBs vendored (`spec/mib/huawei/`); no YANG vendored | Unpublished | gRPC dial-out telemetry | [huawei.md](targets/huawei.md) |
| Juniper (Mist cloud / Junos) | Cloud (Mist) or standalone Junos | REST+WSS (Mist); NETCONF/REST/gNMI/JTI (Junos) | API token or OAuth2 (Mist); local/AAA + SSH key (Junos); SNMPv3 USM | Community OpenAPI (Mist, not vendored); Junos YANG not vendored | 5,000 calls/hour/token (Mist) | Webhooks + WSS stream (Mist) | [juniper-mist-junos.md](targets/juniper-mist-junos.md) |
| LANCOM (LMC / LCOS / LCOS LX / LCOS SX) | Cloud (LMC) fronting three device firmwares | REST/JSON, 13 microservices (LMC); CLI/SNMP (devices) | API key / basic / bearer JWT (LMC, not OAuth2 despite `SOURCES.md`); SNMPv2c/v3 | OpenAPI 3.0.3 vendored (`spec/openapi/lancom/lmc-openapi/`); MIBs vendored | Unpublished; hard request-shape caps (1500 rows, 50 IDs) | None (poll-only `alerts`) | [lancom.md](targets/lancom.md) |
| MikroTik (RouterOS / SwOS) | No cloud tier; standalone per device | REST/JSON, binary API, SSH, SNMP (RouterOS); HTTP Digest only (SwOS) | Local user/group, shared across every surface; SNMPv1/v2c community or v3 | Community OpenAPI vendored (3,652 paths); MIB vendored | Unpublished; REST has a 60s per-request timeout | Binary-API `/listen` subscription only | [mikrotik.md](targets/mikrotik.md) |
| Ruckus / CommScope (SmartZone, Ruckus One, Unleashed, ICX) | Controller (SmartZone), cloud (Ruckus One), or controller-less (Unleashed/standalone ICX) | REST (SmartZone, two independently versioned trees); OAuth2+JWT REST (Ruckus One); RESTCONF+SNMP+CLI (ICX standalone) | Session cookie (SmartZone AP); `serviceTicket` (SmartZone switch, separate login); OAuth2 client-credentials (Ruckus One) | SmartZone Swagger vendored (688 paths); ICX YANG vendored (111 modules); MIBs vendored | Unpublished on every surface | None confirmed (poll-only) | [ruckus.md](targets/ruckus.md) |
| SMB cloud managers (Netgear Insight, Extreme XIQ, D-Link Nuclias, Zyxel Nebula, HPE Instant On) | Cloud, per vendor | REST (XIQ, Nebula); unverified (Insight, Nuclias); none (Instant On) | Username/password→JWT (XIQ); API key (Nebula); unverified (Insight, Nuclias) | XIQ OpenAPI browsable, not vendored; Nebula OpenAPI on GitHub, not vendored; none for the rest | 7,500/hour/org (XIQ); 10 req/s (Nebula, unverified); none published elsewhere | None found on any of the five | [smb-cloud-managers.md](targets/smb-cloud-managers.md) |
| TP-Link Omada | Self-hosted or hardware controller, or Omada Cloud | REST/JSON, two generations (`/openapi/v1`, legacy `/api/v2`) | OAuth2 client-credentials or auth-code (Open API); cookie+CSRF (legacy) | Not published as a downloadable file (served per-controller) | 10 req/s per controller/org, fixed | None found | [tp-link-omada.md](targets/tp-link-omada.md) |
| Ubiquiti UniFi | Self-hosted controller, UniFi OS console, or Site Manager cloud relay | REST/JSON (Network Integration API, Site Manager API); cookie (legacy) | `X-API-Key` (both official APIs); cookie+CSRF (legacy, path differs by deployment) | OpenAPI 3.1/3.0.3 vendored (`spec/openapi/ubiquiti/`) | 10,000 req/min (Site Manager, community-sourced); none found for local API | Alarm Manager webhooks (payload undocumented); WSS event feed (undocumented) | [ubiquiti-unifi.md](targets/ubiquiti-unifi.md) |

## Cross-cutting requirements matrix

Every cell cites the dossier the claim comes from — lab files by their
`labswNN` name, target files by their filename.

### Credential types to store

| Type | Where seen |
| --- | --- |
| API key, header variant | Meraki `Authorization: Bearer` (meraki); LANCOM `Authorization: LMC-API-KEY` (lancom); Zyxel `X-ZyxelNebula-API-Key` (smb-cloud-managers); Ubiquiti `X-API-Key`, both official APIs (ubiquiti-unifi) |
| OAuth2 client-credentials | Ruckus One (ruckus); Fortinet FortiCloud IAM (fortinet); TP-Link Omada Open API Client Credentials mode (tp-link-omada); ClearPass (hpe-aruba) |
| OAuth2 authorization-code | Aruba Central (hpe-aruba); Meraki (alternative to key, meraki); TP-Link Omada Authorization Code mode (tp-link-omada) |
| JWT session login | LANCOM LMC bearer token from `/userlogin` (lancom); Extreme XIQ `POST /login` (smb-cloud-managers); Ruckus One JWT from client-credentials exchange (ruckus) |
| HTTP digest | MikroTik SwOS, confirmed live (labsw02); Meraki local device status page (meraki) |
| Session cookie + CSRF | Fortinet FortiGate `/logincheck` (fortinet); AOS-CX (hpe-aruba); AOS-S (hpe-aruba); Ubiquiti classic API, path differs standalone vs UniFi OS (ubiquiti-unifi); TP-Link legacy `/api/v2` (tp-link-omada); LANCOM GS-2326+ web login, confirmed live (labsw04) |
| SNMPv1/v2c community | All five lab devices answer v1 and/or v2c with a plaintext community (labsw02, labsw03 configured-but-unreachable, labsw04, labsw05, labsw06) |
| SNMPv3 MD5 auth | Confirmed rejected on LANCOM (labsw04: "Authentication failure, wrong protocol") and Cisco SG220 (labsw05: same rejection) |
| SNMPv3 SHA auth | Working on LANCOM (labsw04), Cisco SG220 (labsw05); configured-but-unreachable on Huawei S220 (labsw03) |
| SNMPv3 SHA-256 auth | Configured on Huawei S220 but never reachable — transport disabled at the interface level, not a credential problem (labsw03) |
| SNMPv3 DES priv | Working on Cisco SG220, the only device in the corpus where DES succeeds and AES fails for the same user (labsw05) |
| SNMPv3 AES-128 priv | Working on LANCOM (labsw04); configured on Huawei S220 (labsw03, unreachable); not configured for the Cisco SG220's `tegi` user (labsw05, times out) |
| SSH password | Huawei S220 (labsw03, paramiko-only), LANCOM (labsw04), Ruckus ICX7150 (labsw06) all confirmed live; RouterOS/UniFi/Omada/EdgeSwitch all document SSH password auth (mikrotik, ubiquiti-unifi, tp-link-omada) |
| Certificate pinning | Ruckus ICX7150's manufacturer-issued device cert, `CN=SN-<serial>` (labsw06); FortiGate REST API Admin optional PKI/cert binding (fortinet) |

### Config models seen

| Model | Where seen |
| --- | --- |
| Immediate-apply CLI, no candidate stage | RouterOS (mikrotik); classic/pre-YunShan Huawei VRP (huawei); ICX FastIron (ruckus, labsw06); LANCOM CLI (lancom, labsw04) |
| Two-stage commit (candidate → commit) | YunShan-generation Huawei VRP, `~`/`*` prompt cue (huawei); IOS-XE NETCONF once `netconf-yang feature candidate-datastore` is enabled (cisco-catalyst-iosxe) |
| Candidate + confirmed-commit with auto-rollback | Junos NETCONF, 600s default window (juniper-mist-junos); IOS-XE NETCONF, coupled to the candidate datastore (cisco-catalyst-iosxe) |
| Cloud intent/rollout with poll-able state | LANCOM LMC `rollout` + `GET .../state` (`configState`/`updateState`) (lancom); Catalyst Center `202 Accepted` + `taskId` poll (cisco-catalyst-iosxe); Meraki action batches, confirmed/unconfirmed (cisco-meraki); Ruckus One async writes + `activity/{requestId}` poll (ruckus) |
| PATCH/PUT REST, immediate effect | AOS-CX `PUT`/`PATCH` on running-config, persistence a separate `fullconfigs` copy (hpe-aruba); FortiGate CMDB (with 7.4.1+ transaction support) (fortinet); Ubiquiti Integration API (ubiquiti-unifi); Omada Open API (unverified partial-update semantics) (tp-link-omada) |
| Binary blob backup | MikroTik SwOS `/backup.swb`, plain-text-but-`.b`-encoded, contains the admin password (labsw02); LANCOM SX `config-file export` via TFTP only (labsw04) |
| No config API at all, web/CLI only | SwOS (labsw02); Cisco SG220 Smart-tier, no CLI, backup URL unconfirmed (labsw05); HPE Instant On, vendor-stated "no APIs" (smb-cloud-managers) |

### Inventory identifiers per target

| Identifier | Stable? | Where seen |
| --- | --- | --- |
| Serial number | Yes, near-universal | Meraki (primary claim key), Fortinet, LANCOM (`status.serial`), Ruckus One, TP-Link (implied), Aruba, MikroTik (`/system routerboard`); confirmed on every lab device except Huawei S220 (no `display elabel` on this CLI tier, labsw03) |
| MAC address | Yes, but not always the primary key | RouterOS/UniFi/Omada/Mist all key device paths on MAC; LANCOM treats MAC as queryable, not primary (`status.mac`) |
| UUID / opaque platform ID | No — resets on unclaim/reclaim or re-adoption | Catalyst Center device UUID (unverified stability, cisco-catalyst-iosxe); LANCOM `deviceId` (lancom); Ubiquiti `id`/`deviceId` (ubiquiti-unifi); Mist internal `id` (juniper-mist-junos) |
| RouterOS `.id` | No — reassigned across reboots on some item types | mikrotik |
| `omadacId` | Controller-level, stable per controller | tp-link-omada |
| Meraki claim by serial, order-number claim breaks after partial claim | Serial only, once partially claimed | cisco-meraki |

### Telemetry modes

| Mode | Where seen |
| --- | --- |
| Poll REST | Virtually every target's monitor/statistics endpoints (all 12 target dossiers) |
| SNMP walk, slow-agent numbers observed | LANCOM enterprise MIB: 11,291 varbinds / ~6 min (labsw04); Cisco SG220 proprietary MIB: 52,448 varbinds in 180s without reaching the end (labsw05); Ruckus ICX7150 full mib-2: 13,635 varbinds / 3m12s (labsw06); MikroTik full walk: 1,662 varbinds / ~5s, by contrast fast (labsw02) |
| Webhooks with signing | Meraki (`sharedSecret` HMAC, optional) (cisco-meraki); Mist (`X-Mist-Signature-v2` HMAC-SHA256 + legacy SHA1) (juniper-mist-junos); Aruba Central (HMAC, TLS 1.3) (hpe-aruba); Ubiquiti Alarm Manager (payload undocumented) (ubiquiti-unifi) |
| Websocket streaming | Aruba Central Streaming API, Advanced-license gated (hpe-aruba); Mist websocket (juniper-mist-junos); Ubiquiti's undocumented `wss://.../events` feed (ubiquiti-unifi) |
| gNMI dial-out | IOS-XE MDT via gRPC tunnel service (cisco-catalyst-iosxe); Huawei gRPC dial-out with `huawei-telemetry.proto` (huawei); Junos JTI (port numbers unverified) (juniper-mist-junos) |
| Syslog | Universal fallback across every vendor with a device-resident agent; none of the cloud-only targets (Meraki, Ruckus One, most SMB cloud managers) expose syslog as an API-level push |

### Pagination styles

| Style | Where seen |
| --- | --- |
| `Link` header (RFC 5988) | Meraki (cisco-meraki) |
| `offset`/`limit`, capped | Aruba Central (20–100 per endpoint) (hpe-aruba); Ubiquiti Integration API (max 200, default 25) (ubiquiti-unifi) |
| `page`/`count`/`total_pages` | Extreme XIQ (smb-cloud-managers) |
| Opaque cursor (`nextToken`, `startingAfter`) | Ubiquiti Site Manager (ubiquiti-unifi); Meraki `startingAfter`/`endingBefore` (cisco-meraki) |
| `currentPage`/`currentPageSize` | TP-Link legacy `/api/v2` (tp-link-omada) |
| No pagination — full result set every call | MikroTik REST (mikrotik); RouterOS |
| Filter-plus-hard-cap, no cursor at all | LANCOM LMC (1500-row `limit`, 50-ID batch cap) (lancom) |

### Rate-limit budgets

| Budget | Where seen |
| --- | --- |
| Numeric, vendor-confirmed | Meraki 10 req/s + burst (cisco-meraki); Aruba Central 7 req/s + 5,000/day (hpe-aruba); Mist 5,000/hour/token (juniper-mist-junos); Extreme XIQ 7,500/hour/org (smb-cloud-managers); TP-Link Omada Open API 10 req/s/controller, staff-confirmed non-raisable (tp-link-omada); AOS-CX 6 sessions/user, 48/switch (hpe-aruba) |
| Numeric, community-sourced only | Ubiquiti Site Manager 10,000/min (ubiquiti-unifi); Fortinet ~100/s read, ~30/s write (fortinet); Zyxel Nebula, unverified figure sought but not found (smb-cloud-managers) |
| Unpublished | Catalyst Center (per-API, instance-visible only) (cisco-catalyst-iosxe); Huawei NCE NBI (huawei); Ruckus SmartZone and Ruckus One (ruckus); LANCOM LMC (lancom); MikroTik (mikrotik, only a 60s per-request timeout) |

### Device-side protocols that stay open under cloud/controller management

Confirmed across the corpus: Meraki (SNMP both dashboard-proxied and local),
Aruba Central over AOS-CX/AOS-S, Mist over Junos, Huawei NCE-Campus over
CloudEngine, LANCOM LMC over every device class, TP-Link Omada over
JetStream/EAP standalone mode, Ubiquiti UniFi over SSH (`set-inform` L3
adoption path assumes it). The one confirmed exception in the corpus is
HPE Instant On, which HPE staff state plainly has no API surface once
cloud-managed (smb-cloud-managers) — and Catalyst Center flags but does not
block out-of-band CLI/SNMP changes as drift (cisco-catalyst-iosxe).

### Discovery signals

| Signal | Where seen |
| --- | --- |
| LLDP | Working and populated on Huawei S220 (labsw03), LANCOM (labsw04), Cisco SG220 (labsw05), Ruckus ICX7150 (labsw06, at the IEEE OID, not the `mib-2.99` alias); absent entirely on MikroTik SwOS (labsw02, no LLDP MIB, no UI tab) |
| MNDP (MikroTik-proprietary) | Only discovery mechanism on SwOS, in place of LLDP (labsw02) |
| CDP | Enabled at the protocol level on Cisco SG220 but empty (no CDP-speaking neighbor in the lab) (labsw05); CISCOSB-CDP MIB is SMB-line-only, not IOS/IOS-XE (cisco-catalyst-iosxe) |
| Omada discovery ports | UDP 29810 (discovery), TCP 29811–29817 (management/adoption/firmware/telemetry, port purposes shift across controller generations), UDP 27001 (mobile app) (tp-link-omada) |
| mDNS | Not probed on any lab device this pass; not documented as a discovery mechanism in any target dossier except as an unverified DNS-name fallback (`unifi`, `omada`) |
| DHCP options 43/138/150 | Option 138 for Omada controller discovery across subnets (tp-link-omada); option 43 for UniFi controller discovery (ubiquiti-unifi) and as a LANCOM TR-069/CWMP ACS pointer (lancom); option 150 (FTP server) for Junos ZTP, not TFTP despite the name (juniper-mist-junos) |
| SNMP `sysDescr` as a zero-credential fingerprint | Leaks vendor+model+firmware on 4 of 5 lab devices via a default/guessable community (labsw02 `tegi`, labsw04 `public`, labsw05 `tegi`, labsw06 `public`); Huawei S220 blocks this entirely since SNMP is transport-disabled (labsw03) |
| TLS/HTTP banner fingerprint | Ruckus ICX7150's `CN=SN-<serial>` manufacturer cert (labsw06); Cisco SG220's `Server: GoAhead-Webs` + TLS-1.0-only + `CN=0.0.0.0` combination (labsw05); LANCOM's `Server: eCos Embedded Web Server` (labsw04) |

## What the lab teaches that the docs do not

- **MikroTik SwOS's config backup leaks the admin password in cleartext.**
  `/backup.swb` includes a `pwd.b:{pwd:'<hex>'}` section that decodes to the
  device's actual admin password; any backup this device class produces must
  be treated as a secret, not just as config data (labsw02).
- **Cisco SG220's clock is stuck in 2013 and TLS maxes out at 1.0.** The
  self-signed cert reads `notBefore=2013-05-02`, `notAfter=2014-05-02`
  (expired), `CN=0.0.0.0`, no SAN, and legacy renegotiation is required to
  even complete the handshake — every HTTP/TLS timestamp from this device is
  unusable for correlation (labsw05).
- **LANCOM's GS-2326+ drops GetBulk responses above roughly 1,400 bytes,
  per prior lab documentation** — this capture used GetNext throughout to
  stay safe and did not reproduce the drop directly, but treats the warning
  as authoritative rather than retesting it against a live device (labsw04).
  The same device has no `show running-config`-equivalent CLI command at
  all: config is only exportable via TFTP, never viewable inline, and the
  CLI pager cannot be disabled by any command found in any mode.
- **Huawei S220 ships with SNMP, NETCONF, RESTCONF, and the web UI all
  configured but administratively unreachable.** The SNMPv3 `tegi` user is
  fully provisioned with SHA2-256/AES128 and shows `Active`, but
  `undo snmp-agent protocol source-status all-interface` disables the
  listener on every interface; the web UI and RESTCONF are both bound to a
  `Vlanif1` that is administratively down. This is a transport problem, not
  a credential problem, and it looks identical to "device unreachable" from
  outside (labsw03).
- **Ruckus ICX7150's SNMPv3 user reverts across a reload.** A `tegi` v3 user
  documented as working in an earlier capture "lived in running-config
  only" and no longer exists after this device reloaded; `show snmp user`
  and `show snmp group` are both empty today. Any fleet policy requiring
  authPriv-only SNMPv3 needs an explicit out-of-band provisioning step for a
  freshly reset ICX unit, not an assumption that prior state survives
  (labsw06). The same device also enforces a session-limit/pager quirk:
  `skip-page-display` only works from privileged EXEC, silently no-ops at
  user EXEC.
- **Which standard MIBs each lab device does NOT implement**, confirmed by a
  full or near-full walk rather than inferred: MikroTik SwOS has no
  `ipAddrTable`, no `entPhysicalTable`, no LLDP MIB, no Q-BRIDGE MIB, no
  Power-Ethernet MIB, no Host Resources MIB (labsw02). LANCOM has no
  `ipAddressTable` (RFC 4293), no LLDP MIB objects (LLDP itself works, just
  not over SNMP), no LAG MIB, no Power-Ethernet MIB, no Host Resources MIB
  (labsw04). Cisco SG220 has no legacy `dot1dTpFdbTable` (uses Q-BRIDGE
  instead) and no Host Resources MIB (labsw05). Ruckus ICX7150 has no
  legacy `ipAddrTable`/`ipNetToMedia`, no standard LAG MIB, no
  Power-Ethernet MIB, no Host Resources MIB, and the LLDP MIB only answers
  at its IEEE OID, not the `mib-2.99` alias some tooling assumes (labsw06).

## Gaps and open questions

Grouped by target, everything the dossiers mark unverified that a live
system would settle:

- **Cisco (catalyst-iosxe)**: Catalyst Center's actual per-API rate limits
  (visible only from a live instance's own catalog); whether SD-Access
  intent pushes are synchronous with task completion; NX-OS out of scope
  entirely.
- **Cisco Meraki**: full monitoring-endpoint catalog and native polling
  granularity; webhook retry interval; exact grant type for OAuth2; Cloud
  Management with IOS-XE's endpoint family (same as native MS, or separate).
- **Fortinet**: whether a narrower profile than `super_admin` can do config
  backup; the exact CMDB transaction parameter grammar (`X-TRANSACTION-ID`);
  real (not community-sourced) rate-limit numbers.
- **HPE Aruba**: exact Central role-name granularity; Central config-push
  propagation delay; AOS-S factory-default 401 behavior (found via
  WebSearch only, not independently fetched); whether AOS-CX has NETCONF at
  all.
- **Huawei**: NCE-Campus token TTL, refresh mechanism, and rate limits; how
  current the public `Huawei/yang` GitHub tree is; which `huawei-*` modules
  carry `notification` statements.
- **Juniper/Mist**: OAuth2 flow details for Mist (both fetched pages
  rendered only navigation chrome); JTI gRPC/gNMI port numbers (32767,
  50051) unconfirmed against a directly fetched page; Mist drift-detection/
  overwrite behavior on an adopted EX switch — flagged as needing a lab
  device to validate.
- **LANCOM**: API-key lifetime/rotation policy; propagation delay from
  rollout trigger to device applying config; which of the two device-delete
  endpoints (`devices` vs `control` service) is authoritative.
- **MikroTik**: exact MNDP wire-format field offsets; CAPsMAN's CAP-count
  ceiling.
- **Ruckus**: numeric rate limit on any of SmartZone, Ruckus One, or ICX
  RESTCONF/SNMP — the dossier flags this explicitly as worth closing with a
  lab device before committing to a polling interval; whether FastIron 10.x
  adds NETCONF alongside RESTCONF.
- **SMB cloud managers**: Netgear Insight's and D-Link Nuclias's entire API
  surface sits behind a partner-program login this pass could not reach;
  whether local SNMP/CLI survives Insight or Instant On cloud enrollment.
- **TP-Link Omada**: access-token and refresh-token TTLs; exact HTTP status
  on rate-limit overflow; whether a numeric/controller-assigned device ID
  exists as an alternate key beside MAC; whether Omada devices resolve
  `omada` via DNS as a discovery fallback.
- **Ubiquiti UniFi**: whether the Site Manager API key is actually
  read-only (community claim not found on the primary source when
  re-checked); SNMP trap availability (two community sources directly
  contradict each other); Alarm Manager webhook payload schema; UniFi OS
  3.1.6 compatibility claim (single unfetched source).
- **Lab devices generally**: LANCOM's >1,400-byte GetBulk drop was not
  reproduced live this pass and remains a documented-but-unretested warning
  (labsw04); Cisco SG220's real config-backup URL was never found
  (labsw05); the DHCP-snooping "Add Information Option" field on LANCOM
  reads as enabled but was flagged as worth re-checking against the UI
  label meaning before relying on it operationally (labsw04).

## Recommended next steps for FlowSeer

1. Build the SNMPv2c/v3 poller first, with a v1/v2c fallback path — it is
   the one surface that works, in some form, on all five lab devices and on
   nearly every target vendor's standalone-device tier, and the corpus's
   biggest single risk (Huawei's transport-disabled agent, labsw03) is a
   detectable device state, not a protocol gap.
2. Build the SNMPv3 credential layer to support SHA and SHA-256 auth
   paired with AES-128 and DES priv from day one, not just SHA/AES — the
   lab alone exercises DES-only (Cisco SG220) and SHA-256-configured
   (Huawei S220) devices that a SHA/AES-only client would fail against.
3. Treat "device unreachable" and "credential wrong" as distinguishable
   states in the SNMP client, not conflated into one timeout — three of
   five lab devices produce a silent timeout for a bad community/user
   rather than an SNMP error PDU (labsw02, labsw03, labsw06), which will
   otherwise read as a network problem.
4. Prototype the Huawei CLI adapter against paramiko (or netmiko, which
   sits on paramiko) specifically, not the macOS system OpenSSH client —
   this is a confirmed, reproducible incompatibility, not a one-off (labsw03).
5. Prototype cloud REST adapters for LANCOM LMC and Ubiquiti UniFi next,
   because both already have OpenAPI specs vendored in this repo
   (`spec/openapi/lancom/`, `spec/openapi/ubiquiti/`) — the spec-to-adapter
   path is shortest there, and LMC additionally covers one of the five lab
   devices directly.
6. Design the config-capture abstraction around three shapes, not one:
   immediate-apply CLI text (RouterOS, LANCOM, ICX), two-stage/candidate
   commit (YunShan Huawei, Junos, IOS-XE NETCONF), and cloud rollout with
   poll-able convergence state (LMC, Catalyst Center, Meraki action
   batches) — a single "push config, assume it applied" model will silently
   misbehave against at least two of the three.
7. Fix the Ruckus ICX7150's missing SNMPv3 user before relying on this lab
   device for further SNMPv3 capture work — **this is a live-device write
   and needs explicit approval before it is done**, since it changes running
   configuration on hardware whose earlier write run is recorded in
   `docs/runbooks/lab-icx7150-first-write.md`.
8. Fix the LANCOM GS-2326+'s NTP configuration so its logs carry usable
   timestamps — **this is also a live-device write needing approval**; until
   then, FlowSeer must correlate this device's events on collector-side
   receipt time, never the device's own clock.
9. Leave the Cisco SG220 and Huawei S220's stopped clocks/disabled SNMP
   alone rather than fixing them — both are useful as permanent regression
   fixtures for "device reports an unusable timestamp" and "device has
   fully-configured-but-unreachable SNMP" respectively, cases FlowSeer will
   meet in the field and should have a lab instance of.
