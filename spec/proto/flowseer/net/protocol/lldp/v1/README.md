# LLDP

The `flowseer.net.protocol.lldp.v1` package holds what the Link Layer
Discovery Protocol owns: what a device announces about itself, how each of
its ports runs the protocol, and what its neighbors announced back.

Neighbors are a device-scoped table, not an attribute of an interface. A row
names the local interface it was heard on by that interface's device-local
name, so a walk of the neighbor table stands on its own and nothing has to be
grafted onto an interface to be stored. The port settings message is the same
shape from the other direction: one row per port, naming the interface.

Chassis and port identifiers keep the protocol's own encoding — a subtype
beside the raw octets. The subtype decides how the octets read, so a MAC
address stays six bytes and a locally assigned string stays whatever the
device chose; turning either into text is a display concern, and doing it in
the schema would lose the original. Management addresses follow the same
rule at the family level: LLDP prefixes an address with an IANA address
family number, so an address that is IPv4 or IPv6 lands in the typed arm and
anything else keeps its family number and octets.

Capabilities are a repeated open enum whose integers are the protocol's own
bit positions, so a capability this version does not name still arrives
intact.

The package covers what a device stores after receiving announcements.
Decoding an LLDP data unit off the wire is a different job and stays out
until a parser needs it.

The port-numbering columns LLDP maintains — the LLDP-MIB's local port number
and its management-address interface identifiers — do not appear here.
Resolving them to the interface name these messages use is the collecting
mapper's job.

Device identity, tenant, lifecycle, provenance, and observation time belong to
the entity or envelope that carries these values.

## Sources

The package's field and enum contracts cite:

- [IEEE Std 802.1AB](https://standards.ieee.org/ieee/802.1AB/7268/) for the
  identifier subtypes, the TLV types, and the system capability positions.
- [LLDP-MIB](https://www.ieee802.org/1/files/public/MIBs/LLDP-MIB-200505060000Z.txt)
  for the per-port administrative states and the shape of the local and
  remote tables.
- [IANA Address Family Numbers](https://www.iana.org/assignments/address-family-numbers)
  for the management address families.
