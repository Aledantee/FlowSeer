package snmpmap_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	lldpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lldp/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/snmp"
	"go.aledante.io/FlowSeer/src/common/snmpmap"
)

var (
	lldpPortConfigEntry = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 1, 6, 1)
	lldpLocPortEntry    = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 7, 1)
	lldpLocManAddrEntry = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 8, 1)
	lldpRemEntry        = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 4, 1, 1)
	lldpRemManAddrEntry = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 4, 2, 1)

	lldpLocChassisIDSubtypeOID = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 1, 0)
	lldpLocChassisIDOID        = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 2, 0)
	lldpLocSysNameOID          = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 3, 0)
	lldpLocSysDescOID          = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 4, 0)
	lldpLocSysCapSupportedOID  = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 5, 0)
	lldpLocSysCapEnabledOID    = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 6, 0)
)

// colOID builds the instance OID of column col for the row whose index
// suffix is arcs. LLDP-MIB indexes several tables on more than one arc,
// so the index is a slice rather than the single ifIndex the IF-MIB
// helpers take.
func colOID(entry snmp.OID, col uint32, arcs ...uint32) snmp.OID {
	return entry.Append(col).Append(arcs...)
}

// intAt builds an INTEGER varbind at an arbitrary OID.
func intAt(oid snmp.OID, value int32) vbFixture {
	return vbFixture{oid: oid, vb: snmp.Integer32Var{
		Header: snmp.Header{OID: oid, Kind: snmp.KindInteger32},
		Value:  value,
	}}
}

// octetsAt builds an OCTET STRING varbind at an arbitrary OID. BITS
// values ride the same wire type, so a bitmap column uses this too.
func octetsAt(oid snmp.OID, value []byte) vbFixture {
	return vbFixture{oid: oid, vb: snmp.OctetStringVar{
		Header: snmp.Header{OID: oid, Kind: snmp.KindOctetString},
		Value:  value,
	}}
}

// bitsOctets renders the set positions as the BITS wire form: position 0
// is the most significant bit of the first octet (RFC 2578 §7.1.4).
func bitsOctets(positions ...uint32) []byte {
	highest := uint32(0)
	for _, p := range positions {
		if p > highest {
			highest = p
		}
	}

	octets := make([]byte, highest/8+1)
	for _, p := range positions {
		octets[p/8] |= 0x80 >> (p % 8)
	}

	return octets
}

// remRow builds the mandatory columns of one lldpRemTable row: the
// chassis identifier and the port identifier every announcement carries.
func remRow(idx []uint32, chassisSubtype int32, chassis []byte, portSubtype int32, port []byte) []vbFixture {
	return []vbFixture{
		intAt(colOID(lldpRemEntry, 4, idx...), chassisSubtype),
		octetsAt(colOID(lldpRemEntry, 5, idx...), chassis),
		intAt(colOID(lldpRemEntry, 6, idx...), portSubtype),
		octetsAt(colOID(lldpRemEntry, 7, idx...), port),
	}
}

// macRemRow is the common case: a neighbor identified by its chassis MAC
// address, announcing an interface-name port identifier, seen on local
// port 3 as remote index 1.
func macRemRow() []vbFixture {
	return remRow(
		[]uint32{1000, 3, 1},
		4, // macAddress
		[]byte{0x00, 0x1b, 0x21, 0x0a, 0x0b, 0x0c},
		5, // interfaceName
		[]byte("Gi1/0/24"),
	)
}

// lldpFacts runs the mapper over vbs, requires no error, and holds every
// emitted message to its own schema rules — the mapper may not emit a
// message the model would reject.
func lldpFacts(t *testing.T, vbs []vbFixture, portNames map[uint32]string) snmpmap.LLDPFacts {
	t.Helper()

	facts, err := snmpmap.LLDP(context.Background(), &fakeSession{vbs: vbs}, portNames)
	if err != nil {
		t.Fatalf("LLDP: %v", err)
	}

	validateFacts(t, facts)

	return facts
}

