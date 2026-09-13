# Multicast snooping

This package keeps one multicast group table and one router-port set for each
configured VLAN. It consumes decoded IGMP and MLD messages; the caller supplies
the IP source address and the logical ingress port selected by the bridge.
Physical LAG members are ignored because forwarding uses their logical parent.
An admitted control trace carries an immutable fact with the decoded protocol
type, sender, group, source set, and group-record transitions. Source sets use
canonical order. Group records retain their decoded order because learning
applies them in sequence, and two records for the same group can produce a
different final membership when reversed.

For example, an IGMPv2 report for `239.1.1.1` on `1/1/1`, followed by a query
from a non-zero source on `1/1/4`, makes `Resolve(10, 239.1.1.1)` return
`[1/1/1 1/1/4], true`. The boolean is membership-only registration. A learned
router port still appears in the returned ports for an unregistered group, but
does not change the boolean to true.

## Group records

IGMPv1 and IGMPv2 reports and MLDv1 reports add or refresh membership. Leaves
and Done messages remove the matching entry only when `FastLeave` is enabled;
otherwise its existing timer continues.

IGMPv3 and MLDv2 records use the following group-only approximation. Sources
decide whether a record means join or leave, but are not stored.

| Record type | With sources | Without sources |
| --- | --- | --- |
| `MODE_IS_INCLUDE` | Add or refresh | Leave behavior |
| `MODE_IS_EXCLUDE` | Add or refresh | Add or refresh |
| `CHANGE_TO_INCLUDE_MODE` | Add or refresh | Leave behavior |
| `CHANGE_TO_EXCLUDE_MODE` | Add or refresh | Add or refresh |
| `ALLOW_NEW_SOURCES` | Add or refresh | No change |
| `BLOCK_OLD_SOURCES` | No change | No change |

An IGMP query learns its ingress as a router port when its IPv4 source is not
`0.0.0.0`. An MLD query requires an IPv6 link-local source. Static router ports
come from configuration and never expire.

## Aging and snapshots

Membership and learned router-port entries expire after their configured
interval. An unset interval uses 260 seconds. `Age(now)` removes entries whose
expiry is at or before `now`; it does not touch static router ports. `Groups`
and `RouterPorts` return sorted snapshots, and `Clone` preserves the timers in
an independent layer.

`Resolve` is intentionally narrower than a bridge forwarding policy. It returns
the membership/router union and membership registration state. The caller
decides whether an unregistered group floods or uses the returned router ports.

The learning rules follow RFC 4541 sections 2.1.1 and 2.1.2. The 260-second
default comes from RFC 2236 section 8.4 and RFC 2710 section 7.4. Record layouts
and source lists are defined by RFC 3376 section 4.2.12 and RFC 3810 section
5.2.12.
