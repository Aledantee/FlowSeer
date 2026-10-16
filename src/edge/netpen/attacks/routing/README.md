# Routing behavior fixtures

Run the package tests from the netpen module:

```sh
cd src/edge/netpen
go test -race ./attacks/routing
```

The tests use an in-memory link and recorded packet fixtures. WPAD also opens
a temporary HTTP listener through the `check.Deps.Listen` seam, which the
tests replace with an in-memory listener. No test requires a network
interface.

`Behaviors()` supplies the OSPF, EIGRP, and WPAD functions for the catalog.
The runner provides dependencies and executes armed teardown steps after
completion or failure.

Each behavior reads the wire for the effect its probe should have:

- `ospf` sends a hello, reads the target's hello, then sends a hello
  listing the target every 10 s while reading for the target's hello that
  lists the attack leg's router ID (RFC 2328 section 10.5, 2-WayReceived).
  That hello is `confirmed`, and the database description and router-LSA
  update follow it. A hello from the target without it at the window's end
  is `resisted`, no hello from the target is `resisted`, and a window under
  10 s is `pending` naming `--observe 20s`.
- `eigrp` sends a hello with the Parameters TLV, reads a hello in its
  autonomous system, sends the null update with the Init flag, and reads
  for the target's unicast acknowledgment (RFC 7868 sections 5.2 and 5.3.3).
  The acknowledgment is `confirmed`, and the route update follows it. A
  hello carrying the Peer Termination TLV, or the older K-values-of-255
  form, is `resisted` as a refused adjacency, and no hello in the system is
  `resisted`.
- `wpad` binds the attack leg's IPv4 on port 80, waits for an NBT-NS or
  LLMNR query for `wpad`, answers it unicast with the query's id, and reads
  the listener's request log. A request for `/wpad.dat` is `confirmed`, an
  answered query without one is `resisted`, and no query is `pending`.

All three stop on receive or decode errors and cancellation. EIGRP rejects
incomplete, fragmented, or unrelated IPv4 envelopes before decoding.

A `confirmed` verdict is netpen's own reading of the frames the peer sent;
the behaviors do not verify route installation or successful restoration.
OSPF packets carry valid RFC 2328 checksums (the header checksum and the
Fletcher LSA-body checksum), which real IOS-XE requires to form an
adjacency, but the exchange does not implement a full adjacency state
machine. The fixture generator mirrors the packet construction, so byte
equality alone cannot establish protocol correctness; the response fixtures
are authored from the RFC layouts and read back by gopacket or the owned
layer.

WPAD serves only a PAC response and closes the listener when the behavior
returns. The HTTP handler records request paths and nothing else; it does
not capture authentication exchanges or forward proxy traffic.

`harvest_routing.go` writes the routing pcaps under `../testdata/routing`.
It stamps every packet with the epoch, so repeated generation leaves a
fixture byte-identical unless its frames change.
