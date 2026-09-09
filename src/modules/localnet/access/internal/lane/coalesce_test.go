package lane_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/lane"
)

func readKey() lane.CoalesceKey {
	return lane.CoalesceKey{
		Device: "d1", OperationKind: "interface.read", Target: "ethernet 1/1/1",
		PolicyKey: "read-policy", PolicyVersion: 1,
	}
}

func TestCoalescerSharesOneResultAcrossConcurrentCallers(t *testing.T) {
	c := &lane.Coalescer{}
	key := readKey()

	first, finish, isNew := c.Start(key)
	if !isNew {
		t.Fatal("expected the first caller to be new")
	}

	second, _, isNew := c.Start(key)
	if isNew {
		t.Fatal("expected the second caller to join the in-flight Ticket")
	}

	var wg sync.WaitGroup
	results := make([]any, 2)
	errs := make([]error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0], errs[0] = first.Wait(context.Background())
	}()
	go func() {
		defer wg.Done()
		results[1], errs[1] = second.Wait(context.Background())
	}()

	finish("observed", nil)
	wg.Wait()

	if results[0] != "observed" || results[1] != "observed" {
		t.Fatalf("expected both waiters to see the same result, got %v and %v", results[0], results[1])
	}
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("expected no error, got %v and %v", errs[0], errs[1])
	}
}

func TestCoalescerStartsFreshWorkAfterFinish(t *testing.T) {
	c := &lane.Coalescer{}
	key := readKey()

	_, finish, isNew := c.Start(key)
	if !isNew {
		t.Fatal("expected the first Start to be new")
	}
	finish("first", nil)

	_, _, isNew = c.Start(key)
	if !isNew {
		t.Fatal("expected a Start after the terminator to admit fresh work")
	}
}

func TestCoalescerNeverSharesAcrossMutationAndReadOperationKinds(t *testing.T) {
	c := &lane.Coalescer{}
	read := readKey()
	mutation := lane.CoalesceKey{
		Device: "d1", OperationKind: "interface.description_change", Target: "ethernet 1/1/1",
		PolicyKey: "read-policy", PolicyVersion: 1,
	}

	_, _, readIsNew := c.Start(read)
	_, _, mutationIsNew := c.Start(mutation)

	if !readIsNew || !mutationIsNew {
		t.Fatal("expected a read and a mutation on the same interface to never coalesce, even with an identical target")
	}
}

// TestCoalescerNeverSharesAcrossAccessPolicies is the authority half of the
// key. A joiner is answered from the session the owner opened, so two reads
// that coalesce are two reads served under one acquisition — and if they were
// admitted under different access policies, the joiner's own policy was never
// checked against anything.
func TestCoalescerNeverSharesAcrossAccessPolicies(t *testing.T) {
	c := &lane.Coalescer{}
	underA := readKey()
	underB := underA
	underB.PolicyKey = "another-policy"

	_, _, firstIsNew := c.Start(underA)
	_, _, secondIsNew := c.Start(underB)

	if !firstIsNew || !secondIsNew {
		t.Fatal("two reads under different access policies shared one ticket")
	}

	sameKeyNewVersion := underA
	sameKeyNewVersion.PolicyVersion = 2
	if _, _, isNew := c.Start(sameKeyNewVersion); !isNew {
		t.Fatal("a rotated policy version joined the ticket admitted under the old one")
	}
}

// TestALateTerminatorDoesNotAnswerTheNextGroup pins what makes the
// terminator bound to its ticket rather than named by its key.
//
// A key does not identify a ticket over time. A terminator that resolved its
// key would deliver this work's outcome to whichever group happened to be
// waiting when it ran, which after a second Start for the same key is a
// different group of callers waiting on different work.
func TestALateTerminatorDoesNotAnswerTheNextGroup(t *testing.T) {
	c := &lane.Coalescer{}
	key := readKey()

	_, firstFinish, _ := c.Start(key)
	firstFinish("first", nil)

	second, secondFinish, isNew := c.Start(key)
	if !isNew {
		t.Fatal("expected fresh work after the first group finished")
	}

	// The first group's terminator runs again, late.
	firstFinish("stale", errors.New("stale"))

	secondFinish("second", nil)
	got, err := second.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() error = %v, want the second group's own outcome", err)
	}
	if got != "second" {
		t.Errorf("Wait() = %v, want %q: a late terminator answered the next group", got, "second")
	}
}
