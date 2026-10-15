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
			received, dropped, next := tt.cur.deltaSince(tt.prev)
			if received != tt.wantReceived || dropped != tt.wantDropped {
				t.Errorf("deltaSince = (received %d, dropped %d), want (%d, %d)",
					received, dropped, tt.wantReceived, tt.wantDropped)
			}
			if next != tt.cur {
				t.Errorf("next baseline = %+v, want %+v", next, tt.cur)
			}
		})
	}
}

func TestCounterPairDeltaSinceCarriesPendingDrops(t *testing.T) {
	tests := []struct {
		name    string
		initial counterPair
		samples []counterPair
		want    [][2]uint64
	}{
		{
			name:    "drop appears before receive",
			initial: counterPair{recv: 100},
			samples: []counterPair{{recv: 100, drop: 1}, {recv: 101, drop: 1}, {recv: 102, drop: 1}},
			want:    [][2]uint64{{0, 0}, {0, 1}, {1, 0}},
		},
		{
			name:    "pending drops span repeated and partial samples",
			initial: counterPair{recv: 100},
			samples: []counterPair{{recv: 101, drop: 3}, {recv: 101, drop: 3}, {recv: 102, drop: 3}, {recv: 105, drop: 3}},
			want:    [][2]uint64{{0, 1}, {0, 0}, {0, 1}, {2, 1}},
		},
		{
			name:    "pending drop wraps before receive wraps",
			initial: counterPair{recv: math.MaxUint32, drop: math.MaxUint32},
			samples: []counterPair{{recv: math.MaxUint32, drop: 0}, {recv: 0, drop: 0}, {recv: 1, drop: 0}},
			want:    [][2]uint64{{0, 0}, {0, 1}, {1, 0}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			last := tt.initial
			for i, cur := range tt.samples {
				received, dropped, next := cur.deltaSince(last)
				last = next
				if received != tt.want[i][0] || dropped != tt.want[i][1] {
					t.Errorf("sample %d = (received %d, dropped %d), want (%d, %d)",
						i, received, dropped, tt.want[i][0], tt.want[i][1])
				}
			}
		})
	}
}
