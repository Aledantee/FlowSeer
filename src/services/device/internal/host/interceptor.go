package host

import (
	"context"
	"errors"
	"log/slog"

	connect "connectrpc.com/connect"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// LoggingInterceptor records every failure a handler returns, with the cause
// behind the sentence the caller was given.
//
// The unwrap is the whole point. A handler's error renders its client-facing
// sentence from Error(), so a log line formatted with %v or %s or handed the
// Connect error directly records the sanitized text and silently drops
// everything behind it: "the device's record cannot be reached right now" with
// no bucket, no transport, no cause. The chain is still there and reachable,
// so this pulls the failure out of it and logs that, whose LogValue carries the
// code, the merged attributes, and the captured stacks.
//
// A line that looks right and records nothing is found during an incident,
// not before one.
func LoggingInterceptor(log *slog.Logger) connect.Interceptor {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return loggingInterceptor{log: log}
}

type loggingInterceptor struct {
	log *slog.Logger
}

func (i loggingInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		resp, err := next(ctx, req)
		i.record(ctx, req.Spec().Procedure, err)
		return resp, err
	}
}

func (i loggingInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		err := next(ctx, conn)
		i.record(ctx, conn.Spec().Procedure, err)
		return err
	}
}

// WrapStreamingClient passes through. This service is a server; a client
// interceptor here would log calls it does not own the outcome of.
func (i loggingInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// record logs one failed call. A successful one is not logged: metrics carry
// volume and traces carry timing, and a line per request buries the failures
// among them.
func (i loggingInterceptor) record(ctx context.Context, procedure string, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) {
		// The caller hung up or the stream closed. Nothing failed, and a
		// server-stream that an edge holds open across a restart would
		// otherwise log an error every time it reconnects.
		return
	}

	code := connect.CodeOf(err)
	i.log.Log(ctx, levelFor(code), "rpc call failed",
		slog.String(string(semconv.RPCSystemNameKey), "connectrpc"),
		slog.String(string(semconv.RPCMethodKey), procedure),
		slog.String(string(semconv.RPCResponseStatusCodeKey), code.String()),
		slog.String(string(semconv.ErrorTypeKey), errorType(err, code)),
		slog.Any("error", internalCause(err)),
	)
}

// internalCause is the failure the handler actually hit, not the sentence its
// caller was given.
//
// [errs.Error] renders its own message followed by its causes, so a chain read
// from the outside stops at the client-facing wrapper, whose Error() is the
// sanitized text by design. Reaching past it with [errors.As] gets the
// innermost failure that carries the chain, and its LogValue renders the whole
// tree. An error from outside this repository is logged as it is; there is
// nothing better to be had from it.
func internalCause(err error) any {
	var internal *errs.Error
	if errors.As(err, &internal) {
		return internal
	}
	return err
}

// errorType classifies the failure for filtering and grouping: the errs code
// where there is one, which is a bounded vocabulary the service declares, and
// otherwise the Connect code, which is bounded by the protocol.
func errorType(err error, code connect.Code) string {
	if errCode, ok := errs.CodeOf(err); ok {
		return errCode.String()
	}
	return code.String()
}

// levelFor grades a failure by who has to act on it.
//
// A refused request is an answer, not an incident: the caller named a device
// that does not exist or a firmware epoch that has moved, learned so, and can
// act. Those are DEBUG, because a line per refusal at a higher level buries
// the failures nobody chose. Unauthenticated, PermissionDenied and
// ResourceExhausted are WARN — each is a normal answer that is also worth
// noticing in aggregate, one for security and one for capacity. The rest mean
// the service failed the caller, and are ERROR at the boundary that owns the
// outcome.
func levelFor(code connect.Code) slog.Level {
	switch code {
	case connect.CodeInternal, connect.CodeUnknown, connect.CodeDataLoss, connect.CodeUnavailable:
		return slog.LevelError
	case connect.CodeUnauthenticated, connect.CodePermissionDenied, connect.CodeResourceExhausted:
		return slog.LevelWarn
	default:
		return slog.LevelDebug
	}
}
