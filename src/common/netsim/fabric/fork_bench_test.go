package fabric_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
)

func BenchmarkFork(b *testing.B) {
	fab := netsimtest.RepresentativeFabric()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = fab.Fork()
	}
}

func BenchmarkForkAndStep100(b *testing.B) {
	fab := netsimtest.RepresentativeFabric()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fork := fab.Fork()
		_ = fork.Run(100)
	}
}
