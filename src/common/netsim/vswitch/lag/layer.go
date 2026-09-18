package lag

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// ReasonUnsupportedLACPDU indicates that an Ethernet frame carried an unparseable or unsupported LACPDU.
const ReasonUnsupportedLACPDU trace.Reason = "unsupported-lacpdu"

// Emission describes an Ethernet frame to transmit out a member port.
type Emission struct {
	Port  string
	Frame ethernet.Frame
}

// Effects lists frames to emit and LAGs whose set of enabled members changed.
type Effects struct {
	Emissions []Emission
	Changed   []string
}

// Info summarizes the runtime aggregation status of one link aggregation group.
type Info struct {
	Mode                  Mode
	Enabled               []string
	Attached              []string
	PartnerSystemID       netaddr.MAC
	PartnerSystemPriority uint16
	PartnerKey            uint16
	Up                    bool

	// Pending names member ports that may still change state on their own
	// (a running link delay, an Expired partner, or an attached partner
	// without synchronization) and when. A pending member changes no other
	// field of Info; the answer above is definite as of now.
	Pending []Pending
}

// MemberInfo summarizes the runtime status of one member port in a link aggregation group.
type MemberInfo struct {
	LinkUp     bool
	Enabled    bool
	Attached   bool
	Actor      lacp.Info
	Partner    lacp.Info
	Status     Status
	LACPDUsTx  uint64
	LACPDUsRx  uint64
	BadLACPDUs uint64
}

// PendingCause identifies why a member port may still change state.
type PendingCause string

const (
	// PendingLinkDelay marks a member whose up or down delay timer is running.
	PendingLinkDelay PendingCause = "link-delay"

	// PendingPartnerExpired marks a member whose partner information is Expired
	// and will move to Defaulted when its receive timer elapses.
	PendingPartnerExpired PendingCause = "partner-expired"

	// PendingUnsynchronized marks a member attached to the lead partner that has
	// not yet advertised synchronization.
	PendingUnsynchronized PendingCause = "unsynchronized"
)

// Pending names one member port that may still change state on its own, and
// the time at which that is currently scheduled to happen.
type Pending struct {
	Member string
	Cause  PendingCause
	At     time.Time
}

// bucketEntry is the runtime state of one of a LAG's 256 hash buckets: the
// member last assigned to carry it, and when that assignment was made.
type bucketEntry struct {
	member     string
	assignedAt time.Time
	assigned   bool
}

type lagState struct {
	name        string
	cfg         LAG
	memberNames []string

	// enabledOrder is the OVS-style list of enabled members: bucket lookup
	// rotates a chosen member to the back, and a newly enabled member joins
	// the back, so order here is not alphabetical.
	enabledOrder    []string
	attachedMembers []string
	partnerSysID    netaddr.MAC
	partnerSysPrio  uint16
	partnerKey      uint16

	// lastActive is the active-backup member last chosen on a committing
	// call, kept so active-backup does not fail back once its member
	// recovers.
	lastActive string

	buckets [256]bucketEntry
}

func (l *lagState) clone() *lagState {
	cp := *l
	cp.cfg = l.cfg.Clone()
	cp.memberNames = slices.Clone(l.memberNames)
	cp.enabledOrder = slices.Clone(l.enabledOrder)
	cp.attachedMembers = slices.Clone(l.attachedMembers)

	return &cp
}

type memberState struct {
	name             string
	lagName          string
	portID           uint16
	cfg              Member
	carrier          bool
	linkUp           bool
	hasPendingLink   bool
	pendingLinkUp    bool
	linkDelayTimer   time.Time
	attached         bool
	enabled          bool
	status           Status
	partnerDefaulted bool
	partner          lacp.Info
	actor            lacp.Info
	lastTxActor      lacp.Info
	hasTxActor       bool
	rxTimer          time.Time
	rxPeriod         time.Duration
	txTimer          time.Time
	txPeriod         time.Duration
	lacpdusTx        uint64
	lacpdusRx        uint64
	badLACPDUs       uint64
}

func (m *memberState) clone() *memberState {
	cp := *m

	return &cp
}

// Layer implements the link aggregation layer for a virtual switch.
// It manages member selection, bond modes, up/down delays, and the LACP exchange.
// Layer is not safe for concurrent use.
type Layer struct {
	cfg      Config
	ports    port.Table
	systemID netaddr.MAC
	now      time.Time
	lags     map[string]*lagState
	members  map[string]*memberState
}

