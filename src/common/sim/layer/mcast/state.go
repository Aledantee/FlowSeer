package mcast

import (
	"net/netip"
	"time"
)

// FilterMode is a port's RFC 3376 §6.4 current-state multicast filter mode for one group.
type FilterMode uint8

const (
	// Include admits only the sources named in a port's source records.
	Include FilterMode = iota
	// Exclude admits every source except the ones whose source record has a zero timer.
	Exclude
)

// String returns "INCLUDE" or "EXCLUDE".
func (m FilterMode) String() string {
	if m == Exclude {
		return "EXCLUDE"
	}

	return "INCLUDE"
}

// recordKind identifies the state transition described by a group or address record.
// Its values match both igmp.RecordType and mld.RecordType, so a caller converts directly.
type recordKind uint8

const (
	recIsInclude recordKind = 1
	recIsExclude recordKind = 2
	recToInclude recordKind = 3
	recToExclude recordKind = 4
	recAllow     recordKind = 5
	recBlock     recordKind = 6
)

// groupPortState is the RFC 3376 §6.4 router state held for one (VLAN, group, logical ingress port).
//
// sources holds a timer for every source record regardless of mode: in Include it is the
// set A, in Exclude a zero time.Time marks a source in Y (blocked) and any other value marks
// a source in X (still forwarded). groupExpires is meaningful only in Exclude.
//
// pendingGroupFiredAt and pendingSourceFiredAt record when an unmatched "Send Q" action last
// fired, for the group-specific and the group-and-source-specific query respectively. A zero
// value means no action is outstanding. olderHostUntil is the deadline of a compatibility
// timer started by a legacy report or leave; while it runs, BLOCK records are ignored and
// CHANGE_TO_EXCLUDE_MODE records are treated as carrying no sources (RFC 3810 §8.3.2).
type groupPortState struct {
	mode                 FilterMode
	groupExpires         time.Time
	sources              map[netip.Addr]time.Time
	olderHostUntil       time.Time
	pendingGroupFiredAt  time.Time
	pendingSourceFiredAt time.Time
}

func newGroupPortState() *groupPortState {
	return &groupPortState{mode: Include, sources: make(map[netip.Addr]time.Time)}
}

func cloneGroupPortState(gps *groupPortState) *groupPortState {
	cp := &groupPortState{
		mode:                 gps.mode,
		groupExpires:         gps.groupExpires,
		olderHostUntil:       gps.olderHostUntil,
		pendingGroupFiredAt:  gps.pendingGroupFiredAt,
		pendingSourceFiredAt: gps.pendingSourceFiredAt,
		sources:              make(map[netip.Addr]time.Time, len(gps.sources)),
	}
	for addr, expires := range gps.sources {
		cp.sources[addr] = expires
	}

	return cp
}

// isEmptyInclude reports whether gps is an Include-mode state with no source records,
// which carries no information and is treated the same as an absent record.
func (gps *groupPortState) isEmptyInclude() bool {
	return gps.mode == Include && len(gps.sources) == 0
}

// applyRecord applies one RFC 3376 §6.4 record to gps, in place.
// startsOlderHost marks a record produced by the version-compatibility mapping
// (RFC 3810 §8.3.2), which (re)starts the older-version-host timer instead of being
// subject to it.
func applyRecord(now time.Time, gmi time.Duration, gps *groupPortState, kind recordKind, reported []netip.Addr, startsOlderHost bool) {
	olderHostActive := gps.olderHostUntil.After(now)
	if startsOlderHost {
		gps.olderHostUntil = now.Add(gmi)
	} else if olderHostActive {
		switch kind {
		case recBlock:
			return
		case recToExclude:
			reported = nil
		}
	}

	if gps.sources == nil {
		gps.sources = make(map[netip.Addr]time.Time)
	}

	if gps.mode == Include {
		applyIncludeRecord(now, gmi, gps, kind, reported)
	} else {
		applyExcludeRecord(now, gmi, gps, kind, reported)
	}
}

