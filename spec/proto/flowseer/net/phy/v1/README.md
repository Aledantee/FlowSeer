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
