//go:build restconf_conformance_complete

package restconf_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/internal/conformance"
)

// TestConformanceCorpusComplete is the merge gate: unlike the
// always-on integrity test, it fails while ANY corpus row is still
// pending. Run via `go test -tags=restconf_conformance_complete ./src/common/restconf`.
func TestConformanceCorpusComplete(t *testing.T) {
	conformance.RunComplete(t, restconfCorpus)
}
