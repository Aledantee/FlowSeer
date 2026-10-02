# ICX7150 comparison

Run this test on the Linux host connected to two access ports of LABSW06.
The test simulates the same cloned finite sources before sending them through
the switch. It prints a JSON comparison for a 10,000-frame flow and two
interleaved 5,000-frame flows. Both runs offer 1,000 frames per second in
aggregate and use 128 wire octets per frame.

Set the actual NIC names, switch ports, access VLAN, NIC MACs, and negotiated
host-facing speeds before running:

```sh
export NETSIMLOAD_LAB_TX_INTERFACE=eth1 NETSIMLOAD_LAB_RX_INTERFACE=eth2
export NETSIMLOAD_LAB_TX_PORT=1/1/1 NETSIMLOAD_LAB_RX_PORT=1/1/2
export NETSIMLOAD_LAB_VLAN=10
export NETSIMLOAD_LAB_TX_MAC=02:00:00:00:00:01 NETSIMLOAD_LAB_RX_MAC=02:00:00:00:00:02
export NETSIMLOAD_LAB_TX_SPEED_BPS=1000000000 NETSIMLOAD_LAB_RX_SPEED_BPS=1000000000
go test -tags=netsimload_lab -run '^TestICX7150Comparison$' -count=1 -v ./src/edge/netsimload/test/integration
```

Replace every example value with the lab's observed wiring. The test checks
that each named NIC is up and has the configured MAC. Both switch ports must
be untagged members of the configured VLAN. The test connects once to
LABSW06 at `172.16.0.6:22` before opening raw sockets; an unreachable switch
ends the run without retry. A completely unset configuration skips. A partial
or malformed configuration fails before any traffic. Linux raw packet sockets
require `CAP_NET_RAW` or root.

The blast radius is data-plane traffic only: at most 20,000 unicast frames,
2.56 MB on the wire, over about 20 seconds at 1,000 frames per second.
The source and destination are the configured NIC MACs; EtherType is `0x88b5`,
and the first 32 payload octets carry the `FSLD` flow signature. No switch
configuration changes occur. The run stops on the first sender or receiver
error, reports an unreachable switch once, and closes its sockets after each
case. It updates ordinary switch MAC-table and port counters.

Before each live run, record the actual ports, VLAN, NICs, and MACs in the
operator handoff and obtain the owner's go-ahead. A missing sequence stays a
lab observation; the report does not claim a switch drop reason. The pacing
check compares capture timestamps against the scheduled 9.999-second span,
with a 1% bound and zero interface-reported capture drops.
