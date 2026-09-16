# routing

Package `routing` implements layer 3 forwarding for the virtual switch: per-VRF
routed interfaces, a forwarding table built from the interface prefixes and the
configured static routes, neighbor entries, and the rules that pick one next hop
for a packet.

The layer runs deterministically in memory, with no background goroutines and no
wall clock of its own; `now` is a parameter the caller supplies. `Route` and
`Originate` read the packet and the table and, with `commit` set, may also
update the neighbor table described below. With `commit` clear neither one
mutates anything, so the same packet against the same configuration always
leaves the same way and looking at a decision cannot change it — see "Neighbor
lifecycle" for why that distinction exists.

## Example

Two equal-cost routes to one prefix, a third route whose next hop nothing
reaches, and one packet through them.

```go
package main

import (
	"fmt"
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

func main() {
	routerMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	hostMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	viaVLAN10 := netip.MustParseAddr("10.0.10.254")
	viaVLAN20 := netip.MustParseAddr("10.0.20.254")

	cfg := routing.Config{VRFs: map[string]routing.VRF{
		routing.DefaultVRF: {
			Interfaces: map[string]routing.Interface{
				"vlan10": {VLAN: 10, MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
				"vlan20": {VLAN: 20, MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
			},
			Routes: []routing.Route{
				{Prefix: netip.MustParsePrefix("192.0.2.0/24"), NextHop: viaVLAN10, Preference: 1, Metric: 10},
				{Prefix: netip.MustParsePrefix("192.0.2.0/24"), NextHop: viaVLAN20, Preference: 1, Metric: 10},
				{Prefix: netip.MustParsePrefix("198.51.100.0/24"), NextHop: netip.MustParseAddr("203.0.113.1")},
			},
			Neighbors: []routing.Neighbor{
				{Interface: "vlan10", Addr: viaVLAN10, MAC: netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x0a}},
				{Interface: "vlan20", Addr: viaVLAN20, MAC: netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x0b}},
			},
		},
	}}

	layer, err := routing.New(cfg, port.Table{}, "sw1")
	if err != nil {
		panic(err)
	}

	hdr := ip.Header{
		Src:      netip.MustParseAddr("10.0.10.7"),
		Dst:      netip.MustParseAddr("192.0.2.5"),
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}
	pkt, err := hdr.Encode([]byte("payload"))
	if err != nil {
		panic(err)
	}

	now := time.Now()
	res := layer.Route(now, "vlan10", ethernet.Frame{
		Src:       hostMAC,
		Dst:       routerMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}, true)

	fmt.Printf("egress %s towards %s\n", res.Interface, res.Frame.Dst)
	for _, c := range res.Candidates {
		fmt.Printf("candidate %s via %s on %s\n", c.Prefix, c.NextHop, c.Interface)
	}
	for _, w := range layer.WithdrawnRoutes(routing.DefaultVRF) {
		fmt.Printf("withdrawn %s via %s: %s\n", w.Prefix, w.NextHop, w.Reason)
	}
}
```

```
egress vlan20 towards 00:22:33:44:55:0b
candidate 192.0.2.0/24 via 10.0.10.254 on vlan10
candidate 192.0.2.0/24 via 10.0.20.254 on vlan20
withdrawn 198.51.100.0/24 via 203.0.113.1: unresolved
```

Both routes to `192.0.2.0/24` stay in the table and both are reported; which one
carries this packet is the flow hash below. The route to `198.51.100.0/24` is
valid configuration, but no chain of routes in the VRF reaches `203.0.113.1`, so
the table does not hold it.

## Route order

A lookup takes the longest prefix containing the destination, then the lowest
preference, then the lowest metric.

`Preference` is the administrative distance, lower winning, and `Metric` breaks
the tie within one preference. The metric comparison is deliberately not global:
Cisco compares metrics only "if there are multiple paths to the same destination
from a single routing protocol", and metrics from different sources are not
comparable numbers. In a static-only table one preference is one source, so the
distinction does not bite yet; it will as soon as a dynamic protocol lands.

`Preference` is a `uint8`, which covers the Cisco administrative distance range
of 0 to 255. It cannot express a Junos preference, whose range runs to
4,294,967,295.

