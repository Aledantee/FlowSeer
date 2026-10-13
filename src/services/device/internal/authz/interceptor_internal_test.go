package authz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	authzv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/authz/v1"
	edgemodelv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
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

// batchRecorder answers every query from deny and records each BatchCheck call.
type batchRecorder struct {
	batches [][]Query
	deny    func(Query) bool
	err     error
}

func (*batchRecorder) Check(context.Context, Query) (bool, error) { return true, nil }

func (b *batchRecorder) BatchCheck(_ context.Context, queries []Query) ([]bool, error) {
	b.batches = append(b.batches, queries)
	if b.err != nil {
		return nil, b.err
	}
	answers := make([]bool, len(queries))
	for i, q := range queries {
		answers[i] = b.deny == nil || !b.deny(q)
	}
	return answers, nil
}

func tenantIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("0192e6a0-0000-7000-8000-%012d", i+1)
	}
	return ids
}

func operatorContext() context.Context {
	return authn.NewContext(context.Background(), authn.Principal{ID: "operator"})
}

func TestATwoTenantHeaderIsAdmittedInOneBatchAndLeavesTheSetOnTheContext(t *testing.T) {
	checker := &batchRecorder{}
	ids := tenantIDs(2)

	ctx, err := NewInterceptor(checker).admitSet(operatorContext(), []string{ids[0] + "," + ids[1]}, "viewer")
	if err != nil {
		t.Fatalf("admitSet: %v", err)
	}

	if len(checker.batches) != 1 || len(checker.batches[0]) != 4 {
		t.Fatalf("batches = %v, want one batch of 4 queries", checker.batches)
	}
	want := []struct{ object, relation string }{
		{"tenant:" + ids[0], "member"},
		{"tenant:" + ids[0], "viewer"},
		{"tenant:" + ids[1], "member"},
		{"tenant:" + ids[1], "viewer"},
	}
	for i, q := range checker.batches[0] {
		if q.Object != want[i].object || q.Relation != want[i].relation || q.User != "user:operator" {
			t.Errorf("query %d = %+v, want %s#%s for user:operator", i, q, want[i].object, want[i].relation)
		}
	}
	set, err := tenant.SetFrom(ctx)
	if err != nil || !slices.Equal(set, ids) {
		t.Errorf("tenant set = %v, %v, want %v", set, err, ids)
	}
	if _, err := tenant.FromContext(ctx); err == nil {
		t.Error("the context carries a single tenant beside the set")
	}
}

func TestOneDeniedTenantRefusesTheWholeCall(t *testing.T) {
	ids := tenantIDs(3)
	for name, deny := range map[string]func(Query) bool{
		"membership of the second tenant denied": func(q Query) bool {
			return q.Object == "tenant:"+ids[1] && q.Relation == "member"
		},
		"the relation on the third tenant denied": func(q Query) bool {
			return q.Object == "tenant:"+ids[2] && q.Relation == "viewer"
		},
	} {
		t.Run(name, func(t *testing.T) {
			checker := &batchRecorder{deny: deny}

			_, err := NewInterceptor(checker).admitSet(operatorContext(), []string{strings.Join(ids, ",")}, "viewer")

			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Errorf("code = %v (err %v), want PermissionDenied", connect.CodeOf(err), err)
			}
		})
	}
}

func TestTwentyFiveTenantsAreOneBatchAndTwentySixAreRefusedBeforeAnyCheck(t *testing.T) {
	checker := &batchRecorder{}
	interceptor := NewInterceptor(checker)

	if _, err := interceptor.admitSet(operatorContext(), []string{strings.Join(tenantIDs(25), ",")}, "viewer"); err != nil {
		t.Fatalf("25 tenants: %v", err)
	}
	if len(checker.batches) != 1 || len(checker.batches[0]) != maxChecksPerBatch {
		t.Fatalf("batches for 25 tenants = %d of sizes %v, want one batch of %d", len(checker.batches), checker.batches, maxChecksPerBatch)
	}

	checker.batches = nil
	_, err := interceptor.admitSet(operatorContext(), []string{strings.Join(tenantIDs(26), ",")}, "viewer")
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("26 tenants gave code %v (err %v), want InvalidArgument", connect.CodeOf(err), err)
	}
	if len(checker.batches) != 0 {
		t.Errorf("a refused list reached the checker: %v", checker.batches)
	}
}

func TestATenantListRefusesWhatItCannotAdmit(t *testing.T) {
	ids := tenantIDs(2)
	tests := []struct {
		name     string
		values   []string
		wantCode errs.Code
	}{
		{"no header", nil, ErrCodeNoTenant},
		{"an empty header", []string{""}, ErrCodeTenantList},
		{"a trailing comma", []string{ids[0] + ","}, ErrCodeTenantList},
		{"an empty item", []string{ids[0] + ",," + ids[1]}, ErrCodeTenantList},
		{"a duplicate in one line", []string{ids[0] + "," + ids[0]}, ErrCodeTenantList},
		{"a duplicate across lines", []string{ids[0], ids[0]}, ErrCodeTenantList},
		{"an invalid tenant", []string{ids[0] + ",Acme"}, ErrCodeNoTenant},
		{"a wildcard", []string{"*"}, ErrCodeNoTenant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := &batchRecorder{}

			_, err := NewInterceptor(checker).admitSet(operatorContext(), tt.values, "viewer")

			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("code = %v (err %v), want InvalidArgument", connect.CodeOf(err), err)
			}
			if code, _ := errs.CodeOf(err); code != tt.wantCode {
				t.Errorf("error code = %v, want %v (err %v)", code, tt.wantCode, err)
			}
			if len(checker.batches) != 0 {
				t.Errorf("a refused list reached the checker: %v", checker.batches)
			}
		})
	}
}

func TestATenantListTakesSeveralLinesAndIgnoresSpacesAroundItems(t *testing.T) {
	ids := tenantIDs(3)

	got, err := tenantList([]string{ids[0] + " , " + ids[1], ids[2]})

	if err != nil || !slices.Equal(got, ids) {
		t.Errorf("tenantList = %v, %v, want %v", got, err, ids)
	}
}

func TestACheckerFailureOnATenantSetIsUnavailable(t *testing.T) {
	checker := &batchRecorder{err: errors.New("engine down")}

	_, err := NewInterceptor(checker).admitSet(operatorContext(), []string{tenantIDs(1)[0]}, "viewer")

	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("code = %v (err %v), want Unavailable", connect.CodeOf(err), err)
	}
}

func TestASingleTenantMethodRefusesASecondHeaderLine(t *testing.T) {
	ids := tenantIDs(2)

	if got, err := singleTenantHeader([]string{ids[0]}); err != nil || got != ids[0] {
		t.Errorf("one line: %q, %v, want %q", got, err, ids[0])
	}
	if got, err := singleTenantHeader(nil); err != nil || got != "" {
		t.Errorf("no line: %q, %v, want an empty tenant for Admit to refuse", got, err)
	}
	_, err := singleTenantHeader([]string{ids[0], ids[1]})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("two lines gave code %v (err %v), want InvalidArgument", connect.CodeOf(err), err)
	}
}
