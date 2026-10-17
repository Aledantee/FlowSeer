package check

import (
	"context"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// Observation is what an observer learned beside the frames it kept: how
// many frames the predicate skipped and how long it listened. Observation
// values are safe for concurrent reads.
type Observation struct {
	Skipped int
	Elapsed time.Duration
}

// First returns the first frame on leg that match accepts, or nil when
// window ends without one. The frames come from a network peer, so match
// decodes nothing it does not check and returns false for a frame it
// cannot read. A frame carrying a terminal error returns that error at
// once. A canceled ctx returns ctx.Err(), even when the leg's channel is
// closed as well. A channel that closes under a live ctx is an error, since
// a leg that stops delivering before the window has not observed the
// window; the runner records it as a pending verdict. A window of zero or
// less is an error, since the runner always supplies one. First runs
// Receive under a child context it cancels and drains before returning, so
// the leg is free for the next Receive.
func First(ctx context.Context, leg link.Leg, window time.Duration, match func([]byte) bool) ([]byte, Observation, error) {
	var found []byte
	obs, err := observe(ctx, leg, window, func(data []byte) (kept, done bool) {
		if !match(data) {
			return false, false
		}
		found = data
		return true, true
	})
	return found, obs, err
}

// Collect returns every frame on leg that match accepts until window ends,
// under the same rules as [First].
func Collect(ctx context.Context, leg link.Leg, window time.Duration, match func([]byte) bool) ([][]byte, Observation, error) {
	var frames [][]byte
	obs, err := observe(ctx, leg, window, func(data []byte) (kept, done bool) {
		if !match(data) {
			return false, false
		}
		frames = append(frames, data)
		return true, false
	})
	return frames, obs, err
}

// Scan hands every frame on leg to each until window ends and keeps none,
// under the same rules as [First]. It is the observer for a check that
// counts frames or collects one field of each, whose memory then does not
// grow with the traffic the window carries. Every frame counts as skipped,
// since none is kept.
func Scan(ctx context.Context, leg link.Leg, window time.Duration, each func([]byte)) (Observation, error) {
	return observe(ctx, leg, window, func(data []byte) (kept, done bool) {
		each(data)
		return false, false
	})
}

// Sustain alternates one send with one bounded read until match accepts a
// frame or window ends: it calls send, observes leg with [First] for
// interval (or what is left of the window), and repeats. It returns the
// matched frame, nil at the window's end, and an Observation whose Elapsed
// spans the whole call and whose Skipped sums every read. match may record
// what it saw in the caller's variables, which is how a check reads the
// last state it saw once the window is spent. A send error, a receive
// error, or a canceled ctx returns at once with what was observed so far.
// An interval of zero or less is an error, as is a window of zero or less.
func Sustain(ctx context.Context, leg link.Leg, window, interval time.Duration, send func(context.Context) error, match func([]byte) bool) (found []byte, total Observation, err error) {
	if interval <= 0 {
		return nil, total, errs.New().
			Attr("interval", interval).
			Msg("sustain interval is not positive")
	}
	if window <= 0 {
		return nil, total, errs.New().
			Attr("window", window).
			Msg("observation window is not positive")
	}
	start := time.Now()
	defer func() { total.Elapsed = time.Since(start) }()
	for {
		if err := send(ctx); err != nil {
			return nil, total, err
		}
		read := min(interval, window-time.Since(start))
		if read <= 0 {
			return nil, total, nil
		}
		var obs Observation
		found, obs, err = First(ctx, leg, read, match)
		total.Skipped += obs.Skipped
		if err != nil {
			return nil, total, err
		}
		if found != nil {
			return found, total, nil
		}
		if time.Since(start) >= window {
			return nil, total, nil
		}
	}
}

// observe reads leg for window and hands each frame to each, which reports
// whether it kept the frame and whether the observation is complete. A
// frame each does not keep counts as skipped.
func observe(ctx context.Context, leg link.Leg, window time.Duration, each func([]byte) (kept, done bool)) (obs Observation, err error) {
	if window <= 0 {
		return obs, errs.New().
			Attr("window", window).
			Msg("observation window is not positive")
	}
	start := time.Now()
	rxCtx, cancel := context.WithCancel(ctx)
	frames := leg.Receive(rxCtx)
	defer func() {
		cancel()
		for range frames {
		}
		obs.Elapsed = time.Since(start)
	}()
	timer := time.NewTimer(window)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return obs, ctx.Err()
		case <-timer.C:
			return obs, nil
		case f, ok := <-frames:
			if !ok {
				if ctx.Err() != nil {
					return obs, ctx.Err()
				}
				return obs, errs.New().
					Attr("elapsed", time.Since(start)).
					Msg("leg closed before the window")
			}
			if f.Err != nil {
				return obs, f.Err
			}
			kept, done := each(f.Data)
			if !kept {
				obs.Skipped++
			}
			if done {
				return obs, nil
			}
		}
	}
}
