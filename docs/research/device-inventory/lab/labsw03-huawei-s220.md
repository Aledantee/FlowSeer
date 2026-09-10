---
title: Lab device — LABSW03 (172.16.0.3)
date: 2026-09-10
scope: Huawei eKitEngine S220-24P4X, YunShan OS 1.25.0.1 (S200 V600R025C00SPC500); measured on the live device
status: research; read-only capture, no device changes made
---

# LABSW03 (172.16.0.3)

Every fact below was read from the device on the date above. Anything inferred
rather than observed is marked *inferred*. The device's own clock is
unsynchronized (`display ntp status`: stratum 16, no reference) and reports
dates in April 2026, well before the real capture date — command output
timestamps below are copied verbatim from the device and are not wall-clock
accurate.

## Identity

| Field | Value |
| --- | --- |
| Vendor / model | Huawei eKitEngine S220-24P4X (24×GE + 4×10GE SFP+, PoE) |
| Firmware / software version | Huawei YunShan OS, Version 1.25.0.1 (S200 V600R025C00SPC500) |
| Serial | Not obtainable read-only. `display elabel`, `display esn`, and `display device manuinfo` do not exist on this CLI tier (`display de?` only offers `debugging`, `default-parameter`, `device`); SNMP (which would expose `entPhysicalTable`) is unreachable (see below). |
| sysObjectID | Not obtainable — SNMP is unreachable on every version tried (see Management surfaces). |
| sysDescr | Not fetched via SNMP; the equivalent string from `display lldp local` is: `Huawei Switch` / `Huawei YunShan OS` / `Version 1.25.0.1 (S200 V600R025C00SPC500)` / `Copyright (C) 2021-2025 Huawei Technologies Co., Ltd.` / `HUAWEI eKitEngine S220`. |
| Hostname | LABSW03 (`sysname LABSW03`) |
| MAC / base MAC | e8ac-2367-cd86 (LLDP chassis ID, ARP self-entries for Virtual-MEth0 and Vlanif1000) |
| Uptime at capture | `HUAWEI eKitEngine S220 uptime is 0 day, 0 hour, 12 minutes` (device had just booted at the time of this session) |

Hardware detail from `display version`/`display device`: PCB `ES5D2V28S040 VER C`,
Board Type `S220-24P4X`, BIOS `1696`, CPLD `262`, 2048 MB RAM, 1024 MB flash,
one FRU slot with 2 fans and 1 PSU, all `Present/On/Registered/Normal`.

## Management surfaces observed

One row per surface, including the ones that failed and why.

| Surface | Port | Reachable | Auth that worked | Notes (banner, TLS cert subject, version string) |
| --- | --- | --- | --- | --- |
| SSH | 22 | Yes | password (`admin`) | No pre-auth banner (`Remote protocol version 2.0, remote software version -`). macOS OpenSSH 10.3 (LibreSSL) gets reset the instant it sends the password packet — `ssh -vv` with `-o BatchMode=yes` gets past KEXINIT/host-key/NEWKEYS cleanly and only fails at the (skipped) password step, confirming the reset happens post-auth-offer, not during key exchange. `paramiko` 5.0.0 `invoke_shell` succeeds on the very first attempt with the same credentials — see Feature inventory / raw evidence for the negotiated algorithm details. |
| Telnet | 23 | No | — | Closed in the port sweep. Config shows `telnet server enable` but `undo telnet server-source all-interface` and `undo telnet ipv6 server-source all-interface` — the server is administratively on but has no interface bound to accept connections on, so it never listens. |
| HTTP / HTTPS UI | 80/443 | No | — | Both closed in the sweep despite `web-manager enable port 443` and `web-manager http forward enable` being present in the running config, because `web-manager server-source -i Vlanif1` binds the web server to Vlanif1 only; Vlanif1 is administratively down (`display ip interface brief`: `Vlanif1 unassigned down down`) and 172.16.0.3 is reached via Vlanif1000, so the web server is configured but unreachable from this address. |
| REST / JSON API | — | No | — | `http service restconf` is configured, also bound to `server-source Vlanif1`, same reachability gap as the web UI. No probing possible since the underlying HTTP(S) ports never accepted a TCP connection. |
| SNMP | 161/udp | No | none | v1/v2c (`tegi`, `public`) and v3 (`tegi` noAuthNoPriv, and authPriv SHA1/AES — SHA2-256 could not be tried, see SNMP capabilities) all produced a full-timeout `Timeout: No Response` with **no ICMP port-unreachable** (`snmpget -t 2 -r 0` took the full 2.0s, not an instant return) — a silent UDP drop, not a rejection. This matches `undo snmp-agent protocol source-status all-interface` and `undo snmp-agent protocol source-status ipv6 all-interface` in the running config, which disable the SNMP listener on every interface even though `snmp-agent`, the community strings, and the `tegi` v3 user are all fully configured. |
| NETCONF / RESTCONF / gNMI | 830 / 443 / 9339 | No | — | TCP 830 closed in the sweep; `display ssh server status` reports `SNETCONF IPv4 server: Disable`, `SNETCONF IPv6 server: Disable`, `SNETCONF IPv4 server port(830): Disable`. Confirmed independently: `paramiko.Transport.open_session().invoke_subsystem("netconf")` over the working SSH session raises `SSHException: Channel closed.` immediately — the SSH server does not register a `netconf` subsystem at all, so no `<hello>` was ever sent or received. RESTCONF is configured (`http\n service restconf\n  server-source Vlanif1`) but unreachable, same as the web UI (Vlanif1 down). gNMI (9339) closed, no config reference found. |
| Other (vendor discovery, TFTP, LLDP, CDP) | — | LLDP only | — | LLDP is enabled and active (see Discovery signals). No CDP (not a Cisco protocol this device speaks). ZTP is configured (`ztp domain-type registration-center domain register.naas.huawei.com port 10020`) but that is an outbound client feature, not a discoverable surface. |

