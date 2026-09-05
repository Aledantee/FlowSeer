package snmpmap_test

import (
	"context"
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/generated/go/mib/etherlikemib"
	"go.aledante.io/FlowSeer/generated/go/mib/maumib"
	"go.aledante.io/FlowSeer/generated/go/mib/powerethernetmib"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/collect"
	"go.aledante.io/FlowSeer/src/modules/localnet/snmpmap"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// dot3MauType is the IANA-MAU-MIB registration arc a standard MAU type
// hangs under, and ifMauEntry the row root of ifMauTable.
var (
	dot3MauType = snmp.MustOID(1, 3, 6, 1, 2, 1, 26, 4)
	ifMauEntry  = snmp.MustOID(1, 3, 6, 1, 2, 1, 26, 2, 1, 1)
)

// ianaMauType is the object identifier of the registered MAU type n.
func ianaMauType(n uint32) snmp.OID { return dot3MauType.Append(n) }

// etherLikePort is the EtherLike-MIB fixture of one interface with 32-bit
// counters only, as a switch that implements no dot3HCStatsTable reports.
func etherLikePort(ifIndex uint32) []vbFixture {
	return []vbFixture{
		counter32At(etherlikemib.Dot3StatsAlignmentErrors, 3, ifIndex),
		counter32At(etherlikemib.Dot3StatsFCSErrors, 7, ifIndex),
		counter32At(etherlikemib.Dot3StatsSingleCollisionFrames, 11, ifIndex),
		counter32At(etherlikemib.Dot3StatsLateCollisions, 0, ifIndex),
		integerAt(etherlikemib.Dot3StatsDuplexStatus, int32(etherlikemib.Dot3StatsDuplexStatusValueFullDuplex), ifIndex),
	}
}

// physicalIface is an ifIndex-bearing physical interface for the join
// tests.
func physicalIface(name string, ifIndex uint32) *interfacev1.Interface {
	iface := &interfacev1.Interface{}
	iface.SetName(name)
	iface.SetIfIndex(ifIndex)
	iface.SetPhysical(&interfacev1.PhysicalInterface{})

	return iface
}

func mustValid(t *testing.T, m proto.Message) {
	t.Helper()

	if err := protovalidate.Validate(m); err != nil {
		t.Fatalf("message is invalid: %v", err)
	}
}

func TestPhysical_EtherLikeWithoutMau(t *testing.T) {
	// A switch that implements EtherLike-MIB but not MAU-MIB.
	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: etherLikePort(3)})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	facet := facts.Facets[3]
	if facet == nil {
		t.Fatal("no facet for ifIndex 3")
	}

	mustValid(t, facet)

	if got := facet.GetCounters().GetFcsErrors(); got != 7 {
		t.Errorf("fcs_errors = %d, want 7", got)
	}

	if !facet.GetCounters().HasLateCollisions() || facet.GetCounters().GetLateCollisions() != 0 {
		t.Error("explicit zero late_collisions is absent")
	}

	if facet.GetCounters().HasSymbolErrors() {
		t.Error("unreported symbol_errors is present")
	}

	if facet.GetActiveDuplex() != phyv1.EthernetDuplex_ETHERNET_DUPLEX_FULL {
		t.Errorf("active_duplex = %v, want FULL", facet.GetActiveDuplex())
	}

	if facet.HasMauType() || facet.HasTransport() || len(facet.GetAdvertisedLinkModes()) != 0 {
		t.Error("MAU facts are present without MAU-MIB rows")
	}

	if len(facts.PoePorts) != 0 || len(facts.Budgets) != 0 {
		t.Error("PoE facts are present without POWER-ETHERNET-MIB rows")
	}
}

func TestPhysical_HighCapacityCountersWin(t *testing.T) {
	vbs := append(etherLikePort(7),
		counter64At(etherlikemib.Dot3HCStatsFCSErrors, 5_000_000_000, 7),
		counter64At(etherlikemib.Dot3HCStatsSymbolErrors, 9, 7),
	)

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	counters := facts.Facets[7].GetCounters()

	if got := counters.GetFcsErrors(); got != 5_000_000_000 {
		t.Errorf("fcs_errors = %d, want the 64-bit reading", got)
	}

	if got := counters.GetAlignmentErrors(); got != 3 {
		t.Errorf("alignment_errors = %d, want the 32-bit fallback", got)
	}

	if got := counters.GetSymbolErrors(); got != 9 {
		t.Errorf("symbol_errors = %d, want the 64-bit-only reading", got)
	}
}

