package bench_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

// The lenient decoders are the documented fallback the fused raw fast path
// declines into, and two of their error branches are routine rather than
// exceptional: every completed BulkWalk ends on an EndOfMibView varbind, and
// off-spec agents drive the type-mismatch branch per varbind. Both now
// construct an errs error, which captures an origin stack.
//
// BenchmarkGet and friends only drive clean in-spec responses, so that cost is
// invisible to the perf gate's allocs/op baseline. These benchmarks put the
// error branches on the same gate.
func BenchmarkDecodeFallback(b *testing.B) {
	var (
		endOfMibView snmp.VarBind = snmp.EndOfMibViewVar{}
		mismatched   snmp.VarBind = snmp.Counter64Var{Value: 42}
		inSpec       snmp.VarBind = snmp.Integer32Var{Value: 42}
	)

	cases := []struct {
		name string
		vb   snmp.VarBind
	}{
		// The control arm: same call, no error constructed.
		{name: "branch=in_spec", vb: inSpec},
		// Walk termination -- once per completed BulkWalk.
		{name: "branch=end_of_mib_view", vb: endOfMibView},
		// Off-spec agent -- once per offending varbind.
		{name: "branch=type_mismatch", vb: mismatched},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				//nolint:errcheck // the error is the subject, not a failure
				_, _ = snmp.DecodeInt32(c.vb)
			}
		})
	}
}
