package edgebus

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"go.aledante.io/FlowSeer/src/common/service"
)

func TestFollowerAttachesAnEdgeAttachedLater(t *testing.T) {
	hub, err := StartHub(context.Background(), HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)

	attached := make(chan string, 1)
	follower, err := FollowEdges(context.Background(), hub, 10*time.Millisecond, func(edgeID string) (jetstream.ConsumeContext, error) {
		attached <- edgeID
		return newFollowerConsume(), nil
	})
	if err != nil {
		t.Fatalf("follow edges: %v", err)
	}
	t.Cleanup(follower.Close)

	const edgeID = "edge-later"
	if err := hub.AttachEdge(context.Background(), DefaultTenant, edgeID); err != nil {
		t.Fatalf("attach edge: %v", err)
	}
	select {
	case got := <-attached:
		if got != edgeID {
			t.Errorf("attached edge = %q, want %q", got, edgeID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follower did not attach the later edge")
	}
}

func TestFollowerCloseDrainsEveryConsumer(t *testing.T) {
	hub, err := StartHub(context.Background(), HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	for _, edgeID := range []string{"edge-a", "edge-b"} {
		if err := hub.AttachEdge(context.Background(), DefaultTenant, edgeID); err != nil {
			t.Fatalf("attach %s: %v", edgeID, err)
		}
	}

	var mu sync.Mutex
	consumers := map[string]*followerConsume{}
	follower, err := FollowEdges(context.Background(), hub, time.Hour, func(edgeID string) (jetstream.ConsumeContext, error) {
		consumer := newFollowerConsume()
		mu.Lock()
		consumers[edgeID] = consumer
		mu.Unlock()
		return consumer, nil
	})
	if err != nil {
		t.Fatalf("follow edges: %v", err)
	}

	follower.Close()
	for _, edgeID := range []string{"edge-a", "edge-b"} {
		mu.Lock()
		consumer := consumers[edgeID]
		mu.Unlock()
		if consumer == nil {
			t.Fatalf("no consumer for %s", edgeID)
		}
		select {
		case <-consumer.closed:
		case <-time.After(time.Second):
			t.Fatalf("consumer for %s was not drained", edgeID)
		}
	}
}

type followerConsume struct {
	closed chan struct{}
	once   sync.Once
}

func newFollowerConsume() *followerConsume {
	return &followerConsume{closed: make(chan struct{})}
}

func (*followerConsume) Stop() {}

func (c *followerConsume) Drain() {
	c.once.Do(func() { close(c.closed) })
}

func (c *followerConsume) Closed() <-chan struct{} { return c.closed }
