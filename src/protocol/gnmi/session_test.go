package gnmi_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/protocol/gnmi"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

// fakeServer scripts the gNMI service for the unit suite.
type fakeServer struct {
	gpb.UnimplementedGNMIServer

	encodings []gpb.Encoding
	getResp   *gpb.GetResponse
	get       func(context.Context, *gpb.GetRequest) (*gpb.GetResponse, error)
	setResp   *gpb.SetResponse
	setErr    error
	subscribe func(gpb.GNMI_SubscribeServer) error
}

func (f *fakeServer) Capabilities(context.Context, *gpb.CapabilityRequest) (*gpb.CapabilityResponse, error) {
	return &gpb.CapabilityResponse{
		SupportedModels:    []*gpb.ModelData{{Name: "openconfig-system", Organization: "OpenConfig", Version: "2.3.0"}},
		SupportedEncodings: f.encodings,
		GNMIVersion:        "0.10.0",
	}, nil
}

func (f *fakeServer) Get(ctx context.Context, req *gpb.GetRequest) (*gpb.GetResponse, error) {
	if f.get != nil {
		return f.get(ctx, req)
	}
	return f.getResp, nil
}

func (f *fakeServer) Set(context.Context, *gpb.SetRequest) (*gpb.SetResponse, error) {
	return f.setResp, f.setErr
}

func (f *fakeServer) Subscribe(srv gpb.GNMI_SubscribeServer) error {
	if f.subscribe == nil {
		return status.Error(14, "no subscribe script")
	}
	return f.subscribe(srv)
}

// dialFake wires a Session against an in-process server.
func dialFake(t *testing.T, f *fakeServer) *gnmi.Session {
	t.Helper()
	return dialFakeWithOptions(t, f, gnmi.Options{Plaintext: true, Username: "admin", Password: secret.NewString("secret")})
}

func dialFakeWithOptions(t *testing.T, f *fakeServer, opts gnmi.Options) *gnmi.Session {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	gpb.RegisterGNMIServer(grpcSrv, f)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	cc, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	s, err := gnmi.NewSession(context.Background(), cc, opts)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func ifacePath() yang.Path {
	return yang.Path{Segments: []yang.Segment{
		{Module: "openconfig-interfaces", Name: "interfaces"},
		{Name: "interface", Keys: []yang.KeyValue{{Name: "name", Value: "eth0"}}},
		{Name: "state"},
		{Name: "oper-status"},
	}}
}

// Covers conformance matrix row: gn-proto-only-encoding
func TestProtoOnlyPeerRoundTripsGet(t *testing.T) {
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_PROTO},
		getResp: &gpb.GetResponse{Notification: []*gpb.Notification{{
			Timestamp: 42,
			Update: []*gpb.Update{{
				Path: &gpb.Path{Elem: []*gpb.PathElem{
					{Name: "interfaces"},
					{Name: "interface", Key: map[string]string{"name": "eth0"}},
					{Name: "state"},
					{Name: "oper-status"},
				}},
				Val: &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: "UP"}},
			}},
		}}},
	}
	s := dialFake(t, f)
	if got := s.Encoding(); got != "PROTO" {
		t.Errorf("negotiated encoding = %q, want PROTO", got)
	}

	updates, err := s.Get(context.Background(), ifacePath())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(updates))
	}
	u := updates[0]
	if u.Value == nil || u.Value.String != "UP" {
		t.Errorf("value = %+v, want UP", u.Value)
	}
	if got := u.Path.String(); got != "/interfaces/interface[name=eth0]/state/oper-status" {
		t.Errorf("path = %q", got)
	}
}

func TestEncodingNegotiationPrefersJSONIETF(t *testing.T) {
	f := &fakeServer{encodings: []gpb.Encoding{gpb.Encoding_PROTO, gpb.Encoding_JSON_IETF}}
	s := dialFake(t, f)
	if got := s.Encoding(); got != "JSON_IETF" {
		t.Errorf("negotiated encoding = %q, want JSON_IETF", got)
	}
	caps := s.Capabilities()
	if len(caps.Models) != 1 || caps.Models[0].Version != "2.3.0" {
		t.Errorf("models = %+v", caps.Models)
	}
}

