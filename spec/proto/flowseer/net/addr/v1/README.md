# Network Address Types

The `flowseer.net.addr.v1` package defines reusable address-domain primitives
for FlowSeer-owned schemas. Its messages are value types rather than entities,
facets, configuration, state, or events.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: api/edge, model/inventory, net/capture, net/interface, net/ip, net/protocol/lacp, net/protocol/lldp, net/protocol/stp, net/switching, store/device

Deliberately absent:

- Identity, tenant, observation time, and lifecycle. Primitives here are pure values with no entity context.
- Interface scope and routing domains. Interface-specific address bindings live in `net/ip/v1`.
- A MAC address message distinct from EUI-48. An Ethernet MAC address is an EUI-48.

## Contents

The package contains:

- IEEE identifiers: `Eui48Address` and `Eui64Address` value types, the tagged
  `EuiAddress` wrapper whose oneof arm names the width, and Organizationally
  Unique Identifiers (OUIs). There is no separate MAC address message; an
  Ethernet MAC address is an EUI-48.
- IP address-family registry values and FlowSeer address classifications.
- IP classification: FlowSeer's coarse address scopes.
- IP values: IPv4 and IPv6 addresses, canonical masked network prefixes,
  inclusive address ranges, and address lifetimes.

Each concrete address variant carries its own representation and validation.
Tagged wrappers make the variant explicit, so consumers do not have to infer
an address family or EUI width from a byte count. Optionality belongs to the
field that contains one of these values; a present value must satisfy the
package's validation rules.

## Why a separate package

Address primitives are shared vocabulary and are not strictly applicable to
any one FlowSeer layer. Inventory, discovery, configuration, observed state,
events, and telemetry can all refer to the same address without owning its
wire representation or validation contract.

Keeping these types in `net.addr` gives those domains one dependency-neutral
contract and avoids redefining subtly different address messages in each
layer. The package is deliberately narrow: interface scope, provenance,
tenancy, and other layer-specific context stay in the schema that owns that
context.

Packet-header registries such as DSCP, ECN, and IP protocol numbers live in
`flowseer.net.packet.v1`; they classify traffic rather than addresses.

## Sources

The package's field and enum contracts cite:

- The [IEEE Registration Authority](https://standards.ieee.org/products-programs/regauth/)
  for EUI-48, EUI-64, and OUI identifier formats.
- The IANA [Address Family Numbers](https://www.iana.org/assignments/address-family-numbers)
  registry for address-family values.
- [RFC 4861, section 4.6.2](https://www.rfc-editor.org/rfc/rfc4861.html#section-4.6.2)
  and [RFC 8415, section 7.7](https://www.rfc-editor.org/rfc/rfc8415.html#section-7.7)
  for address-lifetime encoding, including the infinite-lifetime sentinel.
