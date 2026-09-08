package dispatch

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	connect "connectrpc.com/connect"

	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeSubscribe identifies a dispatch loop that could not be started.
var ErrCodeSubscribe = errs.NewCode("agent/subscribe")

const (
	defaultMinBackoff = time.Second
	defaultMaxBackoff = 30 * time.Second
)

// Subscriber opens central's dispatch stream. Satisfied by the generated
// client.
type Subscriber interface {
	Subscribe(context.Context, *connect.Request[integrationv1.SubscribeRequest]) (*connect.ServerStreamForClient[integrationv1.SubscribeResponse], error)
}

// Handler applies one dispatch. Returning an error does not end the stream:
// central re-sends what it is still owed, and a message this edge cannot
// apply is answered with a refusal rather than by hanging up.
type Handler interface {
	Handle(ctx context.Context, message *integrationv1.SubscribeResponse) error
}

// Contact is what a watcher outside the loop can see of it.
//
// A reconnecting client is a mechanism that acts when nothing is happening,
// and the failure it hides is total: a loop that has never connected looks
// exactly like one with nothing to do. These three numbers are what tell them
// apart, and a watcher reads them together — Connections zero with Failures
// climbing is a client that has been dead since its first attempt, and
// Connections zero with Failures zero is a first stream still open.
//
// Connections counts streams central actually served, which is not the same
// as calls to Subscribe. Connect's server-stream client returns before the
// server has answered — the transport error surfaces on the first Receive —
// so counting at the call counts attempts, including every one that was
// refused, and the number meant to prove a client is alive is then non-zero
// for a client that has never reached anything. A stream is counted once it
// has delivered a message or ended without error.
//
// It counts contact, not useful service, and the difference has a case. A
// central that accepts every stream and closes it immediately leaves
// Connections climbing, Failures at zero, and the loop backing off to its
// ceiling — because the backoff resets on a delivered message and that
// central delivers none. Read against the two numbers above, that looks
// healthy; it is a loop degrading quietly. Neither number is wrong, and a
// watcher that needs to tell them apart wants Messages, which stays at zero
// there and does not for a working edge.
type Contact struct {
	connections atomic.Int64
	failures    atomic.Int64
	messages    atomic.Int64
}

// Connections is how many streams central served. A stream currently open
// that has delivered nothing yet is not counted until it does or ends: this
// number never overstates contact, which is the direction that matters, since
// its purpose is to prove a client is not dead.
func (c *Contact) Connections() int64 { return c.connections.Load() }

// Failures is how many attempts have failed, whether opening or mid-stream.
func (c *Contact) Failures() int64 { return c.failures.Load() }

// Messages is how many dispatches have arrived.
func (c *Contact) Messages() int64 { return c.messages.Load() }

// Config declares the loop. Construct with keyed fields.
type Config struct {
	Client  Subscriber
	Handler Handler
	// MinBackoff and MaxBackoff bound the wait between attempts, doubling
	// from the first. Zero means one second and thirty.
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// Resync runs before every attempt to open the stream, the first one
	// included. It is where the edge re-lists the devices it serves.
	//
	// Before rather than after, because a dispatch for a device this edge
	// has not onboarded has nowhere to go: the lane refuses it and central
	// has to re-send. Listing first means the reconnection that follows a
	// device being added centrally is the reconnection that can serve it.
	//
	// A failure does not stop the attempt. Whatever kept the listing from
	// answering will most likely stop the stream too, and if it does not,
	// an edge that can still serve the devices it already holds is worth
	// more than one that stops for the ones it does not. Nil skips it.
	Resync func(ctx context.Context) error
	// Wait blocks for d or until ctx ends, reporting whether it elapsed.
	// Nil means a timer; a test substitutes it.
	Wait   func(ctx context.Context, d time.Duration) bool
	Logger *slog.Logger
}

