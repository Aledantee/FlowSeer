---
title: Lab device — LABSW06 (172.16.0.6)
date: 2026-09-10
scope: Ruckus (CommScope) ICX7150-24-POE, IronWare 10.0.10g_cd10T213; measured on the live device
status: research; read-only capture, no device changes made
---

# LABSW06 (172.16.0.6)

Every fact below was read from the device on the date above, over one reused SSH CLI
session (closed cleanly with `exit`) and unauthenticated/read-only SNMP v1/v2c GETs
and walks. Anything inferred rather than observed is marked *inferred*. No `snmpset`,
no configuration-mode command, and no write of any kind was issued.

An earlier capture attempt against this device (`docs/runbooks/lab-icx7150-first-write.md`,
2026-09-09) set and restored one interface description as part of proving a live-write
path; it is unrelated to this read-only capture and is not repeated here.

## Identity

| Field | Value |
| --- | --- |
| Vendor / model | Ruckus Networks (CommScope), ICX7150-24-POE, HW: Stackable ICX7150-24-POE |
| Firmware / software version | IronWare 10.0.10g_cd10T213, labeled `SPR10010g_cd10`, compiled Jul 30 2026; boot code 10.2.12T225 |
| Serial | FEA3220S085 (from `show version`, TLS device cert CN, and `entPhysicalTable` index 2400) |
| sysObjectID | `1.3.6.1.4.1.1991.1.3.64.1.2.1.2` (Foundry/Ruckus enterprise arc) |
| sysDescr | `Ruckus Wireless, Inc. ICX7150-24-POE, IronWare Version 10.0.10g_cd10T213 Compiled on Jul 30 2026 at 17:58:03 labeled as SPR10010g_cd10` |
| Hostname | LABSW06 (`sysName.0`, CLI prompt `SSH@LABSW06>`/`#`) |
| MAC / base MAC | 38:45:3B:0F:CB:C0 (`dot1dBaseBridgeAddress`, matches CLI stack management MAC) |
| Uptime at capture | ~28–30 min into an uptime that reset Jul 30 2026 18:06:55 GMT+00 (cold start); the device's clock/date is not synchronized to real time — see NTP below |

## Management surfaces observed

| Surface | Port | Reachable | Auth that worked | Notes (banner, TLS cert subject, version string) |
| --- | --- | --- | --- | --- |
| SSH | 22 | Yes | Password (`admin`) | Remote banner `SSH-2.0-OpenSSH_9.3`; offers RSA (3072) and ECDSA (384), no ed25519; negotiated host key `ecdsa-sha2-nistp384`, fingerprint `SHA256:xZ3j9T0/Sc9yoSN+m1gT0aVgJoGWCnOfWulHhtr4b6A` — matches the ECDSA-only fact recorded for this device in the first-write runbook |
| Telnet | 23 | No | — | Port closed/filtered in the sweep; `show stack` also reports "Telnet server status: Disabled" |
| HTTP / HTTPS UI | 80/443 | 443 only | — (GET only, no login attempted) | 80 closed/filtered. 443: `Server: nginx`, TLS 1.2, `ECDHE-RSA-AES256-GCM-SHA384`; cert subject `CN=SN-FEA3220S085, O=Ruckus Wireless Inc.`, issuer `CN=RuckusPKI-DeviceSubCA-1, O=Ruckus Wireless Inc.`, validity 2021-05-18 to 2046-05-19 — a manufacturer-issued per-device cert, not self-signed and not trusted by a public root. Root page title `Commscope ICX`, a single-page app (`main.js`, `bootstrap.min.css`, Highcharts) |
| REST / JSON API | 443, `/api/` | Yes, exists | Not tested (GET only) | `/api/` (GET) returns `200` with body `<h1> Web authentication is already done</h1>` — a live backend, not the SPA catch-all. Every other `/api/*` path tried returns a generic `500 Internal Server Error` (Content-Length 290, looks like a bare WSGI/Werkzeug default error page) rather than a clean 401/403, meaning the backend throws on an unauthenticated GET instead of refusing cleanly. All non-`/api/*` paths tried (`/rest/`, `/cgi-bin/`, `/backup.swb`, `/sys.b`, `/link.b`, `/login`) return the same 519-byte SPA `index.html` — client-side routing, not distinct server resources |
| SNMP | 161/udp | v1 and v2c only | community `public` (v1, v2c) | See SNMP capabilities below |
| NETCONF / RESTCONF / gNMI | 830 / 443 / 9339 | No | — | 830 and 9339 closed/filtered in the sweep; 443 serves the web UI/API only, no RESTCONF-shaped endpoint found |
| Other (LLDP, TFTP) | — | LLDP yes | — | LLDP-MIB (`1.0.8802.1.1.2`, the IEEE OID, not the `mib-2.99` alias) answers with 2 neighbors; see Discovery signals |

