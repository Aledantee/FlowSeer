# Device-Local Key Rules

The `flowseer.net.key.v1` package defines the predefined protovalidate rules
that validate device-local names: interface names, network-instance names,
and protocol-instance names. The package holds rules only, no messages — any
package that needs a name applies the rule to its own string field.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: api/device, model/access, model/alarm, model/capture, model/inventory, net/cellular, net/endpoint, net/flow, net/instance, net/interface, net/ip, net/multicast, net/nat, net/portaccess, net/protocol/bfd, net/protocol/bgp, net/protocol/cdp, net/protocol/dhcp, net/protocol/dns, net/protocol/isis, net/protocol/lacp, net/protocol/lldp, net/protocol/ntp, net/protocol/ospf, net/protocol/stp, net/protocol/vrrp, net/qos, net/routing, net/switching, net/system

Deliberately absent:

- A wrapper message per key. A predefined rule gives the field's rule an id
  without a second presence, so a wrapper buys nothing over the extension.
- Identity, tenant, and provenance context. A name is a device-local key;
  the context that scopes it lives with the entity or table that carries it.

## Contents

The four rules share the 1-to-255-character bound of SNMPv2-TC
`DisplayString`, which IF-MIB `ifName` uses:

- `interface_name` accepts what a device reports. Nothing stricter is
  safe: a device may spell its own interface beyond the character class
  below, and rejecting its row loses the reading.
- `shell_safe_interface_name` adds the character class
  `^[A-Za-z0-9][A-Za-z0-9 ./:_-]*$` for a name FlowSeer sends back to a
  device, because the adapter interpolates it into a shell command line.
- `network_instance_name` takes the same bounds as `interface_name`: the
  instance name is device-supplied free text subject to the same size.
- `protocol_instance_name` takes the same bounds: routing protocols can run
  multiple instances per network instance (OSPF processes, IS-IS tags),
  and the instance name is device-supplied free text.

A binary that validates a message carrying one of these rules blank-imports
the generated `flowseer/net/key/v1` package so the registry resolves the
extension (convention 4 of the
[network model structure record](../../../../../../docs/architecture/2026-08-20-network-model-structure-direction.md)),
which is also why a carrier brings the rule into scope with `import option`
rather than a plain import that would link the package unstated.

## Sources

- SNMPv2-TC `DisplayString` (`spec/mib/ietf/SNMPv2-TC`, SIZE 0..255) and
  IF-MIB `ifName` (`spec/mib/ietf/IF-MIB`) for the bounds.
