---
title: Integration target — Huawei enterprise networking
date: 2026-09-10
scope: Device-side VRP/YunShan OS (CloudEngine S-series campus switches, eKitEngine
  S220/S310 SMB line, AR routers), iMaster NCE-Campus/CampusInsight and legacy
  eSight controllers, and the CloudCampus/eKit cloud-managed app for SMB devices.
  Fetched 2026-09-10.
status: research from public documentation; nothing verified against a live system unless stated
---

# Huawei enterprise networking

Every claim carries a source link. Where documentation is contradictory or version-dependent,
say which version the claim holds for.

The repo already vendors two Huawei-related resources: `spec/mib/huawei/` holds 240 SNMP MIB
modules (no YANG), and `docs/research/network-domain-atlas/vendors/huawei.md` is a dossier on
that MIB corpus — which modules matter for campus switching, Huawei's VRF- and VSI-in-the-key
habits, and the `HUAWEI-ENTITY-EXTENT-MIB`/`-IF-EXT-MIB`/`-WLAN-*` groups FlowSeer would poll.
That file also notes YANG models exist on-device via `get-schema` but were not vendored; this
target confirms a second source below. Nothing under `spec/openapi/` is vendored for Huawei —
no `spec/openapi/huawei/` directory exists (checked 2026-09-10).

## What it manages

Huawei's enterprise networking line splits into two management worlds that share firmware
ancestry but almost nothing else operationally:

- **CloudEngine S-series** campus switches (S5700/S6700/S8700 and newer) run VRP on YunShan OS,
  Huawei's current CLI/NETCONF kernel, with SSH/NETCONF/RESTCONF/gRPC all present as device-side
  surfaces [1][5]. These are managed standalone via CLI/SNMP, or fleet-managed by iMaster
  NCE-Campus.
- **eKitEngine S220/S310** are SMB-tier switches, a rebrand of what shipped as CloudEngine-branded
  small switches; Huawei states the rebrand changed appearance/branding only, not function [2].
  They support CLI, web UI, SNMPv1/v2c/v3, and an app-based path, and can run in either
  Traditional (standalone, on-prem) or Cloud management mode, switchable at will [2].
  The cloud side is Huawei's Qiankun CloudService / CloudCampus platform, reached through the eKit
  SME Network Center at `ekit.huawei.com` [9a].
- **AR routers** (AR100–AR6300 families) share the VRP command surface with switches for CLI,
  SNMP, and NETCONF, documented per-model in Huawei's command references [7].

No published scale limits (max sites/devices per NCE-Campus tenant) were found in the pages
fetched for this pass; unverified.

## API surface

| Surface | Protocol / transport | Spec available? (OpenAPI/YANG/other, URL) | Versioning scheme |
| --- | --- | --- | --- |
| VRP/YunShan CLI | SSH (and console/Telnet) | Command references per product+firmware, e.g. `support.huawei.com/.../EDOC1100291031` for CloudEngine S5700 V600R022C01 [3]; not vendored here | Per product family and firmware train (e.g. `V600R022C01`), documentation republished per release |
| NETCONF | SSH, default port 830 | huawei-\* YANG models plus IETF models; downloadable from `github.com/Huawei/yang` (Apache-2.0, organized by device/hardware type) and also fetchable live from a device via `<get-schema>` [4][8] | Per NETCONF YANG API Reference doc, tied to firmware train |
| RESTCONF | HTTPS | Documented per device family under "RESTCONF Configuration"/"RESTCONF Configuration Commands"; enabled with `http service restconf server enable` after the HTTP server is turned on [5] | Same YANG models as NETCONF, RFC 8040 transport |
| gRPC / gNMI telemetry | gRPC over TCP, dial-in or dial-out | `huawei-telemetry.proto` (service data) and `huawei-grpc-dialout.proto` (dial-out RPC) define the model; documented under "gRPC Fundamentals" / "gRPC-based Subscription Fundamentals" per product [6] | Proto-versioned per firmware train, not independently numbered |
| SNMP | UDP/161, v1/v2c/v3 | HUAWEI-\* MIBs vendored at `spec/mib/huawei/` (240 modules, no YANG); see the atlas dossier for which ~30 modules matter for campus gear | MIB set changes per firmware train; `HUAWEI-WLAN-MIB` vs `-WLAN-CONFIGURATION-MIB` overlap depends on AC6605-era vs AirEngine-era firmware (per the vendored dossier) |
| SFTP/FTP/TFTP/SCP | File transfer, config backup | `save [config-filename]` writes the running config to local flash as a `.cfg` file; `tftp <server> put <local> <remote>` and equivalent `sftp`/`ftp`/`scp` client commands push it off-box [10] | N/A |
| eKitEngine web system | HTTPS, browser UI | Per-model "Web-based Configuration Guide" (e.g. S220/S310 V600R023C00). Correction: eKitEngine S220/S310/S530/S620 do have programmatic APIs — a per-model NETCONF YANG API Reference exists (V600R024C10/V600R025C00), and RESTCONF is documented via "Configuring an HTTP Server" in the S220/S310/S530 Configuration Guide [22][23] | Per firmware train |
| iMaster NCE-Campus NBI | REST over HTTPS, JSON | RESTful API Development Guide, versioned per NCE release (V300R020C10 through at least V300R024C10 seen) [11][12] | Path-versioned (`/controller/v2/...`); guide republished per NCE release |
| Qiankun CloudService / CloudCampus | REST/app, cloud-hosted | Operator/tenant-facing docs at `support.huaweicloud.com/intl/.../qiankuncs*`; no public open-API reference for third-party integrators found | unverified |

