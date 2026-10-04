package openfga_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
	"go.aledante.io/FlowSeer/src/services/device/internal/credential"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

const (
	testStoreID = "01JK1234567890ABCDEFGHJKMN"
	testModelID = "01JK1234567890ABCDEFGHJKMM"
	testPSK     = "test-preshared-key-1234"
)

func wantCode(t *testing.T, err error, want errs.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want %s", want)
	}
	got, ok := errs.CodeOf(err)
	if !ok {
		t.Fatalf("error carries no code: %v", err)
	}
	if got != want {
		t.Errorf("error code = %s, want %s (%v)", got, want, err)
	}
}

type fakeOpenFGAServer struct {
	openfgav1.UnimplementedOpenFGAServiceServer
	mu                   sync.Mutex
	getStoreFunc         func(ctx context.Context, req *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error)
	readModelFunc        func(ctx context.Context, req *openfgav1.ReadAuthorizationModelRequest) (*openfgav1.ReadAuthorizationModelResponse, error)
	checkFunc            func(ctx context.Context, req *openfgav1.CheckRequest) (*openfgav1.CheckResponse, error)
	batchCheckFunc       func(ctx context.Context, req *openfgav1.BatchCheckRequest) (*openfgav1.BatchCheckResponse, error)
	recordedCheckReqs    []*openfgav1.CheckRequest
	recordedBatchReqs    []*openfgav1.BatchCheckRequest
	recordedMetadata     []metadata.MD
	checkCallsCount      atomic.Int64
	batchCheckCallsCount atomic.Int64
	getStoreCallsCount   atomic.Int64
	readModelCallsCount  atomic.Int64
	writeCallsCount      atomic.Int64
	readCallsCount       atomic.Int64
	recordedWriteReqs    []*openfgav1.WriteRequest
	recordedReadReqs     []*openfgav1.ReadRequest
	writeFunc            func(ctx context.Context, req *openfgav1.WriteRequest) (*openfgav1.WriteResponse, error)
	readFunc             func(ctx context.Context, req *openfgav1.ReadRequest) (*openfgav1.ReadResponse, error)
}

func (s *fakeOpenFGAServer) GetStore(ctx context.Context, req *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
	s.getStoreCallsCount.Add(1)
	s.mu.Lock()
	fn := s.getStoreFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return &openfgav1.GetStoreResponse{
		Id:   req.GetStoreId(),
		Name: "test-store",
	}, nil
}

func (s *fakeOpenFGAServer) ReadAuthorizationModel(ctx context.Context, req *openfgav1.ReadAuthorizationModelRequest) (*openfgav1.ReadAuthorizationModelResponse, error) {
	s.readModelCallsCount.Add(1)
	s.mu.Lock()
	fn := s.readModelFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	emb, err := openfga.Model()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	m := proto.Clone(emb).(*openfgav1.AuthorizationModel)
	m.Id = req.GetId()
	return &openfgav1.ReadAuthorizationModelResponse{
		AuthorizationModel: m,
	}, nil
}

func (s *fakeOpenFGAServer) Write(ctx context.Context, req *openfgav1.WriteRequest) (*openfgav1.WriteResponse, error) {
	s.writeCallsCount.Add(1)
	s.mu.Lock()
	s.recordedWriteReqs = append(s.recordedWriteReqs, req)
	fn := s.writeFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return &openfgav1.WriteResponse{}, nil
}

func (s *fakeOpenFGAServer) Read(ctx context.Context, req *openfgav1.ReadRequest) (*openfgav1.ReadResponse, error) {
	s.readCallsCount.Add(1)
	s.mu.Lock()
	s.recordedReadReqs = append(s.recordedReadReqs, req)
	fn := s.readFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return &openfgav1.ReadResponse{}, nil
}

func (s *fakeOpenFGAServer) Check(ctx context.Context, req *openfgav1.CheckRequest) (*openfgav1.CheckResponse, error) {
	s.checkCallsCount.Add(1)
	s.mu.Lock()
	s.recordedCheckReqs = append(s.recordedCheckReqs, req)
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		s.recordedMetadata = append(s.recordedMetadata, md)
	}
	fn := s.checkFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return &openfgav1.CheckResponse{
		Allowed: true,
	}, nil
}

func (s *fakeOpenFGAServer) BatchCheck(ctx context.Context, req *openfgav1.BatchCheckRequest) (*openfgav1.BatchCheckResponse, error) {
	s.batchCheckCallsCount.Add(1)
	s.mu.Lock()
	s.recordedBatchReqs = append(s.recordedBatchReqs, req)
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		s.recordedMetadata = append(s.recordedMetadata, md)
	}
	fn := s.batchCheckFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}

	result := make(map[string]*openfgav1.BatchCheckSingleResult)
	for _, c := range req.GetChecks() {
		result[c.GetCorrelationId()] = &openfgav1.BatchCheckSingleResult{
			CheckResult: &openfgav1.BatchCheckSingleResult_Allowed{
				Allowed: true,
			},
		}
	}
	return &openfgav1.BatchCheckResponse{
		Result: result,
	}, nil
}

type testServerHarness struct {
	server   *grpc.Server
	listener net.Listener
	endpoint string
	caFile   string
	keyFile  string
	fake     *fakeOpenFGAServer
}

func newTestServerHarness(t *testing.T) *testServerHarness {
	t.Helper()
	dir := t.TempDir()

	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"localhost"},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privKey.PublicKey, privKey)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatalf("WriteFile ca.pem: %v", err)
	}

	keyFile := filepath.Join(dir, "psk.key")
	if err := os.WriteFile(keyFile, []byte(testPSK+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile psk.key: %v", err)
	}

	tlsCert, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: mustMarshalECPrivateKey(privKey),
	}))
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewServerTLSFromCert(&tlsCert)))
	fake := &fakeOpenFGAServer{}
	openfgav1.RegisterOpenFGAServiceServer(grpcServer, fake)

	go func() {
		_ = grpcServer.Serve(listener)
	}()

	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	return &testServerHarness{
		server:   grpcServer,
		listener: listener,
		endpoint: "https://" + listener.Addr().String(),
		caFile:   caFile,
		keyFile:  keyFile,
		fake:     fake,
	}
}

