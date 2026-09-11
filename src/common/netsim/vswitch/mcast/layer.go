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

// Entry is one learned group membership. Expires is the instant at which [Layer.Age] removes it.
type Entry struct {
	Group   netip.Addr
	Port    string
	Expires time.Time
}

// RouterPort is one static or learned multicast-router port.
// Static entries have a zero Expires value and are not aged.
type RouterPort struct {
	Port    string
	Expires time.Time
	Static  bool
}

type groupKey struct {
	group netip.Addr
	port  string
}

type routerPortState struct {
	expires time.Time
	static  bool
}

type vlanState struct {
	fastLeave          bool
	membershipInterval time.Duration
	routerPortInterval time.Duration
	groups             map[groupKey]time.Time
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
func New(cfg Config, ports port.Table) *Layer {
	cfg = cfg.Clone()
	l := &Layer{
		cfg:    cfg,
		ports:  ports.Clone(),
		byVLAN: make(map[vlan.ID]*vlanState, len(cfg.VLANs)),
	}

	for vid, vlanCfg := range cfg.VLANs {
		state := &vlanState{
			fastLeave:          vlanCfg.FastLeave,
			membershipInterval: vlanCfg.membershipInterval(),
			routerPortInterval: vlanCfg.routerPortInterval(),
			groups:             make(map[groupKey]time.Time),
			routers:            make(map[string]routerPortState, len(vlanCfg.RouterPorts)),
		}
		for _, name := range vlanCfg.RouterPorts {
			if logicalPort(ports, name) {
				state.routers[name] = routerPortState{static: true}
			}
		}
		l.byVLAN[vid] = state
	}

	return l
}

// Clone returns an independent snapshot with all configuration, ports, entries, and expiries preserved.
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
			groups:             make(map[groupKey]time.Time, len(state.groups)),
			routers:            make(map[string]routerPortState, len(state.routers)),
		}
		maps.Copy(stateCopy.groups, state.groups)
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
	case igmp.ReportV1, igmp.ReportV2:
		join(now, message.Group, portName, state)
	case igmp.Leave:
		leave(message.Group, portName, state)
	case igmp.ReportV3:
		for _, record := range message.Records {
			learnIGMPRecord(now, portName, record, state)
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
	case mld.ReportV1:
		join(now, message.Group, portName, state)
	case mld.Done:
		leave(message.Group, portName, state)
	case mld.ReportV2:
		for _, record := range message.Records {
			learnMLDRecord(now, portName, record, state)
		}
	}
}

// Age removes memberships and learned router ports whose expiry is not after now.
func (l *Layer) Age(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, state := range l.byVLAN {
		for key, expires := range state.groups {
			if !expires.After(now) {
				delete(state.groups, key)
			}
		}
		for name, router := range state.routers {
			if !router.static && !router.expires.After(now) {
				delete(state.routers, name)
			}
		}
	}
}

// Resolve returns the sorted union of members of group and all router ports on vid.
// registered is true only when at least one membership entry names group.
func (l *Layer) Resolve(vid vlan.ID, group netip.Addr) ([]string, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	state, ok := l.byVLAN[vid]
	if !ok {
		return nil, false
	}

	ports := make(map[string]struct{}, len(state.routers))
	for name := range state.routers {
		ports[name] = struct{}{}
	}

	registered := false
	for key := range state.groups {
		if key.group == group {
			registered = true
			ports[key.port] = struct{}{}
		}
	}

	resolved := make([]string, 0, len(ports))
	for name := range ports {
		resolved = append(resolved, name)
	}
	slices.Sort(resolved)

	return resolved, registered
}

// Groups returns a deterministic snapshot of memberships on vid.
func (l *Layer) Groups(vid vlan.ID) []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	state, ok := l.byVLAN[vid]
	if !ok || len(state.groups) == 0 {
		return nil
	}

	entries := make([]Entry, 0, len(state.groups))
	for key, expires := range state.groups {
		entries = append(entries, Entry{Group: key.group, Port: key.port, Expires: expires})
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
		entries = append(entries, RouterPort{Port: name, Expires: router.expires, Static: router.static})
	}
	slices.SortFunc(entries, func(a, b RouterPort) int {
		return strings.Compare(a.Port, b.Port)
	})

	return entries
}

// Retain replaces the port table and drops entries for which keep reports false.
// Expiries of retained entries are preserved.
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

func learnIGMPRecord(now time.Time, portName string, record igmp.GroupRecord, state *vlanState) {
	switch record.Type {
	case igmp.ModeIsExclude, igmp.ChangeToExcludeMode:
		join(now, record.Group, portName, state)
	case igmp.ModeIsInclude, igmp.ChangeToIncludeMode:
		if len(record.Sources) == 0 {
			leave(record.Group, portName, state)
		} else {
			join(now, record.Group, portName, state)
		}
	case igmp.AllowNewSources:
		if len(record.Sources) > 0 {
			join(now, record.Group, portName, state)
		}
	case igmp.BlockOldSources:
		return
	}
}

func learnMLDRecord(now time.Time, portName string, record mld.AddressRecord, state *vlanState) {
	switch record.Type {
	case mld.ModeIsExclude, mld.ChangeToExcludeMode:
		join(now, record.Group, portName, state)
	case mld.ModeIsInclude, mld.ChangeToIncludeMode:
		if len(record.Sources) == 0 {
			leave(record.Group, portName, state)
		} else {
			join(now, record.Group, portName, state)
		}
	case mld.AllowNewSources:
		if len(record.Sources) > 0 {
			join(now, record.Group, portName, state)
		}
	case mld.BlockOldSources:
		return
	}
}

func logicalPort(ports port.Table, name string) bool {
	p, ok := ports.Port(name)

	return ok && p.LagParent == ""
}

func join(now time.Time, group netip.Addr, portName string, state *vlanState) {
	state.groups[groupKey{group: group, port: portName}] = now.Add(state.membershipInterval)
}

func leave(group netip.Addr, portName string, state *vlanState) {
	if state.fastLeave {
		delete(state.groups, groupKey{group: group, port: portName})
	}
}

func learnRouter(now time.Time, portName string, state *vlanState) {
	if router := state.routers[portName]; router.static {
		return
	}

	state.routers[portName] = routerPortState{expires: now.Add(state.routerPortInterval)}
}
