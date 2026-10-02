# Multicast snooping

This package keeps RFC 3376 §6.4 router state for each configured VLAN, multicast
group, and logical ingress port. It consumes decoded IGMP and MLD messages; the
caller supplies the IP source address and the logical ingress port selected by
the bridge. Physical LAG members are ignored because forwarding uses their
logical parent. An admitted control trace carries an immutable fact with the
decoded protocol type, sender, group, source set, and group-record transitions.
Source sets use canonical order. Group records retain their decoded order
because learning applies them in sequence, and two records for the same group
can produce a different final state when reversed.

## Router state

For each (VLAN, group, port), the package holds a filter mode, a group timer,
and a set of source records, each an address with its own timer. A timer of
zero is a real state — the source's timer has run to zero without the record
being deleted — not an absent record. Following RFC 3376 §6.4's notation,
`INCLUDE (A)` is an include-mode port with source set `A`, and `EXCLUDE (X,Y)`
is an exclude-mode port where `X` holds sources with a running timer and `Y`
holds sources whose timer has reached zero. A port with no router state is
`INCLUDE ({})`.

`GMI`, the group membership interval, is the VLAN's `MembershipInterval`
(default 260 seconds, RFC 2236 §8.4 and RFC 2710 §7.4). `LMQT`, the last member
query time, is `LastMemberQueryInterval * LastMemberQueryCount` (defaults 1
second and 2, the robustness variable, RFC 3376 §8.1, §8.8, §8.9, and RFC 3810
§9.8).

### Current-state records (§6.4.1)

| Router state | Report | New state | Actions |
| --- | --- | --- | --- |
| `INCLUDE (A)` | `IS_IN (B)` | `INCLUDE (A+B)` | `(B)=GMI` |
| `INCLUDE (A)` | `IS_EX (B)` | `EXCLUDE (A*B, B-A)` | `(B-A)=0`, delete `(A-B)`, group timer `=GMI` |
| `EXCLUDE (X,Y)` | `IS_IN (A)` | `EXCLUDE (X+A, Y-A)` | `(A)=GMI` |
| `EXCLUDE (X,Y)` | `IS_EX (A)` | `EXCLUDE (A-Y, Y*A)` | `(A-X-Y)=GMI`, delete `(X-A)`, delete `(Y-A)`, group timer `=GMI` |

### Filter-mode-change and source-list-change records (§6.4.2)

| Router state | Report | New state | Actions |
| --- | --- | --- | --- |
| `INCLUDE (A)` | `ALLOW (B)` | `INCLUDE (A+B)` | `(B)=GMI` |
| `INCLUDE (A)` | `BLOCK (B)` | `INCLUDE (A)` | Send Q(G, `A*B`) |
| `INCLUDE (A)` | `TO_EX (B)` | `EXCLUDE (A*B, B-A)` | `(B-A)=0`, delete `(A-B)`, Send Q(G, `A*B`), group timer `=GMI` |
| `INCLUDE (A)` | `TO_IN (B)` | `INCLUDE (A+B)` | `(B)=GMI`, Send Q(G, `A-B`) |
| `EXCLUDE (X,Y)` | `ALLOW (A)` | `EXCLUDE (X+A, Y-A)` | `(A)=GMI` |
| `EXCLUDE (X,Y)` | `BLOCK (A)` | `EXCLUDE (X+(A-Y), Y)` | `(A-X-Y)=` group timer, Send Q(G, `A-Y`) |
| `EXCLUDE (X,Y)` | `TO_EX (A)` | `EXCLUDE (A-Y, Y*A)` | `(A-X-Y)=` group timer, delete `(X-A)`, delete `(Y-A)`, Send Q(G, `A-Y`), group timer `=GMI` |
| `EXCLUDE (X,Y)` | `TO_IN (A)` | `EXCLUDE (X+A, Y-A)` | `(A)=GMI`, Send Q(G, `X-A`), Send Q(G) |

IGMPv1, IGMPv2, and MLDv1 reports map to `IS_EX ({})`; an IGMPv2 leave or an
MLDv1 done message maps to `TO_IN ({})` (RFC 3810 §8.3.2). Either mapping
(re)starts an older-version-host timer for that group and port, lasting one
GMI. While it runs, a `BLOCK` record is ignored outright and a `TO_EX (x)`
record is treated as `TO_EX ({})`. MLDv2 uses the same tables as IGMPv3 (RFC
3810 §7.4-§7.6).

Fast leave keeps its own shortcut, independent of the tables above: a message
or record shaped like a leave — an IGMPv2 leave, an MLDv1 done, or a
`MODE_IS_INCLUDE`/`CHANGE_TO_INCLUDE_MODE` record carrying no sources —
deletes the port's router state outright instead of going through `TO_IN`.