Preference 0 is reserved for connected routes, which are built at preference 0
and metric 0. A static route configured at 0 normalizes to 1, following NX-OS,
where a static route's "range is from 1 to 255. The default is 1" over a table
that puts connected at 0 and static at 1. The reservation also means a `uint8`
needs no pointer to tell an unset field from an explicit 0. Without it a static
route at preference 0 would tie the connected route covering its prefix, join its
candidate set, and the flow hash would spread one destination between on-link
delivery and a next hop — an outcome no vendor documents.

## The candidate set

Every route that contains the destination and ties the winner on prefix length,
preference, and metric is a candidate, and all of them are reported in
`Result.Candidates`, so a reader can tell an alternative path from a path that
was never there. An equal-length prefix that does not contain the destination is
a different prefix and never joins the set.

Candidates are ordered by next hop, then egress interface. The order is part of
the contract: the reduction below turns a hash into an index into this slice, and
the route fact's canonical string is compared verbatim across constructions.

One prefix carries at most 64 equal-cost routes. That is FRR's number — it "is
generally compiled with a limit of 64 way ECMP" — and an uncapped set would be
netsim inventing the one behavior no router has. Above the cap the first 64 in
canonical order are installed and the rest are withdrawn as `max-paths`. The cap
applies twice, because there are two ways to exceed it: to the paths one
recursive route inherits from the route that resolved it, and to the equal routes
several configured routes put on one prefix.

## The flow hash

