package lane_test

import (
	"context"
	"sync"
	"testing"

	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/lane"
)

func TestCoalescerSharesOneResultAcrossConcurrentCallers(t *testing.T) {
	c := &lane.Coalescer{}
	key := lane.CoalesceKey{Device: "d1", OperationKind: "interface.read", Target: "ethernet 1/1/1"}

	first, isNew := c.Start(key)
	if !isNew {
		t.Fatal("expected the first caller to be new")
	}

	second, isNew := c.Start(key)
	if isNew {
		t.Fatal("expected the second caller to join the in-flight ticket")
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

	c.Finish(key, "observed", nil)
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
	key := lane.CoalesceKey{Device: "d1", OperationKind: "interface.read", Target: "ethernet 1/1/1"}

	_, isNew := c.Start(key)
	if !isNew {
		t.Fatal("expected the first Start to be new")
	}
	c.Finish(key, "first", nil)

	_, isNew = c.Start(key)
	if !isNew {
		t.Fatal("expected a Start after Finish to admit fresh work")
	}
}

func TestCoalescerNeverSharesAcrossMutationAndReadOperationKinds(t *testing.T) {
	c := &lane.Coalescer{}
	readKey := lane.CoalesceKey{Device: "d1", OperationKind: "interface.read", Target: "ethernet 1/1/1"}
	mutationKey := lane.CoalesceKey{Device: "d1", OperationKind: "interface.description_change", Target: "ethernet 1/1/1"}

	_, readIsNew := c.Start(readKey)
	_, mutationIsNew := c.Start(mutationKey)

	if !readIsNew || !mutationIsNew {
		t.Fatal("expected a read and a mutation on the same interface to never coalesce, even with an identical target")
	}
}