## Authentication and authorisation

- **Device SSH/NETCONF/RESTCONF**: local username/password or AAA (RADIUS/HWTACACS), same
  credential store across CLI, NETCONF, and RESTCONF sessions on a given device — this follows
  from all three being configured under the same AAA/SSH user model in Huawei's device docs, but
  the exact interaction of per-VTY ACLs with NETCONF/RESTCONF sessions was not independently
  verified in this pass; unverified: whether a NETCONF-only role exists distinct from CLI access.
- **iMaster NCE-Campus RESTful API**: `POST /controller/v2/tokens` with `userName`/`password` in
  a JSON body returns a token; requests go to the northbound floating IP on port 18002 over
  HTTPS [12]. The calling account needs the "open API operator" role and explicit permission on
  the managed objects it will touch [12]. unverified: token TTL, refresh mechanism, and exact
  rate limits — not found in the pages fetched this pass.
- **eKitEngine Cloud mode**: a device pairs with Qiankun CloudService/CloudCampus by DHCP option,
  a registration/query center, or manual configuration of the platform address, then is switched
  from Traditional to Cloud management mode; switches ship in Traditional mode by default and the
  operator explicitly opts into cloud management [13][14].
- **SFTP/FTP config backup**: authenticates as a separate FTP/SFTP client session using
  device-local or AAA credentials; TFTP has no authentication at all, which the vendor's own docs
  call out as a plaintext, unauthenticated risk [10].

## Data model

- **VRP/YunShan CLI**: `system-view` enters configuration mode. YunShan OS (CloudEngine's current
  kernel) defaults to **two-stage commit**: edits made in system-view land in a *candidate*
  configuration only; the CLI prompt changes from `~` (committed) to `*` (uncommitted changes
  pending) as a visual cue [1][15]. `commit` moves candidate into the running configuration;
  `commit trial <seconds>` stages a timed trial that auto-reverts unless confirmed, and
  `abort trial` cancels it early [15]. `display configuration candidate [changes]` shows what is
  staged but not yet committed; `display current-configuration` shows the running configuration;
  `save` persists the running configuration to the startup file [1][15]. Classic (pre-YunShan)
  VRP on older switch/router firmware applies each command immediately on entry, with no
  candidate/commit stage — this is the behavior the vendored MIB dossier and Huawei's own
  cli-overview docs contrast against YunShan's two-stage default [1].
- **Rollback**: `rollback configuration last <n>` reverts to an earlier state; `rollback
  configuration to file <filename>` restores from a saved configuration file;
  `display configuration commit list` shows commit history, and
  `display configuration commit changes at <commitID>` shows what a given commit changed [15].
- **NETCONF**: standard `<get-config>`/`<edit-config>` against the `candidate` datastore, `<commit>`
  to activate — this mirrors the CLI's own candidate/commit split rather than introducing a
  second model [4]. YANG modules are namespaced `huawei-*` for proprietary models alongside
  standard `ietf-*` modules; both are present in the `Huawei/yang` GitHub tree, organized by
  device/hardware type [8].
- **iMaster NCE-Campus**: organizes devices under sites; RESTful endpoints exist for device
  management, configuration, monitoring, and alarms per the northbound API guide's own table of
  contents [11]. unverified: exact site/device field list and identifier scheme — not fetched in
  this pass.

