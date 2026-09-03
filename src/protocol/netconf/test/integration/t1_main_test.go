//go:build yang_integration_t1

package integration

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/protocol/netconf"
	"go.aledante.io/FlowSeer/src/protocol/yang/test/integration/testenv"
)

// t1Target is the running netopeer2 container's host:port.
var t1Target string

// TestMain owns normal container cleanup; testcontainers' reaper handles a
// process that exits before cleanup, such as after a test panic.
func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		fmt.Fprintln(os.Stderr, "[yang_integration_t1] skipping container tier in short mode")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	target, cleanup, err := testenv.StartNetopeer2(ctx, "../../../yang/test/integration/testenv/testdata/netopeer2")
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "t1 environment:", err)
		os.Exit(1)
	}
	t1Target = target
	code := m.Run()
	if err := cleanup(); err != nil {
		fmt.Fprintln(os.Stderr, "t1 cleanup:", err)
		code = 1
	}
	os.Exit(code)
}

// dialT1 opens a session to the t1 server.
func dialT1(t *testing.T) *netconf.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := netconf.Dial(ctx, t1Target, netconf.Options{
		Username:              testenv.Netopeer2User,
		Password:              testenv.Netopeer2Password,
		InsecureIgnoreHostKey: true,
	})
	if err != nil {
		t.Fatalf("dial %s: %v", t1Target, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	return s
}
