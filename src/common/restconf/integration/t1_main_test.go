//go:build yang_integration_t1

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/restconf"
	"go.aledante.io/FlowSeer/src/common/yang/integration/testenv"
)

// t1BaseURL is the running clixon container's origin.
var t1BaseURL string

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	base, cleanup, err := testenv.StartClixon(ctx, "../../yang/integration/testenv/testdata/clixon")
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "t1 environment:", err)
		os.Exit(1)
	}
	t1BaseURL = base
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// dialT1 opens a session against the t1 server.
func dialT1(t *testing.T) *restconf.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := restconf.Dial(ctx, t1BaseURL, restconf.Options{})
	if err != nil {
		t.Fatalf("dial %s: %v", t1BaseURL, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