// New constructs a link aggregation layer from the given configuration, port table, and switch system ID.
// It returns an error if the configuration is invalid against the ports.
func New(cfg Config, ports port.Table, systemID netaddr.MAC) (*Layer, error) {
	norm := cfg.Normalize(ports, systemID)
	if err := norm.Validate(ports); err != nil {
		return nil, err
	}
	return newLayer(norm, ports, systemID), nil
}

func newLayer(cfg Config, ports port.Table, systemID netaddr.MAC) *Layer {
	layer := &Layer{
		cfg:      cfg,
		ports:    ports.Clone(),
		systemID: systemID,
		lags:     make(map[string]*lagState, len(cfg.LAGs)),
		members:  make(map[string]*memberState),
	}

	for _, lagName := range sortedKeys(cfg.LAGs) {
		lagCfg := cfg.LAGs[lagName]
		tableMembers := ports.Members(lagName)
		var memNames []string
		for _, m := range tableMembers {
			memNames = append(memNames, m.Name)
		}
		slices.Sort(memNames)

		ls := &lagState{
			name:        lagName,
			cfg:         lagCfg,
			memberNames: memNames,
		}
		layer.lags[lagName] = ls

		txPeriod := SlowPeriod
		if lagCfg.LACP.Fast {
			txPeriod = FastPeriod
		}

		for idx, memName := range memNames {
			mCfg := lagCfg.Members[memName]
			ms := &memberState{
				name:             memName,
				lagName:          lagName,
				portID:           uint16(idx + 1),
				cfg:              mCfg,
				linkUp:           false,
				status:           Defaulted,
				partnerDefaulted: true,
				partner:          lacp.Info{State: lacp.StateDefaulted},
				txPeriod:         txPeriod,
				rxPeriod:         txPeriod,
			}
			ms.updateActorInfo(ls)
			layer.members[memName] = ms
		}
	}

	return layer
}

// Clone creates an independent deep copy of the layer.
func (l *Layer) Clone() *Layer {
	cp := &Layer{
		cfg:      l.cfg.Clone(),
		ports:    l.ports.Clone(),
		systemID: l.systemID,
		now:      l.now,
		lags:     make(map[string]*lagState, len(l.lags)),
		members:  make(map[string]*memberState, len(l.members)),
	}

	for k, v := range l.lags {
		cp.lags[k] = v.clone()
	}
	for k, v := range l.members {
		cp.members[k] = v.clone()
	}

	return cp
}

// SelectionCause identifies why Select or Peek chose a member.
type SelectionCause string

const (
	// CauseKept means the bucket already had an enabled member and kept it.
	CauseKept SelectionCause = "kept"

	// CauseFirstUse means the bucket had never been assigned.
	CauseFirstUse SelectionCause = "first-use"

	// CauseReassigned means the bucket's member was disabled, so a different
	// member was assigned.
	CauseReassigned SelectionCause = "reassigned"

	// CausePrimary means active-backup chose the configured Primary.
	CausePrimary SelectionCause = "primary"

	// CauseLastActive means active-backup kept the member last active rather
	// than failing back.
	CauseLastActive SelectionCause = "last-active"

	// CauseFirstEnabled means active-backup had neither a Primary nor a
	// surviving last-active member and fell back to the lowest-named enabled
	// member.
	CauseFirstEnabled SelectionCause = "first-enabled"
)

// Selection is the outcome of choosing a member to carry a frame.
type Selection struct {
	Member string
	OK     bool

	// Bucket and Prior describe a balanced-mode decision; Prior is the
	// bucket's occupant before this call ("" on CauseFirstUse). Both are
	// zero for an ActiveBackup decision.
	Bucket uint8
	Prior  string

	Cause SelectionCause

	// RebalanceUnmodeled reports that this is a balanced-mode selection whose
	// bucket was assigned at least one rebalance interval ago, with two or
	// more enabled members: OVS would have considered moving it by measured
	// load, which netsim does not model. The caller decides what to do with
	// that, such as raising an Incomplete issue.
	RebalanceUnmodeled bool
}

// Select chooses an enabled member of the named LAG to carry the given frame
// and commits that choice: a balanced bucket's assignment and its move to the
// back of the enabled list, or active-backup's last-active memory.
// It applies the configured bond mode (ActiveBackup, BalanceSLB, or BalanceTCP).
func (l *Layer) Select(now time.Time, lagName string, f ethernet.Frame, vid vlan.ID) Selection {
	return l.selectMember(now, lagName, f, vid, true)
}

