//go:build netconf_conformance_complete

package netconf_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/internal/conformance"
)

// TestConformanceCorpusComplete is the merge gate: unlike the
// always-on integrity test, it fails while ANY corpus row is still
// pending. Run via `go test -tags=netconf_conformance_complete ./src/common/netconf`.
func TestConformanceCorpusComplete(t *testing.T) {
	conformance.RunComplete(t, netconfCorpus)
}
