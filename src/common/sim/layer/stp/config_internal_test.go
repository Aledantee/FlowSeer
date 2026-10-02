package stp

import "testing"

func TestDefaultPathCost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		speedBPS uint64
		want     uint32
	}{
		{speedBPS: 100_000_000_000, want: 200},
		{speedBPS: 200_000_000_000, want: 200},
		{speedBPS: 10_000_000_000, want: 2_000},
		{speedBPS: 40_000_000_000, want: 2_000},
		{speedBPS: 1_000_000_000, want: 20_000},
		{speedBPS: 2_500_000_000, want: 20_000},
		{speedBPS: 100_000_000, want: 200_000},
		{speedBPS: 10_000_000, want: 2_000_000},
		{speedBPS: 1_000_000, want: 20_000},
		{speedBPS: 0, want: 20_000},
	}

	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			got := defaultPathCost(tc.speedBPS)
			if got != tc.want {
				t.Errorf("defaultPathCost(%d) = %d, want %d", tc.speedBPS, got, tc.want)
			}
		})
	}
}
