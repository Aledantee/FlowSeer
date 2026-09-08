package dispatch

import (
	"sync"
	"testing"

	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
)

func resultAt(sequence uint64, phase string) *integrationv1.ExecuteResult {
	result := &integrationv1.ExecuteResult{}
	result.SetSequence(sequence)
	result.SetProgress(&integrationv1.Progress{})
	_ = phase
	return result
}

// TestASecondDispatchForTheSameSequenceIsNotAdmitted is the registry's
// purpose. Central re-dispatches a sequence whenever an Onboarded report
// clears a record's confirmations, and admitting it again would run the
// operation on the device twice.
func TestASecondDispatchForTheSameSequenceIsNotAdmitted(t *testing.T) {
	r := newRegistry()

	if !r.admit("dev-1", 7) {
		t.Fatal("the first dispatch was not admitted")
	}
	if r.admit("dev-1", 7) {
		t.Fatal("a second dispatch for the same sequence was admitted; the operation would run twice")
	}
	// Another device's sequence 7 is a different operation. Central's
	// sequences are per device, not global.
	if !r.admit("dev-2", 7) {
		t.Fatal("another device's sequence 7 was refused; sequences are per device")
	}

	// And the two do not read each other's reports, in either direction.
	r.record("dev-1", resultAt(7, "admitted"))
	if result, _ := r.report("dev-2", 7); result != nil {
		t.Fatal("dev-2's sequence 7 returned dev-1's report")
	}
	r.record("dev-2", resultAt(7, "released"))
	first, _ := r.report("dev-1", 7)
	second, _ := r.report("dev-2", 7)
	if first == nil || second == nil || first == second {
		t.Fatal("the two devices' reports for sequence 7 are not distinct")
	}
	if result, _ := r.report("dev-1", 7); result == nil {
		t.Fatal("dev-1's own report went missing")
	}
}

// TestAnInFlightOperationIsKnownWithNoReportYet pins the distinction the
// caller turns on. Absent means never dispatched and the operation should be
// admitted; known with no report means it is running and there is nothing to
// re-send yet. Collapsing the two would either run it twice or answer a
// re-dispatch with silence.
func TestAnInFlightOperationIsKnownWithNoReportYet(t *testing.T) {
	r := newRegistry()
	r.admit("dev-1", 7)

	result, known := r.report("dev-1", 7)
	if !known {
		t.Fatal("an admitted operation is not known")
	}
	if result != nil {
		t.Fatal("an operation with no report yet returned one")
	}

	if _, known := r.report("dev-1", 8); known {
		t.Fatal("a sequence never dispatched is known")
	}
}

// TestTheStoredReportIsCloned: the registry keeps holding what it hands out,
// and the caller sends it onward. Returning the stored message would let a
// re-send and a later record write the same object from two goroutines.
func TestTheStoredReportIsCloned(t *testing.T) {
	r := newRegistry()
	r.admit("dev-1", 7)
	r.record("dev-1", resultAt(7, "admitted"))

	first, _ := r.report("dev-1", 7)
	second, _ := r.report("dev-1", 7)
	if first == second {
		t.Fatal("two reads returned the same message; a caller mutating one would change the other")
	}
	first.SetSequence(99)
	again, _ := r.report("dev-1", 7)
	if again.GetSequence() != 7 {
		t.Fatalf("mutating a handed-out report changed the stored one: sequence = %d", again.GetSequence())
	}
}

// TestForgettingAnOperationLetsItBeAdmittedAgain covers the leak. Without
// forget this map holds one entry for every operation the edge has ever run,
// for the lifetime of the process.
func TestForgettingAnOperationLetsItBeAdmittedAgain(t *testing.T) {
	r := newRegistry()
	r.admit("dev-1", 7)
	r.record("dev-1", resultAt(7, "released"))
	r.forget("dev-1", 7)

	if _, known := r.report("dev-1", 7); known {
		t.Fatal("a forgotten operation is still known")
	}
	if !r.admit("dev-1", 7) {
		t.Fatal("a forgotten sequence could not be admitted again")
	}
}

// TestConcurrentDispatchAndReportPathsDoNotRace is the shape that produced
// this build's one data race: per-device state written by one path and read by
// another. Under -race, unguarded access here fails.
func TestConcurrentDispatchAndReportPathsDoNotRace(t *testing.T) {
	r := newRegistry()
	const operations = 200

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := range operations {
			r.admit("dev-1", uint64(i))
		}
	}()
	go func() {
		defer wg.Done()
		for i := range operations {
			r.record("dev-1", resultAt(uint64(i), "admitted"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := range operations {
			r.report("dev-1", uint64(i))
			// A second device on the same registry, because the map is
			// shared across devices and a lock that only ever sees one key
			// is not the lock this needs to be.
			r.report("dev-2", uint64(i))
		}
	}()
	wg.Wait()

	// Every operation admitted is known afterwards: the race detector proves
	// the accesses were guarded, and this proves they also happened.
	for i := range operations {
		if _, known := r.report("dev-1", uint64(i)); !known {
			t.Fatalf("operation %d was admitted and is not known", i)
		}
	}
}
