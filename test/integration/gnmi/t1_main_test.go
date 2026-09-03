//go:build yang_integration_t1

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/gnmi"
	"go.aledante.io/FlowSeer/test/integration/yang/testenv"
)

// t1Target is the running reference target's host:port.
var t1Target string

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	target, cleanup, err := testenv.StartGNMITarget(ctx, "../yang/testenv/testdata/gnmitarget")
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

// dialT1 opens a session against the t1 target.
func dialT1(t *testing.T) *gnmi.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := gnmi.Dial(ctx, t1Target, gnmi.Options{Plaintext: true})
	if err != nil {
		t.Fatalf("dial %s: %v", t1Target, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
