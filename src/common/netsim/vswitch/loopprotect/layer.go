package loopprotect

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Emission describes a probe frame to transmit out one virtual switch port
// carrying one VLAN. The VID rides alongside the frame because the switch,
// not this layer, applies the port's VLAN egress tagging. Probe carries the
// same value Frame was encoded from; a caller that resolves VID 0 to a real
// VLAN (the switch does, for a port with no configured VLANs) must set
// Probe.VID to that resolved value and re-encode before transmitting, so the
// payload names the VLAN the probe actually rides rather than the VID 0 it
// was built with.
type Emission struct {
	Port  string
	VID   vlan.ID
	Probe Probe
	Frame ethernet.Frame
}

// Effects lists the probe frames a Wake call emits and the ports whose
// learned forwarding table entries must be flushed as a result of a
// loop-protection action taking effect.
type Effects struct {
	Emissions []Emission
	Flush     []FlushTarget
}

// FlushTarget names a port whose learned forwarding table entries must be
// flushed, and which FIDs on it are stale. An empty FIDs means every FID.
// Receive returns one target, naming the port whose action it just applied,
// only on the transition into a forwarding-denying action (Block or
// Disable): the entries the loop taught that port are stale, exactly as
// spanning tree flushes on a topology change. NoLearn keeps forwarding, so
// it flushes nothing, and a repeat probe for a port already carrying an
// applied action flushes nothing either.
type FlushTarget struct {
	Port string
	FIDs []vlan.ID
}

// PortInfo summarizes the runtime loop-protection status of one port.
type PortInfo struct {
	// Action is the action currently applied to the port, or the empty
	// string when no action is applied.
	Action Action

	// InterVLAN reports whether the most recently returned probe on this
	// port was classified into a different VLAN than the one it was sent on.
	InterVLAN bool

	// Recurrences counts how many times a Timer recovery lifted and a later
	// returned probe reapplied the action.
	Recurrences uint64
}

// portState is the mutable runtime state the layer tracks for one protected port.
type portState struct {
	cfg Port

	applied     bool
	everApplied bool
	waitUntil   time.Time
	recurrences uint64
	interVLAN   bool
	seq         uint32
}

func (p *portState) clone() *portState {
	cp := *p
	cp.cfg.VLANs = slices.Clone(p.cfg.VLANs)

	return &cp
}

// Layer implements netsim's loop-protection capability: a probe codec, a
// per-port timer driven by the fabric clock, and the bridge.Gate pair that
// answers whether a protected port learns and forwards. It runs
// deterministically in memory without background goroutines or wall clocks;
// time advances through explicit, time-stamped calls to Receive, Wake, and
// LinkChange.
type Layer struct {
	mac      netaddr.MAC
	interval time.Duration

	ports       map[string]*portState
	sortedNames []string

	armed       bool
	nextProbeAt time.Time
}

// New constructs a loop-protection layer from the given configuration, port
// table, and switch MAC. It returns an error if the configuration is invalid
// against the ports.
func New(cfg Config, ports port.Table, mac netaddr.MAC) (*Layer, error) {
	if err := cfg.Validate(ports); err != nil {
		return nil, err
	}

	normalized := cfg.Normalize()

	names := make([]string, 0, len(normalized.Ports))
	for name := range normalized.Ports {
		names = append(names, name)
	}
	slices.Sort(names)

	l := &Layer{
		mac:         mac,
		interval:    normalized.Interval,
		ports:       make(map[string]*portState, len(names)),
		sortedNames: names,
	}
	for _, name := range names {
		l.ports[name] = &portState{cfg: normalized.Ports[name]}
	}

	return l, nil
}

