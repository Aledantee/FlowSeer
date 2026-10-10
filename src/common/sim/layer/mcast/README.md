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

IGMPv1 and IGMPv2 reports map to `IS_EX ({})` (RFC 3376 §7.3.2), as do
MLDv1 reports (RFC 3810 §8.3.2). Each group and port keeps separate IGMPv1
and IGMPv2 host timers, with the latter also serving MLDv1. Only a report of
that version starts or refreshes its timer, for one GMI. While either timer
runs, `BLOCK` is ignored and `TO_EX (x)` is treated as `TO_EX ({})`.
While the IGMPv1 timer runs, IGMPv2 Leave and `TO_IN` are also ignored.
MLDv2 uses the same tables as IGMPv3 (RFC 3810 §7.4-§7.6).

An IGMPv2 Leave or MLDv1 Done maps to `TO_IN ({})` and starts no host timer.
The layer also uses that mapping when no compatibility timer runs, a case
those compatibility sections do not specify.

Fast leave keeps its own shortcut, independent of the tables above: a message
or record shaped like a leave — an IGMPv2 leave, an MLDv1 done, or a
`MODE_IS_INCLUDE`/`CHANGE_TO_INCLUDE_MODE` record carrying no sources —
deletes the port's router state outright instead of going through `TO_IN`.
The IGMPv1 rule still ignores Leave and `TO_IN`, even with fast leave enabled.

### Timer expiry (§6.5)

`Resolve`, `Groups`, and `RouterPorts` read state as of their `now` without
writing. `Learn` and `LearnMLD` age a port's state before applying a record,
so a report uses the transition for the mode its timers leave at that time.
`Advance(now)` removes what the reads already ignore:

- A group timer expiring while the port is in `EXCLUDE` moves it to `INCLUDE`.
  Sources with a running timer are kept with their timer; sources at a zero
  timer are deleted. If nothing is left, the port's router state for that
  group is deleted too.
- A source timer expiring while the port is in `INCLUDE` deletes that source
  record; deleting the last one deletes the port's router state.
- A source timer expiring while the port is in `EXCLUDE` moves that source
  from `X` to `Y`; the record stays, now at a zero timer.

For example, a report and query learned at `t0` on the default intervals
leave no membership or learned router port at `t0 + 300s`, even without an
`Advance` call. `TestResolveIgnoresExpiredState` in `layer_test.go` checks it.

### Forwarding (§6.3)

| Filter mode | Source timer | Action |
| --- | --- | --- |
| `INCLUDE` | running | forward |
| `INCLUDE` | no record | do not forward |
| `EXCLUDE` | running | forward |
| `EXCLUDE` | zero | do not forward |
| `EXCLUDE` | no record | forward |

`Resolve(vid, group, source, now)` applies this table per source and returns
the union of admitted member ports and static or unexpired learned router
ports. `registered` is true when any port's group timer runs in `EXCLUDE` or
any of its source timers runs, regardless of whether the queried source is
admitted on it. A learned router port appears in the returned ports without
setting `registered`. Only that live group state and the live router ports
can produce a pending query.

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

A group obligation ends on an unsuppressed `Q(G)` or a record that sets the
group timer. A source obligation keeps the sources that its table row
queried. Each source leaves that set when an unsuppressed `Q(G,A)` names it,
a record sets its timer, or the source is deleted. The source obligation
ends when the set is empty. These rules apply however late the query arrives.
A `Q(G)` leaves source obligations intact because it lowers only the group
timer (RFC 3376 §6.6.1). `Clone` and `Restore` copy both compatibility timers
and the queried set independently.

## Router ports

An IGMP query learns its ingress as a router port when its IPv4 source is not
`0.0.0.0`. An MLD query requires an IPv6 link-local source. A router port's
`Origin` (`Configured` or `Observed`) names which of the two installed it,
and its `Lifetime` (`Static` or `Aging`) names whether `Advance` removes it. The
two axes separate origin from expiry: a record from configuration is always
`Configured` and `Static`, and one a learned query installs is always `Observed`
and `Aging`. `Restore` copies an observed router port with its original expiry,
even when the target's `RouterPortInterval` differs. It leaves a target static
router port in place and copies no configured record from the previous layer.
The zero values of `Origin` and `Lifetime` name neither value of their type.

