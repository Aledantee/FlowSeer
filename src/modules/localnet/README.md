# localnet

The SNMP side of the local-network integration kind: the collector that runs
one collection cycle against a device, and the mappers that turn what the
device answers into FlowSeer's network model. It is the first module admitted
under `src/modules/`; the admission rule is in [that README](../README.md).

| Package   | What it does                                                          |
| --------- | --------------------------------------------------------------------- |
| `collect` | the mapper contract, detection, and the per-device collection cycle   |
| `snmpmap` | the mappers: IF-MIB interfaces, LLDP-MIB neighbors and ports, and the physical-layer and transceiver MIBs; the physical mapper's EtherLike, MAU, and Power-Ethernet tables share a cycle through `collect.Spec` the way the interface mapper's do, and only its vendor transceiver-diagnostics walk still reads its own session directly |
| `access`  | the device-access capabilities: typed SNMP and SSH reads and mutations for one device family per capability, reported as `flowseer.model.access.v1` observations |

The module is a library today. Both hosts the direction record names will
assemble it: the edge agent that polls devices inside a site's network, and the
central process that reaches devices directly. The service-runtime wiring, a
`service.Module` leaf or branch with its configuration, gates, and telemetry,
is written together with the first host, because its shape follows the host's
supervision tree and not the other way round.

## One cycle

A collector holds a fixed set of mappers. Each cycle it reads sysObjectID once
and resolves it through the generated identity table, decides which mappers
apply, walks every table those mappers read exactly once with the union of
their columns, and hands the rows to each mapper as an immutable snapshot:

```go
c := collect.New(snmpmap.InterfaceMapper, snmpmap.LLDPMapper(nil))
cycle, err := c.Collect(ctx, sess)
for _, r := range cycle.Results {
    if ifaces, ok := r.Output.([]*interfacev1.Interface); ok {
        store(ifaces, r.Err)
    }
}
```

The returned error joins the identity read, the presence probes, and every
mapper's error; the results still hold whatever each mapper produced. A
device whose sysObjectID no vendored MIB names keeps the raw OID in
`cycle.Identity` so a caller can report it.

## What a mapper declares

A mapper is pure. It returns a `collect.Spec` naming the tables it requires,
the tables that only enrich its output, the scalars it reads, and, for a
vendor-specific mapper, the sysObjectID prefixes it is limited to. Its `Map`
method reads rows from the snapshot and never touches the session. That is
what lets two mappers share one walk of `ifTable` and what keeps a mapper
testable from a handful of rows:

```go
func (interfaceMapper) Spec() collect.Spec {
    return collect.Spec{
        Name:     "interfaces",
        Required: []collect.TableRead{ifTableRead},
        Optional: []collect.TableRead{ifXTableRead, ifStackTableRead},
    }
}
```

A table read is built from the generated bindings with `collect.NewTable`,
which pairs a table's descriptor and walker with the columns the mapper needs.

## Detection

A mapper applies to a device on this cycle when every required table answers
a presence probe and, when the spec names prefixes, the device's sysObjectID
has one of them as a prefix. Detection never reads sysDescr: its text is
free-form and changes between firmware releases of one model, so a match on
it is a guess dressed up as a fact.

Presence is an instance probe, not a support probe. A table the agent
implements but currently holds no rows in reads as absent, so a mapper whose
required table is empty is skipped this cycle and picked up again when rows
appear.

## What a failure costs

A walk that fails is recorded against that table alone. The other tables of
the cycle are still walked and every applicable mapper still runs. The mapper
decides what the failure means: a required table's failure declines its whole
output, an optional table's failure degrades it, and the rows a failed walk
delivered before it stopped are in the snapshot beside the error. A row whose
instance suffix did not decode as the declared INDEX is dropped by the table
read, since it names nothing the model can hold; the rows around it are
unaffected. The reasoning behind these choices is captured in
[the key-resolution learning](../../../docs/solutions/architecture-patterns/key-resolution-degrades-and-reports-never-fails.md)
and [the decode blast-radius learning](../../../docs/solutions/architecture-patterns/decode-failure-blast-radius-in-generated-walks.md).

## Testing

`collect` is tested against a scripted session: detection with present and
absent required tables and matching and missing prefixes, a shared table
walked once when two mappers need it, and a failed optional table leaving
the other mapper's output intact. The mappers keep their original tests as
characterization coverage; only how rows reach them changed when they moved
here from `src/common/`.
