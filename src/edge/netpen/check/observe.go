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
// closed as well. A closed channel otherwise ends the window early with no
// error. A window of zero or less is an error, since the runner always
// supplies one. First runs Receive under a child context it cancels and
// drains before returning, so the leg is free for the next Receive.
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
				return obs, ctx.Err()
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
