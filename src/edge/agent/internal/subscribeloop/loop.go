package subscribeloop

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeRun identifies a loop that could not be started because its Config
// was incomplete.
var ErrCodeRun = errs.NewCode("agent/subscribeloop-run")

const (
	defaultMinBackoff = time.Second
	defaultMaxBackoff = 30 * time.Second
)

// Stream is what Run reads from one open attempt.
// *connect.ServerStreamForClient[T] satisfies it structurally.
type Stream[T any] interface {
	Receive() bool
	Msg() *T
	Err() error
	Close() error
}

// Opener opens a fresh Stream for one attempt.
type Opener[T any] func(ctx context.Context) (Stream[T], error)

// Handler applies one message. Returning an error does not end the stream:
// the caller decides, through its own protocol, whether a message it cannot
// apply is retried, refused, or otherwise accounted for — the loop only
// counts and logs the drop.
type Handler[T any] interface {
	Handle(ctx context.Context, message *T) error
}

// Events names what Run logs and the attribute keys its own counts are
// logged under. Connected, Disconnected, Dropped, and ResyncFailed are the
// otel.event.name values for the loop's four events. ConnectionCountKey and
// MessageCountKey name the attributes carrying the connection count on the
// connected event and the delivered count on the disconnected event — values
// the loop computes itself, so the caller names them without supplying them.
type Events struct {
	Connected          string
	Disconnected       string
	Dropped            string
	ResyncFailed       string
	ConnectionCountKey string
	MessageCountKey    string
}