func TestPhysical_MauType(t *testing.T) {
	// Registered, unregistered, and unknown
	// types all survive.
	vbs := []vbFixture{
		objectIDAt(maumib.IfMauType, ianaMauType(30), 1, 1),
		objectIDAt(maumib.IfMauType, snmp.MustOID(1, 3, 6, 1, 4, 1, 9, 99), 2, 1),
		objectIDAt(maumib.IfMauType, snmp.MustOID(0, 0), 3, 1),
		objectIDAt(maumib.IfMauType, ianaMauType(26), 4, 1),
		objectIDAt(maumib.IfMauType, ianaMauType(58), 5, 1),
		objectIDAt(maumib.IfMauType, ianaMauType(33), 6, 1),
	}

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	for idx, facet := range facts.Facets {
		mustValid(t, facet)

		t.Logf("ifIndex %d: %v", idx, facet)
	}

	if got := facts.Facets[1].GetMauType().GetIana(); got != 30 {
		t.Errorf("registered type iana = %d, want 30", got)
	}

	if !facts.Facets[1].HasCopper() {
		t.Error("1000BASE-T did not select the copper arm")
	}

	if got := facts.Facets[2].GetMauType().GetOid(); got != "1.3.6.1.4.1.9.99" {
		t.Errorf("unregistered type oid = %q, want the raw identifier", got)
	}

	if facts.Facets[2].HasTransport() {
		t.Error("an unregistered type selected a transport arm")
	}

	if facet, ok := facts.Facets[3]; ok && facet.HasMauType() {
		t.Error("zeroDotZero produced a MAU type")
	}

	if !facts.Facets[4].HasFiber() {
		t.Error("1000BASE-SX did not select the fiber arm")
	}

	if !facts.Facets[5].HasBackplane() {
		t.Error("10GBASE-KR did not select the backplane arm")
	}

	if facts.Facets[6].HasTransport() {
		t.Error("10GBASE-R, a bare PCS, selected a transport arm")
	}
}

func TestPhysical_LowestMauIndexSpeaks(t *testing.T) {
	vbs := []vbFixture{
		objectIDAt(maumib.IfMauType, ianaMauType(26), 7, 2),
		objectIDAt(maumib.IfMauType, ianaMauType(30), 7, 1),
		// A one-arc index is not a MAU row and is skipped.
		objectIDAt(maumib.IfMauType, ianaMauType(26), 8),
	}

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	if got := facts.Facets[7].GetMauType().GetIana(); got != 30 {
		t.Errorf("iana = %d, want the mauIndex 1 reading", got)
	}

	if _, ok := facts.Facets[8]; ok {
		t.Error("a malformed index produced a facet")
	}
}

func TestPhysical_LinkModesAndAutoNegotiation(t *testing.T) {
	vbs := []vbFixture{
		objectIDAt(maumib.IfMauType, ianaMauType(30), 1, 1),
		integerAt(maumib.IfMauAutoNegSupported, 1, 1, 1),
		integerAt(maumib.IfMauAutoNegAdminStatus, int32(maumib.IfMauAutoNegAdminStatusValueEnabled), 1, 1),
		integerAt(maumib.IfMauAutoNegConfig, int32(maumib.IfMauAutoNegConfigValueComplete), 1, 1),
		bitsAt(maumib.IfMauAutoNegCapAdvertisedBits, []uint32{1, 2, 15, 40}, 1, 1),
		bitsAt(maumib.IfMauAutoNegCapReceivedBits, nil, 1, 1),
	}

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	facet := facts.Facets[1]
	mustValid(t, facet)

	want := []phyv1.MauLinkMode{
		phyv1.MauLinkMode_MAU_LINK_MODE_BASE_T_MBPS10,
		phyv1.MauLinkMode_MAU_LINK_MODE_BASE_T_MBPS10_FULL,
		phyv1.MauLinkMode_MAU_LINK_MODE_BASE_T_GBPS1_FULL,
		phyv1.MauLinkMode(40),
	}

	got := facet.GetAdvertisedLinkModes()
	if len(got) != len(want) {
		t.Fatalf("advertised modes = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("advertised mode %d = %v, want %v", i, got[i], want[i])
		}
	}

	if len(facet.GetReceivedLinkModes()) != 0 {
		t.Error("an empty received bitmap produced modes")
	}

	if !facet.GetCapabilities().GetAutoNegotiationSupported() {
		t.Error("auto-negotiation support was not read")
	}

	if !facet.GetAppliedAutoNegotiation().GetEnabled() ||
		facet.GetAppliedAutoNegotiation().GetStatus() != phyv1.AutoNegotiationStatus_AUTO_NEGOTIATION_STATUS_COMPLETE {
		t.Errorf("applied auto-negotiation = %v, want enabled and complete", facet.GetAppliedAutoNegotiation())
	}
}

