package lane_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/lane"
)

func TestQueueOrdersByPriorityAcrossMixedSubmissions(t *testing.T) {
	q := lane.NewQueue(10)

	if _, err := q.Submit(lane.PriorityLow, "low"); err != nil {
		t.Fatalf("submit low: %v", err)
	}
	if _, err := q.Submit(lane.PriorityHigh, "high"); err != nil {
		t.Fatalf("submit high: %v", err)
	}
	if _, err := q.Submit(lane.PriorityNormal, "normal"); err != nil {
		t.Fatalf("submit normal: %v", err)
	}

	wantOrder := []string{"high", "normal", "low"}
	for _, want := range wantOrder {
		item, ok := q.Next()
		if !ok {
			t.Fatalf("expected an item, queue empty early")
		}
		if got := item.Payload.(string); got != want {
			t.Fatalf("expected %q next, got %q", want, got)
		}
	}
}

func TestQueueIsFIFOAmongEqualPriority(t *testing.T) {
	q := lane.NewQueue(10)

	for _, payload := range []string{"first", "second", "third"} {
		if _, err := q.Submit(lane.PriorityNormal, payload); err != nil {
			t.Fatalf("submit %s: %v", payload, err)
		}
	}

	for _, want := range []string{"first", "second", "third"} {
		item, ok := q.Next()
		if !ok {
			t.Fatal("expected an item, queue empty early")
		}
		if got := item.Payload.(string); got != want {
			t.Fatalf("expected %q next, got %q", want, got)
		}
	}
}

func TestQueueOverloadPreservesExistingEntries(t *testing.T) {
	q := lane.NewQueue(2)

	if _, err := q.Submit(lane.PriorityNormal, "a"); err != nil {
		t.Fatalf("submit a: %v", err)
	}
	if _, err := q.Submit(lane.PriorityNormal, "b"); err != nil {
		t.Fatalf("submit b: %v", err)
	}

	_, err := q.Submit(lane.PriorityNormal, "c")
	if code, _ := errs.CodeOf(err); code != lane.ErrCodeOverload {
		t.Fatalf("expected ErrCodeOverload, got %v", err)
	}

	if got := q.Len(); got != 2 {
		t.Fatalf("expected 2 items to remain admitted, got %d", got)
	}

	first, ok := q.Next()
	if !ok || first.Payload.(string) != "a" {
		t.Fatalf("expected a first, got %v ok=%v", first, ok)
	}
	second, ok := q.Next()
	if !ok || second.Payload.(string) != "b" {
		t.Fatalf("expected b second, got %v ok=%v", second, ok)
	}
}

func TestQueuePositionIsMonotonicAndNeverReassigned(t *testing.T) {
	q := lane.NewQueue(10)

	low, err := q.Submit(lane.PriorityLow, "low")
	if err != nil {
		t.Fatalf("submit low: %v", err)
	}
	high, err := q.Submit(lane.PriorityHigh, "high")
	if err != nil {
		t.Fatalf("submit high: %v", err)
	}

	if low.Position >= high.Position {
		t.Fatalf("expected low's position (%d) to precede high's (%d), since low was admitted first even though it dequeues later", low.Position, high.Position)
	}

	dequeued, _ := q.Next()
	if dequeued.Position != high.Position {
		t.Fatalf("expected high's position to be unchanged by dequeue ordering, got %d want %d", dequeued.Position, high.Position)
	}
}