// Peek computes the same choice Select would make, without committing it: no
// bucket assignment, no enabled-list rotation, and no last-active memory
// change.
func (l *Layer) Peek(now time.Time, lagName string, f ethernet.Frame, vid vlan.ID) Selection {
	return l.selectMember(now, lagName, f, vid, false)
}

func (l *Layer) selectMember(now time.Time, lagName string, f ethernet.Frame, vid vlan.ID, commit bool) Selection {
	lag, ok := l.lags[lagName]
	if !ok {
		return Selection{}
	}

	switch lag.cfg.Mode {
	case BalanceSLB:
		return l.balancedSelect(lag, now, hashSLB(lag.cfg.HashBasis, f.Src, vid), commit)

	case BalanceTCP:
		// Balance-tcp requires negotiated LACP: with LACP off, or with no
		// member attached to a partner, it carries nothing unless Fallback
		// applies, and Fallback selects as active-backup
		// (ofproto/bond.c choose_output_member, BM_TCP and LACP_CONFIGURED).
		if lag.cfg.LACP.Mode == Off || len(lag.attachedMembers) == 0 {
			if !lag.cfg.LACP.Fallback {
				return Selection{}
			}

			return l.activeBackupSelect(lag, now, commit)
		}

		return l.balancedSelect(lag, now, hashTCP(lag.cfg.HashBasis, f), commit)

	default: // ActiveBackup
		return l.activeBackupSelect(lag, now, commit)
	}
}

// balancedSelect implements OVS's choose_output_member / get_enabled_member
// (ofproto/bond.c, branch-3.3 73e38c8d): a bucket keeps its member while that
// member stays enabled; otherwise it takes the member at the front of the
// enabled list and that member moves to the back.
func (l *Layer) balancedSelect(lag *lagState, now time.Time, hash uint32, commit bool) Selection {
	if len(lag.enabledOrder) == 0 {
		return Selection{}
	}

	bucket := uint8(hash & 0xff)
	entry := lag.buckets[bucket]

	if entry.assigned && slices.Contains(lag.enabledOrder, entry.member) {
		return Selection{
			Member:             entry.member,
			OK:                 true,
			Bucket:             bucket,
			Prior:              entry.member,
			Cause:              CauseKept,
			RebalanceUnmodeled: l.rebalanceUnmodeled(lag, entry.assignedAt, now),
		}
	}

	cause := CauseFirstUse
	prior := ""
	if entry.assigned {
		cause = CauseReassigned
		prior = entry.member
	}

	front := lag.enabledOrder[0]
	if commit {
		lag.enabledOrder = append(lag.enabledOrder[1:], front)
		lag.buckets[bucket] = bucketEntry{member: front, assignedAt: now, assigned: true}
	}

	return Selection{Member: front, OK: true, Bucket: bucket, Prior: prior, Cause: cause}
}

// rebalanceUnmodeled reports whether a kept bucket is old enough, and the LAG
// has enough enabled members, that OVS would have considered rebalancing it
// by measured load (a signal netsim does not model and so reports instead).
func (l *Layer) rebalanceUnmodeled(lag *lagState, assignedAt, now time.Time) bool {
	interval := lag.cfg.RebalanceInterval
	if interval == nil || *interval <= 0 || assignedAt.IsZero() {
		return false
	}
	if len(lag.enabledOrder) < 2 {
		return false
	}

	return !now.Before(assignedAt.Add(*interval))
}

// activeBackupSelect implements OVS's bond_choose_member: the configured
// Primary if enabled, else the member last active if it is still enabled,
// else the lowest-named enabled member. OVS walks a hash map at that last
// step; the lowest name is netsim's deterministic stand-in for that walk.
func (l *Layer) activeBackupSelect(lag *lagState, _ time.Time, commit bool) Selection {
	if len(lag.enabledOrder) == 0 {
		return Selection{}
	}

	var member string
	var cause SelectionCause

	switch {
	case lag.cfg.Primary != "" && slices.Contains(lag.enabledOrder, lag.cfg.Primary):
		member = lag.cfg.Primary
		cause = CausePrimary
	case lag.lastActive != "" && slices.Contains(lag.enabledOrder, lag.lastActive):
		member = lag.lastActive
		cause = CauseLastActive
	default:
		member = slices.Min(lag.enabledOrder)
		cause = CauseFirstEnabled
	}

	prior := lag.lastActive
	if commit {
		lag.lastActive = member
	}

	return Selection{Member: member, OK: true, Prior: prior, Cause: cause}
}

