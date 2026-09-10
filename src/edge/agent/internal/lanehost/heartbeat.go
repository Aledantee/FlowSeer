// Package lanehost is the agent's side of the device access lane: what keeps
// contact with central, and what that contact's loss does to the lane.
package lanehost

import (
	"context"
	"log/slog"
	"time"

	connect "connectrpc.com/connect"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeHeartbeat identifies a heartbeat loop that could not be started.
var ErrCodeHeartbeat = errs.NewCode("agent/heartbeat")

const (
	defaultInterval       = 30 * time.Second
	defaultMissesToFreeze = 2
)

// Beater is the one EdgeService call this loop makes.
type Beater interface {
	Heartbeat(context.Context, *connect.Request[edgev1.HeartbeatRequest]) (*connect.Response[edgev1.HeartbeatResponse], error)
}

// Freezer is what losing contact freezes: the device access lane, which stops
// admitting side effects until contact returns.
type Freezer interface {
	Freeze(ctx context.Context) error
	Unfreeze(ctx context.Context)
}

// HeartbeatConfig declares the loop. Construct with keyed fields.
type HeartbeatConfig struct {
	Client Beater
	Lane   Freezer
	// AdoptServerTime refreshes the clock used to sign later calls. Nil
	// discards the time returned by central.
	AdoptServerTime func(context.Context, time.Time)
	// AgentVersion is what the binary reports; central records it.
	AgentVersion string
	// Interval spaces attempts and bounds each one. Zero means 30s.
	Interval time.Duration
	// MissesBeforeFreeze is how many consecutive failed attempts freeze the
	// lane. Zero means two.
	MissesBeforeFreeze int
	// Wait blocks for d or until ctx ends, reporting whether the interval
	// elapsed. Nil means a timer; a test substitutes it.
	Wait   func(ctx context.Context, d time.Duration) bool
	Logger *slog.Logger
}

// RunHeartbeat keeps contact with central and freezes the lane when it is
// lost, until ctx ends.
//
// A miss is a failed attempt, not an elapsed interval, and each attempt is
// bounded by the interval. Both halves of that are decisions:
//
// Counting consecutive failed attempts rather than time since the last
// success is what makes a suspended process safe. A laptop asleep for an hour
// wakes with an hour since its last success and nothing wrong; a loop
// measuring elapsed time freezes every device on resume and unfreezes them a
// moment later, which is a fleet-wide outage caused by a lid. Counting
// attempts, the first one after resume succeeds and nothing happens.
//
// Bounding each attempt is what keeps the counter alive. A call that hangs
// forever is contact lost by any useful definition, but it is not a failure —
// it never returns to be counted — so a loop without a deadline stops
// counting exactly when it matters most, and the lane stays unfrozen because
// nothing told it otherwise. The deadline is the interval, so an attempt that
// has not finished by the time the next one is due is a miss.
//
// The lane is unfrozen on the next success, not on some later confirmation:
// central answering is the whole of what the freeze was waiting for.
func RunHeartbeat(ctx context.Context, cfg HeartbeatConfig) error {
	if cfg.Client == nil || cfg.Lane == nil {
		// Both, because the lane is only reached two missed heartbeats into
		// a contact outage. Left to fail there, a missing one is a panic in
		// this goroutine at the least recoverable moment the agent has.
		return errs.New().Code(ErrCodeHeartbeat).Msg("heartbeat needs an EdgeService client and a lane to freeze")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	misses := cfg.MissesBeforeFreeze
	if misses <= 0 {
		misses = defaultMissesToFreeze
	}
	wait := cfg.Wait
	if wait == nil {
		wait = waitFor
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	consecutive := 0
	frozen := false
	for {
		started := time.Now()
		if err := beat(ctx, cfg, interval); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			consecutive++
			if consecutive >= misses && !frozen {
				// Retried while Freeze reports an error, because that error
				// is an undelivered audit record and the fence is not
				// established until its records land. Retrying costs no
				// duplicates: recordFrozen skips a device whose record
				// already went out and marks one only after its delivery
				// succeeded, so a second attempt re-emits for exactly the
				// devices that failed.
				//
				// The cause of the missed heartbeat is what an operator
				// needs first: a central that refuses this agent and a
				// central that cannot be reached both freeze the lane here
				// and have opposite remedies. Without it the record says
				// only that two attempts failed.
				attrs := []any{
					slog.String("otel.event.name", "flowseer.edge.contact.lost"),
					slog.Int("flowseer.edge.missed_heartbeats", consecutive),
					slog.String("error.type", errorType(err)),
				}
				// Bounded by the interval: Freeze holds until its audit
				// record is durable, and a central that stops answering
				// rather than refusing would otherwise stall this loop for
				// as long as the parent context allows.
				freezeCtx, cancel := context.WithTimeout(ctx, interval)
				freezeErr := cfg.Lane.Freeze(freezeCtx)
				cancel()
				if freezeErr != nil {
					log.WarnContext(ctx, "lane frozen with records undelivered",
						append(attrs, slog.String("flowseer.edge.freeze.error_type", errorType(freezeErr)))...)
				} else {
					log.WarnContext(ctx, "contact with central lost; lane frozen", attrs...)
					frozen = true
				}
			}
		} else {
			if frozen {
				cfg.Lane.Unfreeze(ctx)
				frozen = false
				log.InfoContext(ctx, "contact with central restored; lane unfrozen",
					slog.String("otel.event.name", "flowseer.edge.contact.restored"))
			}
			consecutive = 0
		}

		// Measured from the attempt's start, so the interval spaces attempts
		// rather than spacing the end of one from the start of the next. An
		// attempt runs under the interval as its deadline, so waiting a full
		// interval after it returned would put a stalled central's attempts
		// two intervals apart and the freeze at three rather than two.
		if !wait(ctx, interval-time.Since(started)) {
			return nil
		}
	}
}

// beat makes one attempt under its own deadline.
func beat(ctx context.Context, cfg HeartbeatConfig, interval time.Duration) error {
	attempt, cancel := context.WithTimeout(ctx, interval)
	defer cancel()

	request := &edgev1.HeartbeatRequest{}
	request.SetAgentVersion(cfg.AgentVersion)
	response, err := cfg.Client.Heartbeat(attempt, connect.NewRequest(request))
	if err != nil {
		return err
	}
	if cfg.AdoptServerTime != nil && response.Msg.GetServerTime() != nil {
		cfg.AdoptServerTime(ctx, response.Msg.GetServerTime().AsTime())
	}
	return nil
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
