# Link Aggregation Control Protocol (LACP)

The `flowseer.net.protocol.lacp.v1` package holds what the Link Aggregation
Control Protocol owns: an aggregator's protocol state and its member ports'
states, as device-scoped rows naming interfaces.

Rows name the local interface by device-local name, so walking aggregator and
member states stands on its own without embedding protocol facets into the
interface table.

Administrative settings and operational state share one message (`AggregatorState`
for aggregators, `PortState` for members) because LACP implementations configure
and report these attributes as a single unit, and intended and observed values
are observed together from the device.

Device identity, tenant, lifecycle, provenance, and observation time belong to
the entity or envelope that carries these values.

## Sources

The package's field and enum contracts cite:

- [IEEE Std 802.1AX](https://standards.ieee.org/ieee/802.1AX/6857/) for the Link
  Aggregation Control Protocol state machines, timers, and operational state bits.
- [RFC 3636 (IEEE8023-LAG-MIB)](https://datatracker.ietf.org/doc/html/rfc3636)
  for standard aggregator and port MIB objects and counters.
- [Open vSwitch vswitch.ovsschema](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)
  for Port columns `bond_mode`, `lacp`, `other_config:bond-updelay`,
  `other_config:bond-downdelay`, `other_config:bond-hash-basis`,
  `other_config:lacp-time`, `other_config:lacp-fallback-ab`,
  `other_config:lacp-system-id`, `other_config:lacp-system-priority`, and
  Interface columns `other_config:lacp-port-priority`,
  `other_config:lacp-aggregation-key`, and per-port error counters.
