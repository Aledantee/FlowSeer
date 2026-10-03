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
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"

	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
)

func TestOpenFGAListObjectsStopsAtTheCap(t *testing.T) {
	env := startOpenFGAEnv(t, map[string]string{
		"OPENFGA_LIST_OBJECTS_MAX_RESULTS": "10",
	})

	ctx := context.Background()
	// Write 15 edge captures
	for i := 0; i < 15; i++ {
		edge := fmt.Sprintf("edge:cap-%d", i)
		if err := env.WriteTuple(ctx, "user:capuser", "capture", edge); err != nil {
			t.Fatalf("WriteTuple: %v", err)
		}
	}

	// Directly call client.ListObjects to check the response object shape
	resp, err := env.client.ListObjects(env.AuthContext(ctx), &openfgav1.ListObjectsRequest{
		StoreId:              env.storeID,
		AuthorizationModelId: env.modelID,
		Type:                 "edge",
		Relation:             "capture",
		User:                 "user:capuser",
	})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}

	// Response holds 10 objects, and ListObjectsResponse has no other field
	if len(resp.GetObjects()) != 10 {
		t.Fatalf("got %d objects, want 10", len(resp.GetObjects()))
	}

	// Helper call returns 10 objects
	objects, err := env.ListObjects(ctx, "user:capuser", "capture", "edge")
	if err != nil {
		t.Fatalf("env.ListObjects: %v", err)
	}
	if len(objects) != 10 {
		t.Fatalf("env.ListObjects got %d objects, want 10", len(objects))
	}
}

