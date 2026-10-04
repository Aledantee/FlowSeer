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
	attach   func(string) (jetstream.ConsumeContext, error)
	cancel   context.CancelFunc
	done     chan struct{}

	mu        sync.Mutex
	consumers map[string]jetstream.ConsumeContext
	closeOnce sync.Once
	closeDone chan struct{}
}

// FollowEdges starts discovery of attached edge streams.
func FollowEdges(ctx context.Context, hub *Hub, interval time.Duration, attach func(string) (jetstream.ConsumeContext, error)) (*EdgeFollower, error) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	runCtx, cancel := context.WithCancel(ctx)
	f := &EdgeFollower{
		hub: hub, interval: interval, attach: attach, cancel: cancel,
		done: make(chan struct{}), consumers: map[string]jetstream.ConsumeContext{},
		closeDone: make(chan struct{}),
	}
	if err := f.discover(); err != nil {
		cancel()
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
		f.mu.Lock()
		consumers := f.consumers
		f.consumers = map[string]jetstream.ConsumeContext{}
		f.mu.Unlock()
		for _, consumer := range consumers {
			consumer.Drain()
			<-consumer.Closed()
		}
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
			_ = f.discover()
		}
	}
}

func (f *EdgeFollower) discover() error {
	for _, edgeID := range f.hub.AttachedEdges() {
		f.mu.Lock()
		_, following := f.consumers[edgeID]
		f.mu.Unlock()
		if following {
			continue
		}
		consumer, err := f.attach(edgeID)
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
