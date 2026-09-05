package snmpmap

import (
	"context"
	"errors"
	"strconv"

	"go.aledante.io/FlowSeer/generated/go/mib/ianaiftype"
	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/collect"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

var (
	// ErrCodeInterfaceUnnamed identifies an ifTable row that reported
	// neither ifName nor ifDescr. An interface's name is its identity, so
	// such a row yields no message.
	ErrCodeInterfaceUnnamed = errs.NewCode("snmpmap/interface-unnamed")
	// ErrCodeInterfaceUntyped identifies an ifTable row whose ifType never
	// landed. Without a type the mapper cannot pick a kind, and every
	// Interface carries one.
	ErrCodeInterfaceUntyped = errs.NewCode("snmpmap/interface-untyped")
	// ErrCodeInterfaceWalk identifies a failure of one of the IF-MIB table
	// walks. A failed ifTable walk yields no interfaces; a failed ifXTable
	// or ifStackTable walk yields the interfaces ifTable carried, with
	// fewer facts on them.
	ErrCodeInterfaceWalk = errs.NewCode("snmpmap/interface-walk")
)

// ifTableColumns are the ifTable columns [Interfaces] requests. The
// 32-bit counters are among them because they are the fallback source for
// a device that implements no ifXTable.
var ifTableColumns = []snmp.AnyColumn{
	ifmib.IfDescr,
	ifmib.IfType,
	ifmib.IfMtu,
	ifmib.IfPhysAddress,
	ifmib.IfAdminStatus,
	ifmib.IfOperStatus,
	ifmib.IfInOctets,
	ifmib.IfInUcastPkts,
	ifmib.IfInDiscards,
	ifmib.IfInErrors,
	ifmib.IfOutOctets,
	ifmib.IfOutUcastPkts,
	ifmib.IfOutDiscards,
	ifmib.IfOutErrors,
}

// ifXTableColumns are the ifXTable columns [Interfaces] requests.
var ifXTableColumns = []snmp.AnyColumn{
	ifmib.IfName,
	ifmib.IfAlias,
	ifmib.IfInMulticastPkts,
	ifmib.IfInBroadcastPkts,
	ifmib.IfOutMulticastPkts,
	ifmib.IfOutBroadcastPkts,
	ifmib.IfHCInOctets,
	ifmib.IfHCInUcastPkts,
	ifmib.IfHCInMulticastPkts,
	ifmib.IfHCInBroadcastPkts,
	ifmib.IfHCOutOctets,
	ifmib.IfHCOutUcastPkts,
	ifmib.IfHCOutMulticastPkts,
	ifmib.IfHCOutBroadcastPkts,
}

// IfRow is one walked ifTable row and the key it was walked under. The
// key is stated beside the row rather than read from it because a row
// assembled by hand carries no walk-owned key validity; the walk fills
// both from the same decoded suffix.
// Concurrent reads are safe; callers must synchronize mutations of the
// row or its referenced data.
type IfRow struct {
	Key ifmib.IfTableKey
	Row ifmib.IfTableRow
}

// IfXRow is one walked ifXTable row and the ifTable key it augments.
// Concurrent reads are safe; callers must synchronize mutations of the
// row or its referenced data.
type IfXRow struct {
	Key ifmib.IfTableKey
	Row ifmib.IfXTableRow
}

// IfStackRow is one active layering relationship of ifStackTable: Higher
// runs over Lower, both named by their ifTable key. Concurrent reads are
// safe; callers must synchronize mutations.
type IfStackRow struct {
	Higher ifmib.IfTableKey
	Lower  ifmib.IfTableKey
}

// IfMIBRows are the walked IF-MIB rows one interface set is built from.
// ifXTable and ifStackTable are optional: a device that implements
// neither still yields interfaces, with fewer facts on them.
// The zero value maps to an empty interface set. Concurrent reads are
// safe; callers must synchronize mutations of the slices or their rows.
type IfMIBRows struct {
	// If are the ifTable rows, in the order the mapped interfaces appear.
	If []IfRow
	// IfX are the ifXTable rows, joined to If by key.
	IfX []IfXRow
	// Stack are the active layering relationships of ifStackTable.
	Stack []IfStackRow
}

// The IF-MIB reads the interface mapper declares. ifStackTable is asked
// only for its status column: a stored relationship may be inactive, and
// only active rows describe the layering currently in use.
var (
	ifTableRead      = collect.NewTable[ifmib.IfTableRow](ifmib.IfTable.Descriptor(), ifmib.IfTable.Walk, ifTableColumns...)
	ifXTableRead     = collect.NewTable[ifmib.IfXTableRow](ifmib.IfXTable.Descriptor(), ifmib.IfXTable.Walk, ifXTableColumns...)
	ifStackTableRead = collect.NewTable[ifmib.IfStackTableRow](ifmib.IfStackTable.Descriptor(), ifmib.IfStackTable.Walk, ifmib.IfStackStatus)
)

// InterfaceMapper maps IF-MIB to one Interface per ifTable row. It
// requires ifTable and reads ifXTable and ifStackTable when they are
// there; it names no sysObjectID prefix because IF-MIB is standard. Its
// Map output is []*interfacev1.Interface, as [InterfacesFromSnapshot]
// returns it.
var InterfaceMapper collect.Mapper = interfaceMapper{}

type interfaceMapper struct{}

func (interfaceMapper) Spec() collect.Spec {
	return collect.Spec{
		Name:     "interfaces",
		Required: []collect.TableRead{ifTableRead},
		Optional: []collect.TableRead{ifXTableRead, ifStackTableRead},
	}
}

func (interfaceMapper) Map(snap *collect.Snapshot) (any, error) {
	return InterfacesFromSnapshot(snap)
}

// Interfaces reads IF-MIB on sess through [collect.Read] and maps it with
// [InterfacesFromSnapshot]. It does no detection, so a device that
// implements no ifTable reports a failed walk rather than being skipped.
func Interfaces(ctx context.Context, sess snmp.Session) ([]*interfacev1.Interface, error) {
	return InterfacesFromSnapshot(collect.Read(ctx, sess, InterfaceMapper))
}

// InterfacesFromSnapshot returns one Interface per ifTable row of snap,
// in walk order. A device that walked no ifXTable or ifStackTable yields
// interfaces built from ifTable alone.
//
// A failed ifTable walk returns no interfaces and an error carrying
// [ErrCodeInterfaceWalk]. A failed ifXTable or ifStackTable walk returns
// the interfaces ifTable carried and that same error beside them: those
// tables only enrich rows that already stand on their own, so losing one
// degrades the answer rather than voiding it, and the rows the failed walk
// delivered before it stopped still enrich. A row that cannot produce a
// valid message returns alongside the rows that could, as described on
// [InterfacesFromRows].
func InterfacesFromSnapshot(snap *collect.Snapshot) ([]*interfacev1.Interface, error) {
	if err := ifTableRead.Err(snap); err != nil {
		return nil, errs.From(err).Code(ErrCodeInterfaceWalk).Msg("walk ifTable")
	}

	var (
		rows     IfMIBRows
		walkErrs []error
	)

	for _, row := range ifTableRead.Rows(snap) {
		rows.If = append(rows.If, IfRow{Key: row.Key, Row: row})
	}

	for _, row := range ifXTableRead.Rows(snap) {
		rows.IfX = append(rows.IfX, IfXRow{Key: row.Key, Row: row})
	}

	if err := ifXTableRead.Err(snap); err != nil {
		walkErrs = append(walkErrs, errs.From(err).Code(ErrCodeInterfaceWalk).Msg("walk ifXTable"))
	}

	for _, row := range ifStackTableRead.Rows(snap) {
		if !row.Observed(ifmib.IfStackStatus) || row.IfStackStatus != snmp.RowStatusActive {
			continue
		}

		rows.Stack = append(rows.Stack, IfStackRow{
			Higher: ifmib.IfTableKey{IfIndex: ifmib.InterfaceIndex(row.Key.IfStackHigherLayer)},
			Lower:  ifmib.IfTableKey{IfIndex: ifmib.InterfaceIndex(row.Key.IfStackLowerLayer)},
		})
	}

	if err := ifStackTableRead.Err(snap); err != nil {
		walkErrs = append(walkErrs, errs.From(err).Code(ErrCodeInterfaceWalk).Msg("walk ifStackTable"))
	}

	ifaces, rowErr := InterfacesFromRows(rows)

	return ifaces, errors.Join(errors.Join(walkErrs...), rowErr)
}

// InterfacesFromRows maps already-walked rows, so a caller that walks
// IF-MIB for reasons of its own does not walk it twice.
//
// A row whose ifType names no dedicated kind is not dropped: it becomes
// the other arm carrying the raw ifType. A row that cannot produce a
// valid message at all — one with no name, or with no type — is reported
// through the joined error while every other row is still returned, so a
// caller that ignores the error still sees a truthful, if incomplete,
// interface set. It does not modify rows, but returned messages may share
// byte slices with them; callers must synchronize mutations of shared data.
func InterfacesFromRows(rows IfMIBRows) ([]*interfacev1.Interface, error) {
	byKey := make(map[ifmib.IfTableKey]ifmib.IfXTableRow, len(rows.IfX))

	for _, x := range rows.IfX {
		byKey[x.Key] = x.Row
	}

	names := make(map[ifmib.IfTableKey]string, len(rows.If))
	types := make(map[ifmib.IfTableKey]ianaiftype.IANAifType, len(rows.If))

	for _, r := range rows.If {
		if name, ok := interfaceName(r.Row, byKey[r.Key]); ok {
			names[r.Key] = name
		}

		if r.Row.Observed(ifmib.IfType) {
			types[r.Key] = r.Row.IfType
		}
	}

	stack := readStack(rows.Stack)

	ifaces := make([]*interfacev1.Interface, 0, len(rows.If))

	var rowErrs []error

	for _, r := range rows.If {
		iface, err := mapInterface(r, byKey[r.Key], names, types, stack)
		if err != nil {
			rowErrs = append(rowErrs, err)

			continue
		}

		ifaces = append(ifaces, iface)
	}

	return ifaces, errors.Join(rowErrs...)
}

// ifStack is the layering ifStackTable declares, read in both
// directions: what an interface runs over, and what runs over it.
type ifStack struct {
	lower map[ifmib.IfTableKey][]ifmib.IfTableKey
	upper map[ifmib.IfTableKey][]ifmib.IfTableKey
}

// readStack indexes the stack rows. The zero ifIndex is ifStackTable's
// marker for "nothing above" and "nothing below", not an interface.
func readStack(rows []IfStackRow) ifStack {
	s := ifStack{
		lower: make(map[ifmib.IfTableKey][]ifmib.IfTableKey),
		upper: make(map[ifmib.IfTableKey][]ifmib.IfTableKey),
	}

	for _, r := range rows {
		if r.Higher.IfIndex == 0 || r.Lower.IfIndex == 0 {
			continue
		}

		s.lower[r.Higher] = append(s.lower[r.Higher], r.Lower)
		s.upper[r.Lower] = append(s.upper[r.Lower], r.Higher)
	}

	return s
}

// mapInterface builds one Interface from its ifTable row and the
// ifXTable row of the same key, which is the zero row when the device
// implements no ifXTable.
func mapInterface(
	ifRow IfRow,
	x ifmib.IfXTableRow,
	names map[ifmib.IfTableKey]string,
	types map[ifmib.IfTableKey]ianaiftype.IANAifType,
	stack ifStack,
) (*interfacev1.Interface, error) {
	r, key := ifRow.Row, ifRow.Key

	name, ok := names[key]
	if !ok {
		return nil, errs.New().
			Code(ErrCodeInterfaceUnnamed).
			Attr("if_index", key.IfIndex).
			Msgf("ifTable row %d reported no name", key.IfIndex)
	}

	ifType, ok := types[key]
	if !ok || ifType < 1 {
		return nil, errs.New().
			Code(ErrCodeInterfaceUntyped).
			Attr("if_index", key.IfIndex).
			Attr("if_name", name).
			Msgf("ifTable row %d reported no type", key.IfIndex)
	}

	iface := &interfacev1.Interface{}
	iface.SetName(name)

	// The model carries the index arc as the agent spelled it; the key
	// type narrows it to the MIB's range, so the conversion restores the
	// arc's bits.
	if key.IfIndex != 0 {
		iface.SetIfIndex(uint32(key.IfIndex))
	}

	if r.Observed(ifmib.IfAdminStatus) {
		iface.SetAdminStatus(adminStatus(r.IfAdminStatus))
	}

	if r.Observed(ifmib.IfOperStatus) {
		iface.SetOperStatus(operStatus(r.IfOperStatus))
	}

	// A negative ifMtu is outside the MIB's range and says nothing about
	// the interface, so it reads as unreported.
	if r.Observed(ifmib.IfMtu) && r.IfMtu >= 0 {
		iface.SetMtu(uint32(r.IfMtu))
	}

	if r.Observed(ifmib.IfPhysAddress) {
		if mac, ok := euiAddress(r.IfPhysAddress); ok {
			iface.SetMac(mac)
		}
	}

	if x.Observed(ifmib.IfAlias) {
		iface.SetDescription(x.IfAlias)
	}

	if counters := interfaceCounters(r, x); counters != nil {
		iface.SetCounters(counters)
	}

	setKind(iface, ifType, key, names, types, stack)

	return iface, nil
}

// setKind picks the interface's kind arm. The dispatch is on ifType,
// except that a row of an unmodeled type stacked over exactly one known
// interface is a subinterface of it — which is how a device that reports
// its VLAN subinterfaces as propVirtual is read correctly.
func setKind(
	iface *interfacev1.Interface,
	ifType ianaiftype.IANAifType,
	key ifmib.IfTableKey,
	names map[ifmib.IfTableKey]string,
	types map[ifmib.IfTableKey]ianaiftype.IANAifType,
	stack ifStack,
) {
	switch ifType {
	case ianaiftype.IANAifTypeEthernetCsmacd:
		physical := &interfacev1.PhysicalInterface{}
		if lag, ok := lagParent(key, names, types, stack); ok {
			physical.SetLagParent(lag)
		}

		iface.SetPhysical(physical)

	case ianaiftype.IANAifTypeIeee8023adLag:
		iface.SetLag(&interfacev1.LagInterface{})

	case ianaiftype.IANAifTypeL2vlan:
		// The SVI's VLAN lives in its name and nowhere else in IF-MIB. A
		// name that carries no identifier declines to the other arm
		// rather than inventing one the vlan arm requires.
		vlanID, ok := vlanIDFromName(iface.GetName())
		if !ok {
			setOther(iface, ifType)

			return
		}

		vlan := &interfacev1.VlanInterface{}
		vlan.SetVlanId(vlanID)
		iface.SetVlan(vlan)
		// An SVI is the routed presence of a VLAN, so the facet is
		// present; the addresses on it come from an IP-MIB mapping.
		iface.SetIp(&ipv1.IpFacet{})

	case ianaiftype.IANAifTypeSoftwareLoopback:
		iface.SetLoopback(&interfacev1.LoopbackInterface{})

	case ianaiftype.IANAifTypeTunnel:
		iface.SetTunnel(&interfacev1.TunnelInterface{})

	default:
		if parent, ok := soleParent(key, names, stack); ok {
			sub := &interfacev1.Subinterface{}
			sub.SetParent(parent)
			iface.SetSub(sub)

			return
		}

		setOther(iface, ifType)
	}
}

// setOther puts the interface in the arm that drops nothing: the raw
// ifType, preserved as reported.
func setOther(iface *interfacev1.Interface, ifType ianaiftype.IANAifType) {
	other := &interfacev1.OtherInterface{}
	other.SetIfType(uint32(ifType))
	iface.SetOther(other)
}

// lagParent returns the name of the aggregate this port is a member of.
func lagParent(
	key ifmib.IfTableKey,
	names map[ifmib.IfTableKey]string,
	types map[ifmib.IfTableKey]ianaiftype.IANAifType,
	stack ifStack,
) (string, bool) {
	for _, higher := range stack.upper[key] {
		if types[higher] != ianaiftype.IANAifTypeIeee8023adLag {
			continue
		}

		if name, ok := names[higher]; ok {
			return name, true
		}
	}

	return "", false
}

// soleParent returns the name of the one interface this row runs over,
// and false when it runs over none or several — several is a stack this
// mapping cannot read as a parent.
func soleParent(key ifmib.IfTableKey, names map[ifmib.IfTableKey]string, stack ifStack) (string, bool) {
	if len(stack.lower[key]) != 1 {
		return "", false
	}

	name, ok := names[stack.lower[key][0]]

	return name, ok
}

// interfaceName prefers ifName, the device's own short name, over
// ifDescr, which on many agents is a type description rather than a name.
func interfaceName(r ifmib.IfTableRow, x ifmib.IfXTableRow) (string, bool) {
	if x.Observed(ifmib.IfName) && x.IfName != "" {
		return x.IfName, true
	}

	if r.Observed(ifmib.IfDescr) && r.IfDescr != "" {
		return r.IfDescr, true
	}

	return "", false
}

// vlanIDFromName reads the VLAN identifier a device spells into an SVI's
// name, as in "Vlan20". Digits must follow a label, so a name that is
// nothing but a number is not read as an identifier.
func vlanIDFromName(name string) (uint32, bool) {
	digits := len(name)
	for digits > 0 && name[digits-1] >= '0' && name[digits-1] <= '9' {
		digits--
	}

	if digits == 0 || digits == len(name) {
		return 0, false
	}

	id, err := strconv.ParseUint(name[digits:], 10, 32)
	if err != nil || id < 1 || id > 4094 {
		return 0, false
	}

	return uint32(id), true
}

// euiAddress maps ifPhysAddress to the EUI variant its width names. An
// interface with no hardware address reports an empty string, and a
// width that is neither EUI-48 nor EUI-64 is an address this model has
// no arm for; both leave the address absent.
func euiAddress(octets []byte) (*addrv1.EuiAddress, bool) {
	addr := &addrv1.EuiAddress{}

	switch len(octets) {
	case 6:
		eui48 := &addrv1.Eui48Address{}
		eui48.SetOctets(octets)
		addr.SetEui48(eui48)

	case 8:
		eui64 := &addrv1.Eui64Address{}
		eui64.SetOctets(octets)
		addr.SetEui64(eui64)

	default:
		return nil, false
	}

	return addr, true
}

func adminStatus(v ifmib.IfAdminStatusValue) interfacev1.AdminStatus {
	switch v {
	case ifmib.IfAdminStatusValueUp:
		return interfacev1.AdminStatus_ADMIN_STATUS_UP
	case ifmib.IfAdminStatusValueDown:
		return interfacev1.AdminStatus_ADMIN_STATUS_DOWN
	case ifmib.IfAdminStatusValueTesting:
		return interfacev1.AdminStatus_ADMIN_STATUS_TESTING
	}

	return interfacev1.AdminStatus_ADMIN_STATUS_UNSPECIFIED
}

func operStatus(v ifmib.IfOperStatusValue) interfacev1.OperStatus {
	switch v {
	case ifmib.IfOperStatusValueUp:
		return interfacev1.OperStatus_OPER_STATUS_UP
	case ifmib.IfOperStatusValueDown:
		return interfacev1.OperStatus_OPER_STATUS_DOWN
	case ifmib.IfOperStatusValueTesting:
		return interfacev1.OperStatus_OPER_STATUS_TESTING
	case ifmib.IfOperStatusValueUnknown:
		return interfacev1.OperStatus_OPER_STATUS_UNKNOWN
	case ifmib.IfOperStatusValueDormant:
		return interfacev1.OperStatus_OPER_STATUS_DORMANT
	case ifmib.IfOperStatusValueNotPresent:
		return interfacev1.OperStatus_OPER_STATUS_NOT_PRESENT
	case ifmib.IfOperStatusValueLowerLayerDown:
		return interfacev1.OperStatus_OPER_STATUS_LOWER_LAYER_DOWN
	}

	return interfacev1.OperStatus_OPER_STATUS_UNSPECIFIED
}

// counter is a counter reading and whether the device reported it. The
// distinction is the whole point: an unreported counter leaves its field
// absent, where a reported zero sets it.
type counter struct {
	value    uint64
	reported bool
}

// or falls back to next when this counter was not reported.
func (c counter) or(next counter) counter {
	if c.reported {
		return c
	}

	return next
}

// highCapacity reads one 64-bit ifXTable counter.
func highCapacity(x ifmib.IfXTableRow, col snmp.Column[uint64], value uint64) counter {
	return counter{value: value, reported: x.Observed(col)}
}

// ifTableCounter reads one 32-bit ifTable counter.
func ifTableCounter(r ifmib.IfTableRow, col snmp.Column[uint32], value uint32) counter {
	return counter{value: uint64(value), reported: r.Observed(col)}
}

// ifXTableCounter reads one 32-bit ifXTable counter.
func ifXTableCounter(x ifmib.IfXTableRow, col snmp.Column[uint32], value uint32) counter {
	return counter{value: uint64(value), reported: x.Observed(col)}
}

// interfaceCounters maps the counter columns of both tables, preferring
// the 64-bit ifXTable reading of a counter over the 32-bit ifTable one
// that wraps. It returns nil when the device reported no counter at all.
//
// Multicast and broadcast counts exist only in ifXTable, so a device
// without it reports none. They are never derived from
// ifInNUcastPkts/ifOutNUcastPkts, which lump multicast and broadcast
// together and so answer neither question.
func interfaceCounters(r ifmib.IfTableRow, x ifmib.IfXTableRow) *interfacev1.InterfaceCounters {
	c := &interfacev1.InterfaceCounters{}
	reported := false

	set := func(field func(uint64), value counter) {
		if !value.reported {
			return
		}

		field(value.value)

		reported = true
	}

	set(c.SetInOctets, highCapacity(x, ifmib.IfHCInOctets, x.IfHCInOctets).
		or(ifTableCounter(r, ifmib.IfInOctets, r.IfInOctets)))
	set(c.SetOutOctets, highCapacity(x, ifmib.IfHCOutOctets, x.IfHCOutOctets).
		or(ifTableCounter(r, ifmib.IfOutOctets, r.IfOutOctets)))
	set(c.SetInUnicastPackets, highCapacity(x, ifmib.IfHCInUcastPkts, x.IfHCInUcastPkts).
		or(ifTableCounter(r, ifmib.IfInUcastPkts, r.IfInUcastPkts)))
	set(c.SetOutUnicastPackets, highCapacity(x, ifmib.IfHCOutUcastPkts, x.IfHCOutUcastPkts).
		or(ifTableCounter(r, ifmib.IfOutUcastPkts, r.IfOutUcastPkts)))

	set(c.SetInMulticastPackets, highCapacity(x, ifmib.IfHCInMulticastPkts, x.IfHCInMulticastPkts).
		or(ifXTableCounter(x, ifmib.IfInMulticastPkts, x.IfInMulticastPkts)))
	set(c.SetOutMulticastPackets, highCapacity(x, ifmib.IfHCOutMulticastPkts, x.IfHCOutMulticastPkts).
		or(ifXTableCounter(x, ifmib.IfOutMulticastPkts, x.IfOutMulticastPkts)))
	set(c.SetInBroadcastPackets, highCapacity(x, ifmib.IfHCInBroadcastPkts, x.IfHCInBroadcastPkts).
		or(ifXTableCounter(x, ifmib.IfInBroadcastPkts, x.IfInBroadcastPkts)))
	set(c.SetOutBroadcastPackets, highCapacity(x, ifmib.IfHCOutBroadcastPkts, x.IfHCOutBroadcastPkts).
		or(ifXTableCounter(x, ifmib.IfOutBroadcastPkts, x.IfOutBroadcastPkts)))

	// Errors and discards have no high-capacity columns at all.
	set(c.SetInErrors, ifTableCounter(r, ifmib.IfInErrors, r.IfInErrors))
	set(c.SetOutErrors, ifTableCounter(r, ifmib.IfOutErrors, r.IfOutErrors))
	set(c.SetInDiscards, ifTableCounter(r, ifmib.IfInDiscards, r.IfInDiscards))
	set(c.SetOutDiscards, ifTableCounter(r, ifmib.IfOutDiscards, r.IfOutDiscards))

	if !reported {
		return nil
	}

	return c
}
