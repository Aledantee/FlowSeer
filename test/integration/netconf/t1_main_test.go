//go:build yang_integration_t1

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/netconf"
	"go.aledante.io/FlowSeer/test/integration/yang/testenv"
)

// t1Target is the running netopeer2 container's host:port.
var t1Target string

// TestMain owns the container lifecycle so cleanup runs even when a
// test panics (testcontainers' reaper backstops a dying process).
func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	target, cleanup, err := testenv.StartNetopeer2(ctx, "../yang/testenv/testdata/netopeer2")
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "t1 environment:", err)
		os.Exit(1)
	}
	t1Target = target
	code := m.Run()
	cleanup()
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
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}
