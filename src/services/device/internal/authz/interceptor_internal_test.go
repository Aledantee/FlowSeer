package authz

import (
	"context"
	"net/http"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	authzv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/authz/v1"
	edgemodelv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
)

type receiveTestChecker struct{}

func (receiveTestChecker) Check(context.Context, Query) (bool, error) {
	return true, nil
}

func (receiveTestChecker) BatchCheck(_ context.Context, queries []Query) ([]bool, error) {
	answers := make([]bool, len(queries))
	if len(answers) > 0 {
		answers[0] = false
	}
	for i := 1; i < len(answers); i++ {
		answers[i] = true
	}
	return answers, nil
}

type receiveTestConn struct {
	msg      proto.Message
	receives int
	received chan struct{}
}

func (c *receiveTestConn) Spec() connect.Spec {
	return connect.Spec{StreamType: connect.StreamTypeServer}
}

func (c *receiveTestConn) Peer() connect.Peer {
	return connect.Peer{}
}

func (c *receiveTestConn) Receive(msg any) error {
	c.receives++
	if c.received != nil {
		c.received <- struct{}{}
	}
	proto.Merge(msg.(proto.Message), c.msg)
	return nil
}

func (*receiveTestConn) RequestHeader() http.Header {
	return make(http.Header)
}

func (*receiveTestConn) Send(any) error {
	return nil
}

func (*receiveTestConn) ResponseHeader() http.Header {
	return make(http.Header)
}

func (*receiveTestConn) ResponseTrailer() http.Header {
	return make(http.Header)
}

func TestAuthzStreamingConnReturnsTheFirstDeniedCheckOnEveryReceive(t *testing.T) {
	request := apiedgev1.GetEdgeRequest_builder{
		Edge: edgemodelv1.EdgeGlobalRef_builder{
			Edge: edgemodelv1.EdgeLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-0000000000e1")}.Build(),
		}.Build(),
	}.Build()
	rule := authzv1.Rule_builder{
		ObjectType:   proto.String("edge"),
		Relation:     proto.String("view"),
		ObjectIdPath: proto.String("edge.edge.id"),
	}.Build()
	conn := &receiveTestConn{msg: request}
	wrapped := &authzStreamingConn{
		StreamingHandlerConn: conn,
		ctx:                  context.Background(),
		interceptor:          NewInterceptor(receiveTestChecker{}),
		rule:                 rule,
		principal:            authn.Principal{ID: "operator"},
		admittedTenant:       "0192e6a0-0000-7000-8000-0000000000a1",
	}

	first := &apiedgev1.GetEdgeRequest{}
	firstErr := wrapped.Receive(first)
	second := &apiedgev1.GetEdgeRequest{}
	secondErr := wrapped.Receive(second)
	if connect.CodeOf(firstErr) != connect.CodePermissionDenied {
		t.Fatalf("first receive code = %v, want %v", connect.CodeOf(firstErr), connect.CodePermissionDenied)
	}
	if connect.CodeOf(secondErr) != connect.CodePermissionDenied {
		t.Fatalf("second receive code = %v, want %v", connect.CodeOf(secondErr), connect.CodePermissionDenied)
	}
	if conn.receives != 1 {
		t.Fatalf("underlying receives = %d, want 1", conn.receives)
	}
}

type blockingReceiveChecker struct {
	started chan struct{}
	block   chan struct{}
}

func (c *blockingReceiveChecker) Check(context.Context, Query) (bool, error) {
	return true, nil
}

func (c *blockingReceiveChecker) BatchCheck(_ context.Context, queries []Query) ([]bool, error) {
	close(c.started)
	<-c.block
	answers := make([]bool, len(queries))
	for i := range answers {
		answers[i] = true
	}
	return answers, nil
}

func TestAuthzStreamingConnWaitsForTheFirstCheckBeforeAnotherReceive(t *testing.T) {
	request := apiedgev1.GetEdgeRequest_builder{
		Edge: edgemodelv1.EdgeGlobalRef_builder{
			Edge: edgemodelv1.EdgeLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-0000000000e1")}.Build(),
		}.Build(),
	}.Build()
	rule := authzv1.Rule_builder{
		ObjectType:   proto.String("edge"),
		Relation:     proto.String("view"),
		ObjectIdPath: proto.String("edge.edge.id"),
	}.Build()
	checker := &blockingReceiveChecker{started: make(chan struct{}), block: make(chan struct{})}
	conn := &receiveTestConn{msg: request, received: make(chan struct{}, 2)}
	wrapped := &authzStreamingConn{
		StreamingHandlerConn: conn,
		ctx:                  context.Background(),
		interceptor:          NewInterceptor(checker),
		rule:                 rule,
		principal:            authn.Principal{ID: "operator"},
		admittedTenant:       "0192e6a0-0000-7000-8000-0000000000a1",
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- wrapped.Receive(&apiedgev1.GetEdgeRequest{})
	}()
	<-conn.received
	<-checker.started

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- wrapped.Receive(&apiedgev1.GetEdgeRequest{})
	}()
	select {
	case <-secondDone:
		t.Fatal("second receive completed while the first authorization check was blocked")
	case <-time.After(100 * time.Millisecond):
	}

	close(checker.block)
	if err := <-firstDone; err != nil {
		t.Fatalf("first receive: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second receive: %v", err)
	}
	if conn.receives != 2 {
		t.Fatalf("underlying receives = %d, want 2", conn.receives)
	}
}