func mustMarshalECPrivateKey(key *ecdsa.PrivateKey) []byte {
	b, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		panic(err)
	}
	return b
}

func TestNewHappyPath(t *testing.T) {
	harness := newTestServerHarness(t)

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()
}

func TestNewRefusesMalformedStoreAndModelID(t *testing.T) {
	harness := newTestServerHarness(t)

	for _, tc := range []struct {
		name    string
		storeID string
		modelID string
	}{
		{"too short store", "01JK", testModelID},
		{"too long store", "01JK1234567890ABCDEFGHJKMNEXTRA", testModelID},
		{"forbidden char store", "01JK1234567890ABCDEFGHJKM_", testModelID},
		{"too short model", testStoreID, "01JK"},
		{"too long model", testStoreID, "01JK1234567890ABCDEFGHJKMNEXTRA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := openfga.New(context.Background(), openfga.Options{
				Endpoint: harness.endpoint,
				StoreID:  tc.storeID,
				ModelID:  tc.modelID,
				KeyFile:  harness.keyFile,
				CAFile:   harness.caFile,
			})
			wantCode(t, err, openfga.ErrCodeConfig)
		})
	}
}

func TestNewRefusesInvalidEndpoint(t *testing.T) {
	harness := newTestServerHarness(t)

	for _, ep := range []string{
		"http://" + harness.listener.Addr().String(),
		harness.endpoint + "/path",
		"https://localhost",
		"not-a-url",
	} {
		t.Run(ep, func(t *testing.T) {
			_, err := openfga.New(context.Background(), openfga.Options{
				Endpoint: ep,
				StoreID:  testStoreID,
				ModelID:  testModelID,
				KeyFile:  harness.keyFile,
				CAFile:   harness.caFile,
			})
			wantCode(t, err, openfga.ErrCodeConfig)
		})
	}
}

func TestNewRefusesInsecureKeyFiles(t *testing.T) {
	harness := newTestServerHarness(t)
	dir := t.TempDir()

	_, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  filepath.Join(dir, "absent"),
		CAFile:   harness.caFile,
	})
	wantCode(t, err, credential.ErrCodeNotFound)

	realKey := filepath.Join(dir, "real")
	_ = os.WriteFile(realKey, []byte("key"), 0o600)
	symKey := filepath.Join(dir, "symlink")
	_ = os.Symlink(realKey, symKey)
	_, err = openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  symKey,
		CAFile:   harness.caFile,
	})
	wantCode(t, err, credential.ErrCodeSymlinkRefused)

	insecureKey := filepath.Join(dir, "insecure")
	_ = os.WriteFile(insecureKey, []byte("key"), 0o640)
	_, err = openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  insecureKey,
		CAFile:   harness.caFile,
	})
	wantCode(t, err, credential.ErrCodeInsecureMode)
}

func TestNewRefusesStoreMismatch(t *testing.T) {
	harness := newTestServerHarness(t)

	// Server returns 5002
	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = func(_ context.Context, _ *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
		return nil, status.Error(5002, "store not found")
	}
	harness.fake.mu.Unlock()

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()
	wantCode(t, checker.Verify(context.Background()), openfga.ErrCodeStoreMismatch)

	// Server returns different store ID
	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = func(_ context.Context, _ *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
		return &openfgav1.GetStoreResponse{Id: "01JKDIFFERENT1234567890AB"}, nil
	}
	harness.fake.mu.Unlock()

	checker2, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker2.Close() }()
	wantCode(t, checker2.Verify(context.Background()), openfga.ErrCodeStoreMismatch)
}

func TestNewRefusesModelMismatch(t *testing.T) {
	harness := newTestServerHarness(t)

	// The engine reports the model absent.
	harness.fake.mu.Lock()
	harness.fake.readModelFunc = func(_ context.Context, _ *openfgav1.ReadAuthorizationModelRequest) (*openfgav1.ReadAuthorizationModelResponse, error) {
		return nil, status.Error(2001, "model not found")
	}
	harness.fake.mu.Unlock()

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()
	wantCode(t, checker.Verify(context.Background()), openfga.ErrCodeModelMismatch)

	// The engine holds a model one relation short of the embedded one.
	harness.fake.mu.Lock()
	harness.fake.readModelFunc = func(_ context.Context, req *openfgav1.ReadAuthorizationModelRequest) (*openfgav1.ReadAuthorizationModelResponse, error) {
		emb, _ := openfga.Model()
		m := proto.Clone(emb).(*openfgav1.AuthorizationModel)
		m.Id = req.GetId()
		// Remove admin relation from platform
		for _, td := range m.GetTypeDefinitions() {
			if td.GetType() == "platform" {
				delete(td.Relations, "admin")
			}
		}
		return &openfgav1.ReadAuthorizationModelResponse{AuthorizationModel: m}, nil
	}
	harness.fake.mu.Unlock()

	checker2, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker2.Close() }()
	wantCode(t, checker2.Verify(context.Background()), openfga.ErrCodeModelMismatch)
}

func TestNewRefusesWrongKey(t *testing.T) {
	harness := newTestServerHarness(t)

	// Server returns 1500 (wrong key)
	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = func(_ context.Context, _ *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
		return nil, status.Error(1500, "unauthenticated")
	}
	harness.fake.mu.Unlock()

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()
	wantCode(t, checker.Verify(context.Background()), openfga.ErrCodeRefused)
}

