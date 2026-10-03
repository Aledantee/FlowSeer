//go:build authz_integration

package integration_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/testcontainers/testcontainers-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"

	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
)

const testPresharedKey = "fs-test-openfga-preshared-key-12345"

type openFGAEnv struct {
	container    testcontainers.Container
	endpoint     string
	presharedKey string
	certPath     string
	storeID      string
	modelID      string
	conn         *grpc.ClientConn
	client       openfgav1.OpenFGAServiceClient
}

func startOpenFGAEnv(t *testing.T, extraEnv map[string]string) *openFGAEnv {
	t.Helper()
	if testing.Short() {
		t.Skip("OpenFGA integration test")
	}

	tempDir := t.TempDir()
	certPath, keyPath, certPEM := generateTestTLSCert(t, tempDir)

	env := map[string]string{
		"OPENFGA_AUTHN_METHOD":         "preshared",
		"OPENFGA_AUTHN_PRESHARED_KEYS": testPresharedKey,
		"OPENFGA_GRPC_TLS_ENABLED":     "true",
		"OPENFGA_GRPC_TLS_CERT":        "/tmp/tls.crt",
		"OPENFGA_GRPC_TLS_KEY":         "/tmp/tls.key",
		"OPENFGA_HTTP_ENABLED":         "false",
		"OPENFGA_PLAYGROUND_ENABLED":   "false",
	}
	for k, v := range extraEnv {
		env[k] = v
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        openFGAImage,
			Cmd:          []string{"run"},
			ExposedPorts: []string{"8081/tcp"},
			Env:          env,
			Files: []testcontainers.ContainerFile{
				{
					HostFilePath:      certPath,
					ContainerFilePath: "/tmp/tls.crt",
					FileMode:          0o644,
				},
				{
					HostFilePath:      keyPath,
					ContainerFilePath: "/tmp/tls.key",
					FileMode:          0o644,
				},
			},
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start OpenFGA container: %v", err)
	}

	t.Cleanup(func() {
		termCtx, termCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer termCancel()
		if err := ctr.Terminate(termCtx); err != nil {
			t.Errorf("terminate OpenFGA container: %v", err)
		}
	})

	host, err := ctr.Host(context.Background())
	if err != nil {
		t.Fatalf("get container host: %v", err)
	}
	port, err := ctr.MappedPort(context.Background(), "8081/tcp")
	if err != nil {
		t.Fatalf("get mapped port: %v", err)
	}
	endpoint := net.JoinHostPort(host, port.Port())

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(certPEM) {
		t.Fatal("failed to append certificate to pool")
	}

	tlsConfig := &tls.Config{
		RootCAs:    certPool,
		ServerName: "localhost",
	}

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	if err != nil {
		t.Fatalf("dial OpenFGA gRPC: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	healthClient := grpc_health_v1.NewHealthClient(conn)
	waitForServing(t, healthClient)

	client := openfgav1.NewOpenFGAServiceClient(conn)

	authCtx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+testPresharedKey)

	storeResp, err := client.CreateStore(authCtx, &openfgav1.CreateStoreRequest{Name: "flowseer-test"})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	storeID := storeResp.GetId()

	model, err := openfga.Model()
	if err != nil {
		t.Fatalf("load model: %v", err)
	}
	modelResp, err := client.WriteAuthorizationModel(authCtx, &openfgav1.WriteAuthorizationModelRequest{
		StoreId:         storeID,
		SchemaVersion:   model.GetSchemaVersion(),
		TypeDefinitions: model.GetTypeDefinitions(),
		Conditions:      model.GetConditions(),
	})
	if err != nil {
		t.Fatalf("write authorization model: %v", err)
	}
	modelID := modelResp.GetAuthorizationModelId()

	return &openFGAEnv{
		container:    ctr,
		endpoint:     endpoint,
		presharedKey: testPresharedKey,
		certPath:     certPath,
		storeID:      storeID,
		modelID:      modelID,
		conn:         conn,
		client:       client,
	}
}

func (e *openFGAEnv) AuthContext(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+e.presharedKey)
}

