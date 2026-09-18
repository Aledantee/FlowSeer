// Package mcast learns multicast group membership and router ports from IGMP and MLD control messages.
package mcast

import (
	"maps"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

const (
	// ReasonUnregistered indicates that flooding is disabled and no egress exists for an unregistered group.
	ReasonUnregistered trace.Reason = "unregistered"

	// ReasonNoRouterPort indicates that a multicast control report has no router port to receive it.
	ReasonNoRouterPort trace.Reason = "no-router-port"

	// ReasonBadControl indicates that a multicast control frame failed protocol validation.
	ReasonBadControl trace.Reason = "bad-control"
)

// SourceEntry is one source record's timer. A zero Expires is a real state — the source's
// timer has run to zero without the record being deleted — not an absent record.
type SourceEntry struct {
	Address netip.Addr
	Expires time.Time
}

// Entry is one learned (VLAN, group, port) router state.
// GroupExpires is meaningful only when Mode is Exclude. Sources is in address order.
type Entry struct {
	Group        netip.Addr
	Port         string
	Mode         FilterMode
	GroupExpires time.Time
	Sources      []SourceEntry
}

// Origin names who installed a router port record: configuration
// (Configured) or a learned query (Observed). It is independent of
// Lifetime: see [Lifetime].
type Origin string

const (
	// Configured is the zero value: cfg.RouterPorts installed the record.
	Configured Origin = ""

	// Observed is a record a learned query installed.
	Observed Origin = "observed"
)

// Lifetime names whether a router port record ages out on its own.
type Lifetime string

const (
	// Aging is the zero value: [Layer.Age] removes the record once its
	// Expires has passed.
	Aging Lifetime = ""

	// Static is a record [Layer.Age] never removes; its Expires is the
	// zero value.
	Static Lifetime = "static"
)

// RouterPort is one static or learned multicast-router port.
// A Static entry has a zero Expires value and is not aged.
type RouterPort struct {
	Port     string
	Expires  time.Time
	Origin   Origin
	Lifetime Lifetime
}

type groupKey struct {
	group netip.Addr
	port  string
}

type routerPortState struct {
	expires  time.Time
	origin   Origin
	lifetime Lifetime
}

type vlanState struct {
	fastLeave          bool
	membershipInterval time.Duration
	routerPortInterval time.Duration
	lmqt               time.Duration
	groups             map[groupKey]*groupPortState
	routers            map[string]routerPortState
}

// Layer holds multicast snooping state. A Layer is safe for concurrent use.
type Layer struct {
	mu     sync.RWMutex
	cfg    Config
	ports  port.Table
	byVLAN map[vlan.ID]*vlanState
}

// New builds an empty snooping layer and installs valid static router ports from cfg.
// It returns an error if the configuration is invalid against the ports.
func New(cfg Config, ports port.Table) (*Layer, error) {
	if err := cfg.Validate(ports); err != nil {
		return nil, err
	}
	return newLayer(cfg.Normalize(), ports), nil
}

func newLayer(cfg Config, ports port.Table) *Layer {
	l := &Layer{
		cfg:    cfg.Clone(),
		ports:  ports.Clone(),
		byVLAN: make(map[vlan.ID]*vlanState, len(cfg.VLANs)),
	}

	for vid, vlanCfg := range cfg.VLANs {
		state := &vlanState{
			fastLeave:          vlanCfg.FastLeave,
			membershipInterval: vlanCfg.membershipInterval(),
			routerPortInterval: vlanCfg.routerPortInterval(),
			lmqt:               vlanCfg.lastMemberQueryInterval() * time.Duration(vlanCfg.lastMemberQueryCount()),
			groups:             make(map[groupKey]*groupPortState),
			routers:            make(map[string]routerPortState, len(vlanCfg.RouterPorts)),
		}
		for _, name := range vlanCfg.RouterPorts {
			if logicalPort(ports, name) {
				state.routers[name] = routerPortState{origin: Configured, lifetime: Static}
			}
		}
		l.byVLAN[vid] = state
	}

	return l
}

// Clone returns an independent snapshot with all configuration, ports, entries, and timers preserved.
func (l *Layer) Clone() *Layer {
	l.mu.RLock()
	defer l.mu.RUnlock()

	cp := &Layer{
		cfg:    l.cfg.Clone(),
		ports:  l.ports.Clone(),
		byVLAN: make(map[vlan.ID]*vlanState, len(l.byVLAN)),
	}
	for vid, state := range l.byVLAN {
		stateCopy := &vlanState{
			fastLeave:          state.fastLeave,
			membershipInterval: state.membershipInterval,
			routerPortInterval: state.routerPortInterval,
			lmqt:               state.lmqt,
			groups:             make(map[groupKey]*groupPortState, len(state.groups)),
			routers:            make(map[string]routerPortState, len(state.routers)),
		}
		for key, gps := range state.groups {
			stateCopy.groups[key] = cloneGroupPortState(gps)
		}
		maps.Copy(stateCopy.routers, state.routers)
		cp.byVLAN[vid] = stateCopy
	}

	return cp
}

// Snooped reports whether vid has multicast snooping configured.
func (l *Layer) Snooped(vid vlan.ID) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()

	_, ok := l.byVLAN[vid]

	return ok
}

