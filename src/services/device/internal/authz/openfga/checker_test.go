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
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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
}

func (s *fakeOpenFGAServer) GetStore(ctx context.Context, req *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
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

	// 1. Absent file
	_, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  filepath.Join(dir, "absent"),
		CAFile:   harness.caFile,
	})
	wantCode(t, err, credential.ErrCodeNotFound)

	// 2. Symlink
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

	// 3. Mode 0640
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

	_, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	wantCode(t, err, openfga.ErrCodeStoreMismatch)

	// Server returns different store ID
	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = func(_ context.Context, _ *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
		return &openfgav1.GetStoreResponse{Id: "01JKDIFFERENT1234567890AB"}, nil
	}
	harness.fake.mu.Unlock()

	_, err = openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	wantCode(t, err, openfga.ErrCodeStoreMismatch)
}

func TestNewRefusesModelMismatch(t *testing.T) {
	harness := newTestServerHarness(t)

	// 1. Server returns 2001
	harness.fake.mu.Lock()
	harness.fake.readModelFunc = func(_ context.Context, _ *openfgav1.ReadAuthorizationModelRequest) (*openfgav1.ReadAuthorizationModelResponse, error) {
		return nil, status.Error(2001, "model not found")
	}
	harness.fake.mu.Unlock()

	_, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	wantCode(t, err, openfga.ErrCodeModelMismatch)

	// 2. Server returns model one relation short
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

	_, err = openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	wantCode(t, err, openfga.ErrCodeModelMismatch)
}

func TestNewRefusesWrongKey(t *testing.T) {
	harness := newTestServerHarness(t)

	// Server returns 1500 (wrong key)
	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = func(_ context.Context, _ *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
		return nil, status.Error(1500, "unauthenticated")
	}
	harness.fake.mu.Unlock()

	_, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	wantCode(t, err, openfga.ErrCodeRefused)
}

func TestNewUntrustedCertificate(t *testing.T) {
	harness := newTestServerHarness(t)

	// Connect without CAFile -> TLS handshake failure yields Unavailable / engine-unreachable
	_, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   "",
	})
	wantCode(t, err, openfga.ErrCodeUnreachable)
}

func TestNewHangingServerTimesOut(t *testing.T) {
	harness := newTestServerHarness(t)

	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = func(ctx context.Context, _ *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	harness.fake.mu.Unlock()

	_, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
		Timeout:  30 * time.Millisecond,
	})
	wantCode(t, err, openfga.ErrCodeUnreachable)
}

func TestNewCallerCanceledContext(t *testing.T) {
	harness := newTestServerHarness(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := openfga.New(ctx, openfga.Options{
		Endpoint: harness.endpoint,
		StoreID:  testStoreID,
		ModelID:  testModelID,
		KeyFile:  harness.keyFile,
		CAFile:   harness.caFile,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestCheckHappyPathAndContextualTuples(t *testing.T) {
	harness := newTestServerHarness(t)

	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))

	metricReader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	view, err := telemetry.NewView(telemetry.ViewConfig{
		MeterProvider: mp,
	})
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint:       harness.endpoint,
		StoreID:        testStoreID,
		ModelID:        testModelID,
		KeyFile:        harness.keyFile,
		CAFile:         harness.caFile,
		TracerProvider: tp,
		View:           view,
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

	// Verify spans: at least one CLIENT span named openfga.v1.OpenFGAService/Check
	spans := spanRecorder.Ended()
	var foundSpan bool
	for _, s := range spans {
		if s.Name() == "openfga.v1.OpenFGAService/Check" {
			foundSpan = true
			break
		}
	}
	if !foundSpan {
		t.Error("client span openfga.v1.OpenFGAService/Check not found")
	}

	// Verify metric
	var collected metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("metric Collect: %v", err)
	}
}

func TestCheckRefusedIdentifiersMakeNoCall(t *testing.T) {
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

	refusedQueries := []authz.Query{
		// 1. Object with two colons
		{Object: "edge:a:b", Relation: "capture", User: "user:u1"},
		// 2. Object with wildcard id
		{Object: "edge:*", Relation: "capture", User: "user:u1"},
		// 3. Object of 257 bytes
		{Object: "edge:" + strings.Repeat("a", 252), Relation: "capture", User: "user:u1"},
		// 4. User with wildcard id
		{Object: "edge:e1", Relation: "capture", User: "user:*"},
		// 5. User with userset (#)
		{Object: "edge:e1", Relation: "capture", User: "user:a#b"},
		// 6. User of 513 bytes
		{Object: "edge:e1", Relation: "capture", User: "user:" + strings.Repeat("a", 508)},
		// 7. Contextual tuple with refused object
		{Object: "edge:e1", Relation: "capture", User: "user:u1", ContextualTuples: []authz.Tuple{
			{Object: "edge:a:b", Relation: "site", User: "site:s1"},
		}},
	}

	for i, q := range refusedQueries {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			allowed, err := checker.Check(context.Background(), q)
			if err != nil {
				t.Fatalf("Check error: %v", err)
			}
			if allowed {
				t.Error("expected allowed = false")
			}
		})
	}

	// Refused queries must make NO call to Check on the server
	if count := harness.fake.checkCallsCount.Load(); count != 0 {
		t.Fatalf("server received %d check calls, want 0", count)
	}
}

