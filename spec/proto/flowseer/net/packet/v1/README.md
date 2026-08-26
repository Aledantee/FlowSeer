# Packet Header Primitives

The `flowseer.net.packet.v1` package defines ref-free wire-header registries,
exact values, and small reusable match atoms. ACL, QoS, firewall, flow,
protocol, and telemetry schemas may import these values without making this
package aware of their policy or service semantics.

An exact value represents bits observed on the wire. A match value represents
a predicate: alternatives within a repeated field are ORed, while populated
fields are ANDed. Absence of a containing matcher means unconstrained; the
package does not encode wildcards with zero values or `ANY` sentinels.

The package deliberately does not define a universal packet matcher, flow
record, counters, direction, provenance, or observation time.
