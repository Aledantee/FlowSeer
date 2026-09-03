package snmpmap_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"buf.build/go/protovalidate"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/snmp"
	"go.aledante.io/FlowSeer/src/common/snmpmap"
)

// ifRow builds the common ifTable columns of one row: ifIndex, ifDescr,
// ifType, ifMtu, ifAdminStatus and ifOperStatus.
func ifRow(idx uint32, descr string, ifType int32) []vbFixture {
	return []vbFixture{
		integerVar(ifEntry, 1, idx, int32(idx)),
		stringVar(ifEntry, 2, idx, []byte(descr)),
		integerVar(ifEntry, 3, idx, ifType),
		integerVar(ifEntry, 4, idx, 1500),
		integerVar(ifEntry, 7, idx, 1),
		integerVar(ifEntry, 8, idx, 1),
	}
}

// failingWalkSession serves a bounded fixture prefix one repetition at a time,
// then fails its populated cursors. Empty columns end normally so the merge can
// establish complete rows before the later request failure.
type failingWalkSession struct {
	*fakeSession
	root   snmp.OID
	prefix int
	err    error
}

func (s *failingWalkSession) GetBulk(ctx context.Context, nr, reps uint8, oids []snmp.OID, opts ...snmp.CallOption) ([]snmp.VarBind, error) {
	if len(oids) == 0 || !oids[0].HasPrefix(s.root) {
		return s.fakeSession.GetBulk(ctx, nr, reps, oids, opts...)
	}
	if s.prefix == 0 {
		return nil, s.err
	}
	fixtures := s.subtree(s.root)
	fixtures = fixtures[:min(s.prefix, len(fixtures))]
	var out []snmp.VarBind
	for _, o := range oids {
		var next *vbFixture
		populated := false
		for i := range fixtures {
			f := &fixtures[i]
			// The column is the root's entry child. Index arcs may be composite.
			columnLen := s.root.Len() + 2
			column := snmp.OID{}
			for j := 0; j < min(columnLen, o.Len()); j++ {
				column = column.Append(o.At(j))
			}
			if !f.oid.HasPrefix(column) {
				continue
			}
			populated = true
			if f.oid.Compare(o) > 0 {
				next = f
				break
			}
		}
		switch {
		case next != nil:
			out = append(out, next.vb)
		case populated:
			return nil, s.err
		default:
			out = append(out, snmp.EndOfMibViewVar{Header: snmp.Header{OID: o, Kind: snmp.KindEndOfMibView}})
		}
	}
	return out, nil
}

// walkFailure builds the session a degraded-walk test runs against, whose
// failing walk answers nothing before it dies.
func walkFailure(vbs []vbFixture, root snmp.OID) *failingWalkSession {
	return walkFailureAfter(vbs, root, 0)
}

// walkFailureAfter is [walkFailure] for the agent that dies partway: the
// failing walk delivers prefix varbinds of its subtree first.
func walkFailureAfter(vbs []vbFixture, root snmp.OID, prefix int) *failingWalkSession {
	return &failingWalkSession{
		fakeSession: &fakeSession{vbs: vbs},
		root:        root,
		prefix:      prefix,
		err:         errs.Msg("agent stopped answering"),
	}
}

// mapOne runs the mapper over vbs and requires exactly one interface back.
func mapOne(t *testing.T, vbs []vbFixture) *interfacev1.Interface {
	t.Helper()

	ifaces := mapAll(t, vbs)
	if len(ifaces) != 1 {
		t.Fatalf("got %d interfaces, want 1", len(ifaces))
	}

	return ifaces[0]
}

// mapAll runs the mapper over vbs, requires no error, and holds every
// mapped interface to its own schema rules — the mapper may not emit a
// message the model would reject.
func mapAll(t *testing.T, vbs []vbFixture) []*interfacev1.Interface {
	t.Helper()

	ifaces, err := snmpmap.Interfaces(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Interfaces: %v", err)
	}

	for _, iface := range ifaces {
		if err := protovalidate.Validate(iface); err != nil {
			t.Fatalf("mapped interface %q violates its schema: %v", iface.GetName(), err)
		}
	}

	return ifaces
}