## SNMP capabilities

- Full walk: not performed — SNMP does not answer on this device for any version/credential combination tried, so there is nothing to walk. No raw walk files exist under `_raw/labsw03/`.
- Standard MIBs answered: none — every `system`/`interfaces`/etc. subtree query would time out identically to the `sysDescr.0` probes below.
- Enterprise subtrees answered: none, same reason.
- Root cause, directly from `display current-configuration`: `snmp-agent` is fully provisioned (`snmp-agent sys-info version v2c v3`, two `read` communities with cipher-obfuscated secrets, `snmp-agent group v3 tegigrp privacy read-view isoview write-view isoview notify-view isoview`, `snmp-agent usm-user v3 tegi` with `authentication-mode sha2-256` and `privacy-mode aes128`) but then explicitly disabled at the transport layer with `undo snmp-agent protocol source-status all-interface` (and the IPv6 equivalent). `display snmp-agent usm-user` confirms the `tegi` user is `Active` with `Authentication Protocol: sha2-256` / `Privacy Protocol: aes128`, matching the brief's expected FlowSeer-style parameters exactly — the agent process is simply not listening on any interface.
- SHA2-256/AES-128 v3 test: could not be run with the credentials that would actually succeed against the configured algorithm, because pysnmp (needed for `usmHMAC192SHA256AuthProtocol`) is not installed in this environment and package installation is out of scope for this task (see Raw evidence for what was available: paramiko 5.0.0 only, no netmiko, no pysnmp, despite the brief's assumption of a prepared `labenv`). SHA1/AES (net-snmp's ceiling) was tried instead as a proxy and produced the same silent timeout as every other combination, which is the expected result regardless of hash algorithm since the transport itself is disabled.
- Writable objects: N/A, not reachable.
- Traps/informs: `display snmp-agent trap-list` is not a recognized command on this CLI tier; no trap-target configuration lines appear in `display current-configuration`.

## CLI / configuration model