### Timer expiry (§6.5)

`Age(now)` applies the expiry rules lazily relative to `now`:

- A group timer expiring while the port is in `EXCLUDE` moves it to `INCLUDE`.
  Sources with a running timer are kept with their timer; sources at a zero
  timer are deleted. If nothing is left, the port's router state for that
  group is deleted too.
- A source timer expiring while the port is in `INCLUDE` deletes that source
  record; deleting the last one deletes the port's router state.
- A source timer expiring while the port is in `EXCLUDE` moves that source
  from `X` to `Y`; the record stays, now at a zero timer.

`Resolve` computes the same rules against the `now` it is given without
requiring a prior `Age` call, so a caller's aging cadence does not change the
forwarding answer.

### Forwarding (§6.3)

| Filter mode | Source timer | Action |
| --- | --- | --- |
| `INCLUDE` | running | forward |
| `INCLUDE` | no record | do not forward |
| `EXCLUDE` | running | forward |
| `EXCLUDE` | zero | do not forward |
| `EXCLUDE` | no record | forward |

`Resolve(vid, group, source, now)` applies this table per source and returns
the union of admitted member ports and router ports. `registered` is true when
any port holds router state for the group, regardless of whether the queried
source is admitted on it — a learned router port appears in the returned
ports without setting `registered`.

## Queries and the unobserved-query issue

This package never emits a query; "Send Q" in the tables above only records
that the router state expects one. `Resolve` tracks whether that expectation
was met:

- An observed group-specific query (`Group` set, no `Sources`, `Suppress`
  clear) lowers the matching group timers to `LMQT`, never raising them.
- An observed group-and-source-specific query (`Group` set, `Sources`
  non-empty, `Suppress` clear) lowers the named sources' timers to `LMQT`,
  and only those.
- `LMQT` always comes from configuration, never from a query's `MaxResp` or
  `QRV`. A capture may omit the query entirely.
- When a table row fires a "Send Q" action, the VLAN has at least one router
  port, and no matching query arrives within `LMQT` of that row firing,
  `Resolve` reports the group's forwarding as pending a query, until the
  state changes again.
- With no router port on the VLAN, there is no querier to expect a query
  from: the full timers are the real behavior, and `Resolve` never reports a
  pending query.

## Router ports

An IGMP query learns its ingress as a router port when its IPv4 source is not
`0.0.0.0`. An MLD query requires an IPv6 link-local source. A router port's
`Origin` (`Configured` or `Observed`) names which of the two installed it,
and its `Lifetime` (`Static` or `Aging`) names whether `Age` removes it. The
two axes replace a single `Static` boolean that used to answer both
questions at once: a record from configuration is always `Configured` and
`Static`, and one a learned query installs is always `Observed` and
`Aging`, but the fields are independent so a caller reconstructing runtime
state — `InstallObserved` — can install an `Observed`, `Aging` record
carrying an expiry of its own choosing rather than one the layer computes.
`InstallObserved` refuses to override a port a static `Config` entry already
claims.

`mcast.Entry`, the learned group membership record `Groups` returns, has no
`Origin` field: nothing in `Config` can preload a group membership, so every
`Entry` is observed and the axis has one reachable value. Adding a field
that never varies would document a distinction nothing ever makes.

## Aging and snapshots

Learned router-port entries expire after their configured interval (default
260 seconds, same as `MembershipInterval`), or, when installed through
`InstallObserved`, after the expiry the caller gave. `Age(now)` removes
every `Aging` router port whose expiry has passed, and the group timer and
source timer expiry above, in one pass; it does not touch `Static` router
ports. `Groups` and `RouterPorts` return sorted snapshots, and `Clone`
preserves every timer in an independent layer.

`Resolve` is intentionally narrower than a bridge forwarding policy. It
returns the admitted port union, membership registration, and the pending-
query signal. The caller decides whether an unregistered group floods or uses
the returned router ports, and how a pending query surfaces as an issue.

## State retention

Multicast snooping runtime state is snoop-driven and preserved across derivation
whenever both switches have multicast snooping enabled. `vswitch.Derive` retains
the layer across derivation with per-entry filtering (`Retain`), pruning entries
whose VLAN, port, or spanning-tree forwarding state was lost, and replaying the
surviving records via `InstallObserved` and group learning to keep expiries
intact. Derivation reports `Retention().Mcast` as kept with per-entry drops rather
than an all-or-nothing rebuild.

`RetentionKey(cfg Config, ports port.Table) string` encodes every normalized
input the multicast snooping runtime state depends on: its own configuration as
`Diff` sees it and port states for configured router ports.
