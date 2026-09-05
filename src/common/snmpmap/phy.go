package snmpmap

import (
	"context"
	"errors"
	"math"

	"go.aledante.io/FlowSeer/generated/go/mib/etherlikemib"
	"go.aledante.io/FlowSeer/generated/go/mib/maumib"
	"go.aledante.io/FlowSeer/generated/go/mib/powerethernetmib"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// ErrCodePhysicalWalk identifies a failure of one of the physical-layer
// table walks. No physical table is one the others depend on, so a failed
// walk costs the facts that table would have added and nothing else.
var ErrCodePhysicalWalk = errs.NewCode("snmpmap/physical-walk")

// PoePortKey is the Power Ethernet MIB's own key for a PSE port: the group
// (a box in a stack, a module in a rack) and the port within it. The MIB
// never carries an ifIndex, so this is the identity a PoE row has until a
// per-device rule joins it to an interface.
type PoePortKey struct {
	Group uint32
	Port  uint32
}

// PoePortRow is one pethPsePortTable row mapped to the three messages a
// copper arm carries, still keyed the MIB's way. [AttachPhysical] moves
// the messages onto an interface when a join names it; a row with no join
// stays here so nothing is guessed. Concurrent reads are safe; callers
// must synchronize mutations.
type PoePortRow struct {
	PoePortKey
	Settings *phyv1.PoeSettings
	Facet    *phyv1.PoeFacet
	Detail   *phyv1.PoePortDetail
}

// PhysicalFacts are the messages one device's physical-layer MIBs yield.
// The zero value contains no facts. Concurrent reads are safe; callers
// must synchronize mutations of the map, slices, or messages.
type PhysicalFacts struct {
	// Facets are the Ethernet facets built from EtherLike-MIB and MAU-MIB,
	// keyed by ifIndex.
	Facets map[uint32]*phyv1.EthernetFacet
	// PoePorts are the PSE port rows, in walk order, that no join has
	// placed on an interface yet.
	PoePorts []PoePortRow
	// Budgets are the PSE group budgets of pethMainPseTable, in walk order.
	Budgets []*phyv1.PseBudget
}

// dot3StatsColumns are the dot3StatsTable columns [Physical] requests. The
// 32-bit counters are among them because they are the fallback for a
// device that implements no dot3HCStatsTable.
var dot3StatsColumns = []snmp.AnyColumn{
	etherlikemib.Dot3StatsAlignmentErrors,
	etherlikemib.Dot3StatsFCSErrors,
	etherlikemib.Dot3StatsSingleCollisionFrames,
	etherlikemib.Dot3StatsMultipleCollisionFrames,
	etherlikemib.Dot3StatsDeferredTransmissions,
	etherlikemib.Dot3StatsLateCollisions,
	etherlikemib.Dot3StatsExcessiveCollisions,
	etherlikemib.Dot3StatsInternalMacTransmitErrors,
	etherlikemib.Dot3StatsCarrierSenseErrors,
	etherlikemib.Dot3StatsFrameTooLongs,
	etherlikemib.Dot3StatsInternalMacReceiveErrors,
	etherlikemib.Dot3StatsSymbolErrors,
	etherlikemib.Dot3StatsDuplexStatus,
}

// dot3HCStatsColumns are the dot3HCStatsTable columns [Physical] requests.
var dot3HCStatsColumns = []snmp.AnyColumn{
	etherlikemib.Dot3HCStatsAlignmentErrors,
	etherlikemib.Dot3HCStatsFCSErrors,
	etherlikemib.Dot3HCStatsInternalMacTransmitErrors,
	etherlikemib.Dot3HCStatsFrameTooLongs,
	etherlikemib.Dot3HCStatsInternalMacReceiveErrors,
	etherlikemib.Dot3HCStatsSymbolErrors,
}

// ifMauColumns are the ifMauTable columns [Physical] requests.
var ifMauColumns = []snmp.AnyColumn{
	maumib.IfMauType,
	maumib.IfMauAutoNegSupported,
}

// ifMauAutoNegColumns are the ifMauAutoNegTable columns [Physical] requests.
var ifMauAutoNegColumns = []snmp.AnyColumn{
	maumib.IfMauAutoNegAdminStatus,
	maumib.IfMauAutoNegConfig,
	maumib.IfMauAutoNegCapAdvertisedBits,
	maumib.IfMauAutoNegCapReceivedBits,
}

// pethPsePortColumns are the pethPsePortTable columns [Physical] requests.
var pethPsePortColumns = []snmp.AnyColumn{
	powerethernetmib.PethPsePortAdminEnable,
	powerethernetmib.PethPsePortDetectionStatus,
	powerethernetmib.PethPsePortPowerPriority,
	powerethernetmib.PethPsePortMPSAbsentCounter,
	powerethernetmib.PethPsePortPowerClassifications,
	powerethernetmib.PethPsePortInvalidSignatureCounter,
	powerethernetmib.PethPsePortPowerDeniedCounter,
	powerethernetmib.PethPsePortOverLoadCounter,
	powerethernetmib.PethPsePortShortCounter,
}

// pethMainPseColumns are the pethMainPseTable columns [Physical] requests.
var pethMainPseColumns = []snmp.AnyColumn{
	powerethernetmib.PethMainPsePower,
	powerethernetmib.PethMainPseOperStatus,
	powerethernetmib.PethMainPseConsumptionPower,
	powerethernetmib.PethMainPseUsageThreshold,
}

// dot3MauType is the arc under which the IANA-MAU-MIB registers standard
// MAU types as dot3MauType.N (https://www.iana.org/assignments/ianamau-mib).
// The generated registry package names no OBJECT-IDENTITY node, so the
// arc is spelled here.
var dot3MauType = snmp.MustOID(1, 3, 6, 1, 2, 1, 26, 4)

// Physical walks the physical-layer MIBs on sess — EtherLike-MIB,
// MAU-MIB, POWER-ETHERNET-MIB, and the vendored D-Link, HP ProCurve, and
// H3C transceiver tables — and returns the Ethernet facets, PSE port
// rows, and PSE budgets the device reports. Every table is optional and
// none depends on another: a failed walk returns the facts the other
// tables carried and an error carrying [ErrCodePhysicalWalk] beside them,
// and a device that implements none of the tables yields empty facts and
// no error.
//
// Facets are keyed by ifIndex because EtherLike-MIB and MAU-MIB are; PoE
// rows are keyed the Power Ethernet MIB's way because it carries no
// ifIndex at all. [AttachPhysical] joins both onto interfaces.
func Physical(ctx context.Context, sess snmp.Session) (PhysicalFacts, error) {
	facts := PhysicalFacts{Facets: make(map[uint32]*phyv1.EthernetFacet)}

	var walkErrs []error

	walkErrs = append(walkErrs, walkEtherLike(ctx, sess, facts.Facets))
	walkErrs = append(walkErrs, walkMau(ctx, sess, facts.Facets))
	walkErrs = append(walkErrs, walkModules(ctx, sess, facts.Facets))

	ports, portErr := walkPsePorts(ctx, sess)
	facts.PoePorts = ports
	walkErrs = append(walkErrs, portErr)

	budgets, budgetErr := walkPseBudgets(ctx, sess)
	facts.Budgets = budgets
	walkErrs = append(walkErrs, budgetErr)

	return facts, errors.Join(walkErrs...)
}

// AttachPhysical sets each facet on the physical interface of the same
// ifIndex and moves each PoE row whose key joins names onto that
// interface's copper arm. Rows with no join, or whose join names an
// interface that is not a physical port or whose facet already states a
// non-copper transport, stay in facts.PoePorts: the MIB said nothing
// about which interface they belong to, and a wrong guess is worse than a
// standalone row. A facet created for a joined PoE row is also recorded in
// facts.Facets. The caller must not modify joins during the call.
func AttachPhysical(ifaces []*interfacev1.Interface, facts *PhysicalFacts, joins map[PoePortKey]uint32) {
	physical := make(map[uint32]*interfacev1.PhysicalInterface, len(ifaces))

	for _, iface := range ifaces {
		if !iface.HasIfIndex() || !iface.HasPhysical() {
			continue
		}

		physical[iface.GetIfIndex()] = iface.GetPhysical()
	}

	for idx, facet := range facts.Facets {
		if port, ok := physical[idx]; ok {
			port.SetEthernet(facet)
		}
	}

	kept := facts.PoePorts[:0]

	for _, row := range facts.PoePorts {
		idx, ok := joins[row.PoePortKey]
		if !ok {
			kept = append(kept, row)

			continue
		}

		port, ok := physical[idx]
		if !ok {
			kept = append(kept, row)

			continue
		}

		facet := facts.Facets[idx]
		if facet == nil {
			facet = &phyv1.EthernetFacet{}
			facts.Facets[idx] = facet
			port.SetEthernet(facet)
		}

		if facet.HasTransport() && !facet.HasCopper() {
			kept = append(kept, row)

			continue
		}

		copper := facet.GetCopper()
		if copper == nil {
			copper = &phyv1.CopperFacet{}
		}

		if row.Settings != nil {
			copper.SetPoeSettings(row.Settings)
		}

		if row.Facet != nil {
			copper.SetPoe(row.Facet)
		}

		if row.Detail != nil {
			copper.SetPoeDetail(row.Detail)
		}

		facet.SetCopper(copper)
	}

	clear(facts.PoePorts[len(kept):])
	facts.PoePorts = kept
}

// facetAt returns the facet for idx, creating it on first use. A facet
// exists only because an observed row asked for it.
func facetAt(facets map[uint32]*phyv1.EthernetFacet, idx uint32) *phyv1.EthernetFacet {
	facet, ok := facets[idx]
	if !ok {
		facet = &phyv1.EthernetFacet{}
		facets[idx] = facet
	}

	return facet
}

// walkEtherLike maps dot3StatsTable and dot3HCStatsTable, both keyed by
// dot3StatsIndex, which RFC 3635 defines as the ifIndex of the interface.
// A failed 32-bit walk still lets the 64-bit walk run, and the reverse.
func walkEtherLike(ctx context.Context, sess snmp.Session, facets map[uint32]*phyv1.EthernetFacet) error {
	stats := make(map[uint32]etherlikemib.Dot3StatsTableRow)

	var order []uint32

	statsWalk := etherlikemib.Dot3StatsTable.Walk(ctx, sess, dot3StatsColumns...)
	for idx, row := range statsWalk.Iter() {
		ifIndex, ok := singleIndex(idx)
		if !ok {
			continue
		}

		if _, seen := stats[ifIndex]; !seen {
			order = append(order, ifIndex)
		}

		stats[ifIndex] = row
	}

	var walkErrs []error
	if err := statsWalk.Err(); err != nil {
		walkErrs = append(walkErrs, errs.From(err).Code(ErrCodePhysicalWalk).Msg("walk dot3StatsTable"))
	}

	hc := make(map[uint32]etherlikemib.Dot3HCStatsTableRow)

	hcWalk := etherlikemib.Dot3HCStatsTable.Walk(ctx, sess, dot3HCStatsColumns...)
	for idx, row := range hcWalk.Iter() {
		ifIndex, ok := singleIndex(idx)
		if !ok {
			continue
		}

		_, inStats := stats[ifIndex]
		_, inHC := hc[ifIndex]

		if !inStats && !inHC {
			order = append(order, ifIndex)
		}

		hc[ifIndex] = row
	}

	if err := hcWalk.Err(); err != nil {
		walkErrs = append(walkErrs, errs.From(err).Code(ErrCodePhysicalWalk).Msg("walk dot3HCStatsTable"))
	}

	for _, ifIndex := range order {
		r, h := stats[ifIndex], hc[ifIndex]

		counters := ethernetCounters(r, h)
		duplex := ethernetDuplex(r)

		if counters == nil && duplex == phyv1.EthernetDuplex_ETHERNET_DUPLEX_UNSPECIFIED {
			continue
		}

		facet := facetAt(facets, ifIndex)

		if counters != nil {
			facet.SetCounters(counters)
		}

		if duplex != phyv1.EthernetDuplex_ETHERNET_DUPLEX_UNSPECIFIED {
			facet.SetActiveDuplex(duplex)
		}
	}

	return errors.Join(walkErrs...)
}

// dot3Counter reads one 32-bit dot3StatsTable counter.
func dot3Counter(r etherlikemib.Dot3StatsTableRow, col snmp.Column[uint32], value uint32) counter {
	return counter{value: uint64(value), reported: r.Observed(col)}
}

// dot3HCCounter reads one 64-bit dot3HCStatsTable counter.
func dot3HCCounter(h etherlikemib.Dot3HCStatsTableRow, col snmp.Column[uint64], value uint64) counter {
	return counter{value: value, reported: h.Observed(col)}
}

// ethernetCounters maps the counter columns of both tables, preferring
// the 64-bit reading of a counter over the 32-bit one that wraps. It
// returns nil when the device reported no counter at all.
func ethernetCounters(r etherlikemib.Dot3StatsTableRow, h etherlikemib.Dot3HCStatsTableRow) *phyv1.EthernetCounters {
	c := &phyv1.EthernetCounters{}
	reported := false

	set := func(field func(uint64), value counter) {
		if !value.reported {
			return
		}

		field(value.value)

		reported = true
	}

	set(c.SetAlignmentErrors, dot3HCCounter(h, etherlikemib.Dot3HCStatsAlignmentErrors, h.Dot3HCStatsAlignmentErrors).
		or(dot3Counter(r, etherlikemib.Dot3StatsAlignmentErrors, r.Dot3StatsAlignmentErrors)))
	set(c.SetFcsErrors, dot3HCCounter(h, etherlikemib.Dot3HCStatsFCSErrors, h.Dot3HCStatsFCSErrors).
		or(dot3Counter(r, etherlikemib.Dot3StatsFCSErrors, r.Dot3StatsFCSErrors)))
	set(c.SetInternalMacTransmitErrors, dot3HCCounter(h, etherlikemib.Dot3HCStatsInternalMacTransmitErrors, h.Dot3HCStatsInternalMacTransmitErrors).
		or(dot3Counter(r, etherlikemib.Dot3StatsInternalMacTransmitErrors, r.Dot3StatsInternalMacTransmitErrors)))
	set(c.SetFrameTooLongs, dot3HCCounter(h, etherlikemib.Dot3HCStatsFrameTooLongs, h.Dot3HCStatsFrameTooLongs).
		or(dot3Counter(r, etherlikemib.Dot3StatsFrameTooLongs, r.Dot3StatsFrameTooLongs)))
	set(c.SetInternalMacReceiveErrors, dot3HCCounter(h, etherlikemib.Dot3HCStatsInternalMacReceiveErrors, h.Dot3HCStatsInternalMacReceiveErrors).
		or(dot3Counter(r, etherlikemib.Dot3StatsInternalMacReceiveErrors, r.Dot3StatsInternalMacReceiveErrors)))
	set(c.SetSymbolErrors, dot3HCCounter(h, etherlikemib.Dot3HCStatsSymbolErrors, h.Dot3HCStatsSymbolErrors).
		or(dot3Counter(r, etherlikemib.Dot3StatsSymbolErrors, r.Dot3StatsSymbolErrors)))

	// The collision and carrier counters have no high-capacity columns.
	set(c.SetSingleCollisionFrames, dot3Counter(r, etherlikemib.Dot3StatsSingleCollisionFrames, r.Dot3StatsSingleCollisionFrames))
	set(c.SetMultipleCollisionFrames, dot3Counter(r, etherlikemib.Dot3StatsMultipleCollisionFrames, r.Dot3StatsMultipleCollisionFrames))
	set(c.SetDeferredTransmissions, dot3Counter(r, etherlikemib.Dot3StatsDeferredTransmissions, r.Dot3StatsDeferredTransmissions))
	set(c.SetLateCollisions, dot3Counter(r, etherlikemib.Dot3StatsLateCollisions, r.Dot3StatsLateCollisions))
	set(c.SetExcessiveCollisions, dot3Counter(r, etherlikemib.Dot3StatsExcessiveCollisions, r.Dot3StatsExcessiveCollisions))
	set(c.SetCarrierSenseErrors, dot3Counter(r, etherlikemib.Dot3StatsCarrierSenseErrors, r.Dot3StatsCarrierSenseErrors))

	if !reported {
		return nil
	}

	return c
}

// ethernetDuplex reads dot3StatsDuplexStatus, whose unknown value says
// nothing and so reads as unreported.
func ethernetDuplex(r etherlikemib.Dot3StatsTableRow) phyv1.EthernetDuplex {
	if !r.Observed(etherlikemib.Dot3StatsDuplexStatus) {
		return phyv1.EthernetDuplex_ETHERNET_DUPLEX_UNSPECIFIED
	}

	switch r.Dot3StatsDuplexStatus {
	case etherlikemib.Dot3StatsDuplexStatusValueHalfDuplex:
		return phyv1.EthernetDuplex_ETHERNET_DUPLEX_HALF
	case etherlikemib.Dot3StatsDuplexStatusValueFullDuplex:
		return phyv1.EthernetDuplex_ETHERNET_DUPLEX_FULL
	}

	return phyv1.EthernetDuplex_ETHERNET_DUPLEX_UNSPECIFIED
}

// mauKey is the (ifIndex, mauIndex) pair both MAU tables are keyed on.
type mauKey struct {
	ifIndex  uint32
	mauIndex uint32
}

// mauIndex reads a two-arc MAU index.
func mauIndex(o snmp.OID) (mauKey, bool) {
	if o.Len() != 2 {
		return mauKey{}, false
	}

	return mauKey{ifIndex: o.At(0), mauIndex: o.At(1)}, true
}

// walkMau maps ifMauTable and ifMauAutoNegTable. An interface may report
// several MAUs; the one with the lowest mauIndex speaks for the port,
// because the MIB numbers the primary MAU first and the rest are rarely
// anything but repeats.
func walkMau(ctx context.Context, sess snmp.Session, facets map[uint32]*phyv1.EthernetFacet) error {
	type mauRows struct {
		key     mauKey
		mau     maumib.IfMauTableRow
		hasMau  bool
		autoNeg maumib.IfMauAutoNegTableRow
		hasAuto bool
	}

	chosen := make(map[uint32]*mauRows)

	var order []uint32

	at := func(key mauKey) *mauRows {
		rows, ok := chosen[key.ifIndex]
		if !ok {
			rows = &mauRows{key: key}
			chosen[key.ifIndex] = rows
			order = append(order, key.ifIndex)
		}

		return rows
	}

	var walkErrs []error

	mauWalk := maumib.IfMauTable.Walk(ctx, sess, ifMauColumns...)
	for idx, row := range mauWalk.Iter() {
		key, ok := mauIndex(idx)
		if !ok {
			continue
		}

		rows := at(key)
		if rows.hasMau && key.mauIndex >= rows.key.mauIndex {
			continue
		}

		rows.key, rows.mau, rows.hasMau = key, row, true
	}

	if err := mauWalk.Err(); err != nil {
		walkErrs = append(walkErrs, errs.From(err).Code(ErrCodePhysicalWalk).Msg("walk ifMauTable"))
	}

	autoWalk := maumib.IfMauAutoNegTable.Walk(ctx, sess, ifMauAutoNegColumns...)
	for idx, row := range autoWalk.Iter() {
		key, ok := mauIndex(idx)
		if !ok {
			continue
		}

		rows := at(key)
		if key.mauIndex != rows.key.mauIndex {
			continue
		}

		rows.autoNeg, rows.hasAuto = row, true
	}

	if err := autoWalk.Err(); err != nil {
		walkErrs = append(walkErrs, errs.From(err).Code(ErrCodePhysicalWalk).Msg("walk ifMauAutoNegTable"))
	}

	for _, ifIndex := range order {
		rows := chosen[ifIndex]

		if rows.hasMau {
			mapMau(facets, ifIndex, rows.mau)
		}

		if rows.hasAuto {
			mapAutoNeg(facets, ifIndex, rows.autoNeg)
		}
	}

	return errors.Join(walkErrs...)
}

// mapMau sets the MAU type, the transport arm it implies, and
// auto-negotiation support from one ifMauTable row.
func mapMau(facets map[uint32]*phyv1.EthernetFacet, ifIndex uint32, r maumib.IfMauTableRow) {
	if r.Observed(maumib.IfMauType) {
		if mau, ok := mauType(r.IfMauType); ok {
			facet := facetAt(facets, ifIndex)
			facet.SetMauType(mau)
			setTransport(facet, mau)
		}
	}

	if r.Observed(maumib.IfMauAutoNegSupported) {
		facet := facetAt(facets, ifIndex)

		caps := facet.GetCapabilities()
		if caps == nil {
			caps = &phyv1.EthernetCapabilities{}
		}

		caps.SetAutoNegotiationSupported(r.IfMauAutoNegSupported)
		facet.SetCapabilities(caps)
	}
}

// mapAutoNeg sets the applied auto-negotiation facts and the link-mode
// sets from one ifMauAutoNegTable row.
func mapAutoNeg(facets map[uint32]*phyv1.EthernetFacet, ifIndex uint32, r maumib.IfMauAutoNegTableRow) {
	applied := &phyv1.AutoNegotiationFacet{}
	reported := false

	if r.Observed(maumib.IfMauAutoNegAdminStatus) {
		switch r.IfMauAutoNegAdminStatus {
		case maumib.IfMauAutoNegAdminStatusValueEnabled:
			applied.SetEnabled(true)

			reported = true
		case maumib.IfMauAutoNegAdminStatusValueDisabled:
			applied.SetEnabled(false)

			reported = true
		}
	}

	if r.Observed(maumib.IfMauAutoNegConfig) {
		if status := autoNegStatus(r.IfMauAutoNegConfig); status != phyv1.AutoNegotiationStatus_AUTO_NEGOTIATION_STATUS_UNSPECIFIED {
			applied.SetStatus(status)

			reported = true
		}
	}

	advertised := linkModes(r, maumib.IfMauAutoNegCapAdvertisedBits, r.IfMauAutoNegCapAdvertisedBits)
	received := linkModes(r, maumib.IfMauAutoNegCapReceivedBits, r.IfMauAutoNegCapReceivedBits)

	if !reported && advertised == nil && received == nil {
		return
	}

	facet := facetAt(facets, ifIndex)

	if reported {
		facet.SetAppliedAutoNegotiation(applied)
	}

	if advertised != nil {
		facet.SetAdvertisedLinkModes(advertised)
	}

	if received != nil {
		facet.SetReceivedLinkModes(received)
	}
}

// autoNegStatus maps ifMauAutoNegConfig to the normalized taxonomy. The
// MIB's other value says nothing and reads as unreported.
func autoNegStatus(v maumib.IfMauAutoNegConfigValue) phyv1.AutoNegotiationStatus {
	switch v {
	case maumib.IfMauAutoNegConfigValueConfiguring:
		return phyv1.AutoNegotiationStatus_AUTO_NEGOTIATION_STATUS_NEGOTIATING
	case maumib.IfMauAutoNegConfigValueComplete:
		return phyv1.AutoNegotiationStatus_AUTO_NEGOTIATION_STATUS_COMPLETE
	case maumib.IfMauAutoNegConfigValueDisabled:
		return phyv1.AutoNegotiationStatus_AUTO_NEGOTIATION_STATUS_DISABLED
	case maumib.IfMauAutoNegConfigValueParallelDetectFail:
		return phyv1.AutoNegotiationStatus_AUTO_NEGOTIATION_STATUS_FAILED
	}

	return phyv1.AutoNegotiationStatus_AUTO_NEGOTIATION_STATUS_UNSPECIFIED
}

// linkModes maps a link-mode bitmap to the schema's open enum, one value
// per set position. A position the schema does not name is kept as its
// own value; the numbering is the registry's. An unobserved column or an
// empty bitmap yields nil.
func linkModes(r maumib.IfMauAutoNegTableRow, col snmp.Column[snmp.BitSet], bits snmp.BitSet) []phyv1.MauLinkMode {
	if !r.Observed(col) {
		return nil
	}

	positions := bits.Positions()
	modes := make([]phyv1.MauLinkMode, 0, len(positions))

	for _, p := range positions {
		if p > math.MaxInt32 {
			continue
		}

		modes = append(modes, phyv1.MauLinkMode(p))
	}

	if len(modes) == 0 {
		return nil
	}

	return modes
}

// mauType carries an ifMauType identifier without loss: a registration
// under the dot3MauType arc by its number, any other identifier by its
// dotted form. The zeroDotZero sentinel means the type is unknown and
// yields no message.
func mauType(oid snmp.OID) (*phyv1.MauType, bool) {
	if oid.Len() == 0 || (oid.Len() == 2 && oid.At(0) == 0 && oid.At(1) == 0) {
		return nil, false
	}

	mau := &phyv1.MauType{}

	if oid.HasPrefix(dot3MauType) && oid.Len() == dot3MauType.Len()+1 && oid.At(dot3MauType.Len()) > 0 {
		mau.SetIana(oid.At(dot3MauType.Len()))
	} else {
		mau.SetOid(oid.String())
	}

	return mau, true
}

// mauMedium is the medium a registered MAU type runs over, read from the
// PHY family in the IANA-MAU-MIB registration name: T, TX, T2, T4, CX,
// CR, and the coaxial and AUI types are copper; F, FX, LX, SX, BX, PX,
// PR, ER, LR, SR, and the W variants are fiber; KX, KR, and KP are
// backplane. A family that names only a PCS, such as 1000BASE-X or
// 10GBASE-R, says nothing about the medium and is left out.
var mauMedium = map[uint32]func(*phyv1.EthernetFacet){
	1: copperArm, 2: copperArm, 4: copperArm, 5: copperArm, 9: copperArm,
	10: copperArm, 11: copperArm, 14: copperArm, 15: copperArm, 16: copperArm,
	19: copperArm, 20: copperArm, 27: copperArm, 28: copperArm, 29: copperArm,
	30: copperArm, 41: copperArm, 42: copperArm, 43: copperArm, 54: copperArm,
	71: copperArm, 75: copperArm, 79: copperArm, 88: copperArm, 89: copperArm,
	94: copperArm, 97: copperArm, 98: copperArm,

	3: fiberArm, 6: fiberArm, 7: fiberArm, 8: fiberArm, 12: fiberArm, 13: fiberArm,
	17: fiberArm, 18: fiberArm, 23: fiberArm, 24: fiberArm, 25: fiberArm, 26: fiberArm,
	32: fiberArm, 34: fiberArm, 35: fiberArm, 36: fiberArm, 37: fiberArm, 38: fiberArm,
	39: fiberArm, 40: fiberArm, 44: fiberArm, 45: fiberArm, 46: fiberArm, 47: fiberArm,
	48: fiberArm, 49: fiberArm, 50: fiberArm, 51: fiberArm, 52: fiberArm, 53: fiberArm,
	55: fiberArm, 59: fiberArm, 60: fiberArm, 61: fiberArm, 62: fiberArm, 63: fiberArm,
	64: fiberArm, 65: fiberArm, 66: fiberArm, 67: fiberArm, 68: fiberArm, 69: fiberArm,
	72: fiberArm, 73: fiberArm, 74: fiberArm, 76: fiberArm, 77: fiberArm, 78: fiberArm,
	80: fiberArm, 81: fiberArm, 82: fiberArm, 83: fiberArm, 84: fiberArm, 85: fiberArm,
	86: fiberArm, 87: fiberArm, 93: fiberArm, 95: fiberArm, 102: fiberArm,

	56: backplaneArm, 57: backplaneArm, 58: backplaneArm, 70: backplaneArm,
	90: backplaneArm, 91: backplaneArm, 99: backplaneArm, 100: backplaneArm,
}

func copperArm(f *phyv1.EthernetFacet) {
	if !f.HasCopper() {
		f.SetCopper(&phyv1.CopperFacet{})
	}
}

func fiberArm(f *phyv1.EthernetFacet) { f.SetFiber(&phyv1.FiberFacet{}) }

func backplaneArm(f *phyv1.EthernetFacet) { f.SetBackplane(&phyv1.BackplaneFacet{}) }

// setTransport selects the transport arm the MAU type implies. A type
// with no known medium leaves the arm as it is, so a module's media can
// still decide it.
func setTransport(facet *phyv1.EthernetFacet, mau *phyv1.MauType) {
	if !mau.HasIana() {
		return
	}

	if arm, ok := mauMedium[mau.GetIana()]; ok {
		arm(facet)
	}
}

// walkPsePorts maps pethPsePortTable to PoE rows keyed by group and port.
func walkPsePorts(ctx context.Context, sess snmp.Session) ([]PoePortRow, error) {
	var rows []PoePortRow

	walk := powerethernetmib.PethPsePortTable.Walk(ctx, sess, pethPsePortColumns...)
	for idx, row := range walk.Iter() {
		if idx.Len() != 2 || idx.At(0) == 0 || idx.At(1) == 0 {
			continue
		}

		rows = append(rows, mapPsePort(PoePortKey{Group: idx.At(0), Port: idx.At(1)}, row))
	}

	if err := walk.Err(); err != nil {
		return rows, errs.From(err).Code(ErrCodePhysicalWalk).Msg("walk pethPsePortTable")
	}

	return rows, nil
}

// mapPsePort splits one PSE port row into the intent, the delivery state,
// and the detail a copper arm carries. Every port in the table is
// power-sourcing equipment, so the role and support are facts of the
// table, not of a column.
func mapPsePort(key PoePortKey, r powerethernetmib.PethPsePortTableRow) PoePortRow {
	settings := &phyv1.PoeSettings{}
	settingsReported := false

	if r.Observed(powerethernetmib.PethPsePortAdminEnable) {
		settings.SetEnabled(r.PethPsePortAdminEnable)

		settingsReported = true
	}

	if r.Observed(powerethernetmib.PethPsePortPowerPriority) {
		if priority := poePriority(r.PethPsePortPowerPriority); priority != phyv1.PoePriority_POE_PRIORITY_UNSPECIFIED {
			settings.SetPriority(priority)

			settingsReported = true
		}
	}

	facet := &phyv1.PoeFacet{}
	facet.SetSupported(true)
	facet.SetRole(phyv1.PoeRole_POE_ROLE_PSE)

	if r.Observed(powerethernetmib.PethPsePortDetectionStatus) {
		if status := poeStatus(r.PethPsePortDetectionStatus); status != phyv1.PoeStatus_POE_STATUS_UNSPECIFIED {
			facet.SetStatus(status)
		}
	}

	// The MIB numbers class0 as 1, so the class is one less than the
	// enumeration; a value past the named classes is a class the MIB
	// does not define and reads as unreported.
	if r.Observed(powerethernetmib.PethPsePortPowerClassifications) {
		if class := int32(r.PethPsePortPowerClassifications); class >= 1 && class <= 5 {
			facet.SetPowerClass(uint32(class - 1))
		}
	}

	detail := &phyv1.PoePortDetail{}
	detail.SetPseGroup(key.Group)
	detail.SetPsePort(key.Port)

	if r.Observed(powerethernetmib.PethPsePortInvalidSignatureCounter) {
		detail.SetInvalidSignatureCount(uint64(r.PethPsePortInvalidSignatureCounter))
	}

	if r.Observed(powerethernetmib.PethPsePortPowerDeniedCounter) {
		detail.SetPowerDeniedCount(uint64(r.PethPsePortPowerDeniedCounter))
	}

	if r.Observed(powerethernetmib.PethPsePortOverLoadCounter) {
		detail.SetOverloadCount(uint64(r.PethPsePortOverLoadCounter))
	}

	if r.Observed(powerethernetmib.PethPsePortShortCounter) {
		detail.SetShortCount(uint64(r.PethPsePortShortCounter))
	}

	if r.Observed(powerethernetmib.PethPsePortMPSAbsentCounter) {
		detail.SetMpsAbsentCount(uint64(r.PethPsePortMPSAbsentCounter))
	}

	row := PoePortRow{PoePortKey: key, Facet: facet, Detail: detail}
	if settingsReported {
		row.Settings = settings
	}

	return row
}

func poePriority(v powerethernetmib.PethPsePortPowerPriorityValue) phyv1.PoePriority {
	switch v {
	case powerethernetmib.PethPsePortPowerPriorityValueCritical:
		return phyv1.PoePriority_POE_PRIORITY_CRITICAL
	case powerethernetmib.PethPsePortPowerPriorityValueHigh:
		return phyv1.PoePriority_POE_PRIORITY_HIGH
	case powerethernetmib.PethPsePortPowerPriorityValueLow:
		return phyv1.PoePriority_POE_PRIORITY_LOW
	}

	return phyv1.PoePriority_POE_PRIORITY_UNSPECIFIED
}

func poeStatus(v powerethernetmib.PethPsePortDetectionStatusValue) phyv1.PoeStatus {
	switch v {
	case powerethernetmib.PethPsePortDetectionStatusValueDisabled:
		return phyv1.PoeStatus_POE_STATUS_DISABLED
	case powerethernetmib.PethPsePortDetectionStatusValueSearching:
		return phyv1.PoeStatus_POE_STATUS_SEARCHING
	case powerethernetmib.PethPsePortDetectionStatusValueDeliveringPower:
		return phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER
	case powerethernetmib.PethPsePortDetectionStatusValueFault:
		return phyv1.PoeStatus_POE_STATUS_FAULT
	case powerethernetmib.PethPsePortDetectionStatusValueTest:
		return phyv1.PoeStatus_POE_STATUS_TEST
	case powerethernetmib.PethPsePortDetectionStatusValueOtherFault:
		return phyv1.PoeStatus_POE_STATUS_OTHER_FAULT
	}

	return phyv1.PoeStatus_POE_STATUS_UNSPECIFIED
}

// walkPseBudgets maps pethMainPseTable to one budget per PSE group.
func walkPseBudgets(ctx context.Context, sess snmp.Session) ([]*phyv1.PseBudget, error) {
	var budgets []*phyv1.PseBudget

	walk := powerethernetmib.PethMainPseTable.Walk(ctx, sess, pethMainPseColumns...)
	for idx, row := range walk.Iter() {
		group, ok := singleIndex(idx)
		if !ok || group == 0 {
			continue
		}

		budgets = append(budgets, mapPseBudget(group, row))
	}

	if err := walk.Err(); err != nil {
		return budgets, errs.From(err).Code(ErrCodePhysicalWalk).Msg("walk pethMainPseTable")
	}

	return budgets, nil
}

// wattsToMilliwatts converts the MIB's whole watts to the schema's
// milliwatts, saturating rather than wrapping on a value past the field.
func wattsToMilliwatts(watts uint32) uint32 {
	if watts > math.MaxUint32/1000 {
		return math.MaxUint32
	}

	return watts * 1000
}

// mapPseBudget maps one pethMainPseTable row. The MIB reports power in
// whole watts and the threshold in whole percent; a zero nominal power or
// a threshold outside 1 through 99 is outside the MIB's own ranges and
// reads as unreported.
func mapPseBudget(group uint32, r powerethernetmib.PethMainPseTableRow) *phyv1.PseBudget {
	budget := &phyv1.PseBudget{}
	budget.SetPseGroup(group)

	if r.Observed(powerethernetmib.PethMainPsePower) && r.PethMainPsePower > 0 {
		budget.SetPowerMilliwatts(wattsToMilliwatts(r.PethMainPsePower))
	}

	if r.Observed(powerethernetmib.PethMainPseOperStatus) {
		if status := pseOperStatus(r.PethMainPseOperStatus); status != phyv1.PseOperStatus_PSE_OPER_STATUS_UNSPECIFIED {
			budget.SetOperStatus(status)
		}
	}

	if r.Observed(powerethernetmib.PethMainPseConsumptionPower) {
		budget.SetConsumptionMilliwatts(wattsToMilliwatts(r.PethMainPseConsumptionPower))
	}

	if r.Observed(powerethernetmib.PethMainPseUsageThreshold) && r.PethMainPseUsageThreshold >= 1 && r.PethMainPseUsageThreshold <= 99 {
		budget.SetUsageThresholdPercent(uint32(r.PethMainPseUsageThreshold))
	}

	return budget
}

func pseOperStatus(v powerethernetmib.PethMainPseOperStatusValue) phyv1.PseOperStatus {
	switch v {
	case powerethernetmib.PethMainPseOperStatusValueOn:
		return phyv1.PseOperStatus_PSE_OPER_STATUS_ON
	case powerethernetmib.PethMainPseOperStatusValueOff:
		return phyv1.PseOperStatus_PSE_OPER_STATUS_OFF
	case powerethernetmib.PethMainPseOperStatusValueFaulty:
		return phyv1.PseOperStatus_PSE_OPER_STATUS_FAULTY
	}

	return phyv1.PseOperStatus_PSE_OPER_STATUS_UNSPECIFIED
}
