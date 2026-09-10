---
title: Lab device — LABSW04 (172.16.0.4)
date: 2026-09-10
scope: LANCOM GS-2326+ (LCOS SX, Broadcom FASTPATH/Luton26-based), firmware 3.34.0326SU9; measured on the live device
status: research; read-only capture, no device changes made
---

# LABSW04 (172.16.0.4)

Every fact below was read from the device on the date above. Anything inferred
rather than observed is marked *inferred*.

## Identity

| Field | Value |
| --- | --- |
| Vendor / model | LANCOM Systems GS-2326+ (26-port Layer-2 managed gigabit switch, 2x TP/SFP combo) |
| Firmware / software version | v3.34.0326SU9 (BIOS v1.00, hardware-mechanical v1.01-v1.01); *sysDescr* reports build date 19.08.2024 |
| Serial | 4005284520000039 |
| sysObjectID | `.1.3.6.1.4.1.2356.800.3.2329` |
| sysDescr | `LANCOM GS-2326+ 3.34.0326 / 19.08.2024 4005284520000039` |
| Hostname | LABSW04 |
| MAC / base MAC | `00-a0-57-45-34-85` (Host MAC; matches entPhysicalTable and IPv6 link-local `fe80::2a0:57ff:fe45:3485`) |
| Uptime at capture | ~14-25 min across sessions (device rebooted cold shortly before this capture — see syslog: "Switch just made a cold boot") |

The underlying silicon family is Broadcom **Luton26** (`configArchLuton26 = 1` in the
web UI's `lib/config.js`), the same FASTPATH-derived platform LANCOM rebrands as
"LCOS SX". The CLI itself is LANCOM's own tree-structured shell, not literal
FASTPATH `show ...` syntax (see CLI section).

## Management surfaces observed

| Surface | Port | Reachable | Auth that worked | Notes (banner, TLS cert subject, version string) |
| --- | --- | --- | --- | --- |
| SSH | 22 | yes | password, user `admin` | Server software version `lcos-sx_v3.34.0326`; host key `ssh-rsa` SHA256:3mpNvV0CoeLCVEk6Y9q7PSju9jpLCeFgJH2NAevmmDM; only `password` auth offered (no pubkey) |
| Telnet | 23 | yes (banner only, not logged in) | not attempted (read-only rule) | IAC negotiation then `Username: ` prompt with `ESC[10C` cursor move |
| HTTP | 80 | yes at capture time | n/a | 301 redirect to `https://.../login.htm`; `Server: eCos Embedded Web Server`. Re-check during verification found port 80 refusing TCP connections (three attempts) while 443 still answered with an identical redirect body — device-side state changed between capture and verification; not reproduced as originally observed |
| HTTPS UI | 443 | yes | form login (not attempted — no credential submission needed for recon) | Self-signed cert, `O=LANCOM Systems, OU=Engineering, CN=GS-2326+`, notBefore 2011-01-01, notAfter 2030-12-31 (fixed placeholder validity, not device-specific); `HTTPS Minimum Protocol Version: TLSv1.2` (from CLI `https / show`); device clock stuck at 2011-01-01 (`Date:` header and `show log` timestamps both read 2011-01-01 — no NTP configured) |
| REST / JSON API | — | no dedicated API | — | No `/api`, `/rest`, `/cgi-bin` etc.; UI uses internal `config/<page>` GET/POST endpoints returning plain slash-delimited text (see Web/API) |
| SNMP | 161/udp | yes | v1 `public`, v2c `public` (read-only), v3 `tegi` authPriv SHA/AES | v2c `tegi` and v3 `tegi` at lower security levels fail with `authorizationError` — the `tegi` v3 user's group view only grants `authPriv`; SNMPv2 `public`/`private` communities are also configured (private = write, untested per read-only rule; syslog flags this as insecure) |
| NETCONF / RESTCONF / gNMI | 830 / 443 / 9339 | no | — | Ports 830 and 9339 closed; no RESTCONF path found under 443 |
| Other (vendor discovery, TFTP, LLDP, CDP) | — | LLDP active | — | LLDP is enabled per-port (Tx+Rx on port 1, Rx-only on 2–24, full Tx+Rx on 25/26); CDP-awareness flag also present but not exercised |

## SNMP capabilities

- Walk method: plain `snmpwalk` (GetNext), `-t 3 -r 1`, per documented subtree.
  `snmpbulkwalk` with `-Cr10` (default) and `-Cr20` both returned the complete
  443-varbind `ifXTable` with no drops; a synthetic high `-Cr50` request against
  the same table also completed without visible truncation in this test —
  the device did not reproduce the >1400-byte GetBulk drop in this session,
  but the brief's warning is taken as authoritative (net-snmp default request
  size and the switch's control-plane CPU load could still trigger it on
  larger tables); GetNext-based `snmpwalk` was used throughout to stay safe.