## SNMP capabilities

- **Versions/auth tried and their exact outcome:**
  - v1 `public` — succeeds (`sysDescr.0` returned).
  - v2c `public` — succeeds.
  - v2c `tegi` — **times out** (`Timeout: No Response from 172.16.0.6`), i.e. the agent silently drops the request rather than returning an SNMP error for a community it doesn't recognize.
  - v3 noAuthNoPriv `tegi` — `Unknown user name`.
  - v3 authNoPriv `tegi` SHA and MD5 — `Unknown user name`.
  - v3 authPriv `tegi` × {SHA,MD5} × {AES,DES} (4 combinations) — all `Unknown user name`.
  - Confirmed from the CLI: `show snmp user` and `show snmp group` both return empty output, and `show running-config | include snmp` shows only `snmp-server community 2 <encrypted> ro` — **no SNMPv3 user exists on this device**, consistent with the first-write runbook's note that the `tegi` v3 user "lived in running-config only" on a now-reloaded device and no longer does. The v2c `tegi` timeout (rather than an error) is because that community also does not exist; only `public` (read-only) is configured.
- **Full walk:** `snmpwalk -v2c -c public` of the whole `mib-2` subtree (`1.3.6.1.2.1`) returned **13,635 varbinds in 3m12.6s** wall time (`-t 3 -r 1 -Cr 20`). No truncation observed (walk ended past the last mib-2 group, `1.3.6.1.2.1.153`, rather than timing out). `ifNumber.0` = 33 (24×1G copper + 2×1G copper + 2×1G SFP + 4×10G SFP+ + a handful of logical interfaces: `lg1`, `ve 1000`, management).
- **GetBulk behaviour:** `-Cr20` (max-repetitions 20) walked the tree without errors or gaps; a `-Cr1` probe against one column (`ifDescr`) also worked cleanly (33 rows, 0.3s) — no evidence of a repetition-count ceiling below 20.
- **Standard MIBs answered, with row/varbind counts from the full mib-2 walk:**
  - `system` (1): 11
  - `interfaces`/`ifTable` (2.2): 726 (33 interfaces × ~22 columns)
  - `ifXTable` (31.1.1): 627
  - `ipAddressTable` (4.34, RFC 4293 style — the older `ipAddrTable` at 4.20 is **empty**, not implemented): 9 rows, one entry for `172.16.0.6/24` on `ve 1000`, address origin `dhcp`
  - `ipNetToPhysicalTable` (4.35 — `ipNetToMediaTable` at 3.1 empty): 5
  - `dot1dBase`+friends (17.1, 17.2 STP, 17.4.3 FDB, 17.7 Q-BRIDGE): 158 / 544 / 21 / dot1qVlanStatic 86 + dot1qTpFdb 21
  - `entPhysicalTable` (47.1.1.1): **1,734 varbinds in 16.4s** on its own — one row per physical slot/port/sensor/PSU; the chassis-level entry (index 1) has empty serial/name (`entPhysicalDescr.1 = "ICX7150 Standalone"`), the real serial lives at index 2400 (`entPhysicalSerialNum.2400 = "FEA3220S085"`)
  - `hrSystem`/host-resources (25.x): **not implemented** — absent from the full walk
  - `snmp` group (mib-2.11): **not implemented** — "No Such Object" when queried directly
