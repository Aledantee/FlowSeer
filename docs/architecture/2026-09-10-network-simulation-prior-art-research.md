---
title: Network Simulation Prior Art Research
date: 2026-09-10
scope: src/common/netsim
confidence: medium-high
---

# Network simulation prior art research

## Executive summary

The tools that answer "what does this frame do on this device" split into
three families, and FlowSeer's virtual switch sits in a fourth gap none of
them fills:

- **Emulators** run the vendor's or a stand-in's real code: GNS3, EVE-NG,
  Containerlab, SONiC-VS, Open vSwitch, Mininet. Highest fidelity, need root
  or a VM, cannot be diffed against a typed expectation, and answer for
  their own dataplane.
- **Discrete-event simulators** model devices as code over a virtual clock:
  ns-3, OMNeT++ INET, Packet Tracer, and in Go ByteDance's ns-x. They give
  the best trace shape (per-device, per-layer operations on a PDU) and the
  event model a multi-device fabric needs.
- **Model-based verifiers** compute forwarding from configuration without
  moving a packet: Batfish, Forward Networks, the Header Space Analysis
  line of work. They give the comparison shape (a reference snapshot against
  a candidate, every affected flow listed) and the ambition of "all flows",
  but their L2 modelling is thin; Batfish stores VLANs and does not apply
  them in reachability (issue #783).

FlowSeer needs a typed, pure, diffable L2 model that loads from its own
network model and traces one frame or, later, every class of frame. That is
the verifier family's question answered with the simulator family's trace,
over a bridge whose rules come from IEEE 802.1Q. What the plan borrows from
each is listed under "What FlowSeer adopts".

## Findings by tool

### Cisco Packet Tracer (simulation mode)

The PDU Information window shows "how the packet is processed at each layer
of the OSI model by the current device", split into incoming and outgoing
stacks, and names seven operations a device may apply at a layer:
Encapsulate, De-encapsulate, Transfer, Accept, Queue, Drop, Transmit
([PDU Information](https://cisco-packet-tracer-help.yue.zone/default/mode_simulation_PDUinfo.htm)).
Simulation time is event-driven: "Time only advances when there are events
to be captured", and the event list carries Time, Last Device, At Device,
Type, and Info
([Simulation Mode](https://tutorials.ptnetacad.net/help/default/mode_simulation.htm)).
Packet Tracer is closed source and models Cisco IOS only; its value here is
the trace vocabulary, which operators already know how to read.

### Batfish

Differential questions run "by using `snapshot=<current snapshot>` and
`reference_snapshot=<reference snapshot>`", and results pair columns as
`Snapshot_Traces` against `Reference_Traces`
([Differential Questions](https://batfish.readthedocs.io/en/stable/notebooks/differentialQuestions.html)).
Forwarding change validation runs three steps: test current behaviour,
verify the intended effect with `reachability`, and prove no collateral
damage with `differentialReachability`, which searches "for flows that are
successfully delivered in one snapshot but not the other"; traces list a
per-hop action (RECEIVED, FORWARDED, TRANSMITTED, ACCEPTED, DENIED) and a
disposition
([Forwarding Change Validation](https://batfish.readthedocs.io/en/stable/notebooks/linked/introduction-to-forwarding-change-validation.html)).
Topology is layered: supplied Layer-1 adjacencies are combined with Layer-2
and Layer-3 configuration
([Topology](https://batfish.readthedocs.io/en/latest/notebooks/topology.html)).
The L2 gap is documented: hosts on different access VLANs of one subnet are
reported reachable, because VLAN data is stored and not applied
([issue #783](https://github.com/batfish/batfish/issues/783)); VLAN
translation is unsupported
([issue #6576](https://github.com/batfish/batfish/issues/6576)). Batfish
parses vendor configs and runs on a JVM, which the shadow record already
weighed.

FlowSeer's bounded differential search package (`src/common/netsim/search`)
adopts Batfish's `differentialReachability` question for change validation,
evaluating whether prospective changes introduce behavioral divergence. Unlike
Batfish, FlowSeer applies exact IEEE 802.1Q bridging, FDB learning, and STP
filtering directly to Layer-2 networks, exploring finite traffic and fault
domains (`L2TrafficDomain`, `TimedFaultDomain`) with deterministic counterexample
reduction (`search.Minimize`) and trace alignment (`search.Align`). Routed
Layer-3 domain reachability (`L3Domain`) is deferred to a subsequent increment.

### Header Space Analysis and Forward Networks

Forward Networks describes "a mathematically accurate model of every device,
path, and policy" that computes "all possible paths that traffic can
take—not just those paths that are being used"
([Why Network Verification Requires a Mathematical Model](https://www.forwardnetworks.com/blog/resource/why-network-verification-requires-a-mathematical-model/)).
The public foundation is Header Space Analysis, which models a box as a
transfer function on (header, port) and composes those along paths; the NSDI
2012 paper was not reachable from this host (403), so it is cited by name
only. The point for FlowSeer: an exhaustive L2 differential is finite. A
customer bridge's forwarding depends on the ingress port, the classified
VID, and the destination class (known unicast per FDB entry, unknown
unicast, group, reserved), so "every flow that changes" can be enumerated
without symbolic machinery.

### ns-3 and ns-x

ns-3's abstractions are the node ("the basic computing device"), the
channel ("the basic communication subnetwork abstraction", which "can model
anything from simple wires to complex systems like Ethernet switches"), the
net device installed in a node to reach a channel, and topology helpers
([Conceptual Overview](https://www.nsnam.org/docs/tutorial/html/conceptual-overview.html)).
The simulator "will immediately jump from 100 seconds to 200 seconds" to the
next event, and times are 64-bit integers at a chosen resolution
([Manual](https://www.nsnam.org/docs/manual/singlehtml/index.html)). ns-x is
the same idea in Go: nodes decide "what to do when a packet going through",
a builder chains them, and the event loop guarantees order across time
points ([bytedance/ns-x](https://github.com/bytedance/ns-x)). Neither models
an 802.1Q bridge; ns-3's CSMA channel and ns-x's Broadcast node are hubs.

### OMNeT++ INET

`EthernetSwitch` "models an Ethernet switch containing a relay unit and one
MAC unit per port". The relay is `MacRelayUnit` for plain switching or
`Ieee8021dRelay` "when STP or RSTP is needed"; `MacForwardingTable` is a
separate module whose "entries are deleted if their age exceeds a certain
limit" and which "can be pre-loaded from text files" as (VLAN, MAC, port)
lines ([The Ethernet Model](https://inet.omnetpp.org/docs/users-guide/ch-ethernet.html)).
Since INET 4.3 the relay units "no longer work on Ethernet frames only. They
simply expect the packets to contain the necessary metadata such as the
incoming interface indication (InterfaceInd) and the source and destination
MAC address indication (MacAddressInd)"
([INET 4.3.0 release](https://inet.omnetpp.org/2021-01-13-INET-4.3.0-released.html)).
Tagging is its own module, `Ieee8021qTagEpdHeaderInserter`, with a
`vlanTagType` of "c" or "s"
([neddoc](https://doc.omnetpp.org/inet/api-current/neddoc/inet.linklayer.ieee8021q.Ieee8021qTagEpdHeaderInserter.html)).
Packets carry requests and indications as tags separate from the bytes
([Developing Models](https://inet.omnetpp.org/docs/developers-guide/ch-developing-models.html)).
This is the closest structural prior art: port units, a relay, a
forwarding table, and a tagger as separate parts.

### P4 bmv2 `simple_switch`

The pipeline is ingress, a queueing buffer, then egress. Ingress sets
`egress_spec` ("which output port a packet will go to") or `mcast_grp`, and
the buffer makes "0 or more copies of the packet (including all metadata)
based upon the list of (egress_port, egress_rid) values configured by the
control plane"; a packet whose `egress_spec` is `DROP_PORT` "will be dropped
and not stored in the packet buffer"; `instance_type` says whether a packet
is new, resubmitted, recirculated, or cloned
([simple_switch.md](https://github.com/p4lang/behavioral-model/blob/main/docs/simple_switch.md)).
bmv2 "is not meant to be a production-grade software switch"; it exists "for
developing, testing and debugging" ([README](https://github.com/p4lang/behavioral-model/blob/main/README.md)).
The ingress decision as one value (a port, a multicast group, or drop) and
egress as per-copy rewriting is a clean split the plan keeps.

### Linux bridge

With VLAN filtering on, the bridge forwards "based on their destination MAC
address and VLAN tag (both must match)", and `IFLA_BR_VLAN_DEFAULT_PVID` is
"VLAN ID applied to untagged and priority-tagged incoming packets" with
default 1 ([Ethernet Bridging](https://docs.kernel.org/networking/bridge.html)).
This is the reference implementation of the PVID rule the plan takes from
the Q-BRIDGE-MIB, and the one emulator the edge could run without a vendor
image; the shadow record's reasons against emulation still hold.

### IEEE 802.1Q bridge YANG

The model is bridge, then component with a type of `c-vlan`, `s-vlan`,
`d-bridge`, or `edge-relay`, each with its own `filtering-database`
(`aging-time`, static and dynamic entries) and `bridge-vlan` (VID, name,
untagged and egress ports, `vid-to-fid-allocation`); the per-port
parameters are `pvid`, `acceptable-frame`, `enable-ingress-filtering`, and
`port-type`
([ieee802-dot1q-bridge.yang](https://www.ieee802.org/1/files/public/YANGs/ieee802-dot1q-bridge.yang)).
FlowSeer's `vswitch` is exactly one `c-vlan` component; the component and
the FID are the hooks for a later multi-domain device.

### OpenConfig lemming

A Go reference implementation "to clearly and authoritatively specify the
expected behavior of an OpenConfig-compliant device", with a `dataplane`
directory and gNMI, gNOI, gRIBI, P4RT, BGP, and IS-IS front ends, run under
KNE and tested with Ondatra ([openconfig/lemming](https://github.com/openconfig/lemming)).
Not adopted: it is a whole device with a control plane. It is the precedent
for a later step where the virtual switch answers FlowSeer's own collectors
over SNMP or gNMI as a fixture.

### SONiC-VS, Mininet, Ryu

SONiC-VS stores SAI state and lets "Linux networking stack entries" forward
([SONiC Testing Guide](https://github.com/sonic-net/SONiC/wiki/Testing-Guide)).
Mininet runs real Linux code. Ryu's `simple_switch_13` is the textbook
learning switch: flood on miss, learn the source on the ingress port
([Switching Hub](https://osrg.github.io/ryu-book/en/html/switching_hub.html)).
All three confirm the rules; none is a library FlowSeer could import.

## What FlowSeer adopts

1. **Trace as per-layer operations.** A trace is a list of steps, each
   with a layer, an operation from a fixed vocabulary (classify, filter,
   learn, lookup, replicate, rewrite, transmit, drop), and detail, plus the
   final outcome. Packet Tracer's operation list and Batfish's per-hop
   action are the same idea; a fixed vocabulary lets an L3 layer add steps
   without changing the trace type.
2. **Ingress decides, egress rewrites.** Ingress ends in one decision, a
   port, a flood set, or a drop, and egress applies the per-port tag form
   to each copy, as bmv2 does. Copies never re-enter ingress.
3. **Relay, table, tagger, ports as separate parts.** INET's composition
   is the plan's package split: `port`, `bridge` (relay and forwarding
   table), `frame` (tagger), `vswitch` (the switch).
4. **The relay reads a decoded frame and an ingress port, not bytes.** INET
   moved to indications for this reason; the codec is the only place bytes
   are read.
5. **Preloadable forwarding table.** INET's (VLAN, MAC, port) preload is
   the plan's seeds and the expected state's inheritance rule.
6. **Differential shape from Batfish.** A comparison carries the current
   and the expected trace side by side and a `Same` verdict. Bounded
   differential search (`src/common/netsim/search`) enumerates finite L2
   traffic domains and timed faults, pairing divergence detection with
   deterministic counterexample minimization and causal trace alignment.
7. **One `c-vlan` component now, components and FIDs later.** The 802.1Q
   YANG hierarchy is the shape a provider bridge or a multi-domain device
   grows into.
8. **No event scheduler in the single device.** The ns-3 and ns-x event
   model arrives with the fabric, where links and propagation exist; a
   device is a function of (time, port, frame).

## What FlowSeer does not adopt, and why

- Emulation of any kind, for the reasons the shadow record gives; the
  Linux bridge is the one candidate the edge could run, and it still could
  not be diffed against a typed expectation.
- Batfish as the engine: its L2 model does not apply VLANs.
- A symbolic header-space engine: unnecessary at L2 where the flow classes
  are enumerable, and premature before L3.
- gopacket or INET-style chunk APIs for the frame: the codec needs one
  header and a tag stack.

## Confidence and open risks

Confidence is high for the trace and differential shapes, which three
independent tools converge on, and for the INET composition. It is medium
for the "finite L2 differential" claim, which holds for a customer bridge
without protocol-based VLAN classification or VID translation and must be
re-examined when either is modelled. The HSA paper and the INET tagger
documentation were not fetchable from this host; both are cited by name and
by secondary pages only.

## Sources

- [Packet Tracer: PDU Information](https://cisco-packet-tracer-help.yue.zone/default/mode_simulation_PDUinfo.htm)
- [Packet Tracer: Simulation Mode](https://tutorials.ptnetacad.net/help/default/mode_simulation.htm)
- [Batfish: Differential Questions](https://batfish.readthedocs.io/en/stable/notebooks/differentialQuestions.html)
- [Batfish: Forwarding Change Validation](https://batfish.readthedocs.io/en/stable/notebooks/linked/introduction-to-forwarding-change-validation.html)
- [Batfish: Topology](https://batfish.readthedocs.io/en/latest/notebooks/topology.html)
- [Batfish issue #783](https://github.com/batfish/batfish/issues/783), [issue #6576](https://github.com/batfish/batfish/issues/6576)
- [Forward Networks: mathematical model](https://www.forwardnetworks.com/blog/resource/why-network-verification-requires-a-mathematical-model/)
- [ns-3 Conceptual Overview](https://www.nsnam.org/docs/tutorial/html/conceptual-overview.html), [ns-3 Manual](https://www.nsnam.org/docs/manual/singlehtml/index.html)
- [bytedance/ns-x](https://github.com/bytedance/ns-x)
- [INET: The Ethernet Model](https://inet.omnetpp.org/docs/users-guide/ch-ethernet.html), [INET 4.3.0 release](https://inet.omnetpp.org/2021-01-13-INET-4.3.0-released.html), [Ieee8021qTagEpdHeaderInserter](https://doc.omnetpp.org/inet/api-current/neddoc/inet.linklayer.ieee8021q.Ieee8021qTagEpdHeaderInserter.html), [Developing Models](https://inet.omnetpp.org/docs/developers-guide/ch-developing-models.html)
- [bmv2 simple_switch](https://github.com/p4lang/behavioral-model/blob/main/docs/simple_switch.md), [bmv2 README](https://github.com/p4lang/behavioral-model/blob/main/README.md)
- [Linux Ethernet Bridging](https://docs.kernel.org/networking/bridge.html)
- [ieee802-dot1q-bridge.yang](https://www.ieee802.org/1/files/public/YANGs/ieee802-dot1q-bridge.yang)
- [openconfig/lemming](https://github.com/openconfig/lemming)
- [SONiC Testing Guide](https://github.com/sonic-net/SONiC/wiki/Testing-Guide)
- [Ryu: Switching Hub](https://osrg.github.io/ryu-book/en/html/switching_hub.html)
- [Open-source network simulators survey](https://brianlinkletter.com/open-source-network-simulators/)