// psePort is the POWER-ETHERNET-MIB fixture of one PSE port.
func psePort(group, port uint32) []vbFixture {
	return []vbFixture{
		integerAt(powerethernetmib.PethPsePortAdminEnable, 1, group, port),
		integerAt(powerethernetmib.PethPsePortDetectionStatus, int32(powerethernetmib.PethPsePortDetectionStatusValueDeliveringPower), group, port),
		integerAt(powerethernetmib.PethPsePortPowerPriority, int32(powerethernetmib.PethPsePortPowerPriorityValueHigh), group, port),
		integerAt(powerethernetmib.PethPsePortPowerClassifications, int32(powerethernetmib.PethPsePortPowerClassificationsValueClass3), group, port),
		counter32At(powerethernetmib.PethPsePortOverLoadCounter, 2, group, port),
		counter32At(powerethernetmib.PethPsePortMPSAbsentCounter, 0, group, port),
	}
}

func TestPhysical_PoePortsStandaloneUntilJoined(t *testing.T) {
	// The same row stays standalone without a join and lands
	// on the copper arm with one.
	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: psePort(1, 3)})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	if len(facts.PoePorts) != 1 {
		t.Fatalf("PoePorts = %d rows, want 1", len(facts.PoePorts))
	}

	row := facts.PoePorts[0]
	if row.Group != 1 || row.Port != 3 {
		t.Errorf("row key = %d/%d, want 1/3", row.Group, row.Port)
	}

	mustValid(t, row.Settings)
	mustValid(t, row.Facet)
	mustValid(t, row.Detail)

	if !row.Settings.GetEnabled() || row.Settings.GetPriority() != phyv1.PoePriority_POE_PRIORITY_HIGH {
		t.Errorf("settings = %v, want enabled and high priority", row.Settings)
	}

	if row.Facet.GetStatus() != phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER || row.Facet.GetPowerClass() != 3 {
		t.Errorf("facet = %v, want delivering power at class 3", row.Facet)
	}

	if row.Detail.GetOverloadCount() != 2 || !row.Detail.HasMpsAbsentCount() || row.Detail.HasShortCount() {
		t.Errorf("detail = %v, want overload 2, explicit zero MPS-absent, absent short", row.Detail)
	}

	ifaces := []*interfacev1.Interface{physicalIface("Gi1/0/3", 3), physicalIface("Gi1/0/4", 4)}

	snmpmap.AttachPhysical(ifaces, &facts, nil)

	if len(facts.PoePorts) != 1 {
		t.Fatal("a row with no join left PoePorts")
	}

	if ifaces[0].GetPhysical().HasEthernet() {
		t.Error("a row with no join reached an interface")
	}

	snmpmap.AttachPhysical(ifaces, &facts, map[snmpmap.PoePortKey]uint32{{Group: 1, Port: 3}: 3})

	if len(facts.PoePorts) != 0 {
		t.Fatal("a joined row stayed in PoePorts")
	}

	copper := ifaces[0].GetPhysical().GetEthernet().GetCopper()
	if copper == nil {
		t.Fatal("the joined row did not land on the copper arm")
	}

	mustValid(t, ifaces[0].GetPhysical().GetEthernet())

	if copper.GetPoeDetail().GetPsePort() != 3 || copper.GetPoe().GetStatus() != phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER {
		t.Errorf("copper arm = %v, want the joined row's messages", copper)
	}

	if ifaces[1].GetPhysical().HasEthernet() {
		t.Error("the join touched an interface it did not name")
	}
}