func TestNewUntrustedCertificate(t *testing.T) {
	harness := newTestServerHarness(t)

	// Connect without CAFile -> TLS handshake failure yields Unavailable / engine-unreachable
	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   "",
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()
	wantCode(t, checker.Verify(context.Background()), openfga.ErrCodeUnreachable)
}

func TestNewHangingServerTimesOut(t *testing.T) {
	harness := newTestServerHarness(t)

	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = func(ctx context.Context, _ *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	harness.fake.mu.Unlock()

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
		Timeout:  30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()
	wantCode(t, checker.Verify(context.Background()), openfga.ErrCodeUnreachable)
}

func TestNewCallerCanceledContext(t *testing.T) {
	harness := newTestServerHarness(t)

	cancelCalled := make(chan struct{})
	var cancel context.CancelFunc
	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = func(ctx context.Context, _ *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
		if cancel != nil {
			cancel()
			close(cancelCalled)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &openfgav1.GetStoreResponse{Id: testStoreID, Name: "test-store"}, nil
	}
	harness.fake.mu.Unlock()

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()

	var ctx context.Context
	ctx, cancel = context.WithCancel(context.Background())

	err = checker.Verify(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	<-cancelCalled

	// Canceled caller must not cache a failure for subsequent callers.
	cancel = nil
	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("follow-up Verify: %v, want nil", err)
	}
}

func TestVerifyFailedCheckRateLimited(t *testing.T) {
	harness := newTestServerHarness(t)

	var curTime time.Time
	curTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var timeMu sync.Mutex
	clock := func() time.Time {
		timeMu.Lock()
		defer timeMu.Unlock()
		return curTime
	}

	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = func(_ context.Context, _ *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
		timeMu.Lock()
		curTime = curTime.Add(5 * time.Second)
		timeMu.Unlock()
		return nil, status.Error(codes.Unavailable, "engine unavailable")
	}
	harness.fake.mu.Unlock()

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
		Clock:    clock,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()

	err1 := checker.Verify(context.Background())
	wantCode(t, err1, openfga.ErrCodeUnreachable)

	if harness.fake.getStoreCallsCount.Load() != 1 {
		t.Fatalf("getStore calls = %d, want 1", harness.fake.getStoreCallsCount.Load())
	}

	// Injected clock advanced 5s inside GetStore. The next call must be served from cache.
	err2 := checker.Verify(context.Background())
	wantCode(t, err2, openfga.ErrCodeUnreachable)

	if harness.fake.getStoreCallsCount.Load() != 1 {
		t.Fatalf("got %d GetStore calls, want 1 (second call must be served from cache)", harness.fake.getStoreCallsCount.Load())
	}
}

func TestVerifyWaiterContextCanceledWhileInFlight(t *testing.T) {
	harness := newTestServerHarness(t)

	started := make(chan struct{})
	unblock := make(chan struct{})

	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = func(ctx context.Context, _ *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
		close(started)
		select {
		case <-unblock:
			return &openfgav1.GetStoreResponse{Id: testStoreID, Name: "test-store"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	harness.fake.mu.Unlock()

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()

	leaderDone := make(chan error, 1)
	go func() {
		leaderDone <- checker.Verify(context.Background())
	}()

	<-started

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		waiterDone <- checker.Verify(waiterCtx)
	}()

	time.Sleep(20 * time.Millisecond)
	cancelWaiter()

	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter returned %v, want context.Canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("waiter did not return while check is in flight")
	}

	select {
	case <-leaderDone:
		t.Fatal("leader finished prematurely")
	default:
	}

	close(unblock)
	if err := <-leaderDone; err != nil {
		t.Fatalf("leader failed: %v", err)
	}
}

func TestCheckHappyPathAndContextualTuples(t *testing.T) {
	harness := newTestServerHarness(t)

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()

	allowed, err := checker.Check(context.Background(), authz.Query{
		Object:   "edge:e1",
		Relation: "capture",
		User:     "user:u1",
		ContextualTuples: []authz.Tuple{
			{Object: "edge:e1", Relation: "site", User: "site:s1"},
			{Object: "site:s1", Relation: "tenant", User: "tenant:t1"},
		},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !allowed {
		t.Fatal("expected allowed true")
	}

	harness.fake.mu.Lock()
	defer harness.fake.mu.Unlock()

	if len(harness.fake.recordedCheckReqs) != 1 {
		t.Fatalf("got %d check requests, want 1", len(harness.fake.recordedCheckReqs))
	}
	req := harness.fake.recordedCheckReqs[0]
	if req.GetStoreId() != testStoreID || req.GetAuthorizationModelId() != testModelID {
		t.Errorf("wrong store or model: %v", req)
	}
	if req.GetTupleKey().GetObject() != "edge:e1" || req.GetTupleKey().GetRelation() != "capture" || req.GetTupleKey().GetUser() != "user:u1" {
		t.Errorf("wrong tuple key: %v", req.GetTupleKey())
	}
	if len(req.GetContextualTuples().GetTupleKeys()) != 2 {
		t.Fatalf("got %d contextual tuples, want 2", len(req.GetContextualTuples().GetTupleKeys()))
	}

	if len(harness.fake.recordedMetadata) == 0 {
		t.Fatal("no metadata recorded")
	}
	lastMD := harness.fake.recordedMetadata[len(harness.fake.recordedMetadata)-1]
	authHeaders := lastMD.Get("authorization")
	if len(authHeaders) == 0 || authHeaders[0] != "Bearer "+testPSK {
		t.Errorf("authorization metadata = %v, want Bearer %s", authHeaders, testPSK)
	}
}

const (
	checkMethod      = "openfga.v1.OpenFGAService/Check"
	batchCheckMethod = "openfga.v1.OpenFGAService/BatchCheck"
)

var validQuery = authz.Query{Object: "edge:e1", Relation: "capture", User: "user:u1"}

func withTuple(t authz.Tuple) authz.Query {
	q := validQuery
	q.ContextualTuples = []authz.Tuple{t}
	return q
}

func newChecker(t *testing.T, harness *testServerHarness, mutate func(*openfga.Options)) *openfga.Checker {
	t.Helper()
	opts := openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	}
	if mutate != nil {
		mutate(&opts)
	}
	checker, err := openfga.New(context.Background(), opts)
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	t.Cleanup(func() { _ = checker.Close() })
	return checker
}

// Each query is refused by exactly one rule of OpenFGA's validation, so the
// case fails only when that rule is dropped from the Checker.
var refusedQueries = []struct {
	name  string
	query authz.Query
}{
	{"object with a second colon", authz.Query{Object: "edge:a:b", Relation: "capture", User: "user:u1"}},
	{"object without a type", authz.Query{Object: ":e1", Relation: "capture", User: "user:u1"}},
	{"object without a colon", authz.Query{Object: "edgee1", Relation: "capture", User: "user:u1"}},
	{"object with an empty id", authz.Query{Object: "edge:", Relation: "capture", User: "user:u1"}},
	{"object with the wildcard id", authz.Query{Object: "edge:*", Relation: "capture", User: "user:u1"}},
	{"object of 257 bytes", authz.Query{Object: "edge:" + strings.Repeat("a", 252), Relation: "capture", User: "user:u1"}},
	{"object with a hash", authz.Query{Object: "edge:e#1", Relation: "capture", User: "user:u1"}},
	{"object with a space", authz.Query{Object: "edge:e 1", Relation: "capture", User: "user:u1"}},
	{"object with a tab", authz.Query{Object: "edge:e\t1", Relation: "capture", User: "user:u1"}},
	{"object with a line feed", authz.Query{Object: "edge:e\n1", Relation: "capture", User: "user:u1"}},
	{"object with a form feed", authz.Query{Object: "edge:e\f1", Relation: "capture", User: "user:u1"}},
	{"object with a NUL", authz.Query{Object: "edge:e\x001", Relation: "capture", User: "user:u1"}},
	{"relation with a colon", authz.Query{Object: "edge:e1", Relation: "cap:ture", User: "user:u1"}},
	{"relation with an at sign", authz.Query{Object: "edge:e1", Relation: "cap@ture", User: "user:u1"}},
	{"relation with a hash", authz.Query{Object: "edge:e1", Relation: "cap#ture", User: "user:u1"}},
	{"empty relation", authz.Query{Object: "edge:e1", Relation: "", User: "user:u1"}},
	{"relation of 51 bytes", authz.Query{Object: "edge:e1", Relation: strings.Repeat("r", 51), User: "user:u1"}},
	{"user with the wildcard id", authz.Query{Object: "edge:e1", Relation: "capture", User: "user:*"}},
	{"user that is a userset", authz.Query{Object: "edge:e1", Relation: "capture", User: "user:a#b"}},
	{"user with a second colon", authz.Query{Object: "edge:e1", Relation: "capture", User: "user:a:b"}},
	{"user with a space", authz.Query{Object: "edge:e1", Relation: "capture", User: "user:a b"}},
	{"user of 513 bytes", authz.Query{Object: "edge:e1", Relation: "capture", User: "user:" + strings.Repeat("a", 508)}},
	{"contextual tuple object with a second colon", withTuple(authz.Tuple{Object: "edge:a:b", Relation: "site", User: "site:s1"})},
	{"contextual tuple object of 257 bytes", withTuple(authz.Tuple{Object: "edge:" + strings.Repeat("a", 252), Relation: "site", User: "site:s1"})},
	{"contextual tuple relation with an at sign", withTuple(authz.Tuple{Object: "edge:e1", Relation: "si@te", User: "site:s1"})},
	{"contextual tuple user with the wildcard id", withTuple(authz.Tuple{Object: "edge:e1", Relation: "site", User: "site:*"})},
	{"contextual tuple user that is a userset", withTuple(authz.Tuple{Object: "edge:e1", Relation: "site", User: "site:a#b"})},
	{"contextual tuple user with a second colon", withTuple(authz.Tuple{Object: "edge:e1", Relation: "site", User: "site:a:b"})},
	{"contextual tuple user of 513 bytes", withTuple(authz.Tuple{Object: "edge:e1", Relation: "site", User: "site:" + strings.Repeat("a", 508)})},
}

func TestRefusedIdentifiersMakeNoCall(t *testing.T) {
	harness := newTestServerHarness(t)
	checker := newChecker(t, harness, nil)

	for _, tc := range refusedQueries {
		t.Run(tc.name, func(t *testing.T) {
			checksBefore := harness.fake.checkCallsCount.Load()
			batchesBefore := harness.fake.batchCheckCallsCount.Load()

			allowed, err := checker.Check(context.Background(), tc.query)
			if err != nil {
				t.Fatalf("Check error: %v", err)
			}
			if allowed {
				t.Error("Check allowed a query OpenFGA refuses")
			}

			results, err := checker.BatchCheck(context.Background(), []authz.Query{tc.query})
			if err != nil {
				t.Fatalf("BatchCheck error: %v", err)
			}
			if len(results) != 1 || results[0] {
				t.Errorf("BatchCheck results = %v, want [false]", results)
			}

			if got := harness.fake.checkCallsCount.Load() - checksBefore; got != 0 {
				t.Errorf("server received %d Check calls, want 0", got)
			}
			if got := harness.fake.batchCheckCallsCount.Load() - batchesBefore; got != 0 {
				t.Errorf("server received %d BatchCheck calls, want 0", got)
			}
		})
	}
}

// OpenFGA's IsValidObject refuses control characters, '#' and a space, and
// reads nothing else in an id as special: '@' is refused only in a relation,
// and Unicode whitespace other than the space passes. A query holding either
// is the engine's to answer, since a stored relationship can name it.
func TestIdentifiersOpenFGAAcceptsReachTheEngine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query authz.Query
	}{
		{"object with an at sign", authz.Query{Object: "device:a@b", Relation: "capture", User: "user:u1"}},
		{"user with an at sign", authz.Query{Object: "edge:e1", Relation: "capture", User: "user:u@x"}},
		{"object with a no-break space", authz.Query{Object: "edge:e\u00a01", Relation: "capture", User: "user:u1"}},
		{"user with an em space", authz.Query{Object: "edge:e1", Relation: "capture", User: "user:u\u20031"}},
		{"contextual tuple with at signs", withTuple(authz.Tuple{Object: "edge:a@b", Relation: "site", User: "site:s@1"})},
		{"object of 256 bytes", authz.Query{Object: "edge:" + strings.Repeat("a", 251), Relation: "capture", User: "user:u1"}},
		{"user of 512 bytes", authz.Query{Object: "edge:e1", Relation: "capture", User: "user:" + strings.Repeat("a", 507)}},
		{"relation of 50 bytes", authz.Query{Object: "edge:e1", Relation: strings.Repeat("r", 50), User: "user:u1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness := newTestServerHarness(t)
			checker := newChecker(t, harness, nil)

			allowed, err := checker.Check(context.Background(), tc.query)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if !allowed {
				t.Error("Check = false, want the engine's answer true")
			}
			results, err := checker.BatchCheck(context.Background(), []authz.Query{tc.query})
			if err != nil {
				t.Fatalf("BatchCheck: %v", err)
			}
			if len(results) != 1 || !results[0] {
				t.Errorf("BatchCheck = %v, want [true]", results)
			}

			harness.fake.mu.Lock()
			defer harness.fake.mu.Unlock()
			if len(harness.fake.recordedCheckReqs) != 1 {
				t.Fatalf("server received %d Check calls, want 1", len(harness.fake.recordedCheckReqs))
			}
			sent := harness.fake.recordedCheckReqs[0].GetTupleKey()
			if sent.GetObject() != tc.query.Object || sent.GetRelation() != tc.query.Relation || sent.GetUser() != tc.query.User {
				t.Errorf("sent tuple key %v, want %v", sent, tc.query)
			}
			if len(harness.fake.recordedBatchReqs) != 1 {
				t.Fatalf("server received %d BatchCheck calls, want 1", len(harness.fake.recordedBatchReqs))
			}
		})
	}
}

func TestBatchCheckSplitsAndOrders(t *testing.T) {
	harness := newTestServerHarness(t)
	checker := newChecker(t, harness, nil)

	// The engine answers true for objects whose number is a multiple of 3, by
	// the object it was asked about rather than by position, so two valid
	// answers returned in each other's places are caught.
	harness.fake.mu.Lock()
	harness.fake.batchCheckFunc = func(_ context.Context, req *openfgav1.BatchCheckRequest) (*openfgav1.BatchCheckResponse, error) {
		result := make(map[string]*openfgav1.BatchCheckSingleResult)
		for _, c := range req.GetChecks() {
			n, err := strconv.Atoi(strings.TrimPrefix(c.GetTupleKey().GetObject(), "edge:e"))
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			result[c.GetCorrelationId()] = &openfgav1.BatchCheckSingleResult{
				CheckResult: &openfgav1.BatchCheckSingleResult_Allowed{Allowed: n%3 == 0},
			}
		}
		return &openfgav1.BatchCheckResponse{Result: result}, nil
	}
	harness.fake.mu.Unlock()

	// Query 7 names edge:a:b, which OpenFGA refuses.
	queries := make([]authz.Query, 120)
	for i := range queries {
		queries[i] = authz.Query{
			Object:   fmt.Sprintf("edge:e%d", i),
			Relation: "capture",
			User:     "user:u1",
			ContextualTuples: []authz.Tuple{
				{Object: fmt.Sprintf("edge:e%d", i), Relation: "site", User: fmt.Sprintf("site:s%d", i)},
			},
		}
	}
	queries[7].Object = "edge:a:b"

	results, err := checker.BatchCheck(context.Background(), queries)
	if err != nil {
		t.Fatalf("BatchCheck: %v", err)
	}
	if len(results) != 120 {
		t.Fatalf("got %d results, want 120", len(results))
	}
	for i, got := range results {
		want := i%3 == 0 && i != 7
		if got != want {
			t.Errorf("results[%d] = %v, want %v", i, got, want)
		}
	}

	harness.fake.mu.Lock()
	defer harness.fake.mu.Unlock()

	// A chunk holds at most 50 checks, so 119 valid queries make calls of 50, 50 and 19.
	if len(harness.fake.recordedBatchReqs) != 3 {
		t.Fatalf("got %d batch calls, want 3", len(harness.fake.recordedBatchReqs))
	}
	for i, want := range []int{50, 50, 19} {
		if got := len(harness.fake.recordedBatchReqs[i].GetChecks()); got != want {
			t.Errorf("batch %d checks = %d, want %d", i, got, want)
		}
	}

	// Every call names the store and the model, and each check carries its
	// own tuple key and contextual tuples under the id it will be answered by.
	for i, req := range harness.fake.recordedBatchReqs {
		if req.GetStoreId() != testStoreID {
			t.Errorf("batch %d store = %q, want %q", i, req.GetStoreId(), testStoreID)
		}
		if req.GetAuthorizationModelId() != testModelID {
			t.Errorf("batch %d model = %q, want %q", i, req.GetAuthorizationModelId(), testModelID)
		}
		for _, check := range req.GetChecks() {
			n, err := strconv.Atoi(check.GetCorrelationId())
			if err != nil || n < 0 || n >= len(queries) {
				t.Fatalf("batch %d correlation id %q is not a query index", i, check.GetCorrelationId())
			}
			q := queries[n]
			key := check.GetTupleKey()
			if key.GetObject() != q.Object || key.GetRelation() != q.Relation || key.GetUser() != q.User {
				t.Errorf("check %d tuple key = %v, want %v", n, key, q)
			}
			sent := check.GetContextualTuples().GetTupleKeys()
			if len(sent) != len(q.ContextualTuples) {
				t.Errorf("check %d sent %d contextual tuples, want %d", n, len(sent), len(q.ContextualTuples))
				continue
			}
			for j, want := range q.ContextualTuples {
				if sent[j].GetObject() != want.Object || sent[j].GetRelation() != want.Relation || sent[j].GetUser() != want.User {
					t.Errorf("check %d contextual tuple %d = %v, want %v", n, j, sent[j], want)
				}
			}
		}
	}
}

func TestBatchCheckDuplicateQuery(t *testing.T) {
	harness := newTestServerHarness(t)

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()

	q := authz.Query{Object: "edge:e1", Relation: "capture", User: "user:u1"}
	results, err := checker.BatchCheck(context.Background(), []authz.Query{q, q})
	if err != nil {
		t.Fatalf("BatchCheck: %v", err)
	}
	if len(results) != 2 || !results[0] || !results[1] {
		t.Fatalf("results = %v, want [true, true]", results)
	}
}

func TestCheckErrorStatusMapping(t *testing.T) {
	harness := newTestServerHarness(t)

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()

	for _, tc := range []struct {
		name          string
		grpcErr       error
		wantCode      errs.Code
		wantRetryable bool
	}{
		{"status 1010", status.Error(1010, "missing token"), openfga.ErrCodeRefused, false},
		{"status 1500", status.Error(1500, "wrong key"), openfga.ErrCodeRefused, false},
		{"unauthenticated", status.Error(codes.Unauthenticated, "unauthenticated"), openfga.ErrCodeRefused, false},
		{"permission denied", status.Error(codes.PermissionDenied, "denied"), openfga.ErrCodeRefused, false},
		{"unavailable", status.Error(codes.Unavailable, "server unavailable"), openfga.ErrCodeUnreachable, true},
		{"deadline exceeded", status.Error(codes.DeadlineExceeded, "deadline exceeded"), openfga.ErrCodeUnreachable, true},
		{"internal protocol error", status.Error(codes.Internal, "internal server error"), openfga.ErrCodeProtocol, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness.fake.mu.Lock()
			harness.fake.checkFunc = func(_ context.Context, _ *openfgav1.CheckRequest) (*openfgav1.CheckResponse, error) {
				return nil, tc.grpcErr
			}
			harness.fake.mu.Unlock()

			_, err := checker.Check(context.Background(), validQuery)
			wantCode(t, err, tc.wantCode)
			if got := errs.Retryable(err); got != tc.wantRetryable {
				t.Errorf("Retryable = %v, want %v", got, tc.wantRetryable)
			}
		})
	}

	// Caller context canceled returns context.Canceled bare
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = checker.Check(canceledCtx, validQuery)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestBatchCheckErrorMapping(t *testing.T) {
	harness := newTestServerHarness(t)

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()

	validQueries := []authz.Query{validQuery}

	// The response lacks the correlation id that was sent.
	harness.fake.mu.Lock()
	harness.fake.batchCheckFunc = func(_ context.Context, _ *openfgav1.BatchCheckRequest) (*openfgav1.BatchCheckResponse, error) {
		return &openfgav1.BatchCheckResponse{
			Result: map[string]*openfgav1.BatchCheckSingleResult{
				"wrong-cid": {CheckResult: &openfgav1.BatchCheckSingleResult_Allowed{Allowed: true}},
			},
		}, nil
	}
	harness.fake.mu.Unlock()

	_, err = checker.BatchCheck(context.Background(), validQueries)
	wantCode(t, err, openfga.ErrCodeProtocol)

	// One result carries an error.
	harness.fake.mu.Lock()
	harness.fake.batchCheckFunc = func(_ context.Context, _ *openfgav1.BatchCheckRequest) (*openfgav1.BatchCheckResponse, error) {
		return &openfgav1.BatchCheckResponse{
			Result: map[string]*openfgav1.BatchCheckSingleResult{
				"0": {CheckResult: &openfgav1.BatchCheckSingleResult_Error{
					Error: &openfgav1.CheckError{Message: "internal evaluation error"},
				}},
			},
		}, nil
	}
	harness.fake.mu.Unlock()

	_, err = checker.BatchCheck(context.Background(), validQueries)
	wantCode(t, err, openfga.ErrCodeProtocol)

	// The engine refuses the key.
	harness.fake.mu.Lock()
	harness.fake.batchCheckFunc = func(_ context.Context, _ *openfgav1.BatchCheckRequest) (*openfgav1.BatchCheckResponse, error) {
		return nil, status.Error(1500, "wrong key")
	}
	harness.fake.mu.Unlock()

	_, err = checker.BatchCheck(context.Background(), validQueries)
	wantCode(t, err, openfga.ErrCodeRefused)

	// The engine is unavailable.
	harness.fake.mu.Lock()
	harness.fake.batchCheckFunc = func(_ context.Context, _ *openfgav1.BatchCheckRequest) (*openfgav1.BatchCheckResponse, error) {
		return nil, status.Error(codes.Unavailable, "unavailable")
	}
	harness.fake.mu.Unlock()

	_, err = checker.BatchCheck(context.Background(), validQueries)
	wantCode(t, err, openfga.ErrCodeUnreachable)
}

func TestNewRefusesAnotherModelID(t *testing.T) {
	harness := newTestServerHarness(t)

	harness.fake.mu.Lock()
	harness.fake.readModelFunc = func(_ context.Context, _ *openfgav1.ReadAuthorizationModelRequest) (*openfgav1.ReadAuthorizationModelResponse, error) {
		emb, err := openfga.Model()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		m := proto.Clone(emb).(*openfgav1.AuthorizationModel)
		m.Id = "01JK1234567890ABCDEFGHJKMX"
		return &openfgav1.ReadAuthorizationModelResponse{AuthorizationModel: m}, nil
	}
	harness.fake.mu.Unlock()

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if err != nil {
		t.Fatalf("openfga.New: %v", err)
	}
	defer func() { _ = checker.Close() }()
	wantCode(t, checker.Verify(context.Background()), openfga.ErrCodeModelMismatch)
}

// engineCalls are the two operations a Checker sends, run with one valid query.
var engineCalls = []struct {
	name   string
	method string
	run    func(context.Context, *openfga.Checker) error
}{
	{"Check", checkMethod, func(ctx context.Context, c *openfga.Checker) error {
		_, err := c.Check(ctx, validQuery)
		return err
	}},
	{"BatchCheck", batchCheckMethod, func(ctx context.Context, c *openfga.Checker) error {
		_, err := c.BatchCheck(ctx, []authz.Query{validQuery})
		return err
	}},
}

// onEngineCall makes Check and BatchCheck run fn before answering. A non-nil
// error from fn is the call's failure and a nil one lets every check through.
func (s *fakeOpenFGAServer) onEngineCall(fn func(ctx context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkFunc = func(ctx context.Context, _ *openfgav1.CheckRequest) (*openfgav1.CheckResponse, error) {
		if err := fn(ctx); err != nil {
			return nil, err
		}
		return &openfgav1.CheckResponse{Allowed: true}, nil
	}
	s.batchCheckFunc = func(ctx context.Context, req *openfgav1.BatchCheckRequest) (*openfgav1.BatchCheckResponse, error) {
		if err := fn(ctx); err != nil {
			return nil, err
		}
		result := make(map[string]*openfgav1.BatchCheckSingleResult)
		for _, c := range req.GetChecks() {
			result[c.GetCorrelationId()] = &openfgav1.BatchCheckSingleResult{
				CheckResult: &openfgav1.BatchCheckSingleResult_Allowed{Allowed: true},
			}
		}
		return &openfgav1.BatchCheckResponse{Result: result}, nil
	}
}

// The call context's deadline is what the engine receives, so it shows the
// timeout a Checker applies: 5 seconds unless Options.Timeout says otherwise.
func TestEngineCallsCarryTheTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{"default", 0, 5 * time.Second},
		{"configured", 2 * time.Second, 2 * time.Second},
	} {
		for _, call := range engineCalls {
			t.Run(tc.name+"/"+call.name, func(t *testing.T) {
				harness := newTestServerHarness(t)
				checker := newChecker(t, harness, func(o *openfga.Options) { o.Timeout = tc.timeout })

				remaining := make(chan time.Duration, 1)
				harness.fake.onEngineCall(func(ctx context.Context) error {
					deadline, ok := ctx.Deadline()
					if !ok {
						return status.Error(codes.Internal, "call has no deadline")
					}
					remaining <- time.Until(deadline)
					return nil
				})

				if err := call.run(context.Background(), checker); err != nil {
					t.Fatalf("%s: %v", call.name, err)
				}
				if got := <-remaining; got > tc.want || got <= tc.want-time.Second {
					t.Errorf("engine deadline is %v away, want within a second of %v", got, tc.want)
				}
			})
		}
	}
}

// observedChecker is a Checker wired to in-memory span and metric collection
// and to trace-context propagation.
type observedChecker struct {
	*openfga.Checker
	harness *testServerHarness
	spans   *tracetest.SpanRecorder
	reader  *sdkmetric.ManualReader
}

func newObservedChecker(t *testing.T, timeout time.Duration) *observedChecker {
	t.Helper()
	harness := newTestServerHarness(t)
	spans := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	view, err := telemetry.NewView(telemetry.ViewConfig{MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))})
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	checker := newChecker(t, harness, func(o *openfga.Options) {
		o.Timeout = timeout
		o.View = view
		o.TracerProvider = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
		o.Propagator = propagation.TraceContext{}
	})
	return &observedChecker{Checker: checker, harness: harness, spans: spans, reader: reader}
}