func (l *Layer) emitLACPDU(m *memberState) Emission {
	lag := l.lags[m.lagName]
	pdu := lacp.PDU{
		Actor:             m.actor,
		Partner:           m.partner,
		CollectorMaxDelay: 0,
	}
	src := lag.cfg.LACP.SystemID
	if src == (netaddr.MAC{}) {
		src = l.systemID
	}
	frame := lacp.Encode(pdu, src)
	m.lastTxActor = m.actor
	m.hasTxActor = true
	m.lacpdusTx++

	return Emission{
		Port:  m.name,
		Frame: frame,
	}
}

// LinkChange informs the layer that a member port's link transitioned up or down.
// A zero delay applies immediately; a non-zero delay arms a timer.
func (l *Layer) LinkChange(now time.Time, member string, up bool) Effects {
	l.now = now
	m, ok := l.members[member]
	if !ok {
		return Effects{}
	}
	lag := l.lags[m.lagName]

	// The carrier drives the protocol at once; the delay only decides when
	// the member carries traffic, so a partner is heard during an up delay.
	var fx Effects
	if m.carrier != up {
		m.carrier = up
		fx = l.setCarrier(now, m, up)
	}

	delay := lag.cfg.DownDelay
	if up {
		delay = lag.cfg.UpDelay
	}

	if delay == 0 {
		m.hasPendingLink = false
		m.linkDelayTimer = time.Time{}

		return mergeEffects(fx, l.applyLinkChange(m))
	}

	if m.hasPendingLink && m.pendingLinkUp == up {
		return fx
	}
	if m.linkUp == up && !m.hasPendingLink {
		return fx
	}
	if m.linkUp == up && m.hasPendingLink && m.pendingLinkUp != up {
		m.hasPendingLink = false
		m.linkDelayTimer = time.Time{}

		return fx
	}

	m.hasPendingLink = true
	m.pendingLinkUp = up
	m.linkDelayTimer = now.Add(delay)

	return fx
}

func mergeEffects(a, b Effects) Effects {
	out := Effects{
		Emissions: append(a.Emissions, b.Emissions...),
		Changed:   append(a.Changed, b.Changed...),
	}
	slices.Sort(out.Changed)
	out.Changed = slices.Compact(out.Changed)

	return out
}

// setCarrier starts or stops the protocol on a member as its carrier comes
// and goes; the member's enablement follows the delayed link separately.
func (l *Layer) setCarrier(now time.Time, m *memberState, up bool) Effects {
	lag := l.lags[m.lagName]
	if lag.cfg.LACP.Mode == Off {
		return Effects{}
	}

	var emissions []Emission
	var changed []string

	if !up {
		m.status = Defaulted
		m.partnerDefaulted = true
		m.partner = lacp.Info{State: lacp.StateDefaulted}
		m.rxTimer = time.Time{}
		m.txTimer = time.Time{}
		m.hasTxActor = false
		if l.updateLag(lag) {
			changed = append(changed, lag.name)
		}

		return Effects{Changed: changed}
	}

	m.status = Current
	m.partnerDefaulted = true
	m.partner = lacp.Info{State: lacp.StateDefaulted}
	m.rxPeriod = m.txPeriod
	m.rxTimer = now.Add(time.Duration(TimeoutMultiplier) * m.rxPeriod)

	if l.updateLag(lag) {
		changed = append(changed, lag.name)
	}

	if m.mayTx(lag) {
		emissions = append(emissions, l.emitLACPDU(m))
	}
	m.txTimer = now.Add(m.txPeriod)

	for _, name := range lag.memberNames {
		mem := l.members[name]
		if mem.name == m.name {
			continue
		}
		if mem.mayTx(lag) && (!mem.hasTxActor || mem.actor != mem.lastTxActor) {
			emissions = append(emissions, l.emitLACPDU(mem))
			mem.txTimer = now.Add(mem.txPeriod)
		}
	}

	return Effects{
		Emissions: emissions,
		Changed:   changed,
	}
}