// TestInterfaces_UnknownIfTypeKeepsRow covers AE1: an ifType with no
// dedicated arm still produces an interface, carrying the raw type.
func TestInterfaces_UnknownIfTypeKeepsRow(t *testing.T) {
	vbs := ifRow(7, "wlan0", 71) // ieee80211
	vbs = append(vbs, stringVar(ifEntry, 6, 7, []byte{0x00, 0x1b, 0x21, 0x0a, 0x0b, 0x0c}))

	got := mapOne(t, vbs)

	if !got.HasOther() {
		t.Fatalf("got kind %v, want the other arm", got.WhichKind())
	}

	if want := uint32(71); got.GetOther().GetIfType() != want {
		t.Errorf("got ifType %d, want %d", got.GetOther().GetIfType(), want)
	}

	if got.GetName() != "wlan0" {
		t.Errorf("got name %q, want %q", got.GetName(), "wlan0")
	}

	if got.GetIfIndex() != 7 {
		t.Errorf("got ifIndex %d, want 7", got.GetIfIndex())
	}

	if got.GetAdminStatus() != interfacev1.AdminStatus_ADMIN_STATUS_UP {
		t.Errorf("got adminStatus %v, want up", got.GetAdminStatus())
	}

	if got.GetOperStatus() != interfacev1.OperStatus_OPER_STATUS_UP {
		t.Errorf("got operStatus %v, want up", got.GetOperStatus())
	}

	if got.GetMtu() != 1500 {
		t.Errorf("got mtu %d, want 1500", got.GetMtu())
	}

	if !got.GetMac().HasEui48() {
		t.Error("got no EUI-48 mac, want the reported address")
	}
}

func TestInterfaces_KindDispatch(t *testing.T) {
	tests := []struct {
		name   string
		descr  string
		ifType int32
		want   func(*interfacev1.Interface) bool
	}{
		{"physical", "GigabitEthernet0/1", 6, (*interfacev1.Interface).HasPhysical},
		{"lag", "Port-channel1", 161, (*interfacev1.Interface).HasLag},
		{"vlan", "Vlan20", 135, (*interfacev1.Interface).HasVlan},
		{"loopback", "Loopback0", 24, (*interfacev1.Interface).HasLoopback},
		{"tunnel", "Tunnel1", 131, (*interfacev1.Interface).HasTunnel},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapOne(t, ifRow(1, tc.descr, tc.ifType))
			if !tc.want(got) {
				t.Errorf("got kind %v, want the %s arm", got.WhichKind(), tc.name)
			}
		})
	}
}

// TestInterfaces_SviAndAccessPort covers AE2.
func TestInterfaces_SviAndAccessPort(t *testing.T) {
	vbs := ifRow(1, "Vlan20", 135)
	vbs = append(vbs, ifRow(2, "GigabitEthernet0/1", 6)...)

	ifaces := mapAll(t, vbs)
	if len(ifaces) != 2 {
		t.Fatalf("got %d interfaces, want 2", len(ifaces))
	}

	svi, port := ifaces[0], ifaces[1]

	if !svi.HasVlan() {
		t.Fatalf("got kind %v for the SVI, want the vlan arm", svi.WhichKind())
	}

	if svi.GetVlan().GetVlanId() != 20 {
		t.Errorf("got vlan id %d, want 20", svi.GetVlan().GetVlanId())
	}

	if !svi.HasIp() {
		t.Error("got no ip facet on the SVI, want one: an SVI is routed by definition")
	}

	if !port.HasPhysical() {
		t.Fatalf("got kind %v for the port, want the physical arm", port.WhichKind())
	}

	if port.HasIp() {
		t.Error("got an ip facet on the switched port, want none")
	}
}

// TestInterfaces_UnparsableVlanNameDeclines keeps a VLAN row whose name
// carries no identifier: the vlan arm requires one, so the row declines
// to the other arm rather than being dropped or erroring.
func TestInterfaces_UnparsableVlanNameDeclines(t *testing.T) {
	got := mapOne(t, ifRow(1, "management vlan", 135))

	if !got.HasOther() {
		t.Fatalf("got kind %v, want the other arm", got.WhichKind())
	}

	if got.GetOther().GetIfType() != 135 {
		t.Errorf("got ifType %d, want 135", got.GetOther().GetIfType())
	}
}

