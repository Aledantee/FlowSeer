package evidence

import (
	"sync"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeNoPolicy identifies a Consult or Record call for an operation Kind
// that has no configured lifetime: the direction record requires an absent
// lifetime to block mutation rather than fall back to an unbounded cache.
var ErrCodeNoPolicy = errs.NewCode("evidence/no-policy")

// Kind names one operation whose route evidence this package tracks.
// Adding a capability's operation adds a Kind; the type stays a small
// closed enum so a Policy's cardinality is bounded and reviewable.
type Kind int

const (
	// KindUnspecified is the zero value and is never recorded or consulted.
	KindUnspecified Kind = iota
	// KindInterfaceRead is the interface capability's read.
	KindInterfaceRead
	// KindInterfaceDescriptionChange is the interface capability's
	// description mutation.
	KindInterfaceDescriptionChange
)

// String returns the kind's name for logging and diagnostics.
func (k Kind) String() string {
	switch k {
	case KindInterfaceRead:
		return "interface_read"
	case KindInterfaceDescriptionChange:
		return "interface_description_change"
	default:
		return "unspecified"
	}
}

// Policy configures how long recorded evidence for one Kind may be
// consulted before it must be re-learned. A Kind absent from Policy, or
// mapped to a non-positive duration, has no configured lifetime.
type Policy map[Kind]time.Duration

func (p Policy) lifetime(kind Kind) (time.Duration, bool) {
	lifetime, ok := p[kind]
	if !ok || lifetime <= 0 {
		return 0, false
	}

	return lifetime, true
}

type key struct {
	device      string
	fingerprint string
	kind        Kind
	route       inventoryv1.ManagementProtocol
}

type entry struct {
	completeness accessv1.Completeness
	expiresAt    time.Time
}

// Store is the route-evidence cache for every device this edge serves. The
// zero value is not usable; construct one with [NewStore].
//
// Nothing reads it yet. access.Lane calls Record after every complete
// observation and InvalidateFingerprint when a device's firmware epoch
// changes, but Consult has no caller outside this package's own tests, so
// the cache answers no question and removing the invalidation would fail no
// test. It is maintained rather than deleted because recording under the
// correct epoch, and invalidating when that epoch moves, is the half that is
// hard to get right and easy to break silently later; the reader is the
// straightforward half. Wiring Consult into route selection is a named
// follow-up on the lane host contract plan.
//
// This paragraph is here rather than only in the module README because the
// person who comes to add that reader will be reading this file, and a
// mechanism documented as not yet alive is a different thing from one that
// is quietly dead.
type Store struct {
	policy Policy

	mu      sync.Mutex
	entries map[key]entry
}

// NewStore builds a Store governed by policy. policy is not copied; a
// caller must not mutate it after construction.
func NewStore(policy Policy) *Store {
	return &Store{policy: policy, entries: make(map[key]entry)}
}

// Consult reports whether route may be trusted, without a fresh read, to
// answer kind on device under fingerprint as of now. ok is false with no
// error when no unexpired evidence exists yet — the caller should try the
// route and call Record on success. err is non-nil, wrapping
// [ErrCodeNoPolicy], when kind has no configured lifetime at all: the
// direction record requires this to block mutation before any device
// contact, not merely fall through to a live read.
func (s *Store) Consult(device, fingerprint string, kind Kind, route inventoryv1.ManagementProtocol, now time.Time) (completeness accessv1.Completeness, ok bool, err error) {
	if _, has := s.policy.lifetime(kind); !has {
		return accessv1.Completeness_COMPLETENESS_UNSPECIFIED, false, errs.New().Code(ErrCodeNoPolicy).
			Msgf("no evidence lifetime configured for operation kind %s", kind)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	e, found := s.entries[key{device: device, fingerprint: fingerprint, kind: kind, route: route}]
	if !found || !e.expiresAt.After(now) {
		return accessv1.Completeness_COMPLETENESS_UNSPECIFIED, false, nil
	}

	return e.completeness, true, nil
}

// Record stores route's completeness for kind on device under fingerprint,
// valid from now for the kind's configured lifetime. It returns
// [ErrCodeNoPolicy] under the same condition as Consult, so a caller cannot
// silently establish evidence for a kind the operator never configured a
// lifetime for.
func (s *Store) Record(device, fingerprint string, kind Kind, route inventoryv1.ManagementProtocol, completeness accessv1.Completeness, now time.Time) error {
	lifetime, has := s.policy.lifetime(kind)
	if !has {
		return errs.New().Code(ErrCodeNoPolicy).Msgf("no evidence lifetime configured for operation kind %s", kind)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries[key{device: device, fingerprint: fingerprint, kind: kind, route: route}] = entry{
		completeness: completeness,
		expiresAt:    now.Add(lifetime),
	}

	return nil
}

// InvalidateFingerprint drops every entry recorded for device under any
// fingerprint other than current. This is the firmware-epoch invalidation
// the epoch-independence rule requires: evidence learned under a
// stale epoch must never answer Consult again, however long its lifetime
// window has left.
func (s *Store) InvalidateFingerprint(device, current string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k := range s.entries {
		if k.device == device && k.fingerprint != current {
			delete(s.entries, k)
		}
	}
}
