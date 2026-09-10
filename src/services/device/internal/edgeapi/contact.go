package edgeapi

import (
	"time"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
)

// Contact derives how recently an enrolled edge was heard from, out of the
// heartbeat timestamp its record stores. The value is never stored: a
// heartbeat writes only last_seen_at, so an edge unplugged for a year does not
// go on reporting active because nothing ran to age it. A dormant edge is
// still enrolled, and its next call returns it to active.
type Contact struct {
	staleAfter   time.Duration
	dormantAfter time.Duration
}

// NewContact returns the derivation for a deployment's thresholds. Values that
// are not positive, or a dormant threshold at or below the stale one, fall back
// to the service defaults.
func NewContact(staleAfter, dormantAfter time.Duration) Contact {
	if staleAfter <= 0 {
		staleAfter = defaultStaleAfter
	}
	if dormantAfter <= staleAfter {
		dormantAfter = max(defaultDormantAfter, staleAfter*2)
	}
	return Contact{staleAfter: staleAfter, dormantAfter: dormantAfter}
}

// Apply rewrites the record's contact to what its last heartbeat says at now.
// An edge that is not enrolled has no contact, which is what the schema
// requires of one that never enrolled.
func (c Contact) Apply(record *edgev1.EdgeRecord, now time.Time) {
	state := record.GetState()
	if state == nil {
		return
	}
	if state.GetLifecycle() != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED {
		state.ClearContact()
		return
	}
	state.SetContact(c.of(state.GetLastSeenAt().AsTime(), now))
}

func (c Contact) of(lastSeen, now time.Time) edgev1.EdgeContact {
	switch silence := now.Sub(lastSeen); {
	case silence >= c.dormantAfter:
		return edgev1.EdgeContact_EDGE_CONTACT_DORMANT
	case silence >= c.staleAfter:
		return edgev1.EdgeContact_EDGE_CONTACT_STALE
	default:
		return edgev1.EdgeContact_EDGE_CONTACT_ACTIVE
	}
}