// applyIncludeRecord applies the §6.4.1 and §6.4.2 rows whose router state is INCLUDE(A).
func applyIncludeRecord(now time.Time, gmi time.Duration, gps *groupPortState, kind recordKind, reported []netip.Addr) {
	switch kind {
	case recIsInclude, recAllow:
		refreshSources(gps, now.Add(gmi), reported)
	case recToInclude:
		blocked := keysNotIn(gps.sources, reported)
		refreshSources(gps, now.Add(gmi), reported)
		fireSourceQuery(gps, now, len(blocked) > 0)
	case recBlock:
		queried := keysIn(gps.sources, reported)
		fireSourceQuery(gps, now, len(queried) > 0)
	case recIsExclude, recToExclude:
		transitionIncludeToExclude(now, gmi, gps, reported, kind == recToExclude)
	}
}

// applyExcludeRecord applies the §6.4.1 and §6.4.2 rows whose router state is EXCLUDE(X,Y).
func applyExcludeRecord(now time.Time, gmi time.Duration, gps *groupPortState, kind recordKind, reported []netip.Addr) {
	switch kind {
	case recIsInclude, recAllow:
		refreshSources(gps, now.Add(gmi), reported)
	case recToInclude:
		blocked := runningKeysNotIn(gps.sources, reported)
		refreshSources(gps, now.Add(gmi), reported)
		fireSourceQuery(gps, now, len(blocked) > 0)
		gps.pendingGroupFiredAt = now
	case recBlock:
		added := addBlockedSources(gps, reported)
		fireSourceQuery(gps, now, len(added) > 0)
	case recIsExclude, recToExclude:
		rebuildExclude(now, gmi, gps, reported, kind == recToExclude)
	}
}

// transitionIncludeToExclude applies INCLUDE(A) + IS_EX(B)/TO_EX(B) -> EXCLUDE(A*B, B-A):
// sources common to A and B keep their running INCLUDE timer, sources only in B start at a
// zero timer, and sources only in A are dropped. TO_EX additionally queries A*B.
func transitionIncludeToExclude(now time.Time, gmi time.Duration, gps *groupPortState, reported []netip.Addr, isToEx bool) {
	old := gps.sources
	next := make(map[netip.Addr]time.Time, len(reported))
	var commonCount int
	for _, s := range reported {
		if expires, ok := old[s]; ok {
			next[s] = expires
			commonCount++
		} else {
			next[s] = time.Time{}
		}
	}

	gps.mode = Exclude
	gps.sources = next
	gps.groupExpires = now.Add(gmi)
	if isToEx {
		fireSourceQuery(gps, now, commonCount > 0)
	}
}

// rebuildExclude applies the EXCLUDE(X,Y) + IS_EX(A)/TO_EX(A) rows: a source kept in the
// reported set retains its old record (running or blocked); a brand new source starts at
// GMI for IS_EX or at the pre-update group timer for TO_EX. Sources dropped from the
// reported set are deleted outright, and the group timer always refreshes to GMI. TO_EX
// additionally queries A-Y, using the pre-update Y.
func rebuildExclude(now time.Time, gmi time.Duration, gps *groupPortState, reported []netip.Addr, isToEx bool) {
	old := gps.sources
	oldGroupExpires := gps.groupExpires
	next := make(map[netip.Addr]time.Time, len(reported))
	var queriedCount int
	for _, s := range reported {
		expires, existed := old[s]
		switch {
		case existed:
			next[s] = expires
		case isToEx:
			next[s] = oldGroupExpires
		default:
			next[s] = now.Add(gmi)
		}
		if !existed || !expires.IsZero() {
			queriedCount++
		}
	}

	gps.sources = next
	gps.groupExpires = now.Add(gmi)
	if isToEx {
		fireSourceQuery(gps, now, queriedCount > 0)
	}
}

// addBlockedSources applies EXCLUDE(X,Y) + BLOCK(A) -> EXCLUDE(X+(A-Y), Y): a source in
// A-Y that is brand new starts at the current (pre-update) group timer; one already in X
// keeps its running timer untouched. It returns A-Y, the query target.
func addBlockedSources(gps *groupPortState, reported []netip.Addr) []netip.Addr {
	added := make([]netip.Addr, 0, len(reported))
	for _, s := range reported {
		if expires, ok := gps.sources[s]; ok && expires.IsZero() {
			continue // in Y, untouched
		}
		added = append(added, s)
		if _, existed := gps.sources[s]; !existed {
			gps.sources[s] = gps.groupExpires
		}
	}

	return added
}

