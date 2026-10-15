package rawsocket

import (
	"math"
	"testing"
)

func TestCounterPairDeltaSince(t *testing.T) {
	tests := []struct {
		name         string
		prev, cur    counterPair
		wantReceived uint64
		wantDropped  uint64
	}{
		{
			name:         "three frames then none",
			prev:         counterPair{},
			cur:          counterPair{recv: 3},
			wantReceived: 3,
		},
		{
			name:         "no frames since the previous call",
			prev:         counterPair{recv: 3},
			cur:          counterPair{recv: 3},
			wantReceived: 0,
		},
		{
			name:         "drops are not received",
			prev:         counterPair{recv: 10, drop: 1},
			cur:          counterPair{recv: 15, drop: 3},
			wantReceived: 3,
			wantDropped:  2,
		},
		{
			name:         "recv wraps past 2^32",
			prev:         counterPair{recv: math.MaxUint32 - 1},
			cur:          counterPair{recv: 3},
			wantReceived: 5,
		},
		{
			name:         "drop wraps past 2^32",
			prev:         counterPair{recv: math.MaxUint32 - 1, drop: math.MaxUint32},
			cur:          counterPair{recv: 4, drop: 1},
			wantReceived: 4,
			wantDropped:  2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received, dropped := tt.cur.deltaSince(tt.prev)
			if received != tt.wantReceived || dropped != tt.wantDropped {
				t.Errorf("deltaSince = (received %d, dropped %d), want (%d, %d)",
					received, dropped, tt.wantReceived, tt.wantDropped)
			}
		})
	}
}