## Telemetry and events

- **gRPC/gNMI dial-out**: the device acts as gRPC client and pushes subscribed data to a collector
  it dials out to, encoded per `huawei-grpc-dialout.proto` (RPC framing) and
  `huawei-telemetry.proto` (sampled data, GPB or JSON); dial-out is documented as the mode for
  large-scale networks, versus dial-in where the collector connects to the device [6]. A
  configuration example sets a destination group with a collector IP/port and TLS encryption on
  the gRPC channel [6].
- **SNMP traps**: HUAWEI-ALARM-MIB carries `hwAlarmActiveTable`/`hwAlarmSyncTable`/
  `hwEventSyncTable`, described in the vendored atlas dossier as Huawei's most complete alarm
  resynchronization model, keyed per trap target.
- **NETCONF notifications**: not independently verified in this pass beyond the general YANG
  notification mechanism referenced in Huawei's own YANG description [4]; unverified: which
  huawei-\* modules ship `notification` statements for campus switches.
- **iMaster NCE-CampusInsight**: a separate analytics product, not the NBI controller itself. It
  ingests telemetry (explicitly named as its collection mechanism) to do fault prediction and
  per-client experience tracking across Wi-Fi/LAN/WAN, claiming to surface 85% of issues
  proactively per its own marketing data sheet [16]. Treat the 85% figure as a vendor claim, not
  an independently measured number.

## Rate limits, quotas, pagination

None found with a source. The iMaster NCE-Campus NBI guide's security/token pages were fetched
but did not state numeric rate limits or pagination page sizes in the excerpts retrieved [12].
unverified: NBI request rate limits, pagination size for list endpoints (devices, sites, alarms).

## Device-side protocols still available

All of SSH CLI, NETCONF, RESTCONF, gRPC/gNMI, and SNMP remain reachable directly against a
CloudEngine device regardless of whether it is also managed by iMaster NCE-Campus — none of the
fetched docs describe NCE locking out direct device access, unlike some cloud-first vendors.
eKitEngine devices are different: switching a device into Cloud management mode is an explicit,
reversible CLI/DHCP-driven step, and the device still exposes SSH/SNMP/web locally either way [2][14].

Lab-relevant device-side fact (not from public docs, from this repo's own lab notes, not
independently re-verified in this research pass): one lab CloudEngine S220 answers SNMP UDP/161
requests with ICMP port-unreachable because its running configuration has
`undo snmp-agent protocol source-status all-interface` set — the command that must be enabled
(`snmp-agent protocol source-status all-interface`) for the agent to answer on any interface at
all [17][18].

## Provisioning and onboarding

- **eKitEngine SMB devices**: two onboarding paths into Qiankun/CloudCampus — barcode-scan-based
  registration that records device info for automatic onboarding, and Wi-Fi-based deployment
  where the operator's phone joins the AP's own management Wi-Fi to configure it, both through
  the CloudCampus Android/iOS app [19]. Devices default to Traditional (standalone) management
  and must be explicitly switched to cloud management [2][13].
- **CloudEngine/AR (NCE-managed)**: devices register with iMaster NCE-Campus for management; the
  CloudCampus app can also be used here to record and upload device information as part of
  onboarding [19]. unverified: exact ZTP/auto-discovery mechanism (DHCP option, USB, or manual
  IP entry) for NCE-managed CloudEngine switches specifically — the fetched pages describe this
  primarily for the SMB/eKit line.
- **Firmware**: `startup saved-configuration <file>` sets the next-boot startup configuration
  file [10]; firmware upgrade commands exist per-model in the "Upgrade Maintenance Configuration
  Commands" references but were not examined in depth this pass.

## Known quirks and traps

- **Two firmware generations, two commit models.** YunShan OS defaults to two-stage commit
  (`system-view` → edit → `commit`); classic VRP on older CloudEngine/S-series firmware commits
  immediately. An integration that assumes one behavior universally will either leave changes
  uncommitted on YunShan devices or find there is no rollback window on classic VRP [1][15].
- **eKitEngine rebrand is cosmetic.** Huawei's own docs state the S310's CloudEngine-to-eKitEngine
  transition changed only appearance and branding, not function — firmware and command surface
  carried over [2]. Do not assume a naming difference implies a protocol difference.
