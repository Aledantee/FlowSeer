package edgebus

import (
	"context"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

// EdgeFollower follows the source stream of every edge attached to a hub.
type EdgeFollower struct {
	hub      *Hub
	interval time.Duration
	attach   func(context.Context, context.Context, string) (jetstream.ConsumeContext, error)
	cancel   context.CancelFunc
	done     chan struct{}

	mu        sync.Mutex
	consumers map[string]jetstream.ConsumeContext
	closeOnce sync.Once
	closeDone chan struct{}
}

// FollowEdges starts discovery of attached edge streams. Discovery ends when
// ctx ends or [EdgeFollower.Close] is called. Delivery lifetime ends when ctx
// ends, Close is called, or the first discovery pass fails, before the drain.
// Consumers stay attached after ctx ends until Close. The returned follower
// must be closed. attach receives the pass context, the follower lifetime
// context, and the edge id. The pass context bounds the attach operation. An
// interval pass failure leaves the delivery lifetime live.
func FollowEdges(ctx context.Context, hub *Hub, interval time.Duration, attach func(context.Context, context.Context, string) (jetstream.ConsumeContext, error)) (*EdgeFollower, error) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	runCtx, cancel := context.WithCancel(ctx)
	f := &EdgeFollower{
		hub: hub, interval: interval, attach: attach, cancel: cancel,
		done: make(chan struct{}), consumers: map[string]jetstream.ConsumeContext{},
		closeDone: make(chan struct{}),
	}
	if err := f.discover(ctx, runCtx); err != nil {
		cancel()
		f.drainConsumers()
		return nil, err
	}
	spawn.Go(runCtx, "edgebus.EdgeFollower.follow", func() { f.follow(runCtx) })
	return f, nil
}

// Close stops discovery and drains every edge consumer.
func (f *EdgeFollower) Close() {
	f.closeOnce.Do(func() {
		f.cancel()
		<-f.done
		f.drainConsumers()
		close(f.closeDone)
	})
	<-f.closeDone
}

func (f *EdgeFollower) follow(ctx context.Context) {
	defer close(f.done)
	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			_ = f.discover(ctx, ctx)
		}
	}
}

func (f *EdgeFollower) discover(ctx, lifetime context.Context) error {
	edges := f.hub.AttachedEdges()
	for _, edgeID := range edges {
		f.mu.Lock()
		_, following := f.consumers[edgeID]
		f.mu.Unlock()
		if following {
			continue
		}
		consumer, err := f.attach(ctx, lifetime, edgeID)
		if err != nil {
			return err
		}
		if consumer == nil {
			return errs.New().Attr("edge", edgeID).Msg("edge attach returned a nil consumer")
		}
		f.mu.Lock()
		f.consumers[edgeID] = consumer
		f.mu.Unlock()
	}
	return nil
}

func (f *EdgeFollower) drainConsumers() {
	f.mu.Lock()
	consumers := f.consumers
	f.consumers = map[string]jetstream.ConsumeContext{}
	f.mu.Unlock()
	for _, consumer := range consumers {
		consumer.Drain()
		<-consumer.Closed()
	}
}