// applyLinkChange moves the member's delayed link to its carrier and
// re-evaluates what the LAG carries.
func (l *Layer) applyLinkChange(m *memberState) Effects {
	if m.linkUp == m.carrier {
		return Effects{}
	}
	m.linkUp = m.carrier
	lag := l.lags[m.lagName]

	var changed []string
	if l.updateLag(lag) {
		changed = append(changed, lag.name)
	}

	return Effects{Changed: changed}
}

// Receive processes an incoming LACPDU on the named member port.
func (l *Layer) Receive(now time.Time, member string, pdu lacp.PDU) Effects {
	l.now = now
	m, ok := l.members[member]
	if !ok {
		return Effects{}
	}
	m.lacpdusRx++
	lag := l.lags[m.lagName]
	if !m.carrier || lag.cfg.LACP.Mode == Off {
		return Effects{}
	}

	m.partner = pdu.Actor
	m.partnerDefaulted = false
	m.status = Current

	if pdu.Actor.State&lacp.StateShortTimeout != 0 {
		m.rxPeriod = FastPeriod
	} else {
		m.rxPeriod = SlowPeriod
	}
	m.rxTimer = now.Add(time.Duration(TimeoutMultiplier) * m.rxPeriod)

	var changed []string
	if l.updateLag(lag) {
		changed = append(changed, lag.name)
	}

	var emissions []Emission
	for _, name := range lag.memberNames {
		mem := l.members[name]
		if mem.mayTx(lag) && (!mem.hasTxActor || mem.actor != mem.lastTxActor) {
			emissions = append(emissions, l.emitLACPDU(mem))
			mem.txTimer = now.Add(mem.txPeriod)
		}
	}

	return Effects{
		Emissions: emissions,
		Changed:   changed,
	}
}

// Wake advances timer-driven state to now, applying expired delays and timeouts.
func (l *Layer) Wake(now time.Time) Effects {
	l.now = now
	var emissions []Emission
	changedMap := make(map[string]struct{})

	for _, name := range sortedKeys(l.members) {
		m := l.members[name]
		if m.hasPendingLink && !m.linkDelayTimer.IsZero() && !m.linkDelayTimer.After(now) {
			m.hasPendingLink = false
			m.linkDelayTimer = time.Time{}
			fx := l.applyLinkChange(m)
			emissions = append(emissions, fx.Emissions...)
			for _, ch := range fx.Changed {
				changedMap[ch] = struct{}{}
			}
		}
	}

	for _, lagName := range sortedKeys(l.lags) {
		lag := l.lags[lagName]
		if lag.cfg.LACP.Mode == Off {
			continue
		}
		lagNeedsUpdate := false
		for _, name := range lag.memberNames {
			m := l.members[name]
			if !m.carrier || m.rxTimer.IsZero() || m.rxTimer.After(now) {
				continue
			}

			switch m.status {
			case Current:
				m.status = Expired
				m.rxTimer = now.Add(time.Duration(TimeoutMultiplier) * m.rxPeriod)
				lagNeedsUpdate = true
			case Expired:
				m.status = Defaulted
				m.partnerDefaulted = true
				m.partner = lacp.Info{State: lacp.StateDefaulted}
				m.rxTimer = time.Time{}
				lagNeedsUpdate = true
			}
		}

		if lagNeedsUpdate {
			if l.updateLag(lag) {
				changedMap[lag.name] = struct{}{}
			}
			for _, name := range lag.memberNames {
				mem := l.members[name]
				if mem.mayTx(lag) && (!mem.hasTxActor || mem.actor != mem.lastTxActor) {
					emissions = append(emissions, l.emitLACPDU(mem))
					mem.txTimer = now.Add(mem.txPeriod)
				}
			}
		}
	}

	for _, lagName := range sortedKeys(l.lags) {
		lag := l.lags[lagName]
		if lag.cfg.LACP.Mode == Off {
			continue
		}
		for _, name := range lag.memberNames {
			m := l.members[name]
			if !m.carrier || m.txTimer.IsZero() || m.txTimer.After(now) {
				continue
			}
			if m.mayTx(lag) {
				emissions = append(emissions, l.emitLACPDU(m))
			}
			m.txTimer = now.Add(m.txPeriod)
		}
	}

	var changed []string
	for k := range changedMap {
		changed = append(changed, k)
	}
	slices.Sort(changed)

	return Effects{
		Emissions: emissions,
		Changed:   changed,
	}
}