- Shell type: VRP-style (Huawei YunShan OS is VRP-derived), single unprivileged-then-privileged prompt `<LABSW03>` (user-view; this account has `local-user admin privilege level 3`, the max). No visible `system-view` transition was attempted (out of scope — read-only `display`/`show` commands only, per the brief).
- Pager: default pager exists (`---- More ----` handling was coded defensively but never triggered because `screen-length 0 temporary` was run first each session and reliably disabled it for the session — confirmed by the `Info: The configuration takes effect on the current user terminal interface only.` response).
- How config is read: `display current-configuration` returns the full active config as VRP-dialect plain text (`#`-delimited stanzas, `!` comment lines for metadata) — see the sanitised excerpt below and the full text under `_raw/labsw03/cli-session-1-display-commands.txt`.
- How config is applied: immediate, one command at a time — there is no two-stage/candidate-commit model. `display configuration candidate` is not a recognized command (`Error: Wrong parameter found at '^' position.`), and the current-configuration banner itself proves it (`!Last configuration was updated at 2026-04-24 17:42:42+00:00 by admin` / `!Last configuration was saved at 2026-04-24 17:42:56+00:00 by admin` — updated and saved are 14 seconds apart, i.e. `save` persists what is already live, it does not commit a pending change set). This is standard VRP behaviour: `system-view` → per-command apply → `save` (or `save <filename>`) to persist running-config to the startup file. `save` was **not** run (destructive/state-changing, excluded by the brief).
- Rollback: no `display configuration rollback` command exists on this tier (not probed directly, but no rollback-related config appears and `display startup` shows a single, non-versioned startup file `flash:/hw-baseline.zip` with no rollback checkpoints listed — `startup checkpoint auto-save disable` is explicit in the config).
- Session limits/timeouts: `user-interface maximum-vty 15` (matches the SSH login banner: "max number of VTY users is 15"); SSH auth timeout 60s, 3 retries, server-side IP-blocking (`SSH server ip-block: Enable`) all from `display ssh server status`.
- Key-exchange / host-key / cipher algorithms offered (from `ssh -vv` against the live server, no password sent — see below and the raw capture for the full KEXINIT):
  - KEX: `curve25519-sha256`, `curve25519-sha256@libssh.org`, `diffie-hellman-group16-sha512`, `diffie-hellman-group-exchange-sha256` — negotiated `curve25519-sha256`.
  - Host key: `rsa-sha2-512`, `rsa-sha2-256` only (no ed25519/ecdsa offered) — negotiated `rsa-sha2-512`, actual key `ssh-rsa SHA256:48KqBOx7zhwZJn737xSVG1nBe6ybKv/dOsOudRqwUjQ`.
  - Ciphers: `AEAD_AES_256_GCM`/`aes256-gcm@openssh.com`, `AEAD_AES_128_GCM`/`aes128-gcm@openssh.com`, `aes256-ctr`, `aes192-ctr`, `aes128-ctr` — negotiated `aes128-gcm@openssh.com` both directions.
  - MACs: `AEAD_AES_256_GCM`, `AEAD_AES_128_GCM`, `hmac-sha2-512`, `hmac-sha2-256` (implicit under the AEAD ciphers negotiated).
  - This matches the running config's explicit `ssh server cipher`/`ssh server hmac`/`ssh server key-exchange`/`ssh server dh-exchange min-len 3072` lines exactly.
  - Why the macOS OpenSSH client fails but paramiko does not: `ssh -vv` with `BatchMode=yes` (no password offered) completes key exchange and `NEWKEYS` with no error; the reset only happens when a password is actually sent as the OpenSSH 10.3/LibreSSL client. paramiko 5.0.0 negotiates the same KEX (`curve25519-sha256`) and completed both key exchange and password auth without issue on the first attempt across three separate sessions in this capture — the incompatibility is specific to the OpenSSH 10.3/LibreSSL 3.3.6 client's password-auth packet framing or post-NEWKEYS behavior against this server, not the crypto negotiation itself.
- Sanitised `display version` output:

  ```
  Huawei YunShan OS
  Version 1.25.0.1 (S200 V600R025C00SPC500)
  Copyright (C) 2021-2025 Huawei Technologies Co., Ltd.
  HUAWEI eKitEngine S220 uptime is 0 day, 0 hour, 12 minutes

  S220-24P4X(Master) 1 : uptime is  0 day, 0 hour, 11 minutes
          StartupTime 2026/04/26   17:09:55
  Memory      Size    : 2048 M bytes
  Flash       Size    : 1024 M bytes
  S220-24P4X version information:
  1.PCB       Version : ES5D2V28S040 VER C
  2.MAB       Version : 0
  3.Board     Type    : S220-24P4X
  4.BIOS      Version : 1696
  5.CPLD      Version : 262
  ```

## Web / API

- Not reachable — see Management surfaces above. `web-manager` and `http service restconf` are both configured but bound to `Vlanif1`, which is administratively down; the device is managed over `Vlanif1000` (172.16.0.3) in this lab. No login flow, cookie, or endpoint could be observed since no TCP connection to 80/443 succeeds.
- The running config also carries `smart-upgrade http url houp.huawei.com` and `ztp domain-type registration-center domain register.naas.huawei.com port 10020` — both outbound-only cloud-management hooks, not local API surfaces.

## Feature inventory (as observed)