// endedSpan is the one ended span named method; the Checker's startup calls
// make spans of their own under other names.
func (o *observedChecker) endedSpan(t *testing.T, method string) sdktrace.ReadOnlySpan {
	t.Helper()
	var found []sdktrace.ReadOnlySpan
	for _, span := range o.spans.Ended() {
		if span.Name() == method {
			found = append(found, span)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d ended spans named %s, want 1", len(found), method)
	}
	return found[0]
}

// durationPoint is the one rpc.client.call.duration point for method.
func (o *observedChecker) durationPoint(t *testing.T, method string) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := o.reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("metric Collect: %v", err)
	}
	var found []metricdata.HistogramDataPoint[float64]
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if m.Name != "rpc.client.call.duration" || !ok {
				continue
			}
			for _, point := range hist.DataPoints {
				if got, _ := point.Attributes.Value("rpc.method"); got.AsString() == method {
					found = append(found, point)
				}
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d rpc.client.call.duration points for %s, want 1", len(found), method)
	}
	return found[0]
}

func spanAttributes(span sdktrace.ReadOnlySpan) map[string]string {
	attrs := make(map[string]string)
	for _, kv := range span.Attributes() {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	return attrs
}

func TestEngineCallTelemetryOnSuccess(t *testing.T) {
	for _, call := range engineCalls {
		t.Run(call.name, func(t *testing.T) {
			o := newObservedChecker(t, 0)

			if err := call.run(context.Background(), o.Checker); err != nil {
				t.Fatalf("%s: %v", call.name, err)
			}

			span := o.endedSpan(t, call.method)
			if span.SpanKind() != trace.SpanKindClient {
				t.Errorf("span kind = %v, want client", span.SpanKind())
			}
			wantAttrs := map[string]string{"rpc.system.name": "grpc", "rpc.method": call.method}
			if got := spanAttributes(span); !maps.Equal(got, wantAttrs) {
				t.Errorf("span attributes = %v, want %v", got, wantAttrs)
			}
			if span.Status().Code != otelcodes.Unset {
				t.Errorf("span status = %v, want unset", span.Status())
			}
			if len(span.Events()) != 0 {
				t.Errorf("span events = %v, want none", span.Events())
			}

			o.harness.fake.mu.Lock()
			defer o.harness.fake.mu.Unlock()
			if len(o.harness.fake.recordedMetadata) != 1 {
				t.Fatalf("engine recorded %d calls, want 1", len(o.harness.fake.recordedMetadata))
			}
			wantTraceparent := fmt.Sprintf("00-%s-%s-01", span.SpanContext().TraceID(), span.SpanContext().SpanID())
			if got := o.harness.fake.recordedMetadata[0].Get("traceparent"); len(got) != 1 || got[0] != wantTraceparent {
				t.Errorf("traceparent = %v, want [%s]", got, wantTraceparent)
			}

			point := o.durationPoint(t, call.method)
			if got, _ := point.Attributes.Value("rpc.system.name"); got.AsString() != "grpc" {
				t.Errorf("rpc.system.name = %q, want grpc", got.AsString())
			}
			if got := point.Attributes.Len(); got != 2 {
				t.Errorf("a successful call's point carries %d attributes, want rpc.system.name and rpc.method: %v", got, point.Attributes.ToSlice())
			}
			if point.Count != 1 {
				t.Errorf("point count = %d, want 1", point.Count)
			}
		})
	}
}

// A call fails in four ways that read differently to an operator: the engine
// refuses it, the caller cancels it, the caller's deadline passes, or the
// Checker's own timeout does. Only the first and last are the engine's doing,
// so the caller's two must not be classified as engine faults.
func TestEngineCallFailureTelemetryAndErrors(t *testing.T) {
	const engineDetail = "engine-detail-that-must-not-reach-telemetry"

	type outcome struct {
		timeout   time.Duration
		engine    func(ctx context.Context) error
		caller    func() (context.Context, context.CancelFunc)
		wantType  string
		checkErr  func(t *testing.T, err error)
		callBlock bool
	}
	blockUntilDone := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	bare := func(want error) func(*testing.T, error) {
		return func(t *testing.T, err error) {
			t.Helper()
			if !errors.Is(err, want) {
				t.Errorf("error = %v, want %v", err, want)
			}
			if code, ok := errs.CodeOf(err); ok {
				t.Errorf("error carries code %s, want the bare context error", code)
			}
		}
	}

	cases := map[string]outcome{
		"engine refuses": {
			engine:   func(context.Context) error { return status.Error(1500, engineDetail) },
			wantType: "authz/engine-refused",
			checkErr: func(t *testing.T, err error) { t.Helper(); wantCode(t, err, openfga.ErrCodeRefused) },
		},
		"caller cancels mid-call": {
			engine:    blockUntilDone,
			wantType:  "context.canceled",
			checkErr:  bare(context.Canceled),
			callBlock: true,
		},
		"caller deadline passes mid-call": {
			engine: blockUntilDone,
			caller: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 150*time.Millisecond)
			},
			wantType: "context.deadline_exceeded",
			checkErr: bare(context.DeadlineExceeded),
		},
		"checker timeout passes": {
			timeout:  50 * time.Millisecond,
			engine:   blockUntilDone,
			wantType: "authz/engine-unreachable",
			checkErr: func(t *testing.T, err error) {
				t.Helper()
				wantCode(t, err, openfga.ErrCodeUnreachable)
				if !errs.Retryable(err) {
					t.Error("an unreachable engine is not marked retryable")
				}
			},
		},
	}

	for name, tc := range cases {
		for _, call := range engineCalls {
			t.Run(name+"/"+call.name, func(t *testing.T) {
				o := newObservedChecker(t, tc.timeout)

				started := make(chan struct{})
				var once sync.Once
				o.harness.fake.onEngineCall(func(ctx context.Context) error {
					once.Do(func() { close(started) })
					return tc.engine(ctx)
				})

				ctx, cancel := context.WithCancel(context.Background())
				if tc.caller != nil {
					cancel()
					ctx, cancel = tc.caller()
				}
				defer cancel()
				if tc.callBlock {
					go func() {
						<-started
						cancel()
					}()
				}

				err := call.run(ctx, o.Checker)
				if err == nil {
					t.Fatal("call succeeded, want a failure")
				}
				tc.checkErr(t, err)

				span := o.endedSpan(t, call.method)
				if span.Status().Code != otelcodes.Error {
					t.Errorf("span status = %v, want error", span.Status().Code)
				}
				if span.Status().Description == "" || strings.Contains(span.Status().Description, engineDetail) || strings.Contains(span.Status().Description, err.Error()) {
					t.Errorf("span status description = %q, want fixed text free of the error's own", span.Status().Description)
				}
				if len(span.Events()) != 0 {
					t.Errorf("span events = %v, want none", span.Events())
				}
				if got := spanAttributes(span)["error.type"]; got != tc.wantType {
					t.Errorf("span error.type = %q, want %q", got, tc.wantType)
				}

				point := o.durationPoint(t, call.method)
				if got, ok := point.Attributes.Value("error.type"); !ok || got.AsString() != tc.wantType {
					t.Errorf("point error.type = %q (present=%v), want %q", got.AsString(), ok, tc.wantType)
				}
				if point.Count != 1 {
					t.Errorf("point count = %d, want 1", point.Count)
				}
			})
		}
	}
}