// Clone creates an independent deep copy of the loop-protection layer,
// preserving every port's applied action and recovery timer.
func (l *Layer) Clone() *Layer {
	cp := &Layer{
		mac:         l.mac,
		interval:    l.interval,
		ports:       make(map[string]*portState, len(l.ports)),
		sortedNames: slices.Clone(l.sortedNames),
		armed:       l.armed,
		nextProbeAt: l.nextProbeAt,
	}
	for name, ps := range l.ports {
		cp.ports[name] = ps.clone()
	}

	return cp
}

// Learns reports whether the named port learns MAC addresses into the
// filtering database for the given VLAN. An untracked port, or a tracked
// port with no action currently applied, always learns. Every applied
// action denies learning.
func (l *Layer) Learns(portName string, _ vlan.ID) bool {
	ps, ok := l.ports[portName]
	if !ok || !ps.applied {
		return true
	}

	return false
}

// Forwards reports whether the named port forwards traffic carrying the
// given VLAN. An untracked port, a tracked port with no action currently
// applied, or a port applying NoLearn (which stops learning but keeps
// forwarding) forwards; Block and Disable deny it.
func (l *Layer) Forwards(portName string, _ vlan.ID) bool {
	ps, ok := l.ports[portName]
	if !ok || !ps.applied {
		return true
	}

	return ps.cfg.Action == NoLearn
}

// PortInfo returns runtime loop-protection information for the named port.
// If the port is not tracked by the layer, PortInfo returns a zero value.
func (l *Layer) PortInfo(portName string) PortInfo {
	ps, ok := l.ports[portName]
	if !ok {
		return PortInfo{}
	}

	info := PortInfo{
		InterVLAN:   ps.interVLAN,
		Recurrences: ps.recurrences,
	}
	if ps.applied {
		info.Action = ps.cfg.Action
	}

	return info
}

// Receive processes a probe that returned to this switch, naming this
// switch's own port as its sender. It applies that port's configured action
// to that port, marks PortInfo.InterVLAN when the classified vid disagrees
// with the payload's own VID (an inter-VLAN loop), and advances the
// recovery timer per the port's recovery mode. A probe naming a port this
// layer does not track is ignored. The ingress port the probe returned on
// belongs to the caller's trace step, not to this layer.
//
// Before evaluating the probe, Receive expires an elapsed Timer or
// LoopCleared recovery window using the same rule Wake uses, so a probe
// delivered at or after the window's expiry sees the action as already
// lifted rather than reading a stale applied state that Wake alone would
// have caught later.
func (l *Layer) Receive(now time.Time, vid vlan.ID, p Probe) Effects {
	ps, ok := l.ports[p.Port]
	if !ok {
		return Effects{}
	}

	ps.interVLAN = vid != p.VID

	if ps.applied && !ps.waitUntil.IsZero() {
		switch ps.cfg.Recovery.Mode {
		case Timer, LoopCleared:
			if !now.Before(ps.waitUntil) {
				ps.applied = false
				ps.waitUntil = time.Time{}
			}
		}
	}

	wasApplied := ps.applied

	switch ps.cfg.Recovery.Mode {
	case LoopCleared:
		if !ps.applied {
			ps.applied = true
		}
		ps.waitUntil = now.Add(ps.cfg.Recovery.Duration)

	case Timer:
		if !ps.applied {
			if ps.everApplied {
				ps.recurrences++
			}
			ps.applied = true
			ps.everApplied = true
			ps.waitUntil = now.Add(ps.cfg.Recovery.Duration)
		}
		// A returned probe while the action is still applied changes
		// nothing: Timer lifts on its own schedule regardless of whether
		// the loop persists, and adds no backoff.

	case Manual:
		ps.applied = true
	}

	if !wasApplied && ps.applied && ps.cfg.Action != NoLearn {
		return Effects{Flush: []FlushTarget{{Port: p.Port}}}
	}

	return Effects{}
}

