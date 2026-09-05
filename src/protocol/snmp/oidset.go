package snmp

import "sort"

// OIDSet is an immutable sorted set of OIDs answering longest-prefix
// lookups, the shape a sysObjectID identity table needs. The zero value
// is the empty set. Build one with [NewOIDSet]; it is safe for concurrent
// use once built.
type OIDSet struct {
	oids []OID
}

// NewOIDSet builds a set from oids in [OID.Compare] order, collapsing
// exact duplicates and dropping the empty OID, which would otherwise match
// every lookup. The input slice is neither retained nor modified.
func NewOIDSet(oids []OID) OIDSet {
	sorted := make([]OID, 0, len(oids))
	for _, o := range oids {
		if o.Len() > 0 {
			sorted = append(sorted, o.Clone())
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Compare(sorted[j]) < 0 })
	set := sorted[:0]
	for i, o := range sorted {
		if i == 0 || !o.Equal(sorted[i-1]) {
			set = append(set, o)
		}
	}
	return OIDSet{oids: set}
}

// Len returns the number of distinct members.
func (s OIDSet) Len() int { return len(s.oids) }

// Longest returns the deepest member that is a prefix of oid, which is
// oid itself when it is a member, and false when no member is a prefix.
//
// Members between a prefix of oid and oid in sort order all share that
// prefix, so the greatest member not above oid is either the answer or
// lies under a sibling; in the latter case every remaining candidate is
// at most as long as the common prefix of that member and oid, and the
// search repeats on that shorter target. Each round is one binary search
// and shortens the target, so a lookup costs at most oid.Len() rounds.
func (s OIDSet) Longest(oid OID) (OID, bool) {
	target := oid
	for {
		i := sort.Search(len(s.oids), func(j int) bool { return s.oids[j].Compare(target) > 0 }) - 1
		if i < 0 {
			return OID{}, false
		}
		member := s.oids[i]
		if oid.HasPrefix(member) {
			return member.Clone(), true
		}
		target = OID{subs: oid.subs[:commonPrefixLen(member, oid)]}
	}
}

func commonPrefixLen(a, b OID) int {
	n := min(a.Len(), b.Len())
	for i := range n {
		if a.subs[i] != b.subs[i] {
			return i
		}
	}
	return n
}