func TestAttachPhysical_KeepsRowOffNonCopperArm(t *testing.T) {
	vbs := append(psePort(1, 3), objectIDAt(maumib.IfMauType, ianaMauType(26), 3, 1))

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	ifaces := []*interfacev1.Interface{physicalIface("Te1/0/3", 3)}
	snmpmap.AttachPhysical(ifaces, &facts, map[snmpmap.PoePortKey]uint32{{Group: 1, Port: 3}: 3})

	if len(facts.PoePorts) != 1 {
		t.Error("a PoE row joined onto a fiber arm")
	}

	if !ifaces[0].GetPhysical().GetEthernet().HasFiber() {
		t.Error("the fiber facet did not reach the interface")
	}
}

func TestPhysical_PseBudgets(t *testing.T) {
	// Two PSE groups on a stacked switch.
	vbs := []vbFixture{
		gauge32At(powerethernetmib.PethMainPsePower, 370, 1),
		integerAt(powerethernetmib.PethMainPseOperStatus, int32(powerethernetmib.PethMainPseOperStatusValueOn), 1),
		gauge32At(powerethernetmib.PethMainPseConsumptionPower, 0, 1),
		integerAt(powerethernetmib.PethMainPseUsageThreshold, 80, 1),
		gauge32At(powerethernetmib.PethMainPsePower, 740, 2),
		integerAt(powerethernetmib.PethMainPseUsageThreshold, 0, 2),
	}

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	if len(facts.Budgets) != 2 {
		t.Fatalf("Budgets = %d, want 2", len(facts.Budgets))
	}

	first, second := facts.Budgets[0], facts.Budgets[1]
	mustValid(t, first)
	mustValid(t, second)

	if first.GetPseGroup() == second.GetPseGroup() {
		t.Error("two groups share a key")
	}

	if first.GetPowerMilliwatts() != 370_000 || first.GetOperStatus() != phyv1.PseOperStatus_PSE_OPER_STATUS_ON {
		t.Errorf("group 1 = %v, want 370 W on", first)
	}

	if !first.HasConsumptionMilliwatts() || first.GetConsumptionMilliwatts() != 0 {
		t.Error("explicit zero consumption is absent")
	}

	if second.HasUsageThresholdPercent() {
		t.Error("an out-of-range threshold was kept")
	}
}

func TestAttachPhysical_AbsentFacetStaysAbsent(t *testing.T) {
	// An interface with no physical rows keeps an absent
	// facet.
	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: etherLikePort(3)})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	ifaces := []*interfacev1.Interface{physicalIface("Gi1/0/3", 3), physicalIface("Gi1/0/9", 9)}
	snmpmap.AttachPhysical(ifaces, &facts, nil)

	if !ifaces[0].GetPhysical().HasEthernet() {
		t.Error("the EtherLike facet did not reach its interface")
	}

	if ifaces[1].GetPhysical().HasEthernet() {
		t.Error("an interface with no rows gained a facet")
	}
}

func TestPhysical_FailedMauWalkKeepsOtherFacts(t *testing.T) {
	vbs := append(etherLikePort(3), psePort(1, 3)...)
	vbs = append(vbs, objectIDAt(maumib.IfMauType, ianaMauType(30), 3, 1))

	sess := walkFailure(vbs, ifMauEntry)

	facts, err := snmpmap.Physical(context.Background(), sess)
	if err == nil {
		t.Fatal("a failed MAU walk returned no error")
	}

	if code, ok := errs.CodeOf(err); !ok || code != snmpmap.ErrCodePhysicalWalk {
		t.Errorf("error code = %v, want ErrCodePhysicalWalk", err)
	}

	if facts.Facets[3] == nil || !facts.Facets[3].HasCounters() {
		t.Error("the EtherLike facts were lost with the MAU walk")
	}

	if len(facts.PoePorts) != 1 {
		t.Error("the PoE rows were lost with the MAU walk")
	}
}