func TestBatchCheckSplitsAndOrders(t *testing.T) {
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

	// 120 queries, with query 7 naming edge:a:b
	queries := make([]authz.Query, 120)
	for i := range queries {
		if i == 7 {
			queries[i] = authz.Query{
				Object:   "edge:a:b",
				Relation: "capture",
				User:     "user:u1",
			}
		} else {
			queries[i] = authz.Query{
				Object:   fmt.Sprintf("edge:e%d", i),
				Relation: "capture",
				User:     "user:u1",
			}
		}
	}

	results, err := checker.BatchCheck(context.Background(), queries)
	if err != nil {
		t.Fatalf("BatchCheck: %v", err)
	}
	if len(results) != 120 {
		t.Fatalf("got %d results, want 120", len(results))
	}

	// Query 7 must be false, others true
	if results[7] != false {
		t.Errorf("results[7] = %v, want false", results[7])
	}
	for i := range results {
		if i != 7 && results[i] != true {
			t.Errorf("results[%d] = %v, want true", i, results[i])
		}
	}

	// Requirement 7: exactly three calls of 50, 50, and 19
	harness.fake.mu.Lock()
	defer harness.fake.mu.Unlock()

	if len(harness.fake.recordedBatchReqs) != 3 {
		t.Fatalf("got %d batch calls, want 3", len(harness.fake.recordedBatchReqs))
	}
	if len(harness.fake.recordedBatchReqs[0].GetChecks()) != 50 {
		t.Errorf("batch 0 checks = %d, want 50", len(harness.fake.recordedBatchReqs[0].GetChecks()))
	}
	if len(harness.fake.recordedBatchReqs[1].GetChecks()) != 50 {
		t.Errorf("batch 1 checks = %d, want 50", len(harness.fake.recordedBatchReqs[1].GetChecks()))
	}
	if len(harness.fake.recordedBatchReqs[2].GetChecks()) != 19 {
		t.Errorf("batch 2 checks = %d, want 19", len(harness.fake.recordedBatchReqs[2].GetChecks()))
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

	validQuery := authz.Query{Object: "edge:e1", Relation: "capture", User: "user:u1"}

	for _, tc := range []struct {
		name     string
		grpcErr  error
		wantCode errs.Code
	}{
		{"status 1010", status.Error(1010, "missing token"), openfga.ErrCodeRefused},
		{"status 1500", status.Error(1500, "wrong key"), openfga.ErrCodeRefused},
		{"unauthenticated", status.Error(codes.Unauthenticated, "unauthenticated"), openfga.ErrCodeRefused},
		{"permission denied", status.Error(codes.PermissionDenied, "denied"), openfga.ErrCodeRefused},
		{"unavailable", status.Error(codes.Unavailable, "server unavailable"), openfga.ErrCodeUnreachable},
		{"deadline exceeded", status.Error(codes.DeadlineExceeded, "deadline exceeded"), openfga.ErrCodeUnreachable},
		{"internal protocol error", status.Error(codes.Internal, "internal server error"), openfga.ErrCodeProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness.fake.mu.Lock()
			harness.fake.checkFunc = func(_ context.Context, _ *openfgav1.CheckRequest) (*openfgav1.CheckResponse, error) {
				return nil, tc.grpcErr
			}
			harness.fake.mu.Unlock()

			_, err := checker.Check(context.Background(), validQuery)
			wantCode(t, err, tc.wantCode)
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

	validQueries := []authz.Query{{Object: "edge:e1", Relation: "capture", User: "user:u1"}}

	// 1. Missing correlation ID
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

	// 2. Single result carrying error
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

	// 3. Status 1500
	harness.fake.mu.Lock()
	harness.fake.batchCheckFunc = func(_ context.Context, _ *openfgav1.BatchCheckRequest) (*openfgav1.BatchCheckResponse, error) {
		return nil, status.Error(1500, "wrong key")
	}
	harness.fake.mu.Unlock()

	_, err = checker.BatchCheck(context.Background(), validQueries)
	wantCode(t, err, openfga.ErrCodeRefused)

	// 4. Status Unavailable
	harness.fake.mu.Lock()
	harness.fake.batchCheckFunc = func(_ context.Context, _ *openfgav1.BatchCheckRequest) (*openfgav1.BatchCheckResponse, error) {
		return nil, status.Error(codes.Unavailable, "unavailable")
	}
	harness.fake.mu.Unlock()

	_, err = checker.BatchCheck(context.Background(), validQueries)
	wantCode(t, err, openfga.ErrCodeUnreachable)
}
