# Filter Primitives

The `flowseer.net.filter.v1` package defines packet filter rule sets, rules,
match predicates, actions, and the interface filter facet.

## Boundaries

Imports: net/addr, net/packet, net/switching

Imported by: net/interface

Deliberately absent:

- Stateful connection tables and dynamic state synchronization.
- Interface refs and entity references. Rule sets are device-scoped named
  values, and interface bindings use device-local set names.
- NAT, packet rewrites, and address-list alias objects.

`FilterMatch` is the one match type for packet criteria. Its L2 terms are the
Ethernet header match of RFC 8519 §4.2 and OpenConfig's
`ethernet-header-config`: source and destination MAC, each with an optional
mask (`MacMatch`), and the EtherType. It also carries a PCP list and a DSCP
list, the header fields QoS classification reads. A non-empty list matches a
packet whose value is any member, as the prefix and port lists do.

`MacMatch` holds an `Eui48Address` rather than a `MacAddress`:
`yang:mac-address` is six octets, and an EUI-64 mask on an Ethernet header has
no meaning. Address bits outside the mask are ignored rather than rejected,
because neither source requires them clear, and a device reporting them would
otherwise fail its whole rule set.

The network simulator evaluates only the IP-layer terms. It leaves out a rule
set any of whose rules carries an EtherType, MAC, PCP, or DSCP term, and
reports it as an unsupported facet, because translating the rule without them
would widen it and dropping the rule would let a later rule decide.

## Sources

The package's field and enum contracts cite:

- [RFC 8519](https://www.rfc-editor.org/rfc/rfc8519.html) for access control
  list (ACL) data models and rule match semantics, and its §4.2
  (`ietf-packet-fields`) for the Ethernet header and DSCP match fields.
- OpenConfig `openconfig-packet-match`
  (`spec/yang/openconfig/openconfig-packet-match.yang`) for the
  `ethernet-header-config` MAC, mask, and EtherType leaves and the IP header
  `dscp-set`.
