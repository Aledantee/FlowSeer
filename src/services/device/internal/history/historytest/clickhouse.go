// Package historytest starts a pinned ClickHouse server for the history store's
// integration tests: the history package's own, the read call's, and the
// device service's end-to-end test. It is test support, like authztest, and no
// shipping code imports it.
package historytest

import (
	"context"
	_ "embed"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"go.aledante.io/FlowSeer/src/services/device/internal/history"
)

// Image is the server the tests run. The device service suite and the lab
// declare the same digest, and a test in each checks it.
const Image = "clickhouse/clickhouse-server:26.8@sha256:9b61e3c635c04ad5bb521eb4f6e61ce7585b5580814e51c25bb9e8292ce43364"

// AdminPassword is the password of the admin user the container starts with.
const AdminPassword = "adminpw"

// Passwords of the service users the migrator creates from Server.Cfg's files.
const (
	WriterPassword  = "writer-secret"
	ReaderPassword  = "reader-secret"
	AnalystPassword = "analyst-secret"
)

//go:embed cluster.xml
var clusterConfig []byte

// Server is one running ClickHouse container, an admin connection to it, and a
// history Config that names it. The database flowseer exists on a single node
// and is left for the migrator to create on a cluster node.
type Server struct {
	// Cfg names the server and four users whose password files sit in Dir.
	Cfg history.Config
	// Admin is a pool connected as the admin user, for setup and for checking
	// what the service wrote.
	Admin clickhouse.Conn
	// Dir holds the password files.
	Dir string
}

// Start runs the pinned image and waits until the admin user can query. With
// cluster set the server also runs an embedded Keeper and a one-node cluster
// named flowseer, which checks the replicated DDL form and no replication
// behavior. It skips under -short and fails, not skips, when the image cannot
// start. The container is removed when the test ends.
func Start(t *testing.T, cluster bool) *Server {
	t.Helper()
	if testing.Short() {
		t.Skip("ClickHouse integration test")
	}

	req := testcontainers.ContainerRequest{
		Image:        Image,
		ExposedPorts: []string{"9000/tcp", "8123/tcp"},
		Env: map[string]string{
			"CLICKHOUSE_USER":                      "flowseer_admin",
			"CLICKHOUSE_PASSWORD":                  AdminPassword,
			"CLICKHOUSE_DB":                        "flowseer",
			"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1",
		},
		WaitingFor: wait.ForHTTP("/ping").WithPort("8123/tcp").WithStartupTimeout(90 * time.Second),
	}
	if cluster {
		// The image would create flowseer as an Atomic database, and the
		// migrator must create it Replicated.
		delete(req.Env, "CLICKHOUSE_DB")
		req.Files = []testcontainers.ContainerFile{{
			Reader:            strings.NewReader(string(clusterConfig)),
			ContainerFilePath: "/etc/clickhouse-server/config.d/cluster.xml",
			FileMode:          0o644,
		}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	t.Cleanup(func() {
		if ctr == nil {
			return
		}
		termCtx, termCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer termCancel()
		if err := ctr.Terminate(termCtx); err != nil {
			t.Errorf("terminate ClickHouse container: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("start ClickHouse container: %v", err)
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	endpoint := net.JoinHostPort(host, port.Port())

	s := &Server{Dir: t.TempDir()}
	s.Cfg = history.Config{
		Endpoints: []string{endpoint},
		Database:  "flowseer",
		Admin:     s.User(t, "flowseer_admin", AdminPassword),
		Writer:    s.User(t, "flowseer_writer", WriterPassword),
		Reader:    s.User(t, "flowseer_reader", ReaderPassword),
		Analyst:   s.User(t, "flowseer_analyst", AnalystPassword),
	}
	if cluster {
		s.Cfg.Cluster = "flowseer"
	}

	s.Admin, err = clickhouse.Open(&clickhouse.Options{
		Addr: []string{endpoint},
		Auth: clickhouse.Auth{Username: "flowseer_admin", Password: AdminPassword},
	})
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	t.Cleanup(func() { _ = s.Admin.Close() })

	probe := "SELECT 1"
	if cluster {
		probe = "SELECT count() FROM system.zookeeper WHERE path = '/'"
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		err := s.Admin.Exec(ctx, probe)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never became ready: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return s
}

// User writes a password file for name under Dir and returns the user that
// reads it. Writing it again rotates the password.
func (s *Server) User(t *testing.T, name, password string) history.User {
	t.Helper()
	path := filepath.Join(s.Dir, name+".password")
	if err := os.WriteFile(path, []byte(password+"\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	return history.User{Name: name, PasswordFile: path}
}

// Open builds a Store for the server's Config and closes it when the test ends.
func (s *Server) Open(t *testing.T) *history.Store {
	t.Helper()
	store, err := history.Open(s.Cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// Migrated opens a Store and brings the server to the current schema.
func (s *Server) Migrated(t *testing.T) *history.Store {
	t.Helper()
	store := s.Open(t)
	if _, err := history.Migrate(context.Background(), store); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

// ConnectAsAnalyst opens a pool for the analyst with an explicit password, to
// check what that user may do. The pool is closed when the test ends.
func (s *Server) ConnectAsAnalyst(t *testing.T, password string) clickhouse.Conn {
	t.Helper()
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: s.Cfg.Endpoints,
		Auth: clickhouse.Auth{Username: "flowseer_analyst", Password: password},
	})
	if err != nil {
		t.Fatalf("open analyst connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// Scalar returns the first column of the first row of query as a string, which
// the query must produce with toString where the column is not a string.
func (s *Server) Scalar(t *testing.T, query string) string {
	t.Helper()
	var out string
	if err := s.Admin.QueryRow(context.Background(), query).Scan(&out); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
}