- **No vendored YANG in this repo yet**, unlike the MIB corpus. `github.com/Huawei/yang` is the
  best public source found (Apache-2.0, organized by device/hardware type), as an alternative or
  supplement to live `<get-schema>` pulls [8]. unverified: how current that GitHub tree is
  relative to the newest CloudEngine firmware trains — its README did not state a sync policy in
  the fetched excerpt.
- **SNMP needs an explicit interface-binding command.** `snmp-agent protocol source-status
  all-interface` (or a specific `source-interface`) must be set before the agent responds on any
  interface; a device with this unset silently drops SNMP with no response rather than an SNMP
  error, which reads as "device unreachable" rather than "SNMP misconfigured" [18]. This matches
  the lab observation above.
- **WLAN MIB set is firmware-era-dependent.** The vendored atlas dossier notes
  `HUAWEI-WLAN-MIB` and `HUAWEI-WLAN-CONFIGURATION-MIB` overlap, and which one is populated
  depends on whether the firmware predates or postdates the AirEngine AP generation — a decoder
  cannot assume one module is authoritative across all Huawei WLAN gear.
- **eSight is a legacy product** with published end-of-support/life-cycle bulletins on Huawei's
  support site; iMaster NCE-Campus is the current replacement for campus network management [20].
  Do not scope new integration work against eSight.

## What FlowSeer needs

- **Credentials to store, per surface**: device AAA username/password (or key) for
  SSH/NETCONF/RESTCONF; separate SNMP v3 (preferred) or v2c community credentials per device;
  iMaster NCE-Campus a bearer token obtained from `/controller/v2/tokens` using an "open API
  operator" account [12]; for eKitEngine cloud-managed devices, whatever credential Qiankun
  CloudService issues per tenant (not independently characterized in this pass).
- **Inventory**: `display device` (serials/hardware) and SNMP `HUAWEI-ENTITY-EXTENT-MIB` (already
  identified as the single most important Huawei MIB in the vendored dossier) for hardware
  inventory over SNMP; NETCONF/RESTCONF `<get>` against `huawei-*` and `ietf-*` device/interface
  models for the same over NETCONF/RESTCONF, subject to per-model YANG availability.
- **Configuration**: NETCONF `<edit-config>` against the candidate datastore plus `<commit>`
  mirrors the CLI two-stage model and is the safer FlowSeer write path on YunShan-generation
  devices, since it gets the same trial/rollback semantics as the CLI; classic VRP devices need a
  different assumption (immediate effect, no candidate stage).
  `spec/mib/huawei/HUAWEI-CONFIG-MAN-MIB` exists for SNMP-side config management events per the
  vendored dossier, though NETCONF is the better primary path for FlowSeer to write through.
- **Telemetry**: gRPC dial-out with `huawei-telemetry.proto`/`huawei-grpc-dialout.proto` for
  push-based data; SNMP polling against the ~30 campus-relevant modules the vendored dossier
  already narrows the 240-module corpus down to, as a fallback for devices that do not expose
  gRPC telemetry.
- **Discovery**: LLDP for topology (neighbor chassis/port/system-name, available via CLI
  `display lldp neighbor` and NETCONF LLDP RPCs per-model) [21]; SNMP `sysORTable`/vendored
  HUAWEI-DEVICE-MIB for capability discovery, matching the vendored dossier's advice to target
  modules deliberately rather than blind-walk the tree on Huawei given its scale.
- **Minimum API version target**: no hard minimum found; NETCONF/RESTCONF/gRPC-telemetry support
  spans many firmware trains (V200Rxx through V600Rxx seen across fetched pages), so FlowSeer
  should pin to whatever firmware the lab/target fleet runs rather than assuming a single cutoff.

## Sources

