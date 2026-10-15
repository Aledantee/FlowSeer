package otlpapi

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// A code with no mapping answers Internal and the generic message, which is
// safe but silent. Adding a code therefore has to fail here until someone
// decides what the edge on the other end learns from it. ErrCodeStart is a
// failure to build the handler, which no caller of the call can see.
func TestEveryCodeThisPackageDeclaresIsMapped(t *testing.T) {
	for _, code := range errs.Codes() {
		if !strings.HasPrefix(code.String(), "otlpapi/") || code == ErrCodeStart {
			continue
		}
		if _, ok := ClientErrors[code]; !ok {
			t.Errorf("%s has no client mapping", code)
		}
	}
}
