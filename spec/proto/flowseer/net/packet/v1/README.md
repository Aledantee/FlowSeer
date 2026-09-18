# Packet Header Primitives

The `flowseer.net.packet.v1` package defines ref-free wire-header registries,
exact values, and small reusable match atoms. ACL, QoS, firewall, flow,
protocol, and telemetry schemas may import these values without making this
package aware of their policy or service semantics.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: net/capture, net/switching

Deliberately absent:

- Universal packet matchers, flow records, counters, and packet directions.
- Observation time, provenance, and tenant context.

An exact value represents bits observed on the wire. A match value represents
a predicate: alternatives within a repeated field are ORed, while populated
fields are ANDed. Absence of a containing matcher means unconstrained; the
package does not encode wildcards with zero values or `ANY` sentinels.

The package deliberately does not define a universal packet matcher, flow
record, counters, direction, provenance, or observation time.

## Sources

The package's field and enum contracts cite:

- IANA registries:
  [Differentiated Services Field Codepoints](https://www.iana.org/assignments/dscp-registry),
  [ICMP Type Numbers](https://www.iana.org/assignments/icmp-parameters),
  [ICMPv6 Parameters](https://www.iana.org/assignments/icmpv6-parameters),
  [IEEE 802 Numbers](https://www.iana.org/assignments/ieee-802-numbers), and
  [Assigned Internet Protocol Numbers](https://www.iana.org/assignments/protocol-numbers).
- [RFC 2474](https://www.rfc-editor.org/rfc/rfc2474.html),
  [RFC 2597](https://www.rfc-editor.org/rfc/rfc2597.html),
  [RFC 3246](https://www.rfc-editor.org/rfc/rfc3246.html),
  [RFC 5865](https://www.rfc-editor.org/rfc/rfc5865.html),
  [RFC 8622](https://www.rfc-editor.org/rfc/rfc8622.html), and
  [RFC 9956](https://www.rfc-editor.org/rfc/rfc9956.html) for the DiffServ
  field and named per-hop behaviors.
- [RFC 3168, section 5](https://www.rfc-editor.org/rfc/rfc3168.html#section-5)
  for ECN codepoints.
- [RFC 9293, section 3.1](https://www.rfc-editor.org/rfc/rfc9293.html#section-3.1)
  for TCP header flags.
- [RFC 5332](https://www.rfc-editor.org/rfc/rfc5332.html) for the MPLS
  EtherType meanings.