// TestInterfaces_Counters32Bit covers AE4: without ifXTable the counters
// come from ifTable, and a column the device never answered stays absent
// instead of reading as an explicit zero.
func TestInterfaces_Counters32Bit(t *testing.T) {
	vbs := ifRow(2, "eth0", 6)
	vbs = append(vbs,
		counter32Var(ifEntry, 10, 2, 100), // ifInOctets
		counter32Var(ifEntry, 11, 2, 11),  // ifInUcastPkts
		counter32Var(ifEntry, 12, 2, 99),  // ifInNUcastPkts
		counter32Var(ifEntry, 13, 2, 0),   // ifInDiscards, reported as zero
		counter32Var(ifEntry, 14, 2, 14),  // ifInErrors
		counter32Var(ifEntry, 16, 2, 160), // ifOutOctets
		counter32Var(ifEntry, 18, 2, 88),  // ifOutNUcastPkts
	)

	c := mapOne(t, vbs).GetCounters()

	if c.GetInOctets() != 100 {
		t.Errorf("got inOctets %d, want 100", c.GetInOctets())
	}

	if c.GetInUnicastPackets() != 11 {
		t.Errorf("got inUnicastPackets %d, want 11", c.GetInUnicastPackets())
	}

	if !c.HasInDiscards() || c.GetInDiscards() != 0 {
		t.Error("got no inDiscards, want the device's reported zero")
	}

	if c.GetOutOctets() != 160 {
		t.Errorf("got outOctets %d, want 160", c.GetOutOctets())
	}

	// AE4: ifOutUcastPkts, ifOutDiscards and ifOutErrors were never
	// answered, so they must stay absent rather than read as zero.
	for _, unreported := range []struct {
		name string
		has  bool
	}{
		{"outUnicastPackets", c.HasOutUnicastPackets()},
		{"outDiscards", c.HasOutDiscards()},
		{"outErrors", c.HasOutErrors()},
	} {
		if unreported.has {
			t.Errorf("got %s present, want absent: the device never reported it", unreported.name)
		}
	}

	// Multicast and broadcast live only in ifXTable and are never
	// derived from the combined non-unicast counts above.
	for _, derived := range []struct {
		name string
		has  bool
	}{
		{"inMulticastPackets", c.HasInMulticastPackets()},
		{"inBroadcastPackets", c.HasInBroadcastPackets()},
		{"outMulticastPackets", c.HasOutMulticastPackets()},
		{"outBroadcastPackets", c.HasOutBroadcastPackets()},
	} {
		if derived.has {
			t.Errorf("got %s present, want absent: no ifXTable row exists", derived.name)
		}
	}
}

func TestInterfaces_HighCapacityCountersWin(t *testing.T) {
	vbs := ifRow(1, "eth0", 6)
	vbs = append(vbs,
		counter32Var(ifEntry, 10, 1, 100),
		counter32Var(ifEntry, 16, 1, 160),
		stringVar(ifXEntry, 1, 1, []byte("Ethernet1/1")),
		counter64Var(ifXEntry, 6, 1, 1<<40),  // ifHCInOctets
		counter64Var(ifXEntry, 10, 1, 1<<41), // ifHCOutOctets
		counter32Var(ifXEntry, 2, 1, 7),      // ifInMulticastPkts
	)

	got := mapOne(t, vbs)

	if got.GetName() != "Ethernet1/1" {
		t.Errorf("got name %q, want the ifXTable name %q", got.GetName(), "Ethernet1/1")
	}

	c := got.GetCounters()

	if c.GetInOctets() != 1<<40 {
		t.Errorf("got inOctets %d, want the high-capacity %d", c.GetInOctets(), uint64(1)<<40)
	}

	if c.GetOutOctets() != 1<<41 {
		t.Errorf("got outOctets %d, want the high-capacity %d", c.GetOutOctets(), uint64(1)<<41)
	}

	if c.GetInMulticastPackets() != 7 {
		t.Errorf("got inMulticastPackets %d, want 7", c.GetInMulticastPackets())
	}
}

func TestInterfaces_PhysAddress(t *testing.T) {
	tests := []struct {
		name  string
		bytes []byte
		want  []byte
	}{
		{"eui48", []byte{0x00, 0x1b, 0x21, 0x0a, 0x0b, 0x0c}, []byte{0x00, 0x1b, 0x21, 0x0a, 0x0b, 0x0c}},
		{"empty", []byte{}, nil},
		{"unusable length", []byte{0x00, 0x1b}, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vbs := append(ifRow(1, "eth0", 6), stringVar(ifEntry, 6, 1, tc.bytes))

			got := mapOne(t, vbs)

			if tc.want == nil {
				if got.HasMac() {
					t.Errorf("got mac %v, want absent", got.GetMac())
				}

				return
			}

			if !got.GetMac().HasEui48() {
				t.Fatalf("got mac %v, want an EUI-48", got.GetMac())
			}

			if octets := got.GetMac().GetEui48().GetOctets(); !bytes.Equal(octets, tc.want) {
				t.Errorf("got octets %x, want %x", octets, tc.want)
			}
		})
	}
}