// Covers conformance matrix row: gn-stream-termination-latch
func TestSubscribeStreamOrderAndSync(t *testing.T) {
	update := func(name, val string) *gpb.SubscribeResponse {
		return &gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_Update{Update: &gpb.Notification{
			Update: []*gpb.Update{{
				Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "interface", Key: map[string]string{"name": name}}}},
				Val:  &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: val}},
			}},
		}}}
	}
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF},
		subscribe: func(srv gpb.GNMI_SubscribeServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			_ = srv.Send(update("eth0", "UP"))
			_ = srv.Send(update("eth1", "DOWN"))
			_ = srv.Send(&gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_SyncResponse{SyncResponse: true}})
			_ = srv.Send(update("eth1", "UP"))
			return status.Error(14, "device rebooted")
		},
	}
	s := dialFake(t, f)

	stream, err := s.Subscribe(context.Background(), gnmi.SubscribeOptions{
		Mode:  gnmi.ModeStream,
		Paths: []yang.Path{{Segments: []yang.Segment{{Name: "interfaces"}}}},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var got []string
	for ev := range stream.Iter() {
		if ev.Sync {
			got = append(got, "SYNC")
			continue
		}
		for _, u := range ev.Updates {
			got = append(got, u.Path.String()+"="+u.Value.String)
		}
	}
	want := []string{
		"/interface[name=eth0]=UP",
		"/interface[name=eth1]=DOWN",
		"SYNC",
		"/interface[name=eth1]=UP",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
	// The abrupt server termination latched as terminal.
	if stream.Err() == nil {
		t.Error("server termination did not latch Err")
	}
}

func TestSubscribeOnceEndsCleanly(t *testing.T) {
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF},
		subscribe: func(srv gpb.GNMI_SubscribeServer) error {
			if _, err := srv.Recv(); err != nil {
				return err
			}
			_ = srv.Send(&gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_SyncResponse{SyncResponse: true}})
			return nil
		},
	}
	s := dialFake(t, f)
	stream, err := s.Subscribe(context.Background(), gnmi.SubscribeOptions{
		Mode:  gnmi.ModeOnce,
		Paths: []yang.Path{{Segments: []yang.Segment{{Name: "interfaces"}}}},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	sawSync := false
	for ev := range stream.Iter() {
		if ev.Sync {
			sawSync = true
		}
	}
	if !sawSync {
		t.Error("ONCE stream ended without sync_response")
	}
	if err := stream.Err(); err != nil {
		t.Errorf("clean ONCE termination latched %v", err)
	}
}

// Covers conformance matrix row: gn-per-path-set-error
func TestSetPartialFailureNamesPath(t *testing.T) {
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF},
		setResp: &gpb.SetResponse{Response: []*gpb.UpdateResult{{
			Path:    &gpb.Path{Elem: []*gpb.PathElem{{Name: "system"}, {Name: "config"}, {Name: "hostname"}}},
			Op:      gpb.UpdateResult_UPDATE,
			Message: &gpb.Error{Message: "hostname is read-only on this platform"}, //nolint:staticcheck // deprecated upstream; exercised deliberately
		}}},
	}
	s := dialFake(t, f)

	err := s.Set(context.Background(), gnmi.SetRequest{
		Updates: []gnmi.PathValue{{
			Path: yang.Path{Segments: []yang.Segment{{Name: "system"}, {Name: "config"}, {Name: "hostname"}}},
			JSON: []byte(`"edge-1"`),
		}},
	})
	if err == nil {
		t.Fatal("Set succeeded despite per-path failure")
	}
	if code, ok := errs.CodeOf(err); !ok || code != gnmi.ErrCodeRPC {
		t.Errorf("code = %v, want %v", code, gnmi.ErrCodeRPC)
	}
	attrs := errs.Attributes(err)
	if fp, _ := attrs["failed_path"].(string); !strings.Contains(fp, "hostname") {
		t.Errorf("failed_path attr = %v", attrs)
	}
}

func TestDialRefusesImplicitTLSPosture(t *testing.T) {
	_, err := gnmi.Dial(context.Background(), "127.0.0.1:1", gnmi.Options{})
	if err == nil {
		t.Fatal("Dial without a TLS posture succeeded")
	}
	if code, ok := errs.CodeOf(err); !ok || code != gnmi.ErrCodeTransport {
		t.Errorf("code = %v, want %v", code, gnmi.ErrCodeTransport)
	}
	if !strings.Contains(err.Error(), "TLS posture") {
		t.Errorf("error %q does not explain the required posture", err)
	}
}

func TestPathProtoRoundTrip(t *testing.T) {
	p := yang.Path{Segments: []yang.Segment{
		{Module: "openconfig-network-instance", Name: "network-instances"},
		{Name: "network-instance", Keys: []yang.KeyValue{{Name: "name", Value: "VRF red"}}},
		{Name: "protocol", Keys: []yang.KeyValue{
			{Name: "identifier", Value: "STATIC"},
			{Name: "name", Value: "static [v4]"},
		}},
	}}
	proto := gnmi.ToProtoPath(p)
	if len(proto.GetElem()) != 3 || proto.GetElem()[1].GetKey()["name"] != "VRF red" {
		t.Fatalf("proto path = %v", proto)
	}
	back := gnmi.FromProtoPath(proto)
	// Modules do not travel through gNMI paths; compare name/keys.
	if back.String() != "/network-instances/network-instance[name=VRF red]/protocol[identifier=STATIC][name=static [v4\\]]" {
		t.Errorf("round-trip = %q", back.String())
	}
}

