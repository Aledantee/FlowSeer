---
title: Lab device — LABSW05 (172.16.0.5)
date: 2026-09-10
scope: Cisco SG220-26P (Smart switch), firmware 1.3.0.62; measured on the live device
status: research; read-only capture, no device changes made
---

# LABSW05 (172.16.0.5)

Every fact below was read from the device on the date above. Anything inferred
rather than observed is marked *inferred*. Login was performed once (read-only
GET-with-credentials, see Web/API) to inspect authenticated read pages; no
configuration change was made or attempted.

## Identity

| Field | Value |
| --- | --- |
| Vendor / model | Cisco SG220-26P (26-Port Gigabit PoE Smart Switch) |
| Firmware / software version | 1.3.0.62 (`.1.3.6.1.4.1.9.6.1.101.2.4.0`) |
| Serial | DNI1810021Z (entPhysicalSerialNum, chassis entry `.47.1.1.1.1.11.67108992`) |
| sysObjectID | `1.3.6.1.4.1.9.6.1.88.26.2` (matches the brief's expected value) |
| sysDescr | `26-Port Gigabit PoE Smart Switch` |
| Hostname | LABSW05 (sysName; also LLDP chassis/sysname) |
| MAC / base MAC | B0:00:B4:46:13:2E (dot1dBaseBridgeAddress and LLDP chassis ID) |
| Uptime at capture | ~15–21 min across captures (device rebooted shortly before this capture; device clock is wrong — see below) |

Device clock is stuck at **2013-05-02** (seen in every HTTP `Date:` header and
the TLS certificate `notBefore`); this is a GoAhead/RTC default-clock artifact,
not a real timestamp. Any HTTP-layer timestamp from this device is unusable
for correlation without NTP.

## Management surfaces observed

| Surface | Port | Reachable | Auth that worked | Notes (banner, TLS cert subject, version string) |
| --- | --- | --- | --- | --- |
| SSH | 22 | No — `nc -z` times out | n/a | Matches brief: no SSH on this device |
| Telnet | 23 | No — `nc -z` times out | n/a | Matches brief: no Telnet |
| HTTP | 80 | Yes | N/A (redirect only) | `Server: GoAhead-Webs`; unconditional 302 to `http://172.16.0.5/csd36f9d6/` (a random per-boot session-context prefix, see Web/API) |
| HTTPS UI | 443 | Yes | Web form login (GET-with-credentials, see below) | `Server: GoAhead-Webs`; TLS maxes out at **TLSv1.0** with cipher `AES256-SHA` (TLSv1.1/1.2/1.3 all fail to negotiate); self-signed cert `CN=0.0.0.0`, no SAN, `notBefore=2013-05-02`, `notAfter=2014-05-02` (expired); legacy renegotiation required (`ssl_choose_client_version`/`unsafe legacy renegotiation` errors without it) |
| REST / JSON API | — | No dedicated REST path found | — | See Web/API: the UI uses an XML query-string CGI (`System.xml`, `device/wcd`), not a documented REST/JSON surface |
| SNMP | 161/udp | Yes | v1 `tegi`, v2c `tegi`, v3 `tegi` authPriv SHA/DES | See SNMP capabilities below for the full auth matrix |
| NETCONF / RESTCONF / gNMI | 830 / 443 / 9339 | No | — | Port 830 and 9339 both time out; port 443 only serves the web UI, no RESTCONF payload attempted (out of scope: would need a config write) |
| Other (8291, 8728) | — | No | — | Probed per brief (MikroTik ports); both time out — expected, this is a Cisco device |

## SNMP capabilities

**Auth matrix** (all against `sysDescr.0`, `-t 5 -r 1`):

| Version / level | Credential | Result |
| --- | --- | --- |
| v1 | community `tegi` | OK |
| v2c | community `tegi` | OK |
| v2c | community `public` | timeout (community silently dropped, no error PDU) |
| v2c | community `private` | timeout (same) |
| v3 | user `tegi`, noAuthNoPriv | `authorizationError` (access denied to that object) — the configured SNMPv3 user has no view at noAuthNoPriv |
| v3 | user `tegi`, authNoPriv SHA | `authorizationError` (same — the view requires privacy, not just auth) |
| v3 | user `tegi`, authPriv SHA/DES | OK |
| v3 | user `tegi`, authPriv SHA/AES | timeout (AES not configured for this user, confirms the brief) |
| v3 | user `tegi`, authPriv MD5/DES | `Authentication failure (incorrect password, community or key)` — engine only accepts SHA, not MD5, for this user |
| v3 | user `tegi`, authPriv MD5/AES | same auth failure |
| v3 | user `nouser`, authPriv SHA/DES | `Unknown user name` |

All walks below used v3 `tegi` authPriv SHA/DES, `snmpbulkwalk -Cr50 -t 5 -r 1 -On`.

- Full walk: **not attempted** — brief measured ~284k varbinds / ~15 min; instead walked the listed standard subtrees individually plus the enterprise root at a bounded depth (below). GetBulk with `max-repetitions=50` worked cleanly on every subtree that returned data; no truncation observed on the bounded walks.
- Standard MIBs answered (varbind count, wall time):

  | Subtree | OID | Varbinds | Time | Notes |
  | --- | --- | --- | --- | --- |
  | system | 1.3.6.1.2.1.1 | 11 | <1s | |
  | interfaces (ifTable) | 1.3.6.1.2.1.2 | 675 | 4s | 34 ifIndex entries — 5 `up`, 25 `down`, 4 `notPresent` (empty SFP slots) |
  | ifXTable | 1.3.6.1.2.1.31.1.1 | 598 | 4s | |
  | ipAddrTable | 1.3.6.1.2.1.4.20 | 5 | 2s | single row: 172.16.0.5/24 |
  | ipAddressTable | 1.3.6.1.2.1.4.34 | 9 | 2s | RFC4293 table populated too |
  | ipNetToMedia | 1.3.6.1.2.1.4.22 | 4 | 2s | one ARP entry: gateway 172.16.0.1 → 18:fd:74:e2:6e:88, dynamic |
  | ipNetToPhysical | 1.3.6.1.2.1.4.35 | 1 (no data) | 2s | object not implemented (single "No Such Object" line) |
  | entPhysicalTable | 1.3.6.1.2.1.47.1.1.1 | 480 | 2s | chassis, fan, slot, motherboard entries; serial only populated on the chassis row |
  | lldpLocalSystemData | 1.0.8802.1.1.2.1.3 | 88 | 2s | |
  | lldpRemTable | 1.0.8802.1.1.2.1.4.1 | 18 | 2s | one neighbor, on two local ports (LAG members) — see Discovery |
  | dot1dBase | 1.3.6.1.2.1.17.1 | 153 | 2s | bridge address matches base MAC |
  | dot1dTpFdbTable | 1.3.6.1.2.1.17.4.3 | 1 (no data) | 2s | not implemented (this device uses the Q-BRIDGE FDB table instead) |
  | dot1qVlanStaticTable | 1.3.6.1.2.1.17.7.1.4.3 | 26 | 2s | only 1 static VLAN row (VLAN 1) |
  | dot1qVlanCurrentTable | 1.3.6.1.2.1.17.7.1.4.2 | 38 | 2s | 2 active VLANs: 1 (default) and 1000 (matches the LAG ifIndex range 1000–1003 seen in the LAG MIB — looks port-channel/dynamic, not statically provisioned) |
  | dot1qTpFdbTable | 1.3.6.1.2.1.17.7.1.2.2 | 10 | 2s | |
  | dot1dStp | 1.3.6.1.2.1.17.2 | 496 | 2s | `dot1dStpProtocolSpecification=3` (ieee8021d/rstp compatible); bridge priority populated |
  | LAG (ieee8023ad, `1.2.840.10006.300.43`) | 1.2.840.10006.300.43 | 670 | 4s | 4 aggregator indices 1000–1003 present with member MACs |
  | powerEthernetMIB pethPsePortTable | 1.3.6.1.2.1.105.1.1.1 | 312 | 6s | 26 ports × 12 columns |
  | powerEthernetMIB pethMainPseTable | 1.3.6.1.2.1.105.1.3.1 | 4 | 2s | PSE power=100(unit *inferred* W), status on(1), consumption 0 mW, usage threshold 95% |
  | hrSystem | 1.3.6.1.2.1.25.1 | 0 | 2s | **not implemented** — "No Such Object", this device does not carry the Host Resources MIB (as expected for a Smart-tier switch, no general-purpose OS underneath) |

- Enterprise subtree: `1.3.6.1.4.1.9` (Cisco enterprise root) walked with a hard 180s abort per the brief. It **did not finish**; the walk was killed after 180s having returned 52,448 varbinds, every single one still lexicographically inside `.9.6.1.101` — i.e. within the 180s budget the walk never got past the device's single proprietary MIB branch to reach any other Cisco enterprise subtree (ciscoProducts, ciscoMgmt, etc. beyond `.9.9.23`, see below). This confirms the brief's estimate that the full tree is large and that `.9.6.1.101` alone dominates it.
- `1.3.6.1.4.1.9.6.1.101` children, one level, counts **derived from the aborted 180s partial dump** (not exhaustive — the walk had only progressed to child `.55` before being killed, so children above 55 are not represented at all):

  | Child (`.9.6.1.101.<n>`) | Varbinds seen (partial) |
  | --- | --- |
  | 55 | 44,962 |
  | 0 (leading-zero index artifact, likely a `.0.x` scalar prefix) | 2,626 |
  | 29 | 1,840 |
  | 43 | 1,286 |
  | 35 | 692 |
  | 48 | 596 |
  | 53 | 273 |
  | 26 | 73 |
  | 2 | 28 |
  | 1 | 17 |
  | 54 | 15 |
  | 38 | 12 |
  | 52 | 9 |
  | 50 | 9 |
  | 49 | 6 |
  | 42 | 4 |

  Scalar reads directly under `.9.6.1.101.1.*` and `.9.6.1.101.2.*` were legible without a full walk (via targeted `snmpgetnext`) and gave: port count 26, firmware `1.3.0.62`, plus several string/table entries tied to hostname (`.1.19.1.3.3.1 = "LABSW05"`). Child `.55` is almost certainly a large per-port/per-rule table (QoS, ACL, or ARP-inspection style); this was **not walked further** to stay inside the time budget — flagged as needing a dedicated, separately-budgeted walk if FlowSeer ever needs this vendor MIB.
- Cisco CDP MIB (`1.3.6.1.4.1.9.9.23`) **is present and populated** — see Discovery below; this is outside the address range the brief called out but was checked because the brief asked whether CDP is visible via SNMP.
- GetBulk `max-repetitions=50` worked without error on every subtree; no evidence of truncation or a lower server-side cap.
- Writable objects: not tested (would require `snmpset`, out of scope per the brief's read-only rule). The `rlCopy` MIB objects are known (per the brief) to be accepted but never executed by this firmware — not tested here.
- Traps/informs: not checked (would need to read the running config via CLI, which this device does not expose — see below).

## CLI / configuration model

**No CLI is exposed.** TCP 22 (SSH) and 23 (Telnet) both time out; per the brief, this is expected — the SG220 in its default "Smart switch" mode has no CLI at all (unlike the SG300/350 Managed line, which supports SSH and a CLI). This is the single biggest management-surface difference from a Managed switch: **all configuration on this device goes through the web UI**, there is no way to `show running-config` or apply config line-by-line, and `rlCopy` (the vendor's config-copy MIB table) is present in SNMP but a no-op on this firmware.

## Web / API

- Every root request is redirected to a per-boot random path prefix, e.g. `GET /` → `302 Redirect` to `https://172.16.0.5/csd36f9d6/` (the exact suffix changes across device reboots; observed as a stable constant across every request in this session). This behaves like a lightweight CSRF/session-context guard baked into the URL path rather than a cookie.
- With no active session, `GET /csd36f9d6/` further redirects to `.../config/log_off_page.htm`, which is the login page.
- Login flow, read from `../js/login.js` embedded in the login page:
  - The page first calls `GET ./device/wcd?{EncryptionSetting}` to check whether password encryption (RSA) is configured (`passwEncryptEnable` tag in the XML response). In this capture it returned an empty/unparsed response (`statusCode` and `statusString` both blank) — read as "no RSA key configured", so the client falls back to plaintext.
  - The actual login call is a **plain GET with credentials in the query string** (matches the brief's allowed exception): `GET ./System.xml?action=login&user=<user>&password=<password>&ssd=true&`. This was performed once, as `admin`/the documented lab password, for read-only verification. No POST form submission exists on this firmware; the "form" only triggers a client-side XMLHttpRequest GET.
  - Response is `200 OK`, `Content-Type: text/xml`, `<statusCode>0</statusCode><statusString>OK</statusString>`, and carries a **non-standard response header** `sessionID: UserId=<client-ip>&<token>&;path=/` — **not a `Set-Cookie` header**. `login.js` reads this custom header via `xmlhttp.getResponseHeader("sessionID")` and then writes it into `document.cookie` itself (`set_cookie("sessionID", ...)`, `set_cookie("usernme", ...)`) — the browser never sees a normal cookie negotiation, the device relies on JS to round-trip the header into a cookie on the next request.
  - Sending that value back as `Cookie: sessionID=...; usernme=admin` on a subsequent GET authenticates it (tested against `device/wcd?{DeviceView}`, which returned `Access denied` for that specific *page name*, not an auth failure — the session was accepted, but `DeviceView` and three other guessed page names (`DeviceSummary`, `SystemSummary`, `DeviceInfo`, `SystemInfo`) are not valid page identifiers on this firmware; only exact-match page tokens work and none were discovered in the time budget because the full page inventory lives in JS files not fetched here).
- No JSON endpoint found; the backend is **XML over a query-string CGI** (`System.xml?action=...` and `device/wcd?{TagName}`), not REST/JSON, not JSON-RPC. `/api`, `/rest`, `/cgi-bin` all just fall through the same generic path-prefix redirect (not 404s — the redirector doesn't distinguish existing from nonexistent paths at that level, so a plain 404 sweep is not useful against this firmware).
- Backup/restore endpoint: **not conclusively found**. `GET /csd36f9d6/config/config_upload_download.htm` (a guessed filename based on Cisco small-business UI conventions) returned `200 Data follows` / `Form is not defined` — i.e. the request reached the ASP-style form dispatcher but the filename/form ID was wrong. `/backup.swb`, `/sys.b`, `/link.b` (SG300/350-style paths named in the brief) were not separately confirmed as present or absent — probed but returned only the generic redirect, inconclusive either way. Finding the real backup URL would need either a follow-up authenticated capture of the full JS/page inventory (out of this run's time budget) or vendor documentation; flagged as an open item below.
- CSRF token: none observed distinct from the per-boot path prefix; no anti-CSRF header seen in the login exchange.

## Feature inventory (as observed)

| Feature | Observed state |
| --- | --- |
| VLANs | 2 active (1 default, 1000 — 1000 correlates with the LAG aggregator index range, likely auto-created, not statically provisioned); only VLAN 1 has a static-table row |
| LAG | 4 aggregators present (ifIndex-ish `1000`–`1003`) in the IEEE 802.3ad MIB, each carrying a member MAC — LACP/static port-channel support confirmed present via SNMP |
| STP | Enabled; `dot1dStpProtocolSpecification=3` (STP-compatible per RFC1493's ieee8021d value), bridge priority populated |
| LLDP | Enabled, TX+RX (`lldpLocalSystemData` admin status fields = 5 on tested ports); one real neighbor visible (see Discovery) |
| CDP | **Present and enabled** via SNMP (`ciscoCdpMIB` globally enabled, `cdpGlobalRun=1`); no CDP neighbor cache entries observed on this port set at capture time (only LLDP had a live neighbor) |
| PoE | pethMainPseTable: PSE on, 0 mW currently drawn, 95% usage threshold configured; 26-port `pethPsePortTable` populated |
| Port mirroring / ACL / QoS / IGMP snooping / DHCP snooping / 802.1X / RADIUS/TACACS | Not probed — would require walking the large `.9.6.1.101` proprietary subtree (deferred, see SNMP section) or CLI (unavailable); not determined this session |
| Routing | Single IP (172.16.0.5/24), no routing table entries found beyond the local subnet — this is an L2 Smart switch, no L3 routing expected |
| IPv6 | Not probed |
| NTP | Not probed via SNMP or CLI (no CLI); device's own clock is free-running from a stale 2013 default, strongly suggesting NTP is either unconfigured or not syncing |
| Syslog / SNMP traps | Not determined (would need CLI or the `.55` proprietary subtree) |
| Firmware upgrade mechanism | Not determined this session — inferred to be web-UI-only, consistent with no CLI |

## Discovery signals

- **LLDP**: this device advertises itself with chassis ID = base MAC (B0:00:B4:46:13:2E), system name LABSW05, system description "26-Port Gigabit PoE Smart Switch". It sees **one real LLDP neighbor**, `Lab_SW01`, described as `MikroTik RouterOS 6.49.20 (long-term) CRS317-1G-16S+`, visible on two local ports simultaneously (ifIndex 73 and 74, both showing the same neighbor chassis/port — consistent with a LAG/port-channel toward that MikroTik box).
- **CDP**: globally enabled at the protocol level (SNMP `cdpGlobalRun=1`) but no neighbor cache entries were present for the ports checked — either the MikroTik neighbor doesn't speak CDP (likely, it's not a Cisco device) or CDP hadn't aged in a cache entry yet at capture time (device had only been up ~15–20 min).
- **MAC OUI**: B0:00:B4 is a registered Cisco Systems OUI — consistent with the sysDescr/sysObjectID identification.
- **HTTP/TLS banner fingerprint**: `Server: GoAhead-Webs` plus a self-signed cert with `CN=0.0.0.0` and a max TLS version of 1.0 is itself a strong, unauthenticated fingerprint for "old Cisco Small Business firmware" — a scanner doing nothing but a TLS handshake and HTTP HEAD would already identify the device family.
- **mDNS/SSDP**: not probed this session (would need a local broadcast listener on the lab segment; out of scope for a single-device capture over routed access).
- **Cisco Business Dashboard (CBD) probe support**: not determined. CBD discovery is typically driven from the CBD probe application over the local segment (mDNS/UDP broadcast plus the same web API), not a distinct SNMP-visible flag; nothing in the walked MIBs exposed a CBD-specific object, and testing the real CBD discovery flow was out of scope for a read-only single-device capture.

## What FlowSeer needs from this device

- **Primary inventory + telemetry protocol: SNMPv3** (`authPriv`, `SHA`/`DES` only — AES is not configured on this credential set; v1/v2c also work with the same community string but should not be preferred given they're cleartext on the wire). Any FlowSeer poller targeting this device class must support SHA auth with DES privacy, not just the more modern SHA/AES pairing — DES-only devices are still live in this fleet.
- **No CLI surface exists for this switch tier.** Any FlowSeer feature that assumes `show running-config`-style access (backup, full config diff, staged config push) cannot work against Smart-tier SG220s; it must fall back to whatever the web XML API exposes, or accept that these devices are SNMP-read/UI-write only. This is a hard capability split FlowSeer's device model needs to represent (Smart vs Managed Cisco small-business tiers), not just a per-device quirk.
- **TLS to this device must support TLS 1.0** with legacy renegotiation enabled and a self-signed, expired certificate with no verifiable identity (`CN=0.0.0.0`, no SAN) — any FlowSeer HTTPS client (for the web XML API, if ever automated) needs an explicit low-security TLS profile for this device family, separate from the default modern-TLS profile used elsewhere.
- **The device's own clock cannot be trusted** — seen stuck at 2013-05-02 across every HTTP header and the TLS cert validity window. Timestamps from this device (HTTP `Date`, cert dates) must never be used for event correlation; use collector-side receipt time instead, and flag NTP configuration as a likely-needed remediation on real deployments of this hardware.
- **LLDP is the reliable topology-discovery source for this device**; CDP is enabled but empty in this capture, so FlowSeer's topology builder should treat LLDP as primary and CDP as a secondary/confirmation source only, not depend on CDP alone for Cisco small-business gear.
- **The proprietary `1.3.6.1.4.1.9.6.1.101` MIB branch is large** (tens of thousands of varbinds even partially walked) and holds most of the device's true configuration state (VLANs beyond the standard tables, port config, etc. presumably live in child `.55`). If FlowSeer needs anything beyond the standard MIBs captured here from this device family, budget a dedicated, longer SNMP session per device — walking it inline with a general poll cycle would blow past any reasonable per-device time budget.
- **Config backup automation is an open question for this device**: no backup/download URL was confirmed working in this session. Before building a backup feature against this hardware, a follow-up capture needs to walk the authenticated page set (fetch the full menu/JS tree post-login) to find the real endpoint — flagged here rather than guessed further.

## Raw evidence

All under `docs/research/device-inventory/lab/_raw/labsw05/`:

- `tls-raw.txt`, `tls-cert.pem` — full `openssl s_client -tls1 -cipher ALL:@SECLEVEL=0 -legacy_renegotiation` transcript and extracted certificate.
- `http-headers.txt`, `http-root.html`, `https-headers.txt`, `https-root.html` — initial redirect responses on 80 and 443.
- `https-login.html` *(not created — curl could not complete the handshake through the sandboxed TLS stack; superseded by the openssl-based captures below)*.
- `https-csd-page.txt` — the per-boot session-prefix redirect to the log-off/login page.
- `https-logoff-page.txt` — the actual login page HTML + inline `login.js`, source of the `System.xml`/`device/wcd` API details above.
- `wcd-encryptionsetting.txt` — anonymous probe of `device/wcd?{EncryptionSetting}`.
- `login-response-raw.txt` — the login GET/response, **password redacted** (`password=***REDACTED***`); shows the custom `sessionID` response header.
- `wcd-deviceview.txt` — authenticated-session probe of a guessed page name (`Access denied` — wrong page token, not an auth failure).
- `path-probe.txt`, `probe-api.txt`, `probe-configupload.txt` — generic path probes for API/backup endpoints.
- `walk-system.txt`, `walk-interfaces.txt`, `walk-ifXTable.txt`, `walk-ipAddrTable.txt`, `walk-ipAddressTable.txt`, `walk-ipNetToMedia.txt`, `walk-ipNetToPhysical.txt`, `walk-entPhysicalTable.txt`, `walk-lldpLocalSystemData.txt`, `walk-lldpRemTable.txt`, `walk-dot1dBase.txt`, `walk-dot1dTpFdbTable.txt`, `walk-dot1qVlanStaticTable.txt`, `walk-dot1qVlanCurrentTable.txt`, `walk-dot1qTpFdbTable.txt`, `walk-dot1dStp.txt`, `walk-ieee8023adMIB.txt`, `walk-pethPsePortTable.txt`, `walk-pethMainPseTable.txt`, `walk-hrSystem.txt`, `walk-ciscoCdpMIB.txt` — one file per SNMP subtree walk, numeric OIDs (`-On`), each with a matching `.err` file.
- `walk-enterprise-cisco-root.txt` — the 180s-aborted `1.3.6.1.4.1.9` walk (52,448 lines, all within `.9.6.1.101`).
- `enterprise-toplevel-children.txt` — targeted `snmpgetnext` hops used to read `.9.6.1.101.1.*`/`.2.*` scalars without a full walk.
- `enterprise-9-6-1-101-child-counts.txt` — the one-level child-count breakdown of `.9.6.1.101`, derived from the aborted walk above.

No file under `_raw/` contains the device password; the only file that ever held it (`login-response-raw.txt`) has been scrubbed.
