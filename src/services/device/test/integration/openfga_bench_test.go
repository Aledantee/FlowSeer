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
	for i := range 15 {
		edge := fmt.Sprintf("edge:cap-%d", i)
		if err := env.WriteTuple(ctx, "user:capuser", "capture", edge); err != nil {
			t.Fatalf("WriteTuple: %v", err)
		}
	}

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

	if len(resp.GetObjects()) != 10 {
		t.Fatalf("got %d objects, want 10", len(resp.GetObjects()))
	}
	fields := resp.ProtoReflect().Descriptor().Fields()
	if fields.Len() != 1 || fields.ByName("objects") == nil {
		t.Fatalf("ListObjectsResponse has %d fields, want objects only", fields.Len())
	}

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
	b.Cleanup(func() {
		if err := network.Remove(context.Background()); err != nil {
			b.Errorf("remove network: %v", err)
		}
	})

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
	b.Cleanup(func() { terminateBenchContainer(b, pgCtr, "postgres") })

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
		b.Fatalf("start openfga migrate container: %v", err)
	}
	b.Cleanup(func() { terminateBenchContainer(b, migrateCtr, "openfga migrate") })
	migrateState, err := migrateCtr.State(ctx)
	if err != nil {
		b.Fatalf("read openfga migrate state: %v", err)
	}
	if migrateState.ExitCode != 0 {
		b.Fatalf("openfga migrate exited with code %d", migrateState.ExitCode)
	}

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
				// Ten writers of 100 tuples can pass the 3s default on a
				// shared host; the measured single Checks stay well under it.
				"OPENFGA_REQUEST_TIMEOUT": "30s",
			},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: certPath, ContainerFilePath: "/tmp/tls.crt", FileMode: 0o644},
				{HostFilePath: keyPath, ContainerFilePath: "/tmp/tls.key", FileMode: 0o644},
			},
		},
		Started: true,
	})
	if err != nil {
		b.Fatalf("start openfga run container: %v", err)
	}
	b.Cleanup(func() { terminateBenchContainer(b, openfgaCtr, "openfga") })

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
	b.Cleanup(func() {
		if err := conn.Close(); err != nil {
			b.Errorf("close dial connection: %v", err)
		}
	})

	healthClient := grpc_health_v1.NewHealthClient(conn)
	benchWaitForServing(b, healthClient)

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
	b.Cleanup(func() {
		if err := checker.Close(); err != nil {
			b.Errorf("close checker: %v", err)
		}
	})

	b.Log("Loading benchmark fixture...")
	var allTuples []*openfgav1.TupleKey

	allTuples = append(allTuples,
		&openfgav1.TupleKey{Object: "platform:global", Relation: "enrolled", User: "user:platform_admin"},
		&openfgav1.TupleKey{Object: "tenant:t0", Relation: "partner", User: "tenant:partner0"},
		// tenant#active_admin is admin and member, so the store holds its two
		// halves and the partner's home claim arrives as a contextual tuple.
		&openfgav1.TupleKey{Object: "tenant:partner0", Relation: "admin", User: "user:partner_admin"},
		&openfgav1.TupleKey{Object: "tenant:partner0", Relation: "enrolled", User: "user:partner_admin"},
		&openfgav1.TupleKey{Object: "tenant:t0", Relation: "enrolled", User: "user:m0"},
	)

	for tIdx := range 20 {
		tenantObj := fmt.Sprintf("tenant:t%d", tIdx)
		allTuples = append(allTuples, &openfgav1.TupleKey{
			Object:   tenantObj,
			Relation: "platform",
			User:     "platform:global",
		})

		for rIdx := range 10 {
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

		for uIdx := range 200 {
			rIdx := uIdx % 10
			roleObj := fmt.Sprintf("role:t%d-r%d", tIdx, rIdx)
			userObj := fmt.Sprintf("user:t%d-u%d", tIdx, uIdx)
			allTuples = append(allTuples, &openfgav1.TupleKey{
				Object:   roleObj,
				Relation: "assignee",
				User:     userObj,
			})
		}

		for eIdx := range 2000 {
			edgeObj := fmt.Sprintf("edge:t%d-e%d", tIdx, eIdx)
			allTuples = append(allTuples, &openfgav1.TupleKey{
				Object:   edgeObj,
				Relation: "tenant",
				User:     tenantObj,
			})
		}

		for sIdx := range 4000 {
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

	for range workerCount {
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

	platformAdminClaim := []authz.Tuple{
		{Object: "platform:global", Relation: "claimed", User: "user:platform_admin"},
	}

	b.Run("platform_admin_edge_capture", func(b *testing.B) {
		benchCheck(b, true, func() (bool, error) {
			return checker.Check(ctx, authz.Query{
				Object:           "edge:t0-e0",
				Relation:         "capture",
				User:             "user:platform_admin",
				ContextualTuples: platformAdminClaim,
			})
		})
	})

	b.Run("tenant_capturer_edge_capture", func(b *testing.B) {
		// user:t0-u1 reaches tenant#capturer only through role:t0-r1#assignee.
		benchCheck(b, true, func() (bool, error) {
			return checker.Check(ctx, authz.Query{
				Object:   "edge:t0-e0",
				Relation: "capture",
				User:     "user:t0-u1",
			})
		})
	})

	b.Run("no_grant_edge_capture", func(b *testing.B) {
		benchCheck(b, false, func() (bool, error) {
			return checker.Check(ctx, authz.Query{
				Object:   "edge:t0-e0",
				Relation: "capture",
				User:     "user:stranger",
			})
		})
	})

	b.Run("platform_admin_tenant_full_payload", func(b *testing.B) {
		benchCheck(b, false, func() (bool, error) {
			return checker.Check(ctx, authz.Query{
				Object:           "tenant:t0",
				Relation:         "full_payload",
				User:             "user:platform_admin",
				ContextualTuples: platformAdminClaim,
			})
		})
	})

	sessionQueries := make([]authz.Query, 50)
	for i := range sessionQueries {
		sessionQueries[i] = authz.Query{
			Object:           fmt.Sprintf("capture_session:t0-s%d", i),
			Relation:         "download",
			User:             "user:platform_admin",
			ContextualTuples: platformAdminClaim,
		}
	}
	b.Run("batch_check_50_sessions", func(b *testing.B) {
		benchBatch(b, 50, func() ([]bool, error) {
			return checker.BatchCheck(ctx, sessionQueries)
		})
	})

	b.Run("member_token_lists_tenant", func(b *testing.B) {
		benchCheck(b, true, func() (bool, error) {
			return checker.Check(ctx, authz.Query{
				Object:   "tenant:t0",
				Relation: "member",
				User:     "user:m0",
				ContextualTuples: []authz.Tuple{
					{Object: "tenant:t0", Relation: "claimed", User: "user:m0"},
				},
			})
		})
	})

	b.Run("member_token_lists_nothing", func(b *testing.B) {
		benchCheck(b, false, func() (bool, error) {
			return checker.Check(ctx, authz.Query{
				Object:   "tenant:t0",
				Relation: "member",
				User:     "user:m0",
			})
		})
	})

	b.Run("enrollment_decayed", func(b *testing.B) {
		benchCheck(b, false, func() (bool, error) {
			return checker.Check(ctx, authz.Query{
				Object:   "tenant:t0",
				Relation: "member",
				User:     "user:m_decayed",
				ContextualTuples: []authz.Tuple{
					{Object: "tenant:t0", Relation: "claimed", User: "user:m_decayed"},
				},
			})
		})
	})

	b.Run("partner_admin_token_lists_partner", func(b *testing.B) {
		benchCheck(b, true, func() (bool, error) {
			return checker.Check(ctx, authz.Query{
				Object:   "tenant:t0",
				Relation: "member",
				User:     "user:partner_admin",
				ContextualTuples: []authz.Tuple{
					{Object: "tenant:partner0", Relation: "claimed", User: "user:partner_admin"},
				},
			})
		})
	})

	b.Run("global_admin_token_lists_platform", func(b *testing.B) {
		benchCheck(b, true, func() (bool, error) {
			return checker.Check(ctx, authz.Query{
				Object:           "tenant:t0",
				Relation:         "member",
				User:             "user:platform_admin",
				ContextualTuples: platformAdminClaim,
			})
		})
	})

	b.Run("stranger_member", func(b *testing.B) {
		benchCheck(b, false, func() (bool, error) {
			return checker.Check(ctx, authz.Query{
				Object:   "tenant:t0",
				Relation: "member",
				User:     "user:stranger",
			})
		})
	})
}

// benchCheck runs run b.N times, failing on any error or an answer other than
// expected, and reports the p50 and p99 latency of the calls.
func benchCheck(b *testing.B, expected bool, run func() (bool, error)) {
	b.Helper()
	durations := make([]time.Duration, b.N)
	for i := range durations {
		start := time.Now()
		got, err := run()
		durations[i] = time.Since(start)
		if err != nil {
			b.Fatalf("check: %v", err)
		}
		if got != expected {
			b.Fatalf("check answered %t, want %t", got, expected)
		}
	}
	b.StopTimer()
	reportBenchLatency(b, durations)
}

// benchBatch runs run b.N times, failing on any error, a result count other
// than want, or a false answer, and reports the p50 and p99 latency.
func benchBatch(b *testing.B, want int, run func() ([]bool, error)) {
	b.Helper()
	durations := make([]time.Duration, b.N)
	for i := range durations {
		start := time.Now()
		got, err := run()
		durations[i] = time.Since(start)
		if err != nil {
			b.Fatalf("batch check: %v", err)
		}
		if len(got) != want {
			b.Fatalf("batch check returned %d results, want %d", len(got), want)
		}
		for j, allowed := range got {
			if !allowed {
				b.Fatalf("batch check result %d answered false, want true", j)
			}
		}
	}
	b.StopTimer()
	reportBenchLatency(b, durations)
}

func reportBenchLatency(b *testing.B, durations []time.Duration) {
	b.Helper()
	slices.Sort(durations)
	b.ReportMetric(latencyMillis(durations, 50), "p50_ms")
	b.ReportMetric(latencyMillis(durations, 99), "p99_ms")
}

func latencyMillis(sorted []time.Duration, percentile int) float64 {
	return float64(sorted[percentileIndex(percentile, len(sorted))].Microseconds()) / 1000.0
}

// percentileIndex returns the nearest-rank index of percentile over n samples,
// ceil(percentile*n/100)-1, clamped to the sample range. The arithmetic index
// put p99 at n-1 and ignored b.N when the sample count was fixed.
func percentileIndex(percentile, n int) int {
	idx := (percentile*n+99)/100 - 1
	if idx < 0 {
		return 0
	}
	if idx >= n {
		return n - 1
	}
	return idx
}

func TestBenchPercentileIndexUsesNearestRank(t *testing.T) {
	cases := []struct {
		percentile int
		n          int
		want       int
	}{
		{50, 1, 0},
		{99, 1, 0},
		{50, 100, 49},
		{99, 100, 98},
		{50, 1000, 499},
		{99, 1000, 989},
	}
	for _, c := range cases {
		if got := percentileIndex(c.percentile, c.n); got != c.want {
			t.Fatalf("percentileIndex(%d, %d) = %d, want %d", c.percentile, c.n, got, c.want)
		}
	}
}

func terminateBenchContainer(b *testing.B, ctr testcontainers.Container, what string) {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := ctr.Terminate(ctx); err != nil {
		b.Errorf("terminate %s container: %v", what, err)
	}
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
