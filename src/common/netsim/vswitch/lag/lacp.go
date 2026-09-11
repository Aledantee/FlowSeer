package lag

import (
	"bytes"
	"cmp"
	"slices"

	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

// Status represents the LACP receive state of a member port.
type Status string

const (
	// Current indicates partner information is up to date.
	Current Status = "Current"

	// Expired indicates the receive timer has expired and partner information is aged.
	Expired Status = "Expired"

	// Defaulted indicates partner information is defaulted.
	Defaulted Status = "Defaulted"
)

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
	if m.status == Expired {
		state |= lacp.StateExpired
	}

	key := m.cfg.Key
	if key == 0 {
		key = lag.cfg.LACP.Key
	}
	prio := m.cfg.Priority
	if prio == 0 {
		prio = DefaultPortPriority
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

func (l *Layer) updateLag(lag *lagState) bool {
	oldEnabled := slices.Clone(lag.enabledMembers)

	if lag.cfg.LACP.Mode == Off {
		var enabled []string
		for _, name := range lag.memberNames {
			m := l.members[name]
			m.attached = false
			m.enabled = m.linkUp
			if m.enabled {
				enabled = append(enabled, name)
			}
		}

		enabled = l.applyMinLinks(lag, enabled)

		lag.enabledMembers = enabled
		lag.attachedMembers = nil
		lag.partnerSysID = netaddr.MAC{}
		lag.partnerSysPrio = 0
		lag.partnerKey = 0

		return !slices.Equal(oldEnabled, lag.enabledMembers)
	}

	var lead *memberState
	for _, name := range lag.memberNames {
		m := l.members[name]
		if !m.linkUp || m.status == Defaulted || m.partnerDefaulted {
			continue
		}
		if lead == nil || comparePartner(m.partner, lead.partner) < 0 || (comparePartner(m.partner, lead.partner) == 0 && m.name < lead.name) {
			lead = m
		}
	}

	if lead == nil {
		lag.attachedMembers = nil
		lag.partnerSysID = netaddr.MAC{}
		lag.partnerSysPrio = 0
		lag.partnerKey = 0

		allDefaulted := true
		for _, name := range lag.memberNames {
			m := l.members[name]
			if m.status != Defaulted {
				allDefaulted = false
				break
			}
		}

		var enabled []string
		if lag.cfg.LACP.Fallback && allDefaulted {
			var upMembers []string
			for _, name := range lag.memberNames {
				m := l.members[name]
				if m.linkUp {
					upMembers = append(upMembers, name)
				}
			}
			if len(upMembers) > 0 {
				primary := lag.cfg.Primary
				chosen := upMembers[0]
				if primary != "" && slices.Contains(upMembers, primary) {
					chosen = primary
				}
				for _, name := range lag.memberNames {
					l.members[name].attached = false
					l.members[name].enabled = (name == chosen)
				}
				enabled = append(enabled, chosen)
			} else {
				for _, name := range lag.memberNames {
					l.members[name].attached = false
					l.members[name].enabled = false
				}
			}
		} else {
			for _, name := range lag.memberNames {
				l.members[name].attached = false
				l.members[name].enabled = false
			}
		}

		enabled = l.applyMinLinks(lag, enabled)

		lag.enabledMembers = enabled

		for _, name := range lag.memberNames {
			l.members[name].updateActorInfo(lag)
		}

		return !slices.Equal(oldEnabled, lag.enabledMembers)
	}

	leadPartner := lead.partner
	lag.partnerSysID = leadPartner.SystemID
	lag.partnerSysPrio = leadPartner.SystemPriority
	lag.partnerKey = leadPartner.Key

	var attached []string
	var enabled []string

	for _, name := range lag.memberNames {
		m := l.members[name]
		if m.linkUp && m.status != Defaulted && !m.partnerDefaulted &&
			m.partner.SystemID == leadPartner.SystemID && m.partner.Key == leadPartner.Key {
			m.attached = true
			attached = append(attached, name)
			if (m.partner.State & lacp.StateSynchronization) != 0 {
				m.enabled = true
				enabled = append(enabled, name)
			} else {
				m.enabled = false
			}
		} else {
			m.attached = false
			m.enabled = false
		}
	}

	enabled = l.applyMinLinks(lag, enabled)

	lag.attachedMembers = attached
	lag.enabledMembers = enabled

	for _, name := range lag.memberNames {
		l.members[name].updateActorInfo(lag)
	}

	return !slices.Equal(oldEnabled, lag.enabledMembers)
}

// applyMinLinks disables every enabled member when fewer than the minimum
// are enabled, since a LAG below its minimum carries nothing.
func (l *Layer) applyMinLinks(lag *lagState, enabled []string) []string {
	if lag.cfg.MinLinks == 0 || len(enabled) >= lag.cfg.MinLinks {
		return enabled
	}
	for _, name := range enabled {
		l.members[name].enabled = false
	}

	return nil
}