func refreshSources(gps *groupPortState, expires time.Time, reported []netip.Addr) {
	for _, s := range reported {
		gps.sources[s] = expires
	}
}

func fireSourceQuery(gps *groupPortState, now time.Time, fire bool) {
	if fire {
		gps.pendingSourceFiredAt = now
	}
}

func keysIn(sources map[netip.Addr]time.Time, reported []netip.Addr) []netip.Addr {
	var out []netip.Addr
	for _, s := range reported {
		if _, ok := sources[s]; ok {
			out = append(out, s)
		}
	}

	return out
}

func keysNotIn(sources map[netip.Addr]time.Time, reported []netip.Addr) []netip.Addr {
	reportedSet := make(map[netip.Addr]struct{}, len(reported))
	for _, s := range reported {
		reportedSet[s] = struct{}{}
	}

	var out []netip.Addr
	for s := range sources {
		if _, ok := reportedSet[s]; !ok {
			out = append(out, s)
		}
	}

	return out
}

// runningKeysNotIn returns the sources with a running (non-zero) timer that are not in reported.
func runningKeysNotIn(sources map[netip.Addr]time.Time, reported []netip.Addr) []netip.Addr {
	reportedSet := make(map[netip.Addr]struct{}, len(reported))
	for _, s := range reported {
		reportedSet[s] = struct{}{}
	}

	var out []netip.Addr
	for s, expires := range sources {
		if expires.IsZero() {
			continue
		}
		if _, ok := reportedSet[s]; !ok {
			out = append(out, s)
		}
	}

	return out
}

// admits reports whether a frame from source is forwarded, applying the §6.3 table and the
// §6.5 group-timer-expiry rule lazily: an EXCLUDE state whose group timer is not after now
// behaves as if it had already aged into INCLUDE.
func admits(gps *groupPortState, source netip.Addr, now time.Time) bool {
	mode := gps.mode
	if mode == Exclude && !gps.groupExpires.After(now) {
		mode = Include
	}

	expires, ok := gps.sources[source]
	running := ok && !expires.IsZero() && expires.After(now)
	if mode == Include {
		return running
	}

	return !ok || running
}

// obligationPending reports whether an unmatched "Send Q" action fired at least lmqt ago.
func obligationPending(gps *groupPortState, lmqt time.Duration, now time.Time) bool {
	if !gps.pendingGroupFiredAt.IsZero() && !now.Before(gps.pendingGroupFiredAt.Add(lmqt)) {
		return true
	}

	return !gps.pendingSourceFiredAt.IsZero() && !now.Before(gps.pendingSourceFiredAt.Add(lmqt))
}

// observeQuery applies an observed, unsuppressed group-specific or group-and-source-specific
// query to every port's state for group: it lowers the relevant timers to LMQT (never raising
// them) and, when the query arrived within LMQT of the obligation that requested it, clears
// that obligation.
func observeQuery(now time.Time, group netip.Addr, sources []netip.Addr, suppress bool, state *vlanState) {
	if suppress || !group.IsValid() {
		return
	}

	deadline := now.Add(state.lmqt)
	for key, gps := range state.groups {
		if key.group != group {
			continue
		}

		if len(sources) == 0 {
			if gps.mode == Exclude && gps.groupExpires.After(deadline) {
				gps.groupExpires = deadline
			}
			clearIfMatched(&gps.pendingGroupFiredAt, now, state.lmqt)

			continue
		}

		for _, s := range sources {
			if expires, ok := gps.sources[s]; ok && !expires.IsZero() && expires.After(deadline) {
				gps.sources[s] = deadline
			}
		}
		clearIfMatched(&gps.pendingSourceFiredAt, now, state.lmqt)
	}
}

func clearIfMatched(firedAt *time.Time, now time.Time, lmqt time.Duration) {
	if !firedAt.IsZero() && !now.After(firedAt.Add(lmqt)) {
		*firedAt = time.Time{}
	}
}
