# Mirror receiver integration suite

Proves `rawsocket.OpenMirrorReceiver` against two independent senders it does
not control: the Linux kernel's own `erspan` tunnel device, and an Open
vSwitch `type=erspan` port. Every test builds a scratch network namespace
connected to the host by a veth pair, mirrors egress traffic on the
namespace side onto an ERSPAN tunnel or OVS port addressed back at the host,
and confirms the receiver decodes what arrives.

## Prerequisites

- Linux. The suite is written and cross-compile-checked here; as of this
  writing it has not run on a real Linux host — see the parent plan's
  Verification section for the residual risk this leaves.
- Root, or `CAP_NET_ADMIN` plus `CAP_NET_RAW`: creating a network namespace,
  a veth pair, an `erspan` tunnel device, and `tc` mirred actions all need
  it, and so does the raw socket `OpenMirrorReceiver` itself opens.
- `iproute2` (`ip`, `tc`) on `PATH`.
- `openvswitch` (`ovs-vsctl`, with `ovs-vswitchd` running) on `PATH` for
  `TestOVS_ErspanPort` only; every other test skips cleanly without it.
- A kernel new enough for `erspan_ver 0` (ERSPAN Type I) for
  `TestErspanTunnel_TypeI`: support landed after the initial `erspan_ver`
  1/2 (Type II/III) support, in Linux 4.18. The test probes for it with a
  throwaway `ip link add ... erspan_ver 0` before creating the real tunnel,
  and skips with a clear message rather than assuming any given host has it.
  `erspan_ver` 1 and 2 need no such probe.

## Running

```bash
go test -tags=capture_mirror_integration ./src/modules/capture/rawsocket/test/integration/...
```

Choose this tier explicitly; it never runs as part of the default
`go test -race ./...` sweep. Every test that cannot meet its prerequisites
skips with a message naming what was missing, rather than failing the run.

## What is proved, and what is not

Each test creates real ERSPAN- or OVS-wrapped traffic from an independent
encoder (the kernel's or Open vSwitch's own, neither written by this
repository) and confirms the receiver's decoded envelope carries the
expected wrapper arm. This is the independent-encoder leg the parent plan's
Verification section requires for ERSPAN Type I, II, and III and plain GRE.

It is not a substitute for a shipping ASIC's own bytes: the mirrored traffic
here originates on this same host's kernel or OVS instance, and the ERSPAN
Type III fields the Linux `erspan` tunnel device does not populate (the
security group tag, the non-Ethernet frame type) are exercised only by
`src/modules/capture/mirror`'s own hand-built fixtures, which is a weaker
form of evidence — see the phase 2 plan's Decisions and Verification
sections for why, and for the correction this suite's design responds to
(the Wireshark "erspan-marker" sample captures do not contain what they were
assumed to).