- Per-subtree varbind counts and wall time (raw files under `_raw/labsw04/snmp/`):

  | Subtree | Varbinds | Time |
  | --- | ---: | ---: |
  | system | 20 | <1s |
  | interfaces (ifTable etc.) | 573 | 17s |
  | ifXTable | 443 | 14s |
  | ipAddrTable | 5 | 1s |
  | ipAddressTable | 1 (`No Such Object`) | 0s |
  | ipNetToMedia | 4 | 1s |
  | entPhysicalTable | 15 | 0s |
  | lldpLocalSystemData | 1 (`No Such Object`) | 1s |
  | lldpRemTable | 1 (`No Such Object`) | 0s |
  | dot1dBase | 133 | 5s |
  | dot1dTpFdbTable | 141 | 4s |
  | dot1qVlanStaticTable | 10 | 1s |
  | dot1qTpFdbTable | 24 | 1s |
  | dot1dStp | 170 | 5s |
  | lagMIB (ieee8023ad) | 1 (`No Such Object`) | 1s |
  | pethPsePortTable | 1 (`No Such Object`) | 0s |
  | hrSystem | 1 (`No Such Object`) | 1s |
  | enterprise `.1.3.6.1.4.1.2356` | 11,291 | 351s (~6 min) |

  Total observed: ~12.8k varbinds (the brief's ~14,900 full-tree estimate is
  plausible once the standard-MIB subtrees above are summed with the
  enterprise tree — a true whole-tree walk was not run to avoid the
  10-minute-plus single-session load the brief calls out).

- Standard MIBs answered: `system`, `interfaces`/`ifTable`, `ifXTable`,
  `ipAddrTable` (legacy, populated), `ipNetToMedia` (populated),
  `entPhysicalTable` (single entry — the whole chassis, no per-port physical
  entries), `dot1dBase`+`dot1dTpFdbTable` (bridge/FDB), `dot1qVlanStaticTable`
  + `dot1qTpFdbTable` (Q-bridge), `dot1dStp` (legacy single-instance STP MIB).
  All children under `enterprise.2356.800` — the entire proprietary tree — do
  answer (11,291 varbinds); this is the vendor's FASTPATH-derived private MIB
  and is where port-level detail (LACP, PoE-shaped objects, LLDP remote data
  if exposed, queue stats) actually lives, not the standard MIBs.
- **Not implemented / empty**: `ipAddressTable` (RFC 4293, superseded by the
  legacy `ipAddrTable`), `lldpLocalSystemData`/`lldpRemTable` (standard LLDP
  MIB — LLDP itself works and is CLI-visible, just not SNMP-exposed),
  `ieee8023ad`/`lagMIB` (LACP works and is CLI-visible via `aggregation`, not
  SNMP-exposed), `pethPsePortTable` (no PoE hardware on this model — no `poe`
  top-level CLI command either, consistent), `hrSystem` (Host Resources MIB
  not implemented — expected on switch firmware, not a general-purpose host).
- Writable objects: not tested (read-only rule). The v2c `private` community
  is configured read-write per CLI (`snmp / show snmp`) but untested.
- Traps/informs: `snmp / show trap` returned all 6 trap-host slots empty — no
  trap destinations configured.

## CLI / configuration model

- Shell type: LANCOM's own tree-structured/contextual shell (not literal
  Broadcom FASTPATH `show ...` syntax, despite the FASTPATH-lineage silicon).
  Top-level nouns (`system`, `port`, `vlan`, `lldp`, `stp`, `snmp`,
  `aggregation`, `fdb`, `ip`, `time`, `syslog`, `ssh`, `https`, `account`,
  `privilege`, `access`, `aaa`, `config-file`, and more — see raw
  `ssh-session.log` for the full top-level `?` listing) each *enter a mode*
  (prompt changes from `LABSW04#` to `LABSW04(<noun>)#`) rather than taking
  arguments directly; `show` and other verbs are then typed inside that mode.
  There is no `show version`/`show running-config` command at all — the
  brief's assumed FASTPATH command names (`show interfaces status all`,
  `show vlan brief`, etc.) do not exist on this firmware; use `<noun>` then
  `show <subcommand>` instead. `?` at any point lists valid completions,
  including a bare `<cr>` when the current token is already complete.
