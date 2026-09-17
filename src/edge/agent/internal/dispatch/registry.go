// Package dispatch holds central's dispatch stream open and drives the
// device access lane from what arrives on it.
package dispatch

import (
	"sync"

	"google.golang.org/protobuf/proto"

	dispatchv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1"
)

// registry remembers, per device and sequence, the report this edge last made
// about that operation — or that it is still in flight.
//
// It exists for one case: central re-dispatching a sequence this edge is
// already working on, which happens whenever an Onboarded report clears a
// record's confirmations and the outbox derives the row again. Admitting a
// second lane entry for that sequence would run the operation twice. Answering
// with the report already made is both correct and what central is waiting
// for — it re-dispatched because it had not recorded one.
//
// # Locking
//
// One mutex guards the whole map, and it is never held across a lane call or
// anything else that can block. Every critical section here is a map lookup
// and a pointer swap.
//
// Per-device locks were the alternative and are worse: the map itself would
// still need a lock to find the per-device one, so the contention it saves is
// the contention it adds, and every field would then be guarded by a lock
// found under another lock. A field written on one goroutine and read on
// another is guarded, and the comment names which lock, so the field below
// says it.
type registry struct {
	mu sync.Mutex
	// reports is keyed by device and sequence. A nil value means the
	// operation is in flight and this edge has made no report about it yet,
	// which is distinct from an absent key: absent means never dispatched.
	// Guarded by mu.
	reports map[operationKey]*dispatchv1.ExecuteResult
}

// operationKey names one operation: central's sequence is unique per device,
// not globally, so the device is part of the key.
type operationKey struct {
	device   string
	sequence uint64
}

func newRegistry() *registry {
	return &registry{reports: make(map[operationKey]*dispatchv1.ExecuteResult)}
}

// admit records that this edge is taking on an operation, and reports whether
// it is new. A false return means the sequence is already known and the caller
// must answer from it rather than submitting again.
//
// Populated here, before the lane is given the request, and not when the first
// report comes back. A re-dispatch that arrives while the operation is still
// running has to find it: that is the whole window this exists for, and
// recording at first report would leave it open for exactly as long as the
// operation takes.
func (r *registry) admit(device string, sequence uint64) bool {
	key := operationKey{device: device, sequence: sequence}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, known := r.reports[key]; known {
		return false
	}
	r.reports[key] = nil
	return true
}

// record stores the latest report for an operation. A report for a sequence
// this registry does not know is kept: the lane reports on its own schedule
// and a caller should not have to reason about whether admit ran first.
func (r *registry) record(device string, result *dispatchv1.ExecuteResult) {
	if result == nil {
		return
	}
	key := operationKey{device: device, sequence: result.GetSequence()}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports[key] = result
}

// report returns the last report made about an operation, and whether the
// registry knows the operation at all. A known operation with no report yet
// returns nil and true: it is in flight, and the answer is "nothing to
// re-send", not "never heard of it".
func (r *registry) report(device string, sequence uint64) (*dispatchv1.ExecuteResult, bool) {
	key := operationKey{device: device, sequence: sequence}

	r.mu.Lock()
	defer r.mu.Unlock()
	result, known := r.reports[key]
	if !known || result == nil {
		return nil, known
	}
	// Cloned, because the caller sends this onward and the registry keeps
	// holding it: handing out the stored message would let a re-send and a
	// later record write the same object from two goroutines.
	cloned, _ := proto.Clone(result).(*dispatchv1.ExecuteResult)
	return cloned, true
}

// forget drops a recorded operation once central confirms its report. Without
// it this map is a leak with the lifetime of the process: one entry per
// operation the edge has ever run.
//
// An in-flight marker — a known operation with no report yet — stays, because
// central also confirms progress reports made before Submit returns. Dropping
// an entry for an operation still running would let a re-dispatch of that
// sequence be admitted and applied to the device twice, so the nil check here
// and the caller's own discipline are guarding the same hazard from two
// sides.
func (r *registry) forget(device string, sequence uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := operationKey{device: device, sequence: sequence}
	if r.reports[key] != nil {
		delete(r.reports, key)
	}
}

func (r *registry) discard(device string, sequence uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.reports, operationKey{device: device, sequence: sequence})
}
