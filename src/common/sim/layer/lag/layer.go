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
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// ReasonUnsupportedLACPDU indicates that an Ethernet frame carried an unparseable or unsupported LACPDU.
const ReasonUnsupportedLACPDU trace.Reason = "unsupported-lacpdu"

// LayerName identifies the link aggregation layer.
const LayerName trace.Layer = "lag"

// Rule constants produced by lag.
const (
	RuleEgressNoMember    trace.RuleID = "lag.egress.no_member"
	RuleLACPDUUnsupported trace.RuleID = "lag.lacpdu.unsupported"
	RuleLACPDUAdmit       trace.RuleID = "lag.lacpdu.admit"
	RuleMarkerRespond     trace.RuleID = "lag.marker.respond"
)

// Info summarizes the runtime aggregation status of one link aggregation group.
type Info struct {
	Mode                  Mode
	Enabled               []string
	Attached              []string
	PartnerSystemID       netaddr.MAC
	PartnerSystemPriority uint16
	PartnerKey            uint16
	Up                    bool

	// pending names member ports that may still change state on their own
	// (a running link delay, an Expired partner, or an attached partner
	// without synchronization) and when. A pending member changes no other
	// field of Info; the answer above is definite as of now.
	pending []pending
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

// pendingCause identifies why a member port may still change state.
type pendingCause string

const (
	// pendingLinkDelay marks a member whose up or down delay timer is running.
	pendingLinkDelay pendingCause = "link-delay"

	// pendingPartnerExpired marks a member whose partner information is Expired
	// and will move to Defaulted when its receive timer elapses.
	pendingPartnerExpired pendingCause = "partner-expired"

	// pendingUnsynchronized marks a member attached to the lead partner that has
	// not yet advertised synchronization.
	pendingUnsynchronized pendingCause = "unsynchronized"

	// pendingAggregateWait marks a selected member waiting for the aggregator
	// selection window to close.
	pendingAggregateWait pendingCause = "aggregate-wait"
)

// pending names one member port that may still change state on its own, and
// the time at which that is currently scheduled to happen.
type pending struct {
	Member string
	Cause  pendingCause
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
	aggregateWait   time.Time

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
	aggregateWait    time.Time
	attached         bool
	enabled          bool
	status           Status
	partnerDefaulted bool
	partnerLearned   bool
	actorExpired     bool
	partner          lacp.Info
	actor            lacp.Info
	rxTimer          time.Time
	periodic         periodicState
	periodicTimer    time.Time
	ntt              bool
	txTimes          [3]time.Time
	txCount          int
	lacpdusTx        uint64
	lacpdusRx        uint64
	badLACPDUs       uint64

	// needsReselect records a Partner identity change for Selection Logic.
	needsReselect bool
	selected      bool
	mux           muxState
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

// New constructs a link aggregation layer from the given configuration and environment.
// It returns an error if the configuration is invalid against the ports.
func New(cfg Config, env layer.Env) (*Layer, error) {
	norm := cfg.Normalize(env)
	if err := norm.Validate(env); err != nil {
		return nil, err
	}
	return newLayer(norm, env.Ports, env.MAC), nil
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

		for _, memName := range memNames {
			mCfg := lagCfg.Members[memName]
			ms := &memberState{
				name:          memName,
				lagName:       lagName,
				cfg:           mCfg,
				needsReselect: true,
				mux:           muxDetached,
			}
			ms.recordDefault()
			ms.disableReceive()
			layer.members[memName] = ms
		}
	}

	for idx, name := range sortedKeys(layer.members) {
		m := layer.members[name]
		m.portID = uint16(idx + 1)
		m.updateActorInfo(layer.lags[m.lagName])
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

func (l *Layer) memberSource(m *memberState) netaddr.MAC {
	src := l.lags[m.lagName].cfg.LACP.SystemID
	if src == (netaddr.MAC{}) {
		return l.systemID
	}
	return src
}

// LinkChange informs the layer that a member port's link transitioned up or down.
// A zero delay applies immediately; a non-zero delay arms a timer.
func (l *Layer) LinkChange(now time.Time, member string, up bool) layer.Effects {
	l.now = now
	m, ok := l.members[member]
	if !ok {
		return layer.Effects{}
	}
	lag := l.lags[m.lagName]

	// The carrier drives the protocol at once; the delay only decides when
	// the member carries traffic, so a partner is heard during an up delay.
	var fx layer.Effects
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

func mergeEffects(a, b layer.Effects) layer.Effects {
	out := layer.Effects{
		Emissions: append(a.Emissions, b.Emissions...),
		Changed:   append(a.Changed, b.Changed...),
	}
	slices.Sort(out.Changed)
	out.Changed = slices.Compact(out.Changed)

	return out
}

// setCarrier starts or stops the protocol on a member as its carrier comes
// and goes; the member's enablement follows the delayed link separately.
func (l *Layer) setCarrier(now time.Time, m *memberState, up bool) layer.Effects {
	lag := l.lags[m.lagName]
	if lag.cfg.LACP.Mode == Off {
		return layer.Effects{}
	}

	var changed []string

	if !up {
		m.disableReceive()
		if l.updateLag(lag) {
			changed = append(changed, lag.name)
		}

		return layer.Effects{Emissions: l.transmitLag(lag), Changed: changed}
	}

	// Figure 6-19 reaches SLOW_PERIODIC before EXPIRED requests Short,
	// so carrier up passes through PERIODIC_TX without waiting a second.
	m.updatePeriodic(now, lag)
	m.expireReceive(now)

	if l.updateLag(lag) {
		changed = append(changed, lag.name)
	}

	return layer.Effects{
		Emissions: l.transmitLag(lag),
		Changed:   changed,
	}
}

// applyLinkChange moves the member's delayed link to its carrier and
// re-evaluates what the LAG carries.
func (l *Layer) applyLinkChange(m *memberState) layer.Effects {
	if m.linkUp == m.carrier {
		return layer.Effects{}
	}
	m.linkUp = m.carrier
	lag := l.lags[m.lagName]

	var changed []string
	if l.updateLag(lag) {
		changed = append(changed, lag.name)
	}

	return layer.Effects{Emissions: l.transmitLag(lag), Changed: changed}
}

// Receive processes an incoming LACPDU on the named member port.
func (l *Layer) Receive(now time.Time, member string, pdu lacp.PDU) layer.Effects {
	l.now = now
	m, ok := l.members[member]
	if !ok {
		return layer.Effects{}
	}
	m.lacpdusRx++
	lag := l.lags[m.lagName]
	if !m.carrier || lag.cfg.LACP.Mode == Off {
		return layer.Effects{}
	}

	affectedLAGs := map[string]struct{}{lag.name: {}}
	for _, name := range sortedKeys(l.members) {
		other := l.members[name]
		if other.name != m.name && other.status == PortDisabled &&
			other.partner.SystemID == pdu.Actor.SystemID && other.partner.PortID == pdu.Actor.PortID {
			other.needsReselect = true
			other.partnerLearned = false
			other.recordDefault()
			other.actorExpired = false
			other.disableReceive()
			affectedLAGs[other.lagName] = struct{}{}
		}
	}

	if !sameAggregationPort(m.partner, pdu.Actor) {
		m.needsReselect = true
	}
	const echoState = lacp.StateActive | lacp.StateShortTimeout | lacp.StateSynchronization | lacp.StateAggregation
	actor, echo := m.actor, pdu.Partner
	actor.State &= echoState
	echo.State &= echoState
	if actor != echo {
		m.ntt = true
	}
	m.partner = pdu.Actor
	m.partner.State &^= lacp.StateSynchronization
	activelyMaintained := pdu.Actor.State&lacp.StateActive != 0 ||
		(m.actor.State&lacp.StateActive != 0 && pdu.Partner.State&lacp.StateActive != 0)
	if activelyMaintained && pdu.Actor.State&lacp.StateSynchronization != 0 &&
		(pdu.Actor.State&lacp.StateAggregation == 0 || sameAggregationPort(m.actor, pdu.Partner)) {
		m.partner.State |= lacp.StateSynchronization
	}
	m.partnerDefaulted = false
	m.partnerLearned = true
	m.status = Current
	m.actorExpired = false

	rxPeriod := slowPeriod
	if lag.cfg.LACP.Fast {
		rxPeriod = fastPeriod
	}
	m.rxTimer = now.Add(time.Duration(timeoutMultiplier) * rxPeriod)

	var changed []string
	var emissions []layer.Emission
	for _, lagName := range sortedKeys(affectedLAGs) {
		affected := l.lags[lagName]
		if l.updateLag(affected) {
			changed = append(changed, affected.name)
		}
		emissions = append(emissions, l.transmitLag(affected)...)
	}

	return layer.Effects{
		Emissions: emissions,
		Changed:   changed,
	}
}

// Advance advances timer-driven state to now, applying expired delays and timeouts.
func (l *Layer) Advance(now time.Time) layer.Effects {
	l.now = now
	var emissions []layer.Emission
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
		if !lag.aggregateWait.IsZero() && !lag.aggregateWait.After(now) {
			lagNeedsUpdate = true
		}
		for _, name := range lag.memberNames {
			m := l.members[name]
			if !m.carrier {
				continue
			}

			for !m.rxTimer.IsZero() && !m.rxTimer.After(now) {
				switch m.status {
				case Current:
					m.expireReceive(m.rxTimer)
				case Expired:
					if !sameAggregationPort(m.partner, lacp.Info{State: lacp.StateSynchronization | lacp.StateCollecting}) {
						m.needsReselect = true
					}
					m.recordDefault()
					m.status = Defaulted
					m.actorExpired = false
					m.rxTimer = time.Time{}
				}
				lagNeedsUpdate = true
			}
		}

		if lagNeedsUpdate {
			if l.updateLag(lag) {
				changedMap[lag.name] = struct{}{}
			}
		}
		emissions = append(emissions, l.transmitLag(lag)...)
	}

	var changed []string
	for k := range changedMap {
		changed = append(changed, k)
	}
	slices.Sort(changed)

	return layer.Effects{
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
					update(m.periodicTimer)
					if m.ntt {
						update(m.transmitWake(l.now))
					}
				}
			}
		}
	}
	for _, lag := range l.lags {
		if lag.cfg.LACP.Mode != Off {
			update(lag.aggregateWait)
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

	var pendingList []pending
	for _, name := range lag.memberNames {
		if p, ok := pendingEntry(l.members[name]); ok {
			pendingList = append(pendingList, p)
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
		pending:               pendingList,
	}
}

// pendingEntry reports the single reason, if any, that a member port may
// still change state on its own. A member matching more than one condition
// reports the one checked first below, since Info carries one entry per
// member.
func pendingEntry(m *memberState) (pending, bool) {
	switch {
	case m.mux == muxWaiting:
		return pending{Member: m.name, Cause: pendingAggregateWait, At: m.aggregateWait}, true
	case m.hasPendingLink:
		return pending{Member: m.name, Cause: pendingLinkDelay, At: m.linkDelayTimer}, true
	case m.status == Expired:
		return pending{Member: m.name, Cause: pendingPartnerExpired, At: m.rxTimer}, true
	case m.attached && m.partner.State&lacp.StateSynchronization == 0:
		return pending{Member: m.name, Cause: pendingUnsynchronized, At: m.rxTimer}, true
	default:
		return pending{}, false
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
// runtime state depends on: its own configuration normalized with env, member link states,
// and the switch system ID.
func RetentionKey(cfg Config, env layer.Env) string {
	hasLagPorts := false
	for _, p := range env.Ports.Ports() {
		if p.Kind == port.LAG {
			hasLagPorts = true
			break
		}
	}
	if len(cfg.LAGs) == 0 && !hasLagPorts {
		return ""
	}
	norm := cfg.Normalize(env)
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
	for _, p := range env.Ports.Ports() {
		if p.LagParent != "" {
			memberNames = append(memberNames, p.Name)
		}
	}
	slices.Sort(memberNames)
	for _, name := range memberNames {
		p, _ := env.Ports.Port(name)
		fmt.Fprintf(&b, "%s:parent=%s,admin=%s,oper=%s;", name, p.LagParent, p.AdminStatus, p.OperStatus)
	}

	b.WriteString("\nsystem-id=")
	b.WriteString(env.MAC.String())

	return b.String()
}
