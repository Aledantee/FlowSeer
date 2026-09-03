//go:build gnmi_conformance_complete

package gnmi_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/internal/conformance"
)

// TestConformanceCorpusComplete is the merge gate: unlike the
// always-on integrity test, it fails while ANY corpus row is still
// pending. Run via `go test -tags=gnmi_conformance_complete ./src/protocol/gnmi`.
func TestConformanceCorpusComplete(t *testing.T) {
	conformance.RunComplete(t, gnmiCorpus)
}
