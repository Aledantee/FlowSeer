package stp

import (
	"testing"
	"time"
)

func TestPropagateTopologyChangeRetainsRunningTimer(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	l := newLayer(Config{Ports: map[string]Port{"p1": {}, "p2": {}}})
	l.links["p1"].up = true
	p := l.cist().ports["p1"]
	p.tcActive = true
	p.tcWhile = now.Add(10 * time.Second)

	l.propagateTopologyChange(l.cist(), "p2", now.Add(time.Second), nil)

	if !p.tcWhile.Equal(now.Add(10 * time.Second)) {
		t.Fatalf("running topology-change timer moved to %v, want %v", p.tcWhile, now.Add(10*time.Second))
	}
}
