# Ethernet Physical-Link Primitives

The `flowseer.net.phy.v1` package defines ref-free Ethernet settings,
capabilities, and active physical-link facts. Requested speed, duplex,
auto-negotiation, FEC, and PoE intent are separate from negotiated or measured
values, so callers do not have to infer which meaning a source supplied.

`EthernetFacet` is embedded by value by physical interface kinds. Its
transceiver summary is intentionally shallow; lane-level optics and hardware
identity belong to a future component model. Device, tenant, lifecycle,
provenance, observation time, and collector health stay in the entity or
envelope that carries the interface.

## Sources

The package's field and enum contracts cite:

- [IEEE 802.3](https://standards.ieee.org/ieee/802.3/10422/) for Ethernet
  physical-layer capabilities, speeds, duplex, FEC, and auto-negotiation.
- [RFC 3635](https://www.rfc-editor.org/rfc/rfc3635.html) for Ethernet-like
  interface (EtherLike-MIB) semantics.
- [RFC 3621](https://www.rfc-editor.org/rfc/rfc3621.html) for Power over
  Ethernet (POWER-ETHERNET-MIB) semantics.
- [SNIA SFF-8024](https://members.snia.org/document/dl/26423) for pluggable
  transceiver form-factor identifiers.