func TestPhysical_UnsupportedAutoNegotiationCannotBeEnabled(t *testing.T) {
	// Agents that instantiate ifMauAutoNegTable for every MAU report
	// adminStatus enabled on fiber ports that cannot negotiate.
	vbs := []vbFixture{
		objectIDAt(maumib.IfMauType, ianaMauType(30), 2, 1),
		integerAt(maumib.IfMauAutoNegSupported, 2, 2, 1),
		integerAt(maumib.IfMauAutoNegAdminStatus, int32(maumib.IfMauAutoNegAdminStatusValueEnabled), 2, 1),
		integerAt(maumib.IfMauAutoNegConfig, int32(maumib.IfMauAutoNegConfigValueDisabled), 2, 1),
	}

	facts, err := snmpmap.Physical(context.Background(), &fakeSession{vbs: vbs})
	if err != nil {
		t.Fatalf("Physical: %v", err)
	}

	facet := facts.Facets[2]
	mustValid(t, facet)

	if facet.GetCapabilities().GetAutoNegotiationSupported() {
		t.Error("support was not read as false")
	}

	applied := facet.GetAppliedAutoNegotiation()
	if applied.HasEnabled() || applied.GetStatus() != phyv1.AutoNegotiationStatus_AUTO_NEGOTIATION_STATUS_DISABLED {
		t.Errorf("applied auto-negotiation = %v, want no enabled fact and a disabled status", applied)
	}
}

func TestPhysicalMapper_Spec(t *testing.T) {
	spec := snmpmap.PhysicalMapper.Spec()

	if spec.Name == "" {
		t.Error("Spec.Name is empty")
	}

	if len(spec.Required) != 0 {
		t.Errorf("Required = %d tables, want 0: every physical-layer table is optional", len(spec.Required))
	}

	if got := len(spec.Optional); got != 6 {
		t.Errorf("Optional = %d tables, want 6 (dot3Stats, dot3HCStats, ifMau, ifMauAutoNeg, pethPsePort, pethMainPse)", got)
	}
}

func TestPhysicalMapper_Map(t *testing.T) {
	// physicalMapper.Map must build the same core facts Physical does, from
	// a Snapshot rather than a session.
	sess := &fakeSession{vbs: etherLikePort(3)}
	snap := collect.Read(context.Background(), sess, snmpmap.PhysicalMapper)

	out, err := snmpmap.PhysicalMapper.Map(snap)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}

	facts, ok := out.(snmpmap.PhysicalFacts)
	if !ok {
		t.Fatalf("Map returned %T, want snmpmap.PhysicalFacts", out)
	}

	facet := facts.Facets[3]
	if facet == nil {
		t.Fatal("no facet for ifIndex 3")
	}

	mustValid(t, facet)

	if got := facet.GetCounters().GetFcsErrors(); got != 7 {
		t.Errorf("fcs_errors = %d, want 7", got)
	}
}

func TestPhysical_SharesTablesWithInterfaceMapper(t *testing.T) {
	// Composing PhysicalMapper with InterfaceMapper in one Collector cycle
	// must walk each table root exactly once, even though both mappers
	// declare it (InterfaceMapper) or several tables (PhysicalMapper).
	sess := &countingSession{fakeSession: &fakeSession{vbs: etherLikePort(3)}}

	// The fixture answers no sysObjectID, so Collect's joined error names
	// the identity read; the table walks under test still run.
	_, _ = collect.New(snmpmap.InterfaceMapper, snmpmap.PhysicalMapper).Collect(context.Background(), sess)

	for root, n := range sess.walks {
		if n != 1 {
			t.Errorf("table %s walked %d times, want 1", root, n)
		}
	}
}

// countingSession counts BulkWalk calls per root OID, so a test can prove
// a shared cycle walks each table once regardless of how many mappers
// declare it.
type countingSession struct {
	*fakeSession
	walks map[string]int
}

func (s *countingSession) BulkWalk(ctx context.Context, root snmp.OID, opts ...snmp.CallOption) *snmp.Walker {
	if s.walks == nil {
		s.walks = make(map[string]int)
	}

	s.walks[root.String()]++

	return s.fakeSession.BulkWalk(ctx, root, opts...)
}
