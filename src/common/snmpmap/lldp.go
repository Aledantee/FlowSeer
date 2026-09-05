package snmpmap

import (
	"context"
	"errors"
	"math"
	"strconv"

	"go.aledante.io/FlowSeer/generated/go/mib/lldpmib"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	lldpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lldp/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

var (
	// ErrCodeLLDPNeighborIncomplete identifies an lldpRemTable row that
	// reported no usable chassis or port identifier. Both are mandatory
	// TLVs and every Neighbor carries them, so such a row yields no
	// message.
	ErrCodeLLDPNeighborIncomplete = errs.NewCode("snmpmap/lldp-neighbor-incomplete")
	// ErrCodeLLDPWalk identifies a failure of one of the LLDP-MIB table
	// walks. A failed walk of a table the facts are keyed on —
	// lldpPortConfigTable, lldpRemTable — returns no LLDP facts; a failed
	// walk of one that only enriches those rows returns them with fewer
	// facts on them.
	ErrCodeLLDPWalk = errs.NewCode("snmpmap/lldp-walk")
)

// fatalWalkError marks the failure of a table the surrounding mapping cannot
// stand without, telling [LLDP] apart from the failure of a table that
// only enriches rows already collected. It unwraps to the error it
// marks, so the code and attributes stay discoverable.
type fatalWalkError struct{ err error }

// Error preserves the failed walk's diagnostic message.
func (f fatalWalkError) Error() string { return f.err.Error() }

// Unwrap exposes the walk error to errors.Is and errors.As.
func (f fatalWalkError) Unwrap() error { return f.err }

// isFatalWalkError reports whether err carries a [fatalWalkError] mark.
func isFatalWalkError(err error) bool {
	var f fatalWalkError

	return errors.As(err, &f)
}

// LLDPFacts are the messages one device's LLDP-MIB yields: what the
// device announces about itself, how each of its ports runs the
// protocol, and what its neighbors announced.
// The zero value contains no facts. Concurrent reads are safe; callers
// must synchronize mutations of the slices or messages.
type LLDPFacts struct {
	// LocalSystem is what the device announces device-wide, or nil when
	// it reported none of it.
	LocalSystem *lldpv1.LocalSystem
	// Ports are the per-port settings, one per port the device reports
	// in lldpPortConfigTable or lldpLocPortTable.
	Ports []*lldpv1.PortSettings
	// Neighbors are the announcements held in lldpRemTable, in walk
	// order.
	Neighbors []*lldpv1.Neighbor
}

// portConfigColumns are the lldpPortConfigTable columns [LLDP] requests.
var portConfigColumns = []snmp.AnyColumn{
	lldpmib.LldpPortConfigAdminStatus,
	lldpmib.LldpPortConfigNotificationEnable,
	lldpmib.LldpPortConfigTLVsTxEnable,
}

// locPortColumns are the lldpLocPortTable columns [LLDP] requests.
var locPortColumns = []snmp.AnyColumn{
	lldpmib.LldpLocPortIdSubtype,
	lldpmib.LldpLocPortId,
	lldpmib.LldpLocPortDesc,
}

// locManAddrColumns are the lldpLocManAddrTable columns [LLDP] requests.
// The address itself is an index arc, so the columns are asked for only
// because a walk needs a column to ask for.
var locManAddrColumns = []snmp.AnyColumn{
	lldpmib.LldpLocManAddrIfSubtype,
	lldpmib.LldpLocManAddrIfId,
}

// remColumns are the lldpRemTable columns [LLDP] requests.
var remColumns = []snmp.AnyColumn{
	lldpmib.LldpRemChassisIdSubtype,
	lldpmib.LldpRemChassisId,
	lldpmib.LldpRemPortIdSubtype,
	lldpmib.LldpRemPortId,
	lldpmib.LldpRemPortDesc,
	lldpmib.LldpRemSysName,
	lldpmib.LldpRemSysDesc,
	lldpmib.LldpRemSysCapSupported,
	lldpmib.LldpRemSysCapEnabled,
}

// remManAddrColumns are the lldpRemManAddrTable columns [LLDP] requests,
// for the same reason as [locManAddrColumns].
var remManAddrColumns = []snmp.AnyColumn{
	lldpmib.LldpRemManAddrIfSubtype,
	lldpmib.LldpRemManAddrIfId,
}

// The IANA address family numbers whose management addresses reach the
// typed IP arm (https://www.iana.org/assignments/address-family-numbers).
const (
	ipv4Family = 1
	ipv6Family = 2
)

// tlvTypeOfBit is the whole of lldpPortConfigTLVsTxEnable: LLDP-MIB
// defines the object as BITS { portDesc(0), sysName(1), sysDesc(2),
// sysCap(3) } and nothing else. The bitmap is not a window onto the
// 802.1AB type registry — the MIB says outright that no bit is reserved
// for the management-address TLV, because lldpConfigManAddrTable controls
// that one, and that organizationally-specific TLVs are excluded too. So
// a set position outside this map names no TLV at all: continuing the
// arithmetic past bit 3 would claim a transmission the device never
// announced.
var tlvTypeOfBit = map[snmp.BitPos]lldpv1.TlvType{
	0: lldpv1.TlvType_TLV_TYPE_PORT_DESCRIPTION,
	1: lldpv1.TlvType_TLV_TYPE_SYSTEM_NAME,
	2: lldpv1.TlvType_TLV_TYPE_SYSTEM_DESCRIPTION,
	3: lldpv1.TlvType_TLV_TYPE_SYSTEM_CAPABILITIES,
}

// LLDP walks LLDP-MIB on sess and returns the device's own announcement,
// its per-port settings, and the neighbors it holds.
//
// portNames resolves an LLDP local port number to the interface name the
// rest of the model uses; it is the caller's because LLDP-MIB numbers
// ports on its own and only the caller knows how that numbering lines up
// with IF-MIB on the device at hand. A port number the map does not
// resolve is rendered as its decimal digits rather than dropped: the
// announcement is real and the port number is what identifies it.
//
// A failed walk of a table the facts are keyed on returns no facts and an
// error carrying [ErrCodeLLDPWalk]; a failed walk of an enriching table
// returns the facts collected so far and that error beside them. A remote
// row that cannot produce a valid message returns alongside the rows that
// could, reported through the joined error, so a caller that ignores the
// error still sees a truthful if incomplete neighbor set. A row whose
// index suffix is not what the MIB's INDEX clause describes is skipped
// silently, as is a scalar the agent does not implement.
// Other scalar read errors are returned with the collected facts and
// preserve their causes for [errors.Is] and [errors.As]. The caller must
// not modify portNames during the call.
func LLDP(ctx context.Context, sess snmp.Session, portNames map[uint32]string) (LLDPFacts, error) {
	// A base table's failed walk is fatal where an enriching walk's and a
	// row that could not be mapped are not, so results and errors travel
	// back together and the fatal ones carry a mark.
	ports, portErr := lldpPorts(ctx, sess, portNames)
	if isFatalWalkError(portErr) {
		return LLDPFacts{}, portErr
	}

	neighbors, neighborErr := lldpNeighbors(ctx, sess, portNames)
	if isFatalWalkError(neighborErr) {
		return LLDPFacts{}, errors.Join(neighborErr, portErr)
	}

	// No table the local system reads is one the facts are keyed on, so
	// its failure never ends the mapping: it costs the device's own
	// announcement some addresses, not the ports and neighbors already in
	// hand.
	local, localErr := lldpLocalSystem(ctx, sess)

	return LLDPFacts{LocalSystem: local, Ports: ports, Neighbors: neighbors},
		errors.Join(portErr, neighborErr, localErr)
}

// readScalar reads one scalar with get and hands its value to set,
// reporting whether the read succeeded. A failed read leaves the field
// absent and goes to recordError under name, which decides whether the
// failure is worth reporting at all.
func readScalar[T any](name string, get func() (T, error), set func(T), recordError func(string, error)) bool {
	v, err := get()
	if err != nil {
		recordError(name, err)

		return false
	}

	set(v)

	return true
}

// lldpLocalSystem reads the device's own announcement: the local-system
// scalars plus the management addresses of lldpLocManAddrTable. A scalar
// the agent does not implement leaves its field absent, so a device with
// no LLDP local data at all yields nil rather than an empty message. A
// failed scalar read or address walk returns the announcement built from
// everything else beside its error.
func lldpLocalSystem(ctx context.Context, sess snmp.Session) (*lldpv1.LocalSystem, error) {
	local := &lldpv1.LocalSystem{}
	reported := false
	var readErrs []error

	recordError := func(name string, err error) {
		if err == nil || errors.Is(err, snmp.ErrException) {
			return
		}
		// SNMPv1 reports an unsupported scalar through the PDU status;
		// later versions use exception varbinds instead.
		var pduErr *snmp.PDUError
		if errors.As(err, &pduErr) && pduErr.Status == snmp.NoSuchName {
			return
		}

		readErrs = append(readErrs, errs.Wrap(err, "read "+name))
	}

	subtype, subtypeErr := lldpmib.LldpLocChassisIdSubtypeGet(ctx, sess)
	id, idErr := lldpmib.LldpLocChassisIdGet(ctx, sess)
	recordError("lldpLocChassisIdSubtype", subtypeErr)
	recordError("lldpLocChassisId", idErr)

	if subtypeErr == nil && idErr == nil {
		if chassis, ok := chassisID(subtype, id); ok {
			local.SetChassisId(chassis)

			reported = true
		}
	}

	getName := func() ([]byte, error) { return lldpmib.LldpLocSysNameGet(ctx, sess) }
	if readScalar("lldpLocSysName", getName, func(v []byte) { local.SetSystemName(string(v)) }, recordError) {
		reported = true
	}

	getDesc := func() ([]byte, error) { return lldpmib.LldpLocSysDescGet(ctx, sess) }
	if readScalar("lldpLocSysDesc", getDesc, func(v []byte) { local.SetSystemDescription(string(v)) }, recordError) {
		reported = true
	}

	getSupported := func() (snmp.BitSet, error) { return lldpmib.LldpLocSysCapSupportedGet(ctx, sess) }
	if readScalar("lldpLocSysCapSupported", getSupported, func(v snmp.BitSet) { local.SetCapabilitiesSupported(capabilities(v)) }, recordError) {
		reported = true
	}

	getEnabled := func() (snmp.BitSet, error) { return lldpmib.LldpLocSysCapEnabledGet(ctx, sess) }
	if readScalar("lldpLocSysCapEnabled", getEnabled, func(v snmp.BitSet) { local.SetCapabilitiesEnabled(capabilities(v)) }, recordError) {
		reported = true
	}

	addrs, addrErr := lldpLocManAddrs(ctx, sess)
	readErrs = append(readErrs, addrErr)
	if len(addrs) > 0 {
		local.SetManagementAddresses(addrs)

		reported = true
	}

	if !reported {
		return nil, errors.Join(readErrs...)
	}

	return local, errors.Join(readErrs...)
}

// lldpLocManAddrs walks the local management addresses. Each address is
// the row's whole index — (address subtype, length, octets) — so a row
// whose index is not that shape carries no address and is skipped. A walk
// that stops partway returns the addresses it did read beside its error.
func lldpLocManAddrs(ctx context.Context, sess snmp.Session) ([]*lldpv1.ManagementAddress, error) {
	var addrs []*lldpv1.ManagementAddress

	walk := lldpmib.LldpLocManAddrTable.Walk(ctx, sess, locManAddrColumns...)
	for idx := range walk.Iter() {
		if addr, ok := managementAddress(idx, 0); ok {
			addrs = append(addrs, addr)
		}
	}

	if err := walk.Err(); err != nil {
		return addrs, errs.From(err).Code(ErrCodeLLDPWalk).Msg("walk lldpLocManAddrTable")
	}

	return addrs, nil
}

// lldpPorts maps the per-port settings of both port tables, joined on the
// LLDP port number their indexes carry. A device that implements only one
// of the two still yields ports, with fewer facts on them, and so does a
// device whose lldpLocPortTable walk fails partway — that table only
// enriches the ports lldpPortConfigTable already named.
func lldpPorts(ctx context.Context, sess snmp.Session, portNames map[uint32]string) ([]*lldpv1.PortSettings, error) {
	type portRow struct {
		config lldpmib.LldpPortConfigTableRow
		loc    lldpmib.LldpLocPortTableRow
	}

	rows := make(map[uint32]*portRow)

	var order []uint32

	at := func(num uint32) *portRow {
		row, ok := rows[num]
		if !ok {
			row = &portRow{}
			rows[num] = row
			order = append(order, num)
		}

		return row
	}

	configWalk := lldpmib.LldpPortConfigTable.Walk(ctx, sess, portConfigColumns...)
	for idx, row := range configWalk.Iter() {
		num, ok := singleIndex(idx)
		if !ok {
			continue
		}

		at(num).config = row
	}

	if err := configWalk.Err(); err != nil {
		return nil, fatalWalkError{errs.From(err).Code(ErrCodeLLDPWalk).Msg("walk lldpPortConfigTable")}
	}

	locWalk := lldpmib.LldpLocPortTable.Walk(ctx, sess, locPortColumns...)
	for idx, row := range locWalk.Iter() {
		num, ok := singleIndex(idx)
		if !ok {
			continue
		}

		at(num).loc = row
	}

	var locErr error
	if err := locWalk.Err(); err != nil {
		locErr = errs.From(err).Code(ErrCodeLLDPWalk).Msg("walk lldpLocPortTable")
	}

	ports := make([]*lldpv1.PortSettings, 0, len(order))

	for _, num := range order {
		row := rows[num]

		port := &lldpv1.PortSettings{}
		port.SetInterfaceName(localPortName(num, portNames))

		// The MIB assigns no zero admin status, so a nonsense reading
		// leaves the field absent; a value the schema does not name is
		// kept, since the two numberings are the same registry.
		if row.config.Observed(lldpmib.LldpPortConfigAdminStatus) && row.config.LldpPortConfigAdminStatus > 0 {
			port.SetAdminStatus(lldpv1.PortAdminStatus(row.config.LldpPortConfigAdminStatus))
		}

		if row.config.Observed(lldpmib.LldpPortConfigNotificationEnable) {
			port.SetNotificationsEnabled(row.config.LldpPortConfigNotificationEnable)
		}

		if row.config.Observed(lldpmib.LldpPortConfigTLVsTxEnable) {
			if tlvs := transmittedTLVs(row.config.LldpPortConfigTLVsTxEnable); len(tlvs) > 0 {
				port.SetTransmittedTlvs(tlvs)
			}
		}

		if row.loc.Observed(lldpmib.LldpLocPortIdSubtype) && row.loc.Observed(lldpmib.LldpLocPortId) {
			if portID, ok := portID(row.loc.LldpLocPortIdSubtype, row.loc.LldpLocPortId); ok {
				port.SetPortId(portID)
			}
		}

		if row.loc.Observed(lldpmib.LldpLocPortDesc) {
			port.SetPortDescription(string(row.loc.LldpLocPortDesc))
		}

		ports = append(ports, port)
	}

	return ports, locErr
}

// remKey identifies one lldpRemTable row by the arcs of its index, which
// is also the prefix of every management-address row belonging to it.
type remKey struct {
	timeMark  uint32
	localPort uint32
	remIndex  uint32
}

// lldpNeighbors maps lldpRemTable, joining each row to the management
// addresses lldpRemManAddrTable carries under the same index prefix. A
// failed address walk costs the neighbors it did not reach their
// addresses, not their existence, so it travels back beside them.
func lldpNeighbors(ctx context.Context, sess snmp.Session, portNames map[uint32]string) ([]*lldpv1.Neighbor, error) {
	addrs, addrErr := lldpRemManAddrs(ctx, sess)

	var (
		neighbors []*lldpv1.Neighbor
		rowErrs   []error
	)

	walk := lldpmib.LldpRemTable.Walk(ctx, sess, remColumns...)
	for idx, row := range walk.Iter() {
		key, ok := remIndex(idx)
		if !ok {
			continue
		}

		neighbor, err := mapNeighbor(key, row, addrs[key], portNames)
		if err != nil {
			rowErrs = append(rowErrs, err)

			continue
		}

		neighbors = append(neighbors, neighbor)
	}

	if err := walk.Err(); err != nil {
		fatal := fatalWalkError{errs.From(err).Code(ErrCodeLLDPWalk).Msg("walk lldpRemTable")}

		return nil, errors.Join(fatal, addrErr)
	}

	return neighbors, errors.Join(addrErr, errors.Join(rowErrs...))
}

// lldpRemManAddrs walks the neighbors' management addresses and groups
// them by the remote row they belong to. Both the address family and the
// address octets are index arcs — the table's columns say only how the
// neighbor reaches that address, not what it is. A walk that stops
// partway returns the groups it did read beside its error.
func lldpRemManAddrs(ctx context.Context, sess snmp.Session) (map[remKey][]*lldpv1.ManagementAddress, error) {
	addrs := make(map[remKey][]*lldpv1.ManagementAddress)

	walk := lldpmib.LldpRemManAddrTable.Walk(ctx, sess, remManAddrColumns...)
	for idx := range walk.Iter() {
		key, ok := remIndex(idx)
		if !ok {
			continue
		}

		addr, ok := managementAddress(idx, 3)
		if !ok {
			continue
		}

		addrs[key] = append(addrs[key], addr)
	}

	if err := walk.Err(); err != nil {
		return addrs, errs.From(err).Code(ErrCodeLLDPWalk).Msg("walk lldpRemManAddrTable")
	}

	return addrs, nil
}

// mapNeighbor builds one Neighbor from its lldpRemTable row and the
// management addresses of the same remote index.
func mapNeighbor(
	key remKey,
	row lldpmib.LldpRemTableRow,
	addrs []*lldpv1.ManagementAddress,
	portNames map[uint32]string,
) (*lldpv1.Neighbor, error) {
	neighbor := &lldpv1.Neighbor{}
	neighbor.SetLocalInterfaceName(localPortName(key.localPort, portNames))

	chassis, ok := chassisID(row.LldpRemChassisIdSubtype, row.LldpRemChassisId)
	if !ok || !row.Observed(lldpmib.LldpRemChassisIdSubtype) || !row.Observed(lldpmib.LldpRemChassisId) {
		return nil, errs.New().
			Code(ErrCodeLLDPNeighborIncomplete).
			Attr("local_port", key.localPort).
			Attr("rem_index", key.remIndex).
			Msgf("lldpRemTable row %d/%d reported no usable chassis identifier", key.localPort, key.remIndex)
	}

	neighbor.SetChassisId(chassis)

	port, ok := portID(row.LldpRemPortIdSubtype, row.LldpRemPortId)
	if !ok || !row.Observed(lldpmib.LldpRemPortIdSubtype) || !row.Observed(lldpmib.LldpRemPortId) {
		return nil, errs.New().
			Code(ErrCodeLLDPNeighborIncomplete).
			Attr("local_port", key.localPort).
			Attr("rem_index", key.remIndex).
			Msgf("lldpRemTable row %d/%d reported no usable port identifier", key.localPort, key.remIndex)
	}

	neighbor.SetPortId(port)

	if row.Observed(lldpmib.LldpRemPortDesc) {
		neighbor.SetPortDescription(string(row.LldpRemPortDesc))
	}

	if row.Observed(lldpmib.LldpRemSysName) {
		neighbor.SetSystemName(string(row.LldpRemSysName))
	}

	if row.Observed(lldpmib.LldpRemSysDesc) {
		neighbor.SetSystemDescription(string(row.LldpRemSysDesc))
	}

	if row.Observed(lldpmib.LldpRemSysCapSupported) {
		neighbor.SetCapabilitiesSupported(capabilities(row.LldpRemSysCapSupported))
	}

	if row.Observed(lldpmib.LldpRemSysCapEnabled) {
		neighbor.SetCapabilitiesEnabled(capabilities(row.LldpRemSysCapEnabled))
	}

	if len(addrs) > 0 {
		neighbor.SetManagementAddresses(addrs)
	}

	return neighbor, nil
}

// remIndex reads the (lldpRemTimeMark, lldpRemLocalPortNum, lldpRemIndex)
// arcs an lldpRemTable row is keyed on. The lldpRemManAddrTable index
// begins with the same three arcs and carries the address after them, so
// a longer index still reads as the row it belongs to.
func remIndex(idx snmp.OID) (remKey, bool) {
	if idx.Len() < 3 {
		return remKey{}, false
	}

	return remKey{timeMark: idx.At(0), localPort: idx.At(1), remIndex: idx.At(2)}, true
}

// managementAddress reads an address that an index carries from arc off
// on: the IANA address family, the octet count, and that many octets.
//
// Families 1 and 2 reach the typed arm only when the payload really is an
// address of that family; a length that contradicts the family, like the
// three octets some agents report for IPv4, keeps every octet through the
// other arm instead of being dropped or padded.
func managementAddress(idx snmp.OID, off int) (*lldpv1.ManagementAddress, bool) {
	if idx.Len() < off+2 {
		return nil, false
	}

	family := idx.At(off)

	length := idx.At(off + 1)
	if family < 1 || family > math.MaxUint16 || length < 1 || length > 31 {
		return nil, false
	}

	// The octets are one arc each and the length arc counts them exactly,
	// so an index that runs short or long is not the address it claims.
	if idx.Len() != off+2+int(length) {
		return nil, false
	}

	octets := make([]byte, 0, length)

	for i := range int(length) {
		arc := idx.At(off + 2 + i)
		if arc > math.MaxUint8 {
			return nil, false
		}

		octets = append(octets, byte(arc))
	}

	addr := &lldpv1.ManagementAddress{}

	switch {
	case family == ipv4Family && length == 4:
		v4 := &addrv1.Ipv4Address{}
		v4.SetOctets(octets)

		ip := &addrv1.IpAddress{}
		ip.SetV4(v4)
		addr.SetIp(ip)

	case family == ipv6Family && length == 16:
		v6 := &addrv1.Ipv6Address{}
		v6.SetOctets(octets)

		ip := &addrv1.IpAddress{}
		ip.SetV6(v6)
		addr.SetIp(ip)

	default:
		other := &lldpv1.OtherManagementAddress{}
		other.SetAddressFamily(family)
		other.SetValue(octets)
		addr.SetOther(other)
	}

	return addr, true
}

// chassisID keeps the identifier as the protocol carries it: the subtype
// as reported, including one this schema version does not name, and the
// octets untouched. A subtype of zero or an octet string outside the
// protocol's 1-to-255 range is no identifier at all.
func chassisID(subtype lldpmib.LldpChassisIdSubtype, value []byte) (*lldpv1.ChassisId, bool) {
	if subtype < 1 || len(value) < 1 || len(value) > 255 {
		return nil, false
	}

	id := &lldpv1.ChassisId{}
	id.SetSubtype(lldpv1.ChassisIdSubtype(subtype))
	id.SetValue(value)

	return id, true
}

// portID is [chassisID] for a port identifier, which the protocol shapes
// the same way.
func portID(subtype lldpmib.LldpPortIdSubtype, value []byte) (*lldpv1.PortId, bool) {
	if subtype < 1 || len(value) < 1 || len(value) > 255 {
		return nil, false
	}

	id := &lldpv1.PortId{}
	id.SetSubtype(lldpv1.PortIdSubtype(subtype))
	id.SetValue(value)

	return id, true
}

// capabilities maps a capability bitmap to the schema's open enum, one
// value per set position. A position the schema does not name is kept as
// its own value: the announcement said the neighbor has that capability,
// and only the name is missing.
func capabilities(bits snmp.BitSet) []lldpv1.SystemCapability {
	positions := bits.Positions()
	caps := make([]lldpv1.SystemCapability, 0, len(positions))

	for _, p := range positions {
		if p > math.MaxInt32 {
			continue
		}

		caps = append(caps, lldpv1.SystemCapability(p))
	}

	if len(caps) == 0 {
		return nil
	}

	return caps
}

// transmittedTLVs translates the enablement bitmap into TLV types
// through [tlvTypeOfBit]. Unlike [capabilities], which keeps a position
// it cannot name, a position outside the map is dropped: the capability
// bitmap is a registry that grows, while this one is closed by the MIB
// that defines it.
func transmittedTLVs(bits snmp.BitSet) []lldpv1.TlvType {
	positions := bits.Positions()
	tlvs := make([]lldpv1.TlvType, 0, len(positions))

	for _, p := range positions {
		if tlv, ok := tlvTypeOfBit[p]; ok {
			tlvs = append(tlvs, tlv)
		}
	}

	return tlvs
}

// localPortName resolves an LLDP port number to the interface name the
// caller knows it by, falling back to the number itself so a port the
// caller could not resolve still names the row it came from.
func localPortName(num uint32, portNames map[uint32]string) string {
	if name, ok := portNames[num]; ok && name != "" {
		return name
	}

	return strconv.FormatUint(uint64(num), 10)
}
