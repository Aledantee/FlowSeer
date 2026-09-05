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
	"go.aledante.io/FlowSeer/src/modules/localnet/collect"
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

// The LLDP-MIB table reads the LLDP mapper declares. The management
// address tables carry their address in the index, so their columns are
// asked for only because a walk needs a column to ask for.
var (
	lldpPortConfigRead = collect.NewTable[lldpmib.LldpPortConfigTableRow](
		lldpmib.LldpPortConfigTable.Descriptor(), lldpmib.LldpPortConfigTable.Walk,
		lldpmib.LldpPortConfigAdminStatus,
		lldpmib.LldpPortConfigNotificationEnable,
		lldpmib.LldpPortConfigTLVsTxEnable,
	)
	lldpLocPortRead = collect.NewTable[lldpmib.LldpLocPortTableRow](
		lldpmib.LldpLocPortTable.Descriptor(), lldpmib.LldpLocPortTable.Walk,
		lldpmib.LldpLocPortIdSubtype,
		lldpmib.LldpLocPortId,
		lldpmib.LldpLocPortDesc,
	)
	lldpLocManAddrRead = collect.NewTable[lldpmib.LldpLocManAddrTableRow](
		lldpmib.LldpLocManAddrTable.Descriptor(), lldpmib.LldpLocManAddrTable.Walk,
		lldpmib.LldpLocManAddrIfSubtype,
		lldpmib.LldpLocManAddrIfId,
	)
	lldpRemRead = collect.NewTable[lldpmib.LldpRemTableRow](
		lldpmib.LldpRemTable.Descriptor(), lldpmib.LldpRemTable.Walk,
		lldpmib.LldpRemChassisIdSubtype,
		lldpmib.LldpRemChassisId,
		lldpmib.LldpRemPortIdSubtype,
		lldpmib.LldpRemPortId,
		lldpmib.LldpRemPortDesc,
		lldpmib.LldpRemSysName,
		lldpmib.LldpRemSysDesc,
		lldpmib.LldpRemSysCapSupported,
		lldpmib.LldpRemSysCapEnabled,
	)
	lldpRemManAddrRead = collect.NewTable[lldpmib.LldpRemManAddrTableRow](
		lldpmib.LldpRemManAddrTable.Descriptor(), lldpmib.LldpRemManAddrTable.Walk,
		lldpmib.LldpRemManAddrIfSubtype,
		lldpmib.LldpRemManAddrIfId,
	)
)

// The local-system scalars the LLDP mapper reads.
var (
	lldpLocChassisIDSubtypeRead = collect.NewScalar("lldpLocChassisIdSubtype", lldpmib.LldpLocChassisIdSubtypeGet)
	lldpLocChassisIDRead        = collect.NewScalar("lldpLocChassisId", lldpmib.LldpLocChassisIdGet)
	lldpLocSysNameRead          = collect.NewScalar("lldpLocSysName", lldpmib.LldpLocSysNameGet)
	lldpLocSysDescRead          = collect.NewScalar("lldpLocSysDesc", lldpmib.LldpLocSysDescGet)
	lldpLocSysCapSupportedRead  = collect.NewScalar("lldpLocSysCapSupported", lldpmib.LldpLocSysCapSupportedGet)
	lldpLocSysCapEnabledRead    = collect.NewScalar("lldpLocSysCapEnabled", lldpmib.LldpLocSysCapEnabledGet)
)

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

// LLDPMapper returns the mapper that turns LLDP-MIB into [LLDPFacts]. It
// requires lldpPortConfigTable and lldpRemTable, the tables the facts are
// keyed on, and reads the other three tables and the local-system scalars
// when they are there; it names no sysObjectID prefix because LLDP-MIB is
// standard. Its Map output is an LLDPFacts value.
//
// portNames resolves an LLDP local port number to the interface name the
// rest of the model uses; it is the caller's because LLDP-MIB numbers
// ports on its own and only the caller knows how that numbering lines up
// with IF-MIB on the device at hand. A port number the map does not
// resolve is rendered as its decimal digits rather than dropped: the
// announcement is real and the port number is what identifies it. The
// caller must not modify portNames while the mapper is in use.
func LLDPMapper(portNames map[uint32]string) collect.Mapper {
	return lldpMapper{portNames: portNames}
}

