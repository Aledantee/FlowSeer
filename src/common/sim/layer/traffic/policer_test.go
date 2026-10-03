package traffic_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
)

func TestRetentionKeyTracksQueueBuffer(t *testing.T) {
	t.Parallel()

	base := traffic.Config{Queues: map[string]traffic.PortQueues{
		"1/1/1": {
			MaxRateBPS:   map[vlan.PCP]uint64{0: 1_000_000},
			BufferOctets: map[vlan.PCP]uint64{0: 2000},
		},
	}}
	changed := base.Clone()
	changed.Queues["1/1/1"] = traffic.PortQueues{
		MaxRateBPS:   map[vlan.PCP]uint64{0: 1_000_000},
		BufferOctets: map[vlan.PCP]uint64{0: 4000},
	}

	if traffic.RetentionKey(base, layer.Env{}) == traffic.RetentionKey(changed, layer.Env{}) {
		t.Error("RetentionKey ignored a change confined to a queue buffer")
	}
	if traffic.RetentionKey(base, layer.Env{}) != traffic.RetentionKey(base.Clone(), layer.Env{}) {
		t.Error("RetentionKey differs between a configuration and its clone")
	}
}

func TestNewBucketRejectsInvalidPolicer(t *testing.T) {
	t.Parallel()

	_, err := traffic.NewBucket(traffic.Policer{RateBPS: 1, BurstOctets: 0})
	if err == nil {
		t.Fatal("NewBucket() error = nil, want error")
	}
	if got := errs.Attributes(err)["field"]; got != "burst_octets" {
		t.Errorf("field = %v, want %q", got, "burst_octets")
	}
}

func TestLayerCloneAdmitsIndependently(t *testing.T) {
	t.Parallel()

	ports := trafficPortTable(t)
	cfg := traffic.Config{
		Policers: map[string]traffic.Policer{
			"1/1/1": {RateBPS: 8, BurstOctets: 100},
		},
	}
	l, err := traffic.New(cfg, layer.Env{Ports: ports})
	if err != nil {
		t.Fatalf("traffic.New: %v", err)
	}

	t0 := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	if !l.Admit(t0, "1/1/1", 60) {
		t.Fatal("original layer refused frame, want admitted")
	}

	cloned := l.Clone()
	if cloned == nil {
		t.Fatal("cloned traffic.Layer is nil")
	}

	if !cloned.Admit(t0, "1/1/1", 40) {
		t.Fatal("clone refused 40 octets, want admitted")
	}
	if cloned.Admit(t0, "1/1/1", 1) {
		t.Fatal("clone admitted frame beyond its tokens, want refused")
	}

	if !l.Admit(t0, "1/1/1", 40) {
		t.Fatal("original refused 40 octets after clone drained its bucket, want admitted")
	}
	if l.Admit(t0, "1/1/1", 1) {
		t.Fatal("original admitted frame beyond its tokens, want refused")
	}
}