`mcast.Entry`, the learned group membership record `Groups` returns, has no
`Origin` field: nothing in `Config` can preload a group membership, so every
`Entry` is observed and the axis has one reachable value. Adding a field
that never varies would document a distinction nothing ever makes.

## Aging and snapshots

Learned router-port entries expire after their configured interval (default
260 seconds, same as `MembershipInterval`). A record copied by `Restore`
keeps its original expiry. `Advance(now)` removes
every `Aging` router port whose expiry has passed, and the group timer and
source timer expiry above, in one pass. It does not touch `Static` router
ports. `Groups(vid, now)` and `RouterPorts(vid, now)` return sorted snapshots
of the state left by expiry at `now`, without changing stored state.
`fabric.Snapshot` passes the fabric clock. `Clone` preserves every timer in
an independent layer. `TestReadsMatchAdvance` in `layer_test.go` compares
reads with and without aging first.

`Resolve` is intentionally narrower than a bridge forwarding policy. It
returns the admitted port union, membership registration, and the pending-
query signal. The caller decides whether an unregistered group floods or uses
the returned router ports, and how a pending query surfaces as an issue.

## State retention

`Restore(prev, keep)` copies each port's group state on VLANs both layers snoop
when the port is logical in the target and `keep(vid, port)` admits it. The copy
preserves the filter mode, group and source deadlines, both compatibility deadlines,
and pending queries. Its maps are independent of `prev`. The target's settings
apply to subsequent learning.

For example, `TestRestore` in `layer_test.go` restores `INCLUDE {10.0.0.9}`
with its source deadline unchanged into a layer with a different membership
interval. Replaying it as a legacy report would discard that source filter.

`vswitch.Derive` calls `Restore` with a predicate that checks port forwarding,
VLAN membership, and spanning-tree forwarding. It reports `Retention().Mcast`
as kept whenever both switches have multicast snooping enabled, including when
individual entries are dropped. `TestDeriveKeepsSourceSpecificMulticast` in
`src/common/sim/device/vswitch/derive_internal_test.go` checks the surviving
source filter and its forwarding result after an unrelated VLAN edit.

`RetentionKey(cfg Config, env layer.Env) string` encodes every normalized
input the multicast snooping runtime state depends on: its own configuration as
`Diff` sees it and port states for configured router ports.

## Limits

The layer emits no query and takes LMQT from configuration. One
`MembershipInterval` stands for both GMI and the Older Host Present Interval
(RFC 3376 §8.13) or Older Version Host Present Timeout (RFC 3810 §9.13).
A learned router port uses the layer's configured `RouterPortInterval` as
its lifetime. RFC 4541 §2.1.1 item 1 describes router-port discovery without
specifying a lifetime.
The switch accepts reports from `::` under RFC 4541 §3, but requires a
link-local source for queries and Done. What a snooping switch should do
with a query or Done from `::` is unverified.
A group query obligation ends on an observed group query or on a record
that sets the group timer, and not when the EXCLUDE state it belongs to
ends. After a change to INCLUDE with no group query observed, `Resolve`
reports the obligation pending for as long as a host keeps refreshing a
source (`obligationPending` in `state.go`).

## Configuration diffs

`Diff` compares normalized VLAN configurations and effective flood policy
([diff.go](diff.go)). Router ports use quoted canonical member coordinates
and `mcast.router_port` facts. Replacing eth2 with eth3 emits
`router_ports["eth2"]: eth2 -> nil` followed by
`router_ports["eth3"]: nil -> eth3`. Old-only removals precede new-only
additions in normalized order. Reordering and duplicate-only edits produce no
records ([diff_coverage_test.go](diff_coverage_test.go)).

Retained VLANs compare flood policy, fast leave, router members, then timer
and query-count fields. VLAN addition and removal retain complete snapshots.