- **LLDP-MIB, at its real IEEE OID `1.0.8802.1.1.2` (the `mib-2.99` alias is not populated):** 2,358 varbinds in ~22s. `lldpLocalSystemData`+`lldpLocPortTable` (`.1.3.*`): 96 rows. `lldpRemTable` (`.1.4.1.1.*`): 18 varbinds across 2 neighbor rows — see Discovery signals for the actual neighbor identities.
- **`dot1dStp`:** present under `dot1dBridge` (17.2), 544 varbinds — matches `show 802-1w`'s per-port RSTP table (30 ports/LAGs).
- **`ieee8023adMIB` (LAG, standard OID `1.2.840.10006.300.43`):** **not implemented** — "No Such Object". LAG state is only visible over SNMP via the vendor enterprise tree or over CLI (`show lag`); it is not exposed through the standard LAG MIB.
- **`powerEthernetMIB` (`1.3.6.1.2.1.105`, `pethPsePortTable`/`pethMainPseTable`):** **not implemented** — absent from the full mib-2 walk. PoE state is only visible through the vendor enterprise tree (below) or CLI `show inline power`.
- **Enterprise subtree, `1.3.6.1.4.1.1991` (FOUNDRY-SN-AGENT-MIB):** 9,291 varbinds in 50.7s. Everything answered lives under `.1991.1` (children `.1991.1.2`/`.3`/`.4` etc. return nothing directly attached — nothing at those exact positions, only deeper). Top-level children of `.1991.1` with counts: `.1991.1.1` (snChassis) 45, `.1991.1.2` (snSwitch — mostly LLDP-MED extension tables and VLAN/port config) 991, `.1991.1.3` (per-port stats/config, `snAgentXxx` tables) 8,169 — the largest, dominated by columns `.1991.1.1.3.3` (3,529 entries) and `.1991.1.1.3.14` (2,775 entries), consistent with per-port-per-something (e.g. per-port-per-priority queue) counters across many ports. PoE strings (`Power supply 1 (AC - PoE) present, status ok`, per-module PoE descriptions) live under `.1991.1.1.2` and `.1991.1.1.1`.
- **Writable objects:** not tested — no `snmpset` was issued per the read-only rule. The FOUNDRY-SN-AGENT-MIB declares several read-write objects (module/port config) by convention; none were probed.
- **Traps/informs:** `show snmp server` lists a full set of enabled trap categories (cold/link up/down, auth failure, STP, MAC notification, etc.) but **"Total Trap-Receiver Entries: 0"** — trapping is enabled but no receiver is configured, so nothing is actually sent anywhere.

## CLI / configuration model

