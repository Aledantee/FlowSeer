package pump

import (
	"context"
	"sync"

	"go.aledante.io/FlowSeer/src/common/spawn"
)

// Merge forwards every value of each source into the returned pump. It
// preserves each source's order but does not define an order across sources.
// A source failure stops the other sources and becomes the merged pump's first
// terminal error. Context cancellation records the unwrapped context error when
// no source error was recorded first. The caller owns the returned pump and
// must call [Pump.Cancel] when finished; Merge signals sources to stop but never
// closes or cancels them. The context and buffer follow [New]'s rules.
func Merge[T any](ctx context.Context, buf int, sources ...*Pump[T]) *Pump[T] {
	merged := New[T](ctx, buf)
	if len(sources) == 0 {
		merged.Done()
		return merged
	}

	results := make(chan error, len(sources))
	stopForwarders := make(chan struct{})
	var forwarders sync.WaitGroup
	// forward drains one source into merged and returns that source's
	// terminal error, or nil when it stopped for any other reason. Its result
	// reaches results through exactly one send: the call below on the normal
	// path, or the ReportTo sink when it panics. A panic must not report nil,
	// which the coordinator reads as a source that finished cleanly.
	forward := func(source *Pump[T]) error {
		for {
			select {
			case <-stopForwarders:
				return nil
			default:
			}

			select {
			case <-merged.Stopped():
				return nil
			case <-merged.Context().Done():
				merged.SignalStop()
				return nil
			case <-stopForwarders:
				return nil
			case value, ok := <-source.Data():
				if !ok {
					return source.Err()
				}
				if !merged.Send(value) {
					return nil
				}
			}
		}
	}
	for _, source := range sources {
		forwarders.Add(1)
		spawn.Go(ctx, "Merge.forward", func() {
			defer forwarders.Done()
			results <- forward(source)
		}, spawn.ReportTo(func(err error) { results <- err }))
	}

	// Declared here rather than inside the coordinator so the panic sink can
	// reach it: Merge promises to signal its sources to stop, and a
	// coordinator that panicked would otherwise end the merged pump while
	// every source kept producing into it.
	var stopSourcesOnce sync.Once
	stopSources := func() {
		stopSourcesOnce.Do(func() {
			for _, source := range sources {
				source.SignalStop()
			}
		})
	}

	spawn.Go(ctx, "Merge.coordinate", func() {
		stopped := merged.Stopped()
		contextDone := merged.Context().Done()
		var terminalErr error
		for remaining := len(sources); remaining > 0; {
			select {
			case sourceErr := <-results:
				remaining--
				if sourceErr == nil || terminalErr != nil {
					continue
				}
				if err := merged.Err(); err != nil {
					terminalErr = err
					continue
				}
				if err := merged.Context().Err(); err != nil {
					terminalErr = err
					merged.SignalStop()
					stopSources()
					continue
				}

				terminalErr = sourceErr
				stopSources()
				close(stopForwarders)
			case <-stopped:
				if terminalErr == nil {
					terminalErr = merged.Err()
					if terminalErr == nil {
						terminalErr = merged.Context().Err()
					}
				}
				stopSources()
				stopped = nil
			case <-contextDone:
				if terminalErr == nil {
					terminalErr = merged.Context().Err()
				}
				merged.SignalStop()
				stopSources()
				contextDone = nil
			}
		}
		select {
		case <-merged.Stopped():
			stopSources()
		default:
		}
		forwarders.Wait()

		if terminalErr != nil {
			merged.Fail(terminalErr)
			return
		}
		if err := merged.Context().Err(); err != nil {
			merged.Fail(err)
			return
		}
		merged.Done()
	}, spawn.ReportTo(func(err error) {
		// merged.Fail closes Data() and records the terminal error; without
		// this, a panic anywhere above would leave every consumer's range
		// over merged.Data() blocked forever.
		merged.Fail(err)
		// Merge's contract: the sources are told to stop. The coordinator's
		// normal exits do this; the panic path owes it too, or they go on
		// producing into a pump nothing is draining.
		stopSources()
	}))

	return merged
}