// Run holds central's dispatch stream open until ctx ends, reconnecting with
// backoff whenever it drops.
//
// The stream ending is not an error. Central closes it on its own shutdown,
// on a retirement, and whenever its own connection goes; an edge that treated
// any of those as fatal would need a supervisor to do what this loop does
// anyway. What is reported is the shape of it: every attempt is counted, so
// a loop that has never succeeded is visible as a number rather than as
// silence.
//
// The backoff resets on a delivered message, not on a successful open. A
// stream that opens and immediately drops is a failing central, and resetting
// on the open alone would retry it a thousand times a second.
//
// Each attempt is preceded by cfg.Resync, so the devices this edge serves are
// listed before anything can be dispatched for them.
func Run(ctx context.Context, cfg Config, contact *Contact) error {
	if cfg.Client == nil || cfg.Handler == nil {
		return errs.New().Code(ErrCodeSubscribe).Msg("the dispatch loop needs a client and a handler")
	}
	minBackoff, maxBackoff := cfg.MinBackoff, cfg.MaxBackoff
	if minBackoff <= 0 {
		minBackoff = defaultMinBackoff
	}
	if maxBackoff < minBackoff {
		maxBackoff = max(defaultMaxBackoff, minBackoff)
	}
	wait := cfg.Wait
	if wait == nil {
		wait = waitFor
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	backoff := minBackoff
	for {
		if cfg.Resync != nil {
			if err := cfg.Resync(ctx); err != nil {
				log.WarnContext(ctx, "device listing failed",
					slog.String("otel.event.name", "flowseer.edge.devices.listing_failed"),
					slog.Any("error", err))
			}
		}

		delivered, err := attempt(ctx, cfg, contact, log)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			contact.failures.Add(1)
			log.WarnContext(ctx, "dispatch stream ended",
				slog.String("otel.event.name", "flowseer.edge.dispatch.disconnected"),
				slog.Int64("flowseer.edge.dispatch.messages", delivered),
				slog.Any("error", err))
		}

		if delivered > 0 {
			// This attempt did real work, so whatever ended it was not a
			// central refusing to serve us. Start over at the floor rather
			// than carrying a long backoff earned by earlier failures.
			backoff = minBackoff
		}
		if !wait(ctx, backoff) {
			return nil
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// attempt opens the stream once and reads it until it ends, returning how
// many messages it delivered.
func attempt(ctx context.Context, cfg Config, contact *Contact, log *slog.Logger) (int64, error) {
	stream, err := cfg.Client.Subscribe(ctx, connect.NewRequest(&integrationv1.SubscribeRequest{}))
	if err != nil {
		return 0, err
	}
	defer func() { _ = stream.Close() }()

	var delivered int64
	served := func() {
		if delivered != 1 {
			return
		}
		// Counted on the first message rather than at the call: until
		// something arrives, all this edge knows is that it handed a request
		// to a transport.
		contact.connections.Add(1)
		log.InfoContext(ctx, "dispatch stream open",
			slog.String("otel.event.name", "flowseer.edge.dispatch.connected"),
			slog.Int64("flowseer.edge.dispatch.connections", contact.connections.Load()))
	}

	for stream.Receive() {
		message := stream.Msg()
		contact.messages.Add(1)
		delivered++
		served()
		if err := cfg.Handler.Handle(ctx, message); err != nil {
			// Not fatal to the stream. Central re-sends what it is still
			// owed, and a message this edge cannot apply has already been
			// answered with a refusal by the handler; dropping the stream
			// would cost every other device's messages for one device's
			// problem.
			log.WarnContext(ctx, "dispatch could not be applied",
				slog.String("otel.event.name", "flowseer.edge.dispatch.refused"),
				slog.String("flowseer.device.id", message.GetDeviceId()),
				slog.Any("error", err))
		}
	}

	err = stream.Err()
	if err == nil && delivered == 0 {
		// Served, and had nothing to say. Counted, because central answering
		// and closing is contact — and not counting it would leave an edge
		// central has nothing for looking identical to one that cannot reach
		// central at all.
		contact.connections.Add(1)
	}
	return delivered, err
}

func waitFor(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
