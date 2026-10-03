package authn

import (
	"context"
	"strings"

	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
)

// Interceptor enforces operator authentication on Connect RPC handlers.
type Interceptor struct {
	verifier *Verifier
}

// NewInterceptor constructs an authentication interceptor wrapping the verifier.
func NewInterceptor(verifier *Verifier) *Interceptor {
	return &Interceptor{verifier: verifier}
}

func (i *Interceptor) authenticate(ctx context.Context, authHeader string) (context.Context, error) {
	if authHeader == "" {
		return nil, connecterr.WrapAs(
			connect.CodeUnauthenticated,
			"authentication required",
			errs.New().Code(ErrCodeTokenInvalid).Msg("missing authorization header"),
		)
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || strings.TrimSpace(parts[1]) == "" {
		return nil, connecterr.WrapAs(
			connect.CodeUnauthenticated,
			"authentication required",
			errs.New().Code(ErrCodeTokenInvalid).Msg("malformed authorization header"),
		)
	}

	rawToken := strings.TrimSpace(parts[1])
	principal, err := i.verifier.Verify(ctx, rawToken)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if code, ok := errs.CodeOf(err); ok && code == ErrCodeUnavailable {
			return nil, connecterr.WrapAs(connect.CodeUnavailable, "authentication unavailable", err)
		}
		return nil, connecterr.WrapAs(connect.CodeUnauthenticated, "authentication required", err)
	}

	return NewContext(ctx, principal), nil
}

// WrapUnary wraps a unary handler to verify the Bearer token and inject the Principal.
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		authCtx, err := i.authenticate(ctx, req.Header().Get("Authorization"))
		if err != nil {
			return nil, err
		}
		return next(authCtx, req)
	}
}

// WrapStreamingHandler wraps a streaming handler to verify the Bearer token and inject the Principal.
func (i *Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		authCtx, err := i.authenticate(ctx, conn.RequestHeader().Get("Authorization"))
		if err != nil {
			return err
		}
		return next(authCtx, conn)
	}
}

// WrapStreamingClient passes through streaming client calls.
func (i *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}
