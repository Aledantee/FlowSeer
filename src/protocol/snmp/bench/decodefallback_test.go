package bench_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// BenchmarkDecodeFallback measures successful decoding and errors on exception
// values or incompatible wire types. The local walk fixture exits its subtree
// before an exception value, so the ordinary walk benchmarks do not measure
// these decoder error paths. This benchmark has no committed gate baseline.
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