type lldpMapper struct {
	portNames map[uint32]string
}

func (lldpMapper) Spec() collect.Spec {
	return collect.Spec{
		Name:     "lldp",
		Required: []collect.TableRead{lldpPortConfigRead, lldpRemRead},
		Optional: []collect.TableRead{lldpLocPortRead, lldpRemManAddrRead, lldpLocManAddrRead},
		Scalars: []collect.ScalarRead{
			lldpLocChassisIDSubtypeRead,
			lldpLocChassisIDRead,
			lldpLocSysNameRead,
			lldpLocSysDescRead,
			lldpLocSysCapSupportedRead,
			lldpLocSysCapEnabledRead,
		},
	}
}

func (m lldpMapper) Map(snap *collect.Snapshot) (any, error) {
	return LLDPFromSnapshot(snap, m.portNames)
}

// LLDP reads LLDP-MIB on sess through [collect.Read] and maps it with
// [LLDPFromSnapshot]. It does no detection, so a device that implements
// no LLDP-MIB reports failed walks rather than being skipped. portNames
// is as on [LLDPMapper].
func LLDP(ctx context.Context, sess snmp.Session, portNames map[uint32]string) (LLDPFacts, error) {
	return LLDPFromSnapshot(collect.Read(ctx, sess, LLDPMapper(portNames)), portNames)
}

// LLDPFromSnapshot returns the device's own announcement, its per-port
// settings, and the neighbors it holds, from the LLDP-MIB rows and
// scalars in snap.
//
// A failed walk of a table the facts are keyed on returns no facts and an
// error carrying [ErrCodeLLDPWalk]; a failed walk of an enriching table
// returns the facts built from everything else and that error beside
// them, with the rows the failed walk did deliver still enriching. A
// remote row that cannot produce a valid message returns alongside the
// rows that could, reported through the joined error, so a caller that
// ignores the error still sees a truthful if incomplete neighbor set. A
// scalar the agent does not implement leaves its field absent; any other
// scalar read error is returned with the collected facts and preserves
// its cause for [errors.Is] and [errors.As].
func LLDPFromSnapshot(snap *collect.Snapshot, portNames map[uint32]string) (LLDPFacts, error) {
	var fatal []error

	if err := lldpPortConfigRead.Err(snap); err != nil {
		fatal = append(fatal, errs.From(err).Code(ErrCodeLLDPWalk).Msg("walk lldpPortConfigTable"))
	}

	if err := lldpRemRead.Err(snap); err != nil {
		fatal = append(fatal, errs.From(err).Code(ErrCodeLLDPWalk).Msg("walk lldpRemTable"))
	}

	if len(fatal) > 0 {
		return LLDPFacts{}, errors.Join(fatal...)
	}

	ports, portErr := lldpPorts(snap, portNames)
	neighbors, neighborErr := lldpNeighbors(snap, portNames)
	local, localErr := lldpLocalSystem(snap)

	return LLDPFacts{LocalSystem: local, Ports: ports, Neighbors: neighbors},
		errors.Join(portErr, neighborErr, localErr)
}