func validateFacts(t *testing.T, facts snmpmap.LLDPFacts) {
	t.Helper()

	msgs := make([]proto.Message, 0, len(facts.Neighbors)+len(facts.Ports)+1)
	if facts.LocalSystem != nil {
		msgs = append(msgs, facts.LocalSystem)
	}

	for _, p := range facts.Ports {
		msgs = append(msgs, p)
	}

	for _, n := range facts.Neighbors {
		msgs = append(msgs, n)
	}

	for _, m := range msgs {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("mapped %T violates its schema: %v", m, err)
		}
	}
}

// oneNeighbor runs the mapper and requires exactly one neighbor back.
func oneNeighbor(t *testing.T, vbs []vbFixture, portNames map[uint32]string) *lldpv1.Neighbor {
	t.Helper()

	facts := lldpFacts(t, vbs, portNames)
	if len(facts.Neighbors) != 1 {
		t.Fatalf("got %d neighbors, want 1", len(facts.Neighbors))
	}

	return facts.Neighbors[0]
}

// TestLLDP_ChassisIdVerbatim pins KTD6: the subtype and the octets reach
// the message exactly as the agent reported them, with no display string
// invented from either.
func TestLLDP_ChassisIdVerbatim(t *testing.T) {
	got := oneNeighbor(t, macRemRow(), map[uint32]string{3: "GigabitEthernet0/1"})

	if got.GetLocalInterfaceName() != "GigabitEthernet0/1" {
		t.Errorf("got local interface %q, want the resolved name", got.GetLocalInterfaceName())
	}

	chassis := got.GetChassisId()
	if chassis.GetSubtype() != lldpv1.ChassisIdSubtype_CHASSIS_ID_SUBTYPE_MAC_ADDRESS {
		t.Errorf("got chassis subtype %v, want macAddress", chassis.GetSubtype())
	}

	if want := []byte{0x00, 0x1b, 0x21, 0x0a, 0x0b, 0x0c}; !bytes.Equal(chassis.GetValue(), want) {
		t.Errorf("got chassis octets %x, want %x", chassis.GetValue(), want)
	}

	port := got.GetPortId()
	if port.GetSubtype() != lldpv1.PortIdSubtype_PORT_ID_SUBTYPE_INTERFACE_NAME {
		t.Errorf("got port subtype %v, want interfaceName", port.GetSubtype())
	}

	if want := []byte("Gi1/0/24"); !bytes.Equal(port.GetValue(), want) {
		t.Errorf("got port octets %q, want %q", port.GetValue(), want)
	}
}

// TestLLDP_UnnamedSubtypeSurvives keeps a subtype the schema does not
// name: the registry may assign one this version has never heard of, and
// dropping it would lose the announcement.
func TestLLDP_UnnamedSubtypeSurvives(t *testing.T) {
	vbs := remRow([]uint32{1, 3, 1}, 9, []byte{0x01}, 8, []byte{0x02})

	got := oneNeighbor(t, vbs, map[uint32]string{3: "eth0"})

	if want := lldpv1.ChassisIdSubtype(9); got.GetChassisId().GetSubtype() != want {
		t.Errorf("got chassis subtype %v, want the unnamed %v", got.GetChassisId().GetSubtype(), want)
	}

	if want := lldpv1.PortIdSubtype(8); got.GetPortId().GetSubtype() != want {
		t.Errorf("got port subtype %v, want the unnamed %v", got.GetPortId().GetSubtype(), want)
	}
}

