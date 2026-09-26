# QoS Primitives

The `flowseer.net.qos.v1` package defines a device's QoS configuration: the
trust mode, classifiers, and egress queues of an interface, and the
classifiers and policers they name.

## Boundaries

Imports: net/filter, net/key, net/measure, net/packet, net/switching

Imported by: nothing

Deliberately absent:

- DiffServ functional-block chains, TSN, scheduler policies, and
  forwarding-group definitions.
- WRED curves and RED thresholds. OpenConfig's WRED container is empty.
- Policer conform, exceed, and violate actions, and percentage-relative
  policers. A mapper that reports only a percentage leaves `policer` absent.
- CoS-to-queue and DSCP-to-queue maps, and QoS counters.
- A classifier `type` (IPv4, IPv6, MPLS, Ethernet). The `FilterMatch` fields a
  term sets already say which headers it reads, and OpenConfig's two copies
  of that enum disagree.
- Network instance key. `QosInterface` is scoped by its interface, and
  `Classifier` is a device-scoped named value like `FilterRuleSet`.

## Contents

- `trust_mode.proto`: `TrustMode`, the header field a port believes on
  ingress.
- `classifier.proto`: `Classifier` and its ordered `ClassifierTerm`s.
- `policer.proto`: `Policer`, a single-rate or two-rate policer.
- `queue_discipline.proto`: `QueueDiscipline`, drop tail, RED, or WRED.
- `queue.proto`: `Queue`, one egress queue.
- `qos_interface.proto`: `QosInterface`, the per-interface row.

`TrustMode` has five values rather than the three every source shares
(untrusted, CoS, DSCP). EdgeSwitch also trusts IP precedence, and Cisco SMB
has `cos-dscp`, which trusts the PCP for L2 traffic and the DSCP for L3
traffic. Mapping either to one of the three would report a trust the port
does not apply. The enum names the vendor mode (`COS`); fields that carry a
value name the header field (`set_pcp`), as the `vlan_pcp` rule does.

The interface binding is a row keyed by `interface_name`, not a facet on
`Interface`. OpenConfig lists QoS interfaces at device level by
`interface-id`, outside `/interfaces`, the same shape as the STP port row.
`QosInterface` names its classifiers by `Classifier.name`, as `FilterFacet`
names rule sets.

A classifier term reuses `flowseer.net.filter.v1.FilterMatch` for its
conditions, including its PCP, DSCP, MAC, and EtherType terms, so QoS and
packet filters share one match type as OpenConfig's QoS classifier and ACL
share `openconfig-packet-match`. A queue is identified by name, by number, or
both, because OpenConfig keys queues by name with an optional number and
EdgeSwitch numbers them. Its bandwidth is carried both as bit rates and as
basis points, because EdgeSwitch states bandwidth as a whole percentage.

## Sources

- OpenConfig QoS (`spec/yang/cisco/iosxe/2611/openconfig-qos-elements.yang`,
  `openconfig-qos-interfaces.yang`, `openconfig-qos-types.yang`) for
  classifiers, terms, remarking, policers, queues, and queue types.
- EdgeSwitch `EdgeSwitch-QOS-COS-MIB` and `EdgeSwitch-QOS-DIFFSERV-PRIVATE-MIB`
  (`spec/mib/ubiquiti/edgemax/`) for trust modes, numbered queues, and
  percentage bandwidth.
- D-Link `DLINKSW-QOS-MIB` (`spec/mib/dlink/`) and Cisco SMB
  `CISCOSBqosclimib.mib` (`spec/mib/cisco/smb/`) for trust modes.
- [RFC 2474](https://www.rfc-editor.org/rfc/rfc2474.html) for the DSCP and
  [IEEE 802.1Q](https://standards.ieee.org/ieee/802.1Q/10323/) for the PCP a
  term remarks.
