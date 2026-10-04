package edgebus

import (
	"context"
	"errors"
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
	follower, err := FollowEdges(context.Background(), hub, 10*time.Millisecond, func(_ context.Context, _ context.Context, edgeID string) (jetstream.ConsumeContext, error) {
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

func TestFollowerInitialAttachUsesCallerContext(t *testing.T) {
	hub, err := StartHub(context.Background(), HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	if err := hub.AttachEdge(context.Background(), DefaultTenant, "edge-a"); err != nil {
		t.Fatalf("attach edge: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	canceled := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		follower, err := FollowEdges(ctx, hub, time.Hour, func(attachCtx, _ context.Context, _ string) (jetstream.ConsumeContext, error) {
			close(started)
			<-attachCtx.Done()
			close(canceled)
			return nil, attachCtx.Err()
		})
		if follower != nil {
			follower.Close()
		}
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initial attach did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("initial attach context was not canceled")
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("initial discovery succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("initial discovery did not return")
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
	follower, err := FollowEdges(context.Background(), hub, time.Hour, func(_ context.Context, _ context.Context, edgeID string) (jetstream.ConsumeContext, error) {
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

func TestFollowerFirstDiscoveryFailureDrainsAttachedConsumers(t *testing.T) {
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

	attached := make(chan *followerConsume, 1)
	calls := 0
	_, err = FollowEdges(context.Background(), hub, time.Hour, func(_ context.Context, _ context.Context, _ string) (jetstream.ConsumeContext, error) {
		calls++
		consumer := newFollowerConsume()
		select {
		case attached <- consumer:
		default:
		}
		if calls == 1 {
			return consumer, nil
		}
		return nil, errors.New("injected discovery failure")
	})
	if err == nil {
		t.Fatal("first discovery succeeded")
	}
	select {
	case consumer := <-attached:
		select {
		case <-consumer.closed:
		case <-time.After(time.Second):
			t.Fatal("partially attached consumer was not drained")
		}
	case <-time.After(time.Second):
		t.Fatal("first discovery did not attach a consumer")
	}
}

func TestFollowerCloseCancelsIntervalAttach(t *testing.T) {
	hub, err := StartHub(context.Background(), HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	if err := hub.AttachEdge(context.Background(), DefaultTenant, "edge-a"); err != nil {
		t.Fatalf("attach first edge: %v", err)
	}

	started := make(chan struct{})
	canceled := make(chan struct{})
	var startedOnce sync.Once
	var canceledOnce sync.Once
	calls := 0
	follower, err := FollowEdges(context.Background(), hub, 10*time.Millisecond, func(ctx context.Context, _ context.Context, _ string) (jetstream.ConsumeContext, error) {
		calls++
		if calls == 1 {
			return newFollowerConsume(), nil
		}
		startedOnce.Do(func() { close(started) })
		<-ctx.Done()
		canceledOnce.Do(func() { close(canceled) })
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("follow edges: %v", err)
	}

	if err := hub.AttachEdge(context.Background(), DefaultTenant, "edge-b"); err != nil {
		t.Fatalf("attach second edge: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("interval attach did not start")
	}
	// Wait for the next discovery tick to become pending while attach is blocked.
	<-time.After(30 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		follower.Close()
		close(done)
	}()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("interval attach context was not canceled")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("follower close did not finish")
	}
	if calls != 2 {
		t.Fatalf("attach calls = %d, want 2 before and during close", calls)
	}
}

func TestFollowerIntervalAttachFailureKeepsLifetime(t *testing.T) {
	hub, err := StartHub(context.Background(), HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	if err := hub.AttachEdge(context.Background(), DefaultTenant, "edge-a"); err != nil {
		t.Fatalf("attach first edge: %v", err)
	}

	var lifetime context.Context
	failed := make(chan struct{})
	var failedOnce sync.Once
	follower, err := FollowEdges(context.Background(), hub, 10*time.Millisecond, func(_ context.Context, lifetimeCtx context.Context, edgeID string) (jetstream.ConsumeContext, error) {
		if edgeID == "edge-a" {
			lifetime = lifetimeCtx
			return newFollowerConsume(), nil
		}
		failedOnce.Do(func() { close(failed) })
		return nil, errors.New("injected interval discovery failure")
	})
	if err != nil {
		t.Fatalf("follow edges: %v", err)
	}
	t.Cleanup(follower.Close)
	if lifetime == nil {
		t.Fatal("initial attach did not capture follower lifetime")
	}
	if err := hub.AttachEdge(context.Background(), DefaultTenant, "edge-b"); err != nil {
		t.Fatalf("attach later edge: %v", err)
	}
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("interval attach did not fail")
	}
	select {
	case <-lifetime.Done():
		t.Fatal("interval attach failure canceled follower lifetime")
	default:
	}
}

func TestFollowerLifetimeEndsAfterClose(t *testing.T) {
	hub, err := StartHub(context.Background(), HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	if err := hub.AttachEdge(context.Background(), DefaultTenant, "edge-a"); err != nil {
		t.Fatalf("attach edge: %v", err)
	}

	var lifetime context.Context
	follower, err := FollowEdges(context.Background(), hub, time.Hour, func(_ context.Context, lifetimeCtx context.Context, _ string) (jetstream.ConsumeContext, error) {
		lifetime = lifetimeCtx
		return newFollowerConsume(), nil
	})
	if err != nil {
		t.Fatalf("follow edges: %v", err)
	}
	if lifetime == nil {
		t.Fatal("initial attach did not capture follower lifetime")
	}
	follower.Close()
	select {
	case <-lifetime.Done():
	case <-time.After(time.Second):
		t.Fatal("follower lifetime did not end after close")
	}
}

func TestFollowerLifetimeEndsAfterFirstDiscoveryFailure(t *testing.T) {
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

	var lifetime context.Context
	_, err = FollowEdges(context.Background(), hub, time.Hour, func(_ context.Context, lifetimeCtx context.Context, edgeID string) (jetstream.ConsumeContext, error) {
		lifetime = lifetimeCtx
		if edgeID == "edge-a" {
			return newFollowerConsume(), nil
		}
		return nil, errors.New("injected first discovery failure")
	})
	if err == nil {
		t.Fatal("first discovery succeeded")
	}
	if lifetime == nil {
		t.Fatal("initial attach did not capture follower lifetime")
	}
	select {
	case <-lifetime.Done():
	case <-time.After(time.Second):
		t.Fatal("follower lifetime did not end after first discovery failure")
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