// TestLLDP_ManagementAddressFromIndexArcs covers the fact that a
// management address is carried in the row's index, not in a column: the
// family and the octets come from the index suffix alone.
func TestLLDP_ManagementAddressFromIndexArcs(t *testing.T) {
	tests := []struct {
		name  string
		arcs  []uint32
		check func(*testing.T, *lldpv1.ManagementAddress)
	}{
		{
			name: "ipv4",
			arcs: []uint32{1, 4, 192, 0, 2, 10},
			check: func(t *testing.T, addr *lldpv1.ManagementAddress) {
				t.Helper()

				if !addr.GetIp().HasV4() {
					t.Fatalf("got %v, want the IPv4 arm", addr)
				}

				if want := []byte{192, 0, 2, 10}; !bytes.Equal(addr.GetIp().GetV4().GetOctets(), want) {
					t.Errorf("got %x, want %x", addr.GetIp().GetV4().GetOctets(), want)
				}
			},
		},
		{
			name: "ipv6",
			arcs: []uint32{2, 16, 0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
			check: func(t *testing.T, addr *lldpv1.ManagementAddress) {
				t.Helper()

				if !addr.GetIp().HasV6() {
					t.Fatalf("got %v, want the IPv6 arm", addr)
				}

				if got := len(addr.GetIp().GetV6().GetOctets()); got != 16 {
					t.Errorf("got %d octets, want 16", got)
				}
			},
		},
		{
			name: "other family",
			arcs: []uint32{6, 2, 0xab, 0xcd},
			check: func(t *testing.T, addr *lldpv1.ManagementAddress) {
				t.Helper()

				if !addr.HasOther() {
					t.Fatalf("got %v, want the other arm", addr)
				}

				if addr.GetOther().GetAddressFamily() != 6 {
					t.Errorf("got family %d, want 6", addr.GetOther().GetAddressFamily())
				}

				if want := []byte{0xab, 0xcd}; !bytes.Equal(addr.GetOther().GetValue(), want) {
					t.Errorf("got %x, want %x", addr.GetOther().GetValue(), want)
				}
			},
		},
		{
			name: "ipv4 family with a payload that is not an address",
			arcs: []uint32{1, 3, 192, 0, 2},
			check: func(t *testing.T, addr *lldpv1.ManagementAddress) {
				t.Helper()

				if !addr.HasOther() {
					t.Fatalf("got %v, want the other arm for a malformed IPv4 payload", addr)
				}

				if addr.GetOther().GetAddressFamily() != 1 {
					t.Errorf("got family %d, want 1", addr.GetOther().GetAddressFamily())
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vbs := macRemRow()
			idx := append([]uint32{1000, 3, 1}, tc.arcs...)
			vbs = append(vbs, intAt(colOID(lldpRemManAddrEntry, 4, idx...), 42))

			got := oneNeighbor(t, vbs, map[uint32]string{3: "eth0"})
			if len(got.GetManagementAddresses()) != 1 {
				t.Fatalf("got %d management addresses, want 1", len(got.GetManagementAddresses()))
			}

			tc.check(t, got.GetManagementAddresses()[0])
		})
	}
}

// TestLLDP_CapabilitiesKeepUnnamedPositions is the point of the repeated
// open enum: a position IEEE assigns after this schema version still
// reaches a consumer, as its own value.
func TestLLDP_CapabilitiesKeepUnnamedPositions(t *testing.T) {
	vbs := macRemRow()
	vbs = append(vbs,
		octetsAt(colOID(lldpRemEntry, 11, 1000, 3, 1), bitsOctets(2, 4, 11)),
		octetsAt(colOID(lldpRemEntry, 12, 1000, 3, 1), bitsOctets(4)),
	)

	got := oneNeighbor(t, vbs, map[uint32]string{3: "eth0"})

	want := []lldpv1.SystemCapability{
		lldpv1.SystemCapability_SYSTEM_CAPABILITY_BRIDGE,
		lldpv1.SystemCapability_SYSTEM_CAPABILITY_ROUTER,
		lldpv1.SystemCapability(11),
	}
	if !slices.Equal(got.GetCapabilitiesSupported(), want) {
		t.Errorf("got supported %v, want %v", got.GetCapabilitiesSupported(), want)
	}

	if want[2].Enum().Descriptor().Values().ByNumber(11) != nil {
		t.Error("position 11 is a named capability; pick an unnamed one for this test")
	}

	enabled := []lldpv1.SystemCapability{lldpv1.SystemCapability_SYSTEM_CAPABILITY_ROUTER}
	if !slices.Equal(got.GetCapabilitiesEnabled(), enabled) {
		t.Errorf("got enabled %v, want %v", got.GetCapabilitiesEnabled(), enabled)
	}
}

// TestLLDP_OversizedCapabilityBitmapKeepsFacts covers a neighbor
// reporting an absurdly wide capability bitmap. A column decode error
// ends the lldpRemTable walk, which is fatal, so declining the bitmap
// would cost the device every fact it has over one malformed field.
func TestLLDP_OversizedCapabilityBitmapKeepsFacts(t *testing.T) {
	wide := make([]byte, snmp.MaxBitSetOctets+8)
	wide[0] = 0x20 // bridge, inside the bound

	vbs := append(macRemRow(), octetsAt(colOID(lldpRemEntry, 11, 1000, 3, 1), wide))

	got := oneNeighbor(t, vbs, map[uint32]string{3: "eth0"})

	want := []lldpv1.SystemCapability{lldpv1.SystemCapability_SYSTEM_CAPABILITY_BRIDGE}
	if !slices.Equal(got.GetCapabilitiesSupported(), want) {
		t.Errorf("got supported %v, want %v", got.GetCapabilitiesSupported(), want)
	}
}

// TestLLDP_UnresolvedLocalPortKeepsNeighbor covers a device whose LLDP
// port numbering the caller could not resolve: the announcement is real
// and the port number identifies it, so the row is kept.
func TestLLDP_UnresolvedLocalPortKeepsNeighbor(t *testing.T) {
	got := oneNeighbor(t, macRemRow(), nil)

	if got.GetLocalInterfaceName() != "3" {
		t.Errorf("got local interface %q, want the bare port number", got.GetLocalInterfaceName())
	}
}

// TestLLDP_MalformedIndexDeclines feeds index suffixes the MIB's INDEX
// clause cannot produce. Each row is skipped; nothing panics and the
// well-formed row still maps.
func TestLLDP_MalformedIndexDeclines(t *testing.T) {
	vbs := macRemRow()
	// A remote row two arcs short of (timeMark, localPortNum, remIndex).
	vbs = append(vbs, remRow([]uint32{7}, 4, []byte{0x01}, 5, []byte("x"))...)
	// A management address whose index claims four octets and carries two.
	vbs = append(vbs, intAt(colOID(lldpRemManAddrEntry, 4, 1000, 3, 1, 1, 4, 192, 0), 1))
	// A management address index that stops before the length arc.
	vbs = append(vbs, intAt(colOID(lldpRemManAddrEntry, 4, 1000, 3, 1, 1), 1))

	got := oneNeighbor(t, vbs, map[uint32]string{3: "eth0"})

	if len(got.GetManagementAddresses()) != 0 {
		t.Errorf("got %d management addresses, want none: every index was malformed", len(got.GetManagementAddresses()))
	}
}

// TestLLDP_IncompleteNeighborReported holds a row that cannot make a
// valid message: the mandatory identifiers are what a Neighbor is keyed
// on, so the row is reported through the error while the usable rows
// still return.
func TestLLDP_IncompleteNeighborReported(t *testing.T) {
	vbs := macRemRow()
	vbs = append(vbs, intAt(colOID(lldpRemEntry, 4, 2000, 4, 1), 4)) // chassis subtype alone

	facts, err := snmpmap.LLDP(context.Background(), &fakeSession{vbs: vbs}, map[uint32]string{3: "eth0"})
	if err == nil {
		t.Fatal("got no error, want the unusable row reported")
	}

	incomplete := errs.New().Code(snmpmap.ErrCodeLLDPNeighborIncomplete).Msg("incomplete")
	if !errors.Is(err, incomplete) {
		t.Errorf("got %v, want an incomplete-neighbor code", err)
	}

	if len(facts.Neighbors) != 1 {
		t.Fatalf("got %d neighbors, want the one usable row", len(facts.Neighbors))
	}

	validateFacts(t, facts)
}

// TestLLDP_FailedEnrichingWalksKeepFacts covers the two LLDP tables that
// only enrich rows another table already named: losing one costs those
// rows some facts, not their existence.
func TestLLDP_FailedEnrichingWalksKeepFacts(t *testing.T) {
	tests := []struct {
		name string
		root snmp.OID
	}{
		{"lldpLocPortTable", snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 7)},
		{"lldpLocManAddrTable", snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 8)},
		{"lldpRemManAddrTable", snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 4, 2)},
	}

	vbs := append(macRemRow(),
		intAt(colOID(lldpPortConfigEntry, 2, 3), 3), // txAndRx
		octetsAt(colOID(lldpLocPortEntry, 4, 3), []byte("uplink")),
		intAt(colOID(lldpRemManAddrEntry, 4, 1000, 3, 1, 1, 4, 198, 51, 100, 7), 1),
	)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facts, err := snmpmap.LLDP(context.Background(), walkFailure(vbs, tc.root), map[uint32]string{3: "eth0"})
			if err == nil {
				t.Fatalf("got no error, want the failed %s walk reported", tc.name)
			}

			walkErr := errs.New().Code(snmpmap.ErrCodeLLDPWalk).Msg("walk")
			if !errors.Is(err, walkErr) {
				t.Errorf("got %v, want an LLDP-walk code", err)
			}

			if len(facts.Ports) != 1 {
				t.Errorf("got %d ports, want the lldpPortConfigTable row", len(facts.Ports))
			}

			if len(facts.Neighbors) != 1 {
				t.Fatalf("got %d neighbors, want the lldpRemTable row", len(facts.Neighbors))
			}

			validateFacts(t, facts)
		})
	}
}