// Learn applies an IGMP message received from a logical ingress port.
// Messages for unsnooped VLANs, unknown ports, and physical LAG members have no effect.
func (l *Layer) Learn(now time.Time, vid vlan.ID, portName string, source netip.Addr, message igmp.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.byVLAN[vid]
	if !ok || !logicalPort(l.ports, portName) {
		return
	}

	switch message.Type {
	case igmp.Query:
		if source.Is4() && !source.IsUnspecified() {
			learnRouter(now, portName, state)
		}
		observeQuery(now, message.Group, message.Sources, message.Suppress, state)
	case igmp.ReportV1, igmp.ReportV2:
		learnLegacyJoin(now, message.Group, portName, state)
	case igmp.Leave:
		learnLeave(now, message.Group, portName, state)
	case igmp.ReportV3:
		for _, record := range message.Records {
			learnGroupRecord(now, portName, recordKind(record.Type), record.Group, record.Sources, state)
		}
	}
}

// LearnMLD applies an MLD message received from a logical ingress port.
// Router-port learning requires an IPv6 link-local source.
func (l *Layer) LearnMLD(now time.Time, vid vlan.ID, portName string, source netip.Addr, message mld.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.byVLAN[vid]
	if !ok || !logicalPort(l.ports, portName) {
		return
	}

	switch message.Type {
	case mld.Query:
		if source.Is6() && source.IsLinkLocalUnicast() {
			learnRouter(now, portName, state)
		}
		observeQuery(now, message.Group, message.Sources, message.Suppress, state)
	case mld.ReportV1:
		learnLegacyJoin(now, message.Group, portName, state)
	case mld.Done:
		learnLeave(now, message.Group, portName, state)
	case mld.ReportV2:
		for _, record := range message.Records {
			learnGroupRecord(now, portName, recordKind(record.Type), record.Group, record.Sources, state)
		}
	}
}

// Age applies the RFC 3376 §6.5 timer-expiry rules and removes learned router ports whose
// expiry is not after now. It is the only place aging mutates stored state; Resolve computes
// the same rules lazily against the now it is given, so a caller need not call Age first.
func (l *Layer) Age(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, state := range l.byVLAN {
		for key, gps := range state.groups {
			ageGroupPortState(gps, now)
			if gps.isEmptyInclude() {
				delete(state.groups, key)
			}
		}
		for name, router := range state.routers {
			if router.lifetime != Static && !router.expires.After(now) {
				delete(state.routers, name)
			}
		}
	}
}

func ageGroupPortState(gps *groupPortState, now time.Time) {
	if gps.mode == Include {
		for addr, expires := range gps.sources {
			if !expires.After(now) {
				delete(gps.sources, addr)
			}
		}

		return
	}

	if gps.groupExpires.After(now) {
		for addr, expires := range gps.sources {
			if !expires.IsZero() && !expires.After(now) {
				gps.sources[addr] = time.Time{}
			}
		}

		return
	}

	gps.mode = Include
	gps.groupExpires = time.Time{}
	for addr, expires := range gps.sources {
		if expires.IsZero() || !expires.After(now) {
			delete(gps.sources, addr)
		}
	}
}

// Resolve returns the sorted union of admitted member ports and router ports on vid, applying
// the §6.3 forwarding table for source against each port's router state as of now. registered
// is true only when at least one port holds a router-state record for group, regardless of
// whether source is admitted on it. pending is true when a "Send Q" action has gone more than
// LMQT without a matching observed query, which only happens when vid has a router port.
func (l *Layer) Resolve(vid vlan.ID, group netip.Addr, source netip.Addr, now time.Time) (ports []string, registered, pending bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	state, ok := l.byVLAN[vid]
	if !ok {
		return nil, false, false
	}

	admitted := make(map[string]struct{}, len(state.routers))
	for name := range state.routers {
		admitted[name] = struct{}{}
	}

	hasRouter := len(state.routers) > 0
	for key, gps := range state.groups {
		if key.group != group {
			continue
		}
		registered = true
		if admits(gps, source, now) {
			admitted[key.port] = struct{}{}
		}
		if hasRouter && obligationPending(gps, state.lmqt, now) {
			pending = true
		}
	}

	resolved := make([]string, 0, len(admitted))
	for name := range admitted {
		resolved = append(resolved, name)
	}
	slices.Sort(resolved)

	return resolved, registered, pending
}