- **Shell type:** IOS-like (Brocade/Foundry FastIron style), three prompt levels observed and matching the runbook's documented shapes: `SSH@LABSW06>` (user EXEC), `SSH@LABSW06#` (privileged EXEC after `enable`), `(config)#` not entered (config mode was never used, per the read-only rule).
- **Login:** `enable` from user EXEC needs no password (`"No password has been assigned yet..."`) — matches the first-write runbook.
- **Pager:** default pager string is `--More--, next page: Space, next line: Return key, quit: Control-c`. **`skip-page-display` only works from privileged EXEC (`#`), not user EXEC (`>`)** — the first capture attempt issued it at `>` and got `Invalid input ->skip-page-display / Type ? for a list / Node doesn't exist`; issuing it after `enable` returned `Disable page display mode` and paging stayed off for the rest of the session, including `show running-config` and the 4000-line dynamic log buffer.
- **Commands that don't exist on this firmware** (return `Invalid input -><token> / Type ? for a list / Node doesn't exist`): `show chassis` (use `show media`... no — see below), `show web-management`, `show ssh` (use `show ip ssh config`), `show licenses`. The brief's list of candidate commands was a superset; the working subset is recorded below.
- **Session limits:** confirmed practically — the runbook's ICX7150 fact ("FastIron limits concurrent SSH sessions, unclean exit causes the next login to fail with `Permission denied`") held here too: a first capture script mis-sequenced `skip-page-display` before `enable`, desynced against a pager prompt, and had to be killed rather than exited cleanly. A retry immediately after (`nc -z` polling showed port 22 open throughout) worked on the first attempt with no `Permission denied` — so either the kill was clean enough at the TCP level, or this device's session-limit trip threshold is more forgiving than the runbook's warning implies; only one clean session was open at any time throughout this capture, as required.
- **`show who` / `show stack`:** confirm exactly one active SSH session (the capturing session itself, from `10.20.0.207`), Telnet server disabled, and a single-unit "stack" (`stack is not enabled`, one unit, standalone).
- **How config is read:** `show running-config` (plain text, streamed, no JSON/XML form).
- **How config is applied:** *inferred, not tested here* — the first-write runbook already established line-by-line immediate apply to running-config, `write memory` for startup, and no candidate/commit or rollback support. Not re-verified in this read-only run.
- **Export/import:** not tested (would require a write-capable operation); `copy running-config tftp` is documented in the runbook context but not exercised here.
- **Host-key/kex algorithms offered (from `show ip ssh config` and `ssh -vv`):** `show ip ssh config` reports `Host Key: RSA 3072, ECDSA 384`; encryption `aes256-cbc,aes192-cbc,aes128-cbc,aes256-ctr,aes192-ctr,aes128-ctr,3des-cbc`; login timeout 120s; SCP enabled; client/server rekey at 500000K/30m. The live `ssh -vv` negotiation (client OpenSSH 10.3) picked KEX `sntrup761x25519-sha512@openssh.com` and host key `ecdsa-sha2-nistp384` from a server offer of `rsa-sha2-512,rsa-sha2-256,rsa-sha2-512,rsa-sha2-256,ecdsa-sha2-nistp384` (RSA listed twice, no ed25519) — raw negotiation in `_raw/labsw06/ssh-vv.txt`.
- **`show version`-equivalent output (sanitized):**

  ```text
  Copyright (c) Ruckus Networks, Inc. All rights reserved.
    UNIT 1: compiled on Jul 30 2026 at 17:58:03 labeled as SPR10010g_cd10
      (33554432 bytes) from Primary SPR10010g_cd10.bin (UFI)
        SW: Version 10.0.10g_cd10T213
      Compressed Primary Boot Code size = 786944, Version:10.2.12T225 (mnz10212)
       Compiled on Wed Jul  9 10:01:21 2025

    HW: Stackable ICX7150-24-POE
  ==========================================================================
  UNIT 1: SL 1: ICX7150-24P-2X10G_2X1G POE 24-port Management Module
        Serial  #:FEA3220S085
        Software Package: ICX7150_BASE_L3_SOFT_PACKAGE
        Current License: 2X10G
        P-ASIC  0: type B160, rev 11  Chip BCM56160_B0
  ==========================================================================
  UNIT 1: SL 2: ICX7150-2X1GC 2-port 2G Module
  ==========================================================================
  UNIT 1: SL 3: ICX7150-4X10GF 4-port 40G Module
  ==========================================================================
   1000 MHz ARMv7 Cortex-A9 processor 88 MHz bus
      8 MB boot flash memory
      2 GB code flash memory
      1 GB DRAM
  ```

## Web / API

- **Login flow:** not exercised (GET only, per the read-only rule). The root page (`/`, 519 bytes) is a bootstrapped Angular/React-style SPA (`Commscope ICX` title) that pulls `main.js` (1.68 MB), Highcharts, and Bootstrap CSS; the actual login form is client-rendered, not visible from a raw GET.
- **Session/cookie/CSRF:** not observed — no `Set-Cookie` on the unauthenticated GETs captured (`https-root.txt`). Response headers include `Access-Control-Allow-Origin: https://172.16.0.6`, `X-Frame-Options: DENY`, `Strict-Transport-Security` (note: the header name itself has a stray trailing colon — `Strict-Transport-Security::` — as served).
- **JSON/XML endpoints discovered:** `/api/` is a live backend (200, distinct body from the SPA). Route names harvested from strings in `main.js` (likely SPA client-side routes, not confirmed server endpoints): `/dashboard`, `/getrunconfig`, `/global`, `/infra`, `/lag`, `/layer3`, `/lldp`, `/polling`, `/ports`, `/routing`, `/smartzone`, `/stack`, `/syslog`, `/vlan`, `/access`, `/dns`, `/aaaservers`, `/backuprestore`, `/upload`, `/timeout`, `/connectionissue`. Unauthenticated GETs to `/api/<name>` (e.g. `/api/dashboard`, `/api/getrunconfig`) all return a bare `500 Internal Server Error` rather than `401`/`403` — the backend appears to throw on missing auth/session state instead of refusing cleanly. No endpoint returned structured JSON to an unauthenticated GET.
- **Backup/restore:** `/backuprestore` appears as an SPA route name in `main.js`; the FastIron CLI equivalent (`copy running-config tftp`, per the first-write runbook) was not exercised.

