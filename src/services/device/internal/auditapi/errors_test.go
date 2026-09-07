package auditapi

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// A code with no mapping answers Internal and the generic message, which is
// safe but silent. Adding a code therefore has to fail here until someone
// decides what the edge on the other end learns from it.
// The check covers the codes this package declares; a code it passes through
// from a dependency is the table's own to keep current.
func TestEveryCodeThisPackageDeclaresIsMapped(t *testing.T) {
	for _, code := range errs.Codes() {
		if !strings.HasPrefix(code.String(), "auditapi/") {
			continue
		}
		if _, ok := ClientErrors[code]; !ok {
			t.Errorf("%s has no client mapping", code)
		}
	}
}