func TestGetOnClosedSession(t *testing.T) {
	f := &fakeServer{encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF}}
	s := dialFake(t, f)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	_, err := s.Get(context.Background(), ifacePath())
	if !errors.Is(err, gnmi.ErrSessionClosed) {
		t.Errorf("Get on closed session = %v, want ErrSessionClosed", err)
	}
}

func TestUnaryCallerCancellation(t *testing.T) {
	s := dialFake(t, &fakeServer{encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.Get(ctx, ifacePath())
	if err != context.Canceled {
		t.Errorf("Get error = %v, want unwrapped context.Canceled", err)
	}
	if err := s.Set(ctx, gnmi.SetRequest{}); err != context.Canceled {
		t.Errorf("Set error = %v, want unwrapped context.Canceled", err)
	}
}

func TestRPCTimeoutCapsLaterCallerDeadline(t *testing.T) {
	remaining := make(chan time.Duration, 1)
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF},
		get: func(ctx context.Context, _ *gpb.GetRequest) (*gpb.GetResponse, error) {
			deadline, _ := ctx.Deadline()
			remaining <- time.Until(deadline)
			return &gpb.GetResponse{}, nil
		},
	}
	s := dialFakeWithOptions(t, f, gnmi.Options{RPCTimeout: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := s.Get(ctx, ifacePath()); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := <-remaining; got <= 0 || got > 2*time.Second {
		t.Errorf("server deadline remaining = %v, want positive and at most 2s for 1s RPC timeout", got)
	}
}

// Covers conformance matrix row: gn-leaf-list-typed-value
func TestLeafListTypedValueDecodes(t *testing.T) {
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_PROTO},
		getResp: &gpb.GetResponse{Notification: []*gpb.Notification{{
			Timestamp: 42,
			Update: []*gpb.Update{{
				Path: &gpb.Path{Elem: []*gpb.PathElem{
					{Name: "interfaces"},
					{Name: "interface", Key: map[string]string{"name": "Ethernet5"}},
					{Name: "ethernet"},
					{Name: "state"},
					{Name: "supported-speeds"},
				}},
				Val: &gpb.TypedValue{Value: &gpb.TypedValue_LeaflistVal{
					LeaflistVal: &gpb.ScalarArray{Element: []*gpb.TypedValue{
						{Value: &gpb.TypedValue_StringVal{StringVal: "SPEED_10GB"}},
						{Value: &gpb.TypedValue_StringVal{StringVal: "SPEED_25GB"}},
					}},
				}},
			}},
		}}},
	}
	s := dialFake(t, f)

	updates, err := s.Get(context.Background(), ifacePath())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(updates))
	}
	u := updates[0]
	if u.Value != nil {
		t.Errorf("Value = %+v, want nil for a leaf-list", u.Value)
	}
	if len(u.Values) != 2 {
		t.Fatalf("Values = %d, want 2", len(u.Values))
	}
	if u.Values[0].String != "SPEED_10GB" || u.Values[1].String != "SPEED_25GB" {
		t.Errorf("Values = %+v, want SPEED_10GB then SPEED_25GB", u.Values)
	}
}

// Covers conformance matrix row: gn-leaf-list-typed-value
func TestEmptyLeafListDecodesToEmptySlice(t *testing.T) {
	f := &fakeServer{
		encodings: []gpb.Encoding{gpb.Encoding_PROTO},
		getResp: &gpb.GetResponse{Notification: []*gpb.Notification{{
			Timestamp: 42,
			Update: []*gpb.Update{{
				Path: &gpb.Path{Elem: []*gpb.PathElem{
					{Name: "interfaces"},
					{Name: "interface", Key: map[string]string{"name": "Ethernet5"}},
					{Name: "ethernet"},
					{Name: "state"},
					{Name: "supported-speeds"},
				}},
				Val: &gpb.TypedValue{Value: &gpb.TypedValue_LeaflistVal{
					LeaflistVal: &gpb.ScalarArray{},
				}},
			}},
		}}},
	}
	s := dialFake(t, f)

	updates, err := s.Get(context.Background(), ifacePath())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(updates))
	}
	if got := updates[0].Values; got == nil || len(got) != 0 {
		t.Errorf("Values = %+v, want an empty non-nil slice", got)
	}
}