## Feature inventory (as observed)

| Feature | Observed state |
| --- | --- |
| VLANs | 4 configured: VLAN 666 "BREACH-TARGET" (tagged on ports M2/1-2 only, unusual name, observed as-is — not investigated further under the read-only rule), VLAN 1000 "VLAN1000" (tagged, spans all 24×1G + 2×1G + 4×10G + LAG 1 — the management/data VLAN, carries `ve 1000` at 172.16.0.6/24), VLAN 4000 "DEFAULT-VLAN" (untagged, same port set), plus the reserved single-spanning-tree VLAN |
| LAG | 1 LAG (`lg1`, dynamic/LACP, "UPLINKSFP"), members `1/3/2` (up) and `1/3/4` (down), LACP system priority 1, long/short timeout 90/3s, key 20001; partner system ID `18fd.74e2.6e84` — matches the LLDP neighbor "Lab_SW01" |
| STP | RSTP (802.1w) active on VLAN 4094 (single spanning-tree instance covering VLANs 666/1000/4000); root bridge `10000cea1478f204` reached via `lg1`; local ports `1/1/12`, `1/3/1` designated-forwarding, `1/3/2`/`1/3/4`/`lg1` root-forwarding |
| LLDP | Enabled; TX/RX interval 30s, hold 4, reinit 2, TTL 120s (from `lldpMIB` config scalars); 2 neighbors seen (see Discovery signals) |
| PoE | 370,000 mW total budget, 0 mW currently allocated/consumed on any port — no PD detected on any of the 24 PoE-capable ports at capture time; port `1/1/12` (the Kali LLDP neighbor's link) shows `Non-PD` with fault `non-standard PD` |
| Port mirroring | Not checked (not in the requested command set) |
| ACL | Not checked |
| QoS | Not checked (no `show qos` issued) |
| IGMP snooping | Not checked |
| DHCP snooping | Not checked |
| 802.1X | Not checked |
| Routing | L3 interface `ve 1000` only, address `172.16.0.6/24` via DHCP client on the VE; no static routes examined |
| IPv6 | Not configured on `ve 1000` (IPv4-only entry in `ipAddressTable`); not otherwise probed |
| NTP | **Not synchronized** — `show ntp status`: "Clock is unsynchronized, no reference clock", NTP client/server/master all disabled. The device's own log timestamps (`Jul 30 2026...`) reflect a clock that free-runs from a cold-start reference rather than wall-clock time |
| Syslog | Local buffer only (4000-line dynamic buffer, 48 messages logged at capture time); syslog trap category is **Disabled** in `show snmp server`'s trap list, and no syslog server/relay was found configured |
| SNMP traps | Trap categories broadly enabled but **0 trap-receiver entries** — nothing configured to receive them |
| RADIUS/TACACS | Not checked |
| Firmware upgrade | Mechanism not exercised; `show flash`/`show media` show dual (primary/secondary) image slots, consistent with a standard TFTP/SCP image-copy-then-reload flow, but this was not tested |

## Discovery signals

- **LLDP (what LABSW06 advertises about itself):** system name `LABSW06`, capabilities bridge+router, chassis ID = base MAC `38:45:3B:0F:CB:C0`, management address `172.16.0.6`.
- **LLDP (what LABSW06 sees from neighbors) — the only two real neighbors observed:**
  - Local port `1/1/12` ↔ remote chassis `78:01:5A:B0:05:00`, port `78:01:5A:B0:05:01` (port ID type MAC, port description `eth1`), system name **`labtest`**, system description `Kali GNU/Linux Rolling Linux 7.0.12+kali-amd64 ... x86_64`, capabilities WLAN-AP+router (enabled), management addresses `172.16.0.21` (v4) and an fd87:... ULA (v6). This matches the lab-network memory fact that `.20`-range hosts include a Kali box; here it's reachable at `172.16.0.21` over this port.
  - Local port `1/3/2` (LAG `lg1` member) ↔ remote chassis `18:FD:74:E2:6E:78`, port ID (interface name) `bridge/bond-icx/sfp-sfpplus13`, system name **`Lab_SW01`**, system description `MikroTik RouterOS 6.49.20 (long-term) CRS317-1G-16S+`, capabilities bridge+router (enabled) — this is the far end of the LAG uplink, a MikroTik CRS317-1G-16S+ switch/router not previously documented in this repository's lab-network memory.
- **HTTP/TLS banner:** `Server: nginx`; TLS cert CN `SN-FEA3220S085` (== the device serial) issued by `RuckusPKI-DeviceSubCA-1` — this pattern (CN is the serial, issued by a vendor device sub-CA) is itself a discoverable fingerprint for Ruckus/CommScope ICX devices on a network scan, independent of any SNMP/CLI access.
- **SNMP sysDescr with `public`:** answers immediately and fully — `public` alone is enough to fingerprint vendor, model, and exact firmware build from an unauthenticated scan.
- **MAC OUI:** base MAC `38:45:3B:xx:xx:xx` (Ruckus/CommScope-registered block, consistent with the vendor identification above).
- **mDNS/SSDP:** not probed (out of scope for the command set exercised; would need a UDP multicast listener, not attempted).
- **Security log evidence of prior/external activity (from `show logging`):** repeated `SNMP: Auth. failure, intruder IP: 10.20.0.207` entries and `sshd: Failed publickey SSH access by user admin from 10.20.0.207` — both are this capture session's own SNMPv3 probing and SSH key-based connection attempts (this host's IP), not a third party; recorded here because they are a good illustration of how visible failed-auth attempts are in the device's own buffer.