// TestLLDP_PartialLocalManAddrWalkKeepsItsOwnRows covers what a walk that
// answers nothing cannot show: the addresses lldpLocManAddrTable did
// deliver reach the local system, and the announcement survives with them.
func TestLLDP_PartialLocalManAddrWalkKeepsItsOwnRows(t *testing.T) {
	vbs := append(macRemRow(),
		octetsAt(lldpLocSysNameOID, []byte("core-sw-1")),
		intAt(colOID(lldpLocManAddrEntry, 5, 1, 4, 198, 51, 100, 7), 1),
		intAt(colOID(lldpLocManAddrEntry, 5, 1, 4, 198, 51, 100, 8), 1),
	)

	sess := walkFailureAfter(vbs, snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 8), 1)

	facts, err := snmpmap.LLDP(context.Background(), sess, map[uint32]string{3: "eth0"})
	if err == nil {
		t.Fatal("got no error, want the failed lldpLocManAddrTable walk reported")
	}

	if len(facts.Neighbors) != 1 {
		t.Errorf("got %d neighbors, want the lldpRemTable row", len(facts.Neighbors))
	}

	local := facts.LocalSystem
	if local == nil {
		t.Fatal("got no local system, want the scalars and the address read before the failure")
	}

	if len(local.GetManagementAddresses()) != 1 {
		t.Fatalf("got %d management addresses, want only the one the walk delivered",
			len(local.GetManagementAddresses()))
	}

	want := []byte{198, 51, 100, 7}
	if got := local.GetManagementAddresses()[0].GetIp().GetV4().GetOctets(); !bytes.Equal(got, want) {
		t.Errorf("got %x, want %x", got, want)
	}

	validateFacts(t, facts)
}

