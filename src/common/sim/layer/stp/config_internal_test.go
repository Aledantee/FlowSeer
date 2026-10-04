package stp

import "testing"

func TestDefaultPathCost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		speedBPS uint64
		want     uint32
	}{
		{name: "100 Gbps", speedBPS: 100_000_000_000, want: 200},
		{name: "400 Gbps", speedBPS: 400_000_000_000, want: 200},
		{name: "40 Gbps", speedBPS: 40_000_000_000, want: 2_000},
		{name: "10 Gbps", speedBPS: 10_000_000_000, want: 2_000},
		{name: "2.5 Gbps", speedBPS: 2_500_000_000, want: 20_000},
		{name: "1 Gbps", speedBPS: 1_000_000_000, want: 20_000},
		{name: "100 Mbps", speedBPS: 100_000_000, want: 200_000},
		{name: "10 Mbps", speedBPS: 10_000_000, want: 2_000_000},
		{name: "unknown speed", speedBPS: 0, want: 20_000},
		{name: "sub-10 Mbps", speedBPS: 1_000_000, want: 20_000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := defaultPathCost(tc.speedBPS)
			if got != tc.want {
				t.Errorf("defaultPathCost(%d) = %d, want %d", tc.speedBPS, got, tc.want)
			}
		})
	}
}
