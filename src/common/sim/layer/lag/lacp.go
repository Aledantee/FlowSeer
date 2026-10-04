package lag

import (
	"bytes"
	"cmp"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

const aggregateWaitTime = 2 * time.Second

type muxState string

const (
	muxDetached               muxState = "DETACHED"
	muxWaiting                muxState = "WAITING"
	muxAttached               muxState = "ATTACHED"
	muxCollectingDistributing muxState = "COLLECTING_DISTRIBUTING"
	muxStandby                muxState = "STANDBY"
)

// Status represents the LACP receive state of a member port.
type Status string

const (
	// PortDisabled indicates carrier is absent and learned partner identity is retained.
	PortDisabled Status = "PortDisabled"

	// Current indicates partner information is up to date.
	Current Status = "Current"

	// Expired indicates the member is waiting three seconds for fresh partner information.
	Expired Status = "Expired"

	// Defaulted indicates partner information is defaulted.
	Defaulted Status = "Defaulted"
)

func sameAggregationPort(a, b lacp.Info) bool {
	return comparePartner(a, b) == 0 && a.State&lacp.StateAggregation == b.State&lacp.StateAggregation
}

func (m *memberState) recordDefault() {
	m.partner = lacp.Info{State: lacp.StateSynchronization | lacp.StateCollecting}
	m.partnerDefaulted = true
}

func (m *memberState) disableReceive() {
	m.status = PortDisabled
	m.partner.State &^= lacp.StateSynchronization
	m.rxTimer = time.Time{}
	m.txTimer = time.Time{}
	m.hasTxActor = false
}

func (m *memberState) expireReceive(now time.Time) {
	m.status = Expired
	m.partner.State = m.partner.State&^lacp.StateSynchronization | lacp.StateShortTimeout
	m.actorExpired = true
	m.rxTimer = now.Add(time.Duration(timeoutMultiplier) * fastPeriod)
}

func (m *memberState) mayTx(lag *lagState) bool {
	if !m.carrier || lag.cfg.LACP.Mode == Off {
		return false
	}
	if lag.cfg.LACP.Mode == Active {
		return true
	}

	return m.partner.State&lacp.StateActive != 0 && (m.status == Current || m.status == Expired)
}

func (m *memberState) updateActorInfo(lag *lagState) {
	var state lacp.State

	if lag.cfg.LACP.Mode == Active {
		state |= lacp.StateActive
	}
	if lag.cfg.LACP.Fast {
		state |= lacp.StateShortTimeout
	}
	state |= lacp.StateAggregation

	if m.attached {
		state |= lacp.StateSynchronization
	}
	if m.enabled {
		state |= lacp.StateCollecting | lacp.StateDistributing
	}
	if m.status == Defaulted || m.partnerDefaulted {
		state |= lacp.StateDefaulted
	}
	if m.actorExpired {
		state |= lacp.StateExpired
	}

	key := m.cfg.Key
	if key == 0 {
		key = lag.cfg.LACP.Key
	}
	prio := m.cfg.Priority
	if prio == 0 {
		prio = defaultPortPriority
	}

	m.actor = lacp.Info{
		SystemPriority: lag.cfg.LACP.SystemPriority,
		SystemID:       lag.cfg.LACP.SystemID,
		Key:            key,
		PortPriority:   prio,
		PortID:         m.portID,
		State:          state,
	}
}

func comparePartner(a, b lacp.Info) int {
	if a.SystemPriority != b.SystemPriority {
		return cmp.Compare(a.SystemPriority, b.SystemPriority)
	}
	if c := bytes.Compare(a.SystemID[:], b.SystemID[:]); c != 0 {
		return c
	}
	if a.Key != b.Key {
		return cmp.Compare(a.Key, b.Key)
	}
	if a.PortPriority != b.PortPriority {
		return cmp.Compare(a.PortPriority, b.PortPriority)
	}

	return cmp.Compare(a.PortID, b.PortID)
}

// updateEnabledOrder reconciles the OVS-style enabled-member list against a
// freshly computed target set. A member leaving the target is removed,
// leaving the relative order of the rest untouched (the positions bucket
// selection rotated stay valid). A member newly in the target joins the back;
// several joining in one call join in name order
// (bond_enable_member: ovs_list_insert before the list head, called once per
// member in iteration order, so a name-ordered batch ends in name order at
// the tail).
func updateEnabledOrder(order, target []string) []string {
	targetSet := make(map[string]struct{}, len(target))
	for _, name := range target {
		targetSet[name] = struct{}{}
	}

	inOrder := make(map[string]struct{}, len(order))
	kept := make([]string, 0, len(order))
	for _, name := range order {
		inOrder[name] = struct{}{}
		if _, ok := targetSet[name]; ok {
			kept = append(kept, name)
		}
	}

	var added []string
	for _, name := range target {
		if _, ok := inOrder[name]; !ok {
			added = append(added, name)
		}
	}
	slices.Sort(added)

	return append(kept, added...)
}

func (l *Layer) updateLag(lag *lagState) bool {
	oldEnabled := slices.Clone(lag.enabledOrder)
	oldAttached := slices.Clone(lag.attachedMembers)
	oldActors := make(map[string]lacp.Info, len(lag.memberNames))
	oldSelected := make(map[string]bool, len(lag.memberNames))
	oldMux := make(map[string]muxState, len(lag.memberNames))
	for _, name := range lag.memberNames {
		m := l.members[name]
		oldActors[name] = m.actor
		oldSelected[name] = m.selected
		oldMux[name] = m.mux
	}

	if lag.cfg.LACP.Mode == Off {
		var enabled []string
		for _, name := range lag.memberNames {
			m := l.members[name]
			m.selected = false
			m.mux = muxDetached
			m.aggregateWait = time.Time{}
			m.attached = false
			m.enabled = m.linkUp
			if m.enabled {
				enabled = append(enabled, name)
			}
		}

		if lag.cfg.MinLinks != 0 && len(enabled) < lag.cfg.MinLinks {
			enabled = nil
			for _, name := range lag.memberNames {
				l.members[name].enabled = false
			}
		}
		lag.enabledOrder = updateEnabledOrder(lag.enabledOrder, enabled)
		lag.attachedMembers = nil
		lag.partnerSysID = netaddr.MAC{}
		lag.partnerSysPrio = 0
		lag.partnerKey = 0
		lag.aggregateWait = time.Time{}

		return l.lagChanged(lag, oldEnabled, oldAttached, oldActors, oldSelected, oldMux)
	}

	target, lead := l.selectionTarget(lag)
	current := selectedMembers(lag, l.members)
	if !slices.Equal(current, target) {
		targetSet := make(map[string]struct{}, len(target))
		for _, name := range target {
			targetSet[name] = struct{}{}
		}
		for _, name := range lag.memberNames {
			l.members[name].selected = false
			if _, ok := targetSet[name]; ok {
				l.members[name].selected = true
			}
		}
		if len(target) > 0 {
			lag.aggregateWait = l.now.Add(aggregateWaitTime)
		} else {
			lag.aggregateWait = time.Time{}
		}
	}
	if !lag.aggregateWait.IsZero() && !lag.aggregateWait.After(l.now) {
		lag.aggregateWait = time.Time{}
	}

	if lead != nil {
		lag.partnerSysID = lead.partner.SystemID
		lag.partnerSysPrio = lead.partner.SystemPriority
		lag.partnerKey = lead.partner.Key
	} else if len(target) == 0 {
		lag.partnerSysID = netaddr.MAC{}
		lag.partnerSysPrio = 0
		lag.partnerKey = 0
	}

	waiting := !lag.aggregateWait.IsZero()
	for _, name := range lag.memberNames {
		m := l.members[name]
		m.aggregateWait = lag.aggregateWait
		m.mux = muxDetached
		if !m.selected {
			continue
		}
		switch {
		case waiting:
			m.mux = muxWaiting
		case !m.carrier:
			m.mux = muxDetached
		case !m.linkUp:
			m.mux = muxStandby
		default:
			m.mux = muxAttached
		}
	}

	ready := 0
	for _, name := range lag.memberNames {
		m := l.members[name]
		if m.mux == muxAttached && m.partner.State&lacp.StateSynchronization != 0 {
			ready++
		}
	}
	minLinksMet := lag.cfg.MinLinks == 0 || ready >= lag.cfg.MinLinks
	var attached []string
	var enabled []string
	for _, name := range lag.memberNames {
		m := l.members[name]
		if m.mux == muxAttached && m.partner.State&lacp.StateSynchronization != 0 && minLinksMet {
			m.mux = muxCollectingDistributing
		}
		m.attached = m.mux == muxAttached || m.mux == muxCollectingDistributing
		m.enabled = m.mux == muxCollectingDistributing
		if m.attached {
			attached = append(attached, name)
		}
		if m.enabled {
			enabled = append(enabled, name)
		}
	}
	lag.attachedMembers = attached
	lag.enabledOrder = updateEnabledOrder(lag.enabledOrder, enabled)

	for _, name := range lag.memberNames {
		l.members[name].updateActorInfo(lag)
	}

	return l.lagChanged(lag, oldEnabled, oldAttached, oldActors, oldSelected, oldMux)
}

func (l *Layer) lagChanged(lag *lagState, oldEnabled, oldAttached []string, oldActors map[string]lacp.Info, oldSelected map[string]bool, oldMux map[string]muxState) bool {
	if !slices.Equal(oldEnabled, lag.enabledOrder) || !slices.Equal(oldAttached, lag.attachedMembers) {
		return true
	}
	for _, name := range lag.memberNames {
		m := l.members[name]
		if m.actor != oldActors[name] || m.selected != oldSelected[name] || m.mux != oldMux[name] {
			return true
		}
	}

	return false
}

func selectedMembers(lag *lagState, members map[string]*memberState) []string {
	var selected []string
	for _, name := range lag.memberNames {
		if members[name].selected {
			selected = append(selected, name)
		}
	}

	return selected
}

func (l *Layer) selectionTarget(lag *lagState) ([]string, *memberState) {
	var lead *memberState
	for _, name := range lag.memberNames {
		m := l.members[name]
		if !l.selectionCandidate(lag, m) {
			continue
		}
		if lead == nil || comparePartner(m.partner, lead.partner) < 0 ||
			(comparePartner(m.partner, lead.partner) == 0 && m.name < lead.name) {
			lead = m
		}
	}
	if lead != nil {
		var target []string
		for _, name := range lag.memberNames {
			m := l.members[name]
			if l.selectionCandidate(lag, m) && l.partnerInGroup(m.partner, lead.partner) {
				target = append(target, name)
				continue
			}
			if !m.carrier && m.selected && l.partnerInGroup(m.partner, lead.partner) {
				target = append(target, name)
			}
		}
		if lead.partner.State&lacp.StateAggregation == 0 {
			target = []string{lead.name}
		}

		return target, lead
	}

	current := selectedMembers(lag, l.members)
	if len(current) > 0 {
		if lag.partnerSysID != (netaddr.MAC{}) || lag.partnerKey != 0 {
			var retained []string
			for _, name := range current {
				m := l.members[name]
				if l.partnerMatchesStoredGroup(m.partner, lag) {
					retained = append(retained, name)
				}
			}
			if len(retained) > 0 {
				return retained, nil
			}
		} else {
			var retained []string
			for _, name := range current {
				if l.members[name].linkUp {
					retained = append(retained, name)
				}
			}
			if len(retained) > 0 {
				return retained, nil
			}
		}
	}

	if !lag.cfg.LACP.Fallback {
		return nil, nil
	}
	var fallback []string
	for _, name := range lag.memberNames {
		m := l.members[name]
		if m.linkUp && m.partnerDefaulted && (m.status == Defaulted || m.status == PortDisabled) && l.memberKey(lag, m) == lag.cfg.LACP.Key {
			fallback = append(fallback, name)
		}
	}
	if len(fallback) == 0 {
		return nil, nil
	}
	chosen := slices.Min(fallback)
	if lag.cfg.Primary != "" && slices.Contains(fallback, lag.cfg.Primary) {
		chosen = lag.cfg.Primary
	}

	return []string{chosen}, nil
}

func (l *Layer) selectionCandidate(lag *lagState, m *memberState) bool {
	if !m.carrier || !m.partnerLearned || m.partnerDefaulted || (m.status != Current && m.status != Expired) || l.memberKey(lag, m) != lag.cfg.LACP.Key {
		return false
	}
	if m.partner.SystemID != m.actor.SystemID {
		return true
	}
	for _, name := range lag.memberNames {
		peer := l.members[name]
		if peer == m || peer.actor.PortID != m.partner.PortID {
			continue
		}

		return m.name < peer.name
	}

	return true
}

func (l *Layer) memberKey(lag *lagState, m *memberState) uint16 {
	if m.cfg.Key != 0 {
		return m.cfg.Key
	}
	return lag.cfg.LACP.Key
}

func (l *Layer) partnerInGroup(a, b lacp.Info) bool {
	if a.State&lacp.StateAggregation == 0 || b.State&lacp.StateAggregation == 0 {
		return a == b
	}

	return a.SystemPriority == b.SystemPriority && a.SystemID == b.SystemID && a.Key == b.Key
}

func (l *Layer) partnerMatchesStoredGroup(info lacp.Info, lag *lagState) bool {
	return info.SystemPriority == lag.partnerSysPrio && info.SystemID == lag.partnerSysID && info.Key == lag.partnerKey
}