- Prompt shapes: `LABSW04#` at root, `LABSW04(<mode>)#` inside a mode (e.g.
  `LABSW04(system)#`, `LABSW04(snmp)#`). No separate privilege-escalation step
  observed — logging in as `admin` lands directly at privilege level 15
  (confirmed via `privilege / show`, which lists every command group at level
  15).
- Pager: `--More--, q to quit` appears mid-output for long tables; sending a
  space continues, `q` quits early. No `terminal length 0` equivalent was
  found (no such command in any mode explored); the pager cannot be disabled,
  so scripted capture must handle it (our automation sent a space on every
  `--More--` prompt).
- Session/auth facts: password-only SSH auth; login/logout events are logged
  per-session (see `syslog / show log` sample in raw capture, which also
  incidentally showed three "Bad password attempt" warnings from failed
  automation earlier in this session — no lockout observed, but see Isolation
  note below). No banner beyond `Type 'help' or '?' to get help.` after login.
- Config read/write: `config-file export <tftp-server> <file>` exists at the
  root level (confirmed via `config-file ?`) but **was not run** per the
  brief's read-only rule and because it requires a live TFTP target; the
  syntax itself was not further disambiguated since entering the
  `config-file` mode with a bare `<cr>` produces a mode with no visible
  subcommands of its own (the real command is a single root-level line, not a
  mode). No `show running-config`-equivalent that prints config to the
  terminal was found — config is only exportable via TFTP, not viewable
  inline.
- Sanitised `system / show` output (the closest equivalent to `show
  version`/`show sysinfo`):

  ```
  Model Name                   : LANCOM GS-2326+
  System Description           : 26-Port Layer-2 Managed Gigabit Ethernet Switch with 2x TP/SFP COMBO
  Device Name                  : LABSW04
  System Uptime                : 00:24:14
  BIOS Version                 : v1.00
  Firmware Version              : v3.34.0326SU9
  Hardware-Mechanical Version  : v1.01-v1.01
  Serial Number                : 4005284520000039
  IPv4 Address                 : 172.16.0.4
  Host MAC Address             : 00-a0-57-45-34-85
  Console Baudrate             : 115200
  RAM Size                     : 128
  Flash Size                   : 32
  CPU Load (100ms, 1s, 10s)    : 2%, 58%, 19%
  Bridge FDB Size              : 8192 MAC addresses
  Transmit Queue               : 8 queues per port
  Maximum Frame Size           : 9600
  LMC Pairing State            : Not-Authenticated-With-LMC,No-Cloud-Management
  ```

## Web / API

- Login flow: plain HTML form POST to `config/login` (username + password
  fields, `maxlength=32`); no CSRF token visible in the form. Three cookies
  are set client-side by JS before login: `cid`, `seid`, and (HTTPS only)
  `sesslid`, each a random integer — session tracking, not a server-issued
  session ID at this stage.
- No REST/JSON API: `/api`, `/rest`, `/cgi-bin`, `/backup.swb`, `/sys.b`,
  `/link.b` all return the same 301-to-`/login.htm` as any unknown path, i.e.
  they are not distinguishable from "not found." The UI's own JS
  (`lib/ajax.js`) calls a **`config/<page>`** URL scheme instead — e.g. the
  login page itself polls `GET /config/login` and parses the response as a
  `/`-delimited plain-text string (`values[0]` = tacacs status, `values[1]` =
  lockout status), not JSON or XML. This confirms the device exposes a
  plain-text, page-specific pseudo-API under `/config/*`, not a general
  REST/JSON surface. `lib/config.js` is a static constants file (port counts,
  ACL/QoS limits, `configArchLuton26 = 1`) useful for identifying the exact
  hardware family from the web UI alone, without authentication.
- Backup/restore: no endpoint found via GET probing; the CLI exposes
  `config-file export <tftp> <file>` (TFTP-based, not HTTP) as the only
  config-export mechanism observed (not executed, see CLI section).

## Feature inventory (as observed)