The candidate is chosen by hashing the packet's layer 3 flow with FNV-1a
(`hash/fnv`'s `fnv.New32a`), over these bytes in this order:

1. Source address (4 octets for IPv4, 16 for IPv6).
2. Destination address (4 or 16 octets).
3. IPv6 flow label (4 octets, big-endian uint32). IPv4 contributes nothing here.

Nothing else enters. No protocol octet, no ports. This is what a router nobody
configured hashes: Cisco CEF's default per-destination load sharing takes the
source and destination addresses with no transport fields, and Linux's default
`fib_multipath_hash_policy=0` is layer 3, with ports entering only at policy 1.
RFC 2991 section 3 warns that "including transport-layer information in the
next-hop selection process can actually be problematic". A five-tuple would model
a box someone had configured, and netsim has no field that says they did — the
hash input is the vendor default, not a knob, and `ip cef load-sharing full` and
`fib_multipath_hash_policy` have no equivalent here.

Two consequences are worth stating, because they are why the input is short:

- Every fragment of a datagram takes one next hop, with no fragment rule in the
  code. The transport ports live in the first fragment alone, so a hash that read
  them would have had to buy this property back with a fragment test. RFC 2991
  section 2 wants a flow on one path for reordering and path-MTU reasons, and
  reassembly is not even on its list.
- A TCP segment behind an IPv6 extension header hashes like one without. `ip.Decode`
  does not walk the extension header chain, so `Header.Protocol` is the fixed
  header's first Next Header; hashing it would split one flow on the headers its
  packets happen to carry. The flow label is in the fixed header instead, and
  RFC 6437 put it there precisely "to enable and encourage the use of the flow
  label for various forms of stateless load distribution, especially across Equal
  Cost Multi-Path (ECMP) and/or Link Aggregation Group (LAG) paths".

FNV-1a folds the last bytes written in with the fewest multiplications, so flows
that differ only in the final octet of the destination move the hash by about
1/256 of the space each. A run of consecutive destinations from one source can
therefore sit in one region and take one next hop. Spreading a synthetic load
across paths takes a spread of addresses, not a spread of last octets.

## The reduction: hash-threshold, not modulo

The 32-bit hash space is cut into as many contiguous equal-width regions as there
are candidates, in canonical order, and the packet takes the candidate whose
region holds its hash (RFC 2992). The last region absorbs the remainder.

The reduction is not `hash % len(candidates)`, which would be shorter and wrong
for what netsim is for. RFC 2992 measures modulo-N as "the most disruptive of the
algorithms; if a next-hop is added or removed the disruption is (N-1)/N", against
"between 1/4 and 1/2" for hash-threshold. "Which flows move when this path fails"
is the question this table exists to answer, and under modulo-N nearly every flow
in the VRF moves when any path does. Linux reduces by cumulative upper bounds and
Cisco by a 16-bucket load-share table; hash-threshold is the published form of the
same idea.

What the reduction buys is a bound, not immobility. When a candidate goes, every
region grows, so a flow can only slide down into the region below it: the first
candidate keeps everything it had, and no flow lands on a candidate it was not on
or below. Flows near a region's lower edge do move, which is the 1/4 to 1/2 above.

There is no bucket table and no remembered state, unlike the bond hash in
`lag`, which persists a flow across a member's return. The routing table is
rebuilt whole on every `Derive`, and hash-threshold gives the disruption bound
with nothing to keep. `Originate` selects the same way, over the source address
it chose and the destination, so it reads nothing from the caller's payload.

## Recursive next hops

A static route whose next hop is not on-link resolves against its own VRF's table
when the table is built, and installs carrying the on-link next hop and interface
it reached. `Route.NextHop` keeps the configured value for `Canonical`, `Diff`,
and the facts; the resolved pair is what the frame is built for, because the
neighbor lookup keys on the next hop rather than on the egress interface.

Resolution walks at most 8 routes. Cisco names a "maximum IPv6 forwarding
recursion depth" without publishing a value, BIRD allows exactly one level, and
FRR bounds recursion by its self-match test instead. Eight is netsim's own
number, above BIRD's so an ordinary two-step chain resolves, and low enough to
catch a configuration mistake; a real static chain is one or two hops, so the
bound guards against a loop rather than limiting anyone.

A next hop does not resolve through the default route, following FRR: "Nexthop
tracking doesn't resolve nexthops via the default route by default". Without that
rule every unresolvable next hop in a VRF carrying a default route would quietly
resolve through it.

Resolution walks the whole table, which is netsim's simplification. FRR resolves a
next hop only against the selected, installed route for the matching prefix, and
only for route types with recursion enabled. In a static-only table with one
selection rule those gates coincide with walking the table; a dynamic protocol
would end that and the resolver would have to grow them.

Resolution consults routes and connected prefixes only, never neighbors. A next
hop the table reaches but no neighbor entry resolves is a per-packet drop, not a
withdrawal — see below.

## Withdrawn routes

`Layer.WithdrawnRoutes(vrf)` returns the configured static routes the forwarding
table does not hold, sorted by prefix then next hop, each with the reason and the
chain of prefixes resolution walked. `Chain` starts at the route's own prefix and
ends where resolution stopped, so a self-recursive route reads
`[10.0.0.0/8, 10.0.0.0/8]` and an `A via B`, `B via A` cycle reads `[A, B, A]`.
The reasons are `self-recursive`, `depth-exceeded`, `unresolved`, and
`max-paths`.

A withdrawn route is valid configuration, not a construction error: real gear
keeps it in the configuration and out of the routing table, and so does netsim.
Forwarding then answers from the routes that remain — a less specific route, or
`no-route` — and that answer is complete, not an admission that netsim lacked an
input.

A withdrawal, a `neighbor-miss`, and a `neighbor-pending` are three different
answers. A withdrawn route means no chain of routes reaches the next hop, and
it is decided once, when the table is built. A `neighbor-miss` means the VRF
will never resolve the next hop to a MAC address — it does not observe
neighbors at all, or it already tried and gave up. A `neighbor-pending` means
the table reached the next hop and resolution is still in progress; the next
section describes when a lookup lands on which of the two.

## Neighbor lifecycle

A neighbor entry occupies one of five states: `Unobserved` (no entry — the
zero value, not a state a lookup ever reports as such), `Incomplete`,
`Reachable`, `Stale`, and `Failed`. The set is RFC 4861 section 7.3.2's IPv6
state machine, which netsim also runs for ARP: Linux keeps one neighbour table
for both families, and inventing a second vocabulary for IPv4 would buy
nothing a shared one does not already say. `Delay` and `Probe` are
deliberately absent — both exist only to schedule a unicast solicitation
toward Neighbor Unreachability Detection, and netsim never solicits, so no
input can reach either one. A state a test can never drive is worse than no
state at all.

A configured `routing.Neighbor` enters the table as `Reachable` with no
expiry, so a static binding never ages out; that is `New`'s job, not the state
machine's. Everything else on the table is driven by two calls:

- **`Layer.Observe(now, Advertisement)`** applies RFC 4861 section 7.2.5 to
  an observed link-layer address binding, over both families through one
  family-neutral record. `Advertisement` carries the interface, the address,
  the MAC, whether a link-layer address was supplied at all, and the
  solicited, override, and router flags. If no entry exists for the address,
  Observe does nothing — "there is no need to create an entry if none exists,
  since the recipient has apparently not initiated any communication with the
  target." An `Incomplete` entry records the address and moves to `Reachable`
  when the advertisement is solicited or to `Stale` otherwise (Override is
  ignored in this case). Elsewhere, Override clear and a differing address
  moves a `Reachable` entry to `Stale` without adopting the new address, and
  leaves any other state untouched; Override set (or no address supplied, or
  the address already matches) updates the cache, landing on `Reachable` when
  solicited or `Stale` when the update changed the address.

  ARP has no solicited or override flags of its own, so the switch maps
  them: a reply becomes `{Solicited: true, Override: true, Router: false}`
  and a request's sender fields become
  `{Solicited: false, Override: true, Router: false}`. RFC 826's merge rule
  overwrites a known sender's hardware address unconditionally, which is what
  `Override: true` produces under rule II regardless of which family sent it;
  `Solicited` then carries the one difference the RFC 4861 table needs: a
  reply confirms the forward path a solicitation was sent on, and a request
  only refreshes the binding.

- **`Layer.Wake(now)`** turns a state change into an effect. An `Incomplete`
  entry whose resolution deadline has passed becomes `Failed`, and its held
  frames are reported in `Effects.Failed`, still carried rather than
  discarded — nothing here ever generates the retransmissions RFC 4861 counts
  against, so `Failed` names a timeout and never an exhausted solicitation
  count. Any entry that already holds frames and is no longer `Incomplete`
  (because `Observe` resolved it since the previous `Wake`) has them reported
  in `Effects.Released`. Either way the queue is drained, so calling `Wake`
  again before anything else changes reports nothing further.
  `Layer.NextWake()` reports the earliest `Incomplete` deadline, and
  `Layer.Age(now)` is the separate timer that moves a `Reachable` entry past
  its `ReachableTime` to `Stale`.

A `Stale` entry forwards on its cached MAC and stays `Stale`. RFC 4861
section 7.3.2 says of `STALE` that "until traffic is sent to the neighbor, no
attempt should be made to verify its reachability," and real gear would move
to `Delay` and start probing on the next send; netsim has no probe to send,
so it forwards and leaves the state alone. Treating a `Stale` hit as a drop
would reintroduce exactly the invented failure this lifecycle exists to
remove.

### The hold queue

Under `NeighborObserved`, a `Route` or `Originate` call that misses the
table and is told to `commit` creates an `Incomplete` entry (if one does not
already exist) and appends the frame to that neighbor's hold queue, bounded
by `NeighborPolicy.HoldDepth`. RFC 4861 section 7.2.2: "the number of queued
packets per neighbor SHOULD be limited to some small value. When a queue
overflows, the new arrival SHOULD replace the oldest entry." `HoldDepth`
defaults to 3 rather than the permitted minimum of 1, because an analysis
library is asked which of several frames arrived, and a depth of 1 answers
that for the last one only.

`commit` is what makes a preview safe. With it clear, a miss reports
`neighbor-pending` without creating an entry or queuing anything, so
`Switch.Peek` can be called any number of times without perturbing what a
later commit sees, and two consecutive comparisons of the same pair of
switches agree. Under `NeighborDisabled`, or against an entry already
`Failed`, both values of `commit` report `neighbor-miss` and change nothing —
there is nothing pending to preview.

### Policy

`VRF.NeighborPolicy` holds `Mode` (`NeighborObserved`, the zero value, or
`NeighborDisabled`), `ReachableTime`, `ResolutionTimeout`, and `HoldDepth`.
Left at zero, the timers and depth normalize to the RFC 4861 section 10
defaults: `ReachableTime` 30 seconds, `ResolutionTimeout` 3 seconds
(`MAX_MULTICAST_SOLICIT` × `RETRANS_TIMER`, 3 × 1 second), and `HoldDepth` 3.
`NeighborDisabled` is for a VRF — a host stack, say — that never resolves an
address it was not told about: every miss there is `neighbor-miss`, never
`neighbor-pending`.