func (e *openFGAEnv) Write(ctx context.Context, writes []*openfgav1.TupleKey, deletes []*openfgav1.TupleKeyWithoutCondition) error {
	var w *openfgav1.WriteRequestWrites
	if len(writes) > 0 {
		w = &openfgav1.WriteRequestWrites{TupleKeys: writes}
	}
	var d *openfgav1.WriteRequestDeletes
	if len(deletes) > 0 {
		d = &openfgav1.WriteRequestDeletes{TupleKeys: deletes}
	}
	_, err := e.client.Write(e.AuthContext(ctx), &openfgav1.WriteRequest{
		StoreId:              e.storeID,
		AuthorizationModelId: e.modelID,
		Writes:               w,
		Deletes:              d,
	})
	return err
}

func (e *openFGAEnv) WriteTuple(ctx context.Context, user, relation, object string) error {
	return e.Write(ctx, []*openfgav1.TupleKey{
		{
			User:     user,
			Relation: relation,
			Object:   object,
		},
	}, nil)
}

func (e *openFGAEnv) Check(ctx context.Context, user, relation, object string, contextualTuples ...*openfgav1.TupleKey) (bool, error) {
	var ct *openfgav1.ContextualTupleKeys
	if len(contextualTuples) > 0 {
		ct = &openfgav1.ContextualTupleKeys{TupleKeys: contextualTuples}
	}
	resp, err := e.client.Check(e.AuthContext(ctx), &openfgav1.CheckRequest{
		StoreId:              e.storeID,
		AuthorizationModelId: e.modelID,
		TupleKey: &openfgav1.CheckRequestTupleKey{
			User:     user,
			Relation: relation,
			Object:   object,
		},
		ContextualTuples: ct,
	})
	if err != nil {
		return false, err
	}
	return resp.GetAllowed(), nil
}

func (e *openFGAEnv) ListObjects(ctx context.Context, user, relation, objectType string, contextualTuples ...*openfgav1.TupleKey) ([]string, error) {
	var ct *openfgav1.ContextualTupleKeys
	if len(contextualTuples) > 0 {
		ct = &openfgav1.ContextualTupleKeys{TupleKeys: contextualTuples}
	}
	resp, err := e.client.ListObjects(e.AuthContext(ctx), &openfgav1.ListObjectsRequest{
		StoreId:              e.storeID,
		AuthorizationModelId: e.modelID,
		Type:                 objectType,
		Relation:             relation,
		User:                 user,
		ContextualTuples:     ct,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetObjects(), nil
}

func waitForServing(t *testing.T, healthClient grpc_health_v1.HealthClient) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resp, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{
			Service: "openfga.v1.OpenFGAService",
		})
		cancel()
		if err == nil && resp.GetStatus() == grpc_health_v1.HealthCheckResponse_SERVING {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("timed out waiting for OpenFGA health check to report SERVING")
}

// dockerDaemonHost resolves the host the container port is published on, the
// same value Container.Host returns after start. The certificate must name it
// because a client that verifies against the endpoint host rather than
// localhost reaches the server only if the name matches.
func dockerDaemonHost(t *testing.T) string {
	t.Helper()
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		t.Fatalf("create docker provider: %v", err)
	}
	defer func() { _ = provider.Close() }()
	host, err := provider.DaemonHost(context.Background())
	if err != nil {
		t.Fatalf("resolve docker daemon host: %v", err)
	}
	return host
}

// generateTestTLSCert makes a server certificate for the test environment. It
// names localhost, the loopback addresses, and the Docker daemon host, because
// a client that verifies against the endpoint host rather than localhost
// reaches the server only if the name matches.
func generateTestTLSCert(t *testing.T, dir string) (certPath, keyPath string, certPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := time.Now().Add(24 * time.Hour)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}

	dnsNames := []string{"localhost", "host.docker.internal"}
	ipAddresses := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	if dockerHost := dockerDaemonHost(t); dockerHost != "" {
		if ip := net.ParseIP(dockerHost); ip != nil {
			ipAddresses = append(ipAddresses, ip)
		} else {
			dnsNames = append(dnsNames, dockerHost)
		}
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"FlowSeer Test"},
			CommonName:   "localhost",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal ec private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	certPath = filepath.Join(dir, "server.crt")
	keyPath = filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o644); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath, certPEM
}