| Feature | Observed state |
| --- | --- |
| VLANs | 2 static VLANs: `1` (default, all 26 ports tagged/trunk) and `1000` (`mgmt`, all 26 ports); ports 2–24 are `Access`/PVID 1000, ports 1/25/26 are `Trunk`/PVID 1; no forbidden-VLAN entries |
| LAG | 1 group, `LLAG1`, LACP, ports 25+26 aggregated (no ieee8023ad SNMP exposure, CLI-only) |
| STP | Enabled, single CIST instance (not multi-instance MSTP in use); root bridge is a different device (`10:00-0C:EA:14:78:F2:04`) reached via `LLAG1` (root port), port 8 is a designated forwarding port |
| LLDP | Per-port Tx/Rx config (port 1 full, 2–24 Rx-only, 25/26 full); neighbours seen: a Kali Linux host on port 8 (`labtest`, `78-01-5A-B0-05-00`) and a MikroTik `Lab_SW01` (CRS317-1G-16S+, RouterOS 6.49.20) on `LLAG1` via multiple SFP+ interfaces |
| PoE | Not present — no `poe` CLI command, no PoE SNMP table populated; this GS-2326+ unit is the non-PoE variant |
| Port mirroring | `mirror` command group exists (not exercised) |
| ACL | `acl` command group exists (not exercised) |
| QoS | `qos` command group exists (not exercised) |
| IGMP snooping | `igmp` command group exists (not exercised) |
| DHCP snooping | `dhcp-snooping` command group exists (not exercised) |
| 802.1X | `dot1x-supplicant` (client-side only) and `nas` (802.1X authenticator, "Network Access Server") groups exist |
| Routing / L3 | Single management IP `172.16.0.4/24` on VLAN 1000, static gateway `172.16.0.1`; no separate routed interfaces — this is an L2-only switch |
| IPv6 | Link-local only (`fe80::2a0:57ff:fe45:3485`); no global IPv6 configured |
| NTP | `time / show ntp` lists 5 empty server slots — **not configured**; device clock is stuck at 2011-01-01 (visible in HTTP headers and syslog timestamps) |
| Syslog | Remote syslog disabled (`Server Mode : Disabled`); local ring buffer holds 23 entries at capture time, levels down to Info |
| SNMP traps | Configured targets: none (all 6 slots empty) |
| RADIUS/TACACS | `aaa` command group exists (RADIUS config + statistics); not exercised. Login page JS references a `tacacs` status field, suggesting TACACS+ support too |
| Firmware upgrade | `firmware` command group exists (not exercised) |

## Discovery signals

- **SNMP** (no credentials needed): v1/v2c `public` community answers
  `sysDescr` directly: `LANCOM GS-2326+ 3.34.0326 / 19.08.2024 4005284520000039`
  — vendor, model, firmware, build date, and serial number all leak via the
  default read community with zero SNMPv3 setup.
- **HTTP**: `Server: eCos Embedded Web Server` header on both 80 and 443;
  redirect target `/login.htm` and page title identify it as a LANCOM/Luton26
  FASTPATH-family switch even before authentication.
- **SSH**: banner string `lcos-sx_v3.34.0326` in the SSH version exchange
  identifies vendor and exact firmware without authenticating.
- **Telnet**: banner is a bare `Username:` prompt with IAC option negotiation
  (`IAC WILL ECHO`) — no vendor string leaks over telnet itself, unlike SSH.
- **LLDP**: this switch advertises itself over LLDP to neighbours per its
  per-port config (ports 1, 25, 26 have LLDP Tx enabled); its own chassis
  ID/system name would appear as `LABSW04` to anything listening upstream.
