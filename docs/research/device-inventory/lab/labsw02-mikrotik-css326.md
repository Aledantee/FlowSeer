---
title: Lab device — LABSW02 (172.16.0.2)
date: 2026-09-10
scope: MikroTik CSS326-24G-2S+ running SwOS v2.18; measured on the live device
status: research; read-only capture, no device changes made
---

# LABSW02 (172.16.0.2)

Every fact below was read from the device on the date above. Anything inferred
rather than observed is marked *inferred*. No `snmpset`, HTTP write, CLI
config command, or reboot was issued against this device; the only near-write
action was fetching `/backup.swb`, which the device produces read-only.

## Identity

| Field | Value |
| --- | --- |
| Vendor / model | MikroTik CSS326-24G-2S+ (`brd` field decodes to `CSS326-24G-2S+`) |
| Firmware / software version | SwOS v2.18 (`ver` field decodes to `2.18`) |
| Serial | `HCQ08CFCZBS` (matches SNMP `14988.1.1.7.3.0` and HTTP `sys.b` field `sid`) |
| sysObjectID | `.1.3.6.1.2.1.1.2.0` = `.1.3.6.1.4.1.14988.2` (MikroTik enterprise root `14988`, sub-node `2`; no public MIB text resolved the leaf name — *inferred* to be a generic "MikroTik switch/SwOS" identity node, not a per-model OID) |
| sysDescr | `CSS326-24G-2S+ SwOS v2.18` |
| Hostname | `LABSW02` (SNMP `sysName`, and HTTP `sys.b` field `id` decodes to `LABSW02`) |
| MAC / base MAC | `18:FD:74:2B:28:29` (dot1dBase `dot1dBaseBridgeAddress`, matches `sys.b` field `mac` and port 1's `ifPhysAddress`) |
| Uptime at capture | SNMP `sysUpTime` = 75236 ticks = 0:12:32 at the time of the full walk; HTTP `sys.b` field `upt` = `0x00018309` = 99081, scaled /100 by the UI's own decoder = 990.81 s (~16:31) a few minutes later — consistent, not a discrepancy |

## Management surfaces observed

| Surface | Port | Reachable | Auth that worked | Notes (banner, TLS cert subject, version string) |
| --- | --- | --- | --- | --- |
| SSH | 22 | No | — | `nc -z` closed/filtered; no listener |
| Telnet | 23 | No | — | `nc -z` closed/filtered; no listener |
| HTTP UI | 80 | Yes | HTTP Digest, user `admin` | `HTTP/1.0`; no `Server:` header at all; `GET /` → `303 Use Instead`, `Location: /index.html` (SPA catch-all — every unknown path, e.g. `/api`, `/rest`, `/cgi-bin`, `/status`, `/login.html`, redirects the same way); `WWW-Authenticate: Digest realm="CSS326-24G-2S+", qop="auth", nonce=…, stale=FALSE`; realm string equals the model name |
| HTTPS UI | 443 | No | — | `nc -z` closed/filtered; no TLS listener at all |
| REST / JSON API | — | No | — | No separate REST surface; the `.b` endpoints (see below) are the only structured API and are reached via plain GET under Digest auth, content-type `application/x-javascript` |
| SNMP | 161/udp | Yes | v1 and v2c with community `tegi` | v1 and v2c/`tegi` both answered identically (SwOS answers SNMPv1 too, not just v2c); v2c/`public` → net-snmp reports `Timeout: No Response` (community silently dropped, not `authorizationError` — no SNMPv2 report PDU); SNMPv3 (noAuthNoPriv, authNoPriv SHA) → `snmpget: Timeout` for every v3 attempt — consistent with the known fact that SwOS has no SNMPv3 engine at all (it never responds, rather than rejecting) |
| NETCONF / RESTCONF / gNMI | 830 / 443 / 9339 | No | — | All closed/filtered; not applicable to this class of device |
| Other (discovery, TFTP, LLDP, CDP) | — | n/a (read-only, not probed live) | — | The UI exposes a "Mikrotik Discovery Protocol" toggle (`sys.b` field `pdsc`, enabled on all ports) — this is MNDP, MikroTik's proprietary L2 discovery broadcast, not LLDP or CDP; no LLDP MIB is present (see SNMP capabilities) |

## SNMP capabilities

- Full walk: `snmpwalk -On -v2c -c tegi -t 3 -r 1 172.16.0.2 .1` returned **1,662 varbinds in ~5 seconds** wall time, in a single pass with default GetBulk behavior (net-snmp's automatic max-repetitions; no truncation, no errors, no need to walk subtrees separately). Raw: `_raw/labsw02/snmpwalk-full.txt`.
- Standard MIBs answered (varbind counts, from the single full walk):
  - `system` (`.1.3.6.1.2.1.1`) — 20 varbinds (sysDescr, sysObjectID, sysUpTime, sysContact empty, sysName, sysLocation empty, sysServices=2, sysORTable with 3 rows: SNMPv2-MIB, IF-MIB, BRIDGE-MIB)
  - `interfaces` (`ifTable`, `.1.3.6.1.2.1.2`) — 547 varbinds; `ifNumber` = 26 (24× GE copper Port1–Port24 + SFP1/SFP2)
  - `ifXTable` (`.1.3.6.1.2.1.31`) — 495 varbinds; `ifName` mirrors `ifDescr` (`Port1`…`Port24`, `SFP1`, `SFP2`)
  - `dot1dBase` (`.1.3.6.1.2.1.17.1`) — 3 varbinds (bridge address, 26 ports, type=2 transparent-only)
  - `dot1dTpFdbTable` (`.1.3.6.1.2.1.17.4`) — 153 varbinds (learned MAC/FDB entries)
  - `dot1dStp` (`.1.3.6.1.2.1.17.2`) — 300 varbinds (RSTP state, per the `rstp.b` HTTP data below)
  - MikroTik enterprise (`.1.3.6.1.4.1.14988`) — 14 varbinds total: `14988.1.1.3.11.0` = INTEGER 540 (unlabelled, *inferred* health/temperature-adjacent counter, not resolvable without the vendor MIB), `14988.1.1.7.3.0` = serial `HCQ08CFCZBS`, `14988.1.1.7.4.0` = firmware `2.18`, and an 11-varbind SFP table (`14988.1.1.19.1.1.{2,5,6,7,8}.{25,26}`) covering SFP1/SFP2 name, temperature, voltage, Tx/Rx power
- **Not present at all** (0 varbinds, confirmed by grep over the full walk, not separately re-walked): `ipAddrTable`/`ipNetToMedia` (`.1.3.6.1.2.1.4.20/22/34/35`), `entPhysicalTable` (`.1.3.6.1.2.1.47.1.1`), `lldpLocalSystemData`/`lldpRemTable` (`.1.3.6.1.2.1.109`), `dot1qVlanStaticTable`/`dot1qTpFdbTable` (Q-BRIDGE MIB, `.1.3.6.1.2.1.17.7`), `powerEthernetMIB` (`.1.3.6.1.2.1.105`), `hrSystem` (`.1.3.6.1.2.1.25.1`). SwOS's SNMP agent is intentionally minimal: system/IF-MIB/BRIDGE-MIB (STP+FDB) plus a thin enterprise slice for serial/firmware/SFP. **VLAN configuration is not visible over SNMP at all** — it only exists in the HTTP `.b` API (see below).
- GetBulk max-repetitions: not explicitly tuned; net-snmp's default worked cleanly for the whole tree in one pass, no truncation observed.
- Writable objects: none tested (`snmpset` not run — hard rule). The MIBs answered are all read-only tables (IF-MIB, BRIDGE-MIB, system) in this agent; SwOS's configuration surface is the HTTP `.b` API, not SNMP SET.
- Traps/informs: not configured/visible; no trap-target MIB objects present.

## CLI / configuration model

**No CLI exists on this device.** No SSH (22) or Telnet (23) listener was reachable, and the brief's known facts confirm this ahead of the probe. There is no `show`/`display` command surface at all — SwOS is HTTP-UI-only, backed by a set of binary/JS-object-literal endpoints under `/`.

## Web / API

### Login flow
- HTTP Digest auth (RFC 2617/7616 style, `qop="auth"`), realm = the model string `CSS326-24G-2S+`, user `admin`. No cookies, no CSRF token, no session state observed — every request re-authenticates via Digest headers.
- `GET /` and `GET /index.html` require no auth and return the SPA shell (gzip-compressed `text/html`, ~44.7 KB decompressed). All JavaScript is inline in a single `<script>` block in `index.html` (no external `.js` files referenced) — saved as `_raw/labsw02/index.html` and its extracted script body as `_raw/labsw02/inline.js`.
- Every data endpoint (`.b` suffix) requires Digest auth and returns `401` first, then `200` with `Content-Type: application/x-javascript`, `Expires: 0` on the authenticated retry.

### The `.b` endpoints — encoding observed

Format: a JS object-literal-like text (not JSON — unquoted keys, single-quoted hex strings, `0x`-prefixed hex numbers, no trailing commas issue). Confirmed shape:

- **String fields** are single-quoted and hold the ASCII text **hex-encoded byte-by-byte**, no separators, e.g. `id:'4c414253573032'` → hex bytes `4c 41 42 53 57 30 32` → ASCII `LABSW02`. Same pattern decodes `sid` → `HCQ08CFCZBS`, `brd` → `CSS326-24G-2S+`, `ver` → `2.18`, `nm` (port names) → `Port1`…`Port26`/`SFP1`/`SFP2`.
- **Numeric fields** are `0x`-prefixed hex integers, generally the plain hex of the underlying value (JS's `toString(16)`, left-padded to even length by the encoder in `inline.js` function `Pa`).
- **Bitmask fields** over the 26 physical ports use a 32-bit mask per feature, e.g. `en:0x03ffffff` = all 26 ports (bits 0–25) enabled; `allp:0x03ffffff` likewise.
- **Per-port array fields** are JS arrays indexed 0–25 for ports 1–26 in order, e.g. `link.b`'s `spd:[0x07,0x07,…]` (one entry per port).
- **MAC address fields** are 6-byte hex strings, same ASCII-hex-of-bytes convention, e.g. `lacp.b`'s `mac:['000000000000',…,'18fd74e26e86','18fd74e26e86']` (LAG partner MAC for the two SFP ports, `00…00` = no LACP partner on copper ports).
- **IPv4 fields** are packed as a 32-bit hex value with the **octets in reverse (little-endian) byte order**: `sys.b`'s `cip:0x020010ac` reads as bytes `02 00 10 ac`, reversed → `ac.10.00.02` = `172.16.0.2` (the device's own management address). `sys.b`'s `ip:0x0158a8c0` similarly decodes to `192.168.88.1` (the configured "static IP" fallback field, unrelated to the lab addressing — MikroTik's factory default gateway, *inferred* leftover default, not actively used since `iptp` shows the device is DHCP-with-fallback and the lab actually assigned `172.16.0.2`).
- Array/list endpoints (`vlan.b`, `host.b`, `acl.b`) are `[{...}, {...}]` — a JSON-like array of the same object-literal record shape; empty as `[]`.

### How the UI applies settings (read from `inline.js`, not exercised)
- Reads: `XMLHttpRequest.open("GET", url, true)` against the same `.b` path that supplied the form's current values (function `Ya`/`pb` pattern), `responseType` left default (text), then parsed with the same object-literal decoder.
- Writes: the "Apply All" button calls `Va(a)` → builds the changed payload with `a.h.save(a)`, then `Ua(a, b)` → `P(a.h.url, b, callback)`. `P` (`inline.js` line ~14) does `new XMLHttpRequest; e.open("POST", a, true); e.setRequestHeader("Content-Type","text/plain"); e.send(b)` — **a plain POST to the same `.b` URL the data was read from, body `text/plain`, in the same encoding described above**, containing only the changed fields (the payload builder in `Sa`/`Pa` walks the modified rows). The status callback shows `"Applying changes..."` then either success or `"Could not apply changes"` / `"Lost connection"`.
- `resetstats`, `reseterrs`, `resethist` are separate fire-and-forget `POST` endpoints (`/resetstats`, `/reseterrs`, `/resethist`) with body `"*"` — not GET-able, which explains why probing `!stats.b` as a bare GET only ever returned `303 Use Instead` (it's not meant to be fetched that way; the live per-second table is `stats.b` itself, refreshed by re-GETting it).
- Backup: `POST /backup.swb` (button "Save Backup", function `Cb`) triggers a browser download of the current `GET /backup.swb` response — **the backup mechanism is simply "GET the same file the browser would download"**, no separate export step. Confirmed by fetching it directly with `curl --digest` (see Backup/restore below).
- Upgrade: a distinct `POST /upgrade` endpoint exists (function referenced at `inline.js` line ~29) for firmware upload — **not exercised**, read-only rule.

### Endpoints that did not return data (GET, Digest-authenticated, confirmed with a fresh nonce each time — not a caching artifact)
`poe.b`, `dhost.b`, `igmp.b`, and `!igmp.b`/`!stats.b` (URL-encoded `%21` form also tried) all returned `303 Use Instead → /index.html` instead of `200`. Cross-checked against `inline.js`:
- `poe.b` — the UI only renders the "PoE" tab when the model has PoE output (`Z(!1, …)` guards in the "PoE" and "PoE Out" sections evaluate false for this hardware); the CSS326-24G-2S+ is a non-PoE model, so the endpoint is not served at all. Confirmed independently by `sys.b`'s `npoe:0x00`.
- `dhost.b` and `!igmp.b` are the "live" (dynamic) discovered-hosts and IGMP-group tables (note the `!` prefix the JS actually uses, e.g. `Ob("!dhost.b", …)`, `Ob("!igmp.b", …)`); every variant attempted (`dhost.b`, `igmp.b` without the bang, and `%21dhost.b`/`%21igmp.b` with it) redirected. *Inferred*: these likely require a request header/referer the SPA sends that `curl` did not (or SwOS serves them only from an active browser session context) — not confirmed further, since this is read-only exploration and not worth extra load on the device.
- `host.b` (**static** hosts) and `acl.b` both answered `200` with body `[]` — both tables are empty on this device.

### Backup/restore
- `GET /backup.swb` (Digest auth) → `200`, `Content-Type: application/octet-stream`, 3,215 bytes as served, plain ASCII text (not actually binary): a concatenation of `<endpoint>.b:{...}` sections — `vlan.b:`, `lacp.b:`, `pwd.b:`, `snmp.b:`, `rstp.b:`, `link.b:`, `fwd.b:`, `sys.b:`, `acl.b:`, `host.b:` — each holding the same object-literal payload the live endpoint returns.
- **The backup includes a `pwd.b:{pwd:'<hex>'}` section containing the admin password, hex-encoded in cleartext** (decoded and confirmed to equal the device's actual admin password from the credentials file, then immediately scrubbed from the saved raw copy — see Raw evidence). This is a real operational finding: anyone who can Digest-auth to the device can retrieve its plaintext admin password via `/backup.swb`, and any backup file this device produces must be handled as a secret.
- No separate restore endpoint was probed (`POST /backup.swb` uploads a *new* backup to apply — not exercised, per the read-only rule).

## Feature inventory (as observed)

| Feature | Observed state |
| --- | --- |
| VLANs | 1 configured: `nm=Lab`, `vid=1000` (`0x3e8`), all 26 ports member (`mbr:0x03ffffff`), learning on, port isolation off, IGMP snooping off. Per-port VLAN mode (`fwd.b`'s `vlan[]`): "optional" (1) on Port1–24, "enabled" (2) on SFP1/SFP2; VLAN receive = "any" on every port; default VLAN ID = 1000 on every port. |
| LAG | `lacp.b`: mode = "passive" on all 26 ports (default, unconfigured); SFP1/SFP2 (`grp`/`sgrp`=1) are grouped into LAG group 1 with a live partner MAC `18:fd:74:e2:6e:86`; ports 1–24 ungrouped, no partner. |
| STP mode | RSTP enabled on all 26 ports (`rstp.b` `ena:0x03ffffff`, `rstp` mode field also all-ports=RSTP). SFP1/SFP2 show `role`=2 (root) with non-zero root path cost `0x7dc`=2012 and forwarding state; the 24 copper ports show `role`=3 (designated), cost 0, discarding/idle — consistent with the lab's uplink topology going out the SFP ports. Bridge priority read from `sys.b` (`prio:0xf000`=61440 default, `cost` mode "short"). |
| LLDP | Not supported at all — no LLDP MIB, no LLDP UI tab. Only "Mikrotik Discovery Protocol" (MNDP), a proprietary MikroTik L2 broadcast discovery mechanism, exists (`sys.b` field `pdsc`, enabled on all ports). |
| PoE | Not present on this hardware (`sys.b` `npoe:0x00`; UI hides the PoE tabs; `/poe.b` unreachable). |
| Port mirroring | Configured but currently disabled: `fwd.b` `imr`(ingress)=0, `omr`(egress)=0, `mrto`(mirror-to port)=1 (would be Port1 if enabled). |
| ACL | Empty (`acl.b` → `[]`). ACL fields decoded from `inline.js` support MAC/VLAN/IP 5-tuple match + redirect/mirror/drop/rate-limit/re-tag actions, but nothing is configured. |
| QoS | Only a global "Ingress Rate" limiter field per port exists (`fwd.b` `ir[]`, all 0 = unlimited); no separate QoS/queueing subsystem observed. |
| IGMP snooping | Globally off (`sys.b` `igmp:0x00`, `igmq`/querier off, IGMP version field present but snooping disabled); per-VLAN IGMP snooping also off (`vlan.b` `igmp:0x00`). Live group table (`!igmp.b`) unreachable via plain GET (see above), consistent with snooping being off (nothing to show). |
| DHCP/PPPoE snooping | Configured globally trusted on all ports (`sys.b` `dtrp:0x03ffffff`), Add Information Option (Option 82) off (`ainf:0x01` actually shows enabled — worth re-checking against the UI label meaning before relying on it operationally; recorded as observed, not independently verified beyond the raw field). |
| 802.1X | No fields for 802.1X found anywhere in `inline.js` — not supported on SwOS. |
| Routing (L3) | None — SwOS is a pure L2 switch; no `ipAddrTable`, no static routes UI. |
| IPv6 | Not present in any endpoint or the SNMP walk. |
| NTP | Not present — no NTP fields in `sys.b` or elsewhere; SwOS has no clock/NTP client. |
| Syslog | Not present — no syslog config surface found. |
| SNMP traps | Not configured (SNMP MIB walk shows no trap-target objects); `snmp.b` only exposes enable/community/contact/location (`en:0x01, com:'tegi', ci:'', loc:''`). |
| RADIUS/TACACS | Not present — no AAA fields anywhere. |
| Firmware upgrade | `POST /upgrade` endpoint referenced in the UI (not exercised) plus a separate "Manual Upgrade" flow (`Lb` function, not decoded further — out of scope for a read-only capture). |

## Discovery signals

- SNMP with a guessed/public community gets **no response at all** (not a rejection) — a scanner using only `public` will conclude SNMP is closed on this device; only `tegi` (or, interestingly, plain SNMPv1 with the same community) reveals it.
- `sysDescr` via a working community string cleanly identifies vendor+model+firmware in one string: `CSS326-24G-2S+ SwOS v2.18`.
- MAC OUI `18:FD:74` is MikroTik's registered OUI (all 26 ports' `ifPhysAddress` share this prefix, incrementing by port).
- HTTP: no `Server:` header at all (a scanner gets no banner from the HTTP response headers), but the Digest `realm=` header leaks the exact model string (`CSS326-24G-2S+`) even to an unauthenticated `GET`.
- No mDNS/SSDP probed (out of scope of the brief's explicit endpoint list; the only broadcast-discovery mechanism this device offers is MNDP, MikroTik-proprietary, not standard mDNS/SSDP).

## What FlowSeer needs from this device

- **Inventory/identity**: SNMP v2c (or even v1) with the device's SNMP community is sufficient for identity (`sysDescr`, `sysName`, `sysObjectID`, MAC) and interface inventory (`ifTable`/`ifXTable`, 26 ports). No entity MIB, so physical inventory (serial, firmware) must come from the MikroTik enterprise leaves `14988.1.1.7.3.0`/`14988.1.1.7.4.0` instead of the standard `entPhysicalTable`.
- **Configuration (VLANs, LAG, STP, forwarding, ACL, SNMP settings)**: none of this is visible over SNMP. FlowSeer must speak the HTTP `.b` API with Digest auth to read or change VLANs, port isolation, mirroring, LAG, RSTP, and ACLs — there is no CLI and no config-file export/import beyond the `.b`-shaped backup. A client needs: (1) an HTTP Digest auth implementation, (2) an encoder/decoder for the hex-string / `0x`-hex-number / bitmask-per-port object-literal format described above (not JSON — needs a small custom parser), (3) knowledge that IPv4 fields are byte-reversed 32-bit hex.
- **Telemetry**: `stats.b` (throughput, packet counts, errors, histograms) and `sfp.b` (optical diagnostics) are the richest per-port telemetry sources and refresh live (the UI polls every 1 s); SNMP's IF-MIB counters (`ifXTable`) cover basic byte/packet counters too and are cheaper to poll at scale, so FlowSeer should prefer SNMP for routine polling and reserve the HTTP `.b` endpoints for SFP diagnostics and anything SNMP doesn't expose (VLANs, ACLs, LAG partner MAC, mirror config).
- **Backup**: `GET /backup.swb` is the entire config-backup mechanism and returns the admin password in cleartext (hex-encoded) as part of the payload — FlowSeer must treat any stored backup of this device class as a secret, redact or strip the `pwd.b` section before persisting/displaying it, and never log the raw response.
- **Discovery**: SNMP `sysDescr` with the correct community is the only network-visible fingerprint this device offers cleanly; there is no LLDP to build a topology graph from, so cross-device topology for CSS326 switches will have to come from MAC-based FDB correlation (`dot1dTpFdbTable`, 153 entries, is available over SNMP) or from MNDP if FlowSeer ever adds a MikroTik-specific discovery listener — not from a standards-based neighbor protocol.
- **Failure semantics to handle**: v2c requests with a wrong community time out silently (no v2c-report PDU), and SNMPv3 requests against this device also just time out (no engine) — FlowSeer's SNMP client needs to distinguish "device unreachable" from "wrong/unsupported credentials" by trying the known-good community/version rather than relying on an explicit error response.

## Raw evidence

All under `docs/research/device-inventory/lab/_raw/labsw02/` (all captures scrubbed of the admin password; see below):

- `snmpwalk-full.txt` — full `snmpwalk -On -v2c -c tegi -t 3 -r 1 172.16.0.2 .1`, 1,662 varbinds, numeric OIDs.
- `index.html` — the SPA shell as served (curl `--compressed`, so decompressed on disk).
- `inline.js` — the SPA's single inline `<script>` body, extracted for reading (not modified).
- `sys.b.raw`, `link.b.raw`, `vlan.b.raw`, `fwd.b.raw`, `snmp.b.raw`, `rstp.b.raw`, `lacp.b.raw`, `host.b.raw`, `acl.b.raw`, `sfp.b.raw`, `stats.b.raw` — each with a matching `.headers` file; these are `curl -i --digest` captures (headers + body in one file), so each includes the initial `401` challenge followed by the authenticated `200` response.
- `poe.b.raw`/`.headers`, `dhost.b.raw`/`.headers`, `igmp.b.raw`/`.headers`, `bang-igmp.b.raw`/`.headers`, `bang-stats.b.raw`/`.headers` — the `303 Use Instead` responses for the endpoints that did not return data (see Web/API above for why).
- `backup.swb` — the fetched backup file. **Scrubbed**: the original `pwd.b:{pwd:'<15-byte hex-encoded admin password>'}` section was replaced with `pwd.b:{pwd:'REDACTED'}` before this file was written to disk under `docs/`; the unscrubbed bytes were never written anywhere under `docs/` and existed only transiently in this session's process memory during decoding.
- `backup.swb.headers` — the corresponding response headers.

No SSH/Telnet/TLS/NETCONF/RESTCONF/gNMI artifacts exist — none of those surfaces were reachable (see Management surfaces observed).