1. [CLI Overview Commands — CloudEngine 9800/8800/6800/5800 V200R020C10 Command Reference](https://support.huawei.com/enterprise/en/doc/EDOC1100198444/bbd0bc8e/cli-overview-commands) — fetch attempted 2026-09-10, page returned no extractable content directly; corroborated via search-result excerpt describing two-stage mode and `~`/`*` prompts.
2. [S310-24P4S — S200, S300, S500, S600 Hardware Description](https://support.huawei.com/enterprise/en/doc/EDOC1100335757/e029b575/s310-24p4s) — fetched 2026-09-10 via search excerpt; states the CloudEngine→eKitEngine rebrand changed appearance/branding only.
3. [Upgrade Maintenance Configuration Commands — CloudEngine S5700 V600R022C01 Command Reference](https://support.huawei.com/enterprise/en/doc/EDOC1100291031/116aa164/upgrade-maintenance-configuration-commands) — located via search 2026-09-10, not deep-fetched.
4. [NETCONF Configuration Commands — CloudEngine S8700 V600R022C00 Command Reference](https://support.huawei.com/enterprise/en/doc/EDOC1100278255/96532bed/netconf-configuration-commands) — located via search 2026-09-10; direct fetch returned empty (Huawei support pages did not render for the fetch tool). Content on `<edit-config>`/candidate corroborated by search excerpt only.
5. [RESTCONF Configuration — CloudEngine S3700, S5700, S6700 V600R023C00 Configuration Guide](https://support.huawei.com/enterprise/en/doc/EDOC1100334384/4e32ac74/restconf-configuration) — located via search 2026-09-10; `http service restconf server enable` command confirmed via search excerpt.
6. [gRPC Fundamentals — CloudEngine 16800 V200R024C00 Configuration Guide](https://support.huawei.com/enterprise/en/doc/EDOC1100420493/efe2a34e/grpc-fundamentals) and [gRPC-based Subscription Fundamentals — CloudEngine S3700/S5700/S6700 V600R023C10](https://support.huawei.com/enterprise/en/doc/EDOC1100366275/1f2c6147/grpc-based-subscription-fundamentals) — located via search 2026-09-10; dial-out mode, `.proto` filenames, and TLS destination-group example confirmed via search excerpt.
7. [SNMP Configuration Commands — AR100/AR120/AR150/AR160/AR200/AR1200/AR2200/AR3200/AR3600 V200R009 Command Reference](https://support.huawei.com/enterprise/en/doc/EDOC1000174046/dab469bd/snmp-configuration-commands) — located via search 2026-09-10, not deep-fetched.
8. [Huawei/yang GitHub repository](https://github.com/Huawei/yang) — fetched 2026-09-10. Apache-2.0 license, 84 commits at fetch time, organized by device/hardware type per its own description; README did not state a currency/sync policy in the fetched excerpt.
9. [CloudCampus Service_Specifications — Huawei Qiankun CloudService docs](https://support.huaweicloud.com/intl/en-us/moredocuments-qiankuncs/qiankuncs_moredoc_specifications_0001.html) — located via search 2026-09-10; general Qiankun CloudService/CloudCampus platform reference. Correction: this page does not mention `ekit.huawei.com` (checked directly 2026-09-10); see [9a] for the actual source of that claim.
9a. [Logging In to Huawei eKit SME Network Center](https://support.huawei.com/enterprise/en/doc/EDOC1100373532) — located via search 2026-09-10; confirms `ekit.huawei.com` as the eKit SME Network Center login point (Service tab → SME Network Center in the Cloud Management Platform area).
10. Config backup commands (`save`, `tftp ... put`, `startup saved-configuration`) — sourced from Huawei "Backing Up the Configuration File" pages for the S1720/S2700/S5700/S6720 family, located via search 2026-09-10 (e.g. https://support.huawei.com/enterprise/en/doc/EDOC1000141931/a4d0a8db/backing-up-the-configuration-file); confirmed via search excerpt, not a direct render.
11. [iMaster NCE-Campus V300R021C00 RESTful API Development Guide](https://support.huawei.com/enterprise/en/doc/EDOC1100211745) — located via search 2026-09-10; table of contents (device management, configuration, monitoring, alarms) confirmed via a mirrored Scribd copy fetched 2026-09-10 (https://www.scribd.com/document/711645305/iMaster-NCE-Campus-V300R021C00-Introduction-to-Northbound-APIs).
12. [Relay Authentication of iMaster NCE-Campus (API Mode) — V300R022C00 NBI Guide](https://support.huawei.com/enterprise/en/doc/EDOC1100261014/7668c763/relay-authentication-of-imaster-nce-campus-api-mode) and [RESTful API Security — V300R024C10 NBI Guide](https://support.huawei.com/enterprise/en/doc/EDOC1100458567/5d5f999a/restful-api-security) — located via search 2026-09-10; `/controller/v2/tokens`, port 18002, "open API operator" role confirmed via search excerpt.
13. [Understanding Cloud-based Management — S1720/S2700/S5700/S6720 V200R011C10 Configuration Guide](https://support.huawei.com/enterprise/en/doc/EDOC1000178167/4d08a699/understanding-cloud-based-management) — located via search 2026-09-10; `work-mode cloud-mng` command and default-to-Traditional-mode behavior confirmed via search excerpt.
14. [Cloud-based Management Configuration Commands — S1720/S2700/S5700/S6720 V200R011C10 Command Reference](https://support.huawei.com/enterprise/en/doc/EDOC1000178165/70dc575f/cloud-based-management-configuration-commands) — located via search 2026-09-10, not deep-fetched.
15. [Huawei CloudEngine configuration basics. Commit\Rollback (isp-tech.ru)](https://isp-tech.ru/en/huawei-cloudengine-basic-configuration/) — fetched directly 2026-09-10. Third-party writeup, cross-checked against Huawei's own "Rolling Back Configurations" and "display configuration commit" pages surfaced in the same search (https://info.support.huawei.com/hedex/api/pages/EDOC1100277644/AEM10221/04/resources/software/nev8r10_vrpv8r16/user/galaxy/vrp_cfgfile_cfg_0022.html) for `commit trial`, `rollback configuration last`, and `display configuration commit list/changes` command names.
16. [iMaster NCE-CampusInsight — Huawei Enterprise product page](https://e.huawei.com/en/products/network-analysis/campusinsight) — located via search 2026-09-10; telemetry-based collection and the "85% of potential issues" figure are from Huawei's own data sheet/marketing copy, treated here as a vendor claim.
17. Lab observation, this repository's own operational notes (not a public source): a lab Huawei S220 (LABSW03) returns ICMP port-unreachable on SNMP UDP/161. Not independently re-verified against a live device in this research pass.
18. [snmp-agent protocol source-status all-interface — command reference](https://info.support.huawei.com/hedex/api/pages/EDOC1100363264/AEN0403J/05/resources/command/yunshan/SNMPAMULTISOURCESTATUS(SNMPOM).html) — located via search 2026-09-10; command purpose and `undo` form confirmed via search excerpt.
19. [Registering Devices for Onboarding — CloudCampus Solution V100R023C00 Deployment Guide](https://support.huawei.com/enterprise/en/doc/EDOC1100356141/9a7fa19a/registering-devices-for-onboarding) — located via search 2026-09-10; barcode-scan and Wi-Fi-based onboarding paths confirmed via search excerpt.
20. [Huawei eSight Network Life Cycle Notices Bulletins](https://support.huawei.com/enterprise/en/management-system/esight-network-pid-6725036/bulletins?type=life-cycle-notices) — located via search 2026-09-10; existence of end-of-support bulletins confirmed via search result title, page not deep-fetched.
21. [LLDP — S12700 and S12700E V200R019C00 NETCONF YANG API Reference](https://support.huawei.com/enterprise/en/doc/EDOC1100116534/ebc19aaf/lldp) — located via search 2026-09-10; NETCONF LLDP query/enable RPCs confirmed via search excerpt, not deep-fetched.
22. [NETCONF Message Formats — S220, S310, S530, and S620 V600R024C10 NETCONF YANG API Reference](https://support.huawei.com/enterprise/en/doc/EDOC1100460424/191e3192/netconf-message-formats) and [Modifying and Committing the Configuration — S220, S310, S530, and S620 V600R024C10 Configuration Guide](https://support.huawei.com/enterprise/en/doc/EDOC1100460424/3fc9dab6/modifying-and-committing-the-configuration) — located via search 2026-09-10; confirms a dedicated NETCONF YANG API Reference exists for eKitEngine S220/S310/S530/S620, contradicting this dossier's earlier claim that no programmatic API was found for eKitEngine.
23. [Configuring an HTTP Server — S220, S310, and S530 V600R023C10 Configuration Guide](https://support.huawei.com/enterprise/en/doc/EDOC1100380861/b03fe60/configuring-an-http-server) — located via search 2026-09-10; confirms RESTCONF/HTTP-server configuration is documented for the S220/S310/S530 eKitEngine line, not only for CloudEngine.

Note on fetch reliability: `support.huawei.com` pages consistently returned empty content to the
WebFetch tool in this pass (likely client-side rendering the fetch tool cannot execute), so most
Huawei-hosted claims above rely on WebSearch result excerpts rather than a direct page render.
Two non-Huawei-hosted pages (isp-tech.ru, github.com/Huawei/yang) and one Scribd mirror rendered
directly and are marked as fetched above.