| Feature | Observed state |
| --- | --- |
| VLANs | 4 total: 1 (default, all ports untagged/down), 666 "BREACH-TARGET" (tagged on GE1/0/23-24), 999 "dead-loopfree-cisco" (access on GE1/0/16), 1000 (management/uplink VLAN, untagged on GE1/0/2, tagged everywhere else including Eth-Trunk1) |
| LAG | 1 static Eth-Trunk (`Eth-Trunk1`, LACP-static mode, `mode lacp-static`), members 10GE1/0/1-4, 2 of 4 members up/selected (10GE1/0/3, 10GE1/0/4); partner system ID `18fd-74e2-6e7e` |
| STP | RSTP mode (`stp mode rstp`), instance 0 priority 16384; only Eth-Trunk1 is a live STP port, role ROOT, forwarding; per-port `stp root-protection` is set on every access/trunk edge port |
| LLDP | Enabled, txAndRx on all ports; 1 neighbor seen (see Discovery signals) |
| PoE | All 24 GE ports configured for 30000 mW (30W, PoE++/4PPoE class) user-set max, 0 mW currently drawn on every port (nothing powered) |
| Port mirroring | Not configured (no `observe-port`/`port mirroring` lines in running config) |
| ACL | None configured |
| QoS | Default schedule-profile and diffserv-domain only, no active policy |
| IGMP snooping | Not observed in running config (no `igmp snooping` lines) |
| DHCP snooping | Enabled globally (`dhcp enable`); `dhcp snooping trusted` set on Eth-Trunk1 and GE1/0/1-2; no bindings currently learned (`display dhcp snooping user-bind all`: 0 entries) |
| 802.1X | Profiles defined (`dot1x_authen_profile`, `dot1xmac_authen_profile`, `mac_authen_profile`) but not applied to any interface |
| Routing (L3) | Two L3 interfaces: `Vlanif1` (down, DHCP/IPv6-autoconf, described "ztp") and `Vlanif1000` (up, 172.16.0.3/24, `ip address dhcp-alloc`); a management VRF `_management_vpn_` exists but is empty; default route via 172.16.0.1 learned dynamically (`display arp`, `RM_ADD_DEFAULTRT` log entry) |
| IPv6 | Only on Vlanif1 (link-local + global autoconf + DHCPv6); not used on the management path |
| NTP | Configured but unsynchronized — `clock status: unsynchronized`, `stratum 16`, no reference clock; `ntp server source-interface all disable` |
| Syslog | `info-center logfile compression lzma`; local log buffer active, 512/10240 bytes used, 38 messages at capture (LLDP, MSTP, interface, FIPS self-test, and hardware-registration events — see raw capture for full text) |
| SNMP traps | Not observable — SNMP transport disabled entirely (see SNMP capabilities) |
| RADIUS/TACACS | An `authentication-scheme radius` exists (`authentication-mode radius`) but no RADIUS server host is configured; local AAA is the default scheme in use |
| Firmware upgrade | `startup` shows a single active image `flash:/S220_V600R025C00SPC500.cc`, no next-boot change pending, no patches (`display patch-information`: "No patch exists"); cloud-assisted upgrade path exists via `smart-upgrade http url houp.huawei.com` |
| CPU / memory | CPU: 4% current, 9%/13% one/five-min average, 2 cores, overload threshold 90%; `display memory-usage` is not a recognized command on this CLI tier (untested equivalent) |

## Discovery signals

- LLDP: this device advertises chassis ID `e8ac-2367-cd86` (MAC), system name `LABSW03`, system description as shown under Identity, capabilities `bridge router`, on every port with `txAndRx` enabled. It has learned exactly one neighbor, seen on both live Eth-Trunk1 member ports (10GE1/0/3 and 10GE1/0/4): `Lab_SW01`, described as `MikroTik RouterOS 6.49.20 (long-term) CRS317-1G-16S+`, remote chassis ID `18fd-74e2-6e78` / `18fd-74e2-6e7e` (LACP partner), ports named `bridge/bond-huawei/sfp-sfpplus8` and `...sfpplus7`.
- No CDP (not spoken by this device).
- mDNS/SSDP: not probed — no UDP multicast capture was attempted; out of scope for a single-device SSH/SNMP dossier and the brief's tools (`curl`, `nc`, `ssh`, snmp CLIs) do not cover it.
- MAC OUI: `e8ac-23` is a registered Huawei Technologies OUI block, consistent with the vendor identification from CLI/LLDP.
- HTTP/SSH banners: SSH pre-auth banner is empty (`Remote protocol version 2.0, remote software version -`) — no version string leaks pre-auth. HTTP/HTTPS never accepts a connection (see above), so no banner is exposed there either. A scanner with no credentials at all would see: TCP 22 open with no informative banner, everything else closed — effectively no unauthenticated fingerprinting surface beyond "something speaks SSH2 here."

## What FlowSeer needs from this device

