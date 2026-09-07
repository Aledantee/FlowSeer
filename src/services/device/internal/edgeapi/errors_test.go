package edgeapi

import (
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// A code with no mapping answers Internal and the generic message, which is
// the safe default and a silent one: an operator's InvalidArgument would
// arrive as an unexplained Internal and nothing would say why. Adding a code
// therefore has to fail here until someone decides what its caller learns.
// The check covers the codes this package declares; a code it passes through
// from a dependency is the table's own to keep current.
func TestEveryCodeThisPackageDeclaresIsMapped(t *testing.T) {
	for _, code := range errs.Codes() {
		if !strings.HasPrefix(code.String(), "edgeapi/") {
			continue
		}
		if _, ok := clientErrors[code]; !ok {
			t.Errorf("%s has no client mapping", code)
		}
	}
}
