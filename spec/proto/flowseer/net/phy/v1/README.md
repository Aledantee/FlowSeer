# Physical Link Primitives

The `flowseer.net.phy.v1` package defines ref-free physical-link facets for
network interfaces. It records link characteristics and inline observations
such as PoE and pluggable-transceiver state without turning hardware components
into interface identities.

`EthernetFacet` is embedded by value by physical interface kinds. Device,
tenant, lifecycle, and observation metadata stay in the entity or envelope that
carries the interface.