// TestInterfaces_StackRelationships checks both ifStackTable readings: a
// row over a single lower interface with no arm of its own becomes a
// subinterface, and a physical port under an aggregate names its LAG.
func TestInterfaces_StackRelationships(t *testing.T) {
	vbs := ifRow(1, "GigabitEthernet0/1", 6)
	vbs = append(vbs, ifRow(2, "GigabitEthernet0/1.100", 53)...) // propVirtual
	vbs = append(vbs, ifRow(3, "Port-channel1", 161)...)
	vbs = append(vbs, ifRow(4, "GigabitEthernet0/2", 6)...)
	vbs = append(vbs, stackVar(2, 1), stackVar(3, 4), stackVar(1, 0), stackVar(0, 3))

	ifaces := mapAll(t, vbs)
	if len(ifaces) != 4 {
		t.Fatalf("got %d interfaces, want 4", len(ifaces))
	}

	sub := ifaces[1]
	if !sub.HasSub() {
		t.Fatalf("got kind %v, want the sub arm", sub.WhichKind())
	}

	if parent := sub.GetSub().GetParent(); parent != "GigabitEthernet0/1" {
		t.Errorf("got parent %q, want %q", parent, "GigabitEthernet0/1")
	}

	member := ifaces[3]
	if !member.HasPhysical() {
		t.Fatalf("got kind %v, want the physical arm", member.WhichKind())
	}

	if lag := member.GetPhysical().GetLagParent(); lag != "Port-channel1" {
		t.Errorf("got lagParent %q, want %q", lag, "Port-channel1")
	}

	standalone := ifaces[0]
	if standalone.GetPhysical().HasLagParent() {
		t.Errorf("got lagParent %q on a standalone port, want absent", standalone.GetPhysical().GetLagParent())
	}
}

// TestInterfaces_UnusableRowsReported checks the contract around a row
// that cannot become a valid message: it is reported, and the rows
// around it still map.
func TestInterfaces_UnusableRowsReported(t *testing.T) {
	vbs := []vbFixture{
		integerVar(ifEntry, 3, 1, 6), // ifType alone: no name
		stringVar(ifEntry, 2, 2, []byte("eth1")),
		stringVar(ifEntry, 2, 3, []byte("eth2")), // name alone: no ifType
		integerVar(ifEntry, 3, 3, 6),
	}

	ifaces, err := snmpmap.Interfaces(context.Background(), &fakeSession{vbs: vbs})
	if err == nil {
		t.Fatal("got no error, want the unusable rows reported")
	}

	unnamed := errs.New().Code(snmpmap.ErrCodeInterfaceUnnamed).Msg("unnamed")
	if !errors.Is(err, unnamed) {
		t.Errorf("got %v, want an unnamed-interface code", err)
	}

	untyped := errs.New().Code(snmpmap.ErrCodeInterfaceUntyped).Msg("untyped")
	if !errors.Is(err, untyped) {
		t.Errorf("got %v, want an untyped-interface code", err)
	}

	if len(ifaces) != 1 || ifaces[0].GetName() != "eth2" {
		t.Fatalf("got %d interfaces, want only the usable one", len(ifaces))
	}
}

// TestInterfaces_FailedIfXWalkKeepsInterfaces holds the degraded path the
// package promises: ifXTable only enriches rows ifTable already carried,
// so a device whose ifXTable walk dies still yields its interfaces, with
// the failure reported beside them.
func TestInterfaces_FailedIfXWalkKeepsInterfaces(t *testing.T) {
	vbs := append(ifRow(1, "eth0", 6), ifRow(2, "eth1", 6)...)

	sess := walkFailure(vbs, snmp.MustOID(1, 3, 6, 1, 2, 1, 31, 1, 1))

	ifaces, err := snmpmap.Interfaces(context.Background(), sess)
	if err == nil {
		t.Fatal("got no error, want the failed ifXTable walk reported")
	}

	walkErr := errs.New().Code(snmpmap.ErrCodeInterfaceWalk).Msg("walk")
	if !errors.Is(err, walkErr) {
		t.Errorf("got %v, want an interface-walk code", err)
	}

	if len(ifaces) != 2 {
		t.Fatalf("got %d interfaces, want both ifTable rows", len(ifaces))
	}

	if ifaces[0].GetName() != "eth0" || ifaces[1].GetName() != "eth1" {
		t.Errorf("got names %q and %q, want the ifDescr fallbacks", ifaces[0].GetName(), ifaces[1].GetName())
	}
}

