package capture

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/protobuf/proto"

	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/capture"
)

// runSession drives one active capture session end-to-end: opening the upload
// stream, providing the initial assertion and chunk, running the capture
// engine, sending periodic re-assertions, and forwarding batches until a budget
// stops it, an operator cancels it, or inactivity times out.
func (h *Handler) runSession(sessionCtx context.Context, engineCtx context.Context, cfg *modelcapturev1.CaptureSessionConfig, sess *activeSession) {
	sessionID := cfg.GetRef().GetCaptureSession().GetId()
	defer h.removeSession(sessionID)

	log := h.logger.With(
		slog.String("flowseer.capture.session.id", sessionID),
	)

	stream := h.client.UploadCapture(sessionCtx)

	// The stream must open with a SignedEdgeAssertion.
	openingAssertion, err := h.signAssertion(sessionCtx)
	if err != nil {
		log.ErrorContext(sessionCtx, "sign opening assertion failed",
			slog.String("error.type", errorType(err)))
		_, _ = stream.CloseAndReceive()
		return
	}

	if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
		Assertion: openingAssertion,
	}.Build()); err != nil {
		log.ErrorContext(sessionCtx, "send opening assertion failed",
			slog.String("error.type", errorType(err)))
		_, _ = stream.CloseAndReceive()
		return
	}

	// Send initial chunk to associate the stream with sessionID in central.
	// This transitions central to RUNNING, claims the upload, and ensures
	// central can attribute an early failure to this session.
	initChunk := captureedgev1.UploadCaptureRequest_builder{
		Chunk: modelcapturev1.CapturePacketChunk_builder{
			Session:       cfg.GetRef(),
			FirstSequence: proto.Uint64(0),
			Final:         proto.Bool(false),
		}.Build(),
	}.Build()

	if err := stream.Send(initChunk); err != nil {
		log.ErrorContext(sessionCtx, "send initial chunk failed",
			slog.String("error.type", errorType(err)))
		_, _ = stream.CloseAndReceive()
		return
	}

	// Prepare capture engine.
	engineCfg := capture.Config{
		Source: cfg.GetSource(),
		Filter: cfg.GetFilter(),
		Budget: cfg.GetBudget(),
	}

	var engine *capture.Engine
	if h.openSource != nil {
		src, reportsDrops, err := h.openSource(engineCtx, engineCfg)
		if err != nil {
			log.ErrorContext(sessionCtx, "open custom packet source failed",
				slog.String("error.type", errorType(err)))
			_, _ = stream.CloseAndReceive()
			return
		}
		engine = capture.NewWithSource(src, cfg.GetBudget(), reportsDrops)
	} else {
		var err error
		engine, err = capture.New(engineCfg)
		if err != nil {
			log.ErrorContext(sessionCtx, "create capture engine failed",
				slog.String("error.type", errorType(err)))
			_, _ = stream.CloseAndReceive()
			return
		}
	}

	pump, err := engine.Run(engineCtx)
	if err != nil {
		log.ErrorContext(sessionCtx, "start capture engine failed",
			slog.String("error.type", errorType(err)))
		_, _ = stream.CloseAndReceive()
		return
	}

	reassertTicker := time.NewTicker(h.reassertInterval)
	defer reassertTicker.Stop()

	inactivityTimer := time.NewTimer(h.inactivityTimeout)
	defer inactivityTimer.Stop()

	for {
		select {
		case <-reassertTicker.C:
			reassert, err := h.signAssertion(sessionCtx)
			if err != nil {
				log.WarnContext(sessionCtx, "sign mid-stream assertion failed",
					slog.String("error.type", errorType(err)))
				continue
			}
			if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
				Assertion: reassert,
			}.Build()); err != nil {
				log.WarnContext(sessionCtx, "send mid-stream assertion failed",
					slog.String("error.type", errorType(err)))
				pump.SignalStop()
				pump.Cancel()
				return
			}

		case <-inactivityTimer.C:
			if sess.stopping.Load() {
				// Operator stop is currently flushing the final batch.
				continue
			}
			// Inactivity timeout: close upload stream without sending final chunk
			// so central's failStream transitions the session to FAILED.
			log.WarnContext(sessionCtx, "capture inactivity timeout elapsed; aborting upload stream without final chunk")
			pump.SignalStop()
			pump.Cancel()
			_, _ = stream.CloseAndReceive()
			return

		case batch, ok := <-pump.Data():
			if !ok {
				// Pump closed without final batch. Terminate upload without final chunk.
				_, _ = stream.CloseAndReceive()
				return
			}

			if len(batch.Records) > 0 {
				if !inactivityTimer.Stop() {
					select {
					case <-inactivityTimer.C:
					default:
					}
				}
				inactivityTimer.Reset(h.inactivityTimeout)
			}

			// Telemetry privacy: log only metadata (sequence, counts), never payloads.
			log.DebugContext(sessionCtx, "uploading capture chunk",
				slog.Uint64("flowseer.capture.chunk.first_sequence", batch.FirstSequence),
				slog.Int("flowseer.capture.chunk.packet_count", len(batch.Records)),
				slog.Bool("flowseer.capture.chunk.final", batch.Final))

			chunkMsg := captureedgev1.UploadCaptureRequest_builder{
				Chunk: modelcapturev1.CapturePacketChunk_builder{
					Session:       cfg.GetRef(),
					FirstSequence: proto.Uint64(batch.FirstSequence),
					Packets:       batch.Records,
					Counters:      batch.Counters,
					Final:         proto.Bool(batch.Final),
				}.Build(),
			}.Build()

			if err := stream.Send(chunkMsg); err != nil {
				log.WarnContext(sessionCtx, "upload chunk failed",
					slog.String("error.type", errorType(err)))
				pump.SignalStop()
				pump.Cancel()
				return
			}

			if batch.Final {
				if _, err := stream.CloseAndReceive(); err != nil {
					log.WarnContext(sessionCtx, "close upload stream failed",
						slog.String("error.type", errorType(err)))
				}
				return
			}

		case <-sessionCtx.Done():
			pump.SignalStop()
			pump.Cancel()
			_, _ = stream.CloseAndReceive()
			return
		}
	}
}

func errorType(err error) string {
	if code, ok := errs.CodeOf(err); ok {
		return string(code)
	}
	return "unknown"
}
