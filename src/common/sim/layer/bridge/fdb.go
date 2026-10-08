package bridge

import (
	"bytes"
	"slices"
	"sort"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// FDBLookupScope returns the exact construction metadata scope for a forwarding
// database lookup by filtering database ID and destination MAC address.
func FDBLookupScope(nodeID string, fid vlan.ID, mac netaddr.MAC) analysis.Scope {
	return fdbLookupScope(
		analysis.ProtocolScope(nodeID, string(LayerName), "0"),
		fid,
		mac,
	)
}

func fdbLookupScope(parent analysis.Scope, fid vlan.ID, mac netaddr.MAC) analysis.Scope {
	return analysis.FieldScope(parent, "fdb", strconv.Itoa(int(fid)), mac.String())
}

// Counters records forwarding database lifecycle events on a [Layer].
type Counters struct {
	Learned uint64
	Expired uint64
	Evicted uint64
	Moved   uint64
}

// Origin names who installed a forwarding database record: an operator or a
// preloaded seed (Configured), or ingress learning from a live frame's
// source address (Observed). It is independent of Lifetime: a configured
// record can still age, and an observed one can be pinned past its aging
// time.
type Origin string

const (
	// Configured is a record installed by an operator or a preloaded seed.
	Configured Origin = "configured"

	// Observed is a record ingress learning installed from a live frame.
	Observed Origin = "observed"
)

// Lifetime names whether a forwarding database record ages out on its own.
type Lifetime string

const (
	// Aging is a record that expires after the bridge's configured aging time.
	Aging Lifetime = "aging"

	// Static is a record [Layer.Advance] never removes.
	Static Lifetime = "static"
)

// Seed represents a forwarding database entry to preload into a [Layer].
type Seed struct {
	FID       vlan.ID
	MAC       netaddr.MAC
	Port      string
	Origin    Origin
	Lifetime  Lifetime
	LearnedAt time.Time
}

// Entry represents an active record in the forwarding database.
type Entry struct {
	FID       vlan.ID
	MAC       netaddr.MAC
	Port      string
	Origin    Origin
	Lifetime  Lifetime
	LearnedAt time.Time
}

type fdbKey struct {
	fid vlan.ID
	mac netaddr.MAC
}

// Counters returns the forwarding database lifecycle counters as of the last write.
func (b *Layer) Counters() Counters {
	return b.counters
}

// Flush removes dynamic forwarding database entries matching the given
// targets: a target's Port must match the entry's port, and either its FIDs
// is empty or contains the entry's FID. Two targets may name the same port;
// their FID sets are unioned rather than one replacing the other, and an
// empty set on either side widens the port to every FID, so a caller that
// builds its targets tree by tree does not silently lose the earlier tree's
// flush.
func (b *Layer) Flush(targets []layer.FlushTarget) {
	if len(targets) == 0 {
		return
	}
	byPort := make(map[string][]vlan.ID, len(targets))
	for _, target := range targets {
		fids, seen := byPort[target.Port]
		switch {
		case !seen:
			byPort[target.Port] = target.FIDs
		case len(fids) == 0 || len(target.FIDs) == 0:
			byPort[target.Port] = nil
		default:
			merged := make([]vlan.ID, 0, len(fids)+len(target.FIDs))
			merged = append(merged, fids...)
			merged = append(merged, target.FIDs...)
			byPort[target.Port] = merged
		}
	}
	for key, e := range b.fdb {
		if e.Lifetime == Static {
			continue
		}
		fids, ok := byPort[e.Port]
		if !ok {
			continue
		}
		if len(fids) == 0 || slices.Contains(fids, key.fid) {
			delete(b.fdb, key)
			b.dynamic--
		}
	}
}

// Learn validates and preloads the forwarding database with the provided seeds. A seed naming a
// LAG member is stored under the LAG, as the relay learns it, so a later lookup
// treats the aggregation as one port. Invalid or duplicate seeds leave the table unchanged.
// Aging seeds count as learned and are bounded by MaxEntries, evicting the
// oldest dynamic entry when the table exceeds the bound. An aging seed for an existing
// static entry changes nothing. Static seeds count nothing.
func (b *Layer) Learn(seeds []Seed) error {
	normalized, err := NormalizeSeeds(b.cfg, layer.Env{Ports: b.ports}, seeds)
	if err != nil {
		return err
	}

	for _, s := range normalized {
		key := fdbKey{fid: s.FID, mac: s.MAC}
		existing, exists := b.fdb[key]
		if s.Lifetime == Static {
			if exists && existing.Lifetime != Static {
				b.dynamic--
			}
			b.fdb[key] = Entry(s)
			continue
		}

		if exists && existing.Lifetime == Static {
			continue
		}

		if !exists {
			if b.cfg.MaxEntries > 0 && b.dynamic >= b.cfg.MaxEntries {
				b.evictOldestDynamic()
			}
			b.fdb[key] = Entry(s)
			b.dynamic++
			b.counters.Learned++
		} else {
			if existing.Port != s.Port {
				existing.Port = s.Port
				b.counters.Moved++
			}
			existing.LearnedAt = s.LearnedAt
			b.fdb[key] = existing
		}
	}

	return nil
}

// Entries returns the forwarding database as of its last write, sorted by FID then MAC.
// It does not expire entries.
func (b *Layer) Entries() []Entry {
	entries := make([]Entry, 0, len(b.fdb))
	for _, e := range b.fdb {
		entries = append(entries, e)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].FID != entries[j].FID {
			return entries[i].FID < entries[j].FID
		}

		return bytes.Compare(entries[i].MAC[:], entries[j].MAC[:]) < 0
	})

	return entries
}

// Forget removes a forwarding database entry with the given FID and MAC address,
// regardless of whether it is static or dynamic. It reports whether an entry was present.
func (b *Layer) Forget(fid vlan.ID, mac netaddr.MAC) bool {
	key := fdbKey{fid: fid, mac: mac}
	e, exists := b.fdb[key]
	if !exists {
		return false
	}
	delete(b.fdb, key)
	if e.Lifetime != Static {
		b.dynamic--
	}

	return true
}

// Advance removes dynamic forwarding database entries older than the configured aging time relative to now. The returned layer.Effects is always empty.
func (b *Layer) Advance(now time.Time) layer.Effects {
	for key, e := range b.fdb {
		if b.expired(e, now) {
			delete(b.fdb, key)
			b.dynamic--
			b.counters.Expired++
		}
	}
	return layer.Effects{}
}

func (b *Layer) expired(e Entry, now time.Time) bool {
	return e.Lifetime == Aging && now.Sub(e.LearnedAt) > b.agingTime
}

func (b *Layer) evictOldestDynamic() (Entry, bool) {
	var (
		oldestKey fdbKey
		oldest    Entry
		found     bool
	)
	for key, e := range b.fdb {
		if e.Lifetime == Static {
			continue
		}
		if !found {
			oldestKey = key
			oldest = e
			found = true
			continue
		}
		if e.LearnedAt.Before(oldest.LearnedAt) {
			oldestKey = key
			oldest = e
		} else if e.LearnedAt.Equal(oldest.LearnedAt) {
			if e.FID < oldest.FID || (e.FID == oldest.FID && bytes.Compare(e.MAC[:], oldest.MAC[:]) < 0) {
				oldestKey = key
				oldest = e
			}
		}
	}
	if !found {
		return Entry{}, false
	}
	delete(b.fdb, oldestKey)
	b.dynamic--
	b.counters.Evicted++

	return oldest, true
}
