package authz

import (
	"context"
	"net/http"
	"testing"

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
}

func (c *receiveTestConn) Spec() connect.Spec {
	return connect.Spec{StreamType: connect.StreamTypeServer}
}

func (c *receiveTestConn) Peer() connect.Peer {
	return connect.Peer{}
}

func (c *receiveTestConn) Receive(msg any) error {
	c.receives++
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
