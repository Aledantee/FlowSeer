package syslog

import (
	"context"
	"errors"
	"testing"
)

func TestAdmissionHeadroomAndRelease(t *testing.T) {
	l, err := (Limits{MaxPayload: 256, MaxFrames: 1, MetadataBytes: 256, MaxBytes: 16384}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	a, err := newAdmission(l, l.headroom())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.acquire(context.Background(), 256, true, false); err != nil {
		t.Fatal(err)
	}
	if err := a.acquire(context.Background(), 256, true, false); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.acquire(canceled, 256, true, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	a.release(256, true)
	if err := a.acquire(context.Background(), 256, true, false); err != nil {
		t.Fatal(err)
	}
	a.release(256, true)
	a.stop()
	a.release(l.headroom(), false)
	if b, f := a.snapshot(); b != 0 || f != 0 {
		t.Fatal(b, f)
	}
	for _, bad := range []Limits{{MaxPayload: -1}, {FrameTimeout: -1}, {MaxBytes: 1}, {MaxPayload: int(^uint(0) >> 1)}} {
		if _, err := bad.normalized(); err == nil {
			t.Fatal("invalid limit accepted", bad)
		}
	}
}
