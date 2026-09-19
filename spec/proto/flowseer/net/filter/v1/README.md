# Filter Primitives

The `flowseer.net.filter.v1` package defines packet filter rule sets, rules,
match predicates, actions, and the interface filter facet.

## Boundaries

Imports: net/addr, net/packet

Imported by: net/interface

Deliberately absent:

- Stateful connection tables and dynamic state synchronization.
- Interface refs and entity references. Rule sets are device-scoped named
  values, and interface bindings use device-local set names.
- NAT, packet rewrites, and address-list alias objects.

## Sources

The package's field and enum contracts cite:

- [RFC 8519](https://www.rfc-editor.org/rfc/rfc8519.html) for access control
  list (ACL) data models and rule match semantics.
