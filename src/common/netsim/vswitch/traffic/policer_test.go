package traffic_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func mustNewBucket(t *testing.T, cfg traffic.Policer) *traffic.Bucket {
	t.Helper()
	bucket, err := traffic.NewBucket(cfg)
	if err != nil {
		t.Fatalf("NewBucket: %v", err)
	}

	return bucket
}

func TestBucketAdmitRefillsByElapsedTime(t *testing.T) {
	t.Parallel()

	bucket := mustNewBucket(t, traffic.Policer{RateBPS: 1_000_000, BurstOctets: 10_000})
	t0 := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	for i := range 9 {
		if !bucket.Admit(t0, 1038) {
			t.Fatalf("Admit refused frame %d, want admitted", i+1)
		}
	}
	if bucket.Admit(t0, 1038) {
		t.Fatal("Admit accepted tenth frame, want refused")
	}
	if !bucket.Admit(t0.Add(10*time.Millisecond), 1038) {
		t.Fatal("Admit refused frame after 10 ms refill, want admitted")
	}
	if got, want := bucket.Tokens(), float64(870); got != want {
		t.Errorf("Tokens = %v, want %v", got, want)
	}
}

func TestBucketCapsRefillAndUnlimitedRate(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	bucket := mustNewBucket(t, traffic.Policer{RateBPS: 1_000_000, BurstOctets: 10_000})
	if !bucket.Admit(t0, 9000) {
		t.Fatal("Admit refused initial frame, want admitted")
	}
	if !bucket.Admit(t0.Add(time.Hour), 0) {
		t.Fatal("Admit refused zero-octet probe, want admitted")
	}
	if got, want := bucket.Tokens(), float64(10_000); got != want {
		t.Errorf("Tokens after long refill = %v, want %v", got, want)
	}

	unlimited := mustNewBucket(t, traffic.Policer{})
	if !unlimited.Admit(t0, 1_000_000) {
		t.Fatal("rate-zero bucket refused a frame")
	}
}

func TestBucketCloneHasIndependentState(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	bucket := mustNewBucket(t, traffic.Policer{RateBPS: 8, BurstOctets: 10})
	if !bucket.Admit(t0, 4) {
		t.Fatal("Admit refused initial frame, want admitted")
	}
	clone := bucket.Clone()
	if !clone.Admit(t0, 2) {
		t.Fatal("clone refused frame, want admitted")
	}
	if got, want := bucket.Tokens(), float64(6); got != want {
		t.Errorf("original Tokens = %v after clone mutation, want %v", got, want)
	}
}

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

	if traffic.RetentionKey(base) == traffic.RetentionKey(changed) {
		t.Error("RetentionKey ignored a change confined to a queue buffer")
	}
	if traffic.RetentionKey(base) != traffic.RetentionKey(base.Clone()) {
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
