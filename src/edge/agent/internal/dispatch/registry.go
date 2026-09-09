// Package dispatch holds central's dispatch stream open and drives the
// device access lane from what arrives on it.
package dispatch

import (
	"sync"

	"google.golang.org/protobuf/proto"

	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
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
// found under another lock. The rule that matters is the one this build has
// already broken once — a field written on one goroutine and read on another
// is guarded, and the comment names which lock — so both fields below say it.
type registry struct {
	mu sync.Mutex
	// reports is keyed by device and sequence. A nil value means the
	// operation is in flight and this edge has made no report about it yet,
	// which is distinct from an absent key: absent means never dispatched.
	// Guarded by mu.
	reports map[operationKey]*integrationv1.ExecuteResult
}

// operationKey names one operation: central's sequence is unique per device,
// not globally, so the device is part of the key.
type operationKey struct {
	device   string
	sequence uint64
}

func newRegistry() *registry {
	return &registry{reports: make(map[operationKey]*integrationv1.ExecuteResult)}
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
func (r *registry) record(device string, result *integrationv1.ExecuteResult) {
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
func (r *registry) report(device string, sequence uint64) (*integrationv1.ExecuteResult, bool) {
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
	cloned, _ := proto.Clone(result).(*integrationv1.ExecuteResult)
	return cloned, true
}

// forget drops a recorded operation once central confirms its report. An
// in-flight marker stays until Submit returns, because central also confirms
// progress reports made before that return.
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