// TestLLDP_UnusableRowsSurviveLaterFailure keeps the neighbor rows that
// could not be mapped visible when a later walk fails too: an operator
// who is told only about the walk never learns the rows were unusable.
func TestLLDP_UnusableRowsSurviveLaterFailure(t *testing.T) {
	vbs := append(macRemRow(), intAt(colOID(lldpRemEntry, 4, 2000, 4, 1), 4)) // chassis subtype alone

	// The local management addresses are read after the neighbors.
	sess := walkFailure(vbs, snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 3, 8))

	facts, err := snmpmap.LLDP(context.Background(), sess, map[uint32]string{3: "eth0"})
	if err == nil {
		t.Fatal("got no error, want both failures reported")
	}

	if len(facts.Neighbors) != 1 {
		t.Errorf("got %d neighbors, want the usable row kept across the later failure", len(facts.Neighbors))
	}

	incomplete := errs.New().Code(snmpmap.ErrCodeLLDPNeighborIncomplete).Msg("incomplete")
	if !errors.Is(err, incomplete) {
		t.Errorf("got %v, want the incomplete-neighbor code alongside the walk failure", err)
	}

	walkErr := errs.New().Code(snmpmap.ErrCodeLLDPWalk).Msg("walk")
	if !errors.Is(err, walkErr) {
		t.Errorf("got %v, want an LLDP-walk code", err)
	}
}

