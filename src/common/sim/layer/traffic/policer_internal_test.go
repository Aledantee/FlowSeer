package traffic

import (
	"testing"
	"time"
)

func mustNewBucket(t *testing.T, cfg Policer) *Bucket {
	t.Helper()
	bucket, err := NewBucket(cfg)
	if err != nil {
		t.Fatalf("NewBucket: %v", err)
	}

	return bucket
}

func TestBucketAdmitRefillsByElapsedTime(t *testing.T) {
	t.Parallel()

	bucket := mustNewBucket(t, Policer{RateBPS: 1_000_000, BurstOctets: 10_000})
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
	if got, want := bucket.tokens, float64(870); got != want {
		t.Errorf("tokens = %v, want %v", got, want)
	}
}

func TestBucketCapsRefillAndUnlimitedRate(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	bucket := mustNewBucket(t, Policer{RateBPS: 1_000_000, BurstOctets: 10_000})
	if !bucket.Admit(t0, 9000) {
		t.Fatal("Admit refused initial frame, want admitted")
	}
	if !bucket.Admit(t0.Add(time.Hour), 0) {
		t.Fatal("Admit refused zero-octet probe, want admitted")
	}
	if got, want := bucket.tokens, float64(10_000); got != want {
		t.Errorf("tokens after long refill = %v, want %v", got, want)
	}

	unlimited := mustNewBucket(t, Policer{})
	if !unlimited.Admit(t0, 1_000_000) {
		t.Fatal("rate-zero bucket refused a frame")
	}
}

func TestBucketCloneHasIndependentState(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	bucket := mustNewBucket(t, Policer{RateBPS: 8, BurstOctets: 10})
	if !bucket.Admit(t0, 4) {
		t.Fatal("Admit refused initial frame, want admitted")
	}
	clone := bucket.Clone()
	if !clone.Admit(t0, 2) {
		t.Fatal("clone refused frame, want admitted")
	}
	if got, want := bucket.tokens, float64(6); got != want {
		t.Errorf("original tokens = %v after clone mutation, want %v", got, want)
	}
}
