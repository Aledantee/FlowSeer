// Package snmpmap turns walked SNMP table rows into FlowSeer protobuf
// messages.
//
// A mapper is the seam between two generated worlds: the MIB bindings
// under generated/go/mib, which speak columns and rows, and the protobuf
// messages under generated/go/proto, which speak the network model. Both
// are consumed through their public API only.
//
// Each mapper here is a [collect.Mapper]: it declares the tables and
// scalars it reads and maps a [collect.Snapshot] the collector filled.
// The mapper never touches the session, so a host that runs several
// mappers against one device pays for each table once. For example, a
// caller with a context ctx and an open [snmp.Session] sess collects
// interfaces and neighbors in one cycle:
//
//	cycle, err := collect.New(snmpmap.InterfaceMapper, snmpmap.LLDPMapper(portNames)).Collect(ctx, sess)
//	for _, r := range cycle.Results {
//		if ifaces, ok := r.Output.([]*interfacev1.Interface); ok {
//			use(ifaces)
//		}
//	}
//	return err
//
// [Interfaces] and [LLDP] are the single-mapper shortcuts for a device
// whose tables are known to be there.
//
// Keep partial results together with their error: an optional table that
// failed still leaves the rows the required tables carried.
//
// # Presence
//
// The model draws a hard line between "the device reported zero" and "the
// device does not report this", so every mapper sets a field only when the
// walked row says the column was observed. Generated rows answer that
// through their Observed method; a zero-valued Go field on a row whose
// column never landed leaves the protobuf field absent.
//
// # Declines
//
// Mappers follow the SNMP library's decline-not-error semantics. A row
// that cannot be classified as precisely as the model would allow falls
// back to a less specific shape — an interface of an unmodeled type
// becomes the other arm carrying its raw ifType — rather than failing the
// collection or being dropped. Only a row that cannot produce a valid
// message at all surfaces as an error, and even then the rows around it
// are still returned.
//
// # Keys
//
// Mappers never decode an instance suffix themselves. A generated row
// carries its INDEX decoded into a typed Key, and tables join on those
// keys: ifXTable enriches ifTable through the shared ifmib.IfTableKey,
// and lldpRemManAddrTable's key begins with the lldpRemTable key it
// belongs to. A row whose suffix did not decode as the declared INDEX
// names nothing the model can hold; the collector drops it before the
// snapshot is built, without an error and without touching the rows
// around it. The one join the MIBs do not declare, an LLDP local port
// number to an interface name, stays with the caller of [LLDPMapper].
package snmpmap
