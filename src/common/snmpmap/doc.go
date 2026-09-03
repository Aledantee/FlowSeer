// Package snmpmap turns walked SNMP table rows into FlowSeer protobuf
// messages.
//
// A mapper is the seam between two generated worlds: the MIB bindings
// under generated/go/mib, which speak columns and rows, and the protobuf
// messages under generated/go/proto, which speak the network model. Both
// are consumed through their public API only.
//
// Keep partial results together with their error. For example, a caller
// with a context ctx and an open [snmp.Session] sess can collect names even
// when an optional table fails:
//
//	ifaces, err := snmpmap.Interfaces(ctx, sess)
//	names := make([]string, 0, len(ifaces))
//	for _, iface := range ifaces {
//		names = append(names, iface.GetName())
//	}
//	return names, err
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
package snmpmap