// TestLLDP_PortSettingsTlvBitmap covers the translation this mapper owns:
// lldpPortConfigTLVsTxEnable is a bitmap of MIB bit positions, while the
// schema speaks 802.1AB TLV type numbers.
func TestLLDP_PortSettingsTlvBitmap(t *testing.T) {
	vbs := []vbFixture{
		intAt(colOID(lldpPortConfigEntry, 2, 3), 3), // txAndRx
		intAt(colOID(lldpPortConfigEntry, 3, 3), 1), // notifications enabled
		octetsAt(colOID(lldpPortConfigEntry, 4, 3), bitsOctets(0, 1, 3)),
		intAt(colOID(lldpLocPortEntry, 2, 3), 5), // interfaceName
		octetsAt(colOID(lldpLocPortEntry, 3, 3), []byte("Gi0/3")),
		octetsAt(colOID(lldpLocPortEntry, 4, 3), []byte("uplink")),
	}

	facts := lldpFacts(t, vbs, map[uint32]string{3: "GigabitEthernet0/3"})
	if len(facts.Ports) != 1 {
		t.Fatalf("got %d ports, want 1", len(facts.Ports))
	}

	port := facts.Ports[0]

	want := []lldpv1.TlvType{
		lldpv1.TlvType_TLV_TYPE_PORT_DESCRIPTION,
		lldpv1.TlvType_TLV_TYPE_SYSTEM_NAME,
		lldpv1.TlvType_TLV_TYPE_SYSTEM_CAPABILITIES,
	}
	if !slices.Equal(port.GetTransmittedTlvs(), want) {
		t.Errorf("got TLVs %v, want %v", port.GetTransmittedTlvs(), want)
	}

	if port.GetInterfaceName() != "GigabitEthernet0/3" {
		t.Errorf("got interface %q, want the resolved name", port.GetInterfaceName())
	}

	if port.GetAdminStatus() != lldpv1.PortAdminStatus_PORT_ADMIN_STATUS_TX_AND_RX {
		t.Errorf("got admin status %v, want txAndRx", port.GetAdminStatus())
	}

	if !port.GetNotificationsEnabled() {
		t.Error("got notifications disabled, want the reported enablement")
	}

	if port.GetPortId().GetSubtype() != lldpv1.PortIdSubtype_PORT_ID_SUBTYPE_INTERFACE_NAME {
		t.Errorf("got port-id subtype %v, want interfaceName", port.GetPortId().GetSubtype())
	}

	if port.GetPortDescription() != "uplink" {
		t.Errorf("got port description %q, want %q", port.GetPortDescription(), "uplink")
	}
}

// TestLLDP_TlvBitmapIgnoresReservedPositions covers a bitmap position
// past the four LLDP-MIB names. The MIB reserves none of them — bit 4 is
// explicitly not the management-address TLV — so a set position there
// names no TLV and may not be extrapolated into one.
func TestLLDP_TlvBitmapIgnoresReservedPositions(t *testing.T) {
	vbs := []vbFixture{
		intAt(colOID(lldpPortConfigEntry, 2, 3), 3), // txAndRx
		octetsAt(colOID(lldpPortConfigEntry, 4, 3), bitsOctets(2, 4, 20)),
	}

	facts := lldpFacts(t, vbs, nil)
	if len(facts.Ports) != 1 {
		t.Fatalf("got %d ports, want 1", len(facts.Ports))
	}

	want := []lldpv1.TlvType{lldpv1.TlvType_TLV_TYPE_SYSTEM_DESCRIPTION}
	if got := facts.Ports[0].GetTransmittedTlvs(); !slices.Equal(got, want) {
		t.Errorf("got TLVs %v, want %v: bits 4 and 20 name no TLV in this bitmap", got, want)
	}
}

