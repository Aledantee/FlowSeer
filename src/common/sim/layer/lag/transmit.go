package lag

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

type periodicState uint8

const (
	noPeriodic periodicState = iota
	fastPeriodic
	slowPeriodic
)

func (m *memberState) updatePeriodic(now time.Time, lag *lagState) {
	if !m.mayTx(lag) {
		m.periodic = noPeriodic
		m.periodicTimer = time.Time{}
		m.ntt = false
		return
	}

	short := m.partner.State&lacp.StateShortTimeout != 0
	if m.periodic == noPeriodic {
		m.periodic = fastPeriodic
		m.periodicTimer = now.Add(fastPeriod)
	}
	if m.periodic == fastPeriodic && !short {
		m.periodic = slowPeriodic
		m.periodicTimer = now.Add(slowPeriod)
		return
	}
	if (m.periodic == slowPeriodic && short) || !m.periodicTimer.After(now) {
		m.ntt = true
		m.periodic = slowPeriodic
		period := slowPeriod
		if short {
			m.periodic = fastPeriodic
			period = fastPeriod
		}
		m.periodicTimer = now.Add(period)
	}
}

func (m *memberState) transmitWake(now time.Time) time.Time {
	if m.txCount == len(m.txTimes) {
		wake := m.txTimes[0].Add(fastPeriod)
		if wake.After(now) {
			return wake
		}
	}
	return now
}

func (l *Layer) transmitLag(lag *lagState) []layer.Emission {
	var emissions []layer.Emission
	for _, name := range lag.memberNames {
		m := l.members[name]
		m.updatePeriodic(l.now, lag)
		if !m.ntt || m.transmitWake(l.now).After(l.now) {
			continue
		}
		for m.txCount > 0 && !m.txTimes[0].Add(fastPeriod).After(l.now) {
			copy(m.txTimes[:], m.txTimes[1:])
			m.txCount--
		}
		pdu := lacp.PDU{Actor: m.actor, Partner: m.partner}
		emissions = append(emissions, layer.Emission{Port: m.name, Frame: lacp.Encode(pdu, l.memberSource(m))})
		m.ntt = false
		m.txTimes[m.txCount] = l.now
		m.txCount++
		m.lacpdusTx++
	}
	return emissions
}
