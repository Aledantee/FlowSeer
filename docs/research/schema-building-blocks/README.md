---
title: Schema building blocks dossiers
date: 2026-09-25
status: research; decisions live in docs/architecture/2026-09-25-schema-building-blocks-direction.md
---

# Schema building blocks dossiers

Nine dossiers written on 2026-09-25 as the evidence for the
[schema building blocks record](../../architecture/2026-09-25-schema-building-blocks-direction.md).
Each covers one domain and has the same sections: the sources read, the
standards facts, a provider data matrix, proposed primitives and entities,
traps, and open questions. Every claim cites a fetched URL or a path under
`spec/`; claims the author could not verify say "unverified".

A dossier's proposals are inputs. Where the record decided differently (unit
choices in 02 and 08, the package for port-access sessions in 05 and 07, the
home of a shared signal-quality message in 07), the record wins and says why.

| Dossier | Domain | Read before |
| --- | --- | --- |
| [01](01-wifi-technology.md) | IEEE 802.11 bands, channels, widths, PHY generations, BSS, security, MLO | the `net/wlan` phase |
| [02](02-rf-and-ap-telemetry.md) | Radio state, tx power, noise, utilization, rogue scans, per-provider matrix | the `net/wlan` phase |
| [03](03-clients-endpoints.md) | Endpoint identity, randomized MACs, attachment, fingerprinting, the Endpoint entity | the endpoint phase |
| [04](04-platform-system.md) | Components, sensors, CPU and memory, firmware, NTP, syslog, alarms | the platform phase |
| [05](05-l2-and-instances.md) | Network instances, MSTP, LLDP extensions, CDP, 802.1X, IGMP snooping | the instance and L2 phases |
| [06](06-l3-routing-services.md) | RIB and FIB, BGP, OSPF, IS-IS, VRRP, BFD, DHCP, DNS, NAT, the address-origin fix | the routing and L3 phases |
| [07](07-qos-security-ops-wan.md) | QoS, ACLs, AAA, flow export, syslog, cellular, WAN path quality | the services and WAN phase |
| [08](08-consistency-units-audit.md) | Unit conventions from OpenConfig, IETF, SMI, and AIP; audit of today's schema | the foundation phase |
| [09](09-wifi-registries.md) | AKM and cipher suite selectors, operating classes, country element, WPA3 modes | the `net/wlan` phase |

When a canonical host blocked automated fetches, the mirror used instead is named in the dossier that used them (09 uses a GitHub mirror of
hostap).
