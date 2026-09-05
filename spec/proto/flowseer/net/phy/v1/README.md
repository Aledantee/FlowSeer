# Ethernet Physical-Link Primitives

The `flowseer.net.phy.v1` package defines ref-free Ethernet settings,
capabilities, and active physical-link facts. Requested speed, duplex,
auto-negotiation, FEC, and PoE intent are separate from negotiated or measured
values, so callers do not have to infer which meaning a source supplied.

`EthernetFacet` is embedded by value by physical interface kinds. The facet
carries the facts every transport shares: link rate and duplex,
auto-negotiation, FEC, the operational MAU type, the advertised and received
link modes, and the Ethernet-specific error counters. What only one medium can
have lives in the transport oneof: the copper arm carries Power over Ethernet
intent, delivery state, and the PSE port row behind it, while the fiber,
backplane, and other arms state the medium and carry nothing else. A source
that reports no medium leaves the oneof absent.

PoE rows keep the Power Ethernet MIB's own PSE group and port numbering as
their key, so a row can be carried before a source supplies the join to an
interface. `PseBudget` is the group-level power budget from the same MIB; it
carries the group index and no reference, so it stands on its own until an
entity embeds it. Device, tenant, lifecycle, provenance, observation time, and
collector health stay in the entity or envelope that carries the interface.

## Sources

The package's field and enum contracts cite:

- [IEEE 802.3](https://standards.ieee.org/ieee/802.3/10422/) for Ethernet
  physical-layer capabilities, speeds, duplex, FEC, and auto-negotiation.
- [RFC 3635](https://www.rfc-editor.org/rfc/rfc3635.html) for Ethernet-like
  interface (EtherLike-MIB) semantics and error counters.
- [RFC 4836](https://www.rfc-editor.org/rfc/rfc4836.html) for medium
  attachment unit (MAU-MIB) types and auto-negotiation link modes, with the
  [IANA-MAU-MIB](https://www.iana.org/assignments/ianamau-mib) as the
  registry of type identifiers and link-mode bit positions.
- [RFC 3621](https://www.rfc-editor.org/rfc/rfc3621.html) for Power over
  Ethernet (POWER-ETHERNET-MIB) semantics, per-port fault counters, and the
  group-level power budget.
- [SNIA SFF-8024](https://members.snia.org/document/dl/26423) for pluggable
  transceiver form-factor identifiers.