// lldpLocalSystem reads the device's own announcement: the local-system
// scalars plus the management addresses of lldpLocManAddrTable. A scalar
// the agent does not implement leaves its field absent, so a device with
// no LLDP local data at all yields nil rather than an empty message. A
// failed scalar read or address walk returns the announcement built from
// everything else beside its error.
func lldpLocalSystem(snap *collect.Snapshot) (*lldpv1.LocalSystem, error) {
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

	subtype, subtypeErr := lldpLocChassisIDSubtypeRead.Value(snap)
	id, idErr := lldpLocChassisIDRead.Value(snap)
	recordError("lldpLocChassisIdSubtype", subtypeErr)
	recordError("lldpLocChassisId", idErr)

	if subtypeErr == nil && idErr == nil {
		if chassis, ok := chassisID(subtype, id); ok {
			local.SetChassisId(chassis)

			reported = true
		}
	}

	if name, err := lldpLocSysNameRead.Value(snap); err == nil {
		local.SetSystemName(string(name))

		reported = true
	} else {
		recordError("lldpLocSysName", err)
	}

	if desc, err := lldpLocSysDescRead.Value(snap); err == nil {
		local.SetSystemDescription(string(desc))

		reported = true
	} else {
		recordError("lldpLocSysDesc", err)
	}

	if caps, err := lldpLocSysCapSupportedRead.Value(snap); err == nil {
		local.SetCapabilitiesSupported(capabilities(caps))

		reported = true
	} else {
		recordError("lldpLocSysCapSupported", err)
	}

	if caps, err := lldpLocSysCapEnabledRead.Value(snap); err == nil {
		local.SetCapabilitiesEnabled(capabilities(caps))

		reported = true
	} else {
		recordError("lldpLocSysCapEnabled", err)
	}

	addrs, addrErr := lldpLocManAddrs(snap)
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

// lldpLocManAddrs reads the local management addresses. Each address is
// the row's whole key — the address subtype and the address octets. A
// walk that stopped partway returns the addresses it did read beside its
// error.
func lldpLocManAddrs(snap *collect.Snapshot) ([]*lldpv1.ManagementAddress, error) {
	var addrs []*lldpv1.ManagementAddress

	for _, row := range lldpLocManAddrRead.Rows(snap) {
		if addr, ok := managementAddress(row.Key.LldpLocManAddrSubtype, row.Key.LldpLocManAddr); ok {
			addrs = append(addrs, addr)
		}
	}

	if err := lldpLocManAddrRead.Err(snap); err != nil {
		return addrs, errs.From(err).Code(ErrCodeLLDPWalk).Msg("walk lldpLocManAddrTable")
	}

	return addrs, nil
}

// lldpPorts maps the per-port settings of both port tables, joined on the
// LLDP port number their keys carry. A device that implements only one
// of the two still yields ports, with fewer facts on them, and so does a
// device whose lldpLocPortTable walk failed partway — that table only
// enriches the ports lldpPortConfigTable already named.
func lldpPorts(snap *collect.Snapshot, portNames map[uint32]string) ([]*lldpv1.PortSettings, error) {
	type portRow struct {
		config lldpmib.LldpPortConfigTableRow
		loc    lldpmib.LldpLocPortTableRow
	}

	rows := make(map[lldpmib.LldpPortNumber]*portRow)

	var order []lldpmib.LldpPortNumber

	at := func(num lldpmib.LldpPortNumber) *portRow {
		row, ok := rows[num]
		if !ok {
			row = &portRow{}
			rows[num] = row
			order = append(order, num)
		}

		return row
	}

	for _, row := range lldpPortConfigRead.Rows(snap) {
		at(row.Key.LldpPortConfigPortNum).config = row
	}

	for _, row := range lldpLocPortRead.Rows(snap) {
		at(row.Key.LldpLocPortNum).loc = row
	}

	var locErr error
	if err := lldpLocPortRead.Err(snap); err != nil {
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

// lldpNeighbors maps lldpRemTable, joining each row to the management
// addresses lldpRemManAddrTable carries under the same remote key. A
// failed address walk costs the neighbors it did not reach their
// addresses, not their existence, so it travels back beside them.
func lldpNeighbors(snap *collect.Snapshot, portNames map[uint32]string) ([]*lldpv1.Neighbor, error) {
	addrs, addrErr := lldpRemManAddrs(snap)

	var (
		neighbors []*lldpv1.Neighbor
		rowErrs   []error
	)

	for _, row := range lldpRemRead.Rows(snap) {
		neighbor, err := mapNeighbor(row, addrs[row.Key], portNames)
		if err != nil {
			rowErrs = append(rowErrs, err)

			continue
		}

		neighbors = append(neighbors, neighbor)
	}

	return neighbors, errors.Join(addrErr, errors.Join(rowErrs...))
}

// lldpRemManAddrs groups the neighbors' management addresses by the
// remote row they belong to. Both the address family and the address
// octets are key parts — the table's columns say only how the neighbor
// reaches that address, not what it is. A walk that stopped partway
// returns the groups it did read beside its error.
func lldpRemManAddrs(snap *collect.Snapshot) (map[lldpmib.LldpRemTableKey][]*lldpv1.ManagementAddress, error) {
	addrs := make(map[lldpmib.LldpRemTableKey][]*lldpv1.ManagementAddress)

	for _, row := range lldpRemManAddrRead.Rows(snap) {
		addr, ok := managementAddress(row.Key.LldpRemManAddrSubtype, row.Key.LldpRemManAddr)
		if !ok {
			continue
		}

		// The address key begins with the remote row's own key, which is
		// how the two tables join.
		key := lldpmib.LldpRemTableKey{
			LldpRemTimeMark:     row.Key.LldpRemTimeMark,
			LldpRemLocalPortNum: row.Key.LldpRemLocalPortNum,
			LldpRemIndex:        row.Key.LldpRemIndex,
		}

		addrs[key] = append(addrs[key], addr)
	}

	if err := lldpRemManAddrRead.Err(snap); err != nil {
		return addrs, errs.From(err).Code(ErrCodeLLDPWalk).Msg("walk lldpRemManAddrTable")
	}

	return addrs, nil
}

// mapNeighbor builds one Neighbor from its lldpRemTable row and the
// management addresses of the same remote key.
func mapNeighbor(
	row lldpmib.LldpRemTableRow,
	addrs []*lldpv1.ManagementAddress,
	portNames map[uint32]string,
) (*lldpv1.Neighbor, error) {
	localPort, remIndex := row.Key.LldpRemLocalPortNum, row.Key.LldpRemIndex

	neighbor := &lldpv1.Neighbor{}
	neighbor.SetLocalInterfaceName(localPortName(localPort, portNames))

	chassis, ok := chassisID(row.LldpRemChassisIdSubtype, row.LldpRemChassisId)
	if !ok || !row.Observed(lldpmib.LldpRemChassisIdSubtype) || !row.Observed(lldpmib.LldpRemChassisId) {
		return nil, errs.New().
			Code(ErrCodeLLDPNeighborIncomplete).
			Attr("local_port", localPort).
			Attr("rem_index", remIndex).
			Msgf("lldpRemTable row %d/%d reported no usable chassis identifier", localPort, remIndex)
	}

	neighbor.SetChassisId(chassis)

	port, ok := portID(row.LldpRemPortIdSubtype, row.LldpRemPortId)
	if !ok || !row.Observed(lldpmib.LldpRemPortIdSubtype) || !row.Observed(lldpmib.LldpRemPortId) {
		return nil, errs.New().
			Code(ErrCodeLLDPNeighborIncomplete).
			Attr("local_port", localPort).
			Attr("rem_index", remIndex).
			Msgf("lldpRemTable row %d/%d reported no usable port identifier", localPort, remIndex)
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

// managementAddress reads an address from the key parts that carry it:
// the IANA address family and the address octets. The MIB bounds the
// family to a 16-bit number and the address to 1 to 31 octets; a key
// outside either bound is no address, and the row is skipped.
//
// Families 1 and 2 reach the typed arm only when the payload really is an
// address of that family; a length that contradicts the family, like the
// three octets some agents report for IPv4, keeps every octet through the
// other arm instead of being dropped or padded.
func managementAddress(subtype int32, address string) (*lldpv1.ManagementAddress, bool) {
	length := len(address)
	if subtype < 1 || subtype > math.MaxUint16 || length < 1 || length > 31 {
		return nil, false
	}

	family := uint32(subtype)
	octets := []byte(address)

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
// caller could not resolve still names the row it came from. The lookup
// is on the arc as the agent spelled it, which the conversion restores
// from the narrowed key type.
func localPortName(num lldpmib.LldpPortNumber, portNames map[uint32]string) string {
	arc := uint32(num)

	if name, ok := portNames[arc]; ok && name != "" {
		return name
	}

	return strconv.FormatUint(uint64(arc), 10)
}