// TestLLDP_PortSettingsUnreportedColumnsStayAbsent keeps the presence
// distinction U7 landed: a column the agent never answered leaves its
// field absent rather than reading as a configured zero.
func TestLLDP_PortSettingsUnreportedColumnsStayAbsent(t *testing.T) {
	vbs := []vbFixture{intAt(colOID(lldpPortConfigEntry, 2, 9), 4)} // disabled

	facts := lldpFacts(t, vbs, nil)
	if len(facts.Ports) != 1 {
		t.Fatalf("got %d ports, want 1", len(facts.Ports))
	}

	port := facts.Ports[0]

	if port.GetInterfaceName() != "9" {
		t.Errorf("got interface %q, want the bare port number", port.GetInterfaceName())
	}

	if port.HasNotificationsEnabled() {
		t.Error("got a notification setting the agent never reported, want absent")
	}

	if len(port.GetTransmittedTlvs()) != 0 {
		t.Errorf("got TLVs %v, want none: the bitmap was never reported", port.GetTransmittedTlvs())
	}

	if port.HasPortId() {
		t.Error("got a port identifier with no lldpLocPortTable row, want absent")
	}
}

// TestLLDP_LocalSystem covers the scalars and the local management
// address table, whose address also lives in its index arcs.
func TestLLDP_LocalSystem(t *testing.T) {
	vbs := []vbFixture{
		intAt(lldpLocChassisIDSubtypeOID, 4),
		octetsAt(lldpLocChassisIDOID, []byte{0x00, 0x1b, 0x21, 0x01, 0x02, 0x03}),
		octetsAt(lldpLocSysNameOID, []byte("core-sw-1")),
		octetsAt(lldpLocSysDescOID, []byte("FlowSeer test switch")),
		octetsAt(lldpLocSysCapSupportedOID, bitsOctets(2, 4)),
		octetsAt(lldpLocSysCapEnabledOID, bitsOctets(2)),
		intAt(colOID(lldpLocManAddrEntry, 5, 1, 4, 198, 51, 100, 7), 1),
	}

	local := lldpFacts(t, vbs, nil).LocalSystem
	if local == nil {
		t.Fatal("got no local system, want the reported scalars")
	}

	if local.GetSystemName() != "core-sw-1" {
		t.Errorf("got system name %q, want %q", local.GetSystemName(), "core-sw-1")
	}

	if local.GetSystemDescription() != "FlowSeer test switch" {
		t.Errorf("got system description %q, want the reported one", local.GetSystemDescription())
	}

	if local.GetChassisId().GetSubtype() != lldpv1.ChassisIdSubtype_CHASSIS_ID_SUBTYPE_MAC_ADDRESS {
		t.Errorf("got chassis subtype %v, want macAddress", local.GetChassisId().GetSubtype())
	}

	want := []lldpv1.SystemCapability{
		lldpv1.SystemCapability_SYSTEM_CAPABILITY_BRIDGE,
		lldpv1.SystemCapability_SYSTEM_CAPABILITY_ROUTER,
	}
	if !slices.Equal(local.GetCapabilitiesSupported(), want) {
		t.Errorf("got supported %v, want %v", local.GetCapabilitiesSupported(), want)
	}

	if len(local.GetManagementAddresses()) != 1 {
		t.Fatalf("got %d management addresses, want 1", len(local.GetManagementAddresses()))
	}

	addr := local.GetManagementAddresses()[0]
	if want := []byte{198, 51, 100, 7}; !bytes.Equal(addr.GetIp().GetV4().GetOctets(), want) {
		t.Errorf("got %x, want %x", addr.GetIp().GetV4().GetOctets(), want)
	}
}

// TestLLDP_NoLocalSystemReported covers a device that answers none of the
// local scalars: the block is absent rather than an empty message.
func TestLLDP_NoLocalSystemReported(t *testing.T) {
	if local := lldpFacts(t, macRemRow(), nil).LocalSystem; local != nil {
		t.Errorf("got local system %v, want absent", local)
	}
}
