# Routing behavior fixtures

Run the package tests from the netpen module:

```sh
cd src/edge/netpen
go test -race ./attacks/routing
```

The tests use an in-memory link and recorded packet fixtures. WPAD also opens
a temporary HTTP listener on loopback. No test requires a network interface.

`Behaviors()` supplies the OSPF, EIGRP, and WPAD functions for
`runner.Options.Behaviors`. The runner provides dependencies and executes armed
teardown steps after completion or failure. OSPF and EIGRP tolerate a missing
response after 100 ms, but stop on receive or decode errors and cancellation.
EIGRP rejects incomplete, fragmented, or unrelated IPv4 envelopes before decoding.

These sequences model fixture traffic. A finding records an attempted sequence;
the behaviors do not verify route installation or successful restoration. OSPF
packet and LSA checksums are left zero, and its exchange does not implement a full
adjacency state machine. The fixture generator mirrors the packet construction,
so byte equality alone cannot establish protocol correctness.

WPAD serves only a PAC response on an OS-selected loopback port and closes the
listener when the behavior returns. Its credential length and protocol fields
come from a fixed simulated value, not a captured authentication exchange. The
port-collision test checks the error-code spelling; it does not exercise a bind
failure.

`harvest_routing.go` writes the routing pcaps under `../testdata/routing`. It uses
the current time for capture timestamps, so repeated generation changes the pcap
files even when packet bytes remain identical.