## What FlowSeer needs from this device

- **Inventory/identity:** SNMP v1/v2c with `sysDescr`/`sysObjectID`/`entPhysicalTable` is sufficient and fast (entPhysicalTable alone: 1,734 varbinds in 16s) — no need to touch the much larger enterprise tree for basic inventory.
- **Config/CLI plane:** must speak FastIron's IOS-like dialect specifically — command availability is a strict subset of generic "show" commands (`show chassis`, `show web-management`, `show ssh`, `show licenses` all fail on this firmware/model). An adapter needs a per-command capability table rather than assuming a common show-command surface across FastIron devices.
- **Pager handling:** the adapter must issue `enable` before `skip-page-display` — sending it at user EXEC silently fails with no side effect other than an `Invalid input` message, which is easy to swallow accidentally in an adapter that doesn't check for it explicitly.
- **SNMPv3 is a prerequisite, not a given:** this device has no SNMPv3 user at all today (confirmed both by the v3 "Unknown user name" answers and directly by `show snmp user`/`show snmp group` being empty). Any policy that mandates authPriv-only SNMPv3 (as FlowSeer's device layer does, per the first-write runbook) needs an out-of-band provisioning step for a freshly reset or newly onboarded ICX7150 before FlowSeer can read it at all.
- **LLDP topology discovery works well here** and is cheap (2.4k varbinds, ~22s) — worth preferring over CLI `show lldp neighbors detail` parsing for topology, since the MIB is at the IEEE OID (`1.0.8802.1.1.2`), not the `mib-2.99` alias some tooling assumes.
- **No LAG-MIB, no Power-Ethernet-MIB:** LAG and PoE state require either CLI parsing (`show lag`, `show inline power`) or the vendor FOUNDRY-SN-AGENT-MIB enterprise tree; standard IETF MIBs for both are unimplemented on this firmware. An integration that assumes `ieee8023adMIB`/`powerEthernetMIB` availability will silently get nothing back (clean "No Such Object", not a timeout) and needs a fallback path.
- **Clock is not trustworthy:** NTP is disabled and unsynchronized, so any timestamp FlowSeer reads from this device's own logs or LLDP/SNMP timers is relative to an arbitrary cold-start reference, not wall-clock time — correlating device-reported times with FlowSeer's own observation timestamps needs an explicit skew correction or must avoid relying on device-side timestamps altogether.
- **The device's own HTTPS management API is not a safe integration target as observed:** unauthenticated GETs to `/api/*` return generic 500s instead of clean auth errors, suggesting the backend doesn't validate session state defensively; FlowSeer should not build on this surface without first confirming (through a controlled, authenticated test) that it behaves correctly rather than leaking stack traces or crashing under load.

## Raw evidence

All under `docs/research/device-inventory/lab/_raw/labsw06/`, passwords/community strings scrubbed:

- `port-sweep.txt` — TCP port sweep (22/23/80/443/830/8291/8728/9339)
- `ssh-vv.txt` — `ssh -vv` banner and full KEX/host-key/cipher negotiation (no auth attempted)
- `ssh-Q-kex-local.txt`, `ssh-Q-key-local.txt` — local client's `ssh -Q kex`/`ssh -Q key` support lists, for comparison
- `https-root.txt` — HTTPS root page headers + body
- `main.js-string-extract.txt` — quoted-string strings extracted from the SPA's `main.js` (the 1.68 MB minified file itself was not kept)
- `tls-cert.txt`, `tls-raw.txt`, `tls-stderr.txt` — TLS handshake, certificate (`openssl x509 -text`), and `s_client` session detail
- `api-probe.txt`, `api-root-body.txt`, `api-dashboard-body.txt` — unauthenticated GET probes against `/api/*` and comparison paths
- `snmp-auth-matrix.txt` — every SNMP version/community/user/auth/priv combination tried, with its exact result
- `snmpwalk-mib2.txt` (+ `.err`) — full `mib-2` walk, 13,635 varbinds, numeric OIDs
- `entPhysicalTable.txt` (+ `.err`) — full `entPhysicalTable` walk, 1,734 varbinds
- `lldpMIB.txt` — full LLDP-MIB walk (`1.0.8802.1.1.2`), 2,358 varbinds
- `lagMIB.txt` — probe of the standard `ieee8023adMIB` OID (empty; not implemented)
- `snmpwalk-enterprise.txt` (+ `.err`) — full FOUNDRY-SN-AGENT-MIB walk (`1.3.6.1.4.1.1991`), 9,291 varbinds
- `cli-session.txt` — the single reused SSH CLI session transcript (password hash and encrypted SNMP community redacted; no plaintext password present)