// Groups returns a deterministic snapshot of router state on vid.
func (l *Layer) Groups(vid vlan.ID) []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	state, ok := l.byVLAN[vid]
	if !ok || len(state.groups) == 0 {
		return nil
	}

	entries := make([]Entry, 0, len(state.groups))
	for key, gps := range state.groups {
		sources := make([]SourceEntry, 0, len(gps.sources))
		for addr, expires := range gps.sources {
			sources = append(sources, SourceEntry{Address: addr, Expires: expires})
		}
		slices.SortFunc(sources, func(a, b SourceEntry) int { return a.Address.Compare(b.Address) })

		entries = append(entries, Entry{
			Group:        key.group,
			Port:         key.port,
			Mode:         gps.mode,
			GroupExpires: gps.groupExpires,
			Sources:      sources,
		})
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		if cmp := a.Group.Compare(b.Group); cmp != 0 {
			return cmp
		}

		return strings.Compare(a.Port, b.Port)
	})

	return entries
}

// RouterPorts returns a deterministic snapshot of static and learned router ports on vid.
func (l *Layer) RouterPorts(vid vlan.ID) []RouterPort {
	l.mu.RLock()
	defer l.mu.RUnlock()

	state, ok := l.byVLAN[vid]
	if !ok || len(state.routers) == 0 {
		return nil
	}

	entries := make([]RouterPort, 0, len(state.routers))
	for name, router := range state.routers {
		entries = append(entries, RouterPort{Port: name, Expires: router.expires, Origin: router.origin, Lifetime: router.lifetime})
	}
	slices.SortFunc(entries, func(a, b RouterPort) int {
		return strings.Compare(a.Port, b.Port)
	})

	return entries
}

// Retain replaces the port table and drops entries for which keep reports false.
// Timers of retained entries are preserved.
func (l *Layer) Retain(ports port.Table, keep func(vid vlan.ID, port string) bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.ports = ports.Clone()
	for vid, state := range l.byVLAN {
		for key := range state.groups {
			if !keep(vid, key.port) {
				delete(state.groups, key)
			}
		}
		for name := range state.routers {
			if !keep(vid, name) {
				delete(state.routers, name)
			}
		}
	}
}

// InstallObserved installs a router port record carrying its own expiry,
// reported as [Observed] and [Aging]. It does nothing for an unsnooped vid, a
// non-logical port, or a port a static [Config] entry already claims: a
// caller reconstructing runtime state after a derive should not be able to
// override configuration. Unlike [Layer.Learn], the expiry is not computed
// from the VLAN's RouterPortInterval, so a caller carrying forward a record
// from another layer can preserve its original deadline.
func (l *Layer) InstallObserved(vid vlan.ID, portName string, expires time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.byVLAN[vid]
	if !ok || !logicalPort(l.ports, portName) {
		return
	}
	if router := state.routers[portName]; router.lifetime == Static {
		return
	}

	state.routers[portName] = routerPortState{expires: expires, origin: Observed, lifetime: Aging}
}

// learnLegacyJoin applies an IGMPv1/v2 or MLDv1 report, which RFC 3810 §8.3.2 maps to
// IS_EX({}), and (re)starts the older-version-host timer for the group on the port.
func learnLegacyJoin(now time.Time, group netip.Addr, portName string, state *vlanState) {
	applyToPort(now, group, portName, state, recIsExclude, nil, true)
}

// learnLeave applies an IGMPv2 leave or MLDv1 done message, which maps to TO_IN({}) and
// (re)starts the older-version-host timer. Fast leave bypasses the table and deletes the
// port's group state outright, since that is the state the table would reach once the
// source and group timers expired.
func learnLeave(now time.Time, group netip.Addr, portName string, state *vlanState) {
	if state.fastLeave {
		delete(state.groups, groupKey{group: group, port: portName})

		return
	}
	applyToPort(now, group, portName, state, recToInclude, nil, true)
}

// learnGroupRecord applies one IGMPv3 group record or MLDv2 address record. A record that
// is shaped like a leave — a MODE_IS_INCLUDE or CHANGE_TO_INCLUDE_MODE carrying no sources —
// gets the same fast-leave shortcut as a legacy leave.
func learnGroupRecord(now time.Time, portName string, kind recordKind, group netip.Addr, sources []netip.Addr, state *vlanState) {
	if state.fastLeave && len(sources) == 0 && (kind == recIsInclude || kind == recToInclude) {
		delete(state.groups, groupKey{group: group, port: portName})

		return
	}
	applyToPort(now, group, portName, state, kind, sources, false)
}

func applyToPort(now time.Time, group netip.Addr, portName string, state *vlanState, kind recordKind, sources []netip.Addr, startsOlderHost bool) {
	key := groupKey{group: group, port: portName}
	gps := state.groups[key]
	if gps == nil {
		gps = newGroupPortState()
	}

	applyRecord(now, state.membershipInterval, gps, kind, sources, startsOlderHost)

	if gps.isEmptyInclude() {
		delete(state.groups, key)
	} else {
		state.groups[key] = gps
	}
}

func logicalPort(ports port.Table, name string) bool {
	p, ok := ports.Port(name)

	return ok && p.LagParent == ""
}

func learnRouter(now time.Time, portName string, state *vlanState) {
	if router := state.routers[portName]; router.lifetime == Static {
		return
	}

	state.routers[portName] = routerPortState{expires: now.Add(state.routerPortInterval), origin: Observed, lifetime: Aging}
}