// NextWake returns the earliest scheduled time at which the layer needs to be woken,
// and reports whether any timer is currently active.
func (l *Layer) NextWake() (time.Time, bool) {
	var next time.Time
	hasTimer := false

	update := func(t time.Time) {
		if t.IsZero() {
			return
		}
		// A timer the layer has not been woken for is due now, not never.
		if !l.now.IsZero() && t.Before(l.now) {
			t = l.now
		}
		if !hasTimer || t.Before(next) {
			next = t
			hasTimer = true
		}
	}

	for _, m := range l.members {
		if m.hasPendingLink {
			update(m.linkDelayTimer)
		}
		if m.carrier {
			lag := l.lags[m.lagName]
			if lag.cfg.LACP.Mode != Off {
				update(m.rxTimer)
				if m.mayTx(lag) {
					update(m.txTimer)
				}
			}
		}
	}

	return next, hasTimer
}

// Info returns the runtime aggregation status of the named LAG.
func (l *Layer) Info(lagName string) Info {
	lag, ok := l.lags[lagName]
	if !ok {
		return Info{}
	}

	var pending []Pending
	for _, name := range lag.memberNames {
		if p, ok := pendingEntry(l.members[name]); ok {
			pending = append(pending, p)
		}
	}

	return Info{
		Mode:                  lag.cfg.Mode,
		Enabled:               slices.Clone(lag.enabledOrder),
		Attached:              slices.Clone(lag.attachedMembers),
		PartnerSystemID:       lag.partnerSysID,
		PartnerSystemPriority: lag.partnerSysPrio,
		PartnerKey:            lag.partnerKey,
		Up:                    len(lag.enabledOrder) > 0,
		Pending:               pending,
	}
}

// pendingEntry reports the single reason, if any, that a member port may
// still change state on its own. A member matching more than one condition
// reports the one checked first below, since Info carries one entry per
// member.
func pendingEntry(m *memberState) (Pending, bool) {
	switch {
	case m.hasPendingLink:
		return Pending{Member: m.name, Cause: PendingLinkDelay, At: m.linkDelayTimer}, true
	case m.status == Expired:
		return Pending{Member: m.name, Cause: PendingPartnerExpired, At: m.rxTimer}, true
	case m.attached && m.partner.State&lacp.StateSynchronization == 0:
		return Pending{Member: m.name, Cause: PendingUnsynchronized, At: m.rxTimer}, true
	default:
		return Pending{}, false
	}
}

// PortInfo returns the runtime aggregation status of the named member port.
func (l *Layer) PortInfo(member string) MemberInfo {
	m, ok := l.members[member]
	if !ok {
		return MemberInfo{}
	}

	return MemberInfo{
		LinkUp:     m.linkUp,
		Enabled:    m.enabled,
		Attached:   m.attached,
		Actor:      m.actor,
		Partner:    m.partner,
		Status:     m.status,
		LACPDUsTx:  m.lacpdusTx,
		LACPDUsRx:  m.lacpdusRx,
		BadLACPDUs: m.badLACPDUs,
	}
}

// BadLACPDU records an undecodable LACPDU received on the named member port.
func (l *Layer) BadLACPDU(member string) {
	if m, ok := l.members[member]; ok {
		m.badLACPDUs++
	}
}

// RetentionKey returns a canonical encoding of every normalized input the layer's
// runtime state depends on: its own configuration as Diff sees it, member link states,
// and the switch system ID.
func RetentionKey(cfg Config, ports port.Table, systemID netaddr.MAC) string {
	hasLagPorts := false
	for _, p := range ports.Ports() {
		if p.Kind == port.Lag {
			hasLagPorts = true
			break
		}
	}
	if len(cfg.LAGs) == 0 && !hasLagPorts {
		return ""
	}
	norm := cfg.Normalize(ports, systemID)
	var b strings.Builder
	b.WriteString("config=")
	for _, name := range sortedKeys(norm.LAGs) {
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(string(snapshotLAG(norm.LAGs[name])))
		b.WriteByte(';')
	}

	b.WriteString("\nmember-state=")
	var memberNames []string
	for _, p := range ports.Ports() {
		if p.LagParent != "" {
			memberNames = append(memberNames, p.Name)
		}
	}
	slices.Sort(memberNames)
	for _, name := range memberNames {
		p, _ := ports.Port(name)
		fmt.Fprintf(&b, "%s:parent=%s,admin=%s,oper=%s;", name, p.LagParent, p.AdminStatus, p.OperStatus)
	}

	b.WriteString("\nsystem-id=")
	b.WriteString(systemID.String())

	return b.String()
}