- **MAC OUI**: `00-A0-57` (device's own host MAC) is a LANCOM-assigned OUI;
  useful for fingerprinting LANCOM gear from ARP/FDB tables alone.

## What FlowSeer needs from this device

- **Inventory**: SNMP v2c `public` sysDescr parsing is sufficient for
  zero-touch identification (vendor, model, firmware, serial) — no
  credentials required. For anything beyond identity (interfaces, VLANs, FDB,
  bridge/STP), SNMPv3 `authPriv` with SHA+AES is required; the v2c `public`
  community is read-only and does not expose the FDB/interface tables tested
  here without checking view restrictions further (not verified in this
  session — `public`'s view was not walked, only `sysDescr` was fetched
  through it).
- **Config/telemetry protocol split**: this device has no RESTCONF/NETCONF/
  gNMI, no JSON REST API, and no in-band "show config to terminal" CLI
  command — the *only* machine-readable channels are (a) SNMP for read
  telemetry and (b) TFTP-based `config-file export` for config backup. Any
  FlowSeer integration needing full running-config text must drive a TFTP
  transfer, not scrape CLI output.
- **CLI automation must be a screen-scraper, not a line-oriented "show"
  client**: commands are stateful (`<noun>` enters a mode, subsequent verbs
  apply inside it), the pager cannot be disabled, and there's no
  `terminal length 0`. An adapter needs a small state machine (mode-tracking
  prompt regex `LABSW04(\([a-z-]+\))?# ` and pager-continuation on
  `--More--`) rather than assuming stock FASTPATH `show ...` syntax.
- **Algorithms to support**: SSH host key is RSA only (`ssh-rsa`, no
  ed25519/ECDSA offered); KEX/cipher negotiation is otherwise modern
  (curve25519, aes128-gcm). TLS minimum version is 1.2. SNMPv3 requires
  SHA+AES (MD5 auth and DES priv both fail auth on this device — see error
  matrix below); net-snmp's lack of SHA-256/AES-192+ support was not a
  limiting factor here since the device only offers SHA-1/AES-128 anyway
  (*inferred* from what authenticated successfully — the device's supported
  algorithm ceiling was not independently confirmed from its own config).
- **Quirks to handle**:
  - NTP is unconfigured out of the box and the clock free-runs from
    2011-01-01 — timestamps in syslog/HTTP will be wrong until NTP is set;
    FlowSeer should not trust device-reported timestamps without checking
    `time / show ntp` first (or correlating with poll-time on the collector
    side).
  - The enterprise MIB walk takes ~6 minutes and returns >11k varbinds
    for a 26-port switch — a full-tree poll is unsuitable for frequent
    polling; FlowSeer should target specific subtrees per the table above.
  - v2c community `tegi` and v3 `tegi` at `noAuthNoPriv`/`authNoPriv` both
    return `authorizationError`, not `unknownUser`/timeout — the `tegi` SNMPv3
    group only grants `authPriv`, so a poller must always request the highest
    security level or get a clear authorization failure rather than silently
    downgrading.

### SNMP auth/version error matrix (as tested against `sysDescr.0`)

| Version / level | Credential | Result |
| --- | --- | --- |
| v1, community `public` | — | OK |
| v2c, community `public` | — | OK |
| v2c, community `tegi` | — | `authorizationError` |
| v3, noAuthNoPriv, user `tegi` | — | `authorizationError` |
| v3, authNoPriv, user `tegi`, SHA | correct passphrase | `authorizationError` |
| v3, authNoPriv, user `tegi`, MD5 | correct passphrase | Authentication failure (wrong protocol, not a valid MD5 key) |
| v3, authPriv, user `tegi`, SHA/AES | correct passphrase | **OK** |
| v3, authPriv, user `tegi`, SHA/DES | correct passphrase | Timeout (no response — DES priv apparently unsupported/silently dropped, not a clean auth error) |
| v3, authPriv, user `tegi`, MD5/DES | correct passphrase | Authentication failure |

## Raw evidence

All paths below are under
`docs/research/device-inventory/lab/_raw/labsw04/`, scrubbed of the device
password:

- `telnet-banner.txt` — raw telnet IAC negotiation + `Username:` banner bytes
- `ssh-vv.txt` — full `ssh -vv` transcript (KEX/host-key/cipher proposals both
  directions, auth methods offered)
- `tls-cert.txt` — HTTPS certificate subject/issuer/validity
- `https-root.txt` — `curl -k -i` of `/` (301 redirect, `eCos Embedded Web
  Server` header)
- `login-htm.txt` — raw (gzip) response headers for `/login.htm`;
  `login-decoded.html` — decompressed page source (login form, cookie/JS
  logic, `config/login` polling call)
- `lib-config.js`, `lib-ajax.js`, `lib-dynforms.js` — the web UI's static JS,
  including hardware-family constants and the `loadXMLDoc`/`config/<page>`
  request helper
- `ssh-session.log` through `ssh-session8.log` — sequential `expect`-driven
  CLI exploration sessions (top-level command tree discovery, per-mode `show`
  syntax discovery, and the actual `show` output used throughout this
  dossier, including the full `syslog / show log` sample and LLDP neighbour
  dump)
- `snmp/` — one file per walked subtree, numeric OIDs (`-On`), matching the
  varbind-count table above

## Isolation / operational notes

- Automated CLI exploration in this session briefly mistimed pager
  interaction on the first two attempts (concatenated keystrokes sent while a
  `--More--` prompt was still active), producing a handful of garbled
  commands and one run of failed logins visible in the device's own
  `syslog / show log` output (three "Bad password attempt" warnings) before
  the automation was corrected to wait for the full prompt after every
  `--More--`. No lockout was triggered and no configuration was changed;
  flagged here per the brief's read-only/one-session discipline rather than
  silently left out of the record.
- `172.16.0.1` (admin gateway) was never touched.
- `config-file export`, `snmpset`, and any `write`/`save`/`reboot`/account
  commands were deliberately not run.
