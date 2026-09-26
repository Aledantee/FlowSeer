# LLDP

The `flowseer.net.protocol.lldp.v1` package holds what the Link Layer
Discovery Protocol owns: what a device announces about itself, how each of
its ports runs the protocol, and what its neighbors announced back.

## Boundaries

Imports: net/addr, net/key, net/packet, net/phy, net/switching

Imported by: nothing

Deliberately absent:

- Device and interface entity references. Rows use device-local interface names.
- Raw LLDPDU wire decoders.
- The local side of the IEEE 802.3 and LLDP-MED extensions, the IEEE 802.1
  organizational TLVs (PVID, VLAN names), and the IEEE 802.3 link
  aggregation TLV.
- Observation time, provenance, and tenant context.

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

## Neighbor extensions

A neighbor's IEEE 802.3 TLVs land in `Ieee8023Extension` and its LLDP-MED
(ANSI/TIA-1057) TLVs in `MedExtension`, each absent when the neighbor sent
none of that organization's TLVs. Only the neighbor side exists. What a
port announces about itself in these TLVs is its own auto-negotiation, MAU
type, PoE state, and MTU, which the interface's Ethernet and PoE facets and
its MTU already hold; the neighbor's side is new information, and its
maximum frame size is what makes an MTU mismatch across a link comparable.
The IEEE 802.3 link aggregation TLV is left out because the V2 MIB moved it
to the IEEE 802.1 extension, which this package does not carry.

The extensions reuse the lower layers' types: the advertised link modes are
`net/phy` `MauLinkMode` values, whose integers are the
`ifMauAutoNegCapAdvertisedBits` positions the TLV carries (bit 0 is the most
significant bit of the first octet); the operational MAU type is `MauType`;
the PoE roles and priorities are `PoeRole` and `PoePriority`; a network
policy's priority takes the `vlan_pcp` rule and its DSCP is an `IpDscp`. A
policy's VLAN is not a `vlan_tag_vid`: the TLV admits 4095, "reserved for
implementation use", and rejecting a value a device really sent would fail
the whole neighbor row, so it carries its own `0..4095` bound.

A few values need converting, and a mapper does it once:

- The LLDP-MED location format follows the TLV's location data format
  octet, where civic address is 2. The MIB's `LocationSubtype` numbers the
  same formats one higher (civic address is 3), so a MIB mapper subtracts
  one.
- The extended power value is tenths of a watt on the wire and in the MIB
  (`0..1023`); `MedPower.power_nanowatts` holds it times 10⁸.
- The IEEE 802.3 power class is the IEEE class number, 0 to 4; the MIB's
  `lldpXdot3RemPowerClass` is that number plus one. A TLV class octet of 0
  leaves the field absent.
- The extended power type sits in bits 7-6 of the TLV's first octet.
  Wireshark's dissector masks it there and reads `0x03` as a PSE; tcpdump
  4.99 reads the same octet as a PD. The schema follows Wireshark.

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
- LLDP-EXT-DOT3-MIB (`spec/mib/ieee/LLDP-EXT-DOT3-MIB`) for the remote
  IEEE 802.3 extension tables, and LLDP-EXT-DOT3-V2-MIB
  (`spec/mib/ieee/LLDP-EXT-DOT3-V2-MIB-200906080000Z.mib`) for where the
  link aggregation TLV moved.
- LLDP-EXT-MED-MIB (`spec/mib/ieee/LLDP-EXT-MED-MIB`) and ANSI/TIA-1057 for
  the remote LLDP-MED tables.
- [RFC 3621](https://www.rfc-editor.org/rfc/rfc3621.html) for the power
  pairs and power classes, and
  [RFC 3636](https://www.rfc-editor.org/rfc/rfc3636.html) for the MAU
  auto-negotiation capability bits.
- The Wireshark LLDP dissector,
  [`epan/dissectors/packet-lldp.c` at commit 16c09df2](https://gitlab.com/wireshark/wireshark/-/blob/16c09df25f262e2e1b432521aba7eb6a3ea4afe3/epan/dissectors/packet-lldp.c),
  for the LLDP-MED application types, location formats, and power type
  bits.
- [IANA Address Family Numbers](https://www.iana.org/assignments/address-family-numbers)
  for the management address families.
