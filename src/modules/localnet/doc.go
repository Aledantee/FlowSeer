// Package localnet is the SNMP side of the local-network integration kind:
// the collector and the mappers that turn what a device answers over SNMP
// into FlowSeer's network model.
//
// The module is a library today. Its packages are assembled by a host, and
// the same packages serve both hosts the direction record names: the edge
// agent that polls devices inside a site's network, and the central process
// that reaches devices directly. The service-runtime wiring (a
// [service.Module] leaf or branch, its configuration, gates, and telemetry)
// is added together with the first host, because the wiring's shape follows
// the host's supervision tree rather than the other way round.
//
// [collect] holds the mapper contract and the per-device collection cycle;
// [snmpmap] holds the mappers themselves.
//
// [service.Module]: go.aledante.io/FlowSeer/src/common/service#Module
// [collect]: go.aledante.io/FlowSeer/src/modules/localnet/collect
// [snmpmap]: go.aledante.io/FlowSeer/src/modules/localnet/snmpmap
package localnet
