// Package mcast learns multicast group membership and router ports from IGMP and MLD control messages.
package mcast

import (
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// LayerName identifies multicast snooping decisions.
const LayerName trace.Layer = "mcast"

// Rule constants produced by mcast.
const (
	RuleControlUnsupported trace.RuleID = "mcast.control.unsupported"
	RuleControlBad         trace.RuleID = "mcast.control.bad"
	RuleControlAdmit       trace.RuleID = "mcast.control.admit"
	RuleGroupMembers       trace.RuleID = "group-members"
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
	// Configured indicates that cfg.RouterPorts installed the record.
	Configured Origin = "configured"

	// Observed is a record a learned query installed.
	Observed Origin = "observed"
)

// Lifetime names whether a router port record ages out on its own.
type Lifetime string

const (
	// Aging indicates that [Layer.Advance] removes the record once its
	// Expires has passed.
	Aging Lifetime = "aging"

	// Static is a record [Layer.Advance] never removes; its Expires is the
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

// Layer holds multicast snooping state. A Layer is not safe for concurrent use.
// The caller must serialize calls.
type Layer struct {
	cfg    Config
	ports  map[string]struct{}
	byVLAN map[vlan.ID]*vlanState
}

// Restore copies group state and observed router ports from prev for VLANs both
// layers snoop, restricted to this layer's logical ports that keep admits.
// It preserves every deadline and pending query without sharing mutable state.
// Configured router ports are not copied, and this layer's static ports prevail.
// The target configuration governs subsequent learning. Both layers must be non-nil.
func (l *Layer) Restore(prev *Layer, keep func(vlan.ID, string) bool) {
	for vid, state := range l.byVLAN {
		old, ok := prev.byVLAN[vid]
		if !ok {
			continue
		}
		for key, gps := range old.groups {
			if _, ok := l.ports[key.port]; ok && keep(vid, key.port) {
				state.groups[key] = cloneGroupPortState(gps)
			}
		}
		for name, router := range old.routers {
			if _, ok := l.ports[name]; !ok || !keep(vid, name) || router.origin != Observed || state.routers[name].lifetime == Static {
				continue
			}
			state.routers[name] = router
		}
	}
}

// New builds an empty snooping layer and installs valid static router ports from cfg.
// It returns an error if the configuration is invalid against the ports.
func New(cfg Config, env layer.Env) (*Layer, error) {
	if err := cfg.Validate(env); err != nil {
		return nil, err
	}
	return newLayer(cfg.Normalize(env), env.Ports), nil
}

func newLayer(cfg Config, ports port.Table) *Layer {
	l := &Layer{
		cfg:    cfg.Clone(),
		ports:  make(map[string]struct{}),
		byVLAN: make(map[vlan.ID]*vlanState, len(cfg.VLANs)),
	}
	for _, p := range ports.Ports() {
		if p.LagParent == "" {
			l.ports[p.Name] = struct{}{}
		}
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
			if _, ok := l.ports[name]; ok {
				state.routers[name] = routerPortState{origin: Configured, lifetime: Static}
			}
		}
		l.byVLAN[vid] = state
	}

	return l
}

// Clone returns an independent snapshot with all configuration, ports, entries, and timers preserved.
func (l *Layer) Clone() *Layer {
	cp := &Layer{
		cfg:    l.cfg.Clone(),
		ports:  maps.Clone(l.ports),
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
	_, ok := l.byVLAN[vid]

	return ok
}

// Learn applies an IGMP message received from a logical ingress port.
// Messages for unsnooped VLANs, unknown ports, and physical LAG members have no effect.
func (l *Layer) Learn(now time.Time, vid vlan.ID, portName string, source netip.Addr, message igmp.Message) {
	state, ok := l.byVLAN[vid]
	_, logical := l.ports[portName]
	if !ok || !logical {
		return
	}

	switch message.Type {
	case igmp.Query:
		if source.Is4() && !source.IsUnspecified() {
			learnRouter(now, portName, state)
		}
		observeQuery(now, message.Group, message.Sources, message.Suppress, state)
	case igmp.ReportV1:
		learnLegacyJoin(now, message.Group, portName, state, 1)
	case igmp.ReportV2:
		learnLegacyJoin(now, message.Group, portName, state, 2)
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
	state, ok := l.byVLAN[vid]
	_, logical := l.ports[portName]
	if !ok || !logical {
		return
	}

	switch message.Type {
	case mld.Query:
		if source.Is6() && source.IsLinkLocalUnicast() {
			learnRouter(now, portName, state)
		}
		observeQuery(now, message.Group, message.Sources, message.Suppress, state)
	case mld.ReportV1:
		learnLegacyJoin(now, message.Group, portName, state, 2)
	case mld.Done:
		learnLeave(now, message.Group, portName, state)
	case mld.ReportV2:
		for _, record := range message.Records {
			learnGroupRecord(now, portName, recordKind(record.Type), record.Group, record.Sources, state)
		}
	}
}

// Advance applies the RFC 3376 §6.5 timer-expiry rules and removes learned router ports whose
// expiry is not after now. The returned layer.Effects is always empty. Reads ignore expired
// state without requiring Advance, and learning ages a port's state before applying a record.
func (l *Layer) Advance(now time.Time) layer.Effects {
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
	return layer.Effects{}
}

func ageGroupPortState(gps *groupPortState, now time.Time) {
	if gps.mode == Include {
		for addr, expires := range gps.sources {
			if !expires.After(now) {
				delete(gps.sources, addr)
				delete(gps.queriedSources, addr)
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
			delete(gps.queriedSources, addr)
		}
	}
}

// Resolve returns the sorted union of admitted member ports and router ports on vid, applying
// the §6.3 forwarding table for source against each port's router state as of now. registered
// is true only when at least one port holds a router-state record for group, regardless of
// whether source is admitted on it. pending is true when a "Send Q" action has gone more than
// LMQT without a matching observed query, which only happens when vid has a router port.
func (l *Layer) Resolve(vid vlan.ID, group netip.Addr, source netip.Addr, now time.Time) (ports []string, registered, pending bool) {
	state, ok := l.byVLAN[vid]
	if !ok {
		return nil, false, false
	}

	admitted := make(map[string]struct{}, len(state.routers))
	for name, router := range state.routers {
		if router.lifetime == Static || router.expires.After(now) {
			admitted[name] = struct{}{}
		}
	}

	hasRouter := len(admitted) > 0
	for key, gps := range state.groups {
		if key.group != group || !groupPortActive(gps, now) {
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

func groupPortActive(gps *groupPortState, now time.Time) bool {
	if gps.mode == Exclude && gps.groupExpires.After(now) {
		return true
	}
	for _, expires := range gps.sources {
		if !expires.IsZero() && expires.After(now) {
			return true
		}
	}
	return false
}

// Groups returns a deterministic snapshot of router state on vid as of now, without
// changing stored state. Expired sources and groups follow the same rules as Advance.
func (l *Layer) Groups(vid vlan.ID, now time.Time) []Entry {
	state, ok := l.byVLAN[vid]
	if !ok || len(state.groups) == 0 {
		return nil
	}

	var entries []Entry
	for key, gps := range state.groups {
		if !groupPortActive(gps, now) {
			continue
		}
		gps = cloneGroupPortState(gps)
		ageGroupPortState(gps, now)
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

// RouterPorts returns a deterministic snapshot of static and unexpired learned router
// ports on vid as of now, without changing stored state.
func (l *Layer) RouterPorts(vid vlan.ID, now time.Time) []RouterPort {
	state, ok := l.byVLAN[vid]
	if !ok || len(state.routers) == 0 {
		return nil
	}

	var entries []RouterPort
	for name, router := range state.routers {
		if router.lifetime != Static && !router.expires.After(now) {
			continue
		}
		entries = append(entries, RouterPort{Port: name, Expires: router.expires, Origin: router.origin, Lifetime: router.lifetime})
	}
	slices.SortFunc(entries, func(a, b RouterPort) int {
		return strings.Compare(a.Port, b.Port)
	})

	return entries
}

// learnLegacyJoin maps reports to IS_EX({}) and starts only that version's timer
// (RFC 3376 §7.3.2 for IGMP, RFC 3810 §8.3.2 for MLD).
func learnLegacyJoin(now time.Time, group netip.Addr, portName string, state *vlanState, version uint8) {
	applyToPort(now, group, portName, state, recIsExclude, nil, version)
}

// learnLeave applies an IGMPv2 leave or MLDv1 done message, which maps to TO_IN({}) and
// starts no compatibility timer. IGMPv1 compatibility ignores it. Fast leave deletes the
// port's group state outright, since that is the state the table would reach once the
// source and group timers expired.
func learnLeave(now time.Time, group netip.Addr, portName string, state *vlanState) {
	if gps := state.groups[groupKey{group: group, port: portName}]; gps != nil && gps.igmpV1HostUntil.After(now) {
		return
	}
	if state.fastLeave {
		delete(state.groups, groupKey{group: group, port: portName})

		return
	}
	applyToPort(now, group, portName, state, recToInclude, nil, 0)
}

// learnGroupRecord applies one IGMPv3 group record or MLDv2 address record. A record that
// is shaped like a leave — a MODE_IS_INCLUDE or CHANGE_TO_INCLUDE_MODE carrying no sources —
// gets the same fast-leave shortcut as a legacy leave.
func learnGroupRecord(now time.Time, portName string, kind recordKind, group netip.Addr, sources []netip.Addr, state *vlanState) {
	if gps := state.groups[groupKey{group: group, port: portName}]; kind == recToInclude && gps != nil && gps.igmpV1HostUntil.After(now) {
		return
	}
	if state.fastLeave && len(sources) == 0 && (kind == recIsInclude || kind == recToInclude) {
		delete(state.groups, groupKey{group: group, port: portName})

		return
	}
	applyToPort(now, group, portName, state, kind, sources, 0)
}

func applyToPort(now time.Time, group netip.Addr, portName string, state *vlanState, kind recordKind, sources []netip.Addr, legacyVersion uint8) {
	key := groupKey{group: group, port: portName}
	gps := state.groups[key]
	if gps != nil {
		ageGroupPortState(gps, now)
		if gps.isEmptyInclude() {
			gps = nil
		}
	}
	if gps == nil {
		gps = newGroupPortState()
	}

	applyRecord(now, state.membershipInterval, gps, kind, sources, legacyVersion)

	if gps.isEmptyInclude() {
		delete(state.groups, key)
	} else {
		state.groups[key] = gps
	}
}

func learnRouter(now time.Time, portName string, state *vlanState) {
	if router := state.routers[portName]; router.lifetime == Static {
		return
	}

	state.routers[portName] = routerPortState{expires: now.Add(state.routerPortInterval), origin: Observed, lifetime: Aging}
}

// RetentionKey returns a canonical encoding of every normalized input the layer's
// runtime state depends on: its own configuration as Diff sees it and the port link
// states for configured router ports.
func RetentionKey(cfg Config, env layer.Env) string {
	if len(cfg.VLANs) == 0 {
		return ""
	}
	norm := cfg.Normalize(env)
	var b strings.Builder
	b.WriteString("config=")
	var allRouterPorts []string
	vids := sortedVLANIDs(norm.VLANs)
	for _, vid := range vids {
		v := norm.VLANs[vid]
		fmt.Fprintf(&b, "%d:{%s};", vid, v.Canonical())
		allRouterPorts = append(allRouterPorts, v.RouterPorts...)
	}

	slices.Sort(allRouterPorts)
	allRouterPorts = slices.Compact(allRouterPorts)

	b.WriteString("\nport-state=")
	for _, name := range allRouterPorts {
		if pt, ok := env.Ports.Port(name); ok {
			fmt.Fprintf(&b, "%s:admin=%s,oper=%s;", name, pt.AdminStatus, pt.OperStatus)
		} else {
			fmt.Fprintf(&b, "%s:absent;", name)
		}
	}

	return b.String()
}
