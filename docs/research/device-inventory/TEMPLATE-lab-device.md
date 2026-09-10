---
title: Lab device — <hostname> (<ip>)
date: <YYYY-MM-DD>
scope: <vendor model, firmware>; measured on the live device
status: research; read-only capture, no device changes made
---

# <hostname> (<ip>)

Every fact below was read from the device on the date above. Anything inferred
rather than observed is marked *inferred*.

## Identity

| Field | Value |
| --- | --- |
| Vendor / model | |
| Firmware / software version | |
| Serial | |
| sysObjectID | |
| sysDescr | |
| Hostname | |
| MAC / base MAC | |
| Uptime at capture | |

## Management surfaces observed

One row per surface, including the ones that failed and why.

| Surface | Port | Reachable | Auth that worked | Notes (banner, TLS cert subject, version string) |
| --- | --- | --- | --- | --- |
| SSH | 22 | | | |
| Telnet | 23 | | | |
| HTTP / HTTPS UI | 80/443 | | | |
| REST / JSON API | | | | |
| SNMP | 161/udp | | version, auth/priv algorithms, community/user, view restrictions | |
| NETCONF / RESTCONF / gNMI | 830 / 443 / 9339 | | | |
| Other (vendor discovery, TFTP, LLDP, CDP) | | | | |

## SNMP capabilities

- Full walk: <n> varbinds in <time>; GetBulk max-repetitions that works; any truncation.
- Standard MIBs answered (with row counts): system, interfaces, ifX, ip, ipNetToMedia/ipNetToPhysical,
  entity (entPhysical), lldp (lldpRem/lldpLoc), bridge (dot1dBase, dot1dTp), q-bridge (dot1qVlan*),
  hostResources, powerEthernet, etc.
- Enterprise subtrees answered: enterprise OID roots with varbind counts and what they cover.
- Writable objects confirmed by `snmpset` **dry test only if explicitly pre-approved**; otherwise list
  what the MIB declares read-write and mark as untested.
- Traps/informs: configured targets, if visible.

## CLI / configuration model

- Shell type (IOS-like, Comware, FASTPATH, menu, none), prompt shapes, pager string, exit behaviour.
- How config is read: exact command(s) and the output format (text, JSON, XML).
- How config is applied: line-by-line vs candidate/commit, save-to-startup command, rollback support.
- How config is exported/imported (TFTP/SCP/HTTP, file format).
- Session limits, timeouts, banner, key-exchange and host-key algorithms offered.
- Sanitised `show version`-equivalent output in a fenced block.

## Web / API

- Login flow (form, digest, token), CSRF, session cookie names.
- Any JSON/XML endpoints discovered (URL, method, auth) and what they return.
- Backup/restore endpoints.

## Feature inventory (as observed)

Table of features with observed state: VLANs (count, ids), LAG, STP mode, LLDP, PoE (budget, per-port),
port mirroring, ACL, QoS, IGMP snooping, DHCP snooping, 802.1X, routing (L3 interfaces, static routes),
IPv6, NTP, syslog, SNMP traps, RADIUS/TACACS, firmware upgrade mechanism.

## Discovery signals

What a scanner sees without credentials: LLDP/CDP advertisement content, mDNS/SSDP, HTTP banner,
SSH banner, SNMP sysDescr with a public community, MAC OUI.

## What FlowSeer needs from this device

Bullet list of concrete integration requirements derived from the above: which protocol for
inventory, which for config, which for telemetry; credentials and algorithms to support; quirks that
must be handled (with the observed evidence).

## Raw evidence

Paths to the raw captures under `docs/research/device-inventory/lab/_raw/<hostname>/` (walk output,
show commands, HTTP responses). Keep raw files free of passwords.