func BenchmarkOpenFGA(b *testing.B) {
	if testing.Short() {
		b.Skip("OpenFGA benchmark skipped under -short")
	}

	ctx := context.Background()
	netName := fmt.Sprintf("openfga-bench-%d", time.Now().UnixNano())
	network, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: testcontainers.NetworkRequest{
			Name: netName,
		},
	})
	if err != nil {
		b.Fatalf("create network: %v", err)
	}
	b.Cleanup(func() { _ = network.Remove(context.Background()) })

	// 1. Start Postgres container
	pgCtr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: postgresImage,
			Networks: []string{
				netName,
			},
			NetworkAliases: map[string][]string{
				netName: {"postgres"},
			},
			Env: map[string]string{
				"POSTGRES_USER":     "postgres",
				"POSTGRES_PASSWORD": "password",
				"POSTGRES_DB":       "openfga",
			},
			ExposedPorts: []string{"5432/tcp"},
			WaitingFor:   wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		b.Fatalf("start postgres container: %v", err)
	}
	b.Cleanup(func() { _ = pgCtr.Terminate(context.Background()) })

	// 2. Run OpenFGA migrate
	migrateCtr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: openFGAImage,
			Cmd:   []string{"migrate"},
			Networks: []string{
				netName,
			},
			Env: map[string]string{
				"OPENFGA_DATASTORE_ENGINE": "postgres",
				"OPENFGA_DATASTORE_URI":    "postgres://postgres:password@postgres:5432/openfga?sslmode=disable",
			},
			WaitingFor: wait.ForExit().WithExitTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		b.Fatalf("run openfga migrate: %v", err)
	}
	_ = migrateCtr.Terminate(context.Background())

	// 3. Start OpenFGA run
	tempDir := b.TempDir()
	certPath, keyPath, certPEM := benchGenerateTLSCert(b, tempDir)

	openfgaCtr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: openFGAImage,
			Cmd:   []string{"run"},
			Networks: []string{
				netName,
			},
			ExposedPorts: []string{"8081/tcp"},
			Env: map[string]string{
				"OPENFGA_DATASTORE_ENGINE":     "postgres",
				"OPENFGA_DATASTORE_URI":        "postgres://postgres:password@postgres:5432/openfga?sslmode=disable",
				"OPENFGA_AUTHN_METHOD":         "preshared",
				"OPENFGA_AUTHN_PRESHARED_KEYS": testPresharedKey,
				"OPENFGA_GRPC_TLS_ENABLED":     "true",
				"OPENFGA_GRPC_TLS_CERT":        "/tmp/tls.crt",
				"OPENFGA_GRPC_TLS_KEY":         "/tmp/tls.key",
				"OPENFGA_HTTP_ENABLED":         "false",
				"OPENFGA_PLAYGROUND_ENABLED":   "false",
			},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: certPath, ContainerFilePath: "/tmp/tls.crt", FileMode: 0o644},
				{HostFilePath: keyPath, ContainerFilePath: "/tmp/tls.key", FileMode: 0o644},
			},
		},
		Started: true,
	})
	if err != nil {
		b.Fatalf("start openfga run: %v", err)
	}
	b.Cleanup(func() { _ = openfgaCtr.Terminate(context.Background()) })

	host, err := openfgaCtr.Host(ctx)
	if err != nil {
		b.Fatalf("get openfga host: %v", err)
	}
	port, err := openfgaCtr.MappedPort(ctx, "8081/tcp")
	if err != nil {
		b.Fatalf("get openfga port: %v", err)
	}
	endpoint := net.JoinHostPort(host, port.Port())

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(certPEM)
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs:    certPool,
		ServerName: "localhost",
	})))
	if err != nil {
		b.Fatalf("dial openfga: %v", err)
	}
	b.Cleanup(func() { _ = conn.Close() })

	healthClient := grpc_health_v1.NewHealthClient(conn)
	benchWaitForServing(b, healthClient)

	// 4. Create Store and write Authorization Model
	client := openfgav1.NewOpenFGAServiceClient(conn)
	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+testPresharedKey)

	storeResp, err := client.CreateStore(authCtx, &openfgav1.CreateStoreRequest{Name: "flowseer-bench"})
	if err != nil {
		b.Fatalf("create store: %v", err)
	}
	storeID := storeResp.GetId()

	model, err := openfga.Model()
	if err != nil {
		b.Fatalf("openfga.Model: %v", err)
	}
	modelResp, err := client.WriteAuthorizationModel(authCtx, &openfgav1.WriteAuthorizationModelRequest{
		StoreId:         storeID,
		SchemaVersion:   model.GetSchemaVersion(),
		TypeDefinitions: model.GetTypeDefinitions(),
		Conditions:      model.GetConditions(),
	})
	if err != nil {
		b.Fatalf("write model: %v", err)
	}
	modelID := modelResp.GetAuthorizationModelId()

	// 5. Connect Checker through openfga.New
	keyFile := benchWriteKeyFile(b, testPresharedKey)
	checker, err := openfga.New(ctx, openfga.Options{
		Endpoint: "https://" + endpoint,
		StoreID:  storeID,
		ModelID:  modelID,
		KeyFile:  keyFile,
		CAFile:   certPath,
	})
	if err != nil {
		b.Fatalf("openfga.New: %v", err)
	}
	b.Cleanup(func() { _ = checker.Close() })

	// 6. Load fixture: 20 tenants, 2000 edges, 4000 sessions, 200 users
	b.Log("Loading benchmark fixture...")
	var allTuples []*openfgav1.TupleKey

	// Global platform setup
	allTuples = append(allTuples,
		&openfgav1.TupleKey{Object: "platform:global", Relation: "enrolled", User: "user:platform_admin"},
		&openfgav1.TupleKey{Object: "tenant:t0", Relation: "partner", User: "tenant:partner0"},
		&openfgav1.TupleKey{Object: "tenant:partner0", Relation: "active_admin", User: "user:partner_admin"},
		&openfgav1.TupleKey{Object: "tenant:t0", Relation: "enrolled", User: "user:m0"},
		&openfgav1.TupleKey{Object: "tenant:t0", Relation: "capturer", User: "user:t0_capturer"},
	)

	for tIdx := 0; tIdx < 20; tIdx++ {
		tenantObj := fmt.Sprintf("tenant:t%d", tIdx)
		allTuples = append(allTuples, &openfgav1.TupleKey{
			Object:   tenantObj,
			Relation: "platform",
			User:     "platform:global",
		})

		// 10 roles
		for rIdx := 0; rIdx < 10; rIdx++ {
			roleObj := fmt.Sprintf("role:t%d-r%d", tIdx, rIdx)
			if rIdx == 0 {
				allTuples = append(allTuples, &openfgav1.TupleKey{
					Object:   tenantObj,
					Relation: "admin",
					User:     roleObj + "#assignee",
				})
			} else if rIdx == 1 {
				allTuples = append(allTuples, &openfgav1.TupleKey{
					Object:   tenantObj,
					Relation: "capturer",
					User:     roleObj + "#assignee",
				})
			}
		}

		// 200 users assigned to roles
		for uIdx := 0; uIdx < 200; uIdx++ {
			rIdx := uIdx % 10
			roleObj := fmt.Sprintf("role:t%d-r%d", tIdx, rIdx)
			userObj := fmt.Sprintf("user:t%d-u%d", tIdx, uIdx)
			allTuples = append(allTuples, &openfgav1.TupleKey{
				Object:   roleObj,
				Relation: "assignee",
				User:     userObj,
			})
		}

		// 2,000 edges
		for eIdx := 0; eIdx < 2000; eIdx++ {
			edgeObj := fmt.Sprintf("edge:t%d-e%d", tIdx, eIdx)
			allTuples = append(allTuples, &openfgav1.TupleKey{
				Object:   edgeObj,
				Relation: "tenant",
				User:     tenantObj,
			})
		}

		// 4,000 sessions
		for sIdx := 0; sIdx < 4000; sIdx++ {
			eIdx := sIdx % 2000
			uIdx := sIdx % 200
			sessionObj := fmt.Sprintf("capture_session:t%d-s%d", tIdx, sIdx)
			edgeObj := fmt.Sprintf("edge:t%d-e%d", tIdx, eIdx)
			userObj := fmt.Sprintf("user:t%d-u%d", tIdx, uIdx)
			allTuples = append(allTuples,
				&openfgav1.TupleKey{
					Object:   sessionObj,
					Relation: "edge",
					User:     edgeObj,
				},
				&openfgav1.TupleKey{
					Object:   sessionObj,
					Relation: "requester",
					User:     userObj,
				},
			)
		}
	}

	// Write tuples concurrently in batches of 100
	const batchSize = 100
	var chunks [][]*openfgav1.TupleKey
	for i := 0; i < len(allTuples); i += batchSize {
		end := i + batchSize
		if end > len(allTuples) {
			end = len(allTuples)
		}
		chunks = append(chunks, allTuples[i:end])
	}

	chunkChan := make(chan []*openfgav1.TupleKey, len(chunks))
	for _, chunk := range chunks {
		chunkChan <- chunk
	}
	close(chunkChan)

	var wg sync.WaitGroup
	const workerCount = 10
	errChan := make(chan error, workerCount)

	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ch := range chunkChan {
				_, wErr := client.Write(authCtx, &openfgav1.WriteRequest{
					StoreId:              storeID,
					AuthorizationModelId: modelID,
					Writes:               &openfgav1.WriteRequestWrites{TupleKeys: ch},
				})
				if wErr != nil {
					select {
					case errChan <- wErr:
					default:
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errChan)

	if wErr := <-errChan; wErr != nil {
		b.Fatalf("write fixture tuples: %v", wErr)
	}
	b.Logf("Fixture loaded successfully (%d tuples)", len(allTuples))

	// 7. Measure and report metrics for the required paths
	measure := func(name string, fn func()) {
		const samples = 100
		durations := make([]time.Duration, samples)
		for i := 0; i < samples; i++ {
			start := time.Now()
			fn()
			durations[i] = time.Since(start)
		}
		slices.Sort(durations)
		p50 := float64(durations[len(durations)*50/100].Microseconds()) / 1000.0
		p99 := float64(durations[len(durations)*99/100].Microseconds()) / 1000.0
		b.ReportMetric(p50, name+"_p50_ms")
		b.ReportMetric(p99, name+"_p99_ms")
	}

	b.ResetTimer()

	// 1. Platform admin, edge#capture
	measure("platform_admin_edge_capture", func() {
		_, _ = checker.Check(ctx, authz.Query{
			Object:   "edge:t0-e0",
			Relation: "capture",
			User:     "user:platform_admin",
			ContextualTuples: []authz.Tuple{
				{Object: "platform:global", Relation: "claimed", User: "user:platform_admin"},
			},
		})
	})

	// 2. Tenant capturer, edge#capture
	measure("tenant_capturer_edge_capture", func() {
		_, _ = checker.Check(ctx, authz.Query{
			Object:   "edge:t0-e0",
			Relation: "capture",
			User:     "user:t0_capturer",
		})
	})

	// 3. Caller with no grant
	measure("no_grant_edge_capture", func() {
		_, _ = checker.Check(ctx, authz.Query{
			Object:   "edge:t0-e0",
			Relation: "capture",
			User:     "user:stranger",
		})
	})

	// 4. Platform admin, tenant#full_payload
	measure("platform_admin_tenant_full_payload", func() {
		_, _ = checker.Check(ctx, authz.Query{
			Object:   "tenant:t0",
			Relation: "full_payload",
			User:     "user:platform_admin",
			ContextualTuples: []authz.Tuple{
				{Object: "platform:global", Relation: "claimed", User: "user:platform_admin"},
			},
		})
	})

	// 5. BatchCheck of 50 sessions
	sessionQueries := make([]authz.Query, 50)
	for i := 0; i < 50; i++ {
		sessionQueries[i] = authz.Query{
			Object:   fmt.Sprintf("capture_session:t0-s%d", i),
			Relation: "download",
			User:     "user:platform_admin",
			ContextualTuples: []authz.Tuple{
				{Object: "platform:global", Relation: "claimed", User: "user:platform_admin"},
			},
		}
	}
	measure("batch_check_50_sessions", func() {
		_, _ = checker.BatchCheck(ctx, sessionQueries)
	})

	// 6. Spike's six membership cases on tenant#member:
	// Case 1: Member, token lists the tenant
	measure("member_token_lists_tenant", func() {
		_, _ = checker.Check(ctx, authz.Query{
			Object:   "tenant:t0",
			Relation: "member",
			User:     "user:m0",
			ContextualTuples: []authz.Tuple{
				{Object: "tenant:t0", Relation: "claimed", User: "user:m0"},
			},
		})
	})

	// Case 2: Member, token lists nothing
	measure("member_token_lists_nothing", func() {
		_, _ = checker.Check(ctx, authz.Query{
			Object:   "tenant:t0",
			Relation: "member",
			User:     "user:m0",
		})
	})

	// Case 3: Enrollment decayed
	measure("enrollment_decayed", func() {
		_, _ = checker.Check(ctx, authz.Query{
			Object:   "tenant:t0",
			Relation: "member",
			User:     "user:m_decayed",
			ContextualTuples: []authz.Tuple{
				{Object: "tenant:t0", Relation: "claimed", User: "user:m_decayed"},
			},
		})
	})

	// Case 4: Partner admin, token lists the partner
	measure("partner_admin_token_lists_partner", func() {
		_, _ = checker.Check(ctx, authz.Query{
			Object:   "tenant:t0",
			Relation: "member",
			User:     "user:partner_admin",
			ContextualTuples: []authz.Tuple{
				{Object: "tenant:partner0", Relation: "claimed", User: "user:partner_admin"},
			},
		})
	})

	// Case 5: Global admin, token lists the platform
	measure("global_admin_token_lists_platform", func() {
		_, _ = checker.Check(ctx, authz.Query{
			Object:   "tenant:t0",
			Relation: "member",
			User:     "user:platform_admin",
			ContextualTuples: []authz.Tuple{
				{Object: "platform:global", Relation: "claimed", User: "user:platform_admin"},
			},
		})
	})

	// Case 6: Stranger
	measure("stranger_member", func() {
		_, _ = checker.Check(ctx, authz.Query{
			Object:   "tenant:t0",
			Relation: "member",
			User:     "user:stranger",
		})
	})
}

func benchWaitForServing(b *testing.B, healthClient grpc_health_v1.HealthClient) {
	b.Helper()
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
		ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
		resp2, err2 := healthClient.Check(ctx2, &grpc_health_v1.HealthCheckRequest{
			Service: "",
		})
		cancel2()
		if err2 == nil && resp2.GetStatus() == grpc_health_v1.HealthCheckResponse_SERVING {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	b.Fatal("timed out waiting for OpenFGA health check to report SERVING")
}

func benchGenerateTLSCert(b *testing.B, dir string) (certPath, keyPath string, certPEM []byte) {
	b.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		b.Fatalf("generate key: %v", err)
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := time.Now().Add(24 * time.Hour)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		b.Fatalf("generate serial: %v", err)
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
		DNSNames:              []string{"localhost", "host.docker.internal"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		b.Fatalf("create certificate: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		b.Fatalf("marshal ec private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	certPath = filepath.Join(dir, "server.crt")
	keyPath = filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		b.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o644); err != nil {
		b.Fatalf("write key: %v", err)
	}
	return certPath, keyPath, certPEM
}

func benchWriteKeyFile(b *testing.B, content string) string {
	b.Helper()
	dir := b.TempDir()
	p := filepath.Join(dir, "psk.key")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		b.Fatalf("write key file: %v", err)
	}
	return p
}