// Wake advances every port's Timer and LoopCleared recovery windows past
// now, lifting an action whose wait has elapsed, and emits one probe per
// protected port per VLAN, walking ports in sorted order so the emissions
// are ordered, except for a port currently applying Disable: that port
// emits nothing. A Disable-configured port that has not yet had the action
// applied still probes, because only a returned probe can apply it in the
// first place. A port with no VLANs emits one probe for VID 0, which the
// switch sends on the port's PVID.
//
// Probes go out only once the configured interval is due. A switch wakes its
// layers together, so this runs at every spanning tree hello and at every
// recovery expiry as well; emitting on each of those would probe far faster
// than the configuration asks for.
func (l *Layer) Wake(now time.Time) Effects {
	if !l.armed {
		l.armed = true
		l.nextProbeAt = now.Add(l.interval)
	}

	for _, name := range l.sortedNames {
		ps := l.ports[name]
		if !ps.applied || ps.waitUntil.IsZero() {
			continue
		}
		switch ps.cfg.Recovery.Mode {
		case Timer, LoopCleared:
			if !now.Before(ps.waitUntil) {
				ps.applied = false
				ps.waitUntil = time.Time{}
			}
		}
	}

	if now.Before(l.nextProbeAt) {
		return Effects{}
	}

	var emissions []Emission

	for _, name := range l.sortedNames {
		ps := l.ports[name]
		if ps.applied && ps.cfg.Action == Disable {
			continue
		}

		vids := ps.cfg.VLANs
		if len(vids) == 0 {
			vids = []vlan.ID{0}
		}

		for _, vid := range vids {
			seq := ps.seq
			ps.seq++

			probe := Probe{
				OriginMAC: l.mac,
				VID:       vid,
				Sequence:  seq,
				Port:      name,
			}
			emissions = append(emissions, Emission{
				Port:  name,
				VID:   vid,
				Probe: probe,
				Frame: Encode(probe, l.mac),
			})
		}
	}

	l.nextProbeAt = now.Add(l.interval)

	return Effects{Emissions: emissions}
}

// NextWake returns the earliest scheduled time at which the layer needs to
// be woken, and reports whether any timer is currently active.
func (l *Layer) NextWake() (time.Time, bool) {
	if !l.armed {
		return time.Time{}, false
	}

	next := l.nextProbeAt
	hasTimer := !next.IsZero()

	for _, name := range l.sortedNames {
		ps := l.ports[name]
		if !ps.applied || ps.waitUntil.IsZero() {
			continue
		}
		switch ps.cfg.Recovery.Mode {
		case Timer, LoopCleared:
			if !hasTimer || ps.waitUntil.Before(next) {
				next = ps.waitUntil
				hasTimer = true
			}
		}
	}

	return next, hasTimer
}

// LinkChange reports a link state transition on the named port. The first
// call, whatever port it names, arms the probe emission cadence. A Manual
// action clears on the down transition, which is what makes a link cycle
// (down, then up) its recovery; an up report with no preceding down leaves
// the action applied. A port this layer does not track is otherwise ignored.
func (l *Layer) LinkChange(now time.Time, portName string, up bool) Effects {
	if !l.armed {
		l.armed = true
		l.nextProbeAt = now.Add(l.interval)
	}

	ps, ok := l.ports[portName]
	if !ok {
		return Effects{}
	}

	if !up && ps.applied && ps.cfg.Recovery.Mode == Manual {
		ps.applied = false
		ps.waitUntil = time.Time{}
	}

	return Effects{}
}

// Clear manually lifts the action applied to the named port and resets its
// Timer recurrence tracking, so a later returned probe treats the next
// applied action as the first one rather than counting it in
// PortInfo.Recurrences: Clear proves the operator addressed the port, not
// that a Timer recovery lifted it. It reports whether the port was tracked
// and had an action applied.
func (l *Layer) Clear(_ time.Time, portName string) bool {
	ps, ok := l.ports[portName]
	if !ok || !ps.applied {
		return false
	}

	ps.applied = false
	ps.everApplied = false
	ps.waitUntil = time.Time{}

	return true
}