// TestInterfaces_PartialIfXWalkKeepsItsOwnRows covers the half of the
// degraded path a walk that answers nothing cannot reach: the rows the
// dying walk did deliver must enrich their interfaces, while the ones it
// never reached fall back as if the table were absent.
func TestInterfaces_PartialIfXWalkKeepsItsOwnRows(t *testing.T) {
	vbs := append(ifRow(1, "eth0", 6), ifRow(2, "eth1", 6)...)
	vbs = append(vbs, ifRow(3, "eth2", 6)...)
	vbs = append(vbs,
		stringVar(ifXEntry, 1, 1, []byte("Gi0/1")),
		stringVar(ifXEntry, 1, 2, []byte("Gi0/2")),
		stringVar(ifXEntry, 1, 3, []byte("Gi0/3")),
	)

	// The fake answers a walk in OID order, so two varbinds are ifName for
	// the first two rows and nothing for the third.
	sess := walkFailureAfter(vbs, snmp.MustOID(1, 3, 6, 1, 2, 1, 31, 1, 1), 2)

	ifaces, err := snmpmap.Interfaces(context.Background(), sess)
	if err == nil {
		t.Fatal("got no error, want the failed ifXTable walk reported")
	}

	if len(ifaces) != 3 {
		t.Fatalf("got %d interfaces, want all three ifTable rows", len(ifaces))
	}

	if ifaces[0].GetName() != "Gi0/1" || ifaces[1].GetName() != "Gi0/2" {
		t.Errorf("got names %q and %q, want the ifNames the walk delivered before failing",
			ifaces[0].GetName(), ifaces[1].GetName())
	}

	if ifaces[2].GetName() != "eth2" {
		t.Errorf("got name %q, want the ifDescr fallback: its ifName was never delivered", ifaces[2].GetName())
	}
}

// TestInterfaces_PartialIfStackWalkKeepsItsOwnRows is the same check for
// ifStackTable, whose rows are relationships rather than columns.
func TestInterfaces_PartialIfStackWalkKeepsItsOwnRows(t *testing.T) {
	vbs := ifRow(1, "GigabitEthernet0/1", 6)
	vbs = append(vbs, ifRow(2, "GigabitEthernet0/1.100", 53)...) // propVirtual
	vbs = append(vbs, ifRow(3, "Port-channel1", 161)...)
	vbs = append(vbs, ifRow(4, "GigabitEthernet0/2", 6)...)
	vbs = append(vbs, stackVar(2, 1), stackVar(3, 4))

	sess := walkFailureAfter(vbs, snmp.MustOID(1, 3, 6, 1, 2, 1, 31, 1, 2), 1)

	ifaces, err := snmpmap.Interfaces(context.Background(), sess)
	if err == nil {
		t.Fatal("got no error, want the failed ifStackTable walk reported")
	}

	if len(ifaces) != 4 {
		t.Fatalf("got %d interfaces, want all four ifTable rows", len(ifaces))
	}

	if parent := ifaces[1].GetSub().GetParent(); parent != "GigabitEthernet0/1" {
		t.Errorf("got parent %q, want the relationship the walk delivered before failing", parent)
	}

	if ifaces[3].GetPhysical().HasLagParent() {
		t.Errorf("got lagParent %q, want absent: that relationship was never delivered",
			ifaces[3].GetPhysical().GetLagParent())
	}
}

// TestInterfaces_FailedIfTableWalkYieldsNothing pins the other half of
// that judgment: ifTable is what an interface is, so its failure leaves
// nothing to return.
func TestInterfaces_FailedIfTableWalkYieldsNothing(t *testing.T) {
	sess := walkFailure(ifRow(1, "eth0", 6), snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2))

	ifaces, err := snmpmap.Interfaces(context.Background(), sess)
	if err == nil {
		t.Fatal("got no error, want the failed ifTable walk reported")
	}

	if len(ifaces) != 0 {
		t.Errorf("got %d interfaces, want none", len(ifaces))
	}
}

// TestInterfaces_NoCountersReported checks that a device reporting no
// counter at all leaves the whole counters message absent rather than
// carrying twelve zeros.
func TestInterfaces_NoCountersReported(t *testing.T) {
	if got := mapOne(t, ifRow(1, "eth0", 6)); got.HasCounters() {
		t.Errorf("got counters %v, want absent", got.GetCounters())
	}
}

func TestInterfaces_Description(t *testing.T) {
	vbs := append(ifRow(1, "eth0", 6), stringVar(ifXEntry, 18, 1, []byte("uplink to core")))

	got := mapOne(t, vbs)

	if got.GetDescription() != "uplink to core" {
		t.Errorf("got description %q, want the ifAlias", got.GetDescription())
	}

	if plain := mapOne(t, ifRow(1, "eth0", 6)); plain.HasDescription() {
		t.Errorf("got description %q with no ifAlias reported, want absent", plain.GetDescription())
	}
}