- **Inventory/config protocol: SSH CLI only, and only via a client that isn't the macOS system OpenSSH.** paramiko (or any implementation that doesn't hit whatever OpenSSH-10.3/LibreSSL-specific incompatibility triggers the reset) is required; netmiko's `huawei` device type should work since it also sits on paramiko, but was not itself testable in this environment (not installed here — see Raw evidence). SNMP, NETCONF, and RESTCONF are all administratively unreachable on this device as configured, so none of them can be a supported path for this specific unit; if FlowSeer wants SNMP/NETCONF from Huawei S220 fleet devices generally, the onboarding flow needs to either configure `snmp-agent protocol source-status all-interface` (a config-changing step, not something to assume is already on) or fall back to CLI scraping.
- **Auth/algorithms to support:** SSH password auth (no keys configured for `admin`); server offers only `rsa-sha2-512`/`rsa-sha2-256` host keys (no ed25519/ecdsa — a client that hard-requires modern host-key types will fail even before password auth); KEX `curve25519-sha256` preferred; AEAD ciphers (`aes128-gcm@openssh.com` negotiated) preferred over CTR/CBC.
- **CLI scraping quirks to encode:** VRP-style prompt `<HOSTNAME>`; must send `screen-length 0 temporary` first every session (not persistent); command errors are `Error: Unrecognized command found at '^' position.` / `Error: Wrong parameter found at '^' position.` / `Error: Too many parameters found at '^' position.` with a `^` pointing at the offending token — useful for a scraper to detect "this command doesn't exist on this box" vs. a real failure. No `display elabel`/`display esn` on this device tier, so serial-number capture needs another source (chassis label, order record, or a firmware tier that does support it) if FlowSeer needs a hardware serial for this SKU.
- **Config capture:** `display current-configuration` gives the full config as flat VRP text in one shot — good for a periodic text diff/backup job; there is no JSON/XML export and no two-stage commit to reconcile, so "capture = current state" always, with `save` (not run here) being the only persistence step and out of the read-only capture's scope.
- **Discovery:** LLDP is the only usable neighbor-discovery source and it does work — FlowSeer's topology mapper can rely on `display lldp neighbor brief`/`display lldp local` for this SKU. The MAC OUI alone (`e8ac-23`) can seed a coarse vendor guess before credentials are available.
- **This confirms and extends the existing lab SNMP access note for `.3`:** "needs sha2-256/aes128 but agent unreachable" was accurate as far as it went; this capture adds the root cause — the SHA2-256/AES128 `tegi` v3 user is fully configured and `Active`, but `undo snmp-agent protocol source-status all-interface` disables the agent's listener on every interface, so no SNMP version or algorithm combination can reach it. It is not a credential or algorithm problem, it is the transport being administratively off. SSH, by contrast, is reachable with plain paramiko on the first try — no algorithm workaround needed beyond avoiding the macOS system ssh client for the password step.

## Raw evidence

All raw captures are under `docs/research/device-inventory/lab/_raw/labsw03/` and have been checked for the device password (none present):

- `ssh-vv-algorithm-negotiation.txt` — full `ssh -vv -o BatchMode=yes` transcript: KEXINIT proposals both directions, negotiated algorithms, host key fingerprint, clean stop at the (skipped) password step.
- `cli-session-1-display-commands.txt` — first paramiko `invoke_shell` session: `screen-length 0 temporary`, `display version`, `display device`, `display elabel` (fails), `display interface brief`, `display vlan`, `display lldp neighbor brief`, `display lldp local`, `display stp brief`, `display poe power`, `display mac-address`, `display ip interface brief`, `display eth-trunk`, `display ntp status`, `display snmp-agent sys-info`, `display snmp-agent community`, `display snmp-agent usm-user`, `display current-configuration` (full text), `display configuration candidate` (fails), `display ssh server status`, `display ssh user-information`.
- `cli-session-2-display-commands.txt` — second session: `display esn`/`display device manuinfo`/`display snmp-agent trap-list` (all fail — not recognized commands on this tier), `display logbuffer` (full log buffer contents), `display clock`, `display startup`, `display patch-information`, `display users`, `display cpu-usage`, `display memory-usage` (fails), `display port vlan`, `display arp`, `display dhcp snooping user-bind all`.
- `cli-session-3-command-discovery.txt` — `display de?`/`display ela?`/`display el?` tab-completion-style discovery, confirming no `elabel`-family command exists on this CLI tier.
- `snmp-probe-results.txt` — every `snmpget` attempt (v1/v2c community `tegi` and `public`, v3 `tegi` authPriv SHA1/AES and noAuthNoPriv, v3 unknown user) and the NETCONF-subsystem probe result, all timing out/failing the same way.