// validate refuses a naming the loop cannot log under, with withResync saying
// whether the resync event can fire at all.
//
// An empty name is not rejected anywhere downstream: it reaches a collector as
// a record with no event name, or a number under a key no query can name, and
// the caller learns of it from a dashboard that stayed empty rather than from
// a failure. Refused here instead, where the caller is still starting up.
func (e Events) validate(withResync bool) error {
	unnamed := e.Connected == "" || e.Disconnected == "" || e.Dropped == "" ||
		e.ConnectionCountKey == "" || e.MessageCountKey == "" ||
		(withResync && e.ResyncFailed == "")
	if unnamed {
		return errs.New().Code(ErrCodeRun).
			Msg("the subscribe loop needs a name for every event it logs and a key for each of its two counts")
	}
	return nil
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
// Connections counts streams the peer actually served, which is not the same
// as calls to Open. Connect's server-stream client returns before the server
// has answered — the transport error surfaces on the first Receive — so
// counting at the call counts attempts, including every one that was
// refused, and the number meant to prove a client is alive is then non-zero
// for a client that has never reached anything. A stream is counted once it
// has delivered a message or ended without error.
//
// It counts contact, not useful service, and the difference has a case. A
// peer that accepts every stream and closes it immediately leaves
// Connections climbing, Failures at zero, and the loop backing off to its
// ceiling — because the backoff resets on a delivered message and that peer
// delivers none. Read against the two numbers above, that looks healthy; it
// is a loop degrading quietly. Neither number is wrong, and a watcher that
// needs to tell them apart wants Messages, which stays at zero there and does
// not for a working caller.
type Contact struct {
	connections atomic.Int64
	failures    atomic.Int64
	messages    atomic.Int64
}

// Connections is how many streams the peer served. A stream currently open
// that has delivered nothing yet is not counted until it does or ends: this
// number never overstates contact, which is the direction that matters, since
// its purpose is to prove a client is not dead.
func (c *Contact) Connections() int64 { return c.connections.Load() }

// Failures is how many attempts have failed, whether opening or mid-stream.
func (c *Contact) Failures() int64 { return c.failures.Load() }

// Messages is how many messages have arrived.
func (c *Contact) Messages() int64 { return c.messages.Load() }

// Config declares one loop. Construct with keyed fields.
type Config[T any] struct {
	Open    Opener[T]
	Handler Handler[T]
	// MinBackoff and MaxBackoff bound the wait between attempts, doubling
	// from the first. Zero means one second and thirty.
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// Resync runs before every attempt to open the stream, the first one
	// included.
	//
	// Before rather than after, because whatever Resync prepares this side
	// to receive has to be ready before the stream that carries it opens. A
	// failure does not stop the attempt: whatever kept Resync from answering
	// will most likely stop the stream too, and if it does not, a caller
	// that can still serve what it already holds is worth more than one that
	// stops for what it does not. Nil skips it.
	Resync func(ctx context.Context) error
	// Wait blocks for d or until ctx ends, reporting whether it elapsed.
	// Nil means a timer; a test substitutes it.
	Wait func(ctx context.Context, d time.Duration) bool
	// Events names the loop's four events and its two caller-named count
	// attributes.
	Events Events
	// LogAttrs returns the per-message attributes to add to the dropped
	// event. Nil adds none.
	LogAttrs func(message *T) []slog.Attr
	Logger   *slog.Logger
}

// Run holds a stream open until ctx ends, reconnecting with backoff whenever
// it drops.
//
// The stream ending is not an error. A peer closes it on its own shutdown,
// on a retirement, and whenever its own connection goes; a caller that
// treated any of those as fatal would need a supervisor to do what this loop
// does anyway. What is reported is the shape of it: every attempt is
// counted, so a loop that has never succeeded is visible as a number rather
// than as silence.
//
// The backoff resets on a delivered message, not on a successful open. A
// stream that opens and immediately drops is a failing peer, and resetting
// on the open alone would retry it a thousand times a second.
//
// Each attempt is preceded by cfg.Resync, so whatever it prepares is ready
// before anything can arrive that depends on it.
func Run[T any](ctx context.Context, cfg Config[T], contact *Contact) error {
	if cfg.Open == nil || cfg.Handler == nil {
		return errs.New().Code(ErrCodeRun).Msg("the subscribe loop needs an opener and a handler")
	}
	if err := cfg.Events.validate(cfg.Resync != nil); err != nil {
		return err
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
				log.WarnContext(ctx, "resync failed",
					slog.String("otel.event.name", cfg.Events.ResyncFailed),
					slog.String("error.type", errorType(err)))
			}
		}

		delivered, err := attempt(ctx, cfg, contact, log)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			contact.failures.Add(1)
			log.WarnContext(ctx, "stream ended",
				slog.String("otel.event.name", cfg.Events.Disconnected),
				slog.Int64(cfg.Events.MessageCountKey, delivered),
				slog.String("error.type", errorType(err)))
		}

		if delivered > 0 {
			// This attempt did real work, so whatever ended it was not a
			// peer refusing to serve us. Start over at the floor rather
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
func attempt[T any](ctx context.Context, cfg Config[T], contact *Contact, log *slog.Logger) (int64, error) {
	stream, err := cfg.Open(ctx)
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
		// something arrives, all this side knows is that it handed a
		// request to a transport.
		contact.connections.Add(1)
		log.InfoContext(ctx, "stream opened",
			slog.String("otel.event.name", cfg.Events.Connected),
			slog.Int64(cfg.Events.ConnectionCountKey, contact.connections.Load()))
	}

	for stream.Receive() {
		message := stream.Msg()
		contact.messages.Add(1)
		delivered++
		served()
		if err := cfg.Handler.Handle(ctx, message); err != nil {
			// Dropped, not refused: this loop only knows the handler
			// answered no arm of it. Not fatal to the stream — dropping it
			// would cost every other message for one message's problem.
			attrs := []any{slog.String("otel.event.name", cfg.Events.Dropped)}
			if cfg.LogAttrs != nil {
				for _, a := range cfg.LogAttrs(message) {
					attrs = append(attrs, a)
				}
			}
			attrs = append(attrs, slog.String("error.type", errorType(err)))
			log.WarnContext(ctx, "message dropped; the handler could not apply it", attrs...)
		}
	}

	err = stream.Err()
	if err == nil && delivered == 0 {
		// Served, and had nothing to say. Counted, because a peer answering
		// and closing is contact — and not counting it would leave a caller
		// the peer has nothing for looking identical to one that cannot
		// reach the peer at all.
		contact.connections.Add(1)
	}
	return delivered, err
}

// errorType classifies a failure for the error.type attribute: the error's
// own code where it has one, and the two context causes by name where it does
// not. A bounded value, because it becomes a metric dimension downstream —
// the error's message must not, since it carries whatever the transport put
// in it.
func errorType(err error) string {
	if code, ok := errs.CodeOf(err); ok {
		return string(code)
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "context.deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "context.canceled"
	default:
		return "unknown"
	}
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
