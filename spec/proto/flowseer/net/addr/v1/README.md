# Network Address Types

The `flowseer.net.addr.v1` package defines reusable address-domain primitives
for FlowSeer-owned schemas. Its messages are value types rather than entities,
facets, configuration, state, or events.

## Contents

The package contains:

- IEEE identifiers: EUI-48 and EUI-64 values, a tagged EUI/MAC address
  wrapper, and Organizationally Unique Identifiers (OUIs).
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
