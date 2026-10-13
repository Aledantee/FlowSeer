//go:build history_integration

package integration_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"go.aledante.io/FlowSeer/src/services/device/internal/history"
)

const adminPassword = "adminpw"

// server is one running ClickHouse container and a Config that names it.
type server struct {
	cfg   history.Config
	admin clickhouse.Conn
	dir   string
}

// startClickHouse runs the pinned image and waits until the admin user can
// query. With cluster set, the server also runs an embedded Keeper and a
// one-node cluster named flowseer, which checks the replicated DDL form but
// no replication behavior.
func startClickHouse(t *testing.T, cluster bool) *server {
	t.Helper()
	if testing.Short() {
		t.Skip("ClickHouse integration test")
	}

	req := testcontainers.ContainerRequest{
		Image:        clickHouseImage,
		ExposedPorts: []string{"9000/tcp", "8123/tcp"},
		Env: map[string]string{
			"CLICKHOUSE_USER":                      "flowseer_admin",
			"CLICKHOUSE_PASSWORD":                  adminPassword,
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
			HostFilePath:      filepath.Join("testdata", "cluster.xml"),
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

	dir := t.TempDir()
	s := &server{dir: dir}
	s.cfg = history.Config{
		Endpoints: []string{endpoint},
		Database:  "flowseer",
		Admin:     s.user(t, "flowseer_admin", adminPassword),
		Writer:    s.user(t, "flowseer_writer", "writer-secret"),
		Reader:    s.user(t, "flowseer_reader", "reader-secret"),
		Analyst:   s.user(t, "flowseer_analyst", "analyst-secret"),
	}
	if cluster {
		s.cfg.Cluster = "flowseer"
	}

	s.admin, err = clickhouse.Open(&clickhouse.Options{
		Addr: []string{endpoint},
		Auth: clickhouse.Auth{Username: "flowseer_admin", Password: adminPassword},
	})
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	t.Cleanup(func() { _ = s.admin.Close() })

	probe := "SELECT 1"
	if cluster {
		probe = "SELECT count() FROM system.zookeeper WHERE path = '/'"
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		err := s.admin.Exec(ctx, probe)
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

// user writes a password file for name and returns the user that reads it.
func (s *server) user(t *testing.T, name, password string) history.User {
	t.Helper()
	path := filepath.Join(s.dir, name+".password")
	if err := os.WriteFile(path, []byte(password+"\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	return history.User{Name: name, PasswordFile: path}
}

// open builds a Store for the server's Config.
func (s *server) open(t *testing.T) *history.Store {
	t.Helper()
	store, err := history.Open(s.cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// connectAsAnalyst opens a pool for the analyst with an explicit password, to
// check what that user may do.
func (s *server) connectAsAnalyst(t *testing.T, password string) clickhouse.Conn {
	t.Helper()
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: s.cfg.Endpoints,
		Auth: clickhouse.Auth{Username: "flowseer_analyst", Password: password},
	})
	if err != nil {
		t.Fatalf("open analyst connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// scalar returns the first column of the first row of query as a string.
func (s *server) scalar(t *testing.T, query string) string {
	t.Helper()
	var out string
	if err := s.admin.QueryRow(context.Background(), query).Scan(&out); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
}
