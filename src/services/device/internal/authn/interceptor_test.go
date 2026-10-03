package authn_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
)

type dummyStreamingConn struct {
	header http.Header
}

func (c *dummyStreamingConn) RequestHeader() http.Header   { return c.header }
func (c *dummyStreamingConn) ResponseHeader() http.Header  { return make(http.Header) }
func (c *dummyStreamingConn) ResponseTrailer() http.Header { return make(http.Header) }
func (c *dummyStreamingConn) Send(any) error               { return nil }
func (c *dummyStreamingConn) Receive(any) error            { return nil }
func (c *dummyStreamingConn) Spec() connect.Spec           { return connect.Spec{} }
func (c *dummyStreamingConn) Peer() connect.Peer           { return connect.Peer{} }

func TestInterceptorUnaryCases(t *testing.T) {
	srv := newTestOidcServer(t)
	now := time.Now().Truncate(time.Second)

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.server.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	interceptor := authn.NewInterceptor(verifier)

	claims := map[string]any{
		"iss": srv.server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	validToken := signRSAToken(t, srv.rsaKey, srv.rsaKID, claims)

	// 1. No header -> Unauthenticated "authentication required"
	unaryHandler := interceptor.WrapUnary(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	})

	noHeaderReq := connect.NewRequest(&emptypb.Empty{})
	_, err = unaryHandler(context.Background(), noHeaderReq)
	if err == nil {
		t.Fatal("expected error with no header")
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("got code %v, want Unauthenticated", connect.CodeOf(err))
	}
	if err.Error() != "unauthenticated: authentication required" {
		t.Fatalf("got error message %q, want 'unauthenticated: authentication required'", err.Error())
	}

	// 2. Lowercase "bearer" -> succeeds, principal in context
	var receivedPrincipal authn.Principal
	var handlerCalled bool
	recordingHandler := interceptor.WrapUnary(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		p, ok := authn.FromContext(ctx)
		if !ok {
			t.Fatal("expected principal in context")
		}
		receivedPrincipal = p
		handlerCalled = true
		return connect.NewResponse(&emptypb.Empty{}), nil
	})

	lowerReq := connect.NewRequest(&emptypb.Empty{})
	lowerReq.Header().Set("Authorization", "bearer "+validToken)
	_, err = recordingHandler(context.Background(), lowerReq)
	if err != nil {
		t.Fatalf("expected success with lowercase bearer, got %v", err)
	}
	if !handlerCalled || receivedPrincipal.Subject != "u1" {
		t.Fatalf("handler not called or wrong principal: %+v", receivedPrincipal)
	}

	// 3. Malformed scheme Basic
	basicReq := connect.NewRequest(&emptypb.Empty{})
	basicReq.Header().Set("Authorization", "Basic QWxhZGRpbjpvcGVuIHNlc2FtZQ==")
	_, err = unaryHandler(context.Background(), basicReq)
	if err == nil {
		t.Fatal("expected error with Basic auth")
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("got code %v, want Unauthenticated", connect.CodeOf(err))
	}
}

func TestInterceptorStreamingHandler(t *testing.T) {
	srv := newTestOidcServer(t)
	now := time.Now().Truncate(time.Second)

	verifier, err := authn.NewVerifier(authn.Options{
		Issuers: []authn.IssuerConfig{
			{
				Issuer:   srv.server.URL,
				Audience: "flowseer-device",
			},
		},
		Client: srv.server.Client(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	interceptor := authn.NewInterceptor(verifier)

	claims := map[string]any{
		"iss": srv.server.URL,
		"aud": "flowseer-device",
		"sub": "u1",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	validToken := signRSAToken(t, srv.rsaKey, srv.rsaKID, claims)

	// 1. Streaming call without header -> Unauthenticated
	streamHandler := interceptor.WrapStreamingHandler(func(_ context.Context, _ connect.StreamingHandlerConn) error {
		return nil
	})

	noHeaderConn := &dummyStreamingConn{header: make(http.Header)}
	err = streamHandler(context.Background(), noHeaderConn)
	if err == nil {
		t.Fatal("expected streaming error with no header")
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("got code %v, want Unauthenticated", connect.CodeOf(err))
	}

	// 2. Streaming call with Bearer -> succeeds, principal in context
	var streamPrincipal authn.Principal
	var streamCalled bool
	recordingStream := interceptor.WrapStreamingHandler(func(ctx context.Context, _ connect.StreamingHandlerConn) error {
		p, ok := authn.FromContext(ctx)
		if !ok {
			t.Fatal("expected principal in streaming context")
		}
		streamPrincipal = p
		streamCalled = true
		return nil
	})

	bearerConn := &dummyStreamingConn{
		header: http.Header{
			"Authorization": []string{"Bearer " + validToken},
		},
	}
	err = recordingStream(context.Background(), bearerConn)
	if err != nil {
		t.Fatalf("expected stream success with Bearer, got: %v", err)
	}
	if !streamCalled || streamPrincipal.Subject != "u1" {
		t.Fatalf("stream handler not called or wrong principal: %+v", streamPrincipal)
	}
}
