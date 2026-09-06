package snmp

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/secret"
)

// TestCommunity_NoLeakInRendering asserts that the v1/v2c shared secret
// survives being formatted: a SessionConfig reaches a debug log or a
// dial error, and a Trap reaches a handler that prints it.
func TestCommunity_NoLeakInRendering(t *testing.T) {
	const community = "s3cret-community"
	cfg := ApplyOptions(WithCommunity(secret.NewString(community)))
	trap := Trap{
		Community: secret.NewString(community),
		Version:   V2c,
		Received:  time.Now(),
	}
	for _, tc := range []struct {
		name string
		got  string
	}{
		{"config %v", fmt.Sprintf("%v", cfg)},
		{"config %+v", fmt.Sprintf("%+v", cfg)},
		{"config %#v", fmt.Sprintf("%#v", cfg)},
		{"trap %v", fmt.Sprintf("%v", trap)},
		{"trap %+v", fmt.Sprintf("%+v", trap)},
		{"trap %#v", fmt.Sprintf("%#v", trap)},
	} {
		if strings.Contains(tc.got, community) {
			t.Errorf("%s leaked the community: %s", tc.name, tc.got)
		}
		if !strings.Contains(tc.got, secret.Redacted) {
			t.Errorf("%s is not redacted: %s", tc.name, tc.got)
		}
	}
}
